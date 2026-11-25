# Reuse the receiver type in method diagnostics

Part of #11534 and #8171.

For a call such as `make().method(arg)`, the receiver checks infer the type of `make()`. The method checks previously repeated this step. They now use the type from the receiver checks. Argument checks still run between the receiver and method checks. Qualified variants and associated functions still return from their own diagnostic paths.

Two stage-2 compilers built the same sources from `23878d9486` for x86-64 Linux. Both runs used Callgrind with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`.

| Source | Operation | Instructions before | Instructions after | Instructions saved |
| --- | --- | ---: | ---: | ---: |
| `conformance/cases/generic_array_method_chain/main.fern` | Compile | 862,407,374 | 861,804,356 | 603,018 |
| `compiler/checker.fern` | Check | 14,644,684,827 | 14,629,823,016 | 14,861,811 |
| `compiler/checker.fern` | Compile | 52,786,341,723 | 52,771,117,886 | 15,223,837 |

The checker binaries matched on x86-64 Linux, ARM64 Linux, ARM64 Darwin, both Android targets and WASI. Both method-chain binaries returned 19.

With heap counts enabled, the fixed checker compile made 66,429,076 allocations, down from 66,441,902. Both compilers freed every allocation.
