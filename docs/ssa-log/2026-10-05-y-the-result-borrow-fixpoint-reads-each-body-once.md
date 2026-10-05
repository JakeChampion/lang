# 2026-10-05 — the result-borrow fixpoint reads each body once

Self-host checker, every target. Refs #11534, #8171.

## The shape

`ow_result_borrows` finds the functions whose pointer result borrows from a
parameter. It settles a fixed point over the functions returning a pointer:
after the first round, a function is examined again only if it calls one the
previous round marked. To decide that, `ow_calls_any` walked the function's
whole body once per round, looking for calls to the marked names: 6,007 body
walks compiling `checker.fern`.

Only a function in the pending set can ever be marked. So each pending body
is now walked once, by `ow_calls_to`, keeping its calls to pending names.
Each round asks whether any of those names was marked last round. The slice
escape fixpoint in `check_module_pass` already works this way
(`slc_call_names`).

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at f7a88274 against this branch. The two compilers build
`checker.fern` for x86-64 and arm64, and `fern.fern`, to byte-identical
binaries. `scripts/selfhost-emit-hashes` matches on all 2,001 rows:

| | main | calls gathered once |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 19.040 G | 19.004 G (−0.18%) |
| `ow_result_borrows`, inclusive | 174 M | 139 M |
| `ow_calls_any`, inclusive | 92.8 M | 4.6 M |
| `ow_calls_to`, inclusive | — | 51.3 M |
