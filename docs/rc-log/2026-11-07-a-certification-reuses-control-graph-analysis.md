# Certification reuses control graph analysis

Certification now passes its existing block positions to the dominator analysis.
It derives reachability from the dominator preorder labels.
The dependency verifier uses the same operations.
Each extracted graph receives fresh analysis, and the proof rules remain unchanged.

The fixed checker compile decreases from 52,096,031 allocation events to 52,050,468.
Both compilers free every allocation and report zero live bytes.
Both emit identical assembly.
The same published stage0 builds both compilers without `-g`.
Certification stays enabled, while `FERN_RC_CERTIFY_MEMO=0` disables its proof cache.

The allocation reduction is 45,563 events, or 0.0875% of the complete cold compile.
Seven paired wall-time samples per cache mode vary too much to establish a speedup.
The harness now measures cold and warm proof caches separately.
The [measurement archive](../benchmarks/compiler-graphs-2026-11-07/README.md) retains every timing sample, exact counters, and profile summaries.

Dependency verification passes on x86-64, ARM64 under QEMU, and WebAssembly.
The regression graph covers sparse IDs, loops, a nonzero entry position, and an unreachable self-loop.
The selected certifier tests, source check, Go vet, and CI self-tests pass locally.

The cold CPU profile attributes 24.1% of inclusive samples to certification and 36.7% to `semlower.substitution`.
Those percentages describe a single diagnostic run and can overlap.
The next measurements target repeated semantic planning and certification dataflow.
The [compiler performance plan](../COMPILER-PERFORMANCE.md) records the controls and priorities.
