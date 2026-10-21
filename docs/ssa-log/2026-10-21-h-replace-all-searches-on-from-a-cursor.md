# 2026-10-21 — replace-all searches on from a cursor

`asm_ir.ssa_replace_all` and `ssa_resolve_tails`, over `util.index_of_sub_from`.
Refs #8171.

## The shape

The x86 emitter buffers a function's body with marks where its epilogue and
tail jumps go, and fills them in once the frame is chosen. `ssa_replace_all`
re-sliced the rest of the text after each match and searched the copy, and
`ssa_resolve_tails` sliced the rest per tail mark: a copy the length of
what remained, per match.

## What changed

`util.index_of_sub_from` is the substring search from an offset, on the
same kernels; both rewrites search on from a cursor and slice only what they
emit.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the swap-pass slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 74.595 G | 74.585 G (−0.01%) |

Small: a body has few marks, so the copies were few. Kept for the shape,
which no longer scales with the body's length per mark. The whole-compiler
binary is byte-identical to the base compiler's.
