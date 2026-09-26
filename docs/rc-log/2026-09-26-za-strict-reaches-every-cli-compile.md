# Strict reaches every CLI compile CI runs

`2026-09-26-y-strict-mode-is-the-gate.md` named three suites. This change
extends the gate to the rest of the places CI compiles with the self-host
CLI, the one driver that builds a substitution and so the only one that
reads `FERN_SEM_IR_STRICT`:

- `internal/e2e`, through a `TestMain` like `internal/e2eselfhost`'s.
- The four scripts that measure the self-hosted compiler:
  `perf-bench-selfhost`, `cliff-bench`, `selfhost-alloc-bench` and
  `coreutils-bench`.
- The multicall binary a release ships: the release step, and
  `TestSelfHostMulticallCompilesWhole`, which compiles it as the release
  does.

Strict alone is not enough to pin the typed path. `FERN_SEM_IR=` (empty)
turns the typed path off before strict is read, and a non-empty
`FERN_SEM_IR_ONLY` or `FERN_SEM_IR_SKIP` bypasses the refusal branch. So
each of these sites pins four settings: `FERN_SEM_IR=1`, the two lists
empty, and strict on. The coreutils package shares them as `typedPathEnv`.

The multicall test's cache key had been one file. The build-cache import
walker treated `../cat` as an external import, so all 107 of the
dispatcher's imports fell out of it. An import starting with `.` is now
local.

The current list lives in `SELFHOST-SEMANTIC-SOURCE.md`, step 2 of
"Retiring the AST lowering".

## Measured

- `checker.fern`, the cliff and alloc subject: 1401 of 1401 declarations.
- The multicall module compiles strict: 105 s on x86-64.
- All 29 benchmarks compile strict on x86-64, arm64 and wasm.
