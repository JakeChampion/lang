# Explicit full-range YCbCr sample conversion

Issue #13619 is a native child of JPEG milestone #13216.
This scalar Fern kernel converts explicit eight-bit Y, Cb, and Cr samples to RGB8 values.
It does not identify encoded color meaning, reconstruct chroma, or decode a complete color JPEG image.
The restricted grayscale decoder remains separate.

## Numeric contract

The reference is [JFIF 1.02, page 3](https://www.w3.org/Graphics/JPEG/jfif3.pdf).
The PDF SHA256 is `1135325aa12ee6b3def5a374fecdf3d6d0de88dc4f205af7de8d25a9c949e173`.
The kernel uses the decimal equations printed in that document:

```text
R = Y + 1.402 * (Cr - 128)
G = Y - 0.34414 * (Cb - 128) - 0.71414 * (Cr - 128)
B = Y + 1.772 * (Cb - 128)
```

Every input has the full unsigned range zero through 255.
The chroma offset is 128, and Y has no video-range offset or rescaling.
The implementation evaluates these decimal coefficients exactly in signed 64-bit hundred-thousandths.
It rounds to the nearest integer, with exact half ties toward positive infinity, then clips to zero through 255.
Clipping before integer division preserves this result at either endpoint.
All intermediates fit the selected integer type over the complete declared byte domain.

This contract uses the printed five-decimal green coefficients.
Other libraries can use different coefficient precision, integer approximations, rounding, or chroma filters.
Exact agreement with this mathematical contract is not codec parity or a universal color-quality claim.

## API and ownership

Import `packages/codecs/jpeg/ycbcr`.
`convert(y, cb, cr)` returns a `pixels.Pixel` with opaque alpha 255.
`convert_row(y, cb, cr, max_output_bytes, cancelled)` accepts three borrowed byte views at the same resolution.
Each view must contain the same positive sample count.
The result owns a packed RGB byte array with exactly three bytes per input position.
The row API does not infer dimensions, sampling factors, component identifiers, marker meaning, or profiles.

Invalid shapes return `Shape` before any callback or output allocation.
Checked multiplication, a nonpositive byte limit, or an exceeded byte limit returns `Limit`.
The row output also has a hard logical cap of 64 MiB.
Callers can select a lower cap, including the exact output length.
Input arrays remain unchanged, and output updates use ordinary copy-on-write semantics.
Independent calls return independent owned results.

The API checks cancellation before and after allocation, at groups of at most 64 samples, and before success.
Cancellation returns `Cancelled` and releases private output.
The bulk allocation and zero fill do not contain internal cancellation checks.
The callback remains trusted code with unmetered effects and costs.
The logical byte cap does not establish allocator capacity, physical/process memory, aggregate admission, or a hard CPU deadline.
Allocator failure remains a runtime concern rather than a recoverable conversion error.

## Finite independent reference

The generator uses Python `Fraction` arithmetic to evaluate the printed equations independently of the Fern integer implementation.
It rounds each exact rational result and then clips it.
The reference bundle contains 70,844 distinct sample triples in 139 rows.
It includes all 65,536 Cb/Cr pairs at Y=128, an endpoint grid, all neutral intensities, exact half ties, and deterministic random controls.
The generator records the specification, Python, rational-library, random-library, and source identities.

The target worker checks both scalar and row API results.
The collector compares every RGB byte exactly, without numeric tolerance.
It also checks row identity, sample count, storage length, output hashes, and the worker heap census.
A fresh process handles at most eight rows and must report nonzero balanced allocations and zero final live bytes.

Each target campaign checks source and tool hashes before and after execution.
Failures retain the completed prefix and failed input/output evidence and still return failure.
The 20-second worker timeout is an operational test limit, not a converter deadline guarantee.

Five TAP groups cover neutral endpoints, clipping, exact half ties, packed channel order, shape and byte admission, borrowed windows, ownership, and every cancellation poll.
Protocol fault controls reject one-value differences, swapped channels, wrong storage lengths, missing output, incorrect row metadata, and invalid heap censuses.
The finite corpus does not qualify encoded JPEG compatibility, photographs, perceptual quality, profiles, security, throughput, physical memory bounds, or production operation.
M5 and the epic remain open.

## Reproduction

Run these commands from `fern-image` with a normal repository compiler build:

```sh
FERN="$PWD/../bin/fern" ./scripts/test-jpeg-ycbcr.sh native
FERN="$PWD/../bin/fern" ./scripts/test-jpeg-ycbcr.sh wasm
QEMU_AARCH64=/path/to/qemu-aarch64 FERN="$PWD/../bin/fern" ./scripts/test-jpeg-ycbcr.sh arm64
python3 -m unittest discover -s tests -p qualify_jpeg_ycbcr_test.py
python3 tests/generate_jpeg_ycbcr.py tests/fixtures/jpeg-ycbcr-reference.json.gz
```

The reference uses only offline Python standard-library tools.
The Fern runtime has no C, Python, or external codec dependency.
New raw records use `jpeg-ycbcr-*` names and preserve all earlier datasets.

## Recorded qualification

The new records are [native](../benchmarks/results/jpeg-ycbcr-native.json), [Wasm](../benchmarks/results/jpeg-ycbcr-wasm.json), and [ARM64](../benchmarks/results/jpeg-ycbcr-arm64.json).
Their source commit is `8f1ad1e30e9bfabd98839c8140e322c365c44f46`.
The clean compiler reports `dfa23fa508f9`, with SHA256 `68b47eac8e56bb44944fcde126bb29d80ad2173afb7ac57eaf6c52e7b0f44282`.
Compiler and stdlib sources match the integrated main revision `3899b879b3dacc9975f0f2d5ac5c900d6a756298`.

Each target completed all 70,844 triples in 139 rows across 18 fresh workers.
All 54 workers reported nonzero balanced heap counts and zero final live bytes.
Every RGB byte matched the independent rational reference exactly, and the target sample records match.
Native qualification locks 523 source and tool hashes, while each runner target locks 524.
These include all 163 stdlib sources and the compiler's Fern sources.
Each campaign verified immutable source and tool hashes.

These observations apply to explicit sample conversion only.
They do not qualify chroma filters, encoded color detection, codec parity, or complete color JPEG decoding.

All 184 image TAP cases passed on native sanitizer, Wasm leak census, and standalone ARM64/QEMU10.
Full native and Wasm scripts completed with 65 and 41 balanced heap censuses respectively.
The portable ARM run completed 26 executables with balanced heaps and zero final live bytes.
The primary interpreter passed all five new TAP groups.
Both unchanged complexity gates passed without skips.

Eight protocol controls, 1,313 Fern format checks, Go build/vet, workflow lint, selectors, and CI self-tests passed.
Independent reference regeneration produced the same bundle SHA256, `b853e0ab2aa51e6731a38448301103dac4e74f253ea5bb6ce710653c669b63a5`.
These local observations are separate from GitHub CI status.
