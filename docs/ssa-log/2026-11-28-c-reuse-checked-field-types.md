# Reuse checked field types during lowering

Part of #11534 under #8171.

The checker stores field types in declaration order. Semantic lowering reads these types for a record, an enum literal and a match arm.
It can use the same array when all fields already have their runtime type.
Two predicates check the callable ownership flags and the arguments of generic enums in the current scope.
When either test finds a field that needs conversion, the resolver processes each field in declaration order.
Record instance substitution still follows this step.

## Compilation costs

The fixed input was `compiler/checker.fern` on main at `39d6eaab8ff1`.
The compiler and library sources at `1af209620f29` matched that input.
Both stage-2 compilers ran under Callgrind with the same paths, arguments and verification settings.
Each pair produced the same x86-64 Linux binary.

| Stage-2 compile | Main instructions | Branch instructions | Change |
| --- | ---: | ---: | ---: |
| Full checker | 97,220,360,153 | 97,182,243,442 | -38,116,711 |
| 1 field | 37,565,280 | 37,565,199 | -81 |
| 16 fields | 41,685,457 | 41,675,277 | -10,180 |
| 64 fields | 50,918,925 | 50,877,819 | -41,106 |
| Scalar control | 18,515,879 | 18,516,387 | +508 |
| Bare generic enum first | 47,927,262 | 47,935,509 | +8,247 |
| Bare generic enum last | 47,982,076 | 48,000,307 | +18,231 |
| Optimized `http_hello.fern` | 23,587,299,182 | 23,586,854,547 | -444,635 |

The record fixture constructs a record, updates its first field and reads the result.
The two enum controls place a field of type `Shape` before or after 32 integer fields.
The generic enum `Shape[T]` declares the variant `Circle(i32)`. Both controls construct the record, update an integer field and match the enum.
The [record lookup study](2026-11-28-b-use-record-declarations-in-lowering.md#reproduce-the-record-fixture) includes the generator.
The scalar control calls a function that adds one to an integer.

| Heap allocations | Main | Branch | Change |
| --- | ---: | ---: | ---: |
| Full checker | 74,949,604 | 74,883,060 | -66,544 |
| 64 fields | 117,210 | 117,185 | -25 |
| 1 field | 99,745 | 99,740 | -5 |

The instrumented compilers freed every allocation. Both stage-2 compilers reproduced themselves at stage 3.
The branch compiler grew by 80 bytes.

## Verification

All 6,300 conformance results matched across x86-64 Linux, ARM64 Linux and WASI.
These included 4,476 identical binaries and 1,824 matching refusals.
Both reports had SHA-256 `406b04067781d7118b9d693d7db428a529bb1fa5dde69ace752e326a0c883c99`.
All 532 expected-error JSON diagnostics matched.
Six checker outputs, the HTTP component and both freestanding assembly outputs matched.

The 114 focused comparisons covered record construction, updates, field reads, generic enums and matches on all three targets.
Another 42 comparisons covered ordinary builds, allocation checks and sanitizer builds.
They included two imported record types with the same name.

A direct test passed on each compiler on all three targets.
It checked a generic enum and a plain enum with the same name in two scopes.
It also checked nested map, array and tuple types, argument order and ownership of function parameters and results.
The original input types retained their fields and flags.

The closure payload and enum-in-record fixtures gave 34 more matching binary and execution pairs.
They covered owned callbacks in constructed, returned and nested variants.
Both compilers passed the allocation checks on all three targets and the x86-64 sanitizer cases.

Both compilers passed 115 ownership certifier cases. All 2,380 per-function mutation records matched.
The strict benchmark checks passed for the five ownership rows and both shared-array rows on each compiler.
Both stage-1 compilers measured 57,847,600 shared-array bytes in 1,006,254 copies on either checker source.

## Integration with main

The branch includes the ARM64 multiply and Unicode changes from main at `b27bd49fc684`.
Both stage-1 compilers reproduced the required ARM64 shifts and adds for multiplication by a constant.
The control for multiplication by ten kept its multiply instruction.
The arithmetic program and all 35 regex cases passed on x86-64 Linux, ARM64 Linux and WASI.
The 64-field record and the record with a generic enum as its last field also passed on all three targets.

All 31 ARM64 benchmark assemblies affected by the new size baselines matched byte for byte.
Both compilers reproduced every one of those line counts.
