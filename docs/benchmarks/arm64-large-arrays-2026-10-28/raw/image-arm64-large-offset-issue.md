Parent: #13725 under #13717 and #13687.

The expanded wide-arithmetic reference driver fails ARM64 assembly on current main.
Native and Wasm execution and the Go interpreter pass the same 4474 exact reference cases.
The ARM64 emitter produces 379 stores beyond the 32760-byte scaled immediate limit.
The arithmetic files remain frozen without source changes.

A separate runtime-valued array with 4100 records reproduces five invalid stores at offsets 32768 through 32800.
A constant-only array does not reproduce the failure because the compiler uses static data.
The reduced fixture checks every value and tag.
It avoids the separate literal-pool diagnostic from an earlier exploratory fixture.

Predeclared scope:

- Correct ARM64 scalar load and store address emission when a byte offset exceeds the instruction encoding.
- Preserve immediate encodings for offsets that fit the scaled or unscaled forms.
- Materialize larger offsets without changing the base register, value register, or live allocated registers.
- Preserve all guards, array sizes, reference cases, workloads, baselines, and thresholds.
- Keep the parent arithmetic helpers and generated cases byte-identical throughout this prerequisite.
- Cover loads and stores, integer and floating-point registers, immediate boundaries, and large record arrays where applicable.
- Preserve the current native and Wasm behavior.

Acceptance:

- [ ] Preserve the original expanded failure, reduced failure, exploratory controls, and exact source and tool identities.
- [ ] Add regression tests that fail on the current emitter and check every array element after the correction.
- [ ] Correct the address emission without a fixture workaround or a validation exemption.
- [ ] Pass the targeted native, Wasm, and ARM64 tests with nonzero balanced zero-live checks.
- [ ] Complete a clean standard build, appropriate source checks, format, Go build, vet, and unchanged complexity gates.
- [ ] Merge one scoped READY PR normally at the exact full head before the parent arithmetic work resumes.

The full retained-machine recovery remains open.
All actual M5 and epic acceptance gates remain open.
