# 2026-10-03 — the array box shim builds no frame

`asm_ir`'s `__fern_arr_box`, x86-64 only. Refs #8171.

## The shape

`__fern_arr_box` allocates a box for an array of a given capacity and
writes its header. It is the second most-called runtime routine on the
stage-2 compile of `checker.fern`, 27.5 M calls after `__fern_alloc`'s
36.5 M. It built a frame (`pushq %rbp; movq %rsp, %rbp … leave`) only to
keep the capacity across its call to `__fern_alloc`.

## What changed

`__fern_alloc` clobbers only `%rax` and `%rdi`, so the capacity is pushed
before the call and popped after it. That one push also aligns the call.
Three instructions go from every box. A traced build keeps the frame,
because it names allocation sites by walking the frame chain.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at 9b7dc74d with the direct bump (2026-10-03-e) applied, and
from this change on top of that:

| | with the bump | this change |
|---|--:|--:|
| stage 2, total Ir | 25.386 G | 25.298 G (−0.35%) |
| `__fern_arr_box`, inclusive Ir | 712 M | 629 M |

Emitted bytes change on 490 of the 1,965 `selfhost-emit-hashes` rows, all
x86-64.

## Witnessed

`TestSelfHostAllocFastPath` now also requires `__fern_arr_box` to reach
`__fern_alloc` and return with no `pushq %rbp`, which main's does not.
`TestSelfHostArrPush*`, `TestSelfHostRcTrace*` (the traced build keeps the
frame), `TestSelfHostSSA*`, `TestSelfHostStructCopy*` and
`TestSelfHostArray*` (737 passing, no skips), the stage-2 build and its
compile of `checker.fern`, the emit-hash sweep, the lint ratchet and
`make fmt-check`.
