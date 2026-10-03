# 2026-10-03 — a small freelist miss bumps without a frame

`asm_ir`'s `__fern_alloc`, x86-64 only. Refs #8171.

## The shape

A request of at most 256 words is popped from its class's freelist in a
frameless head (2026-10-02-d). A miss went back to the framed path: the
frame, `%rcx` and `%rdx` saved, the class computed and the freelist read
again, then the bump. On the stage-2 compile of `checker.fern`, per
instruction counts:

| `__fern_alloc` path | entries |
|---|--:|
| all calls | 36.5 M |
| small freelist hit (frameless) | 33.2 M |
| framed path | 3.29 M |
| of which: bump | 3.10 M |

Below 257 words a class is its word count, so on a small miss the rounded
request is already the size to bump.

## What changed

A small miss now bumps the arena in the head, with `%rax` and `%rdi`
alone: load the heap pointer, advance it by the rounded size, check the
end, store it, return. Only the first allocation, before the arena is
mapped, still takes the framed path. Arena exhaustion traps with exit 125,
as before.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at 9b7dc74d and from this change applied to it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 25.444 G | 25.386 G (−0.23%) |
| `__fern_alloc`, inclusive Ir | 526 M | 467 M |

Emitted bytes change on 490 of the 1,965 `selfhost-emit-hashes` rows, all
x86-64.

## Witnessed

`TestSelfHostAllocFastPath`, new: `__fern_alloc`'s listing must reach both
the freelist pop and the bump, each followed by a return, before any
`pushq %rbp`. Main fails the bump half. The freelist half pins
2026-10-02-d, which shipped without a test. A program allocating three
sizes runs on every host target. `TestSelfHostArrPush*`,
`TestSelfHostRcTrace*` (a traced build wraps the allocator, unchanged),
`TestSelfHostSSA*`, `TestSelfHostMapIterReclaim*` (522 passing, no skips),
the stage-2 build and its compile of `checker.fern`, the emit-hash sweep,
the lint ratchet and `make fmt-check`.
