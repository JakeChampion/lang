# Check bound parameters once in named calls

Part of #11534 and #8171.

For each argument, the checker determines whether the parameter uses a bound type variable. It now uses that answer for both generic and ordinary argument diagnostics. Concrete parameters skip the generic diagnostic helper.

Both stage-2 compilers built the same checker source from `1376fb2891` for x86-64 Linux. That revision had an invalid call in the IR verifier. Both compiler builds used the same verifier file from #17141 at `f85eb27265`. The checker change was the only difference between them. Callgrind ran with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`.

| Workload | Instructions before | Instructions after | Instructions saved |
| --- | ---: | ---: | ---: |
| Compile the checker | 52,771,660,465 | 52,756,172,756 | 15,487,709 (0.0293%) |
| Check the checker | 14,427,114,081 | 14,411,632,317 | 15,481,764 (0.1073%) |

The full compile made 67,490,516 heap allocations before the change and 67,465,560 after it. Both runs freed every allocation.

All 6,222 conformance compile outcomes matched across x86-64 Linux, ARM64 Linux and WASI. All 4,449 emitted binaries matched byte for byte. Both compilers rejected all 518 expected-error fixtures with identical diagnostics.

The fixed checker binaries matched on six targets. The HTTP component and the assembly outputs for `x86-64-freestanding` and `arm64-freestanding` also matched. Each stage-2 compiler reproduced itself. Twelve native generic-call fixtures passed their execution checks.
