# An append onto a `.with` copy releases the copy

2026-09-24. Native.

```
var xs: i32[] = [1, 2, 3];
var zs: i32[] = xs.with(2, i).append(8);
return zs.len() + xs[1];
```

One block leaked per call. It is the `withs` shape in
`chained_array_methods`, which was all 50 of that fixture's unpaired
allocations.

## Cause

`xs` is read after the `.with`, so `arraySetInc` retains it and
`__fern_arr_cow_inplace` takes its copy path. The `.with` result is a fresh
rc 1 buffer that only the chain holds. `.append` releases an owned receiver
after the grow, but `ownedAppendReceiver` admitted only an inner append, a
literal and a fresh call result. The `.with` copy was none of those, so
nothing released it when the grow moved to a new buffer.

## Change

`ownedAppendReceiver` admits a `.with` whose receiver took the forced-copy
retain. A `.with` that ran in place is still left out, because its result is
the receiver's own buffer and the receiver's slot may still release it.

## Measured

- The shape above, three calls on x86-64 `-sanitize`: 6 of 9 blocks freed
  before, 9 of 9 after. With string elements, 12 of 21 before on x86-64,
  and 21 of 21 after on x86-64 and arm64. Both match the interpreter.
- `TestAppendOntoAWithCopyReleasesTheCopy` pins both on x86-64, arm64 and
  wasm. All six legs fail without the change.
- `chained_array_methods` in the conformance census: 50 unpaired → 0.
