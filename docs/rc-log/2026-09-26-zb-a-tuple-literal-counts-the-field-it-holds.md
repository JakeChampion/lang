# A tuple literal counts the record field it holds

In the AST lowering (`FERN_SEM_IR=`), a tuple literal holding a record's array
field stored the field read with no count (#10203, #10314):

```fern
function pair(r: Rec): (i32, string[]) { return (r.n, r.xs); }
```

The effect depended on the field's type:
- **`string[]` field.** `strarrfld_scan` marked `Rec.xs`, which withheld every
  `Rec`'s deep drop: 40 bytes leaked per call (3 allocs, 2 frees).
- **`i32[]` field.** It has no such mark, so the record's release freed a buffer
  the tuple still read. Once a same-size array reused the block, the program
  answered 13 instead of 9. `FERN_SANITIZE` reads it as clean, because its
  quarantine stops the reuse.

## The protocol

`tuple_field_share_kind` is the one decision point. It is a function of the
element node and its checker stamp (`fa.ty`) alone: `a` for an array field
read, `S` for a `string[]` one, `""` otherwise. The tuple literal retains
exactly those elements. Two strings carry the release, and each gained a value:

| string | alphabet | new value |
|---|---|---|
| a tuple local's element kinds (`tup_elem_kinds`) | `.` `a` `s` `t` `S` | `S`: `__fern_str_arr_free` |
| a returned tuple's `ARRF:` flags | `0` `1` `2` | `2`: every return holds a retained `string[]` field read |

These readers must move together when either alphabet grows:
- `emit_tup_elem_releases`;
- `tup_kinds_rebind_safe`, which accepts `S` like `a`;
- `mark_tuple_elem_binding`, the bound-result `ARRF:` reader;
- the discarded-call `ARRF:` release in `lower_stmt_inner`;
- `ssarc.tuple_flags`, which writes `2` for a produced callee's `string[]` position;
- `returned_moved_arr_slots`, whose tuple arm skips a retained element instead
  of keeping its struct from the return sweep (#10315,
  `2026-09-26-zd-a-callee-local-record-behind-a-returned-tuple.md`).

The admission side also changed:
- `tuple_ann_admits_fresh_mixed` admits the element to the shallow `TUP:` class.
- `fieldmove_expr` reads it as a counted share. The struct literal's `string` and
  `string[]` retains are gated, so they stay marked there.
- `strarrfld_scan` no longer marks an `S` element.

Native needs no mirror: its retain and release are type-driven
(`needsRcIncOnAlias` admits the field read; `__drop_strarr` releases it).

## Measured

`TestSelfHostTupleFieldShare` runs nine shapes:
- on x86-64 (leakcheck and the sanitizer), arm64 and wasm;
- under the semantic, AST and both mixed lowerings;
- with a native leg that holds `internal/` to the same answers.

19 cells fail on main.

## The harness trap

`asm_ir_run`, the self-host driver behind `TestSelfHostLeakMatrixX86_64`, does
not run `checker.annotate_module`. `fa.ty` is empty there, so
`tuple_field_share_kind` never fires. A matrix cell for this shape measures the
pre-fix lowering: `native=clean selfhost=leak` on the local row, though the CLI
is clean. The cross-compiler check therefore lives in the new test's native
leg, not in the matrix. A predicate that reads a checker stamp is invisible to
that matrix until its driver annotates.

## Still leaking

The retain has no releaser in these contexts, so they still leak on the AST
lowering, as on main. The byte totals moved, in opposite directions. Each
probe rebinds the record after the literal, on x86-64; the census is allocs /
frees / live bytes:

| fragment | main | this change |
|---|---|---|
| `var (a, b) = (r.n, r.ys)` (`i32[]`) | 5 / 3 / 72 | 5 / 3 / 88 |
| `f((r.n, r.ys))` (`i32[]`) | 5 / 3 / 72 | 5 / 3 / 88 |
| `var (a, b) = (r.n, r.xs)` (`string[]`) | 5 / 2 / 112 | 5 / 3 / 80 |
| `match ((r.n, r.xs))` (`string[]`) | 5 / 2 / 112 | 5 / 3 / 80 |

The `string[]` rows gain a free because the record's deep drop is no longer
withheld. The `i32[]` rows now keep the buffer the tuple retained, 16 bytes
more. On main that buffer was freed under a tuple that still pointed at it.
The semantic lowering is clean on all four. `refused_elem_extracted`
(`var u = p.1`) also leaks. Its mechanism changed: before, `strarrfld_scan`'s
mark withheld the record's drop; now it is the tuple's unreleased retain, since
the extraction refuses `TUPELEMOK:`. The byte count is the same.

## Next

- #10315: a callee-local record behind a returned tuple, kept from the sweep by
  `returned_moved_arr_slots`' tuple arm.
- #10319: a `string[]` field built from `.to_string()` elements is refused by
  `strarr_value_is_fresh`, which withholds the record's deep drop.
