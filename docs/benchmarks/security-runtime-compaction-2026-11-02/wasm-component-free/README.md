# Component raw-free classification

CI on `59e4f5d4` found a missing component rule for the new `free` dependency.
The component validator treated it as a host import.
The operation uses the internal allocator runtime and needs no host import.
The validator now accepts that operation in both component modes.
All other import rules remain intact.

Both failing component tests pass in 61.619 seconds.
The Wasm abort, host scratch, and cursor controls pass in 39.481 seconds.
Neither selection skips a test.
Full lint, primary type checks, format checks, and diff checks pass.

The records bind these results to the new source.
The earlier four-stage record retains its `59e4f5d4` source scope.
Full current-head GitHub CI remains a merge gate.
Performance limits remain unchanged.
