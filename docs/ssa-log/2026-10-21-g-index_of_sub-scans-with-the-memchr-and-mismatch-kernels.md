# 2026-10-21 — index_of_sub scans with the memchr and mismatch kernels

`util.index_of_sub`, under `util.contains`, `flatten.is_stdlib_ident`,
`asm_ir.ssa_replace_all`, the tree shaker and the checker's type
substitution. Refs #8171.

## The shape

The compiler's substring search compared byte by byte from every offset of
the haystack, in Fern: 362 M instructions on the whole compiler's
self-compile, seven times the `checker.fern` figure, most of it under three
callers that search long texts (a module identity for `/std/`, an assembly
text for a mark to replace, a type spelling for a bracket).

## What changed

The search finds the needle's first byte with `__memchr`, the vector scan
every backend serves, and compares the rest with `__mismatch`, as
`std/string`'s find does; the answer is the same first offset.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the variant-index slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 75.087 G | 74.862 G (−0.30%) |

The whole-compiler binary is byte-identical to the base compiler's.
