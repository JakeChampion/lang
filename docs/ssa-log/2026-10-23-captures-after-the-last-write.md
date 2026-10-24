# Captures after the last write

A later read-only closure forced a map into a shared cell before its build loop.
Each cell read retained the map, so every insert copied its storage.
At 100,000 keys, the original issue reproducer made 200,018 allocations during the build.

`captureflow` records each block's last assignment per name.
The lift uses that information to distinguish completed writes from writes that can follow a capture.
It retains shared cells for later writes, loop-carried bindings, deferred writes, and writer closures.
A declaration inside a loop starts a fresh variable on each iteration.
The build now makes 17 allocations at 100,000 keys.

The interpreter also cells an earlier reader's variable when a later closure can write it.
Local variables and fresh loop-local variables retain their values.

The final bootstrap reaches a fixed point. All 11 strict size checks pass without baseline changes.
The native regression runs on x86-64, ARM64, and Wasm with memory checks.
Both interpreters pass the complete capture program.

Measurements, exact inputs, source scopes, and logs are in
`docs/benchmarks/capture-after-last-write-2026-10-23`.

The current integration check repeats the measurement after the JSON, codec, system, CSV and record changes.
At 100000 keys, allocation counts fall from 200018 to 17.
All 702 broad compiler regression cases pass.
The unchanged feature census also passes after four index loops become range loops.
The current evidence is in the `current-integration` subdirectory.

This change also fixes #13127, where an earlier reader missed a later writer's update in the self interpreter.
The related Go interpreter correction fixes #13128 because local function calls take precedence over builtins.
Both interpreters and all three execution targets pass the final matrix with balanced allocation counts.
