# 2026-09-30 — a string[][] field bind holds a count of the field (#10548)

AST lowering (`irlower.fern`: `nested_arr_field_read_type`,
`is_counted_rows_type`, `slot_is_reclaimable_arrarr`,
`arrarr_free_helper_of`).

Not a leak but a use-after-free. `nested_arr_field_read_type` admitted only
`is_nested_array_field_type`, which leaves `string[][]` out, so
`let p = r.names` was not even an array slot and took no retain. Rebinding
`r` ran `__field_reclaim_<T>` and freed the rows `p` still read: SIGSEGV on
x86-64. On wasm `p.len()` read 1 for 2, and `p[1][0]` trapped.

The classifier now uses `is_row_array_field_type`, so the bind retains, and
the field-bind site is a lax credit candidate. A string-kind slot with only the
lax credit now releases through `__fern_arrarr_free` (rows, not strings), as
the struct's own field drop does; the strict credit still picks the string
walk.

## Measured (AST lowering)

| shape | before | after |
|---|---|---|
| holder rebound before `p`'s read, x86-64 | SIGSEGV | 10 / 10 |
| `field_bind_strings` row (four rounds), wasm | exit 12, want 24 | 36 / 36, 24 |
| loop-local bind, x86-64 | 16 / 16 | 16 / 16 |

## Trap

The first probe (a loop-local bind) balanced before the fix, and I closed
the issue on it. The rebound holder is the shape that shows the free.

## Still leaking

A holder typed from a call (`let r = mk(i)`) defeats `local_struct_type_of`,
so the bind retains without a credit and its rows leak. An `i32[][]` field
bind has always behaved the same way: 16 / 7 over four rounds.
