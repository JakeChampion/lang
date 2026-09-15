# Native x86 worker experiment

The x86 end-to-end job took 880 seconds in successful run `35007405837`,
including 806 seconds in its main test step. This experiment measures whether
the isolated worker runner already used on ARM64 helps this critical path.

The normal PR lane is unchanged. Manually dispatch `test-e2e-x86_64.yml` on
the experiment branch with `benchmark=pilot`, then `benchmark=full` only after
the pilot passes. The benchmark replaces the normal jobs for that dispatch.
It uses one native x86 runner, prebuilds the test binary, and compares the
production Go test command against two workers in before/after/after/before
order with a fixed total CPU budget. Full mode selects the
same `^TestX86_64` inventory as the production job. The pilot uses four small
parents and must complete all trials in under a minute, excluding setup/build.

The existing runner verifies all test starts and terminal outcomes. The
comparison also requires identical parent inventories and outcomes across
worker counts, including skips. Any failure, missing result or changed
coverage stops the experiment. Artifacts retain JSON events, worker summaries,
binary hash, revision, host identity, CPU budget and trial wall/child CPU times.
These timings exclude setup and the common prebuild. The baseline retains
Go test's launch overhead and warm build-cache lookup. Record job/step times
separately. Keeping the real baseline also detects changes caused by the worker
runner's launch environment, including inherited signal dispositions.

No speedup is established yet. Local ARM emulation is not accepted as native
x86 timing evidence. A successful experiment still needs a separate production
rollout and current-head CI validation.
