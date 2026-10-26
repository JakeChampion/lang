# 2026-10-25 — concat reads a view

`std/array`'s `concat` and `(xs: T[]) concat`; `semsource.concat_kernel`
and `builtin_concat`; `ssasem.same_elements`; `ssarc.concat_call`;
`ssaunits.reads_view`; `__fern_arr_concat` in `asmcore` and `wasm_ir`.
Found on the HTTP/3 hello path.

## The shape

```fern
function __append_range(out: u8[], src: u8[], from: i32, to: i32): u8[] {
  return out.concat(__slice_range(src, from, to));
}
```

`concat` took a `T[]`, so appending part of an array meant copying the part
into an array of its own first: one block per STREAM frame a QUIC
connection sends, freed as soon as the concat had read it.

## What changed

`concat`'s second operand is `[T]`. An array passes as before, and a slice
passes as a view of where it points.

- The scalar kernel takes either operand. `__fern_arr_concat` now takes the
  operand's data address and length, not the array: for an array the call
  passes the box's data and its length, for a view the decoded data and
  length (`ssabytes.read_data`, `ssabytes.read`), which every target already
  decodes once where the view is defined.
- `reads_view` counts a concat's second operand as read within the
  instruction, so a slice handed to it is a frame-held descriptor rather than
  a lent block.
- A concat of reference elements takes the generic `std/array` body, which
  iterates the view.

## Measured

| Row | Before | After |
|---|---:|---:|
| `TestSelfHostConcatReadsAView`'s 1,000 three-byte appends into an array with room, x86-64 | 6,649 | 0 |

The program agrees on x86-64, arm64 and wasm, and frees every block under
`-sanitize`. The 6,649 is the compiler before this change on the new
signature, where a view operand took the generic path.
