# 2026-10-03 — arr_inc_elems retains each element inline

`asm_ir`'s `__fern_arr_inc_elems`, x86-64 only. Refs #8171.

## The shape

Before a pointer-element buffer is copied, `__fern_arr_inc_elems` gives
each element one more count, so that each copy owns its elements. It did
so by pushing each element and calling `__fn___fern_rc_inc` through its
stack entry, holding the array, length and index in three callee-saved
registers across the calls. On the stage-2 compile of `checker.fern` that
was 14.7 M of `rc_inc`'s 16.1 M stack-entry calls. Each one cost a push,
the call, `rc_inc` reloading its argument from the stack twice, the
return and the stack fixup.

## What changed

The loop applies `rc_inc`'s guards to each element inline: skip a tagged
or low pointer, strip a byte view's tag, skip a static box's negative
count, otherwise add one to the count in memory. With no call left, the
helper needs no saved registers. `%rsi` / `%rdx` hold the length and
index and `%rcx` / `%edi` the element and its count, all of which a
caller of the helper already treats as clobbered.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at 31695b8d with the low-address change (2026-10-03-i)
applied, and with this change on top:

| | with the low-address change | this change |
|---|--:|--:|
| stage 2, total Ir | 24.461 G | 24.357 G (−0.42%) |
| `__fern_arr_inc_elems`, inclusive Ir | 313 M | 209 M |

Emitted bytes change on 108 of the 1,965 `selfhost-emit-hashes` rows, all
x86-64.

## Witnessed

`TestSelfHostArrIncElemsInline` is new. A program pushes onto a string
array another binding still holds, whose elements are heap strings and
static literals. The listing must call the helper, and the helper's loop
must hold no call or push. Both arrays must read back their elements, no
count may underflow, and the program must exit 42 on every host target.
Also run: `TestSelfHostSemanticProduction`,
`TestSelfHostSemanticAllocationCounts`, `TestSelfHostSpreadCarryElems*`,
`TestSelfHostSSAPhysicalRC*`, `TestSelfHostStrElemHandoff*`,
`TestSelfHostFieldAppendInPlace*`, `TestSelfHostRcRuntime*`,
`TestSelfHostRcTrace*` and `TestSelfHostArrPush*`.
