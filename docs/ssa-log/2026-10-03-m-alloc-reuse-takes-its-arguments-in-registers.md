# 2026-10-03 — alloc_reuse takes its arguments in registers

`asm_ir`'s `__fern_alloc_reuse` and `ssa_reg_helper`, x86-64 only. Refs
#8171.

## The shape

`__fern_alloc_reuse(token, slots)` hands a dying box back to a
construction of the same size, or frees it and allocates fresh. It had
only a stack entry, so each of its 11.1 M calls on the stage-2 compile of
`checker.fern` pushed both arguments, loaded them back in the helper, and
popped them in the caller. It was the second most-called helper still
taking its arguments on the stack, after `rc_inc` from
`arr_inc_elems` (since inlined, 2026-10-03-l).

## What changed

The helper gains a register entry, `__fn___fern_alloc_reuse.r`, with the
token in `%rax` and the slots in `%rsi`. Those are the first two pool
registers, where `ssa_helper_call` puts a helper's arguments. The stack
entry loads both and falls into it. The mismatch path keeps the slots on
the stack across the donor's free, whose event hook and size class may
call. `ssa_reg_helper` lists the helper, and the entry module exports the
new label for the per-module link.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at 31695b8d and from this change applied to it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 24.577 G | 24.538 G (−0.16%) |

Emitted bytes change on 490 of the 1,965 `selfhost-emit-hashes` rows, all
x86-64: every program carries the helper.

## Witnessed

`TestSelfHostAllocReuseRegisterEntry` is new. A record rebuilt from
itself in a loop must call the reuse only through its register entry, the
stack entry must load the token and slots and fall into it, and the
program must exit 42 on every host target. Also run:
`TestSelfHostSemanticReuse*` (whose allocation counts and leak balance
cover the reuse and mismatch paths), `TestSelfHostReuse*`,
`TestSelfHostRcPreciseDrop*`, `TestSelfHostSemanticAllocationCounts`,
`TestSelfHostSemanticProduction`, `TestSelfHostRcTrace*` and
`TestSelfHostSanitize*`.
