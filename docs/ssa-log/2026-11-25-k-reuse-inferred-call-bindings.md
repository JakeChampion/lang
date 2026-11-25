# Reuse inferred type arguments for named call diagnostics

Part of #11534 and #8171.

A named generic call can use the widths of integer literals to check its arguments. Its constraint diagnostics use the original inferred types. The checker now infers the type arguments once and keeps both sets of types for these checks.

Both stage-2 compilers built the same checker source from `846338c795` for x86-64 Linux. Callgrind ran with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`.

| Workload | Instructions before | Instructions after | Instructions saved |
| --- | ---: | ---: | ---: |
| Compile the checker | 53,062,690,732 | 53,030,042,566 | 32,648,166 (0.0615%) |
| Check the checker | 14,519,538,065 | 14,486,904,665 | 32,633,400 (0.2248%) |
| Check the callback fixture | 8,918,341 | 8,910,584 | 7,757 (0.0870%) |

The full compile made 67,296,205 heap allocations before the change and 67,269,055 after it. Both runs freed every allocation.

All 6,222 conformance outcomes matched on x86-64 Linux, ARM64 Linux and WASI. All 4,449 emitted binaries matched byte for byte. Both compilers rejected all 518 expected-error fixtures with identical output.

The fixed checker binaries matched on six targets. The HTTP component and the assembly for `arm64-freestanding` and `x86-64-freestanding` also matched. Each stage-2 compiler reproduced itself. Both compilers passed the mixed-width integer, dynamic trait bound and bound string literal cases.
