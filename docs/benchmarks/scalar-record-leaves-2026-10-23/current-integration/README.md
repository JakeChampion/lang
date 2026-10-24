# Current scalar record leaf checks

The source commit is `45b3a95f91970b05027c48a1d3560ebff5a24bbb`.
The native compiler and library bytes match checkpoint `dd01b2d6cdddb26ee2c8ec7330aefdaf90dbd0d7`.
The later merge adds the Go namespace and default-constant fixes.

All 121 compiler regression cases and the feature census pass.
No case failed or skipped.
The regression checks distinct methods with one name, wide builders, loop phis, finalizers and immutable arrays.
It also checks that a scalar call marked `@noinline` stays a call.
The 18-test SIMD suite passes both interpreters and all three execution targets.
All three runtime allocation counts balance with zero live bytes.

The original benchmark source and library are identical for both compilers.
Four pairs alternate execution order on one host.
Each executable performs three warmups and twenty timed iterations over 20000 elements.
Both record evaluators now allocate zero per operation.
The scalar evaluator also allocates zero.

| Evaluator | Baseline allocations per element | Candidate allocations per element | Baseline median p50 | Candidate median p50 |
| --- | ---: | ---: | ---: | ---: |
| f64 | 0 | 0 | 185785.5 ns | 194358.5 ns |
| Dual[f64] | 21 | 0 | 2734264.5 ns | 374483.5 ns |
| DoubleDouble[f64] | 323 | 0 | 65366768.5 ns | 32465648.5 ns |

The scalar timing rises 4.6%. The two record timings fall.
These times describe this host and workload.
The allocation gate is exact.
The report records every execution and executable hash.

The released stage0 pin produces byte-identical stages 2 and 3.
All 11 strict driver size gates pass.
Go format, the compiler test package vet check, and all 1200 canonical Fern format checks pass.
The Go semantic reference comes from `d457d861824ad9aad5a7a36a61b7de44ec627d13`.
Its standard library bytes match this branch.
The native builds use the candidate compiler directly.

The manifest records 496 source file hashes.
The source patch passes a reverse-application check.
The archive contains all 38 benchmark and runtime output logs.
Earlier evidence in the parent directory retains its original source scope.
