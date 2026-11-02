# Integration with main

Main commit `7ea2ec826a9d314ebaff24cbc42590bc4b1073ed` adds the two-byte mismatch path.
The merge preserves that source and its measured limits.
The four lower red-black-tree instruction limits from this PR remain unchanged.
Only adjacent comments conflicted in the benchmark file.

The combined source passes seven Linux test roots in 100.270 seconds.
These tests cover mismatch behavior, nested-token plans, regions, execution and complexity.
The suite uses native ARM64, QEMU x86 and Wasm where each test supports them.
The source gate covers five runtime targets and 90 compiler drivers.
The lint gate also passes.

Compiler stages 2, 3 and 4 match at `ac031f74b7b9c7a46d734f6a6d7844fd11ceb0ad5894e08044f23854d4c7bc12`.
Stage 1 differs.
The qualification record binds compiler, standard-library and embedding inputs to their hashes.
The embedding retains its earlier version label.
Earlier cost records retain their earlier compiler scope.
Full CI on the merge commit remains required.
