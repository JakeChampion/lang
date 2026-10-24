# Captures after the last write

Issue: #12927. Base: `8207edece5bcf7bb94bc5f4192024abc94c367d4`.

A map built in a loop copied on every insert when a later closure read it.
The compiler selected a shared cell because the variable changed anywhere in the function.
The cell kept a second map reference throughout the build.

The compiler now checks which assignments can follow each capture.
A read-only capture after the last assignment keeps the value without a shared cell.
Later outer assignments, writes in closures, and deferred assignments retain shared cells.
Loop backedges count as later writes for bindings outside the loop.
Declarations inside a loop create fresh bindings on each iteration.
Their next-iteration writes cannot change an earlier iteration's captured value.

The wide-capture pass retains its representation contract.
The interpreter also anticipates a writer closure before an earlier reader takes its snapshot.
This fixes a reader that kept zero after its sibling writer stored seven.
Fresh declarations still create fresh slots, including shadowed and per-iteration locals.

## Map build measurements

The instrumented issue reproducer counts allocations during the build.
Both compilers compile the same source with the same standard library.
Four execution pairs alternate baseline and candidate order on CPU 0.
The table gives median process wall times on this host.
The allocation counts match in all four executions of each variant.

| Keys | Baseline allocations | Candidate allocations | Baseline time | Candidate time |
| ---: | ---: | ---: | ---: | ---: |
| 25,000 | 50,016 | 15 | 0.3650 s | 0.003023 s |
| 50,000 | 100,017 | 16 | 2.8142 s | 0.005071 s |
| 100,000 | 200,018 | 17 | 14.8122 s | 0.010044 s |

The candidate's allocation growth follows capacity changes rather than inserts.
The original program remains in the compressed inputs beside the instrumented version and the helper control.
`measurements.json` records every execution, source hash, executable hash, and host setting.
Timings describe this host and do not establish a portable speed guarantee.

## Compiler measurements

Four pairs compile one fixed copy of the complete compiler, with execution order alternated on CPU 0.
The baseline median is 24.556 seconds. The candidate median is 22.786 seconds.
Baseline runs range from 21.929 to 27.554 seconds, and candidate runs range from 22.041 to 23.391 seconds.

Each variant produces one stable executable hash across all four runs.
Every candidate output matches the candidate compiler byte for byte.
`measure-final.py` records the exact commands and paths.

## Validation

- Stages 2 and 3 match: 14,244,240 bytes, SHA-256 `14f8be091600c86154d02186e89d473428051c8cc63546e53d001856acf4fdd0`.
- All 11 strict driver-size checks pass with the unchanged baselines. The stock driver group passes in 103.182 seconds.
- Final loop, closure-container, alias, environment, value-block, capture, and complexity checks pass in 120.338 seconds.
- The focused fresh-loop regression passes in 20.539 seconds on all three targets, with the x86-64 sanitizer and balanced allocation checks.
- The complete capture regression passes in both interpreters. It covers map and array builds, fresh loop locals, later assignments, sibling writes, backedges, deferred writes, and wide values.
- The reader-before-writer probe changes from `70` to `77` in the self-host interpreter. The Go interpreter returns `77`.
- All 1,177 tracked Fern sources in the formatter gate pass. `go vet ./...` passes.

Earlier sweeps have explicit source scopes.
The initial capture, cell, closure, and value-block sweep passes in 397.004 seconds before the interpreter and fresh-loop refinements.
The interpreter sweep passes in 139.372 seconds after the interpreter fix and before the final fresh-loop refinement.
`earlier-initial` and `earlier-with-interp` retain their bootstrap fingerprints and exact changed source inputs.
The archived inputs reconstruct each earlier complete source hash exactly against the same base.

The final compiler and standard library source hash is `a88fabf478074f072b272c2f1a9aa32df780e73aece2538dc40b1827589f5960`.
The hash covers every tracked file under `compiler` and `internal/stdlib`, including the new module.
`source_git` names the base because the measurements preceded the source commit.
The compressed inputs preserve exact bytes. `manifest.json` records the evidence file hashes.

## Existing fix audit

Commit `bae69fe1673f05e2d14c7314c262fe809ee856b5` in PR #12969 fixed issue #12820 on main.
Its exact nested-loop record reproducer now allocates zero times during 100 updates on x86-64, ARM64, and Wasm.
All three sanitizer runs report balanced allocations and zero live bytes.
The existing dead-construction suite also covers loop-carried cycles and finalizers.
We closed the issue after this check. `verified-existing-fix-12820` retains the probe and results from the unmodified main compiler.
