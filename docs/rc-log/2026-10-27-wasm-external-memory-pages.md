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
