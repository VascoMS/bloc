# Professor Meeting Notes

Add one short dated section per meeting. Keep each entry focused on the problem,
the implemented solution, the evidence produced, and any remaining boundary.

## 2026-09-06 — ACS WAN Latency Diagnosis

### Question And Evidence

Recent work tested whether the high three-region ACS latency came from RBC
correctness, per-message libp2p stream negotiation, or application-stream
head-of-line blocking. Issues #23--#26 added matched same-AZ/WAN stream tests,
fixed READY self-admission, finalized transport traces, and tested separate
authenticated control/data lanes.

For n4 three-region `persistent`, the reproducible ACS p50 baseline is about
`232/258/519 ms` for batches `8/32/128`. A batch-128 proposal is about 202 KiB;
PROOF+ECHO contributes about 1.48 MiB of outbound application data per node,
with ECHO near 80% of ACS application bytes. In the lane experiment, READY
queue-wait p50 fell from `126.493 ms` to `0.044 ms` and first-RBC-output p50
fell from `422.542 ms` to `307.503 ms`, but RBC-output-quorum p50 stayed
`435.707/433.699 ms` and ACS p50 changed `518.620/525.464 ms`.

### Network-Measurement Correction

The earlier `40.5/138.5/183.3 ms` Ireland--Frankfurt/US--Ireland/US--Frankfurt
figures are complete tiny HTTP health-request times, not pure RTTs and not RBC
payload transfers. Each probe opens TCP, sends a bodyless `/healthz` request,
and receives a 26-byte JSON body. The corresponding TCP-connect RTT
approximations are `20.1/69.1/91.6 ms`; intra-region connect/total values are
about `0.18/0.64 ms`. The evidence establishes path delay but does not separate
available bandwidth, serialization, flow control, or retransmission cost for
the real RBC payloads.

### Conclusion And Boundary

Persistent prewarmed streams remove per-envelope negotiation, and two lanes
remove READY application-queue blocking, but neither improves the multi-region
RBC quorum or end-to-end ACS. Treat the current numbers as a scoped baseline,
keep single-stream `persistent` as the default, and stop incremental stream
tuning. Meaningful further reduction would require a separately scoped
protocol-level change such as smaller BTE proposals or decoupled data
availability/dissemination. The lane campaign has 30 observations per cell and
supports p50 plus exploratory p95/maximum, not p99.

Detailed evidence: [September ACS communication findings](archive/ACS_COMMUNICATION_LATENCY_FINDINGS_2026-09.md).

## 2026-08-26 — Progress Over the Previous Month

### Summary

Over the past month, the project moved from evaluation preparation to accepted
large-sample WAN latency evidence. We made the campaign statistically defensible,
froze its inputs, prepared consistent cryptographic material for every operator,
verified the complete configuration matrix locally, and then collected 6,000
successful three-region measurements.

### Closed Issues: Problem → Solution

| Issue | Problem | Solution |
|---|---|---|
| [#11](https://github.com/VascoMS/bloc/issues/11) | Resource collection could distort the latency being measured. | Added a separate host-level CPU, memory, and network sampling and validation path. Live resource evidence remains separate work. |
| [#12](https://github.com/VascoMS/bloc/issues/12) | Earlier runs were too small for defensible p99 claims and did not clearly retain failures. | Added 1,000-sample scheduling, Type-7 p99, confidence intervals, and explicit success, failure, and timeout accounting. |
| [#13](https://github.com/VascoMS/bloc/issues/13) | We lacked a representative and reproducible transaction workload. | Built a deterministic signed-transaction corpus and measured plaintext, encrypted-size, and client-preparation overhead by payload class. |
| [#14](https://github.com/VascoMS/bloc/issues/14) | Results could become incomparable if campaigns mixed code or configuration versions. | Validated and froze an evaluation baseline with recorded source, image, configuration, and artifact identities. |
| [#8](https://github.com/VascoMS/bloc/issues/8) | A live cloud campaign could fail after resources were allocated because a configuration had never been exercised. | Ran the complete local campaign contract as validation-only evidence before starting final cloud measurements. |
| [#16](https://github.com/VascoMS/bloc/issues/16) | We still lacked statistically strong end-to-end latency evidence across geographically distributed operators. | Collected matched three-region `n=4,t=3` and `n=7,t=5` campaigns for batches `8/32/128`, with 1,000 accepted observations per configuration. |

### Major Supporting Work Still Formally Open

Most of [Issue #15](https://github.com/VascoMS/bloc/issues/15) is complete. We
created the fixed BMax-128 CRS and public cluster configuration, generated a
separate private bundle for every operator containing its threshold-decryption
share and libp2p identity, and bound the source, images, corpus, configuration,
and bundles with checksums. We also corrected container paths, read-only public
mounts, and non-root secret ownership so every node starts with compatible
cryptographic material without runtime key generation. This setup enabled the
accepted Issue #16 runs; Issue #15 remains open for the separate live resource
collection phase.

### Evidence to Show

- [Critical-path phase breakdown](../results/final-evidence/issue-16-three-region/critical-path-stage-breakdown.png)
- [End-to-end p50/p95/p99 latency](../results/final-evidence/issue-16-three-region/total-slot-latency-percentiles.png)

The headline result is that all 6,000 retained WAN attempts completed
successfully and consistently within the 12-second protocol deadline. The
measurements end at sidecar plaintext materialization; resource usage, batch
512, `n=10`, signing, and block publication are not part of this accepted set.
