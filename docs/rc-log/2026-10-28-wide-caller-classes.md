# Current classes for large-free callers

Issue #13750 adds standalone classification and partition adapters for the current large-free helper.
The old caller models admit 4 GiB allocations but classify them with the old table through 1 GiB.
The current helper uses class 52 at 4 GiB.
The old models and their assertions remain unchanged.
Retained-driver integration and machine call summaries remain separate work.

The adapters use all 61 current capacities through 16 GiB.
They compare word counts with capacity quotients and avoid signed byte multiplication.
Uniform small string allocations retain sentinel minus three.
Uniform oversized allocations retain sentinel minus two.
Invalid or mixed intervals return sentinel minus one.

The new grids refine each old row at current class boundaries.
The array grid contains 70 rows instead of 54.
Each string grid contains 103 rows instead of 87.
All old boundaries, residue ranges, and input domains remain unchanged.
The fused grid extends through 2305843009213693948 words.
The separate grid retains its final 1152921504606846976 singleton with residues one through seven.

An independent Python integer reference defines 1566 cases.
The reference uses byte arithmetic and set-based partition boundaries.
Each class record includes the old result as explicit legacy provenance.
A framed worker and strict collector require exact integer records and nonzero balanced zero-live censuses.
Private controls restore the old ceiling, omit a partition, and swap the fused offset.
The collector must reject all three controls.

The fresh retained-machine failure remains open under #13717.
Full recovery, native milestones, M5, and epic acceptance remain open.
