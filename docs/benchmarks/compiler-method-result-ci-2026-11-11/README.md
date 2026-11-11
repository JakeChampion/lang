# Method-result fixture and current-main costs

This record covers the method-result fixture repair in PR #15215.
The old fixture expects E051 for an immutable string result.
The checkers permit an independent copy of that result.

The repair keeps the resource refusal under the original case name.
A separate string case requires acceptance with its borrowed source still live.
All 15 original case names and expected codes remain.
The complete suite reports 17 test and subtest passes with zero skips.
Go and self-hosted x86-64 diagnostics agree on codes, locations, and messages.

The reproduction uses main `29b1234d0699d6b42d7d85bbcd82dbfb92c6dce2`.
Its old fixture fails with no Go diagnostic.
The repair validation uses `8440203b0ce52b7d1fbe07a9e23a1c9eb0307b03`.
Compiler code and proof rules are unchanged.

## Current-main baseline bank

Main [CI 42916398671](https://github.com/JakeChampion/lang/actions/runs/42916398671) supplies these four exact rows.
The measurement date is 2026-11-10.
The bank date is 2026-11-11.
The completed [x86-64 perf job](https://github.com/JakeChampion/lang/actions/runs/42916398671/job/130211808249) reports each higher cost.

| Row | Prior baseline | Measured main |
| --- | ---: | ---: |
| `checker.allocs` | 39,556,378 | 40,440,085 |
| `checker.frees` | 39,556,378 | 40,440,085 |
| `checker.cliff_bytes` | 38,416,000 | 39,116,232 |
| `checker.cliff_count` | 369,099 | 373,694 |

The source is the same main revision as the fixture reproduction.
These are inherited main costs.
The fixture repair changes no compiler or standard-library code.
All other baseline rows and every tolerance remain unchanged.
The default tolerance remains 1%.

The archive preserves three complete decoded CI logs.
They cover x86-64 perf, ARM64 perf, and all 14 driver-size rows.
Only the four exact corrective rows above change their baselines.
The other reports remain within their thresholds.
Both upward and downward findings retain the comparator's normal behavior.

## Protocol and controls

The unchanged CI scripts measure the compiler on its own checker source.
Both counter protocols already set `FERN_RC_CERTIFY=0` for the measured IR conversion workload.
The allocation protocol builds a separate `FERN_LEAKCHECK=1` compiler.
These flags remain unchanged.
The source identities include the exact scripts, workflow, and stage0 lock.

Allocation and free totals agree.
The decoded reports do not include live bytes or the measured compiler's executable hash.
The records do not measure leaks or elapsed time for a normal compiler.
They establish no external rank.
The complete CI run remains subject to its own verdict.

Strict replay rejects both old baselines with exit one.
The updated baselines match the same exact CI values with exit zero.
The unchanged canonical comparator self-test passes all 22 cases.
A private preload records its child outcomes and preserves scratch inputs.
It changes no comparator argument, assertion, or result.

The `.report` files derive the exact corrective lines from the decoded CI log.
They are separate parsed views, not downloaded artifact reports.
The full log bytes and lossless gzip copies remain distinct.
The negative-control archive retains every captured input, output, and child result.

## Verification

Run this command on the publication checkout:

```sh
python3 docs/benchmarks/compiler-method-result-ci-2026-11-11/verify.py --commit 7ed25d7e1c1c94d73cae445d61852197c4c4e6aa
```

The verifier checks exact data, product files, source identities, and gzip contents.
Later compiler changes or baseline banks need their own qualification.
The original publication remains the source of this record.

## Main integration on 2026-11-11

The branch retains main `3616c8cef7b4bbb518156247bc62ecd86df5ca74`.
That commit contains the three upstream ULC recurrence files from PR #15211.
Its tree matches the PR head `0c5f13cf7b29e5795b2b18b2d5af59e7eb5d597a`.
[CI run 42917525358](https://github.com/JakeChampion/lang/actions/runs/42917525358) reports success for that head.
All 68 jobs are complete, with 28 successful jobs and 40 conditional skips.
The skips establish no test pass.

Compiler, standard library, checker, syntax, and stage0 sources remain unchanged from main `29b1234d06`.
The checker benchmark scripts and inputs are also unchanged.
All 37 data files and twelve gzip identities retain their original bytes.
The fixture and counter controls keep their original source scope.
