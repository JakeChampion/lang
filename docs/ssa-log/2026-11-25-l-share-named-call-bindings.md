# Share generic bindings across named call diagnostics

Part of #11534 and #8171.

The checker used to infer the same type arguments twice for a declared function call. It now infers them once. It keeps the original types for constraint diagnostics and the literal widths for argument checks. The expected result can still supply missing types when the checker determines the destination of each argument.

Both stage-2 compilers built the checker source from `b502130b2f` for x86-64 Linux. Callgrind ran with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`.

| Workload | Instructions before | Instructions after | Instructions saved |
| --- | ---: | ---: | ---: |
| Compile the checker | 53,755,192,210 | 53,709,639,040 | 45,553,170 (0.0847%) |
| Check the checker | 15,108,444,835 | 15,062,233,008 | 46,211,827 (0.3059%) |
| Check the callback fixture | 8,826,455 | 8,818,578 | 7,877 (0.0892%) |

The full compile made 68,631,166 heap allocations before the change and 68,606,604 after it. Each run freed every allocation. Each stage-2 compiler reproduced itself.

All 6,222 conformance outcomes matched across x86-64 Linux, ARM64 Linux and WASI. All 4,449 emitted binaries matched byte for byte. Both compilers rejected all 518 expected-error fixtures with identical diagnostics. The fixed checker binaries matched on six targets. The WASI HTTP component and the assembly outputs for `arm64-freestanding` and `x86-64-freestanding` also matched.

On this base, the x86-64 checker refuted 5,378 planted reference-count errors, weakened 227 and missed 228. With the change it refuted 5,393, weakened 226 and missed 228. The count of unproved values stayed at 706 for the native compiler and 84 for the WASI checker.
