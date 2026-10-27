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

The regression uses record and float arrays with 4095, 4100, and 8193 entries on all three targets.
The stock harness requires strict IR, sanitizers, nonzero allocations, balanced counts, and zero live allocations.
Final source-pinned validation and evidence follow in this PR.
The parent wide-arithmetic files remain frozen and byte-identical.

No allocator behavior, reference expectation, baseline, threshold, or runtime guard changes.
The full retained-machine recovery and all actual M5 and epic gates remain open.
