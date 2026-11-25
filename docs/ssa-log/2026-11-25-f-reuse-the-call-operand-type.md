# Reuse the type of a call in an operator expression

Part of #11534 and #8171.

In `left.read() + right.read()`, the checker needs each result type twice. It uses the type to diagnose call errors and to check the addition. It used to compute each result twice. The checker now computes the result once, applies the type-size limit, and uses that type for both checks. A call under a unary operator, such as `!ready()`, uses the same path.

Two stage-2 compilers built the same source from `139d55edf9` for x86-64 Linux. Both runs used Callgrind with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`.

| Source | Operation | Instructions before | Instructions after | Instructions saved |
| --- | --- | ---: | ---: | ---: |
| `conformance/cases/generic_array_method_chain/main.fern` | Compile | 863,289,157 | 861,931,919 | 1,357,238 |
| `compiler/checker.fern` | Check | 14,635,642,139 | 14,616,631,970 | 19,010,169 |
| `compiler/checker.fern` | Compile | 52,041,297,417 | 52,022,078,650 | 19,218,767 |

With heap counts enabled, the fixed checker compile made 65,754,290 allocations, down from 65,764,614. Both compilers freed every allocation. The checker binaries matched on x86-64 Linux, ARM64 Linux, ARM64 Darwin, both Android targets and WASI. Both method-chain binaries returned 19.
