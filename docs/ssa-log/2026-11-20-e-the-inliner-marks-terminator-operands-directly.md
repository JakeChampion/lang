# The inliner marks terminator operands directly

Issues #8171 and #11534.

The inliner counts instructions to decide which functions to inline. It records which values each block reads. Each block terminator supplies two possible reads, its condition and its value. The inliner previously built an array to visit those two operands. It now marks each operand directly. Both paths skip absent or out-of-range value IDs.

Two self-built x86-64 Linux compilers compiled the same source tree from `ba633a6a3113`. The second compiler included the change to the inliner. Both compiled each input to an x86-64 Linux executable.

| Measurement | Input | Before | After | Reduction |
| --- | --- | ---: | ---: | ---: |
| Callgrind instructions | `compiler/checker.fern` | 48,499,931,813 | 48,392,055,029 | 107,876,784 (0.22%) |
| Heap allocations | `compiler/checker.fern` | 55,176,892 | 53,464,687 | 1,712,205 (3.10%) |
| Heap allocations | `compiler/fern.fern` | 246,853,242 | 237,647,586 | 9,205,656 (3.73%) |

Callgrind ran with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`. Separate compiler builds included the allocation counters from `FERN_LEAKCHECK=1`. These compilers ran with `FERN_RC_CERTIFY=0` and freed every allocation. Each pair produced the same executable for its input. The checker assembly also matched byte for byte on x86-64 Linux, ARM64 Linux and wasm32 WASI.

The CI benchmark emits checker assembly and uses absolute paths. With main at `eac3fcdc40e0` and the GitHub checkout path, the compiler on main took 56,176,237 allocations and the updated compiler took 54,464,032. Both freed every allocation.
