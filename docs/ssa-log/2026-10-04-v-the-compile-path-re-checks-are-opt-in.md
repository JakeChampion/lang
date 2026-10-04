# 2026-10-04 — the compile path's re-checks are opt-in

Self-host compiler, every target. Refs #11534, #8171.

## The shape

Every compile ran verifiers that re-check what the compiler's own passes had
just produced:

- `ssaunits.verify_planned`, called from `ssarc.validate`, re-derives the
  ownership units and checks that the planner's plan agrees.
- `ssadeps.verify` checks the lifted SSA graph is well formed. It ran twice:
  once in `ssadeps.analyze`, and again over the same graph in
  `ssasem.schema_error`.
- `semrecords.verify` checks each function's record and enum tables. It ran
  once per function, 1,935 times compiling `checker.fern`.
- `irverifygate` checks the IR handed to each backend.

These find compiler bugs, not errors in the program, and together they cost
about 6% of a compile. They now run only under `FERN_IR_VERIFY`:

| `FERN_IR_VERIFY` | what runs |
|---|---|
| unset or `0` | no re-checks |
| `1` | the re-checks, plus the IR gate's coverage line on stderr, as native prints it |
| `quiet` | the re-checks, without the line |

`util.verify_on` answers for all of them. The test harness sets `quiet` for
every self-host compile: `e2eharness.SelfHostChildEnv`, the `e2eselfhost`
`TestMain`, and `--env` on the four wasm-hosted harnesses that run the
pipeline inside the guest. So the suites check what a plain compile no longer
does.

Two kinds of check still always run:

- **Capacity refusals.** `ssadeps.limits_error` refuses a graph too large for
  the dominator or live matrices.
- **Refusals of unsupported shapes.** The rest of `ssarc.validate` and of
  `ssasem.schema_error` turn an unsupported shape into a diagnostic rather
  than a miscompile.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at d2f67e60 against this branch:

| | main | opt-in re-checks |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.071 G | 18.993 G (−5.37%) |
| `ssasem.schema_error`, inclusive | 550 M | 93 M |
| `ssarc.validate`, inclusive | 388 M | 79 M |
| `irverifygate.verify_or_refuse`, inclusive | 157 M | 8 M |

The two compilers build `checker.fern` for x86-64 and arm64 and `fern.fern`
to byte-identical binaries. A build under `FERN_IR_VERIFY=quiet` is
byte-identical too: the re-checks only observe.
