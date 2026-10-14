# 2026-10-13 — a box-saving splice has a ceiling

`compiler/seminline.fern`: `max_box_saving_caller_insts`, `splice_calls`,
`splice_round`. Against 2026-10-13-e (a box-saving splice is not held to the
caller's budget) and 2026-10-13-g (a once-called box is saved past the budget
too).

## The failure

Since 8f67dd8, every compile that takes `asm_ir` as a module of its own
program refused `asm_ir.ssa_inst`:
`FERN_SEM_IR: asm_ir__ssa_inst: live matrix overflow`.
`TestSelfHostStage2Bootstrap`, `TestSelfHostStage2Compiler` and
`TestSelfHostWasmHostedX86CompilerMatchesNative` failed on main (#12338).
Bisected over the merges: `fb02233` passes, `8f67dd8` fails.

## The cause

`ssa_inst` threads its emit state through hundreds of emitters, each
returning the state record. In those programs most of them are called from
`ssa_inst` alone or from two places, so each one's splice saves a box, and
2026-10-13-e and -g let such a splice through at any caller size. Nothing
bounded the sum. Each round spliced up to `max_splices` of them into
`ssa_inst`, for up to sixteen rounds, until blocks × values passed what
`ssalive` and `ssadeps` hold (`limits_error`).

## The fix

A box-saving splice is still exempt from `max_caller_insts` (1,500), but it
may grow the caller only to `max_box_saving_caller_insts` (6,000).
`splice_round` walks a caller past the first budget only while it is under
the second. The callers the exemption was made for stay well under it: the
serve loop ends at 1,685 instructions and `apart_long` at about 2,400.

## Witnessed

The three tests above pass again. `TestSelfHostSharedSplicePastCallerBudget`,
`TestSelfHostSharedConstructions` and `TestSelfHostSemanticInline` (each with
its `Off` twin) are unchanged.
