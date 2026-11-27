# Use type-table indices for schema roots

Part of #11534 and #8171.

Semantic lowering collects root types from the values, result and parameters of a function. Previously, a name lookup returned an index into a second array of integer offsets. The offset then selected the type. The root table now gives each spelling and its type the same index. Schema and environment discovery consume the types in their original order.

An empty spelling still adds a root each time. A nonempty spelling still selects the first entry with that spelling and checks structural equality. The direct probe interleaved empty spellings, two names in the same hash bucket, and a struct and union with the same name. Both compilers produced the same six roots in the same order. Both also skipped the scalar input.

## Stage-2 measurements

The comparison used compiler sources from main at `2e2476185d41`. The next main commit, `325e3e00aff7`, updated only the certifier baseline. The pinned stage 0 built each stage 1, which then built its own stage 2. Callgrind counted x86-64 instructions with IR verification and ownership certification enabled.

| Input | Main instructions | Branch instructions | Difference |
| --- | ---: | ---: | ---: |
| 1 record parameter | 52,784,656 | 52,784,403 | -253 |
| 32 record parameters | 63,549,882 | 63,547,825 | -2,057 |
| 128 record parameters | 109,837,271 | 109,830,795 | -6,476 |
| 128 array fields with a dynamic element type | 76,879,358 | 76,878,711 | -647 |
| Scalar control | 17,793,807 | 17,793,785 | -22 |
| Generic closure dispatch | 465,420,032 | 465,416,469 | -3,563 |

The record programs sum the fields of distinct record parameters in a function with the `@noinline` attribute. All six programs produced the expected results and identical outputs on x86-64 Linux, ARM64 Linux and WASI.

The checker benchmark copied 58,521,008 shared-array bytes in 1,022,784 copies on main. The branch copied 57,717,416 bytes in 1,006,175 copies. This saved 803,592 bytes and 16,609 copies. Both compilers reproduced their respective counts on both inputs.

The 16 additional closure and dynamic dispatch programs passed 48 execution comparisons on three targets. All 530 expected-error JSON diagnostics matched. The feature and complexity checks passed. Both compilers accepted the changed file in the format check.
