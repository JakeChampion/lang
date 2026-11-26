# Reuse the scrutinee type in value-producing matches

Part of #11534 and #8171. Follows #17251.

The checker resolves the scrutinee in the outer scope. Each arm uses this type to bind its pattern. The arm-type diagnostic also uses it to identify enums. Destination-type checks, assignment collection and literal inference resolve the scrutinee once per match in each pass. Guards and result expressions use the scope of their own arm.

## Measurements

I built both compilers through stage 2 from `9763e11096b7`. Both compiled the same base sources for x86-64 Linux. Callgrind counted instructions with verification on and the certifier memo off.

| Workload | Base | Branch | Fewer instructions |
| --- | ---: | ---: | ---: |
| Compile the fixed checker | 56,302,465,756 | 56,301,424,801 | 1,040,955 (0.0018%) |
| Check the fixed checker | 15,114,053,134 | 15,112,882,267 | 1,170,867 (0.0077%) |
| Check a 26-arm value match | 12,958,269 | 12,386,690 | 571,579 (4.4109%) |
| Check the `match_multi_arm` fixture | 6,503,914 | 6,503,864 | 50 (0.0008%) |

The 26-arm fixture matches a call that returns `Option[i32]`. It has 24 guarded arms, one unguarded payload arm and one empty-variant arm. The result has a `u8` destination type.

The fixed checker compile used 72,258,454 heap allocations on the base and 72,257,198 on the branch. Both compilers reported zero live bytes at exit.

## Verification

Both stage-2 compilers reproduced themselves. All 6,222 conformance compile outcomes matched. All 4,449 emitted binaries were identical. Both compilers produced the same diagnostics for 518 expected-error fixtures and three focused cases. The fixed checker binaries matched on six targets. The HTTP component and the assembly for `arm64-freestanding` and `x86-64-freestanding` also matched.

All 24 focused value-match programs passed on x86-64 Linux, ARM64 Linux and WASI. All 72 binaries matched. All 78 JSON records across 52 match error fixtures kept their order. Two further programs preserved both E031 diagnostics for incompatible arm types and both E030 diagnostics for an empty value match. The multi-module return test and three comparisons of value-block and statement-form assembly passed on both native targets.

## Certifier counts

The x86-64 checker refuted one fewer planted imbalance in each of `call_result_type` and `mx_match_arm_types`. It refuted one more in `lit_value_branch`. The total refuted fell from 5,378 to 5,377, which matches the main pin. The total weakened rose from 229 to 230. The branch pins 230.

Three strict rows differed from the main pins on both builds. Native `fern_main.unproved` measured 720 against 602. WASI `checker.unproved` measured 70 against 67. Native `checker.missed` measured 229 against 228.
