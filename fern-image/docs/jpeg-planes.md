# Restricted baseline JPEG component planes

Issue #13621 is a native child of JPEG milestone #13216.
This Fern slice reconstructs owned raw component planes from the existing restricted baseline coefficient stream.
It does not reconstruct chroma at image resolution, identify encoded color meaning, apply profiles, or assemble a color RGB image.
The grayscale decoder and explicit YCbCr sample kernel remain separate APIs.

## API and scope

Import `packages/codecs/jpeg/planes`.
`default_limits()` returns `PlaneLimits`, with the existing `entropy.BlockLimits` and a separate `max_plane_bytes` limit.
`inspect(input, limits, cancelled)` validates the header, block plan, component dimensions, and total logical plane storage.
It does not validate entropy data, restart sequences, padding, EOI, or final consumption.
`decode(input, limits, cancelled)` additionally performs complete strict entropy decoding and the scalar inverse transform.
It returns `PlaneImage` only after all final checks succeed.

Input is a complete borrowed encoded byte view.
The admitted header profile remains SOF0, eight-bit precision, one or three components, supported in-input tables, and one complete baseline scan.
Existing input, header, projected RGB8 layout, MCU, and decoded-block limits remain unchanged.
This slice adds no progressive, CMYK, external-table, multiscan, or encoding support.
It preserves the isolated IDCT kernel's explicit coefficient domain and errors.

Each `SamplePlane` contains a `PlaneLayout` and an owned byte array.
The layout retains the frame component identifier and horizontal and vertical factors.
Planes remain in frame order, without numerical identifier sorting or color-name assumptions.
For each component, width is the ceiling of `image_width * horizontal / max_horizontal`.
Height uses the corresponding vertical expression.
Stride equals component width, and storage contains exactly width times height bytes.

The decoder clips partial right and bottom blocks to those component dimensions.
Fractional sampling ratios use the same ceiling rule.
A sole component retains its declared factors, while its sample dimensions remain the full image dimensions.
These sample planes do not define a chroma filter or spatial reconstruction policy.
Identifiers alone do not establish Y/Cb/Cr or RGB meaning.
Metadata remains opaque, and the decoder does not apply EXIF orientation, ICC, or transfer functions.

## Admission, state, and errors

Preparation computes dimensions and the sum of plane bytes in signed 64-bit arithmetic.
It checks the complete sum against the caller's lower limit and a 64 MiB hard logical cap before plane allocation.
A nonpositive plane-byte limit returns `PlaneLimit` before header work or callbacks.
The existing projected RGB8 admission remains conservative even when raw planes need fewer bytes.
This API does not weaken existing header or entropy budgets.

The decoder calls the existing strict coefficient fold after preparation.
That fold parses the header again and preserves its complete validation path.
The decoder does not accept a caller-constructed trusted header or plan.
Each component selects its declared quantizer.
Its private data-only cell contains an optional owned sample plane and no function value.

Before an update, the decoder clears that component's cell and supplies a fresh plane record to its consuming block writer.
Other components keep their own state.
This avoids retaining the old plane in a shared collection during updates and preserves ordinary copy-on-write semantics.
A late entropy, restart, padding, EOI, transform, or cancellation error releases every private partial plane.
No partial image or external consumer callback escapes before success.
Independent calls create independent state and owned results.

`PlaneEntropy` and `PlaneTransform` preserve their underlying error details.
`PlaneCancelled` combines cooperative cancellation from parsing, entropy, inverse transform, and plane writes.
`PlaneStorage` reports an internal component or storage inconsistency.
The API does not convert allocator failure into a recoverable error.

The complete encoded view and complete output planes remain eager.
Logical lengths exclude allocator capacity, metadata, header tables, intermediate arrays, compiler temporaries, and retained caller state.
They do not establish physical/process memory bounds, aggregate admission, peak live memory, or production worker qualification.
Cancellation checks surround each plane allocation, each component/block stage, transform rows, output-block rows, and final success.
Bulk allocation and zero fill do not contain internal cancellation polls.
Trusted callback costs remain unmetered, and cooperative checks do not establish a hard deadline or CPU bound.

## Independent finite reference

The new offline C tool uses public `jpeg_read_raw_data` with `JDCT_ISLOW` in libjpeg-turbo 2.1.5.
It returns raw component samples before chroma reconstruction or color conversion.
The tool uses the existing signed workspace reference packages and static test library.
It is not a Fern runtime dependency and does not replace either earlier reference executable.

The new bundle contains 108 generated and synthetic inputs with 256 component planes.
It preserves every input from the published grayscale bundle, including its three-component rejection controls.
Those controls are valid raw-component inputs here, without any implied encoded color interpretation.
Coverage includes 4:4:4, 4:2:2, 4:2:0, fractional 3/2/1 sampling, sole-component 4-by-4 factors, arbitrary identifiers, and nondefault table destinations.
All independent observations must complete without C warnings.

The collector checks component order, identifiers, factors, dimensions, strides, exact storage lengths, and total byte counts.
Every sample must differ from the integer reference by at most one value, as the isolated IDCT criterion specifies.
Raw records retain each observed maximum, difference count, output hash, and reference hash.
This finite criterion is not a universal error bound, color-quality result, or codec-parity claim.
A fresh worker handles at most eight inputs and must report nonzero balanced heap counts and zero final live bytes.

Each campaign locks compiler, source, reference, and tool hashes and verifies them again after cleanup.
Failures retain completed prefixes and failed input/output evidence and still return failure.
The 20-second worker timeout is an operational test limit, not a decoder deadline guarantee.
Seven TAP groups cover independent zero samples, cropped planes, sampling, identifiers, quantizers, exact limits, ownership, every prefix, late failures, and every observed cancellation poll.
Protocol fault controls reject incorrect metadata, component samples, storage, protocol structure, and heap censuses.
Generated finite images do not qualify photographs, broad compatibility, unbounded fuzzing, security, throughput, physical bounds, or production operation.

## Reproduction

Run these commands from `fern-image` with a normal repository compiler build:

```sh
FERN="$PWD/../bin/fern" ./scripts/test-jpeg-planes.sh native
FERN="$PWD/../bin/fern" ./scripts/test-jpeg-planes.sh wasm
QEMU_AARCH64=/path/to/qemu-aarch64 FERN="$PWD/../bin/fern" ./scripts/test-jpeg-planes.sh arm64
python3 -m unittest discover -s tests -p qualify_jpeg_planes_test.py
```

CI uses the committed bundle without an external C codec installation.
The optional `JPEG_PLANES_ORACLE` selector repeats the C observations locally.
The reference package identities and build flags appear in [IDCT qualification](jpeg-idct.md).
Build `tests/reference_jpeg_planes.c` against that same offline static library.
The new executable SHA256 is `c4595f232739c224a928654d57e4282e9a47e44b10ea069cdb769b4607090961`.
New raw records use `jpeg-planes-*` names and preserve all earlier datasets.

Regenerate the bundle with the offline tool:

```sh
python3 tests/generate_jpeg_planes.py build/jpeg-planes-inputs --oracle /path/to/jpeg-planes-reference \
  --bundle tests/fixtures/jpeg-planes-reference.json.gz --fern-fixtures tests/fixtures/jpeg_planes.fern
"$PWD/../bin/fern" -fmt -w tests/fixtures/jpeg_planes.fern
```

M5 and the epic remain open.

## Recorded qualification

The new records are [native](../benchmarks/results/jpeg-planes-native.json), [Wasm](../benchmarks/results/jpeg-planes-wasm.json), and [ARM64](../benchmarks/results/jpeg-planes-arm64.json).
Their source commit is `901c77da549494fab0edf7671e48c1ad71521af5`.
The clean compiler reports `dfa23fa508f9`, with SHA256 `68b47eac8e56bb44944fcde126bb29d80ad2173afb7ac57eaf6c52e7b0f44282`.
Compiler and stdlib sources match the integrated main revision `974346bb9d9d1dc8082842b832c477d9ceed5a6b`.

Each target completed 108 inputs and 256 component planes across 14 fresh Fern workers.
All 42 workers reported nonzero balanced heap counts and zero final live bytes.
Component metadata and storage lengths matched the independent reference exactly.
The three target sample records match on these finite inputs.
Native qualification locks 529 source and tool hashes, while runner targets lock 530.
These include all 163 stdlib sources and the compiler's Fern sources.

Across 65,613 component sample positions, 1,158 differ from the integer reference, always by one value.
The collector retains these differences explicitly.
The local campaigns repeat all independent C observations and verify immutable source and tool hashes.
Reference regeneration reproduces the bundle and formatted golden fixtures exactly.
The bundle SHA256 is `c06ba56602c497d76a5a8040535df4ccbfc83e2d8c8842e25c39ff0029a0ac62`.
These observations do not establish universal precision, codec parity, chroma quality, or encoded color compatibility.

All 191 image TAP cases passed on native sanitizer, Wasm leak census, and standalone ARM64/QEMU10.
Full native and Wasm scripts completed with 66 and 42 balanced heap censuses respectively.
The portable ARM run completed 27 executables with balanced heaps and zero final live bytes.
The primary interpreter passed all seven new TAP groups.

Both unchanged complexity gates passed without skips.
Eight protocol controls, 1,317 Fern format checks, Go build/vet, workflow lint, selectors, and CI self-tests passed.
These local observations are separate from GitHub CI status.
