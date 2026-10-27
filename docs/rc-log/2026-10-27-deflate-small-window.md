# Three-byte matches within the requested DEFLATE window

The three-byte match shortcut can select a distance beyond the requested window.
The old shortcut checks its 8,192-byte cost cutoff without the negotiated window.
A 9-bit streaming decoder rejects a reduced 604-byte input after byte 600.
A 15-bit decoder recovers the exact input from the same compressed bytes.

The shortcut now also requires a distance within the requested window.
The match-cost cutoff and longer-match chain remain unchanged.
The change does not increase message limits, backpressure limits, or validator tolerances.
All core encoding remains in Fern.
The independent host decoder is a test reference only.

The regression covers windows 8 through 15 at boundary distances and three additional 9-bit cases.
One-byte reference output chunks enforce retention of the requested history window.
A fixed invalid stream must fail at the expected position for window 9.
The same stream must decode exactly for window 15.
All row identities, hex bytes, lengths, EOF state, and trailing input checks remain strict.

The original encoder reports 24 failed nodes across the three targets, including parent nodes.
The native and ARM candidate passes initially accompany a Wasm adapter trap.
The independent allocator prerequisite #13691 fixes that trap through merged PR #13692.
The two codec candidate files remained byte-for-byte unchanged during that prerequisite.
The resumed candidate passes 87 nodes with no failures or skips on native, Wasm, and ARM64/QEMU10.
Its source hashes remain unchanged during the run.

The full Autobahn validator remains an independent acceptance criterion.
Local regressions do not qualify that CI result.
The original 517-case validator, server configuration, and status policy remain unchanged.
Informational and non-strict results retain those labels.
Issue #13690 stays open until its actual CI criterion completes.

## Final source and compiler

Source commit: `1682af4ce80d3d5fb54d7814ec5a9767d6902699`.
The clean two-stage build, version command, and smoke tests pass in 93.476 seconds.
The shipped compiler hash is `6dfe9cf02a30700515ce5987cb8c9d1c0ba9dd063b5014b97f8cf3ac223b8f67`.
The raw compiler hash is `387823a4c57bd0e560e343beec203868b892ababca7acc9252cfbfa4e061c7b5`.
All compiler and stdlib source hashes remain unchanged during the build.

The clean shipped compiler repeats all 27 vectors on each target.
All three workers exit normally, and every streaming reference check passes.
The three compressed output records are byte-identical for this finite corpus.
Native and ARM report 5,646 balanced allocations each, while Wasm reports 5,835.
Every worker reports zero live bytes.
The source, compiler, runner, reference, and output hashes remain recorded and unchanged.

Gunzip and the interpreter WebSocket tests pass four nodes without skips.
The inflate-reader, native and ARM WebSocket tests, and unchanged complexity gates pass 19 nodes without skips.
All 282 image TAP cases pass on each target.
Their 79 native, 55 Wasm, and 40 ARM heap censuses remain balanced with zero live bytes.
All selected source hashes remain unchanged.
The 40 standalone ARM executables do not constitute the full Go ARM harness.

All 2,064 format checks, build, vet, actionlint, test-name checks, workflow self-tests, runtime bundle checks, and retained protocol tests pass.
The unchanged allocation gate measures 35,059,803 allocations and frees against baseline 34,951,426, a 0.31 percent increase.
The report contains no RSS measurement.
The separate source-lint audit failure on unchanged main remains covered by owner PR #13646.
This repair does not duplicate that patch or call the broad source-lint sweep a pass.

The native completion handle disconnected after the scripts completed.
The durable records retain actual script exit codes, complete logs, and unchanged source hashes.
A later recheck confirms the records and source identities with no live processes.
The native and Wasm outer completion statuses remain unclaimed.
No completed run repeats to reconstruct those handles.

## Preserved failures

The initial candidate reports 58 passed nodes and one failed Wasm parent node.
Its first draft metadata incorrectly hard-codes exit zero.
The corrected metadata records actual tool exit one, and both drafts remain preserved.
The allocator prerequisite explains the independent adapter trap without a codec syntax workaround.
The post-prerequisite 87-node result retains its uncommitted-feature provenance.
The later clean compiler records provide separate final qualification.

Raw evidence appears in `docs/benchmarks/deflate-small-window-2026-10-27/`.
The full Autobahn CI criterion remains open until an actual validator result completes.
Queued, pending, failed, cancelled, and skipped substantive validation never counts as a pass.
