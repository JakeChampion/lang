# 2026-10-03 — a spill-free leaf builds no frame

`asm_ir`'s `emit_ssa_function_x86`, x86-64 only. Refs #8171.

## The shape

Every function the x86 register path emitted built an `%rbp` frame:
`pushq %rbp; movq %rsp, %rbp`, its saved registers, an alignment
`subq` when their count was odd, and `leaq; pops; popq %rbp` or `leave`
before each return. A function with no spill slots and no string views
addresses nothing through `%rbp`. If it makes no call, `%rsp` needs no
alignment either. 753 such leaves in an earlier stage-2 build cost 240 M
Ir of frame instructions on the compile of `checker.fern`, among them
`util.hash_bucket`, which builds its frame before the `n <= 1` early
return.

## What changed

A function with no spills, no views and no call that is not inlined is
emitted into a buffer, with a placeholder where each epilogue goes. If no
instruction in the buffered text calls, pushes, pops or names `%rbp` or
`%rsp`, the function is written with only its saved registers pushed. The
canonical frame address follows `%rsp`, each push describes its register,
and each epilogue pops in reverse inside `.cfi_remember_state` /
`.cfi_restore_state`. Otherwise the text is written behind the usual
frame. The check reads instructions, not labels, because every label
carries the function's name. An inlined release that calls its helper on
the free path is caught there.

A traced build (`FERN_RC_TRACE`, `FERN_LEAKCHECK`) keeps every frame,
because it names allocation sites by walking the frame chain.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at 31695b8d and from this change applied to it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 24.577 G | 24.429 G (−0.60%) |
| `util.hash_bucket`, self Ir | 487 M | 470 M |
| stage-2 binary | 12,112,520 B | 12,097,776 B |

The net figure includes the compiler's own cost of buffering each
candidate's text. Emitted bytes change on 393 of the 1,965
`selfhost-emit-hashes` rows, all x86-64. Both sides refuse the same 252.

## Witnessed

`TestSelfHostLeafFrame` is new. `mix` loops and returns early. `wide`
keeps enough values live to use three callee-saved registers without
spilling, and must push them with `.cfi_def_cfa_offset` against `%rsp`.
Neither may name `%rbp` or `leave`. `outer` calls both and keeps its frame.
All three must compute Go's values on every host target. GNU `as` accepts
the listing, and its decoded frame records step the CFA offset with each
push and restore it after each return. Also run: `TestSelfHostCfi*`,
`TestSelfHostELF*`, `TestSelfHostSSA*`, `TestSelfHostX86Gas*`,
`TestSelfHostSemanticProduction`, `TestSelfHostRcTrace*` and
`TestSelfHostBoundsElide*`.
