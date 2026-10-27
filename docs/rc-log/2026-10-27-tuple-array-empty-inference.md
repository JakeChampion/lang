# Tuple array element inference

Refs #13695. Parent #13694 keeps its application files unchanged until this repair merges.

The Go checker accepts `[([], 0), ([1], 1)]` and its interpreter returns the expected result.
The self-host checker also accepts this source before the repair.
All three compiled targets then refuse typed lowering because the array binding retains an unknown tuple component.
The reverse-order, direct nested-array, and explicit-type controls succeed before the repair.

The array checker now joins partial tuple components through the existing value-block join.
Each peer must settle to the shared type with the existing view and nominal checks.
Invalid joins retain an unresolved receiver for subsequent method diagnostics.
The tuple array diagnostic also checks nominal arguments instead of only nominal names.
The Go checker and both application files remain unchanged.

The focused regression checks twelve positive programs across native, ARM64, and Wasm targets.
Each program also passes the Go checker and interpreter.
Eight negative programs require the exact Go diagnostic-code sequence on every compiled target.
The final focused run passes 82 test nodes with no failures or skips.
All compiled positive cases require a nonzero balanced heap census with zero live bytes.

The first test draft omitted an equality derivation on its enum control.
The Go checker correctly rejected that draft with E041.
The corrected control uses the canonical equality derivation.
The preserved first production candidate passes the positive cases but misses three follow-on diagnostic sequences.
The final candidate preserves those strict assertions and passes them.

The clean two-stage build uses source `9c4085d83796cb46356a42ea51aa4ad7df69c2db`.
Both build commands exit zero, and the stage-one smoke test passes.
The shipped compiler SHA-256 is `d1721835de31a7cc774df13ec7322a2d981066c442fcefe8611a19fc4ec4417b`.
The raw compiler SHA-256 is `bae99bcfcf6d38ce531ecc4c0d5fbecd066c3bb71c435750a06f1fdaf3ae0543`.
The compiler and standard-library source hashes remain unchanged through the build.

The direct diagnostic suite passes 4,756 nodes with no failures or skips.
The focused ownership and value-block suite passes 478 nodes with no failures or skips.
That suite includes both unchanged complexity gates.
The full Go checker suite passes 2,000 nodes with no skips.

Native, Wasm, and ARM64 image suites each pass 282 TAP tests.
Their 79, 55, and 40 heap censuses are balanced with zero live bytes.
The ARM64 result covers 40 standalone executables under QEMU10.
It does not qualify the whole Go ARM harness.
Each image campaign retains its source and compiler hash checks.

The unchanged allocation gate reports 35,101,745 allocations and frees against the upstream baseline of 34,951,426.
The increase is 0.43%, within the existing tolerance and ceiling.
The report contains no RSS measurement.

All 2,064 selected Fern files pass the format check.
Go build, vet, workflow lint, test-name checks, CI self-tests, runtime-bundle checks, and protocol controls pass.
The evidence directory retains the initial failures and both corrected candidates.
The tool inventory explicitly identifies its observation after some completed tests.
The two frozen application files remain byte-for-byte unchanged.

Raw records: [tuple array evidence](../benchmarks/tuple-array-empty-inference-2026-10-27/manifest.json).
This compiler prerequisite does not qualify the inverse-matrix application or close any image-engine acceptance gate.
