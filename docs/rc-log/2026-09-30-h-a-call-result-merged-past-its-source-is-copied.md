# 2026-09-30 — a call's result merged past its source is copied (#10815)

Self-host (`ssasem.deep_copy`, reached from `ssasem.merge_copies` and
`ssasem.with_merge_copies`).

`2026-09-30-g` copies a view that reaches a join past its source, and the
views of a container the function built there. A container a **call** returns
was still refused: `a = heads(s)`, with `s` declared in the branch and
`heads` anchored to it, reaches the join holding views only that path's `s`
keeps alive, and the caller has no construction to copy them into.

Such an operand is now rebuilt whole where the copy goes: on the phi's edge, or
just before the instruction that takes it. An array is copied in a loop spliced
into the graph (an empty array, then each element copied and appended), a tuple
element by element, and an Option, Result or enum by a test per variant, the
last known by exclusion, each rebuilt with its fields copied. Every view inside
is copied as a `str` operand is. The spliced blocks go right after the block
they split, and each successor of that block now names the block the terminator
moved to.

Two alternatives were rejected:

- Keeping the source alive past the join with a companion phi allocates
  nothing, but a phi keeps one source alive. A result that accumulates views
  across loop rounds (`a = both(a, s)`) reads every earlier round's source.
- A call handing back counted strings copies for every caller of an anchored
  function, including the callers that anchor it correctly, or needs a copying
  clone of the callee per call site, which adds a body the AST module does not
  declare.

A cell is shared storage a copy would split. A map holding a view is not
rebuilt either: the typed lowering refuses to read a view out of a map column
("a read of a view map value would share the column's view box"), and a copy
would have to read one. A call's result holding either is still refused
(`ssasem.copyable`).

## Measured (x86-64 sanitize, allocs / frees)

| program | before | after |
|---|---|---|
| `a = heads(s)`, `str[]`, branch-local `s` | refused | 26 / 26 |
| `p = split2(s)`, `(str, str)` | refused | 26 / 26 |
| `o = first(s, i)`, `Option[str]`, loop-body `s` | refused | 55 / 55 |
| `g = grid(s)`, `str[][]` | refused | 41 / 41 |
| `a = both(a, s)` in a loop | refused | 45 / 45 |
| `b = heads(s)` in a loop, `s` above it | 24 / 24 | 24 / 24 |

The allocation parity program (below) allocates 36 on the typed path and 37 on
the AST lowering. With the dominance test removed from the pass, its call alone
went from 24 allocations to 39.

## Tests

`TestSelfHostSemanticProduction`:

- `a-str-array-a-call-returns-from-a-branch-local-is-produced`
- `a-tuple-a-call-returns-from-a-branch-local-is-produced`
- `an-option-a-call-returns-from-a-loop-body-local-is-produced`
- `a-nested-array-a-call-returns-from-a-branch-local-is-produced`
- `an-array-a-call-extends-with-a-loop-body-local-is-produced`
- `a-str-array-a-call-returns-from-a-dominating-local-is-produced`

`TestSelfHostSemanticAllocationParity`:
`a-view-of-a-dominating-source-is-not-copied` now also merges a call's result.
