# Restricted JPEG grayscale sample decoding

Issue #13613 is a native child of JPEG milestone #13216.
This Fern slice combines the checked header, coefficient fold, and scalar inverse DCT for one complete single-component scan.
It returns raw component samples repeated in packed RGB8 storage.
It does not decode three-component color images or complete the JPEG milestone.

## Admitted profile

The [header profile](jpeg-headers.md) requires SOF0, eight-bit precision, in-input quantizers and Huffman tables, and a complete baseline scan.
This decoder additionally requires one component.
Component identifiers need not equal one, and valid single-component sampling factors do not change its one-block MCU rule.
The decoder crops the right and bottom edges of partial 8-by-8 blocks.
It selects the component's declared quantizer and the scan's declared Huffman destinations.

The scan retains the [coefficient fold's](jpeg-coefficients.md) strict byte-stuffing, Huffman, run, padding, restart, DC-reset, EOI, and consumption checks.
It also retains the [inverse transform's](jpeg-idct.md) explicit coefficient domain, rounding, clipping, and errors.
Unsupported channel counts, progressive coding, CMYK, and other unsupported header profiles return an explicit unsupported error.
The result is not a claim of broad baseline-JPEG compatibility.

Metadata remains opaque.
The decoder does not apply EXIF orientation, ICC profiles, transfer functions, or other color management.
It does not preserve metadata in its image result.
RGB8 names the storage arrangement here, not a profile-aware color conversion.
The image dimensions and sample order remain those of the encoded component.

## API and validation order

Import `packages/codecs/jpeg/grayscale` and call `decode(input, limits, cancelled)`.
`default_limits()` returns the shared `entropy.BlockLimits` type.
Input is a complete borrowed byte view.
The result owns a `pixels.Image` with RGB8 format, stride `width * 3`, and exactly `stride * height` bytes.
Each pixel contains three identical sample bytes, and pixel reads report alpha 255.

The decoder calls `entropy.inspect_blocks` on its own input.
This validates header, layout, MCU count, and decoded-block count before pixel-buffer allocation.
The inspection API alone does not validate the entropy payload or EOI.
The decoder then performs the complete strict fold and inverse transform before it returns an image.
It never accepts a caller-constructed header or exposes a partial image.

The header is deliberately parsed again by the existing entropy fold.
This preserves its validation path and avoids a trusted-header shortcut.
A later entropy, transform, restart, padding, or EOI error releases the private partial frame.
Cancellation also releases that frame, including after final entropy validation.
The operation does not roll back external effects of the cancellation callback.

The image writer keeps its state in a private data-only cell.
It clears the cell before each block update and supplies a fresh image record to its consuming writer.
This obeys the shared-value contract of `Cell.get` and preserves ordinary array copy-on-write semantics.
The existing entropy accumulator callback signature and behavior remain unchanged.
Independent calls create independent frame state and owned image results.

## Errors and limits

`DecodeUnsupported` identifies the unsupported profile.
`DecodeCancelled` combines cancellation from parsing, entropy, inverse DCT, and pixel output.
Other failures retain their details in `DecodeEntropy`, `DecodeTransform`, or `DecodeStorage`.
The API does not convert allocator failure into a recoverable decoder error.

The shared defaults admit at most 16 MiB of supplied input and 1 MiB through the scan header.
Header marker, pixel, projected RGB8 byte, MCU, and decoded-block limits also apply.
The MCU and block hard caps are 1,048,576 each.
The checked RGB8 layout has a 64 MiB hard byte cap.
Callers can select lower limits through the shared limit records.

These are logical admission limits, not physical, process, aggregate, allocator-capacity, or peak-live-memory bounds.
The full encoded view remains available throughout decoding, and the complete output frame is eager.
Header tables, intermediate arrays, compiler temporaries, and arbitrary callback allocations add costs outside output-byte counts.
A service still needs separate physical admission and worker qualification.

Cancellation checks occur in the existing header and entropy paths, between inverse-transform rows, and during output-block rows.
The decoder also checks before and after pixel-buffer allocation and before success.
The bulk allocation and zero fill do not contain an internal cancellation poll.
The callback remains trusted code with unmetered work and allocation costs.
Cooperative polling does not establish a hard deadline or CPU bound.

## Finite independent reference

The new bundle contains 108 generated and synthetic JPEG inputs.
Thirty-four one-component files have independent grayscale observations from libjpeg-turbo 2.1.5 with `JDCT_ISLOW`.
The other 74 valid three-component files are explicit unsupported-profile controls.
They are not malformed-JPEG observations or evidence of color-decoder compatibility.

The input set includes the earlier coefficient corpus without changing that published bundle.
Three new controls use nonuniform single-component samples with 4-by-4 factors, identifier 201, or nondefault quantizer and Huffman destinations.
The independent C decoder accepts each new control without warnings.
The generator records encoded bytes, expected dimensions, grayscale samples, source identities, and exact C output hashes.
The C library is an offline test tool and is not part of the Fern runtime.

The target collector checks dimensions, stride, storage length, profile status, and every output sample.
All three channels must match exactly within each pixel.
The finite reference criterion permits at most one sample value of difference from the integer decoder, as the IDCT qualification documents.
Each record retains actual maxima, counts of different samples, and output and reference hashes.
This criterion is not a universal error guarantee or a codec-parity claim.

A fresh process handles at most eight inputs and must report nonzero balanced heap counts and zero final live bytes.
Each target campaign checks source and tool hashes before and after execution.
The collector retains completed prefixes and failed input/output artifacts and still returns failure on a mismatch.
Its 20-second process timeout is an operational test limit, not a decoder deadline guarantee.

Eight TAP groups cover independent zero samples, dimensions, cropped edges, real fixtures, sampling, identifiers, and unsupported profiles.
They also cover exact admission, every truncated prefix, padding, EOI, trailing bytes, restarts, transform-domain failures, ownership, and every observed cancellation poll.
The Python fault controls reject incorrect pixels, channel replication, metadata, storage, status, heap census, and protocol structure.
Generated small images do not qualify photographs, perceptual quality, broad compatibility, unbounded fuzzing, security, throughput, or production operation.

## Reproduction

Run the target scripts from `fern-image` with a normal repository compiler build:

```sh
FERN="$PWD/../bin/fern" ./scripts/test-jpeg-grayscale.sh native
FERN="$PWD/../bin/fern" ./scripts/test-jpeg-grayscale.sh wasm
QEMU_AARCH64=/path/to/qemu-aarch64 FERN="$PWD/../bin/fern" ./scripts/test-jpeg-grayscale.sh arm64
python3 -m unittest discover -s tests -p qualify_jpeg_grayscale_test.py
```

CI uses the committed bundle without a C codec installation.
The optional `JPEG_GRAYSCALE_ORACLE` selector repeats the C observations locally.
The oracle and signed reference-package identities appear in [IDCT qualification](jpeg-idct.md) and [coefficient qualification](jpeg-coefficients.md).
The new raw records use `jpeg-grayscale-*` names and preserve all earlier datasets.

Regenerate this bundle with the existing offline reference executable:

```sh
python3 tests/generate_jpeg_grayscale.py build/jpeg-grayscale-inputs --oracle /path/to/jpeg-idct-reference \
  --bundle tests/fixtures/jpeg-grayscale-reference.json.gz
```
