# Instruction baseline refresh

The report measures the combined fixes over main at d70db05.
It includes the lane bounds repair from PR #13031.
All 37 benchmarks were compiled and profiled for both native architectures.
Each profiled program returned the same checksum as its plain execution.

Static counts use the assembly-line rule in scripts/perf-bench.
Dynamic counts use Callgrind 3.24.0.
The x86-64 tool ran directly. The AArch64 tool ran under QEMU and counted guest instructions.
The report contains all 148 static and dynamic metrics.
Per-metric tolerances remain unchanged.

Recursive enum array cleanup now uses a loop for its final child.
That adds about 50 to 60 static instructions to the persistent-container benchmarks.
Their dynamic counts remain within 0.01 percent of the previous baseline.
This trades a small helper-size increase for bounded cleanup stack space.

The report also records smaller changes from the native memory and runtime updates on main.
Both improvements and increases are recorded.
The strict performance comparator passes against the refreshed baseline.
