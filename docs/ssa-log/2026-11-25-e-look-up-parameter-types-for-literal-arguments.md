# Look up parameter types for literal arguments

Part of #11534 and #8171.

When the checker annotates a call such as `send(message, get_id())`, both arguments already have types. It previously looked up the parameter types and walked the argument list again. It now skips that step unless the call has an integer literal expression, an array literal or a struct literal.

An integer literal expression can take its width from a parameter. Arrays can contain struct literals whose field types come from the parameter. If a call contains any of these expressions, the checker processes the full list so each argument keeps its position.

Two stage-2 compilers built the same sources from `ea0db7c0db` for x86-64 Linux. Both runs used Callgrind with `FERN_RC_CERTIFY=1` and `FERN_RC_CERTIFY_MEMO=0`.

| Source | Operation | Instructions before | Instructions after | Instructions saved |
| --- | --- | ---: | ---: | ---: |
| `conformance/cases/generic_array_method_chain/main.fern` | Compile | 867,160,720 | 863,889,778 | 3,270,942 |
| `compiler/checker.fern` | Check | 14,685,953,834 | 14,589,662,267 | 96,291,567 |
| `compiler/checker.fern` | Compile | 49,332,517,956 | 49,276,084,231 | 56,433,725 |

The checker binaries matched on x86-64 Linux, ARM64 Linux, ARM64 Darwin, both Android targets and WASI. The method-chain binaries matched and returned the expected exit status.
