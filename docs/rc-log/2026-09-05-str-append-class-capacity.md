# 2026-09-05 — `__fern_str_append` grows to the allocator's class capacity (#8404)

x86-64 (`emitStrAppendRuntime`) and wasm (`buildStrAppendBody`); arm64 has no
in-place append at all (#8414). Part of the coreutils work (#8278): the
`build(chunk)` helper that turns a 128 KiB `read_chunk` into an output
string line by line, `out = out + slice_unchecked(chunk, i, j + 1)`.

## The shape, measured

`bin/fern -O -target x86-64-linux`, 20 MB of ~44-byte lines (454k lines),
`callgrind`:

```
7,664,940,596  PROGRAM TOTALS
7,458,658,058 (97.31%)  __fern_memcpy
   30,390,526 ( 0.40%)  __fern_alloc
   28,647,653 ( 0.37%)  __fern_memchr
   25,915,562 ( 0.34%)  __fern_strcat
   21,824,880 ( 0.28%)  __str_slice
```

Not the slice materialisation the shape was suspected of — `__str_slice`
plus its allocation and the 20 MB of piece copies are ~1% — but ~30 GB of
memcpy: the accumulator copied whole ~3000 times per chunk. The helper's
in-place test was "same 16-byte class", `(len + 24) & -16` unchanged, and its
own comment said why: "NOT amortised growth — there is no capacity slot in
the 8-byte [rc][len] header to hold one". A 44-byte piece crosses a 16-byte
class every time, so every append fell to `__fern_strcat`.

The capacity slot existed all along, in the size class. `__fern_alloc`'s
large tier (>2048 B) bumps a block at a three-significant-bit capacity
(≤25% slack) and `__fern_free` re-derives that class from the logical size,
so a 100 KiB string already sits in a 112 KiB block. The fix is to ask the
allocator's own class function: in place iff
`class(len + 9) == class(total + 9)`. Below 2 KiB that is the old 16-byte
test; above it the accumulator grows until the capacity is used and copies
once into a block 12–25% larger — amortised O(1) per byte, no header change,
no new runtime state. The class arithmetic is now ONE definition per
backend — `emitSizeClassCap` / `emitSizeClassIndex` on x86-64, the existing
`emitFreelistBin` on wasm — shared by alloc, free and the append, where
alloc and free each carried their own copy before.

| | base | after |
|---|---|---|
| c2 (`w.write(build(chunk))`), 20 MB | 1.18 s | 0.06–0.13 s |
| c1 (accumulator in the arm, after #8394) | 1.16 s | 0.05–0.09 s |
| `FERN_LEAKCHECK` allocs on c2 | 909,765 | 466,008 |
| `FERN_LEAKCHECK` live_bytes on c2 | 40,197,424 | 40,197,424 |

The allocation count halves — the per-line `__fern_strcat` is gone, the
per-line `__str_slice` remains — and live_bytes is identical to the byte,
which is the accounting check: the in-place path charges
`__fern_lc_alloc_bytes` with the difference between the grown and the
original 16-rounded request, because `__fern_free` will charge the grown
one. Without that the pair would have drifted negative by the growth.

## Traps

- **`__heap_bump_bytes()` cannot see this defect.** The obvious AL-01-style
  conformance case printed `linear` under the BASE runtime too: the large
  tier recycles every superseded copy into the next same-class request, so
  fresh bytes were already linear — the cost was copy volume. What
  separates the regimes is the allocation COUNT (one `strcat` per append
  against one per class step, ~75 for 4096 pieces) and the memcpy volume;
  the gate is the `FERN_LEAKCHECK` count on x86-64 and wasm
  (`TestX86_64StrAppendAmortised`, `TestWASMStrAppendAmortised`), each
  checked to fail under the pre-fix runtime.
- **The alloc-scaling calibration row (`s = s + "x"`, n = 400) is
  unaffected**: it never leaves the small tier, where the class is still
  16 bytes and the fold is still quadratic in bytes.
- **The remaining 40 MB live on c2 is not this change** — identical before
  and after: the `read_chunk` payload per chunk (#8396), the I/O result
  boxes (#8405), and the `build(chunk)` temp passed to `Writer.write`
  (#8413, fixed alongside).
- **The self-host runtime has no in-place append**, so the self-host
  compiler stays quadratic on this shape on every target; that is the
  reclaim side of goal 2, not a divergence of this change.
