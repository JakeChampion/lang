# Shape bitmap and empty-plan measurement

The comparison measures a complete optimized x86-64 compile of the fixed `checker.fern` subject.
Both stage-2 compilers use producer `1e0ad6be807a01bbf50a05535d46808d611816016ce388e687d44589c5dc7e52`.
The subject and standard library come from `23c5bc2e4a5993ffe092d04b5ad1dcf771af0269`.
The certifier memo is warm for both profiles.
Instruction counts come from complete Callgrind runs with exit zero.
Wall time under Callgrind is not a native latency result.

`comparison.json` records 3,117,269 fewer instructions and 1,270 fewer boxes.
`output-identity.json` records exact checker output identity for x86-64, ARM64 and Wasm.
`production-preservation.json` records 1,072 measured compiler files that match the checkout.
`baseline-source.tar.gz` retains the source before the change.
`candidate.patch` reconstructs the measured source change.
The raw records retain the original workspace paths.

The profile files include Callgrind input, resolved self costs, resolved caller costs, symbols and process logs.
The ELF records establish equal loaded code and data between release binaries and their counterparts with symbols.
The baseline profile comes from the prior LICM slice with the same producer.
The counter prototype remains a separate experiment and is not the production change.
Its profile saves 2,832,630 instructions and 1,270 boxes against the same baseline.

`contract-source-identity.json` verifies exact helper extraction from the original and candidate sources.
The differential uses actual compiler SSA types and both private helper variants.
Each of the three target runs checks 6,048 helper inputs and preserves input aliases.
Invalid operand indices in some inputs test helper bounds behavior, not the validity of a complete SSA graph.
Native and ARM64 runs balance 438,323 allocations each.
The Wasm run balances 454,559 allocations and reports zero live bytes.

The targeted emitter suites pass 67 nodes in 194.062 seconds, with zero failures and zero skips.
Source checks pass in 15.112 seconds.
The feature and unchanged complexity checks pass four nodes in 3.921 seconds.
The formatter checks all 2,110 inputs.

The differential fixture files use `.fern.txt.gz` so repository format checks do not parse raw experimental inputs.
`archive-manifest.json` records each original path, raw hash, archive hash and raw byte count.
Every compressed file passes a byte-for-byte decompression check before publication.
