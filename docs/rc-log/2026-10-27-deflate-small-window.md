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
