# Reuse the receiver type when checking call arguments

Part of #11534 and #8171.

For `make().method(arg)`, the checker already knows the type of `make()` when it starts to check the arguments. It now uses that type to resolve the method and look up its parameter types. Previously, each lookup typed the receiver expression again.

For a named call, the checker reuses the name lookup that reports an undefined function. Both paths skip argument diagnostics when the call target is missing. When a call has no arguments, the checker skips the parameter lookup. For calls with arguments, it still uses the expected parameter types.

Two stage-2 compilers built the same sources from `6b59a362d2` for x86-64 Linux. Both runs used Callgrind with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`.

| Source | Operation | Instructions before | Instructions after | Instructions saved |
| --- | --- | ---: | ---: | ---: |
| `conformance/cases/generic_array_method_chain/main.fern` | Compile | 859,662,789 | 856,087,418 | 3,575,371 |
| `compiler/checker.fern` | Check | 14,594,808,621 | 14,546,128,166 | 48,680,455 |
| `compiler/checker.fern` | Compile | 52,893,682,287 | 52,844,695,878 | 48,986,409 |

The full checker compile used 66,734,350 allocations, down from 66,802,358. It saved 68,008 allocations. Both compilers freed every allocation. An earlier version looked up parameter types for calls with no arguments. That version saved 25,157,309 instructions but made 20,214 extra allocations.

All 6,213 conformance compile outcomes matched on x86-64 Linux, ARM64 Linux and WASI. All 4,446 emitted binaries matched byte for byte. Both compilers gave identical diagnostics and exit status on all 516 expected-error fixtures.

The fixed checker binaries matched on x86-64 Linux, ARM64 Linux, ARM64 Darwin, both Android targets and WASI. The HTTP component matched. Assembly output matched for both `arm64-freestanding` and `x86-64-freestanding`. The new stage-2 compiler reproduced itself, and both method-chain binaries returned 19.
