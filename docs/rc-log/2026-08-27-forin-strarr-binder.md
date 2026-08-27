# The for-in binder — it never needed a credit

*2026-08-27* — `for_in_str_elem__loop__read`, both leak-matrix arches. Part of
#5338, #7292/#7356 family.

## The cell

```fern
var names: string[] = [mkstr("a"), mkstr("b")];
for s in names { t = (t + s.len()) % 101; }
```

500 allocs / 100 frees against native's 100/100 — the array and both element
boxes surviving every round.

## The note points at the wrong value

> for-in binder is not a StmtVar, no collector credits it

`s` borrows an element that `names` owns; `names`'s own deep free is what
releases it, and a borrow needs no credit. What actually happened is that
`strarr_unsafe_for`'s `StmtFor` arm returned unsafe for the ARRAY the moment the
array was iterated:

```fern
ast.ExprIdent(fid) => { iter_unsafe = fid.name == name; },   // `for x in name`
```

so `names` lost its `"SARR:"` credit and nothing released either level.

The indexed sibling says so directly. Before this change:

| spelling | self-host |
|---|---|
| `for s in names { t = t + s.len(); }` | **500/100** |
| `while (j < names.len()) { t = t + names[j].len(); j = j+1; }` | 500/500 clean |

Two spellings of one read, and only the `for` one leaked. `strarr_expr_unsafe`
already draws the transient-versus-lasting line for `names[j]` in exactly those
positions; the `for` arm drew it at the whole loop instead.

## The arm asks the same question of the binder

`body_unsafe_for` over the loop body decides whether `s` outlives an iteration.
A bind, a return, a container or struct store, or a call whose RESULT is bound
outward all make the loop unsafe again. A binder that shadows the array is
refused outright — the walk would otherwise read a different value under the
same spelling.

Where the line falls is worth one probe each way, because it is not about the
call, it is about where the call's result goes:

| body | verdict |
|---|---|
| `t = t + keepstr(s)` — callee returns `i32` | admitted |
| `kept = stash(s)` — callee returns the string, bound outward | refused |

## Measurements

Every row answers identically on native x86-64, `bin/fern -interp` and the
self-host, before and after.

| probe | native | self-host before | after |
|---|---|---|---|
| `forin_len_read` — the cell | 68, 100/100 | 68, **500/100** | 68, **500/500 clean** |
| `binder_scalar_call_arg` | 65, 1200/1200 | 65, 2000/1200 | 65, **2000/2000 clean** |
| `indexed_read_unchanged` | 68, 100/100 | 68, 500/500 | unchanged |
| `refused_binder_escapes_local` | 72, 1400/1400 | 72, 2400/1600 | unchanged, refused |
| `refused_binder_bound_local` | 72, 1400/1400 | 72, 2400/1600 | unchanged, refused |
| `refused_binder_into_container` | 33, 1800/1800 | 33, 2800/2000 | unchanged, refused |
| `refused_binder_returned` | 72, 1400/1400 | 72, 2400/1600 | unchanged, refused |
| `refused_call_launders_binder` | 72, 1400/1000 | 72, 2400/1600 | unchanged, refused |
| `refused_binder_shadows_array` | 65, 1200/1200 | 65, 2000/1200 | unchanged, refused |

No exit 99, no exit 100, no 139. The churn probes read their value back after
200 rounds have recycled the freelist. The flipped rows were re-run under
`FERN_SANITIZE=1` with `FERN_RC_UNDERFLOW_TRAP=1` and `FERN_RC_FREE_DEBUG=1`:
clean, no trap, no quarantine hit.

One probe kept off the suite is worth recording. `for s in names { if (s ==
mkstr("a")) { … } }` improves from 2800/1200 to 2800/2000 and stays leaking, and
its sanitizer census halves — 44800 bytes in 1600 blocks down to 22400 in 800.
The residue is the per-iteration `mkstr("a")` TEMP inside the comparison, not
the binder or the array, so it belongs to a different shape and is not pinned
here.

## The matrix rows

Two moved, both leak→clean — `for_in_str_elem__loop__read` on each arch — and
nothing else in any of the four matrices did.

## Gates

- per-module emit-all fixpoint — green, 0 skips
- `scripts/cliff-bench` — 456566 / 257574032, IDENTICAL with this diff and with
  it stashed. The move from the previously recorded 458360 / 258145264 is main's
  astwalk folding, not this change.
- the repo complexity ratchet — banked at 411 / **17572**. Baseline on main
  measured 17570 with this diff stashed, so the delta here is +2 (the two forks
  in the `StmtFor` arm); the rest of the fall from the recorded 17608 is the
  same astwalk work.
- all four matrices — the two rows re-pinned, no other row moved
- the shape-selected set (TEST-GATES rule 13, applied to the shape changed:
  files pinning leak accounting that mention `string[]` or a for-in) — 75
  files, 140 test functions
- the new suite, 9 cases

## What remains

Seven cells in `selfhost-leak-matrix.txt`: four `tuple_mixed` rows,
`opt_str__callarg__read`, and the two alias-consumed-by-match denials
(`enum_rc_payload__fnscope__alias_match`, `opt_arr__fnscope__alias_match`) whose
notes call the denial sound — read those before treating them as gaps.
