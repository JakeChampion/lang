# A tuple walks the record array it holds

In the AST lowering (`FERN_SEM_IR=`), a tuple holding a record's struct-array
or enum-array field read leaked the array's elements (#10326):

```fern
function mk(k: i32): (i32, Pt[]) {
    let r: Bag = Bag { n: k, pts: [Pt { x: k, tag: [k, k] }, Pt { x: 2, tag: [2] }] };
    return (r.n, r.pts);
}
```

The tuple literal retains the field read
(`2026-09-26-zb-a-tuple-literal-counts-the-field-it-holds.md`), so the record's
drop finds the buffer shared and declines its element walk. The tuple's `a`
release was one buffer dec, which took the buffer to zero and stranded every
element box and its fields. With a semantic callee, `ssarc.tuple_flags` wrote
`0` for the position, so the AST caller released nothing at all.

## The kind

A new element kind, `b`, releases an array of struct or enum boxes the way a
record's drop releases a field of that type (`asmcore.struct_drop_field_ops`):
the element walk the type admits, under the buffer's and each element's
uniqueness, then the element boxes and the buffer. `emit_box_elem_array_release`
emits it:
- a struct element that `nested_field_deep_drop_ok` admits takes
  `emit_arrstruct_deep_free`;
- an enum element that `enum_arr_elems_walk_ok` admits takes
  `emit_arrenum_deep_free`, whose payload walk is now gated on each element's
  uniqueness when the caller passes `shared`;
- any other struct or enum element takes `__fern_arrarr_free`, the box decs
  alone;
- a tag that names no struct or enum array falls back to the `a` buffer dec.

The element type comes from the tuple slot's tag, so the kinds string stays one
character per position. `is_box_elem_array_type` is the one predicate, asked of
the tag.

The two alphabets now read:

| string | alphabet |
|---|---|
| a tuple local's element kinds | `.` `a` `s` `t` `S` `b` |
| a returned tuple's `ARRF:` flags | `0` `1` `2` `3` (`3` maps to `b`, `arrf_flag_kind`) |

The readers, which must move together when either alphabet grows:
- `emit_tup_elem_releases` runs `b` through `emit_box_elem_array_release`.
- `tup_kinds_rebind_safe` accepts `b` like `a` and `S`.
- The literal's kinds record `b` for an `a` share whose slot tag is a struct or
  enum array.
- `mark_tuple_elem_binding` maps flags through `arrf_flag_kind`.
- The discarded-call `ARRF:` release in `lower_stmt_expr` releases a `3`
  position with the tag from `tuple_ret_type`.
- `tuple_ret_arrfree_flags` writes `3` where every return holds a retained field
  read of a struct or enum array. It now takes the struct table.
- `ssarc.tuple_flags` writes `3` for an array of a non-generic record or enum.
- `returned_moved_arr_slots` is unchanged: it keys on `tuple_field_share_kind`,
  which still answers `a` for these fields.

## The bug under it

Walking the tuple's elements exposed an older over-release: an array literal
stored a borrowed struct or enum parameter uncounted, and so did an indexed box
(`[ps[0]]`). On main, without any tuple, the record's own field drop already
freed the caller's box:

| shape (AST lowering, x86-64) | main | this change |
|---|---|---|
| `Bag { pts: [p, …] }`, `p` a struct param, read back by the caller | answers 9 (want 8), sanitizer use-after-free | 8, 22 / 22 |
| `Flags { fs: [f, …] }`, `f` an enum param | answers 0 (want 6) | 6, 21 / 19 |
| `xs = xs.append(p)`, then a record | answers 9, use-after-free | 8, 23 / 23 |
| `Bag { pts: [ps[0], …] }` | answers 9 | 8, 23 / 21 |

The array literal and the self-append now ask `stored_elem_is_borrow`, the
question the clone-append already asked. `struct_alias_ident_escapes` became
`box_param_ident_escapes` and covers an enum parameter too. A directly returned
literal (`return [e]`) still hands a parameter back uncounted, as the tuple
literal does (`lower_expr_array`'s `returning`). Its callers release the result
shallowly.

Counting the store makes it a counted use of the parameter, so the
counted-parameter tiers credit it: the array-literal element for `PCNT:` and
`ECNT:` (except in a returned literal), and the append for `ECNT:` as well as
`PCNT:`. `stash_fresh_struct_arg`
now checks `ECNT:`, so the caller releases a temporary enum argument after the
call. At an `ECNT:` position that includes a payload-carrying
constructor temp passed to a method or to a callee returning a struct
(`h.mk(Flag.On([i]))`): the credit already proves the callee only stores it
counted, so `fresh_enum_arg_type`'s free-function, `i32`-result restriction
does not apply there. A temporary passed to such a callee balances on every lowering (33 / 33).
On main it balanced only because the callee freed it, and it leaked 33 / 27 with
a semantic callee.

The remaining leaks in that table are main's. It never releases an enum local,
or a struct array, that it passed to the callee, and it leaks the same bytes
whenever the callee is semantic.

One trade is new. An enum parameter appended into an array the callee returns
is now counted, but the AST caller releases a returned `E[]` without an
element walk (the #9187 floor). The element box then leaks, 100 per 100 calls:

| callee | main | this change |
|---|---|---|
| `return xs.append(e)` | 300 / 200 | 300 / 100 |
| `xs = xs.append(e); return xs` | 300 / 300 | 300 / 200 |

Both leak the same way on main when the callee is semantic, and the struct
versions leak on main too. Leaving the enum append uncounted would make a
tuple-held, append-built enum array a use-after-free instead.

## Measured

Allocs / frees on x86-64, for the issue's rows (`TestSelfHostTupleFieldShare`):

| lowering | `Pt[]` main | `Pt[]` now | `Flag[]` main | `Flag[]` now |
|---|---|---|---|---|
| semantic | 30 / 30 | 30 / 30 | 28 / 28 | 28 / 28 |
| AST, and AST callee | 30 / 22 | 30 / 30 | 28 / 22 | 28 / 28 |
| AST main, semantic callee | 30 / 20 | 30 / 30 | 28 / 20 | 28 / 28 |

The test's pins are gone; every row balances on x86-64 (the sanitizer too),
arm64 and wasm. Three rows are new:
- `local_structarr_outlives_record` and `local_enumarr_outlives_record` bind the
  tuple locally. On main these leaked 27 / 23 and 25 / 22.
- `callee_local_structarr_caller_elem` puts the caller's own struct in the
  array.

`TestSelfHostArrlitBorrowedElem` covers the table above, on four lowerings and
native.

A stage-2 self-compile (the stage-1 compiler builds `fern.fern`, and the result
emits main's `fern.fern` as asm) stays an emit-level fixpoint on both lowerings.
Its peak RSS on x86-64:

| stage 2 lowered with | main | this change |
|---|---|---|
| the default lowering | 4.37 GB | 4.31 GB |
| `FERN_SEM_IR=` (AST) | 9.48 GB | 9.85 GB |

The AST figure grows 4%, from counted stores whose caller never releases the
container (the #9187 floor above).

## Still leaking

An unannotated `let p = (r.n, r.pts)` is not credited `TUP:`
(`tuple_ann_admits_fresh_mixed` needs the annotation). It leaks the same
248 bytes as on main.
