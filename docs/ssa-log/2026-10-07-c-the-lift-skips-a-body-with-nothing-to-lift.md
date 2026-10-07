# 2026-10-07 — the lift skips a body with nothing to lift

`lift.lift_lambdas_view_typed`, the lambda lift every module goes through
before lowering. Refs #8171. No emitted byte changes.

## What changed

The lift's worklist ran each function through seven passes:

- the lambda-return desugar;
- the sole-IIFE unwrap;
- the two capture-boxing passes;
- the inline-closure lift;
- the closure lift;
- `lift_stmts`.

Each rebuilt the whole body. On a `checker.fern` compile that came to 567 M
inclusive (3.7%) over 1,896 functions. The inline-closure walk alone was
195 M.

Two shapes give the passes anything to do:

- a lambda;
- a module function named as a value. A struct field, an array element, an
  argument or a local bound to one takes a `$wrap` trampoline.

`needs_lift` looks for either in one read-only walk. Every lambda counts one.
Every identifier naming a module function counts one. Every call whose callee
is such a name counts minus one, cancelling its own callee. A body whose count
stays at zero goes to the output as it is.

A test of lambdas alone is not enough. Stage 3 then refused 551 functions
whose only closure was a named function passed as a value, such as
`astwalk.descend_all` handed to `fold_stmt_pruned`. The new
`fn-names-without-a-lambda` case in `TestSelfHostCaptureFreeFnValue` pins
that boundary. It fails on all three targets when only lambdas count.

## Measured

`checker.fern` (at 2e084b79) built for x86-64-linux under callgrind by
production compilers, no `-g`, each side's stage 3 built by its own stage 2.
The baseline is main at f009e7a36 with the #11799 fix (e0ba0b691). Both stage
3s rebuild themselves byte for byte. The two compile `checker.fern` for
x86-64, arm64 and wasm, and `fern.fern` for x86-64, to the same bytes.

| | before | this change |
|---|--:|--:|
| total Ir | 15.433 G | 15.175 G (−1.67%) |
| stage 3 size | 10,954,584 | 10,956,352 |
