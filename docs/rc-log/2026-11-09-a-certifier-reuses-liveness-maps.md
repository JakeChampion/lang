# The certifier reuses liveness maps

Status: [record] source change and measurements from 2026-11-09.

Each certifier `Walk` holds block positions and definition sites for its graph.
The liveness call rebuilt two block maps and two definition arrays.
`ssalive.compute_kept_with_sites` now accepts the existing maps.
It retains dependency and graph validation and uses the existing liveness transfer rules.
Each extracted graph receives fresh maps before its walk.

The caller must supply maps for the same unchanged graph.
`walk_of` constructs both maps before each `live_of` call.
The call sites cover the root graph and extracted regions.

The new fixture covers implicit parameters, an unused value, sparse IDs, and an entry after its successor in storage order.
The existing fixtures cover loop phis, dependencies, and wide bitsets.
Filtered live sets agree through both entry points.
Fourteen invalid-metadata cases retain their expected diagnostics.
The liveness fixtures pass on x86-64, ARM64 under QEMU, and WebAssembly.
The existing certifier cases pass on x86-64.

The [measurement archive](../benchmarks/compiler-liveness-2026-11-09/README.md) records the fixed checker comparison.
Cold compilation removes 16,644 allocations and 125,065,828 guest instructions.
Warm compilation removes 11,966,410 guest instructions.
Both counter runs free every allocation and report zero live bytes.
Assembly and native executable hashes match in their respective comparisons.
