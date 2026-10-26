# Restricted JFIF RGB8 assembly

`packages/codecs/jpeg/rgb.fern` decodes the restricted three-component JFIF profile into an owned packed RGB8 image.
The API combines the existing profile inspector, raw component decoder, centered chroma filters, and exact sample converter.
The core uses Fern and has no external runtime codec dependency.
This interface does not provide general JPEG compatibility or color management.

## Profile and API

`rgb.inspect(input, limits, cancelled)` returns a checked `RgbPlan` from the first-scan header.
`rgb.decode(input, filter, limits, cancelled)` returns a complete `pixels.Image`.
The explicit filter is `chroma.Filter.Nearest` or `chroma.Filter.Bilinear`.
The input is a borrowed encoded view, and the output is eager owned RGB8 storage.

The input must satisfy the [restricted JFIF profile](jpeg-jfif.md).
Its first APP0 declaration specifies JFIF 1.00 through 1.02, and its frame components must be 1, 2, and 3 in that order.
These components mean Y, Cb, and Cr.
The separate raw grayscale API retains its previous scope.
Additional APP0 segments and all APP1 through APP15 segments remain unsupported.
This rejects ICC, EXIF, Adobe, JFXX, and unknown application metadata before color assembly.

The decoder retains the restricted SOF0, quantizer, Huffman, scan, restart, and numeric domains of its component APIs.
It decodes all raw planes and validates the complete entropy stream, final EOI, and exact consumption before RGB allocation.
Later errors and cancellation never return a partial image.
The output stride is exactly three bytes per frame pixel.
The image uses top-down JFIF sample interpretation without an extra gamma correction.

The centered coordinate is `(x + 0.5) * component_factor / maximum_factor - 0.5` on each axis.
Nominal factor spacing precedes visible edge cropping.
Nearest uses upward index ties, and bilinear uses one final exact integer average with upward half ties.
All three components use this reconstruction rule, including a component that has less than maximum resolution.
The sample converter uses the printed JFIF coefficients from page 3 at exact scale 100,000.
See [component rows](jpeg-chroma.md) and [sample conversion](jpeg-ycbcr.md) for those contracts.

## Named logical storage plan

`RgbLimits` contains `planes`, `max_output_bytes`, and `max_planned_bytes`.
Both byte limits must be positive.
Defaults use 64 MiB for each named byte limit and preserve the existing plane and header defaults.
The output layout, component storage, MCU count, and block count retain their previous admission rules.

The plan reports these logical byte lengths:

- `input_bytes`: encoded input view length
- `planes.bytes`: sum of visible component plane lengths
- `layout.bytes`: packed RGB output length
- `row_bytes`: six times the frame width for three component rows and one RGB row
- `planned_bytes`: the sum of those four values

The decoder checks the complete sum against `max_planned_bytes` and an absolute logical ceiling of 64 MiB.
It checks output bytes separately against `max_output_bytes`.
Both checks precede component plane allocation and RGB allocation.
Header table and plan construction still occur before this sum is available.

This sum excludes tables, block and compiler temporaries, array capacity, metadata, allocator overhead, and caller retention beyond the counted input view.
It does not establish peak live storage, RSS, aggregate worker memory, or a process memory bound.
The input, planes, and output remain eager.
Header and plan checks repeat through the component APIs, which adds work.
No timing or performance improvement claim follows from this composition.

## Errors and cancellation

`Profile(error)`, `Plane(error)`, `Chroma(error)`, and `Conversion(error)` preserve the responsible layer.
`Unsupported` rejects the separate grayscale profile.
`Limit` reports a top-level byte admission failure.
`Storage` rejects an inconsistent internal result.
`Cancelled` combines cancellation from every layer.
Structural header checks precede the profile and byte-plan checks.

Cancellation polls propagate through header, entropy, inverse DCT, component rows, and sample conversion.
The assembler also polls before and after RGB allocation, at groups of 64 pixels during row copies, and before success.
These checks are cooperative and provide no hard deadline.
Owned aliases use normal copy-on-write semantics.
Fresh and nested calls have independent state.

## Independent finite references

The new reference bundle derives inputs from the published raw-plane bundle without changing that bundle.
It adds canonical identifiers, a comment control, unsupported metadata controls, and a neutral 257-by-9 fixture.
The source includes ordinary and optimized Huffman tables, restart markers, odd dimensions, and multiple sample factors.
The C raw-component oracle reruns the earlier observations unchanged.
A separate new C executable uses public libjpeg-turbo 2.1.5 APIs with `JDCT_ISLOW` and non-fancy RGB output.
Both C tools remain offline references only.

Independent Python `Fraction` arithmetic computes nominal sample coordinates, one final bilinear average, and the printed JFIF conversion equations.
Every assembled RGB byte must exactly match rational composition of the worker's own raw component samples.
The independent C raw samples retain their earlier maximum difference of one.
The RGB comparison against those raw samples uses the predeclared maximum channel difference of three.
The supplemental C RGB comparison uses a maximum difference of four for nearest reconstruction with integer sample ratios.
The C RGB oracle does not support fractional sample ratios, while the primary rational reference covers them.

These finite criteria are not universal error bounds, standards qualification, codec parity, or perceptual quality claims.
The issue records the criteria before the campaign.
A comparison failure requires cause analysis, not a larger tolerance.
The collector also requires exact metadata and plans, explicit unsupported outcomes, nonzero balanced heap counts, and zero final live bytes.
It retains failed observations and rejects partial or extra output.
Final campaigns require a clean source commit and verify immutable source and tool hashes.

## Commands

Run the following commands from the repository root with a compatible compiler:

```sh
FERN="$PWD/bin/fern" ./fern-image/scripts/test-jpeg-rgb.sh native
FERN="$PWD/bin/fern" ./fern-image/scripts/test-jpeg-rgb.sh wasm
QEMU_AARCH64=/absolute/path/to/qemu-aarch64 FERN="$PWD/bin/fern" ./fern-image/scripts/test-jpeg-rgb.sh arm64
python3 -m unittest discover -s fern-image/tests -p qualify_jpeg_rgb_test.py
```

`JPEG_RGB_RAW_ORACLE` and `JPEG_RGB_ORACLE` enable optional reruns of the two independent C tools.
`JPEG_RGB_ALLOW_DIRTY=1` permits provisional local experiments and marks their records as dirty.
Dirty records do not qualify as final source-pinned campaign evidence.
New published records use `jpeg-rgb-*` names and preserve all earlier datasets.
M5 and the image-engine acceptance gates remain open.

## Qualification inputs and tools

The reference bundle contains 119 encoded inputs: 76 accepted profiles and 43 unsupported or profile controls.
Both filters produce 152 accepted RGB observations per target.
Independent C RGB observations cover 75 accepted inputs with integer sample ratios.
The fractional input uses the independent raw-component and rational references.

The bundle reproduces byte-identically with SHA256 `2947f368cbba2ce312322f1ef88eb1723db0b708acd94075ad89803e867d5cee`.
Its formatted Fern goldens also reproduce byte-identically.

The clean compiler revision is `6a28836a28abe9c2bf6905bb3a24445b195a7151`.
Its SHA256 is `99bbb2558676f1e59603bdbb49589ba645b37c86c5fac54fa1a5e08194be34a2`.
Compiler and stdlib sources match the integrated main source.
A standard two-stage build and bootstrap smoke checks pass.

The new C RGB reference SHA256 is `2c2dd1e8c6dc1267789d995e26e42391bf62a171f7a305723179b680896d5e56`.
The earlier raw-plane C executable and datasets remain unchanged.

Nine TAP groups pass on native sanitizer, Wasm leak census, standalone ARM64/QEMU10, and the primary interpreter.
Native and ARM report 713,169 balanced allocations, while Wasm reports 927,318.
Every target reports zero final live bytes.
The tests cover finite color goldens, exact plans, inherited limits, unsupported profiles, all tiny stream prefixes, and late restart errors.
They also cover retained outputs, caller alias updates, fresh nested calls, every tiny cancellation poll, and selected wide post-plane cancellation points.
Ten protocol controls and both unchanged complexity gates pass without skips.
