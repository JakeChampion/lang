# Inliner caches static word classes

The word-propagation analysis repeatedly checked types and load sources during its fixed-point scans.
The first scan now computes those static facts once per SSA value.
One packed byte stores eligibility and the propagated read flag.
Later scans only propagate reads through operands.
The graph and summaries remain unchanged within each invocation.

The false-mask helper now allocates its known length directly.
It retains empty results for nonpositive sizes.
The inliner keeps the same propagation rules and scan order.

Relative to the graph-only parent, the selected compiler removes 42,982 cold allocation events and 341,632,767 warm instructions.
These reductions are 0.0826% and 1.1482% on the fixed checker workload.
Both memory probes report 562,524,216 bytes of fresh heap growth.
All counted allocations have matching frees and zero live bytes.
The assembly and native executable remain identical.

Together with graph reuse, the changes remove 88,545 allocation events and 354,975,385 instructions from the original comparison.
These reductions are 0.1700% and 1.1925%, respectively.
The [measurement archive](../benchmarks/compiler-inliner-2026-11-08/README.md) records the producer, fixed input, commands, and hashes.
The measurements do not establish a wall-time gain.

A separate worklist trial reduced instructions but added 127,110 allocation events.
The selected implementation avoids its reverse-edge arrays and queue.
The archive retains that trial and the direct-allocation control.

Owner-lifetime cases and their sanitizer variants pass on x86-64, ARM64 under QEMU, and WebAssembly.
The opaque-return driver also runs on all three targets.
It checks reversed block order, cycles, unused values, missing seeds, constant returns, and opaque consumers.
The source check, format check, and Go vet pass.

The diagnostic profile still identifies semantic planning and ownership certification as the largest inclusive costs.
The [performance plan](../COMPILER-PERFORMANCE.md) records those priorities and the comparison controls.
