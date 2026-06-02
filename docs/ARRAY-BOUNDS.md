# Array bounds checking

Status: policy doc.

Indexing an array or slice out of range — `xs[i]` or `xs[i] = v` where
`i < 0` or `i >= len` — **aborts the program** on every backend. It
never reads or writes past the end and never returns a garbage value.

```
var xs: i32[] = [10, 20, 30];
xs[5]        // aborts: index 5 out of range [0, 3)
xs[0 - 1]    // aborts: negative index
xs[7] = 9    // aborts: out-of-range write
```

This is the same "no silent corruption" stance as the rest of the
language. Reading uninitialised adjacent memory (what an unchecked
index does) is never a defined result, so the access is checked and
the program stops.

## Behaviour per backend

The check compares the index against the array's length prefix (`[base
- 4]`) or the slice header's length field (`[slice + 4]`) before
computing the element address — a single unsigned compare that catches
both a negative index and `index >= len`.

- **x86-64 / arm64** abort with **exit code 134** (the same trap the
  string-slice helper uses).
- **wasm** traps (`unreachable`), which `wasmtime` surfaces as exit
  134.
- **interp** reports a diagnostic (`array index N out of range [0,
  L)`) and exits non-zero — a friendlier message for `fern -interp`
  debugging.

All four agree on the observable contract: an out-of-bounds index
aborts before producing a value.

## Cost

Every array / slice element access carries one length load + compare +
branch. The branch is statically predictable (in-bounds is the common
path), so the steady-state cost is small; there is no `unsafe`/uncheck
escape hatch today.

## Testing

`internal/e2e/array_bounds_test.go` asserts that out-of-range reads,
writes, negative indices, and slice indexing all abort on every
codegen backend, and that ordinary in-range indexing (every element
width, reads + writes, loops, slices) is unaffected.
