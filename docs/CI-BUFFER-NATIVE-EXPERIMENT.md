# Native instruction-buffer experiment

This branch is a measurement vehicle, not a production workflow change.
Dispatch `test-e2e-x86_64.yml` with `buffer-benchmark=pilot` and
`generation=gen1`. Only its benchmark job runs. After the pilot passes,
dispatch the same revision with `buffer-benchmark=full`. The same procedure
can measure `gen0` separately.

The baseline is fixed at `78a457c36beeb0b5d01d5a1ecd59b27e8d2c5217`.
One Go bootstrap builds both versions from their respective Fern sources.
Each Go-built compiler emits and links its self-built generation with the
normal eight-unit batch and existing memory reservation. Fixture setup is
outside the measured phase and remains visible as separate CI steps.

The measurement runs baseline, candidate, candidate, baseline sequentially
on one native Linux x86 runner. Both compile the baseline's identical input.
Each trial records child CPU time, wall time, peak RSS, exit status and exact
assembly hashes. Missing, extra or changed output fails the experiment.
The candidate then compiles its own source with the self-built generation;
those outputs must match its Go-built generation exactly.

The pilot uses one unit for both comparisons and must complete its measurement
and identity checks in under a minute. Full scale times the existing heavy
eight-unit window [8:16] and checks the candidate's complete compiler fixpoint.
Only the scale input changes. No memory reservation, test coverage or normal
production execution path changes in this experiment.

Artifacts retain build identities, unit inventories, environment information,
per-process measurements and verified output hashes. Large compiler binaries
and assembly files are excluded from upload. A successful artifact contains
`verified.json`; partial measurements alone do not establish success.
