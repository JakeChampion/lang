# A nested array held by a record or a tuple is released

On the AST lowering (`FERN_SEM_IR=`), nothing released a `T[][]` or
`string[][]` held by a struct field (#10397). `__struct_drop_<T>` had no arm
for the field. A struct whose only rc field was a nested array did not route
field reclaim at all. A tuple position holding one took a shallow buffer dec,
so its rows leaked.

## Why the field can take the struct-array release

A row stored into a nested array is counted. The array literal, `.append` and
the value-form clones all retain the row (`is_counted_elem_array_type` names
nested arrays). The release is therefore `__fern_arrarr_free`, as for a
struct-array field: it gates on the outer buffer being unique, then decs each
row once. A row another owner still holds survives that dec.

`is_row_array_field_type` names the kind: a nested array, or `string[][]`,
which `is_nested_array_field_type` does not reach. It is read by:
- `struct_has_reclaim_array_field` (routing);
- `struct_drop_field_ops` (`box_walk_dec`) and `field_reclaim_field_ops`
  (`arrarr_free`). Wasm's `__field_reclaim_` walks the rows with
  `$__fern_arr_dec_ptr`; before, it shallow-dec'd the buffer.
- `return_value_is_strictfresh_struct`, so a caller releases the struct a
  producer returns;
- `nested_field_deep_drop_ok`, so an enclosing struct's drop reaches the field;
- `row_arr_value_is_borrowed`: a struct literal now retains a field, tuple
  element or indexed read stored into such a field, for any holder. Before,
  only a bare-ident holder's field read (`enum_arr_field_share_read`) was
  retained.
- The tuple element kinds. A nested-array position, whether a local or a
  retained field read, is `b`, and `emit_box_elem_array_release` releases it
  through `__fern_arrarr_free`. `is_box_elem_array_type` covers it, so a
  returned tuple's ARRF: flag is `3`, and `ssarc.tuple_flags` writes `3` for a
  produced callee's nested-array position.

## Measured (x86-64, allocs / frees)

| shape | main, AST | this change, AST |
|---|---|---|
| `Bag { n: 3, grid: [[3, 1], [2, 3]] }` | 4 / 1 | 4 / 4 |
| the same with a `string[][]` field | 4 / 1 | 4 / 4 |
| `var t: (i32, i32[][]) = (3, g)` | 4 / 2 | 4 / 4 |
| a producer that builds the grid with `.append`, rebound five times | 57 / 22 | 57 / 57 |
| three `Bag`s sharing one grid in a `Bag[]` | 13 / 2 | 13 / 13 |

`TestSelfHostTupleFieldShare`'s `callee_local_nested` row (#10402) now
balances on every lowering (10 / 10, pinned at 10 / 4 before), so its pin is
removed. `callee_local_nested_strarr` stays pinned at 5 / 1, now attributed to
#9556: its `var (n, rows) = mk(3)` destructure leaks for any returned tuple,
and a `(i32, i32[])` return leaks 2 / 0 the same way.

`TestSelfHostNestedArrFieldDrop` runs ten rows on x86-64 (leakcheck and the
sanitizer), arm64 and wasm, under all four lowerings.

## Still leaking

- The string elements of a `string[][]` field: the rows and the buffer are
  released, but a heap string in a row is not. Freeing it needs the
  whole-program admission a `string[]` field's elements go through
  (`strarrfld_scan`).
- An AST `main` does not release a struct that a semantic producer returns
  (#10415). It is not specific to nested arrays: an `i32[]` field leaks the
  same way. Two rows of the test are pinned for it.
- A `T[][]` local aliased by a plain bind (`var h = g`) loses the `ARRARR:`
  credit, so both slots release shallow and the rows leak (3 / 1, #10416).
