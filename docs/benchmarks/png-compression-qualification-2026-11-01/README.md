This record qualifies the selected native compression requirements of #13221.
It does not qualify the production PNG milestone #13214.

PR #13200 merged as `06f560491a4f2c81f89b8732895553f1d6dd3c77`.
Its head CI `40189191231` and merge CI `40191116448` ended as cancelled.
Those gates did not pass. This follow-up requires fresh CI.
Dependency #13211 is complete. The original implementation remains on main.

The compiler source is integrated main `de2ec359a22078e3bfe23402b6766b51def8d665`.
The frozen Fern compiler has SHA256
`81c6afbe815b3253a2be588ffb70cdc7aed37d567ba4f30585d91b60f42447da`.
Its own stages two and three are byte-identical.
No production codec, compiler, output limit, performance baseline, or general
zlib wrapper contract changes in this qualification.

Each target runs the unchanged three-case zlib experiment, four strict PNG
compression tests, and seven unchanged PNG tests with heap sanitization.
The new tests exercise the current strict adapter. Both image CI scripts run them.

| Target | Runner | TAP cases | Processes with balanced, nonzero counts and zero live bytes |
| --- | --- | ---: | ---: |
| ARM64 Darwin | Native | 14 | 3 |
| ARM64 Linux | Native Docker ARM64 | 14 | 3 |
| x86 Linux | QEMU 7.2.22 | 14 | 3 |
| Wasm32 WASI | Wasmtime 46.0.1 | 14 | 3 |

Wasm `-sanitize` records the leak census. It does not detect reference-count
over-release or use-after-free. The compiler warning remains in the raw record.

Python zlib independently decodes all three original fixture streams and
checks their actual stored, fixed-Huffman, and dynamic-Huffman block types.
It also verifies the one-byte strict fixture and alternate header checksums.
Fern performs all tested runtime decompression and PNG processing.

The strict controls reject trailing bytes and a second zlib stream.
They check Adler-32 failure, every truncated prefix, zero and invalid output
budgets, and exact successful output length. Alternate method, window, and
dictionary headers have valid modulo-31 checks, so each policy test reaches
the relevant restriction. Unchanged PNG tests retain all five filter goldens,
malformed chunks, truncated inputs, scanline rules, and allocation budgets.

[qualification.json](qualification.json) records commands, source and binary
hashes, runner identities, TAP output, per-process censuses, and earlier
runner-path failures. The first Linux attempts mounted `/tmp` while macOS
resolved outputs under `/private/tmp`. They failed before decoder execution.
Corrected mounts preserve the same compiled input and execute every case.
Those setup failures are not successful qualification records.

Reproduce one native pilot from the repository root:

```sh
uv run --no-project docs/benchmarks/png-compression-qualification-2026-11-01/qualify.py \
  --compiler /tmp/fern-13998-fixed-point --target arm64-darwin \
  --work /tmp/fern-13221-native
```

For Wasm, set `--target wasm32-wasi` and `--runner 'wasmtime run'`.
The Linux command arrays in the record use Docker mounts at the resolved
output path. Verify the recorded image identity before those runs.
QEMU establishes correctness here. It does not measure native latency.

These results retain the original audit limits. They do not measure exact
transient or aggregate memory peaks, cancellation latency, exhaustive malformed
Huffman compatibility, broad fuzz coverage, or representative throughput.
Streaming PNG, production workers, and broader image acceptance remain separate.
Fresh full CI must pass before this follow-up can close #13221.

Local `make lint-fast`, the source/runtime gate with 85 drivers, and the
source audit pass. The apt-only audit test passes separately on Linux.
The complete unchanged Wasm image script also passes: 330 TAP cases in
46 suites, plus benchmark processes. All 62 process censuses are balanced,
nonzero, and zero-live. The record preserves the CLI compiler hash for that
separate script run. Full native CI remains pending.
