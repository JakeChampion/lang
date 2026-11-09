# IVF input CI performance control, 2026-11-09

PR #14917 adds bounded IVF packet input. It changes no compiler or byte-count source.
CI run `42662988289` stops after the x86-64 performance gate reports stale string-count rows.
The failed job is `129321185181`.

The merged byte-count kernel from PR #14910 changes these counts.
Its [measurement record](../../coreutils-log/2026-11-09-count-batch.md) describes the kernel and its performance limits.
This repair records the measured values. It changes no tolerance, workflow, benchmark, or gate code.

## Parent and candidate control

The control uses parent source `97af82fcbef87363ce78d53cf43bba069b437419` without the IVF change.
The candidate uses source `cbed65e06b5e23717bdb730912f76e5dbfca7311`.
Both use self-host compiler SHA256 `cdea484792b093f84794a162cf124a4e72cff18203a8567387d47f8fa2540b49` from the pinned stage0 build.
Each shipped CLI embeds its own stdlib sources.

Both compile `bench/string_count_byte.fern` for `x86-64-linux`.
The static count follows `scripts/perf-bench`: each assembly line with initial whitespace counts once.
Valgrind 3.24.0 Callgrind supplies the retired instruction count.
Each binary returns checksum exit status 40.

| Metric | Prior baseline | Parent | IVF candidate |
| --- | ---: | ---: | ---: |
| Static assembly lines | 1271 | 1321 | 1321 |
| Retired instructions | 68053368 | 41173368 | 41173368 |
| Self-host x86-64 emitted lines | 1271 | 1321 | 1321 |

Parent and candidate assembly and executable hashes match exactly.
The CI measurements match these local counts.
The [machine-readable record](control.json) retains source identities, hashes, checksum results, and counts.

The new instruction-count ceiling records the improvement in the merged kernel.
The static-size row records its size cost. The tolerance remains 1%.
This comparison isolates an inherited CI baseline mismatch.
It does not measure IVF-reader throughput or codec performance.

## Repair validation

The complete native performance report passes the strict baseline check after this repair.
The complete self-host emitted-size report also passes its strict check.
The comparator's 22 self-tests pass. No test or tolerance changes enter this repair.
