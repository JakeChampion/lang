# Reuse local binding types during literal inference

Part of #11534 and #8171.

The literal-inference pass now uses a local’s declared type both to check its initializer and to bind its name. For an ordinary inferred local, the open enum check already computed the initializer type. The pass uses that type to bind the local. A nested function that binds its own name still gets its recursive scope.

Both stage-2 compilers built the checker source from `de5bf2a13cff` for x86-64 Linux. Callgrind ran with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`.

| Workload | Instructions before | Instructions after | Instructions saved |
| --- | ---: | ---: | ---: |
| Compile the checker | 53,784,708,081 | 53,776,106,939 | 8,601,142 (0.0160%) |
| Check the checker | 15,066,002,572 | 15,048,958,419 | 17,044,153 (0.1131%) |
| Check the literal index-read fixture | 7,913,855 | 7,913,209 | 646 (0.0082%) |

The full compile made 68,762,744 heap allocations before the change and 68,757,518 after it. Both compilers freed every allocation. Each stage-2 compiler reproduced itself.

All 6,222 conformance outcomes matched. All 4,449 emitted binaries and the diagnostics from all 518 expected-error fixtures matched byte for byte. The fixed checker binaries matched on six targets. The WASI HTTP component and the assembly outputs for `arm64-freestanding` and `x86-64-freestanding` also matched.

All 19 recursive-local cases and 10 conformance programs ran on x86-64 Linux, ARM64 Linux and WASI. The tests cover closures, literal widths, empty maps, open enums and tuple and struct patterns. All 87 binaries matched. A separate test of an explicitly typed local that shadows an inferred local also passed on all three targets.
