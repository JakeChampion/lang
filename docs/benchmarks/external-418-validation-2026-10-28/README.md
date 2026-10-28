# Validate the later owner merge

JakeChampion merged newer main changes into PR #13718 at `41879a90c091145420ed4dbf1b69883707dd51a9`.
The isolated worktree preserves that commit through a fast-forward.
The changes concern array truncation, integer operations, library methods and network allocation counts.
The archives preserve the complete owner patch and compiler source hashes.

The focused compiler checks pass 116 nodes in 287.083 seconds, with zero failures.
They cover arrays, truncation, checker hints, network parsers, semantic allocations and a local directory named `std`.
The optional two-fetch census skips because it requires an explicit report path.
The initial driver stops on that skip and preserves its failure record.
This record does not establish a completed census.

The separate two-fetch allocation gate passes in 18.661 seconds, with zero skips.
Its ceiling remains 91 allocations per request.
All 16 integer-operation nodes pass in 14.019 seconds across the interpreter, x86-64, ARM64 and Wasm.
Compiler, source-lint and checker packages pass 2,228 nodes in 31.999 seconds.
Feature and unchanged complexity checks pass four nodes in 9.204 seconds.

The current Go launcher checks all 2,110 formatter inputs in 2.998 seconds.
The formatter record includes its binary hash.
These checks do not establish a full Go formatter corpus pass or a new bootstrap fixpoint.
They do not alter the earlier optimization comparison's frozen sources.

The manifests record compressed and decompressed hashes for each raw archive.
Each archive passes a decompression identity check.
Applicable current-head CI, full retained-machine qualification and issue #4412 remain incomplete.
