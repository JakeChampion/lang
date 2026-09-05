# 2026-09-05 — the ARRARR half of the indexed-append admission

`2026-09-05-append-indexed-elem-destination-walk.md` admitted an index-sourced
element into the STRUCTARR destination's element walk and left the sibling
class open, on the grounds that "its string-kind rows are graded by
`arrarr_row_store_strings_fresh`, and an index read cannot answer freshness …
a separate judgement and a separate change". This is that change, and the
judgement turned out to be structural rather than something to encode.

`arrarr_row_store_ok` admitted only `ast.ExprArray(_)` — a row LITERAL. An
index read (`out = out.append(pre[p])` on an `i32[][]`) was refused, so `out`
was released shallow and its rows stayed live.

The retain covers array elements as well as struct ones
(`indexed_box_elem_escapes` asks `expr_is_arr_src` / `_strarr` / `_f64arr` /
`_i64arr`), so the row arrives at rc 2 and `__fern_arrarr_free`'s rc-guarded
per-element dec lands on 2, not 1. Same counted-store argument as the struct
half.

## The string-kind grading answers itself

`arrarr_self_append_row` returns 1 for a strict row, 0 for a lax one, and the
strict grade is exactly `arrarr_row_store_strings_fresh` — which matches
`ast.ExprArray` alone. An index read therefore cannot reach the strict grade
no matter what: it takes 0, and a string-kind slot needs the strict
`ARRARRS:` credit before it will free element pointers. No extra guard was
needed, and none was added.

Measured paired against the same commit, `bin/fern -interp` as oracle:

| program | main | with the arm | oracle |
|---|---|---|---|
| `i32[][]`, source appended in this frame | 7 / 4, **120 live** | 7 / 7, **0** | 12 |
| `string[][]`, same shape | 7 / 4, 96 | 7 / 4, **96** | 111 |

The string row is the control: it must not move, and it does not.

Across the `TestSelfHostAppendIndexedElem` corpus, one row moves — `nested_arr`,
120 -> 0 — and it is pinned `balanced` now rather than only "frees do not
exceed allocs". `enum_local` stays at 120 by design: its store is UNCOUNTED
(`indexed_box_elem_escapes` excludes enum elements), so crediting its
destination would be a double free. The four rows sourcing from `three()` stay
at 144: a callee-returned array earns no element walk in the caller, which is
the producer-registry gap the predecessor already records and neither change
touches.

## Still open

- **The source side**, unchanged from the predecessor: a callee-returned array
  earns no walk in the caller ("STRUCTARRF:" / "ARC:" producer registries).
  That is what keeps the four `three()` rows leaking.
- **String-kind rows from an index read.** Admissible only if something can
  answer freshness for the row's elements, which an index read cannot. The lax
  grade refusing them is the correct conservative answer, not a gap to close
  by widening this arm.
