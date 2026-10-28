# Main compiler optimization integration

The branch merges main at `b0241b2f3d542a7cc5a8a421cc4df4d24a4d7517` after `9e8edb879cf0f6895350e972ce3aecfc913fff00`.
Main adds method lookup by index, one cold-save sweep, deferred slot-use analysis, shared unit flows and a fixed postorder stack.
The integration preserves seven of the eleven changed paths exactly.
The other four files retain the prior branch changes.
The owner helper implementations remain intact.

Four conflict blocks concern the LICM helper parameters and slot-use setup.
The resolution uses the owner slot count `maxs + 1` and the owner deferred slot-use analysis.
The branch retains its early return for functions without loops and its lexical zero-slot guard scan.
All four owner pure-hoist helpers match main exactly.
The native map and successor-count patches remain identical to their prior branch versions.

The targeted compiler suites pass 144 nodes in 248.298 seconds, with zero failures and zero skips.
They cover LICM, SSA replay, cold calls, register allocation, generic methods, views, leaf frames and PIE.
Source checks pass in 15.953 seconds.
Feature and unchanged complexity checks pass four nodes in 10.365 seconds.
The current public compiler checks all 2,110 formatter inputs in 2.728 seconds.

The rich IR contract checks 350 cases on x86-64, ARM64 and Wasm with sanitizers.
All three outputs match the prior 211,729-byte, 1,400-line record exactly.
The output hash is `cd5c9feafa6f7026b5e97a48c6d6b791df0b27a9f3e43e523a5dccc0472c9397`.
Native and ARM64 runs each balance 73,188 allocations.
The Wasm run balances 77,141 allocations.
All three runs report zero live bytes.

The contract snapshot matches all 1,072 current compiler files.
The record preserves the source, input fixture, commands, output, censuses and conflict stages.
The current stage-1 producer has hash `e744816112dd5727b225a093ea87c39e37df54ce3d0a145a4e2675784217f26b`.
The workspace preserves that producer for the next controlled stage-2 comparison.
This integration record does not establish a new compiler speed improvement or bootstrap fixpoint.

The full Go formatter corpus remains unqualified.
One shard passed 297 nodes on the prior source snapshot.
The next shard stopped before this merge changed its compiler inputs.
The archives retain the completed shard, partial shard and explicit interruption record as historical results.
They do not establish a full corpus pass for the new source.

Every compressed archive passes a decompression identity check.
Applicable current-head CI, full retained-machine qualification and issue #4412 remain incomplete.
Review readiness does not establish complete issue acceptance.
