# Loop splitting reuses the allocator’s floating-point classification

Part of #8171 and #11301.

The x86-64 register allocator classifies SSA values that need floating-point registers. Loop splitting needs the same classification to exclude floating-point values from its spill candidates. The emitter now computes the classification once per graph and passes it to both consumers. It recomputes the table when the loop-index copy changes the graph and computes a separate table for a proposed split.

Two self-built x86-64 Linux compilers built the checker from `b340c0527bf7`. Callgrind counted instructions with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`. Leak-instrumented builds used `FERN_RC_CERTIFY=0` and the GitHub runner’s absolute paths. Both freed every allocation and reported zero live bytes.

| Measurement for `compiler/checker.fern` | Before | After | Reduction |
| --- | ---: | ---: | ---: |
| x86-64 instructions to emit an executable | 48,367,117,416 | 48,321,232,564 | 45,884,852 (0.095%) |
| Heap allocations to emit assembly | 54,436,501 | 54,426,974 | 9,527 (0.018%) |

Both self-built compilers reproduced themselves. They produced the same checker executable. The loop-index copy, loop split, register form, void phi and floating-point loop fixtures passed with byte-identical executables. The loop-split fixture also retained its frame cell. Formatting, compiler complexity and the five reference-count census rows passed.
