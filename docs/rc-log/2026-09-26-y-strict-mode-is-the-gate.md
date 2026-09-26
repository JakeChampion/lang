# Strict mode is the gate

`FERN_SEM_IR_STRICT=1` turns a module the typed path does not produce whole
into a compile error rather than an AST-lowered fallback. Until now it was a
measurement someone ran by hand; the suites compiled without it, so a new
refusal kept the AST lowering and nothing failed.

Three suites now hold it:

- `internal/e2eselfhost`: the package's `TestMain` sets the flag unless the
  environment already names it. The production rows' `FERN_SEM_IR_SKIP` leg
  and `TestSelfHostSemIRStrict`'s off leg clear it for their own compile,
  since they keep the AST lowering on purpose.
- `internal/coreutils`: the self-host builds of the utilities compile strict.
- `internal/e2e`'s semantic differential legs (x86-64, arm64, wasm): a seed
  that compiles must produce every declaration (`requireSemWhole`). Strict
  mode itself would not do here, since a compile failure on a fuzz seed is a
  skipped gap. The three 0.75 floors on the produced-whole fraction are gone:
  every compiling seed was already whole, and every miss was a seed that
  does not compile at all.

## Measured

- `internal/e2eselfhost` under strict, before the rebase onto #10295: 2737
  tests, one failure, the AST self-build of `copy_returned_views` (#10291,
  fixed in #10295). After the rebase the sweep hit the local 3 h timeout
  at `TestSelfHostWriterIRArm64`. No strict refusal came up in the tests
  it ran. The one failure was `wasm-tools` missing from the container.
- `TestDifferential_SelfHostSemantic{X86_64,Arm64,Wasm}`, default window:
  1527 seeds pass, 9 skip as compile gaps.
- `TestSelfHostCoreutilsParity`: every utility's self-host build compiles
  strict. Only `cksum`'s corpus failed locally, because the host's GNU
  cksum 9.4 predates `-a sha2`.
