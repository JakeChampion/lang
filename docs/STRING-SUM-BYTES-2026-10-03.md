# Raw byte input for sum

`sum` reads byte arrays and borrows them through `hash.BsdSum.update_array`
or `hash.SysvSum.update_array`. Arbitrary input never becomes text. BSD
rotation, System V wrapping and folding, block counts and diagnostics retain
their existing behavior. Both methods use the existing native kernels.

The final checksum compiler passes 50 GNU cases on Darwin and core WASM,
with exact output and balanced allocations. Coverage includes both
algorithms, every byte value, empty input, 512-byte and 1 KiB boundaries,
64 KiB read boundaries, multiple chunks, pipes and separate files.
The consumer does not change compiler source; it uses the reproduced stage 2
documented in [the reduction report](STRING-BYTE-REDUCTIONS-2026-10-03.md).

The same compiler builds text and raw versions. Raw input removes 744 bytes
of code and adds 176 bytes of unwind data. Static data is unchanged; file
size falls from 116,273 to 116,257 bytes. No size baseline changes.

Linux target, GNU parity, full unit and lint checks pass on the frozen
source snapshot. The GNU corpus includes options and error behavior.

An 8 KiB pilot precedes the same native benchmark at 8 MiB. Input repeats
all 256 byte values. Both Fern implementations and uutils 0.12.0 match GNU
9.12 exactly before timing. Two warmups precede seven alternating samples,
reading a file through stdin and directing output to the null device.
Peak resident memory is measured separately. No compiler or test job from
this workstream runs during measurement; other macOS services remain active.

| Algorithm | Implementation | Median ms | Sample range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| BSD | Previous text | 13.745125 | 13.487916-18.105583 | 1,212,416 |
| BSD | Raw bytes | 13.652167 | 13.385458-15.825375 | 1,212,416 |
| BSD | GNU 9.12 | 15.462500 | 15.030583-15.871750 | 1,196,032 |
| BSD | uutils 0.12.0 | 12.030708 | 11.852542-12.649208 | 1,753,088 |
| System V | Previous text | 2.653792 | 2.333167-3.672125 | 1,245,184 |
| System V | Raw bytes | 2.593000 | 2.447458-2.808958 | 1,212,416 |
| System V | GNU 9.12 | 4.225500 | 4.000833-4.271958 | 1,196,032 |
| System V | uutils 0.12.0 | 2.877083 | 2.691959-3.568208 | 1,753,088 |

Before/after sample ranges overlap for both algorithms. These measurements
show comparable throughput on this input and establish no general speedup.
