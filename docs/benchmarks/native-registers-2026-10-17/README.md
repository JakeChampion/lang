# Native register-pool measurements, 2026-10-17

The Perf workflow built baseline `5f93dfb1c` and each candidate to a native
ARM64 Darwin fixpoint on one `macos-15` runner per comparison. Both compilers
compiled the same RFC 7748 public-key workload and the same checker source.
Execution order alternated within pairs; warmups precede measurement. X25519
performs 8,192 public-key operations per process. These are shared-runner wall
times, not strict performance gates.

| Candidate | Run | Pairs: X25519/compiler | Median candidate/baseline: X25519 | Compiler |
| --- | --- | --- | --- | --- |
| `46383fe53`: x1–x3/x8 homes, generic argument moves | [39015173167](https://github.com/JakeChampion/lang/actions/runs/39015173167) | 7/5 | 0.971725 | 1.013076 |
| `071ca3782`: short argument-list fast paths | [39015649000](https://github.com/JakeChampion/lang/actions/runs/39015649000) | 15/15 | 1.012042 | 0.998383 |
| `8a760ea56`: x30 homes with saved return addresses, acyclic-pair fast path | [39017203453](https://github.com/JakeChampion/lang/actions/runs/39017203453) | 15/15 | 0.972127 | 0.999837 |

Ratios below one are faster. The v5 run measured about 2.8% faster X25519
and essentially unchanged compiler time. The earlier repeat without x30 did
not reproduce its initial small gain; all three reports are retained here
with every timing and compiler hash, so the variability stays visible.

The ARM64 Darwin assembly for `field25519.mul`, counting the whole function,
changes from 129 load and 42 store instructions to 109 and 33. Instructions
addressing the stack fall from 128 to 99. Some operands and intermediate
values still spill: #12548 remains open for further work, including repeated
constant materialization. The x86 fixed-clobber audit is documented in
[Native register pools](../../NATIVE-REGISTER-POOLS.md).

The raw workflow artifacts are copied unchanged as [v2.json](v2.json),
[v4.json](v4.json) and [v5.json](v5.json). Reproduce a comparison with
`scripts/native-pair-bench`; the ordinary instruction-count, allocation,
constant-time and driver-size gates remain separate.

## Current main integration

The refreshed pool includes main `38735b2b2`'s carry and immediate-constant
lowering, with baseline `2f64be0ce` (the same main plus the consuming-call alias
fix). Two independent `macos-15` workflow runs use the same native compiler
binaries and frozen baseline sources:

| Run | Pairs per workload | X25519 ratio | Compiler ratio |
| --- | ---: | ---: | ---: |
| [39035165698](https://github.com/JakeChampion/lang/actions/runs/39035165698) | 15 | 0.938422 | 1.026097 |
| [39035884774](https://github.com/JakeChampion/lang/actions/runs/39035884774) | 15 | 0.972092 | 0.960723 |
| All 30 paired ratios, median | 30 | 0.951028 | 0.999986 |

The X25519 medians improve in both rounds. Compiler elapsed time is effectively
unchanged across the pooled pairs; individual timings vary substantially.
These remain shared-runner measurements of the frozen baseline workloads.
Raw artifacts are retained as [first](main-387-first.json) and
[repeat](main-387-repeat.json), including every timing and both compiler hashes.
The ordinary performance workflow also passes on the refreshed source:
[39035167910](https://github.com/JakeChampion/lang/actions/runs/39035167910).

The refreshed Linux compiler reaches a fixed point at 12,545,904 bytes,
SHA-256 `948f394b51d1e5576895eb367a529b3dd53f3b8506f04ee83446d44ac170e8f6`.
The ABI, floating-point and hardening suite passes, including link-register
homes under all six ordinary/profile/PAC combinations and a thirteen-argument
paired return. All 11 strict driver size budgets pass.
