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
element by element, a declared record field by field, and an Option, Result or
enum by a test per variant, the last known by exclusion, each rebuilt with its
fields copied. Every view inside is copied as a `str` operand is. The spliced blocks go right after the block
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
would have to read one. A dyn or a function value holding a view is a box
this cannot rebuild. A call's result holding any of these is still refused
(`ssasem.copyable`). A closure capturing a bare view is refused where it is
built ("closure capture type"), but one reaching a view through a captured
record is built and holds that view.

Whether a value holds a view is `ssasem.holds_view`. Before this, the
predicate read only a type's structure and a union's type arguments, so a
declared enum or record with a `str` payload, or a dyn over one, counted as
holding none. Nothing anchored a call's result of such a type to the argument
whose bytes it reads, and no copy was made at a merge. Each declared record
and union now carries the answer, worked out once per module as a fixpoint
over the declarations (`semsource.view_types`). A dyn holds one when a type
implementing its traits does, and a function type when a closure of that type
captures one. Without the function arm, a record holding a closure over a
view-holding record counted as holding no view: merged past its source it
answered 553 where the interpreter answers 313, and it is refused now. A closure
over a view-holding record returned past its source was already refused, since
`holds_view` reads the frame's own closure environments (`Func.envs`).

The function answer is per module and per whole `type_key`, so one closure that
captures a view marks every record with a field of that function type,
whatever that field's own closures capture. That is the trade records make by
name. The key is a conservative identity rather than an exact one: `type_key`
collapses some leaf types, so distinct function types can share the answer,
only ever toward holding a view.

The same predicate decides which self-tail
arguments cross a jump (`ssasem.crosses_jump`), so a recursive argument that
holds views is now declined: a walk down a cons list of `str` payloads keeps a
frame per cell.

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
- `a-three-variant-enum-a-call-returns-from-a-branch-local-is-produced`
- `a-declared-enum-of-views-keeps-its-source-alive`
- `a-record-a-call-returns-from-a-branch-local-is-produced`
- `a-dyn-holding-a-view-keeps-its-source-alive`

`TestSelfHostSemIRStrict`: each of these is refused:

- a dyn holding a view merged past its source;
- a closure capturing a bare view;
- a record holding a closure over a view-holding record, merged past its
  source (compiled whole before the function arm);
- a closure over a view-holding record returned past its source (already
  refused through `Func.envs`; pinned alongside).

`TestSelfHostSemanticTrmc`: `count` over a cons list of `str` payloads keeps
its self-call.

`TestSelfHostSemanticAllocationParity`:
`a-view-of-a-dominating-source-is-not-copied` now also merges a call's result.
