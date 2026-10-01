# 2026-10-01 — a dyn or a map merged past its source is copied (#10878)

Self-host (`ssasem.copy_refusal`, `ssasem.deep_copy`, `ssasem.merge_copies`,
`ssasem.bytes_roots`, `ssasem.borrow_parents`).

`2026-09-30-h` rebuilds a call's result that reaches a join past its source.
`copyable` answered false for a dyn holding a view, a function value holding a
view, a cell and a map, and `held_copies` then made no copy and said nothing.
The verifier reported `dependency unavailable at use` and the function fell to
the AST lowering, or failed the compile under `FERN_SEM_IR_STRICT`.

## Fix

- **dyn.** `deep_copy` tests the box for each concrete holding a view
  (`dyn_is`), and on a hit narrows it (`dyn_as`), copies the concrete and
  widens it back (`dyn_up`). A box that is none of them holds no view: it is
  kept, retagged by a `dyn_as` naming no shape. The verifier admits that form
  only where tests refuted every concrete holding a view (`guarded`), and it
  anchors nothing (`bytes_roots`). A concrete that no test can name (a generic
  record) is refused.
- **map.** Rebuilt in a loop over its two column snapshots (`map_keys`,
  `map_values`), each key and value copied and inserted into a fresh map.
  The loop scaffolding is shared with the array copy (`loop_enter`,
  `loop_leave`).
- **A dyn widened from a box holding no view** anchors nothing either. A dyn
  merged from a branch that built such a record was refused as reading the
  record.
- **No silent skip.** `merge_copies` returns a `MergePlan` whose `why` names
  the kind it cannot copy, and `anchor_module` refuses with it: `a value merged
  past its source has no copy: ...`.

Three kinds stay refused:

- a **cell**: shared storage, which a copy would split, so a write through a
  closure would no longer reach the merged value. No source program reaches
  it: the checker keeps a reference-typed capture read-only (E049), so no
  cell holds a view;
- a **function value**: the typed graph has no op that tests which
  environment a function value carries, so nothing can find the views to
  copy. Capture of a view is an escape position STR-VIEW-CONTRACT.md §3 step 3
  assigns to the checker (#8635);
- a **type that holds itself** (`enum L { Cons(str, L), Nil }`): its copy
  would recurse, which a splice cannot express. `copyable` recursed into the
  same type without end, so the compiler crashed with a segfault on such a
  merge; it is now refused by name.

## A map read was anchored to nothing

Found while writing the map rows. `get`, `get_or` and `values()` on a map
holding views hand back a unit of the value, but its views read the entry's
bytes. Nothing anchored the result to the map, so the map, and the source the
map was anchored to, could be released while the value was still read.
`m = index(mk(3)); v = m.get_or(2, "-")` printed `2;z0 ` where the answer is
`2;bcc`. The read is now anchored to the map, and `get_or`'s to its default
too (`map_read_sources`), in `borrow_parents`, `bytes_roots` and
`view_sources`.

## Measured (x86-64 sanitize, allocs / frees)

| program | before | after |
|---|---|---|
| dyn holding a view, merged from a branch | refused | 325 / 325 |
| dyn over 5 concretes, merged in a loop | refused | 1085 / 1085 |
| dyn widened from a viewless branch local | refused | 319 / 319 |
| `Map[i32, str]` a call returns, branch local | refused | 355 / 355 |
| `Map[string, P]`, `P` holding a view, in a loop | refused | 391 / 391 |
| `get_or` on a view map past the source's last use | wrong answer | 168 / 168 |
| `enum L { Cons(str, L), Nil }` merged | compiler segfault | refused |

Each produced program answers as the AST lowering does on x86-64, arm64 and
wasm.

## Tests

`TestSelfHostSemanticProduction`:

- `a-dyn-holding-a-view-merged-past-its-source-is-copied`
- `a-dyn-over-several-boxes-merged-in-a-loop-is-copied`
- `a-dyn-widened-from-a-viewless-branch-local-is-produced`
- `a-view-map-a-call-returns-from-a-branch-local-is-copied`
- `a-map-of-view-records-a-call-returns-in-a-loop-is-copied`
- `a-view-map-value-read-keeps-the-map-alive`

`TestSelfHostSemIRStrict`: the dyn case no longer refuses. The closure in a
record and the recursive enum are refused naming the kind.
