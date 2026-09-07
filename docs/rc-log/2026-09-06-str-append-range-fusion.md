# 2026-09-06 — `acc + slice_unchecked(s, lo, hi)` appends without the slice (#8770)

x86-64 (`emitStrAppendRangeRuntime`) and wasm (`buildStrAppendRangeBody`);
arm64 has no in-place append at all (#8414), so nothing to fuse into.

`2026-09-05-str-append-class-capacity.md` closed the memcpy half of this shape
and ended with "the per-line `__str_slice` remains". This is that remainder.
`__str_slice` allocated a fresh block and copied the range into it purely so
`__fern_str_append` could copy it back out into the accumulator's slack and
free it: two copies and an allocation for one append.
`__fern_str_append_range(a, s, lo, hi)` is the same append with the piece
given as a range of a second string, so the bytes move once.

## Measured

`bin/fern -O -target x86-64-linux`, 8M iterations, median of 9 runs on the
4-core container:

| probe | before | after |
|---|---|---|
| `out = out + "$\n"` | 58 ms | 57 ms |
| `out = out + slice_unchecked(src, 0, 8)` | 183 ms | 101 ms |

10.2 ns off each append of an 8-byte range, against the 9.2 ns #8770 measured
for the materialisation alone. `coreutils/cat.fern` over 1M lines of
`seq`, retired instructions under callgrind: `-E` 408.6M → 342.9M (−16.1%),
`-v` 509.6M → 457.5M, `-n` 756.9M → 698.6M, plain and `-s` flat to within
0.2%. `-s` appends once per RUN of lines rather than once per line, which is
why it does not move.

The allocation collapse only shows above the 7-byte inline threshold, since
`__str_slice` packs a shorter range without allocating: 500 appends of a
12-byte range go 633 → 266 allocations, while cat's own lines average under 8
bytes on that input and save the second copy and the call rather than an
allocation.

## Traps

- **Bounds must be checked UP FRONT, not left to the fallback.** On the fast
  path there is no `__str_slice` call left to reject a range, so without its
  own check the helper reads out of bounds where the unfused form trapped.
  The e2e cases run several valid appends first so the accumulator is a
  uniquely-held heap buffer by the time the bad range arrives — a trap case
  that fell to the slow path would be testing `__str_slice` instead, which is
  why the x86-64 leg asserts the fused call is in the emitted text.
- **The source may BE the accumulator.** `e = e + slice_unchecked(e, 0, 2)`
  passes one buffer as both operands. It is safe without a `memmove`, and by
  construction rather than by luck: the range lies inside the accumulator's
  current length, which is exactly where the destination begins, so the two
  regions touch and never overlap.
- **A cell read stays unconsumable** (#8067). The fused path is gated behind
  the same `consumeLeftTemp` / `selfStrAppendBin` decision as the plain
  append, so the exclusion is inherited rather than restated — but it is
  pinned by its own ops-level case, because inheriting it silently is how it
  would be lost.
- **The SSA backends have no emitter for it**, and a missing runtime helper
  there is a hard "call target the module never defines" that would drop
  `TestX86_64SSABackendDifferential`'s compared-programs floor. They
  bump-allocate and never reclaim, so `ssa.LiftFromIR` expands the call back
  into `__str_slice` + `__str_concat` — the same treatment `ssaHelperName`
  already gives `__fern_str_append`.
- **The self-host compiler has no in-place append**, so there is no fused
  form for it to mirror; its concat stays allocate-and-copy on every target.
  That is the reclaim side of goal 2, not a divergence of this change. What
  IS mirrored is `irverifyrc.fern`'s rc release set, which
  `TestSelfHostIRVerifyRcReleaseSetMatchesNative` pins entry-for-entry.

## Alongside: one size-class computation, not two

The in-place guard computed the capacity of both the old and the grown
request and compared them for equality. The capacity function is monotone
and idempotent, so `class(req_new) == class(req_old)` exactly when
`req_new <= cap(req_old)` — one computation. What that is worth depends on
the tier: below 2048 bytes the capacity IS the request, so a compare and a
branch go; above it — where an 8 KiB accumulator sits — a `bsr` plus seven
more instructions on x86-64 and a whole `emitFreelistBin` expansion on wasm
do. `examples/bench/string_build` never leaves the small tier and falls
0.27%, with exactly ten instructions off the `.text` of every program that
appends.

The algebra is checked over the whole 16-aligned request domain in
`internal/codegen/x86_64/sizeclass_cap_test.go`; the emitted guard is pinned
by the allocation count of a 2100-append growth across the 2048 tier change,
which is the guard's decision sequence rather than a proxy for it.
