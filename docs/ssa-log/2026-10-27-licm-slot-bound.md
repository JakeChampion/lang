# Reuse the LICM slot bound

`licm_slot_use` scanned the complete operation array to find the maximum local slot for each loop.
`hoist_loop_invariants` already maintains that bound and extends it after each new slot.
The change passes the current bound into the pure-hoist analysis.
The store and load analyses still inspect the current operations for each loop.

The subject is a full `checker.fern` compile with `-O -target x86-64-linux -emit asm`.
One stage-1 compiler produced the baseline and candidate stage-2 compilers.
Both measured compilers use the same frozen input and warm private certifier memo directories.

| Metric | Baseline | Candidate |
|---|---:|---:|
| Instructions | 25,719,653,483 | 25,261,399,930 |
| Maximum-slot scans | 17,152 | 9,199 |
| Instructions in the maximum-slot scan | 515,372,921 | 56,940,213 |
| Calls to `__fern_arr_box` | 20,526,860 | 20,526,860 |

The total instruction count decreases by 1.781725%.
The full checker output is byte-identical on x86-64, ARM64 and Wasm.
A private differential driver compares all operation fields and retained aliases across 350 cases on all three targets.
Each candidate output matches the baseline.

Five native pairs alternate the execution order and give mixed CPU results.
The best baseline and candidate times are 5.640790 and 5.663143 seconds.
The median times are 5.787134 and 5.737805 seconds.
This sample does not establish a native latency increase or decrease.
The measured benefit is fewer instructions for this subject, with the same output and allocation count.

The [evidence](../benchmarks/optimizer-slot-bound-2026-10-27/README.md) includes raw profiles, binary identities, source manifests, commands and superseded prototypes.
This slice does not complete the architecture roadmap in issue #4412.
