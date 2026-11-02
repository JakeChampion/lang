# Cell snapshots across implicit finalizers

Issue #14014 follows the merge of #14012.
An implicit drop can call a finalizer that clears a cell before a borrowed snapshot's last use.
The snapshot then observes released content.
The native reproducer returns zero on the preceding main and one on the initial owner head.
Both runs report five balanced allocations and zero live bytes.

The repair retains the snapshot when the function's value types can contain a user finalizer.
It reuses the recursive containment proof and checks this condition once per function with an otherwise quiet read.
This guard covers direct, contained, and edge drops without a new drop-site analysis.
Ordinary quiet reads still pass their no-retain tests.

The extended cell-borrow suites pass in 79.670 seconds without skips.
The source gate passes all five runtime targets and all 87 source drivers.
Lint, format checks, and diff checks pass.
All four compiler stages match byte for byte.
The original owner tree and actual main `988fa499` match for every tracked file.
The source bindings therefore survive the squash merge without a source change.

All 78 native static metrics and all 78 ARM static and dynamic metrics meet their original limits.
All 39 ARM observable controls match the preceding main.
The report includes 117 emissions across three targets.
Two Wasm advisory metrics match the preceding main exactly.
Their policy and baseline values remain unchanged.

The finalizer guard is conservative in functions that contain resources.
The deterministic native ARM64 `shuf` control validates identical output on all three engines.
At 10,000 values, the owner head records 3,266,702 instructions.
The repaired engine records 3,436,719 instructions, exactly the preceding main's count.
The 100-value pilot confirms the same relationship before scale increases.
The earlier owner's speedup does not describe this repaired resource path.

The JSON files preserve hashes, commands, scope, and results.
The static comparison uses the complete static subset of the unchanged source limits.
The ARM comparison uses the complete ARM subset, including dynamic metrics.
The earlier wrong-scope comparison retains its failed result outside this completed evidence.
Full current-head GitHub CI remains a merge gate.
