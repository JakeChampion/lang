# Checker cliff baseline

Status: [record] measured and recorded on 2026-11-11.

[Main CI job 130273056431](https://github.com/JakeChampion/lang/actions/runs/42942829694/job/130273056431) measures source `2ca148c6757a95ba419a67751dd3598726f07c56`.
Its corrective report gives the exact count below.

| Metric | Previous baseline | Measured value | Tolerance |
| --- | ---: | ---: | ---: |
| `x86_64/checker.cliff_count` | 373694 | 378075 | 1% |

The baseline retains its dated history and every tolerance.
All other numeric rows stay unchanged.
Other displays use rounded values.
This record changes the exact corrective row.

The benchmark disables ownership certification for these counters.
The decoded log omits the measured compiler image hash.
The counters qualify this source-specific baseline row.

## Controls

The projected old row fails strict comparison.
The projected updated row passes.
Both full baselines reject the partial report because its copied-byte metric is absent.
The complete report remains a requirement for CI.

The [earlier comparator archive](../compiler-method-result-ci-2026-11-11/README.md) retains 22 canonical controls at commit `7ed25d7e1c1c94d73cae445d61852197c4c4e6aa`.
The comparator, flag helper, and self-test sources match those controls.
This record verifies all 37 original data hashes without another execution.
The four new replays check this row and the partial reports.

All three decoded CI logs retain their exact UTF8 bytes in gzip files.
The archive includes the executed validator and unchanged protocol sources.
The report file derives one exact corrective line from the log.
It is a partial view, not a downloaded complete report.

## Verification

Run from the repository root:

```sh
python3 docs/benchmarks/compiler-checker-cliff-ci-2026-11-11/verify.py --commit HEAD
```

The verifier checks committed data, raw gzip identities, protocol sources, the publication baseline, and the original comparator controls.
After a later source integration, retain this command with the original publication commit.
