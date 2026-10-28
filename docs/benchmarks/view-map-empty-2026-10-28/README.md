# Lazy native view-map comparison

The comparison measures a complete optimized x86-64 compile of a frozen `checker.fern` subject.
Both stage-2 compilers use producer `1e0ad6be807a01bbf50a05535d46808d611816016ce388e687d44589c5dc7e52`.
The subject and profile standard library come from `d9e569d5e5e5731f39efb13c73a373ccc9aca38f`.
Both builds use the frozen standard library from `23c5bc2e4a5993ffe092d04b5ad1dcf771af0269`.
Both profiles use a warm certifier memo and complete with exit zero.

The baseline uses 25,446,967,823 instructions and 17,683,090 boxes.
The candidate uses 25,435,514,391 instructions and 17,681,143 boxes.
`comparison.json` records 0.045009% fewer instructions and 1,947 fewer boxes.
This fixed-subject result does not establish a native latency improvement or a new bootstrap fixpoint.
The earlier shape and LICM groups use a different subject.

`baseline-source.tar.gz` retains the compiler and standard library snapshot.
`candidate.patch.gz` retains the two native helper changes.
`production-preservation.json` verifies all 1,072 measured compiler files against the checkout.
`output-identity.json` records byte-identical complete checker output on three targets.
The profile archives retain raw Callgrind input, process logs, symbols, resolved self costs and resolved caller costs.
The ELF records prove equal loaded code and data for release binaries and their counterparts with symbols.

The first private prototype allocated maps only for frame-backed views.
The complete checker compile aborted with an array bounds error, because heap views also read the map.
The rejected source patch, build records, failure log and resolved backtrace remain separate from the corrected candidate.
The rejected prototype has no successful profile or performance claim.

The helper differential uses exact original and candidate functions from both native emitters.
Their function text is identical across those emitters.
Each target runs 1,729 cases with identical output and zero live bytes.
Native and ARM64 runs balance 461,093 allocations each.
The Wasm run balances 467,109 allocations.

The cases check maps when heap or frame views exist and normalize absent slots only when no view exists.
They cover empty functions, sparse values, block placement, reverse slot order and input aliases.
Some graphs use unsupported immediate flags and test the helper contract rather than typed SSA validity.
Real emitter tests provide the execution checks.

The targeted emitter suites pass 65 nodes in 215.177 seconds, with zero failures and zero skips.
Source checks pass in 16.371 seconds.
Feature and unchanged complexity checks pass four nodes in 3.630 seconds.
All 2,110 formatter inputs pass.

Raw fixture sources use `.fern.txt.gz` so repository format checks do not parse experimental inputs.
The manifest records original paths, raw hashes, archive hashes and raw byte counts.
Every compressed file passes a byte-for-byte decompression check before publication.
Applicable current-head CI and issue #4412 remain incomplete.
