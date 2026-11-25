# Infer generic type arguments once per call

Part of #11534 and #8171.

A generic call can bind its type parameters from its arguments and its expected result. The checker previously repeated this work for each argument. It now infers the bindings once and uses them to find the expected type of each argument. The compiler also shares these bindings when it instantiates multiple generic callbacks in one call.

Two stage-2 compilers built the same checker sources from `d0094a0aad` for x86-64 Linux. Both used Callgrind with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`.

| Workload | Instructions before | Instructions after | Instructions saved |
| --- | ---: | ---: | ---: |
| Compile the checker | 53,070,523,865 | 52,911,185,766 | 159,338,099 (0.3002%) |
| Check the checker | 14,618,012,739 | 14,458,701,472 | 159,311,267 (1.0898%) |
| Check two generic callbacks and a shadowed name | 9,031,696 | 9,005,644 | 26,052 (0.2885%) |

The full checker compile made 67,529,648 heap allocations before the change and 67,392,536 after it. Both runs freed every allocation.

All 6,222 conformance compile outcomes matched across x86-64 Linux, ARM64 Linux and WASI. All 4,449 emitted binaries matched byte for byte. The two compilers gave identical diagnostics and exit statuses on all 518 expected-error fixtures.

The fixed checker binaries matched on six targets. The HTTP component and the assembly for both `arm64-freestanding` and `x86-64-freestanding` also matched. The stage-2 compiler reproduced itself.
