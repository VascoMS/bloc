import csv
import importlib.util
import json
import re
import shutil
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("campaign_artifacts", ROOT / "scripts/lib/campaign_artifacts.py")
artifacts = importlib.util.module_from_spec(SPEC)
assert SPEC.loader
SPEC.loader.exec_module(artifacts)


class CampaignArtifactsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="bloc runner space ")
        self.root = Path(self.temp.name)

    def tearDown(self):
        self.temp.cleanup()

    def write_csv(self, path, rows):
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open("w", encoding="utf-8", newline="") as handle:
            writer = csv.DictWriter(handle, fieldnames=list(rows[0]), lineterminator="\n")
            writer.writeheader(); writer.writerows(rows)

    def measured(self, nodes="4", batch="8", run="r1"):
        return {"phase": "measured", "success": "true", "consistent": "true", "nodes": nodes,
                "batch_size": batch, "run_id": run, "merge_plan_us": "10", "acs_output_decode_us": "1",
                "agreed_set_us": "2", "merge_us": "3", "ciphertext_decode_us": "2", "batch_plan_us": "2"}

    def test_evaluator_acceptance_and_rejection(self):
        path = self.root / "measurements.csv"
        self.write_csv(path, [self.measured()])
        artifacts.evaluator_assert(path, ["4/8=1"], require_success=True)
        rows = artifacts.read_csv(path); rows[0]["consistent"] = "false"; self.write_csv(path, rows)
        artifacts.evaluator_assert(path, ["4/8=1"], require_success=False)
        with self.assertRaises(ValueError):
            artifacts.evaluator_assert(path, ["4/8=1"], require_success=True)

    def test_scenario_merge_prefixes_block_run_ids(self):
        specs = []
        for block in (1, 2):
            path = self.root / f"block {block}"
            for name in ("run_measurements.csv", "node_measurements.csv", "scenario_summary.csv"):
                self.write_csv(path / name, [self.measured(run="same")])
            artifacts.write_json(path / "scenario_summary.json", {"block": block})
            specs.append(f"{block}:{path}")
        artifacts.merge_scenarios(self.root / "merged", specs, True)
        rows = artifacts.read_csv(self.root / "merged/run_measurements.csv")
        self.assertEqual([r["run_id"] for r in rows], ["block-1-same", "block-2-same"])
        self.assertEqual([r["measurement_block"] for r in rows], ["1", "2"])
        self.assertEqual([r["campaign_order_index"] for r in rows], ["1", "2"])
        node_rows = artifacts.read_csv(self.root / "merged/node_measurements.csv")
        self.assertEqual([r["campaign_order_index"] for r in node_rows], ["1", "2"])

    def test_placement_annotation_does_not_invent_failed_run_critical_node(self):
        phase = self.root / "phase"
        inventory = self.root / "inventory.json"
        artifacts.write_json(inventory, {
            "nodes": [{"id": 0, "region": "us-east-1", "zone": "us-east-1a", "instance_type": "t3.small"}]
        })
        self.write_csv(phase / "node_measurements.csv", [{
            "run_id": "failed", "node_id": "0",
        }])
        self.write_csv(phase / "run_measurements.csv", [{
            "run_id": "failed", "success": "false", "consistent": "false", "critical_node_id": "0",
        }])

        artifacts.annotate_placement(phase, inventory)

        row = artifacts.read_csv(phase / "run_measurements.csv")[0]
        self.assertEqual(row["critical_node_region"], "")
        self.assertEqual(row["critical_node_availability_zone"], "")

    def test_json_is_utf8_without_bom(self):
        path = self.root / "artifact.json"
        artifacts.write_json(path, {"label": "BLOC–portable"})
        data = path.read_bytes()
        self.assertFalse(data.startswith(b"\xef\xbb\xbf"))
        self.assertEqual(json.loads(data), {"label": "BLOC–portable"})

    def test_benchmark_and_evaluator_summaries(self):
        bench = self.root / "bench.txt"
        bench.write_text("BenchmarkThing/pipeline-8  1  10 ns/op  20 B/op  3 allocs/op\n", encoding="utf-8")
        self.assertEqual(artifacts.benchmark_summary([bench])[0]["median_ns_per_op"], 10)
        data = self.root / "eval.csv"; self.write_csv(data, [self.measured()])
        self.assertEqual(artifacts.evaluator_summary(data)[0]["median_merge_plan_us"], 10)

    def test_csv_and_json_merges_preserve_schema_and_utf8(self):
        first, second = self.root / "first.csv", self.root / "second.csv"
        self.write_csv(first, [{"name": "baseline", "value": "1"}])
        self.write_csv(second, [{"name": "optimized–phase", "value": "2"}])
        output = self.root / "merged.csv"
        artifacts.merge_csv(output, [first, second])
        self.assertEqual([row["value"] for row in artifacts.read_csv(output)], ["1", "2"])
        self.assertFalse(output.read_bytes().startswith(b"\xef\xbb\xbf"))
        incompatible = self.root / "incompatible.csv"
        self.write_csv(incompatible, [{"different": "column"}])
        with self.assertRaises(ValueError):
            artifacts.merge_csv(output, [first, incompatible])

        one, two = self.root / "one.json", self.root / "two.json"
        artifacts.write_json(one, {"phase": 1}); artifacts.write_json(two, [{"phase": 2}])
        merged_json = self.root / "merged.json"
        artifacts.merge_json(merged_json, [one, two])
        self.assertEqual(json.loads(merged_json.read_text()), [{"phase": 1}, {"phase": 2}])

    def test_command_records_reject_malformed_metadata(self):
        records = self.root / "commands.tsv"
        records.write_text("stage\t/work space\tbash test.sh\tstart\tfinish\t0\tlog.txt\n", encoding="utf-8")
        self.assertEqual(artifacts.commands_json(records)[0]["exit_code"], 0)
        records.write_text("missing\tfields\n", encoding="utf-8")
        with self.assertRaises(ValueError):
            artifacts.commands_json(records)

    def test_comparison_outputs_and_markdown_report(self):
        campaign = self.root / "campaign with space"
        for phase, ns, merge_us in (("baseline", "100", "20"), ("optimized", "80", "15")):
            phase_root = campaign / phase
            self.write_csv(phase_root / "benchmark-summary.csv", [{
                "benchmark": "BenchmarkThing/n4-b32/pipeline", "samples": "3",
                "median_ns_per_op": ns, "median_bytes_per_op": "40", "median_allocs_per_op": "5",
            }])
            self.write_csv(phase_root / "evaluator-summary.csv", [{
                "nodes": "4", "batch_size": "32", "samples": "3", "median_merge_plan_us": merge_us,
            }])
        artifacts.compare_merge_plan(campaign)
        self.assertEqual(artifacts.read_csv(campaign / "comparison.csv")[0]["latency_delta_percent"], "-20.0")
        self.assertIn("Merge/Plan Optimization Report", (campaign / "REPORT.md").read_text(encoding="utf-8"))

    def make_three_region_phase(self):
        phase = self.root / "n4"
        regions = ["us-east-1", "eu-west-1", "eu-central-1"]
        placement = [
            {"id": node, "region": regions[node % 3], "zone": f"{regions[node % 3]}a", "instance_type": "t3.small"}
            for node in range(4)
        ]
        artifacts.write_json(phase / "manifest.json", {
            "schema_version": "bloc-ec2-three-region-phase/v1", "status": "complete",
            "topology": "T2-three-region", "node_count": 4, "repetitions": 1,
            "batch_sizes": [8], "primary_region": regions[0], "secondary_region": regions[1],
            "tertiary_region": regions[2], "operator_instance_type": "t3.small",
            "controller_instance_type": "t3.small", "docker_image_digest": "sha256:" + "a" * 64,
            "placement": placement, "peering_connection_ids": {
                "primary_secondary": "pcx-1", "primary_tertiary": "pcx-2", "secondary_tertiary": "pcx-3",
            },
        })
        run = {
            "run_id": "run-1", "phase": "measured", "success": "true", "consistent": "true",
            "batch_size": "8", "selected_ciphertexts": "8", "critical_node_id": "2",
            "critical_node_region": regions[2], "critical_node_availability_zone": f"{regions[2]}a",
            "total_slot_us": "100", "proposal_preparation_us": "10", "acs_us": "20",
            "merge_plan_us": "50", "threshold_wait_us": "5", "combine_us": "5", "materialization_us": "10",
        }
        self.write_csv(phase / "run_measurements.csv", [run])
        nodes = []
        for node in range(4):
            nodes.append({
                "run_id": "run-1", "phase": "measured", "success": "true", "consistent": "true",
                "node_id": str(node), "critical_node": str(node == 2).lower(), "metrics_finalized": "true",
                "selected_ciphertexts": "8", "merge_plan_us": "50", "acs_output_decode_us": "10",
                "agreed_set_us": "10", "merge_us": "10", "ciphertext_decode_us": "10", "batch_plan_us": "10",
                "region": regions[node % 3], "availability_zone": f"{regions[node % 3]}a", "instance_type": "t3.small",
            })
        self.write_csv(phase / "node_measurements.csv", nodes)
        targets = {"data": {"activeTargets": [{"health": "up"} for _ in range(4)]}}
        artifacts.write_json(phase / "prometheus-targets-before.json", targets)
        artifacts.write_json(phase / "prometheus-targets.json", targets)
        network = []
        for source in range(4):
            for target in range(4):
                network.append({
                    "source_node_id": str(source), "source_region": regions[source % 3],
                    "target_node_id": str(target), "target_region": regions[target % 3],
                    "attempts": "5", "successes": "5",
                })
        self.write_csv(phase / "network-pre.csv", network)
        self.write_csv(phase / "network-post.csv", network)
        resource_rows = []
        for node in range(4):
            for index in range(4):
                resource_rows.append({
                    "timestamp": f"2026-07-24T00:00:00.{index * 250:03d}Z", "sample_index": str(index), "node": str(node),
                    "region": regions[node % 3], "scenario": "n4-b8", "phase": "resource-measured",
                    "cpu_usage_us": str(100 + index), "memory_current_bytes": "10", "memory_peak_bytes": "12",
                    "network_receive_bytes": str(1000 + index), "network_transmit_bytes": str(2000 + index),
                    "restart_count": "0", "oom_killed": "false",
                })
        self.write_csv(phase / "resource_timeseries.csv", resource_rows)
        return phase

    def test_three_region_phase_contract_accepts_complete_evidence(self):
        phase = self.make_three_region_phase()
        artifacts.assert_three_region_phase(phase)

    def test_three_region_phase_contract_retains_timed_out_attempt(self):
        phase = self.make_three_region_phase()
        runs = artifacts.read_csv(phase / "run_measurements.csv")
        runs[0].update({
            "success": "false",
            "consistent": "false",
            "outcome": "timed_out",
            "timed_out": "true",
            "deadline_met": "false",
            "error": "timed out waiting for results",
        })
        self.write_csv(phase / "run_measurements.csv", runs)

        artifacts.assert_three_region_phase(phase)

    def test_three_region_phase_contract_rejects_unclassified_new_failure(self):
        phase = self.make_three_region_phase()
        runs = artifacts.read_csv(phase / "run_measurements.csv")
        runs[0].update({
            "success": "false",
            "consistent": "false",
            "outcome": "",
            "timed_out": "false",
        })
        self.write_csv(phase / "run_measurements.csv", runs)

        with self.assertRaisesRegex(ValueError, "failure outcome"):
            artifacts.assert_three_region_phase(phase)

    def test_three_region_phase_contract_rejects_incomplete_pairwise_health(self):
        phase = self.make_three_region_phase()
        rows = artifacts.read_csv(phase / "network-post.csv")
        rows[0]["successes"] = "4"
        self.write_csv(phase / "network-post.csv", rows)
        with self.assertRaisesRegex(ValueError, "five health attempts"):
            artifacts.assert_three_region_phase(phase)

    def resource_rows(self):
        fields = (
            "timestamp", "sample_index", "node", "region", "scenario", "phase", "cpu_usage_us",
            "memory_current_bytes", "memory_peak_bytes", "network_receive_bytes", "network_transmit_bytes",
            "restart_count", "oom_killed",
        )
        rows = []
        for node, region in (("0", "us-east-1"), ("1", "eu-west-1")):
            for index, cpu, current, peak, receive, transmit in (
                (0, 100, 10, 12, 1000, 2000), (1, 160, 20, 25, 1300, 2600),
                (2, 220, 15, 25, 1600, 3200), (3, 280, 12, 25, 1900, 3800),
                (4, 340, 11, 25, 2200, 4400),
            ):
                rows.append(dict(zip(fields, (
                    f"2026-07-24T00:00:{index // 4:02d}.{(index % 4) * 250:03d}Z", str(index), node, region, "n2-b8", "resource-measured",
                    str(cpu + int(node) * 10), str(current), str(peak), str(receive + int(node) * 100),
                    str(transmit + int(node) * 100), "0", "false",
                ))))
        return rows

    def test_resource_evidence_summarizes_node_and_cluster_counter_deltas(self):
        path = self.root / "resource_timeseries.csv"
        self.write_csv(path, self.resource_rows())
        summary = artifacts.resource_evidence_summary(path, expected_nodes={"0", "1"}, expected_configurations={("n2-b8", "resource-measured")})
        self.assertEqual(len(summary), 3)
        node_zero = next(row for row in summary if row["scope"] == "node" and row["node"] == "0")
        self.assertEqual(node_zero["cpu_usage_delta_us"], 240)
        self.assertEqual(node_zero["memory_peak_bytes"], 25)
        self.assertEqual(node_zero["network_receive_delta_bytes"], 1200)
        cluster = next(row for row in summary if row["scope"] == "cluster")
        self.assertEqual(cluster["cpu_usage_delta_us"], 480)
        self.assertEqual(cluster["memory_current_max_bytes"], 40)
        self.assertEqual(cluster["memory_peak_bytes"], 50)
        self.assertEqual(cluster["network_transmit_delta_bytes"], 4800)

    def test_resource_evidence_rejects_missing_sample_indexes(self):
        path = self.root / "resource_timeseries.csv"
        rows = self.resource_rows(); rows = [row for row in rows if not (row["node"] == "0" and row["sample_index"] == "1")]
        self.write_csv(path, rows)
        with self.assertRaisesRegex(ValueError, "missing samples"):
            artifacts.resource_evidence_summary(path, expected_nodes={"0", "1"}, expected_configurations={("n2-b8", "resource-measured")})

    def test_resource_evidence_rejects_incomplete_node_configuration_coverage(self):
        path = self.root / "resource_timeseries.csv"
        rows = [row for row in self.resource_rows() if row["node"] == "0"]
        self.write_csv(path, rows)
        with self.assertRaisesRegex(ValueError, "incomplete node/configuration coverage"):
            artifacts.resource_evidence_summary(path, expected_nodes={"0", "1"}, expected_configurations={("n2-b8", "resource-measured")})

    def test_resource_evidence_rejects_counter_resets(self):
        path = self.root / "resource_timeseries.csv"
        rows = self.resource_rows(); rows[1]["network_receive_bytes"] = "900"
        self.write_csv(path, rows)
        with self.assertRaisesRegex(ValueError, "counter reset"):
            artifacts.resource_evidence_summary(path, expected_nodes={"0", "1"}, expected_configurations={("n2-b8", "resource-measured")})

    def test_resource_evidence_rejects_restart_or_oom(self):
        path = self.root / "resource_timeseries.csv"
        rows = self.resource_rows(); rows[0]["restart_count"] = "1"; rows[1]["oom_killed"] = "true"
        self.write_csv(path, rows)
        with self.assertRaisesRegex(ValueError, "restart or OOM"):
            artifacts.resource_evidence_summary(path, expected_nodes={"0", "1"}, expected_configurations={("n2-b8", "resource-measured")})

    def test_resource_evidence_rejects_invalid_phase(self):
        path = self.root / "resource_timeseries.csv"
        rows = self.resource_rows(); rows[0]["phase"] = "measured"
        self.write_csv(path, rows)
        with self.assertRaisesRegex(ValueError, "invalid resource phase"):
            artifacts.resource_evidence_summary(path, expected_nodes={"0", "1"}, expected_configurations={("n2-b8", "resource-measured")})

    def test_resource_evidence_rejects_memory_peak_below_current_or_decreasing(self):
        path = self.root / "resource_timeseries.csv"
        rows = self.resource_rows(); rows[1]["memory_peak_bytes"] = "19"
        self.write_csv(path, rows)
        with self.assertRaisesRegex(ValueError, "memory peak"):
            artifacts.resource_evidence_summary(path, expected_nodes={"0", "1"}, expected_configurations={("n2-b8", "resource-measured")})

    def test_resource_evidence_rejects_truncated_or_off_cadence_samples(self):
        path = self.root / "resource_timeseries.csv"
        rows = self.resource_rows()[:3]
        self.write_csv(path, rows)
        with self.assertRaisesRegex(ValueError, "insufficient resource samples"):
            artifacts.resource_evidence_summary(path, expected_nodes={"0"}, expected_configurations={("n2-b8", "resource-measured")})
        rows = self.resource_rows(); rows[2]["timestamp"] = "2026-07-24T00:00:03.000Z"
        self.write_csv(path, rows)
        with self.assertRaisesRegex(ValueError, "off-cadence"):
            artifacts.resource_evidence_summary(path, expected_nodes={"0", "1"}, expected_configurations={("n2-b8", "resource-measured")})

    def test_host_resource_sampler_declares_250ms_cgroup_and_docker_fallback_contract(self):
        sampler = ROOT / "deploy/ec2/sample-container-resources.sh"
        text = sampler.read_text(encoding="utf-8")
        self.assertIn("interval_ms=250", text)
        self.assertIn("/sys/fs/cgroup", text)
        self.assertIn("docker_call stats --no-stream", text)
        self.assertIn("--stop-file", text)
        self.assertIn("RESOURCE_TIMESERIES", text)
        self.assertNotIn("printenv", text.lower())
        self.assertNotIn("/proc/$pid/environ", text.lower())
        self.assertNotIn(".config.env", text.lower())

    def test_active_runners_collect_dedicated_resource_phase_before_teardown(self):
        for name in ("run-a1-pilot.sh", "run-m3-three-region.sh"):
            text = (ROOT / "deploy/ec2" / name).read_text(encoding="utf-8")
            self.assertIn("sample-container-resources.sh", text)
            self.assertIn("resource-measured", text)
            self.assertIn("resource_timeseries.csv", text)
            self.assertIn("resource-summary", text)

    def test_sampler_and_runners_bound_cadence_and_stop_lifecycle(self):
        sampler = (ROOT / "deploy/ec2/sample-container-resources.sh").read_text(encoding="utf-8")
        self.assertIn('sleep "$remaining_seconds"', sampler)
        self.assertIn("next_deadline_ns", sampler)
        self.assertIn('timeout "$docker_timeout_seconds" docker', sampler)
        self.assertIn('sampler_iteration_max_seconds=$((docker_timeout_seconds * 4))', sampler)
        self.assertIn("fallback_peak_bytes", sampler)
        for name in ("run-a1-pilot.sh", "run-m3-three-region.sh"):
            text = (ROOT / "deploy/ec2" / name).read_text(encoding="utf-8")
            self.assertIn(".sampler.pid", text)
            self.assertIn("kill -0", text)

    def test_runners_gate_live_sampler_and_wait_for_minimum_rows_before_stop(self):
        for name in ("run-a1-pilot.sh", "run-m3-three-region.sh"):
            text = (ROOT / "deploy/ec2" / name).read_text(encoding="utf-8")
            self.assertIn("wc -l", text)
            self.assertIn("minimum_resource_rows", text)
            self.assertIn("sampler_iteration_max_seconds=8", text)
            self.assertIn("sampler_stop_timeout_seconds=10", text)
            iteration = int(re.search(r"sampler_iteration_max_seconds=(\d+)", text).group(1))
            stop_window = int(re.search(r"sampler_stop_timeout_seconds=(\d+)", text).group(1))
            self.assertGreater(stop_window, iteration)
            self.assertIn("kill -0 \\$(cat '$pid_file') || exit 1; touch '$stop_file'", text)


class FinalCampaignArtifactTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="bloc final artifacts ")
        self.root = Path(self.temp.name)

    def tearDown(self):
        self.temp.cleanup()

    def make_phase(self, phase="readiness-pilot", attempts=3, blocks_override=None, warmups_override=None,
                   stream_mode="fresh"):
        root = self.root / phase
        root.mkdir(parents=True)
        blocks = blocks_override if blocks_override is not None else (1 if phase == "readiness-pilot" else 10)
        if attempts % blocks != 0:
            raise ValueError("attempts must divide evenly across blocks")
        source = "c" * 40
        bloc = "123456789012.dkr.ecr.us-east-1.amazonaws.com/bloc-node@sha256:" + "a" * 64
        mempool = "123456789012.dkr.ecr.us-east-1.amazonaws.com/mempool-il@sha256:" + "b" * 64
        artifacts.write_json(root / "manifest.json", {
            "schema_version": "bloc-final-campaign-phase-v1", "status": "complete",
            "topology": "same-az", "phase": phase, "node_count": 4,
            "source_sha": source, "bloc_image": bloc, "mempool_image": mempool,
            "bundle_version": "bloc-campaign-bundle-v1", "public_config_id": "public",
            "encrypted_corpus_id": "corpus", "batches": [8, 32, 128], "seed": 20260621,
            "deadline": "12s", "warmups": warmups_override if warmups_override is not None else (1 if phase == "readiness-pilot" else (0 if phase == "resource" else 10)),
            "repetitions": attempts, "blocks": blocks,
            "sampler": "on" if phase == "resource" else "off",
            "stream_mode": stream_mode,
        })
        generated = root / "generated-public"
        generated.mkdir()
        artifacts.write_json(generated / "cluster.json", {
            "network": {"mode": "libp2p", "stream_mode": stream_mode},
        })
        artifacts.write_json(generated / "remote-eval.json", {"stream_mode": stream_mode})
        artifacts.write_json(root / "inventory.json", {"nodes": [
            {"id": i, "region": "us-east-1", "zone": "us-east-1a", "instance_type": "t3.small"}
            for i in range(4)
        ]})
        scenario = root / "scenarios/controller/results"
        scenario.mkdir(parents=True)
        fields = ["run_id", "phase", "measurement_block", "planned_scenario_runs", "slot", "nodes", "batch_size", "stream_mode",
                  "success", "consistent", "outcome", "timed_out", "selected_ciphertexts"]
        artifacts.write_json(scenario / "manifest.json", {"stream_mode": stream_mode})
        with (scenario / "run_measurements.csv").open("w", newline="", encoding="utf-8") as handle:
            writer = csv.DictWriter(handle, fieldnames=fields); writer.writeheader()
            for batch in (8, 32, 128):
                for block in range(1, blocks + 1):
                    for index in range(1, attempts // blocks + 1):
                        writer.writerow({"run_id": f"measured-r{index:03d}-b{batch}", "phase": "measured",
                                         "measurement_block": str(block), "planned_scenario_runs": str(attempts),
                                         "slot": str((block - 1) * (attempts // blocks) + index),
                                         "nodes": "4", "batch_size": str(batch), "stream_mode": stream_mode, "success": "true",
                                         "consistent": "true", "outcome": "completed", "timed_out": "false",
                                         "selected_ciphertexts": str(batch)})
        return root

    def add_acs_trace_artifacts(self, root, trace_schema="bloc-acs-trace/v1"):
        manifest = json.loads((root / "manifest.json").read_text(encoding="utf-8"))
        manifest["acs_trace_schema"] = trace_schema
        artifacts.write_json(root / "manifest.json", manifest)
        for run_path in root.glob("scenarios/**/run_measurements.csv"):
            scenario_root = run_path.parent
            evaluator_manifest = json.loads((scenario_root / "manifest.json").read_text(encoding="utf-8"))
            evaluator_manifest["acs_trace_schema"] = trace_schema
            artifacts.write_json(scenario_root / "manifest.json", evaluator_manifest)
            node_rows = []
            trace_records = []
            for run in artifacts.read_csv(run_path):
                for node_id in range(4):
                    node_rows.append({
                        "run_id": run["run_id"], "phase": run["phase"],
                        "measurement_block": run["measurement_block"], "node_id": str(node_id),
                        "acs_trace_schema": trace_schema, "stream_mode": run["stream_mode"],
                        "acs_inbound_messages": "1", "acs_inbound_bytes": "10",
                        "acs_outbound_messages": "2", "acs_outbound_bytes": "20",
                        "acs_send_count": "2", "acs_send_total_us": "4",
                        "acs_send_max_us": "2", "acs_send_failures": "0",
                        "acs_encode_total_us": "2", "acs_encode_max_us": "1",
                        "acs_queue_wait_total_us": "0", "acs_queue_wait_max_us": "0",
                        "acs_stream_open_total_us": "0", "acs_stream_open_max_us": "0",
                        "acs_write_total_us": "2", "acs_write_max_us": "1",
                        "acs_finalize_total_us": "0", "acs_finalize_max_us": "0",
                        "acs_stream_open_count": "0" if run["stream_mode"] == "persistent" else "2",
                        "acs_stream_reuse_count": "2" if run["stream_mode"] == "persistent" else "0",
                    })
                    point = lambda offset: {"recorded": True, "offset_us": offset}
                    trace_records.append({
                        "key": {
                            "measurement_block": int(run["measurement_block"]),
                            "run_id": run["run_id"], "node_id": node_id,
                            "slot": int(run["slot"]),
                        },
                        "schema_version": trace_schema, "enabled": True,
                        "aggregate": {
                            "input_started": point(0), "first_rbc_output": point(1),
                            "rbc_output_quorum": point(2), "first_true_bba": point(3),
                            "true_bba_quorum": point(4),
                            "false_input_injected": {"recorded": False, "offset_us": 0},
                            "all_bba_decided": point(5), "truthy_rbc_ready": point(6),
                            "core_decision": point(7),
                        },
                        "wait_us": {"true_bba_quorum_us": 1, "all_bba_us": 2, "truthy_rbc_us": 3},
                        "adapter": {
                            "common_subset_decoded": point(8), "block_body_built": point(9),
                            "node_output_received": point(10),
                        },
                        "rbc": [{"proposer_id": proposer, "trace": {}} for proposer in range(4)],
                        "bba": [{"proposer_id": proposer, "trace": {}} for proposer in range(4)],
                        "messages": [
                            {"subtype": "proof", "trace": {
                                "inbound_count": 1, "inbound_bytes": 10,
                                "outbound_count": 2, "outbound_bytes": 20,
                                "send_count": 2, "send_total_us": 4,
                                "send_max_us": 2, "send_failure_count": 0,
                                "encode": {"count": 2, "total_us": 2, "max_us": 1},
                                "queue_wait": {"count": 2, "total_us": 0, "max_us": 0},
                                "stream_open": {"count": 2, "total_us": 0, "max_us": 0},
                                "write": {"count": 2, "total_us": 2, "max_us": 1},
                                "finalize": {"count": 2, "total_us": 0, "max_us": 0},
                                "stream_open_count": 0 if run["stream_mode"] == "persistent" else 2,
                                "stream_reuse_count": 2 if run["stream_mode"] == "persistent" else 0,
                            }},
                            *[{"subtype": subtype, "trace": {
                                "inbound_count": 0, "inbound_bytes": 0,
                                "outbound_count": 0, "outbound_bytes": 0,
                                "send_count": 0, "send_total_us": 0,
                                "send_max_us": 0, "send_failure_count": 0,
                                "encode": {"count": 0, "total_us": 0, "max_us": 0},
                                "queue_wait": {"count": 0, "total_us": 0, "max_us": 0},
                                "stream_open": {"count": 0, "total_us": 0, "max_us": 0},
                                "write": {"count": 0, "total_us": 0, "max_us": 0},
                                "finalize": {"count": 0, "total_us": 0, "max_us": 0},
                                "stream_open_count": 0, "stream_reuse_count": 0,
                            }} for subtype in ("echo", "ready", "bval", "aux")],
                        ],
                    })
            artifacts.write_csv(scenario_root / "node_measurements.csv", node_rows, list(node_rows[0]))
            with (scenario_root / "acs_trace.jsonl").open("w", encoding="utf-8") as handle:
                for record in trace_records:
                    handle.write(json.dumps(record, separators=(",", ":")) + "\n")

    def add_resource_segments(self, root):
        for node in range(4):
            for block in range(1, 11):
                for batch in (8, 32, 128):
                    rows = []
                    for index in range(4):
                        rows.append({
                            "timestamp": f"2026-08-18T00:00:00.{index * 250:03d}Z",
                            "sample_index": str(index), "node": str(node), "region": "us-east-1",
                            "scenario": f"n4-b{batch}-block-{block}", "phase": "resource-measured",
                            "cpu_usage_us": str(1000 + index * 10),
                            "memory_current_bytes": str(100 + index), "memory_peak_bytes": "200",
                            "network_receive_bytes": str(2000 + index * 20),
                            "network_transmit_bytes": str(3000 + index * 30),
                            "restart_count": "0", "oom_killed": "false",
                        })
                    path = root / f"logs/node-{node}/resources/node-{node}-block-{block}-batch-{batch}.csv"
                    artifacts.write_csv(path, rows, artifacts.RESOURCE_TIMESERIES_FIELDS)
                    path.with_suffix(".log").write_text("", encoding="utf-8")

    def test_final_phase_accepts_complete_pilot_and_retained_failure(self):
        root = self.make_phase()
        rows = artifacts.read_csv(next(root.glob("scenarios/**/run_measurements.csv")))
        rows[0].update(success="false", consistent="false", outcome="timed_out", timed_out="true", selected_ciphertexts="0")
        artifacts.write_csv(next(root.glob("scenarios/**/run_measurements.csv")), rows)
        artifacts.assert_final_phase(root, "same-az", "readiness-pilot")

    def test_final_diagnostic_phase_accepts_complete_acs_trace_artifacts(self):
        root = self.make_phase()
        self.add_acs_trace_artifacts(root)

        artifacts.assert_final_phase(root, "same-az", "readiness-pilot")

    def test_final_diagnostic_latency_accepts_lean_5_by_30_schedule(self):
        root = self.make_phase("latency", attempts=30, blocks_override=3, warmups_override=5)
        self.add_acs_trace_artifacts(root)

        artifacts.assert_final_phase(root, "same-az", "latency")

    def test_final_persistent_stream_diagnostic_accepts_v2_mode_provenance(self):
        root = self.make_phase("latency", attempts=30, blocks_override=3, warmups_override=5,
                               stream_mode="persistent")
        self.add_acs_trace_artifacts(root, "bloc-acs-trace/v2")

        artifacts.assert_final_phase(root, "same-az", "latency")

    def test_final_persistent_stream_diagnostic_rejects_v1_trace(self):
        root = self.make_phase("latency", attempts=30, blocks_override=3, warmups_override=5,
                               stream_mode="persistent")
        self.add_acs_trace_artifacts(root, "bloc-acs-trace/v1")

        with self.assertRaisesRegex(ValueError, "persistent stream mode requires ACS trace schema"):
            artifacts.assert_final_phase(root, "same-az", "latency")

    def test_final_phase_rejects_stream_mode_provenance_drift(self):
        mutations = {
            "missing phase": lambda root: self._remove_json_key(root / "manifest.json", "stream_mode"),
            "phase": lambda root: self._change_json(root / "manifest.json", "stream_mode", "fresh"),
            "cluster": lambda root: self._change_cluster_mode(root, "fresh"),
            "remote": lambda root: self._change_json(root / "generated-public/remote-eval.json", "stream_mode", "fresh"),
            "evaluator": lambda root: self._change_json(next(root.glob("scenarios/**/manifest.json")), "stream_mode", "fresh"),
            "run": lambda root: self._change_csv_mode(next(root.glob("scenarios/**/run_measurements.csv")), "fresh"),
            "node": lambda root: self._change_csv_mode(next(root.glob("scenarios/**/node_measurements.csv")), "fresh"),
        }
        for name, mutate in mutations.items():
            with self.subTest(name=name):
                candidate = self.root / "latency"
                if candidate.exists():
                    shutil.rmtree(candidate)
                root = self.make_phase("latency", attempts=30, blocks_override=3, warmups_override=5,
                                       stream_mode="persistent")
                self.add_acs_trace_artifacts(root, "bloc-acs-trace/v2")
                try:
                    mutate(root)
                    with self.assertRaisesRegex(ValueError, "stream mode"):
                        artifacts.assert_final_phase(root, "same-az", "latency")
                finally:
                    shutil.rmtree(root)

    @staticmethod
    def _change_json(path, key, value):
        payload = json.loads(path.read_text(encoding="utf-8"))
        payload[key] = value
        artifacts.write_json(path, payload)

    @staticmethod
    def _remove_json_key(path, key):
        payload = json.loads(path.read_text(encoding="utf-8"))
        payload.pop(key, None)
        artifacts.write_json(path, payload)

    def test_final_persistent_stream_diagnostic_rejects_v1_v2_mixing(self):
        root = self.make_phase("latency", attempts=30, blocks_override=3, warmups_override=5,
                               stream_mode="persistent")
        self.add_acs_trace_artifacts(root, "bloc-acs-trace/v2")
        evaluator = next(root.glob("scenarios/**/manifest.json"))
        self._change_json(evaluator, "acs_trace_schema", "bloc-acs-trace/v1")

        with self.assertRaisesRegex(ValueError, "ACS trace schema mismatch"):
            artifacts.assert_final_phase(root, "same-az", "latency")

    @staticmethod
    def _change_cluster_mode(root, value):
        path = root / "generated-public/cluster.json"
        payload = json.loads(path.read_text(encoding="utf-8"))
        payload["network"]["stream_mode"] = value
        artifacts.write_json(path, payload)

    @staticmethod
    def _change_csv_mode(path, value):
        rows = artifacts.read_csv(path)
        rows[0]["stream_mode"] = value
        artifacts.write_csv(path, rows, list(rows[0]))

    def test_final_diagnostic_phase_rejects_missing_node_trace(self):
        root = self.make_phase()
        self.add_acs_trace_artifacts(root)
        path = next(root.glob("scenarios/**/acs_trace.jsonl"))
        lines = path.read_text(encoding="utf-8").splitlines()
        path.write_text("\n".join(lines[:-1]) + "\n", encoding="utf-8")

        with self.assertRaisesRegex(ValueError, "missing node trace"):
            artifacts.assert_final_phase(root, "same-az", "readiness-pilot")

    def test_final_diagnostic_phase_rejects_duplicate_trace_key(self):
        root = self.make_phase()
        self.add_acs_trace_artifacts(root)
        path = next(root.glob("scenarios/**/acs_trace.jsonl"))
        lines = path.read_text(encoding="utf-8").splitlines()
        path.write_text("\n".join([*lines, lines[0]]) + "\n", encoding="utf-8")

        with self.assertRaisesRegex(ValueError, "duplicate ACS trace key"):
            artifacts.assert_final_phase(root, "same-az", "readiness-pilot")

    def test_final_diagnostic_phase_rejects_unknown_trace_schema(self):
        root = self.make_phase()
        self.add_acs_trace_artifacts(root)
        manifest = json.loads((root / "manifest.json").read_text(encoding="utf-8"))
        manifest["acs_trace_schema"] = "bloc-acs-trace/v999"
        artifacts.write_json(root / "manifest.json", manifest)

        with self.assertRaisesRegex(ValueError, "unsupported ACS trace schema"):
            artifacts.assert_final_phase(root, "same-az", "readiness-pilot")

    def test_final_diagnostic_phase_rejects_corrupt_trace_details(self):
        root = self.make_phase()
        self.add_acs_trace_artifacts(root)
        path = next(root.glob("scenarios/**/acs_trace.jsonl"))
        original = path.read_text(encoding="utf-8").splitlines()

        def negative_offset(record):
            record["aggregate"]["core_decision"]["offset_us"] = -1

        def impossible_order(record):
            record["aggregate"]["core_decision"]["offset_us"] = 11

        def unknown_proposer(record):
            record["rbc"].append({"proposer_id": 99, "trace": {}})

        def missing_subtype(record):
            record["messages"] = record["messages"][:-1]

        def mismatched_count(record):
            record["messages"][0]["trace"]["inbound_count"] = 2

        for name, mutate, message in (
            ("negative offset", negative_offset, "negative ACS trace offset"),
            ("impossible order", impossible_order, "core decision occurs after"),
            ("unknown proposer", unknown_proposer, "proposer membership"),
            ("missing subtype", missing_subtype, "message subtypes"),
            ("mismatched count", mismatched_count, "aggregate/detail"),
        ):
            with self.subTest(name=name):
                record = json.loads(original[0])
                mutate(record)
                changed = [json.dumps(record, separators=(",", ":")), *original[1:]]
                path.write_text("\n".join(changed) + "\n", encoding="utf-8")
                with self.assertRaisesRegex(ValueError, message):
                    artifacts.assert_final_phase(root, "same-az", "readiness-pilot")
        path.write_text("\n".join(original) + "\n", encoding="utf-8")

    def test_final_phase_accepts_run_ids_scoped_to_measurement_block(self):
        root = self.make_phase("latency", attempts=1000)
        artifacts.assert_final_phase(root, "same-az", "latency")
        path = next(root.glob("scenarios/**/run_measurements.csv"))
        rows = artifacts.read_csv(path)
        first = next(row for row in rows if row["batch_size"] == "8" and row["measurement_block"] == "1")
        second = next(
            row for row in rows
            if row["batch_size"] == "8" and row["measurement_block"] == "1" and row is not first
        )
        second["run_id"] = first["run_id"]
        artifacts.write_csv(path, rows)
        with self.assertRaisesRegex(ValueError, "retained attempts"):
            artifacts.assert_final_phase(root, "same-az", "latency")

    def test_final_resource_phase_merges_exact_segments_and_generates_summaries(self):
        root = self.make_phase("resource", attempts=1000)
        self.add_resource_segments(root)

        artifacts.finalize_final_resource_artifacts(root)
        artifacts.assert_final_phase(root, "same-az", "resource")

        self.assertEqual(len(artifacts.read_csv(root / "resource_timeseries.csv")), 4 * 10 * 3 * 4)
        self.assertEqual(len(artifacts.read_csv(root / "resource-segment-summary.csv")), 4 * 10 * 3 + 10 * 3)
        self.assertEqual(len(artifacts.read_csv(root / "resource-summary.csv")), 4 * 3 + 3)
        scenarios = {row["scenario"] for row in artifacts.read_csv(root / "resource-summary.csv")}
        self.assertEqual(scenarios, {"n4-b8", "n4-b32", "n4-b128"})

    def test_final_resource_phase_rejects_missing_segment_before_acceptance(self):
        root = self.make_phase("resource", attempts=1000)
        self.add_resource_segments(root)
        (root / "logs/node-3/resources/node-3-block-10-batch-128.csv").unlink()

        with self.assertRaisesRegex(ValueError, "resource segment set"):
            artifacts.finalize_final_resource_artifacts(root)

    def test_final_resource_phase_rejects_wrong_operator_region(self):
        root = self.make_phase("resource", attempts=1000)
        self.add_resource_segments(root)
        path = root / "logs/node-2/resources/node-2-block-4-batch-32.csv"
        rows = artifacts.read_csv(path)
        rows[0]["region"] = "eu-west-1"
        artifacts.write_csv(path, rows, artifacts.RESOURCE_TIMESERIES_FIELDS)

        with self.assertRaisesRegex(ValueError, "metadata mismatch"):
            artifacts.finalize_final_resource_artifacts(root)

    def test_final_resource_phase_does_not_accept_manifest_only(self):
        root = self.make_phase("resource", attempts=1000)
        with self.assertRaisesRegex((OSError, ValueError), "resource"):
            artifacts.assert_final_phase(root, "same-az", "resource")

    def test_final_phase_rejects_identity_count_and_phase_mutations(self):
        for field, value, message in (
            ("source_sha", "bad", "source"), ("bloc_image", "latest", "image"),
            ("repetitions", 2, "repetitions"), ("sampler", "on", "sampler"),
        ):
            with self.subTest(field=field):
                root = self.make_phase() if not (self.root / "readiness-pilot").exists() else self.root / "readiness-pilot"
                manifest = json.loads((root / "manifest.json").read_text())
                original = manifest[field]; manifest[field] = value; artifacts.write_json(root / "manifest.json", manifest)
                with self.assertRaisesRegex(ValueError, message):
                    artifacts.assert_final_phase(root, "same-az", "readiness-pilot")
                manifest[field] = original; artifacts.write_json(root / "manifest.json", manifest)

    def test_final_phase_rejects_wrong_selected_count_and_secret_leak(self):
        root = self.make_phase()
        path = next(root.glob("scenarios/**/run_measurements.csv")); rows = artifacts.read_csv(path)
        rows[0]["selected_ciphertexts"] = "7"; artifacts.write_csv(path, rows)
        with self.assertRaisesRegex(ValueError, "selected"):
            artifacts.assert_final_phase(root, "same-az", "readiness-pilot")
        rows[0]["selected_ciphertexts"] = "8"; artifacts.write_csv(path, rows)
        (root / "operator-0.json").write_text("secret")
        with self.assertRaisesRegex(ValueError, "secret"):
            artifacts.assert_final_phase(root, "same-az", "readiness-pilot")

    def test_final_cleanup_requires_successful_empty_categories_and_state(self):
        path = self.root / "cleanup.json"
        artifacts.write_json(path, {"regions": {"us-east-1": {
            "query_succeeded": True, "instances": [], "volumes": [], "vpcs": [],
            "subnets": [], "security_groups": [], "route_tables": [], "key_pairs": [],
            "peering_connections": [],
        }}, "iam": {"query_succeeded": True, "roles": [], "instance_profiles": []}, "terraform_state": []})
        artifacts.assert_final_cleanup(path, ["us-east-1"])
        payload = json.loads(path.read_text()); payload["regions"]["us-east-1"]["volumes"] = ["vol-1"]
        artifacts.write_json(path, payload)
        with self.assertRaisesRegex(ValueError, "volumes"):
            artifacts.assert_final_cleanup(path, ["us-east-1"])

        payload["regions"]["us-east-1"]["volumes"] = []
        payload["regions"]["us-east-1"].pop("peering_connections")
        artifacts.write_json(path, payload)
        with self.assertRaisesRegex(ValueError, "peering_connections"):
            artifacts.assert_final_cleanup(path, ["us-east-1"])


if __name__ == "__main__":
    unittest.main()
