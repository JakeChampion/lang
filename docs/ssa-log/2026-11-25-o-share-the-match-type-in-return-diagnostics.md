# Check the scrutinee once for match return diagnostics

Part of #11534 and #8171. Follows #17230.

Four return-diagnostic passes need the type of a match expression to resolve the pattern bindings in each arm. Each pass now checks the scrutinee once in the outer scope. Each arm still has its own scope. The value-block and lambda passes visit the scrutinee for diagnostics before they visit the arms.

## Measurements

I built both compilers through stage 2 from `bc44d18d120f` and compiled the same base sources with each one. Callgrind counted x86-64 Linux instructions with verification enabled and the certifier memo disabled.

| Workload | Base | Branch | Change in instructions |
| --- | ---: | ---: | ---: |
| Compile the fixed checker | 55,923,388,403 | 55,917,076,274 | -6,312,129 (-0.0113%) |
| Check the fixed checker | 15,051,427,256 | 15,045,114,329 | -6,312,927 (-0.0419%) |
| Check a 26-arm match with a call as its scrutinee | 10,181,334 | 10,078,006 | -103,328 (-1.0149%) |
| Check the existing `match_multi_arm` fixture | 6,467,240 | 6,467,284 | +44 (+0.0007%) |

The fixed checker compile used 70,305,279 heap allocations on the branch and 70,307,381 on the base. Both compilers reported zero live bytes at exit.

## Verification

Both stage-2 compilers reproduced themselves. All 6,222 conformance compile outcomes matched. All 4,449 emitted binaries were identical. All 518 expected-error diagnostics and three focused diagnostics matched. Both compilers produced identical fixed checker binaries on six targets. The HTTP component and the `arm64-freestanding` and `x86-64-freestanding` assembly outputs also matched.

Nineteen match programs passed on x86-64 Linux, ARM64 Linux and WASI. All 57 binaries matched. Across 52 match error fixtures, all 78 JSON records matched in order. Six additional cases covered typed and void functions, value blocks, lambda returns, empty matches and a valid guarded match. On both native targets, the multi-module return test selected the expected enum variant. Three value-block functions produced the same instruction sequences as their statement forms.

## Certifier counts

In `lret_stmts` and `void_ret_diags`, the certifier classified one planted imbalance per function as weakened after the change. It previously classified each as refuted. The total refuted fell from 5,379 to 5,377. The total weakened rose from 227 to 229. The branch pins both counts.

Three strict rows differed from the main pins on both builds. Native `fern_main.unproved` measured 720 against 602. WASI `checker.unproved` measured 70 against 67. Native `checker.missed` measured 229 against 228.
