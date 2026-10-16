# Spill cost and the ARM64 ordmap regression

The linear allocator spills a value for its whole lifetime when it evicts its
home. Reference weight divided by interval length undervalued frequently read
values with long lifetimes: changing instruction positions could change the
victim even when those reads did not change (#12303).

The score is now squared reference weight divided by lifetime length. References
still carry loop weights, and lifetime still prices register occupancy, but
expensive spills receive more protection. Both comparisons use the same score:
choosing an active victim and comparing that victim with the incoming interval.
Cross-products use `f64`, avoiding integer overflow for large weights and spans;
there is no square-root operation. Equal scores retain the latest-ending tie
break.

Eviction also respects the incoming interval's fixed-register constraint
(#12494). A value crossing division, remainder or high multiplication cannot
acquire the ABI result register by evicting its current occupant. The other
placement paths already applied that constraint.

## Measurements

Baseline: `7a0e84d3143f901cca805856c7d8bfa67bc6a69c`, matching stdlib, stage0
`stage0-20261009-823f7b1` (SHA-256
`9e94047df7120c0527840ee34235f4bbb88c309466c890d41e4d655955fbd80a`).
Both compiler variants were built with the same compiler. The public CLI and
`scripts/perf-bench` were checked as well as the raw compiler driver.

All 35 top-level benchmarks were compiled and executed with both variants on
both ISAs. Checksums and stdout match. x86-64 instruction counts use callgrind
3.24.0. ARM64 counts use QEMU 10.0.5 with its instruction-counting plugin
(`-cpu max`, `inline=true`); they count guest instructions, not host work or
native elapsed time. Static instruction counts were measured too.

| Benchmark | ISA | Before | After | Change |
| --- | --- | ---: | ---: | ---: |
| ordmap_insert | ARM64 | 124,679,986 | 118,541,839 | -4.92% |
| perceus_rbtree | ARM64 | 62,476,114 | 61,408,114 | -1.71% |
| perceus_rbtree_ck | ARM64 | 72,953,478 | 72,093,318 | -1.18% |
| pmap_insert | ARM64 | 71,088,485 | 71,781,287 | +0.97% |
| ordmap_insert | x86-64 | 112,046,711 | 109,992,304 | -1.83% |
| enum_match | x86-64 | 53,325,055 | 51,750,055 | -2.95% |
| pmap_insert | x86-64 | 62,223,251 | 61,190,282 | -1.66% |
| pvec_with | x86-64 | 60,156,824 | 59,205,267 | -1.58% |
| sort_strings | x86-64 | 106,413,750 | 105,209,600 | -1.13% |

The ARM64 pmap tradeoff remains within its unchanged 1% budget; its baseline
stays 71,272,111. The ordmap result is also below the 122.2M baseline that
preceded the regression. Only measured improvements exceeding the tolerance
lower the checked-in ceilings. No ceiling or tolerance is raised. Both complete
static/dynamic reports pass the strict performance gate after banking those
improvements.

## Correctness

The allocator tests exercise a frequently read long interval competing with a
cheap short one, and a cold-long-interval control. The fixed-register regression
covers all five constrained operations, an unconstrained target and a second
available register; unchanged main fails its five constrained cases.

The x86-64, ARM64 and WASI IR execution suites and the unchanged complexity
ratchet pass. Bootstrap stage 2 equals stage 3. These checks accompany the
benchmark checksum comparisons; smaller instruction counts alone do not prove
correctness.
