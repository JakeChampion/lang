# Uniqueness tests branch on flags; retains work in memory

Two slices over the inline rc primitives, measured 2026-09-23 on x86-64
(callgrind Ir, static counts over `checker.fern`'s emitted `.s`).

## A brif on `__fern_rc_is_unique` is fused (#10089)

The reuse path branches on a uniqueness test at every record update. The
emitter used to build 0/1 in `%rcx`, move it to the value's home, copy that
into `%r11` and test it. Now `ssa_fuse` recognises the inlined test as a
brif condition and `ssa_unique_flags` leaves the answer in the flags: the
tagged-pointer and heap-floor guards jump to a `_uq` join with ZF clear
("not unique"), and the count compare against one falls into it. The same
slice makes a brif on a plain value test the value's own register instead of
a copy in the scratch.

| | before | after |
|---|---|---|
| x86-64 static | 450,289 | 395,435 (−12.2%) |
| arm64 static | 407,865 | 370,077 (−9.3%) |
| `testq %r11, %r11` | 12,174 | 105 |

| bench | Ir change |
|---|---|
| `sort_inplace` | −15.1% |
| `array_with` | −15.0% |
| `record_update` | −8.7% |
| `sort_ints` | −8.5% |
| `array_append` | −8.3% |
| `pvec_with` | −8.0% |
| `pmap_insert` | −6.9% |
| `ordmap_insert` | −5.5% |
| `tokenize` | −5.3% |
| 20 others | 0% to −4.0% |
| `map_probe_chain` | +0.75% |

`map_probe_chain`'s emitted code is strictly shorter at every changed site,
and at a tenth of the size the new build runs fewer instructions; the row is
99% `__fern_map_find`'s linear scan (#9608), not anything this touched.

## The x86-64 retain and uniqueness test read the count in memory

With the sanitizer off nothing needs the loaded count, so the retain is
`cmpl $0, -8(p); js; addl $1, -8(p)` instead of a load, test, add and store
through `%ecx`, and the uniqueness test is `cmpl $1, -8(p)`. With
`FERN_RC_FREE_DEBUG` the load stays, since the poison check compares the
loaded value. arm64 has no memory operands and already tests the sign with
`tbnz`, so it is unchanged.

x86-64 static: 395,435 → 374,182 (−5.4%).

| bench | Ir change |
|---|---|
| `record_update` | −2.9% |
| `array_with` | −2.9% |
| `sort_inplace` | −2.6% |
| `ordmap_insert` | −2.5% |
| `pvec_with` | −2.3% |
| `pmap_insert` | −1.5% |
| `array_append` | −1.5% |
| `sort_ints` | −1.3% |
| `sort_strings` | −1.2% |
| 6 others | 0% to −0.9% |
