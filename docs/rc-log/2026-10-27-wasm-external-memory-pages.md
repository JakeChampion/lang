# Wasm heap allocations after external memory growth

The Preview 1 adapter can reserve pages through `memory.grow`.
Its allocator does not advance the Fern heap pointer.
The old Fern allocator can then reuse those pages and overwrite adapter state.
A later `fd_write` traps at the adapter's state check.

The reduced regression prints once, appends integers, checks every element, and prints again.
The 32-element control passes on the original compiler.
The 100,000-element case traps after the first print.
The same failure occurs without DEFLATE.

The allocator now records its page extent.
Before a new bump allocation, it detects external growth and moves past the external pages.
After its own growth, it updates the recorded extent.
Free-list allocations retain their current behavior.
The full-memory guard prevents a wrapped address at the 4 GiB limit.
This guard is not a physical memory bound.

The original Preview 1 adapter, component test paths, heap assertions, and native allocators remain unchanged.
The DEFLATE candidate remains frozen in its separate worktree until this prerequisite merges.
The change does not qualify Autobahn or close its parent repair.

## Rejected candidate

The first candidate used direct command components and required an exported allocator for Preview 1 adaptation.
Its new regression passed, but two HTTP probes retained 131,072 bytes each.
The socket probe lacked the required allocator.
Those failures reject that candidate, and its sources and logs remain preserved.
The final change repairs the allocator instead of that test-path change.

## Validation

Source commit: `7a2d10c3a9a8f2844838c95b153a846ba376311e`.
The clean two-stage build, version command, and smoke tests pass.
The source hashes remain unchanged during the build.
The clean compiler repeats both reduced cases with exact output and immutable source and tool hashes.
The small and large cases report five and 19 balanced allocations, respectively, with zero live bytes.

The focused candidate run passes 340 nodes without failures or skips.
The final adapter clients and complexity checks pass 14 nodes without failures or skips.
The heap, allocation-overflow, long-write, copy-on-write, and writer controls pass 39 nodes without failures or skips.
The original Preview 1 HTTP probes and socket probe retain their zero-live assertions.
The source proof confirms that all three original test-path files match main exactly.

All 282 image TAP cases pass on each of native, Wasm, and ARM64/QEMU10.
Their 79, 55, and 40 heap censuses remain balanced with zero live bytes.
All selected source hashes remain unchanged.
The ARM run comprises 40 standalone executables, not the whole Go ARM harness.
Wasm provides the existing leak census, not the native use-after-free detector.

All 2,064 format checks, build, vet, actionlint, test-name checks, workflow self-tests, and runtime bundle checks pass.
The retained collector protocol tests also pass.
The unchanged allocation gate measures 35,060,876 allocations and frees against baseline 34,951,426, a 0.31 percent increase.
The report contains no RSS measurement.

The broader source-lint sweep fails its coverage-table ownership audit on unchanged main as well.
Owner PR #13646 already contains that audit repair.
The preserved failure is not a source-lint pass, and this change does not duplicate the owner's patch.
The rejected test-path candidate and its seven initial ARM skips remain separate from the final results.

Raw records, failures, source snapshots, and provenance appear in `docs/benchmarks/wasm-external-pages-2026-10-27/`.
Queued, pending, failed, cancelled, and skipped substantive CI does not count as a pass.
The DEFLATE repair, Autobahn validator, and broader image-engine acceptance gates remain open.
