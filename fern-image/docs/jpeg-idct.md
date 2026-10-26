# Scalar JPEG dequantization and inverse DCT

Issue #13603 is a native child of JPEG milestone #13216.
The coefficient prerequisite is #13602.
This scalar Fern kernel reconstructs one isolated component block.
It does not assemble a JPEG image or implement chroma reconstruction, color conversion, orientation, profiles, or encoding.
M5 and the epic remain open.

## Mathematical contract

The reference is [ITU-T T.81](https://www.w3.org/Graphics/JPEG/itu-t81.pdf), A.3.1, A.3.3, and A.3.4.
The PDF SHA256 is `631031d4ba56b06abee3e312a0f235b9422da9c7267d1c8f7604418795768bf0`.
Dequantization multiplies each quantized coefficient by its corresponding quantizer.
The inverse transform uses the cosine basis and normalization in A.3.3.
The inverse level shift adds 128 for eight-bit samples, then clips to the sample range.

The kernel evaluates a separable transform in binary64 arithmetic.
Its 64 basis constants come from an independent 90-digit Decimal calculation.
The mathematical reference evaluates the direct two-dimensional sum at that precision.
The public numerical domain is explicit and narrower than arbitrary `i32` values.
Finite agreement does not establish Part 2 precision conformance or complete JPEG compatibility.

A DC-only block uses its exact dequantized DC value divided by eight.
The integer dequantization product fits i64 and is exact in binary64 within the accepted domain.
Division by eight also has an exact binary representation here.
This path preserves DC half-sample ties that cosine-product error can otherwise displace.
It still validates every coefficient and quantizer and polls cancellation at output-row boundaries.

## API and numeric domain

Import `packages/codecs/jpeg/idct`.
`inverse(coefficients, quantizers, cancelled)` returns 64 owned, unshifted binary64 samples in row-major order.
The input contains exactly 64 natural-order quantized coefficients, each from -32768 through 32767.
The 64 quantizers contain nonzero eight-bit values in transmitted zig-zag order, as the header parser returns them.
Invalid lengths, out-of-domain coefficients, and zero quantizers return distinct errors.

`samples(values, cancelled)` accepts exactly 64 finite binary64 values.
It adds 128, rounds to the nearest integer with ties toward positive infinity, and clips to zero through 255.
This rounding contract applies to the supplied binary64 values, rather than an ideal real transform.
The function checks finiteness and clips before integer conversion.
NaN and either infinity return `NonFinite`, while the largest finite magnitudes clip normally.

`reconstruct(coefficients, quantizers, cancelled)` combines those two operations.
All successful results own their arrays and do not retain input views.
Callers can supply borrowed windows, and array updates retain the usual copy-on-write semantics.
The API does not interpret component identifiers, chroma factors, or color meaning.
A complete decoder must supply the correct component quantizer and handle image edges separately.

The error variants are `Shape`, `Quantizer`, `Coefficient`, `NonFinite`, and `Cancelled`.
There is no diagnostic or numeric-domain exemption for a particular fixture or coefficient position.
The kernel checks all coefficients and quantizers, including unused AC quantizers in a DC-only block.
This restricted kernel does not expand the entropy decoder's compatibility claims.

## Resources and cancellation

Each input and output array contains 64 logical elements.
The general inverse path uses fixed-size intermediate arrays for dequantization and the first transform pass.
These lengths do not bound allocator capacity, compiler temporaries, metadata, retained caller state, or process memory.
Allocation failure remains a runtime concern rather than a recoverable error from this API.

Cancellation checks occur during input validation, before transform rows, and before final success.
The DC-only path also checks each output row.
The sample-conversion path checks each group of eight values and its final result.
A callback is trusted code with unmetered CPU and allocation cost.
Fixed loops between checks do not establish a hard deadline or CPU bound.

## Independent finite references

The bundle contains 579 block inputs.
It includes DC endpoints and ties, signed impulses at all 64 basis positions, dense blocks, and four complete-domain edge patterns.
Quantizers vary independently and cover the transmitted-to-natural-order mapping.
The mathematical reference uses 90-digit Decimal arithmetic, a convergent cosine series, and the direct two-dimensional equation.
It does not evaluate the Fern separable binary64 table.

The same bundle also contains 222 blocks from 31 grayscale JPEG fixtures.
Their quantized coefficients come from the committed independent coefficient reference.
A separate libjpeg-turbo 2.1.5 program supplies natural-order quantizers and grayscale samples through `JDCT_ISLOW`.
The local collector can repeat those observations and verify coefficients, quantizers, coordinates, and cropped visible samples.
The library remains an offline test tool, not an application dependency.

The declared finite criteria require absolute unshifted-sample error at most 0.000001 against the mathematical reference.
They also require exact conversion of the observed binary64 samples and visible grayscale differences at most one sample value.
These tolerances are test criteria, not universal error bounds or standards certification.
The records retain actual maxima and all observed rounding differences.
No tolerance, coefficient domain, or fixture exemption expands after a failed observation.

Ideal math and the integer reference differ at 139 of 9,543 visible grayscale positions, by at most one value.
These independent reference differences remain visible in the evidence.
They do not justify a claim of bit-identical output or broad codec parity.
Photograph coverage, image quality, complete decoding, security, performance, and production acceptance remain unqualified.

Each fresh worker handles at most eight block inputs and must report nonzero balanced allocations and zero final live bytes.
The collector imposes a 20-second process timeout and runs one worker at a time per target.
That operational timeout is not a kernel deadline guarantee.
Failed records retain completed prefixes, the active input batch, and available worker output, and the command still fails.
Source and tool hashes must remain unchanged through a successful campaign.

The five TAP groups cover exact DC rounding, basis orientation, quantizer order, numeric domains, shapes, nonfinite values, ownership, and cancellation.
Both DC and mixed-frequency cancellation paths test every observed poll.
Borrowed coefficient and quantizer windows test nonzero offsets.
Eight Python fault controls require rejection of wrong numeric values, nonfinite output, rounding, codec observations, heap censuses, and protocol structure.

## Reproduction

Run the target campaigns from `fern-image` after a normal repository compiler build:

```sh
FERN="$PWD/../bin/fern" ./scripts/test-jpeg-idct.sh native
FERN="$PWD/../bin/fern" ./scripts/test-jpeg-idct.sh wasm
QEMU_AARCH64=/path/to/qemu-aarch64 FERN="$PWD/../bin/fern" ./scripts/test-jpeg-idct.sh arm64
python3 -m unittest discover -s tests -p qualify_jpeg_idct_test.py
```

CI uses the committed bundle and requires no C codec installation.
The optional `JPEG_IDCT_ORACLE` selector repeats all 31 grayscale reference observations before worker execution.
The new C source uses the extracted, signed reference packages documented in [coefficient qualification](jpeg-coefficients.md).
Its current oracle SHA256 is `0aa2bcb1dc9e598c9c51b7f188c2375f4f602f14f3fecd913616b7fafb44740b`.
The existing coefficient oracle and its published datasets remain unchanged.

Regenerate the new bundle and inspect the independently computed basis literals with this command:

```sh
python3 tests/generate_jpeg_idct.py build/jpeg-idct-inputs --oracle /path/to/jpeg-idct-reference \
  --bundle tests/fixtures/jpeg-idct-reference.json.gz --basis build/idct-basis.txt
```

The generator records the Python, Decimal, source, oracle, and coefficient-bundle identities.
The target collector also hashes all compiler and standard-library Fern sources and its selected runner.
New raw records use `jpeg-idct-*` names and preserve all earlier image measurements.
