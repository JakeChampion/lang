# Nonaggregate types in view queries

`holds_view_aggregate` now rejects concrete nonaggregate types before the recursive query.
The previous query always returned false for these types and constructed a result tuple.
Type variables still receive the conservative answer before classification.
Unresolved aggregate spellings retain the recursive fallback.
The cache still stores only complete root queries.
Recursive visits with partial ancestor sets do not enter the cache.

## Fixed input

`fixed-input.json` compares two instrumented compilers on one immutable checkout.
Both compilers use the same builder and runtime libraries.
Three repeats per compiler alternate their execution order.
All six processes produce identical assembly and report zero final live bytes.
The collector verifies source and tool hashes before and after execution.

| Compiler | Allocations | Frees |
| --- | ---: | ---: |
| Before | 34,783,892 | 34,783,892 |
| After | 34,718,352 | 34,718,352 |

The candidate saves 65,540 allocation events on this fixed input.
Source commits and binary hashes appear in the raw record.
The before compiler has the same compiler and stdlib sources as main `717e92f9e2fbef7e4dc793360b67109caee2c62b`.
Both instrumented compilers use builder `29028eb88d3e`.
Its SHA-256 is `0cfd830e60832caf99c3ffddfba66aee6f7669e9833287b1734f58a358c93a40`.

## CI context

The earlier function-facts repair reduced allocation events but did not establish passing CI.
DWARF integration head `e520b4b90ae0a8defdf3e0e36bb9aa277207c748` reports 34,788,196 allocations and frees in job121420078243.
CI40276431276 rejects its 10.0041% drift against the unchanged baseline of 31,624,467.
The local integrated workload reports 34,786,607 and narrowly passes.
These observations use different source and build contexts.
They are not a controlled comparison.

The default gate record supplies the candidate result separately.
The candidate reports 34,717,661 allocations and frees.
The unchanged 10% ceiling passes, and the 1% advisory warning persists.

The default workload compiles the candidate checker itself.
Use the fixed-input campaign to isolate the compiler effect.
No baseline, threshold, exemption, or lifetime rule changes.
The broader allocation issue #13464 remains open.

The compiler reports `ccef3afdde8a-dirty`.
Untracked qualification files caused the dirty marker during the build.
Compiler and stdlib sources match the source commit in the records.
The compiler SHA-256 is `8d8ae719faa91172c41b8c89260395b8917713b8d29c111700950445a2a41d45`.

## Correctness and limits

The complete native diagnostic and Go differential suites pass.
Both unchanged complexity gates pass.
New cases instantiate one generic identity function with scalar values and views.
They preserve local-storage rejection and parameter-backed returns.
The portable collector also exercises recursive holders, generic aggregates, reassignment, and independent function scopes.

These records count allocation events on finite inputs.
They do not measure CPU time, peak live bytes, allocator capacity, RSS, or a process memory bound.
ARM64 probes use QEMU and do not constitute the full Go ARM harness.
Failed, skipped, or pending substantive CI is not passing validation.
