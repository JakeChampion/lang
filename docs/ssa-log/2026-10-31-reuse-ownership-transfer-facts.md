# Reuse ownership transfer facts

Date: 2026-10-31. Issue: #4412. PR: #13897.

Ownership inference computed the same `lent_out` facts for two summaries.
It now computes that vector once per row update.
Both the escape summary and the lend summary use the same vector.

Some parameter modes cannot change for either possible escape fact.
The pass now checks that condition once per produced function.
It omits escape propagation only when all parameter modes remain constant.
It still computes the lend summary.
Invalid analyses retain their conservative results.

## Fixed comparison

Both stage-2 compilers use one stage-1 producer:
`1a9e3d8e2d6fd9f2572a7d6f561a21ddd0085e5baf479b7f541e429c66021a87`.

The baseline is the published source at `5ac4d35`.
All 1,243 compiler and stdlib input blobs match that remote head.
The candidate changes only `ownership.fern` and `semsource.fern`.
The checker source and stdlib remain fixed.
The RC certificate memo remains disabled.

| Metric | Published baseline | Candidate |
| --- | ---: | ---: |
| Callgrind instructions | 38,595,525,990 | 38,579,598,238 |

The decrease is 15,927,752 instructions, or 0.0413 percent.
The checker assembly is byte-identical on x86-64, ARM64, and WebAssembly.
This small compiler-cost decrease does not establish a native latency improvement.

The fixed checker source SHA-256 is:
`5ace3eadd42ce301ec112e6d448ce4420c30e25a08895ba320be41f37aa8fe68`.

The baseline raw profile SHA-256 is:
`dc17538a6f2f1d8944c8cba68ac3165c4ccbec1ed5d2a30ed56b3daee0346263`.

The candidate raw profile SHA-256 is:
`484f2323170b960893d46c3da9489bb4429543235fba6dadfd8c20df0078e123`.

The profile summaries, totals, and collected instruction counts agree.
The raw profiles, exact source inputs, output modules, JSON records, and logs remain preserved.

## Correctness

The lifetime suites pass 179 results on stage 2 and 179 on the stable compiler.
The stable inliner suite passes 61 results.
The ownership suite passes 31 results across all three targets.
Stage 3 and stage 4 are byte-identical.

Two additional controls retain resource owners across unused record and tuple constructions.
Both compiler generations pass all 15 results of the focused suite.
The earlier ownership-fact experiment fails both native controls.
That experiment remains unpublished.

The separate array control fails on both the original baseline and the current published compiler.
Its raw programs and logs remain preserved.
The server allocation gates and full issue acceptance remain incomplete.
The PR remains a draft.
