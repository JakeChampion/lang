# Ratchet the smaller Map instruction counts

CI run `40120850761` reports three ARM64 Map benchmarks below the stored static instruction counts.
The strict gate treats improvements as new limits and requires updated baseline rows.
The compiler and library code remain at the source recorded by the prior validation capsule.

A fresh local compile measures all three benchmarks on both Linux architectures.
The ARM64 results match the CI measurements exactly.
The count is the number of emitted assembly lines that start with whitespace, as measured by `scripts/perf-bench`.
These static counts decrease by 2.23% to 3.03%.
This result does not measure elapsed time or dynamic instruction cost.

| Metric | Previous | Measured |
| --- | ---: | ---: |
| x86_64/map_int.text | 5738 | 5589 |
| x86_64/map_probe_chain.text | 5745 | 5596 |
| x86_64/map_string.text | 6669 | 6520 |
| aarch64/map_int.text | 6106 | 5921 |
| aarch64/map_probe_chain.text | 6117 | 5932 |
| aarch64/map_string.text | 7198 | 7013 |

Only these six static baseline rows change.
All tolerance values and dynamic baseline rows retain their values.
The complete ARM64 CI report passes the strict gate with the updated baseline.
That report contains 78 static and dynamic metrics.
The capsule contains the CI log and report, emitted assembly, hashes, and the baseline patch.
