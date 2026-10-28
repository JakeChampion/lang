# Measure the owner optimization batch

JakeChampion added five compiler optimizations on main through `b0241b2f3d542a7cc5a8a421cc4df4d24a4d7517`.
They use method indices, one cold-save sweep, deferred slot-use analysis, shared unit flows and a fixed postorder stack.
PR #13718 preserves those changes with its prior branch guards.
This entry measures the integration batch, without separate attribution to each optimization.

The comparison uses compiler sources from `9e8edb879cf0f6895350e972ce3aecfc913fff00` and `e6f4bf55f203371cd035f08346af27acf1ec9b1f`.
One stage-1 producer builds both stage-2 compilers.
Its hash is `e744816112dd5727b225a093ea87c39e37df54ce3d0a145a4e2675784217f26b`.
Both compilers compile the same frozen checker and standard library under Callgrind.
The checker hash is `11a3b2c1bbd016a7f255ce7a7bc1d40511dd37ec672a9741783217381cf6a99f`.

| Metric | Before stage-2 | After stage-2 |
|---|---:|---:|
| Instructions | 25,304,265,319 | 23,410,523,324 |
| Calls to `__fern_arr_box` | 17,811,827 | 17,765,646 |

The fixed compile uses 7.483885% fewer instructions and 46,181 fewer boxes.
Complete checker output remains byte-identical on x86-64, ARM64 and Wasm.
Both complete profile runs exit successfully.
Each release binary matches the loaded code and data of its symbol binary, except for ELF section fields.

The before compiler hash is `4862f56c9761bb68e517cf1b518c4824f00fb2651ee0caeb15f54ca414bcc8df`.
The after compiler hash is `01223e2118bee669c27b78f1391a7dc5261e351403e297978530dbe156ed615e`.
The [raw evidence](../benchmarks/owner-optimization-integration-2026-10-28/README.md) preserves source snapshots, full profiles, commands, output bytes and symbol resolution.

This group uses a new producer and subject.
Its result does not combine with earlier groups or establish a native latency improvement.
The later owner commit `41879a90` changes compiler and library sources after these frozen inputs.
This comparison does not measure that later source.
Full retained-machine qualification, applicable current-head CI and issue #4412 remain incomplete.
