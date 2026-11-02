# Wasm raw-free parity

The guard on head `21f09f1c` rejects a valid zero-byte allocation at the heap cursor.
An empty environment causes this path in `env` and `environ`.
The correction permits zero bytes exactly at the cursor.
It preserves both address bounds and both duplicate-head guards.
The census still counts the zero-byte allocation and release.

A program that only calls `__free` also needs the allocator runtime.
The dependency list now records `free` and includes those helpers.
The isolated-dependency test covers the new entry.
The shared outside-heap abort case now links and reports its cause.

The targeted tests cover both interpreters, Wasm aborts, host scratch reclamation, and invalid releases at or above the heap cursor.
All 117 isolated runtime dependencies validate.
The source and lint gates pass.
The compiler reproduces itself byte for byte.
The records retain source hashes, commands, results, and the exact qualification scope.

Performance limits remain unchanged.
Full current-head GitHub CI remains a merge gate.
Earlier records retain their original source scope.
