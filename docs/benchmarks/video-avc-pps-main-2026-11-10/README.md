# PPS qualification with the revised HTTP repair

PR #15089 integrates main `c9547605f5` and the revised HTTP repair `449ddc4ac1` from PR #15087.
Compiler source matches main.
The branch excludes the withdrawn array-view compiler optimization from issue #15093.

The HTTP repair reduces emitted code against the main baseline.
This update records 21 actual reductions: two native static counts, three self-host counts, and 16 profile size counts.
All metric keys and tolerances remain unchanged.
The two HTTP retired-instruction pins from the repair retain their actual CI value of 22,124,422.
Native ARM retired-instruction rows remain unchanged and require CI execution.

The current reports contain 80 native, 120 self-host emission, and 180 profile metrics.
Callgrind 3.24.0 supplies the x86 retired-instruction counts.
The native ARM static count comes from cross-emitted assembly.
`measurements.json` records each changed row, its measurement source, and the compiler hashes.

The controls compare feature and revised-repair standard libraries with the same current compiler.
All five benchmark programs produce identical output for x86, ARM, and WASI.
`controls.json` records all 15 pairs and their hashes.
The older control worktree has the same standard-library tree as revised repair `449ddc4ac1`.
The compiler comes from main and the source paths do not select the withdrawn compiler code.

The local tools ran these report commands:

```sh
scripts/perf-bench native.tsv
scripts/perf-bench-selfhost selfhost.tsv
scripts/profile-costs profile.tsv
```

The controls use this command with each source tree:

```sh
compiler -stdlib TREE/internal/stdlib -target TARGET -emit asm -o OUTPUT bench/BENCHMARK.fern
```

The comparison requires nonempty reports and `FERN_CI_PERF_GATE_STRICT=1`.
These compiler measurements do not establish isolated PPS, codec, or physical-device performance gains.
