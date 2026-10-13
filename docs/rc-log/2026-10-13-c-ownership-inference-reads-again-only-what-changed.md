# 2026-10-13 — ownership inference reads again only what changed

`semsource.infer_rows`, new `row_readers`. Refs #8171. No emitted byte changes.

## The shape

Ownership inference runs to a fixpoint over every produced function. Each
round read every function's graph again, `ownership.escaping` and
`ownership.lends` over all of its instructions, until a whole round changed
no row. Most rounds changed a handful of rows, and every function was read
again for them.

## What changed

A function's answer reads only the rows its instructions name: the callees
of its calls and of the slot phis tail recursion leaves. `row_readers` lists,
per row, the functions that name it. The rounds become a worklist that starts
with every function and, when a row changes, queues the functions that read
it.

A row's bits only fall and its `lends` only rises, so the answer does not
depend on the order the work is taken in. A cap equal to the old round limit
stays in place.

## Measured

The whole compiler under callgrind, with both compilers built from the same
tree and compiling the same frozen copy of main, each on a warm memo.

| | Ir |
|---|--:|
| main at fef3701 | 69.956 G |
| a worklist | 69.445 G (−0.73%) |

`semlower.inferred_rows`, into which the inference is spliced, falls by
288 M and the `ownership` module by 138 M.

The changed compiler compiles `fern.fern` to the same bytes as main's for
x86-64 and arm64, and `checker.fern` for wasm. It also rebuilds itself byte
for byte.
