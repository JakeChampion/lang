# f64 transcendentals: per-backend kernels vs std/float (2026-10-10)

#5541 replaced the hand-written exp, log, sin, cos and pow kernels (x86-64 and
arm64 assembly in the self-host emitters, wasm text, the Go interpreter's
copies and `internal/tables/fdlibm`) with one Fern definition in
`internal/stdlib/std/float.fern` that every target compiles.

## Results are bit-identical

Before the builtins were deleted, the Fern kernels and the builtins were
compared over 400,000 random inputs per function (exp, log, sin, cos, pow) on
arm64-darwin: 0 mismatches. The benchmark checksums below also match bit for
bit.

## Speed

arm64-darwin (Apple M-series), 20M calls per function over a fixed argument
sweep, min of 9 runs, user time. "Before" is the compiler and std/float at the
branch point; "after" is this branch. The machine was shared and repeat runs
moved by up to 10%, so read the ratios, not the absolute numbers.

| function | before ns/call | after ns/call | after / before |
|---|---|---|---|
| exp | 5.01 | 4.29 | 0.86 |
| log | 4.06 | 2.91 | 0.72 |
| sin | 4.60 | 4.09 | 0.89 |
| cos | 4.63 | 4.15 | 0.90 |
| pow (non-integer y) | 12.06 | 12.32 | 1.02 (medians equal) |
| pow (integer y) | 3.85 | 3.83 | 0.99 |
| sin, \|x\| >= 1e7 (Payne-Hanek) | 8.01 | 9.25 | 1.15 |

What makes the Fern kernels competitive is general compiler work done for
them: an f64 register class in the SSA allocator with a d0-d7 / xmm float
argument ABI, static i64/u64/f64 constant arrays, splicing of zero-parameter
constructors, skipping drops of static boxes, bounds-check removal for a
constant or masked index into an array literal, leaf functions without a
frame, and the `__mulhi_u64` builtin (`umulh` / `mulq`). The large-argument
sin is the one regression: its 128-bit products go through `__mulhi_u64`
plus wrapping multiplies where the hand-written kernel kept a carry chain.

## Accuracy

Against a 200-bit mpmath reference, 20,000 random arguments each:

| function | arguments | max ulp |
|---|---|---|
| exp | x in [-700, 700) | 0.85 |
| log | 10^[-300, 300), and 1 +- 0.06 | 0.50 |
| sin | x in [-10, 10) | 1.32 |
| cos | x in [-10, 10) | 1.41 |
| sin | \|x\| < 1e22 | 2.29 |
| cos | \|x\| < 1e22 | 2.71 |
| pow | x in [0, 100), y in [-30, 30) | 193 |

These are the old kernels' errors too, since the results are identical. pow's
error grows with `|y log x|`; that is #12041. The large-argument sin and cos
exceed 2 ulp on some arguments, which `f64_ulp_test.go`'s fixed corpus
happens to miss; that is #12042.
