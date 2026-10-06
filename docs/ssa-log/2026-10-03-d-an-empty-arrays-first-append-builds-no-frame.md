# 2026-10-03 — an empty array's first append builds no frame

`asm_ir`'s `__fern_arr_push`, x86-64 only. Refs #8171.

## The shape

`__fern_arr_push` appends in place in a frameless head when the receiver has
room and may be mutated. Every other append took the framed slow path: the
frame, four callee-saved registers, the grow test, `__fern_arr_box`, then the
element copy loop's checks. Split by instruction counts on the stage-2
compile of `checker.fern`, the slow path ran 9.98 M times:

| case | entries |
|---|--:|
| growth from capacity 0 | 5.84 M |
| growth by doubling | 3.42 M |
| copy of a shared receiver | 0.71 M |

The first row is the first append to an empty array. It copies nothing and
always grows to four slots, so the frame and the loop checks around its one
`__fern_arr_box` call were the whole of its extra cost.

## What changed

The slow path's head now takes an empty receiver with no capacity directly:
it saves the value across `__fern_arr_box(4)`, stores it, sets the length to
1 and returns. The length test matters because a static literal can hold
elements at capacity 0, and those still go through the copy.
`__fern_arr_push_owned`, which calls into the same slow path, gets it too.
The rc-debug build keeps the framed path, whose refcount read checks for
poison, and so does a traced build, which names allocation sites by the frame
chain.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at 9b7dc74d and from this change applied to it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 25.444 G | 25.283 G (−0.63%) |
| `__fern_arr_push`, inclusive Ir | 1.003 G | 842 M |

Emitted bytes change on 257 of the 1,965 `selfhost-emit-hashes` rows, all
x86-64. arm64 is unchanged.

## Witnessed

`TestSelfHostArrPushFastPath` now also requires the x86-64 slow path to
reach `__fern_arr_box` before any `pushq %rbp`, which main's does not, and
appends to a literal that already holds elements. `TestSelfHostArrPush*`,
`TestSelfHostRcTrace*` (traced builds keep the frame), `TestSelfHostSSA*`
(511 passing, no skips), the stage-2 build and its compile of
`checker.fern`, the emit-hash sweep, the lint ratchet and `make fmt-check`.

## Not taken

Routing `__memcpy` through the size-classed `__fern_memcpy` would look like
a 0.5% gain under callgrind, which counts every `rep movsb` iteration as an
instruction. It is not one: the arm64 emitter's note records that the
size-classed copy measured a 10% wall-clock loss over `bench` on
x86-64, where ERMSB is part of the baseline.
