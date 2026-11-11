# Main integration and checker cliff count

Status: [record] measured and recorded on 2026-11-11.

Main `38399ecf5c363e3ef9a2981fa63cef01ff5401cb` reverted two earlier compiler changes.
The owner merged #15234 after its complete exact-head CI succeeded.
The agent did not merge that PR.
Its source tree matches the measured PR head `ca80bd07aba3edc5b60f971ceb76ac3925cdb292` exactly.

## Source integration

The integration preserves both main rollbacks.
It retains the base, local, and main spec files as separate records.
The live spec rows match main.
The removed fixture leaves the live census, with its original measurement and dated history intact.

Owner commit `e3bd8452c16add0ac0be07a553761dea1ba10567` depended on the earlier capability-name change.
The integration reverses its exact three-file delta after main removes that dependency.
The original owner commit remains an ancestor.
Compiler, standard library, checker, syntax, and conformance sources match main exactly.
The integration does not restore the reverted features.

The retained IR harness validates the production compiler refusal for every current refusal fixture.
Accepted programs retain the original callee-resolution and stack-coverage assertions.
The corpus floors and test budgets remain unchanged.
Main now contains 1,334 eligible fixtures, with `diag_e068` as its one refusal fixture.
The upstream rollbacks removed the other three fixtures.

## Exact reverse baseline move

[CI job 130320191632](https://github.com/JakeChampion/lang/actions/runs/42958185608/job/130320191632) measures the identical source tree.
The complete [report artifact](https://github.com/JakeChampion/lang/actions/runs/42958185608/artifacts/13830651344) supplies both metrics.
Its ZIP SHA256 is `53f22f15015fd92b9217eae85e543ff298b66fc35ef5c235a652968f5f4e0e58`.

| Metric | Previous local baseline | Measured value | Final baseline |
| --- | ---: | ---: | ---: |
| Checker cliff count | 378075 | 373796 | 373796 |
| Checker cliff bytes | 39116232 | 39058864 | 39116232 |

The count decrease exceeds the unchanged 1% tolerance.
The byte result remains within that tolerance, so its baseline stays unchanged.
Every other numeric row, tolerance, and dated history stays unchanged.
The earlier [378075 record](../compiler-checker-cliff-ci-2026-11-11/README.md) retains its original source and publication pin.

Strict comparison of the complete report fails with the previous full baseline.
It passes with the updated full baseline.
Neither comparison omits a metric.
The comparator and protocol sources match the earlier 22 canonical controls.
The record verifies their 37 original data hashes without another execution.

The counter protocol disables ownership certification.
The reports omit the measured compiler executable hash.
These source-specific counters establish no normal CPU time, leak, or external rank claim.

## Local controls

The archive includes the current source check and ndarray tests.
It also includes the refusal-classifier controls, source-index tests, format check, vet, and complete CI self-test.
Their result file records every command and exit status.
All selected test results pass with zero skips.
Separate native compiler and complete corpus controls remain local to this integration.
Their status appears in the PR description.

## Verification

Run from the repository root at this publication:

```sh
python3 docs/benchmarks/compiler-main-rollback-ci-2026-11-11/verify.py --commit HEAD
```

The verifier checks all data hashes, gzip identities, source trees, protocol sources, baselines, and comparator controls.
After another source change, pin this command to the original publication commit.
The original counter verifier remains unchanged.
