Large ARM64 array literals emitted stores beyond the immediate-offset range.
Large float literals also exceeded the assembler literal-pool limit within one function.
The emitter now materializes wide offsets and flushes pools after 256 SSA instructions with a branch around each pool.
Small offsets retain their current instruction forms.

The regression checks every element and constant-index boundary loads for record and float arrays with 4095, 4100, and 8193 entries.
The stock harness runs all six fixtures on native, ARM64, and Wasm with strict IR, sanitizers, and allocation checks.
No fixture size, expected value, guard, baseline, or threshold changes.

Validation on committed source `d2495816205d41ba6bcc7d93085bad9b2bde1d5b`:

- The clean standard build, version check, and stage-1 smoke tests passed.
- Six direct ARM64 runs passed with nonzero balanced zero-live counts.
- The unchanged complexity gate passed three test nodes with zero skips.
- The stock harness, existing ARM64 regression checks, runtime checks, source checks, and image suites continue in parallel.
- CI remains unqualified until actual results arrive.

Refs #13727 and #13725.
The parent arithmetic files remain frozen and byte-identical.
The full retained-machine recovery and all actual M5 and epic gates remain open.
