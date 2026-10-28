# 2026-10-28 — the postorder walk keeps a fixed stack

`ssa.postorder`, under `dominators` and `cold_positions`. Refs #8171.

## The shape

`postorder` walks the blocks of a function with an explicit stack. Each
pop called `drop_last_i`, which copies the whole stack less its last
entry. So a deep walk copied the stack once for each block it left.

## What changed

Each block goes on the stack at most once, because `seen` marks it. So
the stack and its `next` row are now arrays of one entry for each block,
with a `depth` count. A pop decreases `depth`.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e, with #13768.

| subject | main at 293eaa55e | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 125.239 G | 124.335 G (−0.72%) |

The whole-compiler binary is byte-identical to the base compiler's.
