# Ring wait selection after receive buffers drain

PR #13689 addresses native child #13688 under #12918.
Application reads can empty a receive buffer while its descriptor remains in the cached ready list.
The previous runtime used that list length to select a nonblocking wait.
The new predicate selects from actual poll, buffer, and end-condition readiness instead.

The delayed-client regression fails before the fix and passes afterward.
The original workloads retain all 5,000 requests, warmup, four-per-second slack, and complete syscall counts.
Qualified ring pins decrease to 2 and 8, while epoll retains 3 and 9.
No larger limit or omitted syscall hides the earlier extra entries.

Source `234d92f49dd409d7cd49d9e6035e032474d889fb` precedes the clean standard build and final validation.
The selected Go runs report 58 PASS nodes with no failures or skips.
All 282 image TAP cases pass on each target, with 79 native, 55 Wasm, and 40 ARM balanced zero-live heap censuses.
Runtime modules, unchanged complexity and allocation gates, and format/build/vet/lint checks pass.

See [the measurements and preserved evidence](../benchmarks/io-uring-stale-ready-2026-10-27/README.md).
Local results do not qualify actual main/nightly recovery, native ARM ring execution, universal syscall bounds, or production acceptance.
Parent #12918 and all actual image-engine acceptance gates remain open.
