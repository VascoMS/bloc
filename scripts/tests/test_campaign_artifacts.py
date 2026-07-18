import csv
import importlib.util
import json
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
        artifacts.evaluator_assert(path, ["4/8=1"])
        rows = artifacts.read_csv(path); rows[0]["consistent"] = "false"; self.write_csv(path, rows)
        with self.assertRaises(ValueError): artifacts.evaluator_assert(path, ["4/8=1"])

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
        self.write_csv(phase / "resource-samples.csv", [{
            "container_status": "running", "restart_count": "0", "oom_killed": "false",
        }])
        return phase

    def test_three_region_phase_contract_accepts_complete_evidence(self):
        phase = self.make_three_region_phase()
        artifacts.assert_three_region_phase(phase)

    def test_three_region_phase_contract_rejects_incomplete_pairwise_health(self):
        phase = self.make_three_region_phase()
        rows = artifacts.read_csv(phase / "network-post.csv")
        rows[0]["successes"] = "4"
        self.write_csv(phase / "network-post.csv", rows)
        with self.assertRaisesRegex(ValueError, "five health attempts"):
            artifacts.assert_three_region_phase(phase)


if __name__ == "__main__":
    unittest.main()
