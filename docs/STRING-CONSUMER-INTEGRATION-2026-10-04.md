# Byte-consumer integration

This integration brings the prepared byte-consumer chain at `b0b5245c8`
onto main `a1f0f5192`. It preserves binary records, comparisons, checksums,
transformations and output without using text as their storage type.
Borrowing byte kernels support those consumers. Extended attributes have
byte APIs; buffered text readers and TZif parsing validate complete text.

This is a prerequisite for changing `buf_take` to return valid UTF-8.
Main still used that operation for binary data in utilities including
Tac, Shred, Sort, Split and DD. The integrated consumers use raw extraction
for those paths. The remaining coreutils text extractions construct text.
The builder contract itself and the PEG capture follow-up are separate
changes. Issue #5714 remains open.

## Validation at publication

The frozen integration contains 8,097 files. All 5,736 Go, Fern and golden
files were compared with the working tree before publication and match.

- The Go interpreter kernel pilot passes in 2.021 seconds.
- Primary opcode registry, constructor, admission, builder-map and shred
  pattern checks pass in 20.591 seconds.
- The full Linux unit suite and `make lint-all` pass.
- A fresh Linux ARM64 bootstrap using the published
  `stage0-20261004-ef49ae0` pin reproduces stage 2 as stage 3: 13,064,384 bytes,
  SHA-256 `2f2fa9b39c0b681c4af4dea95babe8733d3db1784cd3d35c03cc8f152edd0ccf`.
  Stage 1 differs from stage 2.

The complete native/WASM regression selection, GNU parity, Darwin validation
and PR CI are still running or pending. The PR remains a draft until these
gates are complete. Darwin validation includes PTX because this integration
removes its old known-failure entry.

## Measurements

The dated per-migration reports retain their original compiler hashes,
source revisions, benchmark samples, allocation counts and size attribution.
Those measurements describe their recorded snapshots. This integration does
not claim new performance measurements or increase a size baseline.

The builder benchmark will use the integrated consumer baseline once that
dependency is settled. Its earlier compiler-only validation is insufficient
to claim that binary consumers work with the new text contract.
