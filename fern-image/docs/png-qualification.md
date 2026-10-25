# PNG differential and mutation qualification

Issue #13338 belongs to PNG milestone #13214 and cross-cutting suite #13223.
The finite corpus contains 964 generated inputs.
It exercises the restricted noninterlaced RGB8/RGBA8 codec.
It does not qualify photographs, broad PNG compatibility, or production safety.

## Corpus and references

The corpus uses gradients and deterministic noise with seed `0xF3A123`.
It includes one-pixel dimensions, odd dimensions, alpha bytes, and compression levels 0, 1, 6, and 9.
Pillow generates 128 valid files across RGB and RGBA modes.
Ten additional files force all five scanline filters at a larger width than the small golden fixtures.
Pillow independently verifies every forced-filter fixture before the campaign starts.

Three files split IDAT into consecutive pieces, with empty IDAT chunks at both ends.
One file contains an allowed ancillary chunk.

The negative corpus includes unsupported modes, header fields, metadata, and invalid dimensions.
It includes duplicate or missing chunks, nonconsecutive IDAT, trailing bytes, wrong scanline lengths, and an expansion payload.
The corpus tests every truncated prefix of one seeded PNG and one bit change at every byte position.
Another 128 mutations change compressed bytes and repair the PNG chunk CRC.
These mutations reach the native compressed-stream validator.
Python zlib checks their strict frame and output length before Pillow classifies a valid decoded result.

Each accepted Fern decode must match the independent reference in dimensions, channel count, and every pixel byte.
The Fern worker also encodes that image.
Pillow must decode the result to exactly the same bytes.
Every negative case must return an explicit codec error.
A crash, a timeout, an unexpected error, or an unexpected success fails the campaign.

Pillow 12.3.0 is an offline reference dependency.
The requirement file pins its CPython 3.12 Linux x86-64 wheel by SHA-256.
Native image processing does not call Pillow or Python zlib.
The generated patterns use no external artwork.
This work does not assign a dataset license to the repository.
External photographs and their license records remain unfinished under #13223.

## Limits and records

Each input is at most 65536 bytes.
The worker admits at most 65536 pixels and 262144 decoded bytes.
Its encoded output limit is 1048576 bytes.
Each process accepts at most sixteen files, and each subprocess has a twenty-second deadline.
The collector runs one process at a time.
These limits do not establish an aggregate process memory bound or a production CPU budget.

The corpus uses 61 fresh batches per target.
Each batch must exit successfully and report balanced allocations with zero final live bytes.
Native and ARM64 executables use heap sanitization.
Wasm enables the leak census without the native use-after-free detectors.
The ARM64 record uses QEMU on an x86-64 host.
It does not qualify native ARM hardware performance.

All three recorded campaigns pass 964 cases across 61 fresh batches each.
Each target accepts 142 files and rejects 822 files.
Every batch reports zero final live bytes.
Accepted pixel bytes and error classes agree across the three targets.
The separate existing 76 image TAP cases also pass natively and in Wasm with this compiler.

The recorded compiler reports `b54ed262ae03-dirty` after integration of upstream main `407ba9a0d3de11b4d76e123ec414815fb3a55bad`.
The collector was the only modified file during that build.
Compiler and stdlib sources did not change between that integration and recorded source commit `dce2906c7ef4170f763d9868d8ce9c0fb780ed10`.
Each record hashes all 162 workspace stdlib sources, including literate modules.
The raw hashes identify the exact compiler binary and test executable.
Full current-head GitHub CI remains a separate gate.

The record includes each case name, input hash, expected outcome, observed error, and accepted output hashes.
It also records compiler, executable, collector, source, stdlib, Python, Pillow, and reference-library hashes.
Wasm and ARM64 records include their runner hash and version.
The collector checks source and tool immutability before and after execution.
Input files must also remain unchanged.
On failure, the collector preserves the current batch and its case manifest beside the requested output record.

The raw records reside in `benchmarks/results/png-corpus-native.json`, `png-corpus-wasm.json`, and `png-corpus-arm64.json`.
They preserve observations from the compiler and reference versions inside each record.
Input hashes can change with a different Pillow or zlib implementation.
No wall-time, throughput, RSS, visual-quality, or performance-parity claim follows from these records.

## Reproduce

Use CPython 3.12 on Linux x86-64 for the pinned reference wheel.
Run these commands from the repository root:

```sh
python3 -m venv /tmp/png-reference
/tmp/png-reference/bin/python -m pip install --require-hashes -r fern-image/tests/requirements-reference.txt
PYTHON=/tmp/png-reference/bin/python FERN="$PWD/bin/fern" ./fern-image/scripts/test-png-corpus.sh native
PYTHON=/tmp/png-reference/bin/python FERN="$PWD/bin/fern" ./fern-image/scripts/test-png-corpus.sh wasm
QEMU_AARCH64=/path/to/qemu-aarch64 PYTHON=/tmp/png-reference/bin/python \
  FERN="$PWD/bin/fern" ./fern-image/scripts/test-png-corpus.sh arm64
```

Use the repository's pinned Wasmtime version for the Wasm command.
The scripts write fresh records below `fern-image/build/`.
The native and Wasm CI image jobs run the same campaigns and upload their records.
No missing dependency produces a skipped qualification.
The existing seven PNG TAP cases and the wider image suites remain separate checks.
