# `arr_slice` takes the typed path through `__raw_arr_ptr`

2026-09-25. Self-host typed path. Step 1 of retiring the AST lowering,
continued from `2026-09-25-h-system-leaves-take-the-typed-path.md`.

## What changed

`__fern_arr_slice` read its source array's slots from `__raw_addr(a, 8)`,
with `a` an `i32[]`: an address operand the raw floor types as `usize`, and
nothing turned an array into one. The floor gains the inverse of
`__raw_array`:

- `__raw_arr_ptr(a: i32[]): usize`, the array's address. The array value
  already is its box pointer, so the intrinsic emits nothing (`irlower`
  lowers its operand; ssarc's `RawOps` row is empty), exactly as
  `__raw_array` emits nothing in the other direction.
- Its array operand is lent (`raw_floor_contracts`), like `__raw_data`'s
  string: the address is read, not kept.
- It is registered in every table the floor has: the checker's
  `raw_floor_sigs`, ssarc's `raw_floor_ops`, `expr_is_usize`, the AST
  typer's builtin list, and `RUNTIME-INTRINSICS.md`.

`arr_slice` is retyped against it, so 120 of the 128 helper sources check.
The eight left: `read_file` and `open_with` check inside their bundle,
`print_int`, `read_int` and `i32_lcm` wait on #10244, and the three map
helpers call through a bare code address.

## Measured

- Row `arr-slice-takes-the-typed-path`: slices of an `i32[]`, a `string[]`
  and a struct array, read through; the module and the helper produce, and
  the answer matches the AST leg's. An out-of-range slice exits 134 under
  the typed helper, as it does under the AST one.
- Native could not compile the same program: a field read through a
  struct-array view's element, `qs[1].name` with `qs: [P]`, fails with
  `ir: field access on unresolved struct ""`. That is a native bug of its
  own, fixed separately.
