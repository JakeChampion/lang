# 2026-09-05 — the destination of an indexed append earns its element walk

`2026-09-05-append-indexed-elem-retain.md` made `out = out.append(pre[p])` a
COUNTED store: an index read of a struct or array element takes a retain, so the
box arrives in `out` at rc 2. It closed with a residual — "balancing them means
admitting an index-sourced element into the destination's walk" — and this is
that admission, for the struct-array (STRUCTARR) class.

`structarr_elem_store_ok` admitted only a no-base struct literal or a call to a
strict-fresh producer, on a FRESHNESS argument: the element box is solely owned,
so the shallow per-element dec frees a box nothing else names. An index read is
not fresh, so `out` was refused, released shallow, and its elements stayed live.

The retain replaces that argument rather than weakening it. An index-sourced
element is admitted on COUNTED-STORE grounds — the same argument
`arrarr_row_escapes_iter` already makes for a row bind: the box is at rc 2, so
the walk's rc-guarded dec lands on 2, not 1. It frees nothing the source still
names, and reclaims once the source dies. A bare IDENT element stays refused;
nothing retains it, so it really would dangle.

## Both owners have to walk, which is what bounds the win

Two arrays name the box and each owes it one dec. The destination's is this
change. The SOURCE's is its own element walk — and a callee-returned array does
not earn one, so a source from `three()` leaves the box at rc 1, one dec short.

Measured paired at the same commit, `bin/fern -interp` as oracle (every row
answers 12 before and after):

| row | source | baseline | with the admission |
|---|---|---|---|
| `stmt_local_sameframe` | append-built here | 71 / 68, **144 live** | 71 / 71, **0** |
| `stmt_local` | `three()` | 71 / 68, 144 | 71 / 68, 144 |
| `expr_local` | `three()` | 73 / 70, 144 | 73 / 70, 144 |
| `clone_local` | `three()` | 80 / 77, 144 | 80 / 77, 144 |
| `stmt_param` | `three()` | 72 / 69, 144 | 72 / 69, 144 |
| `nested_arr` | append-built here | 71 / 68, 120 | 71 / 68, 120 |
| `enum_local` | append-built here | 71 / 68, 120 | 71 / 68, 120 |
| `str_local` | append-built here | 68 / 68, 0 | 68 / 68, 0 |

The same-frame row is the LICM pass's own shape — a prologue list built by
appends and spliced into the output — and it is the row the corpus did not have,
which is why the residual sat unnoticed behind seven rows that could not move.
It is pinned now as `stmt_local_sameframe`, with `balanced` asserting nothing is
live rather than only that frees do not exceed allocs.

**The predecessor's string row is stale.** It recorded `str_local` at 71 / 68 /
96; the paired baseline above reads 68 / 68 / 0 at HEAD, so something between
the two commits balanced it. Read that table as of its own date.

## What is left, and what is deliberately not

- **The source side.** A callee-returned struct array earns no element walk in
  the caller, which is the four `three()` rows above. That is a producer-registry
  question ("STRUCTARRF:"), not an element-store one.
- **`nested_arr`.** The ARRARR class has the same shape at
  `arrarr_row_store_ok`, and the retain covers array elements too, so the
  argument transfers — except that its string-kind rows are graded by
  `arrarr_row_store_strings_fresh`, and an index read cannot answer freshness.
  An index-sourced row can only be admitted at the lax grade; that is a separate
  judgement and a separate change.
- **`enum_local` must NOT be admitted.** `indexed_box_elem_escapes` excludes
  enum elements, so the store stays uncounted — one owner, one dec. Crediting
  the destination there is a double free, not a leak fix. The boundary of this
  change is exactly the set the retain covers.
