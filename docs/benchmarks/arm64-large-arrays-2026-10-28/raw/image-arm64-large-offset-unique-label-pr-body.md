Large ARM64 array literals emitted stores beyond the immediate-offset range.
Large float literals also exceeded the assembler literal-pool limit within one function.
The emitter now materializes wide offsets and flushes pools after 256 SSA instructions with a branch around each pool.
Each pool uses a unique textual label and a distinct structured ID.
Small offsets retain their current instruction forms.

The regression checks every element and constant-index boundary loads for record and float arrays with 4095, 4100, and 8193 entries.
The stock harness runs all six fixtures on native, ARM64, and Wasm with strict IR, sanitizers, and allocation checks.
No fixture size, expected value, guard, baseline, or threshold changes.

Validation on committed source `3ba81dccd2de600e45fa9539247e28352e07118b`:

- The clean standard build, version check, and stage-1 smoke tests passed.
- Six direct ARM64 runs passed with nonzero balanced zero-live counts.
- The unchanged complexity gate passed three test nodes with zero skips.
- The stock harness and ARM64 offset and literal-pool checks passed 30 test nodes with zero failures or skips.
- Runtime checks passed all five target sets plus the stock feature and fixture checks.
- Format, Go build, vet, workflow lint, CI self-tests, runtime bundle, and 219 source-audit nodes passed.
- The three image suites continue in parallel.
- CI remains unqualified until actual results arrive.

Refs #13727 and #13725.
The parent arithmetic files remain frozen and byte-identical.
The full retained-machine recovery and all actual M5 and epic gates remain open.
