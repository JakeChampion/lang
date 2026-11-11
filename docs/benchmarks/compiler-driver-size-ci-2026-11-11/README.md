# Driver size records from CI

Measurement and bank date: 2026-11-11.
The source publication is `75dbb650647f3e97300c6dc2c9f3f734892f9489`.
Its compiler and standard library match main `4e38d84c7fe34200e300ad55c05ed92d8b67c6b7`.
[CI job 130515952623](https://github.com/JakeChampion/lang/actions/runs/43018200796/job/130515952623) reports all fourteen driver sizes.
The concurrent commit `4b3fa34c0495aa86f660e2503f1d43bbb6f565d0` records four corrective values.

| Driver | Previous bytes | Measured bytes |
| --- | ---: | ---: |
| `checker_codes_run.fern` | 3663328 | 3851760 |
| `checker_modload_run.fern` | 3913968 | 4147040 |
| `asm_run.fern` | 9577776 | 10091776 |
| `asm_ir_run.fern` | 10449984 | 11028400 |

All other numeric rows, dated history, and the 5% tolerance stay unchanged.
The complete report contains exact integer rows from the decoded CI log.
It is not a downloaded artifact report.
The tool UTF-8 hash, local raw hash, and decompressed gzip hash match.
The strict comparator rejects the previous baseline and accepts the updated baseline, with all fourteen rows present.
No compiler benchmark repeats for this replay.

The source records include the comparator, flag helper, stock-driver test, and A19 pin.
The complete CI self-test passes, with its 22 canonical perf-gate controls intact.
The feature census measures twenty arrow lambdas after main adds two.
The resource census measures 1339 fixtures, with twelve pinned leaks and 34 unpaired allocations in total.
The new `owned_field_match_data` row is zero under that RC trace protocol.
These results do not prove normal leak freedom.

The decoded size report does not supply hashes for the measured executables.
The measurements establish no compiler speedup, physical cause, or external rank.
All earlier archives retain their original source identities.
Do not add results from different source snapshots.

```sh
python3 docs/benchmarks/compiler-driver-size-ci-2026-11-11/verify.py --commit HEAD
```

The verifier checks the publication files, source trees, protocol files, exact report, and retained control results.
It executes no compiler workloads.
After a later material source integration, pin this command to this archive's original publication commit.
Retain the source checks and add an integration note.
