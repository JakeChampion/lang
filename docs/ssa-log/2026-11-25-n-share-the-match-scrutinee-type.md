# Share the scrutinee type across match arms

Part of #11534 and #8171.

The checker annotates the scrutinee once in the outer scope. Each arm uses its type for the pattern and the arm scope. If the annotation has no type, the checker resolves the original expression once. The guard and body use the scope of their arm.

## Measurements

I built both compilers through stage 2 on `a9a8180911b0`. Each compiler used the same base sources, standard library and target for each measured workload. Callgrind counted x86-64 instructions with verification enabled and the certifier memo disabled.

| Workload | Base | Branch | Fewer instructions |
| --- | ---: | ---: | ---: |
| Compile the fixed checker | 53,835,356,040 | 53,825,923,289 | 9,432,751 (0.0175%) |
| Check the fixed checker | 15,048,554,758 | 15,034,512,848 | 14,041,910 (0.0933%) |
| Compile the guarded match fixture | 52,691,892 | 52,681,572 | 10,320 (0.0196%) |

The fixed checker compile used 68,766,204 heap allocations, down from 68,769,654. Both compilers reported zero live bytes at exit.

## Verification

Both stage-2 compilers reproduced themselves. All 6,222 conformance compile outcomes matched. All 4,449 emitted binaries were identical. All 518 expected-error fixtures and three focused diagnostics matched. The fixed checker binaries matched on six targets. The HTTP component and the assembly for `arm64-freestanding` and `x86-64-freestanding` matched.

All 19 match programs passed on x86-64 Linux, ARM64 Linux and WASI. All 57 binaries matched. A separate program checked a call and a struct literal as scrutinees. Each target produced the expected result and printed the call marker once. All 78 JSON diagnostic records across 52 match, pattern, payload and guard fixtures matched in order.

## Integration on `df505541c282`

Both compilers produced the same five certifier counts after the baseline change on main. The strict check reported these inherited differences on both builds.

| Reference count metric | Main pin | Base and branch |
| --- | ---: | ---: |
| `x86_64/fern_main.unproved` | 677 | 685 |
| `x86_64/checker.refuted` | 5393 | 5392 |
| `x86_64/checker.weakened` | 226 | 227 |

The six fixed checker outputs and all expected-error diagnostics matched on this base. The HTTP and assembly outputs also matched. The focused match programs passed again on all three targets.
