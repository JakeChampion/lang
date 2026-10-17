# The certifier compacts unused value slots

`rcccompact.fern` alpha-renames the private graph passed to the emitted-RC
certifier. Emission keeps the original graph. Functions with at least 256
value slots and a conservative 25% hole estimate use the compact view.
All parameters, definitions (including unused allocations), operands and
observed terminator fields remain. Ascending original IDs define the dense
order, and findings map back through the inverse table. Instruction immediates,
block IDs, edges and raw IR operations are unchanged.

The checking algorithm, token assumptions, replay rules and refusal behavior
remain the same. Even an inactive terminator value field is preserved because
the existing use-count calculation observes it. A regression demonstrates the
false leak an earlier prototype produced when that field accidentally named
an unused retain result after compaction.

The complete RC-certifier and complexity suite passes (25.357 seconds).
[The measured comparison](../benchmarks/certifier-dense-values-2026-10-17/README.md)
records byte-identical reports and executable outputs on all three backends,
a self-compilation fixed point, a 1.70% instruction reduction, the additional
allocation cost, and rejection of the original #12135 ownership mutant.
Its compiler/source hashes describe the pre-integration comparison exactly.
Cold certification still exceeds #12181's 15% target; this does not close it.

After integration with the scalar-return branch and main 3260020a0, the
combined regression suite passes (32.383 seconds), as do all eleven strict
size checks (123.952 seconds). Stages 2 and 3 match at 12,703,568 bytes,
SHA-256 `4e9a2d4780364cf334a18f95f8731ac54d06eda8130119e0d14cd234d5b2e9fe`.
A fresh comparison against the scalar baseline preserves all three output
binaries and all three full reports byte-for-byte. The raw integrated
reports retain their own source/compiler hashes in the benchmark directory.
