# Professor Meeting Notes

Add one short dated section per meeting. Keep each entry focused on the problem,
the implemented solution, the evidence produced, and any remaining boundary.

## 2026-08-26 — Progress Over the Previous Month

### Summary

Over the past month, the project moved from evaluation preparation to accepted
large-sample WAN latency evidence. We made the campaign statistically defensible,
froze its inputs, verified the complete configuration matrix locally, and then
collected 6,000 successful three-region measurements.

### Closed Issues: Problem → Solution

| Issue | Problem | Solution |
|---|---|---|
| [#11](https://github.com/VascoMS/bloc/issues/11) | Resource collection could distort the latency being measured. | Added a separate host-level CPU, memory, and network sampling and validation path. Live resource evidence remains separate work. |
| [#12](https://github.com/VascoMS/bloc/issues/12) | Earlier runs were too small for defensible p99 claims and did not clearly retain failures. | Added 1,000-sample scheduling, Type-7 p99, confidence intervals, and explicit success, failure, and timeout accounting. |
| [#13](https://github.com/VascoMS/bloc/issues/13) | We lacked a representative and reproducible transaction workload. | Built a deterministic signed-transaction corpus and measured plaintext, encrypted-size, and client-preparation overhead by payload class. |
| [#14](https://github.com/VascoMS/bloc/issues/14) | Results could become incomparable if campaigns mixed code or configuration versions. | Validated and froze an evaluation baseline with recorded source, image, configuration, and artifact identities. |
| [#8](https://github.com/VascoMS/bloc/issues/8) | A live cloud campaign could fail after resources were allocated because a configuration had never been exercised. | Ran the complete local campaign contract as validation-only evidence before starting final cloud measurements. |
| [#16](https://github.com/VascoMS/bloc/issues/16) | We still lacked statistically strong end-to-end latency evidence across geographically distributed operators. | Collected matched three-region `n=4,t=3` and `n=7,t=5` campaigns for batches `8/32/128`, with 1,000 accepted observations per configuration. |

### Evidence to Show

- [Critical-path phase breakdown](../results/final-evidence/issue-16-three-region/critical-path-stage-breakdown.png)
- [End-to-end p50/p95/p99 latency](../results/final-evidence/issue-16-three-region/total-slot-latency-percentiles.png)

The headline result is that all 6,000 retained WAN attempts completed
successfully and consistently within the 12-second protocol deadline. The
measurements end at sidecar plaintext materialization; resource usage, batch
512, `n=10`, signing, and block publication are not part of this accepted set.
