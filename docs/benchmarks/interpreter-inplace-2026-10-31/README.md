# Interpreter array ownership

The statement and loop evaluators borrowed Env, so the caller retained its array values during each write.
The candidate consumes Env through those evaluators and their mutation helpers.
Read-only expression evaluators keep their borrowed contract.
Shallow Env records retain shared values at borrowed transfer boundaries.

The allocation profile at 2000 elements identified array copies inside write_array_in_place.
Both append and with reached the array allocation path.
The profile had no dropped samples. Its weighted samples identify call sites, not exact allocated byte counts.

The paired control and candidate use the same compiler and stdlib.
Only compiler/interp.fern differs between their source trees.
Each input appends zero bytes, replaces each byte, and checks the final value.
Only the element count changes between scales.
The first scale checks the complete pipeline before the larger runs.

Single process durations include startup and concurrent validation. They establish regression behavior, not stable throughput.

| Elements | Control, seconds | Candidate, seconds |
| --- | --- | --- |
| 8 | 0.218986208 | 0.277489250 |
| 20000 | 2.478254292 | 0.058299500 |
| 80000 | 47.774998000 | 0.180738417 |

TestSelfHostInterpArrayInPlace passes all eight semantic cases and its unchanged duration bound.
Separate native and WASI runs balance every allocation and leave zero live bytes.
The complete interpreter self-tests pass with strict checks and zero verifier skips.
The frame and session contracts also pass on WASI. Linux execution legitimately skips on this Mac without QEMU.

The native driver size stays at 2930497 bytes.
The WASI driver adds 6572 bytes, and the x86 compiler image adds 12288 bytes.
These costs follow from the explicit ownership transfers and borrowed record boundaries.
The compiler image remains below its 15881456-byte baseline.
No baseline or test limit changed.

results.json records source identities, the compiler hash, durations, sizes, and allocation counts.
