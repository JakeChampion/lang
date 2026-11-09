# C structs

A C library reads and writes structs in memory. The offset of each field comes from the C rules, not from the order of the Fern fields alone. This module holds the trait `CLayout`. `@derive(ffi.CLayout)` gives a struct the layout of the same struct in C. It also gives the functions that read and write the struct at an address.

## Model

Each field of a struct with a C layout is `u8`, `i32`, `u32`, `i64`, `u64`, `usize`, `f32` or `f64`. A `usize` field holds a C pointer. The size of a field is also its alignment: 1 for `u8`, 4 for `i32`, `u32` and `f32`, and 8 for the others.

Each field starts at the next offset that its alignment divides. The size of the struct rounds up to the largest alignment of a field. These are the rules of C on each 64-bit target of Fern.

The derive makes these functions for a struct `S`:

- `S.c_size()`: the size of the struct in bytes.
- `S.c_load(p)`: the struct at the address `p`.
- `s.c_store(p)`: writes `s` at the address `p`.
- `S.c_offset_f()` for each field `f`: the offset of the field in bytes. Give `p + S.c_offset_f()` to a C function that writes the field.
- `S.c_load_f(p)` and `S.c_store_f(p, v)` for each field `f`: one field of the struct at the address `p`.

The functions that read or write memory are `unsafe`. Call them in an `unsafe` block. Use the functions of one field when a second thread can write the other fields at the same time.

## Contracts

The address must hold `c_size()` bytes that the program can read and write. The largest alignment of a field must divide the address.

The checker refuses the derive on an enum and on a struct with type parameters. It also refuses a field with no C form, such as a `string`.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<trait>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module gives a struct the memory layout of the same struct in C.
```

### `trait`

```fern
<<trait>>=
// A struct with the layout of a C struct. `@derive(ffi.CLayout)` implements
// it.
pub trait CLayout {
  // The size of the struct in bytes.
  function c_size(): usize;
  // The struct at the address `p`.
  unsafe function c_load(p: usize): Self;
  // Writes the struct at the address `p`.
  unsafe function c_store(self: Self, p: usize): void;
}
```
