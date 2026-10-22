# One kernel, three evaluators (2026-10-21)

`kernel.fern` evaluates one generic body (`docs/SIMD-GENERIC-KERNELS.md`) over
20,000 doubles as `f64`, as `simd.Dual[f64]` (value and derivative) and as
`simd.DoubleDouble[f64]`, timed with `std/bench` on x86-64-linux
(`fern -target x86-64-linux -o kernel docs/benchmarks/simd-evaluators-2026-10-21/kernel.fern`).
The kernel is a degree-six Horner polynomial, a logistic through `exp`, and a
square root: fourteen `Lanes` operations and one `Math` call per element.

4-core x86-64 container, 20 timed iterations after 3 warm-ups, p50 per
iteration of the whole array:

| Evaluator | p50 per 20,000 elements | per element | allocations per element |
| --- | --- | --- | --- |
| `f64` | 0.40 ms | 20 ns | 0 |
| `Dual[f64]` | 6.5 ms | 324 ns | 21 |
| `DoubleDouble[f64]` | 72 ms | 3,610 ns | 323 |

## Reading

The answers are right (the suite in `tests/stdlib/simd_test.fern` holds them),
but the records are materialised: every `Dual` operation allocates its result
and every `DoubleDouble` operation allocates each intermediate `two_sum` and
`two_prod` pair, so the dual costs 16x the scalar where the arithmetic alone
is 3x, and the double-double 180x where the arithmetic is roughly 20x.
`seminline` splices a leaf that builds a record from literals; a leaf that
builds one from its parameters' fields (`Dual { v: self.v + o.v, d: self.d +
o.d }`) is not spliced, so its construction survives into the count planner
as an allocation. That is the compiler work the brief's §5 named as the risk
to measure: splicing such leaves and reading the parts off the construction
would take both evaluators to the arithmetic's own cost. Tracked as #12931.
