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

## Integration fixes

The updated candidate preserves the external merge of main `4183c4420`
into PR #11459 (`9488efda5`). Consumer test commands now put driver flags
before the source path, following the current CLI contract. The WASM-hosted
interpreter returns `Unsupported` for all eight extended-attribute calls;
its compiled call graph no longer pulls in native-only xattr operations.

PTX accepts binary numeric prefixes on the Linux target and rejects them
on Darwin, matching the measured GNU behavior on those targets. Tests keep
identical arguments on both sides, with added malformed and range cases.
The older Bookworm-built GNU 9.12 reference lacks C23 binary parsing, so
Linux PTX validation uses an unmodified GNU 9.12 binary built statically
with glibc 2.39. Its SHA-256 is
`44bf61ab959c17341ddda6ee7b2ebb6cf7789ea173d574c9d35484e57fbea6f3`.
Other utilities retain their existing GNU references.

The preceding v2 run passed units, lint, bootstrap, cache-invalidation
probes, the primary-built playground, multicall/uniq checks and the Go
target group (209.802 seconds). Its primary group ran for 1108.093 seconds
and failed only the CRC32 and I/O-error component compile commands because
of their flag ordering. That run stopped before GNU and primary parity;
it was not a full pass. The current candidate corrects those commands.

The current frozen 8,106-file candidate passes the exact-argument GNU PTX
pilot (1.434 seconds), corrected component and WASM interpreter tests
(58.057 seconds), the full Linux unit suite and all lint gates. A fresh
Linux ARM64 bootstrap reproduces stage 2 as stage 3 at 13,065,152 bytes,
SHA-256 `a9b976676ae0c07432d6cd8dbbf74f8688484f57bb6c5d13bfbd5cd8eda78aa1`.
Stage 1 differs. GNU consumer, additional primary target/parity and Darwin
gates remain running or pending; PR CI has not passed on these fixes yet.

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
