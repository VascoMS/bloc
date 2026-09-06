# ACS Communication Latency Findings — September 2026

## Scope

This report consolidates the accepted issue #23--#26 evidence for the BLOC
`n=4`, `f=1` ACS path. It supersedes the pending-experiment conclusions in the
[August communication report](ACS_COMMUNICATION_LATENCY_FINDINGS_2026-08.md)
without rewriting that historical record. Source code, final validators, and
retained raw artifacts remain authoritative.

The report distinguishes protocol structure, measured effects, and remaining
hypotheses. It makes no n7, p99, Byzantine-schedule performance, production-
readiness, or universal ACS lower-bound claim.

## Evidence Ledger

| Evidence | Source | Matrix | Accepted result |
| --- | --- | --- | --- |
| Issue #23 fresh/persistent attribution | `7720b1f5bfce1997f611c1db95cead394b0349c4` | n4, same AZ and three region, batches 8/32/128, 30 measured per cell, trace v2 | 90/90 successful consistent runs and 420 traces per arm; persistent streams improve same-AZ ACS but not WAN ACS |
| Issue #24 READY self-admission | `94334944708d65833432bd9042eb129ffc004ea5` | deterministic RBC regression and full ACS safety gate | the local READY is counted exactly once at the relay transition without waiting for loopback delivery |
| Issue #25 trace finalization | `8a305af84918a01bab55dd93fe5245f2a5aad66d` | trace-on/off warning plus full local safety gate | v3 seals at ACS output and publishes after balanced terminal send accounting; measured observer cost is retained but negligible for matched WAN arms |
| Issue #26 local lane gate | `7a0099f0dd46e678d2e2483b9a166c12d3f99e35` | n4, batches 8/32/128, 30 measured per mode | correctness and mechanism gate passed; local ACS did not improve |
| Issue #26 three-region lane diagnostic | executable source `0caecb9298cb14923bfb07b63483ae90f864bba6` | n4, three region, batches 8/32/128, 5 warmups, 30 measured, 3 blocks, trace v3 | both arms retained 90/90 accepted runs and 420 finalized traces, exact schedules, zero send failures, and empty cleanup |

The issue #26 comparison used immutable BLOC image
`sha256:7b2f82aa625b63fb0992717626c762e2cee192a9466454a18f40cc04b8ac7a56`,
unchanged mempool image
`sha256:3c0c147a92d66c89293f9bda89967bded2ae22795bd37de09fa466ca4dbe38aa`,
public configuration ID
`64ba75655bedb5de328d8cc82b6e8239b6028ed4471cf43ce1e8a2a9607533bd`,
and encrypted corpus ID
`759085a3b2deb7c1f863952a96292771156de0a47a899e26a653abec8b7d148e`.

## What The Implementation Sends

Each of the four proposers starts one erasure-coded RBC. With `n=4,f=1`, the
`(N-2F,2F)` Reed--Solomon layout produces four shards of about half the encoded
proposal size. The proposer sends one distinct shard and Merkle proof to each
participant as PROOF. Every participant then sends its accepted shard/proof to
the other three participants as ECHO.

For encoded proposal length `L`, each node therefore sends approximately
`1.5L` remote PROOF data when it is proposer plus `6L` of ECHO data across the
four concurrent RBCs. The expected large-message total is about `7.5L` per
node, excluding fixed framing and proof overhead.

| Batch | Encoded proposal | Predicted PROOF+ECHO per node | Measured median |
| ---: | ---: | ---: | ---: |
| 8 | 12,234 B | 89.6 KiB | 91.7 KiB |
| 32 | 50,982 B | 373.4 KiB | 375.6 KiB |
| 128 | 201,622 B | 1,476.7 KiB | 1,479.0 KiB |

ECHO contributes about 80% of ACS outbound application bytes at n4. Sharding
reduces each individual fragment and permits reconstruction from a threshold;
it does not make the complete RBC bandwidth-minimal because shards are echoed
all-to-all. The fixed 1,061-byte expansion in every BTE ciphertext remains the
dominant contributor to proposal size.

## Fresh Versus Persistent Streams

Every issue #23 arm used authenticated persistent libp2p peer connections.
`fresh` opened and negotiated a logical application stream per envelope;
`persistent` prewarmed and reused one framed logical stream per peer. The
experiment therefore did not compare a full TCP/security handshake per message
with an open connection.

| Topology | Batch | Fresh ACS p50 | Persistent ACS p50 | Change |
| --- | ---: | ---: | ---: | ---: |
| Same AZ | 8 | 17.534 ms | 8.293 ms | -52.7% |
| Same AZ | 32 | 27.715 ms | 18.333 ms | -33.9% |
| Same AZ | 128 | 63.439 ms | 50.861 ms | -19.8% |
| Three region | 8 | 233.209 ms | 232.449 ms | -0.3% |
| Three region | 32 | 237.621 ms | 259.736 ms | +9.3% |
| Three region | 128 | 515.948 ms | 523.570 ms | +1.5% |

Persistent logical streams remove meaningful same-AZ overhead. All three WAN
cells had overlapping p50 intervals, so stream creation is not the observed
cross-region cause. The persistent batch-128 arm exposed a different mechanism:
large frames serialized small control messages behind one per-peer writer.

## Separate Persistent Control And Data Lanes

Issue #26 compared the single persistent stream with two authenticated,
prewarmed streams per peer. READY/BVAL/AUX used the control lane;
PROOF/ECHO/share used the data lane. Payload bytes, recipients, ACS rounds, the
underlying libp2p connection, and TCP congestion behavior were unchanged.

Both arms retained 90/90 successful, consistent, deadline-met measured slots,
420 sealed/finalized v3 traces including warmups, balanced subtype lifecycles,
and zero send failures. A deterministic 100,000-replicate paired percentile
bootstrap resampled the 30 matched runs while keeping four node traces in one
run cluster.

| Batch | Persistent ACS p50 | Lane ACS p50 | Change | 95% median-difference interval |
| ---: | ---: | ---: | ---: | ---: |
| 8 | 231.933 ms | 232.113 ms | +0.1% | [-1.809, +2.564] ms |
| 32 | 258.217 ms | 246.287 ms | -4.6% | [-35.298, +21.246] ms |
| 128 | 518.620 ms | 525.464 ms | +1.3% | [+0.758, +19.291] ms |

The primary batch-128 mechanism results were:

| Metric | Persistent p50 | Lane p50 | Change | 95% median-difference interval |
| --- | ---: | ---: | ---: | ---: |
| Per-node READY queue wait | 126.493 ms | 0.044 ms | -99.97% | [-315.570, -0.686] ms |
| Critical-node first RBC output | 422.542 ms | 307.503 ms | -27.2% | [-118.857, -72.453] ms |
| Critical-node RBC output quorum | 435.707 ms | 433.699 ms | -0.5% | [-42.469, +4.424] ms |
| Run-level ACS | 518.620 ms | 525.464 ms | +1.3% | [+0.758, +19.291] ms |

The lane mode removes the intended application head-of-line queue and advances
the first RBC output. It does not advance the RBC quorum or end-to-end ACS and
fails the specified 5% batch-128 ACS improvement gate. This is a mechanism-only
result, not an adoption result. `persistent` remains the default.

Batch-128 p95 ACS changed from `566.092 ms` to `555.132 ms`, while maximum
changed from `572.826 ms` to `700.215 ms`. Thirty observations make both tail
figures exploratory and support no p99 claim. Treatment full-slot p50 was also
33.8% higher, dominated by unrelated post-ACS merge and combine computation on
the later separately provisioned fleet. That host-performance confound is not
attributed to the lanes, but no full-slot improvement is claimed.

## Network-Probe Semantics

The accepted M3 topology health matrix is useful context but is not an RBC
payload benchmark. Every attempt launched a new plain-HTTP `curl` process,
opened TCP, sent a bodyless `GET /healthz`, and received a 26-byte ready JSON
body plus normal HTTP headers.

| Path | TCP connect p50 | Complete health request p50 |
| --- | ---: | ---: |
| Intra-region | 0.182 ms | 0.644 ms |
| Ireland--Frankfurt | 20.135 ms | 40.534 ms |
| US--Ireland | 69.133 ms | 138.483 ms |
| US--Frankfurt | 91.554 ms | 183.329 ms |

`avg_connect_ms` is the closest retained RTT approximation. `avg_total_ms`
includes the TCP handshake and a subsequent HTTP request/response and is about
two RTTs on the inter-region paths. Referring to the 40/138/183 ms totals as
RTTs is incorrect. These tiny probes establish connectivity and path delay but
cannot separate propagation, effective bandwidth, serialization, TCP flow
control, retransmissions, or remote scheduling for the real RBC messages.

## Conclusions By Confidence

Supported by accepted evidence:

- the current n4 three-region persistent ACS p50 baseline is approximately
  `232/258/519 ms` for batches `8/32/128`;
- fresh logical-stream negotiation is material in the same AZ but does not
  explain WAN ACS latency;
- one persistent writer introduces batch-128 application head-of-line queueing;
- two lanes remove that queueing and advance first RBC output, but do not move
  the RBC quorum or ACS decision;
- the proposal size and all-to-all ECHO pattern create the expected application
  byte amplification; and
- further direct-stream tuning is not the priority for this architecture.

Not established by current evidence:

- a universal lower bound for ACS latency;
- the separate contributions of RTT, link throughput, loss/retransmission, and
  host scheduling to the remaining WAN time;
- a lane-mode same-AZ result, n7 behavior, or p99/tail improvement; or
- that GossipSub, QUIC, or another transport alone would reduce the payload or
  quorum critical path.

## Decision And Future Boundary

Accept the measured values as the scoped baseline for the implemented
HoneyBadger-style ACS, keep `persistent` as the default, and stop focused
direct-stream tuning. A future latency-reduction task must be separately
authorized and should change the protocol-level payload path: reduce BTE
ciphertext/proposal size, disseminate encrypted bodies outside the ordering
critical path, or use an availability/dispersal construction that lets ACS
order compact commitments. A controlled RTT/bandwidth/loss matrix would improve
causal explanation, but it is not required to accept the current baseline.

## Retained Artifacts

- Issue #23 roots: `results/ec2/bloc-ec2-i23-p1-{sa,tr}-{fr,ps}-c4/`
- Issue #26 control: `results/ec2/bloc-ec2-i26-tr-ps-v3-p1/`
- Issue #26 treatment: `results/ec2/bloc-ec2-i26-tr-ln-v3-p1/`
- Issue #26 comparison:
  `results/local/acs-lane-campaign/issue-26-0caecb9/aws-analysis/three-region-comparison/`
- Issue #26 checksum manifest:
  `results/local/acs-lane-campaign/issue-26-0caecb9/evidence-sha256.txt`

The large raw roots and analysis outputs are intentionally ignored rather than
committed to Git. The checksum manifest binds their relocated primary-workspace
copies. It contains 1,318 file entries and has SHA-256
`c5e07b66d0e4b60e96ad731214dc461f40b6ca6dd89ea8cddfbd9a285fe5fe97`.
Private BTE shares and libp2p keys are excluded from that public evidence
manifest and remain local demo material.
