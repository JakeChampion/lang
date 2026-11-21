# The register allocator counts phi instructions directly

Part of #8171 and #11534.

Phi results in a block need overlapping live ranges because the incoming edge writes them together. The linear-scan allocator used to filter the instruction array and read the length of the result to find the last phi position. It now counts matching instructions in one pass. It counts the whole block, including any phi after an ordinary instruction. An empty block or a block without phis still contributes zero.

Two self-built x86-64 Linux compilers compiled the same checker source from `fc213d60d180`. Only the second compiler included the phi count change. Both reproduced themselves when rebuilt.

| Measurement for `compiler/checker.fern` | Before | After | Reduction |
| --- | ---: | ---: | ---: |
| Callgrind instructions to emit an x86-64 executable | 48,386,225,068 | 48,361,629,841 | 24,595,227 (0.051%) |
| Heap allocations to emit x86-64 assembly | 54,464,032 | 54,436,501 | 27,531 (0.051%) |

Callgrind ran with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`. The allocation measurement used leak-instrumented builds with `FERN_RC_CERTIFY=0` and the absolute paths from the GitHub runner. Both freed every allocation and reported zero live bytes.

The two compilers emitted byte-identical checker binaries on x86-64 Linux, ARM64 Linux and wasm32 WASI. The phi-cycle cases with 2, 24 and 40 live values ran successfully on all three targets.
