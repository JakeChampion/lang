# Baseline JPEG coefficient fold

Issue #13588 is a native child of JPEG milestone #13216.
This Fern API decodes a restricted baseline entropy scan into owned, quantized 8-by-8 coefficient blocks.
It does not produce pixels.
IDCT, dequantization, chroma reconstruction, color conversion, and JPEG encoding remain unfinished.
Milestone #13216 and the image epic remain open.

## Profile and framing

The syntax reference is [ITU-T T.81](https://www.w3.org/Graphics/JPEG/itu-t81.pdf), Annex B and Annex F.
The reference PDF has SHA256 `631031d4ba56b06abee3e312a0f235b9422da9c7267d1c8f7604418795768bf0`.
The [header parser](jpeg-headers.md) admits SOF0, eight-bit samples, and one or three distinct components.
The scan contains every component in frame order, independent of numeric component identifiers.
It requires the referenced quantizer and Huffman tables in the same input.
Progressive, arithmetic, abbreviated-table, partial-component, and multiple-scan images remain unsupported.

The entropy reader supports canonical Huffman codes, signed amplitudes, DC prediction, EOB, ZRL, and the transmitted zig-zag order.
A stuffed `FF 00` represents an entropy byte with value 255.
Unused bits before a marker must be ones.
Restart markers follow the declared interval and cyclic RST0 through RST7 sequence.
Each restart resets all DC predictors.
After the prescribed blocks, the reader requires EOI and exact consumption of the supplied input view.

Marker fill bytes remain valid at restart and EOI boundaries.
Unexpected entropy markers, malformed codes, illegal categories, coefficient-run overflow, incorrect padding, and incorrect restarts return explicit errors.
A missing EOI or trailing bytes also fail.
The parser does not interpret JFIF color meaning, EXIF orientation, Adobe transforms, or ICC profiles.
Successful coefficient decoding does not establish support for those features.

## API and ownership

Import `packages/codecs/jpeg/entropy`.
Call `fold_blocks(input, limits, cancelled, value, consume)` with a borrowed byte view and an owned accumulator.
The consumer accepts the accumulator and one owned `CoefficientBlock`, then returns the next accumulator or its own error.
The generic result distinguishes `BlockDecode(EntropyError)` from `BlockConsumer(E)`.
The fold parses and validates its own header instead of accepting caller-constructed header records.

A block contains its component identifier, horizontal block coordinate, vertical block coordinate, and 64 signed quantized coefficients.
The coefficient array uses natural row-major 8-by-8 order, rather than transmitted zig-zag order.
The component field contains the JPEG identifier, not its array index or a color-channel interpretation.
Consumers can retain blocks or update their arrays through the usual copy-on-write rules.
No result field borrows the input.

An interleaved scan visits MCUs in raster order, then components in frame order, then their vertical and horizontal data units.
The reader decodes edge padding blocks to preserve prediction and framing, but does not emit them outside the component extent.
A single-component scan uses one block per MCU, regardless of its frame sampling factors.
`BlockPlan` reports MCU dimensions, MCU count, decoded block count, and emitted block count.
`BlockFold` returns the final accumulator, owned header, plan, and exact consumed byte count.

Consumer calls can occur before a later malformed block, restart, padding, or EOI error.
Callers must observe overall success before they trust the complete stream.
The API does not roll back external consumer effects.
A consumer error stops the fold and releases its internal state.
The caller controls any state that it retains outside the fold.

## Limits and cancellation

`BlockLimits` contains the existing header limits plus positive MCU and decoded-block limits.
The default and hard cap for each count is 1,048,576.
The header limits retain their 16 MiB input, 1 MiB header extent, marker count, and checked projected-layout admission rules.
The projected RGB8 layout remains an admission calculation, not a pixel result.
The fold checks the complete plan before the first consumer call.

These are logical count and layout limits.
They exclude allocator capacity, metadata, compiler temporaries, consumer retention, and arbitrary callback allocations.
The API does not promise physical, process, aggregate, or recoverable-allocation memory bounds.
It retains access to the complete supplied encoded view and does not accept a streamed encoded input.

Cancellation checks occur in the header parser, before each block and consumer call, at entropy-byte reads, and during marker fill.
The fold also checks cancellation before its final success.
Bounded symbol and amplitude loops can run between checks.
The consumer and cancellation callback remain trusted code with unmetered CPU and allocation costs.
These checks establish cooperative cancellation, not a hard deadline or CPU bound.

## Independent reference and finite qualification

The offline generator uses Pillow 12.3.0 with its bundled libjpeg-turbo 3.1.4.1 encoder.
A separate C program reads quantized coefficients through libjpeg-turbo 2.1.5.
The C oracle links the reference `libjpeg.a`.
It is not a Fern application dependency.
CI uses the committed reference bundle without a runtime C codec or Pillow installation.

The reference bundle contains 105 valid inputs.
They cover finite grayscale and RGB patterns, four dimensions, 4:4:4, 4:2:2, 4:2:0, optimized tables, and restart settings.
Synthetic streams cover signed coefficients, DC predictors, final AC positions, ZRL, a ten-restart cycle, arbitrary identifiers, fractional sampling, and single-component sampling.
A category-boundary stream checks signed amplitudes and stuffed bytes against the independent oracle.
These generated inputs are not photographs or a broad compatibility corpus.

The collector compares all 64 coefficients, identifiers, coordinates, callback order, dimensions, plans, and exact input consumption.
Each fresh worker handles at most eight inputs and must report nonzero balanced allocations with zero final live bytes.
Each process has a 20-second collector timeout.
That operational timeout is not a decoder deadline guarantee.
Failure records preserve completed prefixes and the active input batch, and the command still fails.

The bundle records input, generator, oracle source, oracle binary, Python, Pillow, and bundled-library identities.
The collector checks source and tool hashes before and after each target campaign, including all compiler and standard-library Fern sources.
The optional `JPEG_COEFFICIENT_ORACLE` selector repeats the C observations and checks their exact stored output hashes.
The generator and oracle source accompany the bundle for independent reproduction.

Fifteen TAP groups also cover invalid prefixes, tables, runs, restart sequences, padding, EOI, and trailing bytes.
They test exact admission, borrowed windows, retained blocks, input and output copy-on-write behavior, heap consumer errors, and every observed cancellation poll.
Eight Python fault controls require rejection of coefficient, coordinate, plan, framing, output, reference-schedule, and heap-census mismatches.
Finite tests do not establish unbounded fuzzing, security, performance, pixel quality, cross-codec parity, or production acceptance.

## Reproduction

Run these commands from `fern-image` after a normal repository compiler build:

```sh
FERN="$PWD/../bin/fern" ./scripts/test-jpeg-coefficients.sh native
FERN="$PWD/../bin/fern" ./scripts/test-jpeg-coefficients.sh wasm
QEMU_AARCH64=/path/to/qemu-aarch64 FERN="$PWD/../bin/fern" ./scripts/test-jpeg-coefficients.sh arm64
python3 -m unittest discover -s tests -p qualify_jpeg_coefficients_test.py
```

The script writes a separate JSON record under `build` for each target.
The new records use `jpeg-coefficients-*` names and preserve all earlier PNG, DEFLATE, worker, and allocator datasets.

## Recorded coefficient campaign

The three new records pin source commit `9ec6fcc938589c3b22ab395e80f18db2106ba970`.
The clean compiler reports `dfa23fa508f9`, SHA256 `68b47eac8e56bb44944fcde126bb29d80ad2173afb7ac57eaf6c52e7b0f44282`.
Its compiler and standard-library sources match the campaign source.
Native Linux, Wasmtime 46.0.1, and ARM64/QEMU10 each pass 105 reference cases in 14 fresh workers.
Every worker reports nonzero balanced allocations and zero live bytes.
All three sample records match exactly.

The records include 524 source and tool hashes on native, and 525 on each runner target.
These include all 163 standard-library Fern sources and the compiler sources.
All hashes remain unchanged through each campaign.
Every campaign repeats the offline C oracle and matches its stored output hashes.
The local process timeout remains an operational collector limit, not a hard decoder guarantee.

The records are [native](../benchmarks/results/jpeg-coefficients-native.json), [Wasm](../benchmarks/results/jpeg-coefficients-wasm.json), and [ARM64/QEMU10](../benchmarks/results/jpeg-coefficients-arm64.json).
They establish finite coefficient agreement only.
They do not qualify IDCT, pixels, photographs, broad compatibility, security, performance, physical memory, or production use.
Full current-head CI remains a separate result.

The offline oracle SHA256 is `45c4ac568b6b1c7ac4e7f55db3a388a3670b87f0a54cce8658011562690074c5`.
It uses Debian libjpeg-turbo packages `1:2.1.5-4` from the signed snapshot `20260925T181338Z`.
The development package SHA256 is `e687e3c8b24a3aa74974928b8e0b07f6df2442187492d85cbcea7dacd91e3aba`.
The runtime package SHA256 is `5504299ad0e3c0a9ca9f962a68a650a040db52f9f416a109e74df48d55601d3f`.
The packages remain extracted reference tools, outside the Fern runtime.

The source also passes all 166 TAP cases in the native script and the 23-executable standalone ARM64/QEMU10 run.
All 62 native and 23 ARM heap censuses balance with zero live bytes.
The native script also passes its allocator, cache, fusion, formatter, and negative compiler probes.
The Go oracle accepts the entropy tests after the explicit unit corrections.
The formatter checks 1,304 tracked Fern sources.
Go build, vet, workflow lint, test-selector checks, and the eight Python protocol controls pass.

The full Wasm image script also passes all 166 TAP cases and its smokes.
All 38 Wasm heap censuses balance with zero live bytes.
The compiler SHA256 remains unchanged through the campaigns and full image scripts.
