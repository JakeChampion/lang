# Scalar sRGB destination kernel

The `codecs/icc/srgb_destination` package converts one relative D50 XYZ value to encoded 8-bit sRGB.
It is step 1 of [decision 0003](decisions/0003-icc-srgb-destination.md) under #13659.
It does not read a source profile, transform an image, or change encoded JPEG admission.

## Public contract

`encode(input, cancelled)` accepts an explicit `matrix_eval.XYZ` value and returns `Srgb8`.
The value must be media-relative D50 XYZ, as the v4.4 matrix/TRC model gives it.
The kernel applies no adaptation, no scale for media white, and no black-point compensation.

The kernel does these steps in order:

1. It solves the frozen sRGB colorant matrix with `matrix_solve.solve` (decision 0003, step 6).
2. It clips each linear channel to `[0, 1]`. A negative value or negative zero gives zero.
3. It encodes each channel with `color.linear_to_srgb`, the IEC 61966-2-1 function.
4. It computes `floor(e * 255 + 0.5)` for each encoded value `e`.

`color.linear_to_srgb_byte` holds steps 2 to 4.
The color compositor and the resize kernel use the same function.

`matrix()` returns the nine raw `s15Fixed16` numerators of the frozen profile in row order.
The kernel stores these constants and does not read a profile at run time.

The input admission is the admission of `matrix_solve`.
A nonfinite channel returns `Solve(Input)`.
A channel outside `[-2, 2]` returns `Solve(Unsupported)`.
The kernel does not clip XYZ.
Both errors occur before the first callback.
The solve polls before its arithmetic and before success, and either poll can return `Cancelled`.

## Reference and criteria

The generator `tests/generate_icc_srgb_destination.py` defines 4,239 cases: 4,219 byte results and 20 expected errors.
The corpus has these parts:

- 1,801 exact controls: black, negative zero, and 257 levels of each nonempty sum of colorant columns. These cover the gray axis, the primaries, the secondaries, and media white.
- 112 clip cases above media white and 512 grid cases over `[-2, 2]`.
- 1,024 seeded values inside the D50 white box and 512 seeded values over the whole domain.
- 256 values that the exact forward matrix gives from linear values at a byte boundary.
- 18 input errors and 4 cancellation cases.

The exact reference uses `Fraction` arithmetic for the matrix solve and the clip.
It uses 90-digit `Decimal` arithmetic for the power.
The criteria are the criteria of decision 0003:

1. Each byte must equal the exact byte. A difference of one is permitted only when `255e` is within `1e-9` of a boundary between two bytes. The record keeps each such case.
2. Offline LittleCMS 2.19, from the pinned Pillow 12.3.0 wheel, must give a byte within one code value. It converts `TYPE_XYZ_DBL` to the frozen profile as `TYPE_RGB_8`, with `Relative` intent and flags 324. It applies to the 3,464 byte cases with no negative XYZ channel.
3. Each control must give the exact byte, with no boundary allowance.
4. The three target sample records must be equal. Each worker must report a nonzero balanced heap with zero live bytes.

Criterion 4 of the decision is about alpha bytes. This kernel has no alpha channel.

The worker format is `FSD1`, a cancellation index, and three signed input bit patterns.
This protocol is test infrastructure, not a runtime API.
The collector starts a fresh process for each group of at most eight cases.
`qualify_icc_srgb_destination_test.py` rejects wrong bytes and a difference of one away from a boundary.
It also rejects a LittleCMS difference above one, and wrong errors or polls.
It also rejects malformed output, bad references, process faults, and unbalanced heaps.

## Limits

The kernel does not compose a source profile with the destination. That is step 2.
It has no row or image API, no prepared tables, and no JPEG entry point. Those are steps 3 and 4.
There is no compatibility, performance, physical/process bound, hard-deadline, or production claim.
The finite observations do not establish a universal precision bound.

## Recorded finite campaign

Source `a4b1828ad8d60d5e09ab7abb00f9a700cbf9da90` precedes all three campaigns, with a clean worktree.
The compiler is `bin/fern` from main `fa69253f6c8b`, built while only `fern-image/` files had changes.
Its SHA-256 is `e32ece9c1936e55a61bfbce8943fb491ea371964410ef614478d8ed6a5f4e22e`.
The reference bundle SHA-256 is `4cba60b05a78960b3169d6b7caab6d241235b283f02b4bcc3007f5d1e53d7e96`.

Native x86-64, Wasm under wasmtime 51.0.0, and ARM64 under qemu-aarch64 8.2.2 each pass all 4,239 cases.
Each target uses 530 fresh workers.
All 1,590 workers report nonzero balanced heaps and zero live bytes.
The three target sample records are equal.
The 1,846 native and 1,847 runner source and tool hashes did not change during the campaigns.

Each control byte is exact.
231 channels differ from the exact byte by one, and each is a `byte-boundary` case.
The largest boundary gap of these channels is about `2.25e-14` on the `255e` scale.
Of the 10,392 LittleCMS channels, 10,133 are equal and 259 differ by one.
The TAP suite passes its six groups on all three targets with balanced heaps.

The records are `benchmarks/results/icc-srgb-destination-{native,wasm,arm64}.json`.
