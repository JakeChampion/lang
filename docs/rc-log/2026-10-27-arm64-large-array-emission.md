# ARM64 large array emission

Refs #13727. Parent: #13725.

A 4100-record runtime array produced stores beyond the ARM64 immediate offset limit.
The emitter now uses a reserved address register for offsets outside the scaled and unscaled instruction forms.
Small offsets retain their current forms.
The new regression checks every record field and constant-index boundary loads.

The first correction passed all record cases on native, ARM64, and Wasm.
The float cases exposed a second assembler limit within one large function.
The emitter now flushes literal pools after 256 SSA instructions and branches around each pool.
The function-end flush remains.
Each pool boundary uses a unique textual label as well as a distinct structured label ID.
The first pool candidate reused textual names and failed GNU assembly despite direct in-process success.

The regression uses record and float arrays with 4095, 4100, and 8193 entries on all three targets.
The stock harness requires strict IR, sanitizers, nonzero allocations, balanced counts, and zero live allocations.
Final source-pinned evidence is in [the qualification record](../benchmarks/arm64-large-arrays-2026-10-28/README.md).
The parent wide-arithmetic files remain frozen and byte-identical.

No allocator behavior, reference expectation, baseline, threshold, or runtime guard changes.
The full retained-machine recovery and all actual M5 and epic gates remain open.

The qualified source is `3ba81dccd2de600e45fa9539247e28352e07118b`.
Its clean standard build passed in 78.737 seconds.
The stock targeted suite passed 30 nodes, and the source audit passed 219 nodes, both with zero skips.
The unchanged complexity gate and all runtime checks passed.

Each image target completed 326 TAP tests.
The native, Wasm, and ARM64 runs recorded 85, 61, and 46 balanced zero-live censuses.
The native formatter fixture allocated zero blocks and exposed an incorrect positive-allocation condition in my collector.
The separate audit preserves the original wrapper failure and checks the complete log without a test repeat.

All six direct ARM64 cases passed with nonzero balanced zero-live counts.
The allocation measurement recorded 36900494 balanced events, 5.58 percent above the unchanged 34951426 baseline.
The corrected comparison passed the unchanged ten-percent ceiling and retained the one-percent advisory finding.
No RSS row was collected.
