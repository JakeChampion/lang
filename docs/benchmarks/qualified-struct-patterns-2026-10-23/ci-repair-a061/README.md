# Second CI repair

The CI run for `a061f713aa32d912da8ed135649585f6e52b9984` found failures
in nine self-host shards. The fixes below retain the qualified-pattern tests.

The shared repair from PR #13049 supplies `wasi:cli/stderr` in custom WIT
worlds and restores E061 rejection on raw loader paths. The CLI and
playground check paths use `parse_errors_before_check` because the checker
reports E061 with the block's void type. The source gate recognizes this
helper by its exact name.

The qualified-pattern corpus fixture also contains an imported named-field
variant. Raw module loaders did not resolve its field order after bundling.
`flatten.bundle` now runs the same field-resolution pass as the normal CLI.
The focused verifier run resolves all 26 direct calls and models all nine
bodies. It fails before the fix and passes after it.

The semantic-source golden failure also occurs on unchanged main
`8207edece5bcf7bb94bc5f4192024abc94c367d4`. The refreshed graph records the
void value in `Ok(_)` and renumbers the following values. The comparison
passes with golden regeneration disabled.

The old library test mixed assembly output with a default executable build.
Commit `768f0a0bd3` deliberately requires an executable entry point. The test
now checks E001 and no output file for default builds on all three targets.
It also verifies that assembly output still accepts the library.

The first focused run checks 61 test functions. All semantic tests pass.
One source gate initially rejected the new helper's name. The final corpus
run includes a separate check of that corrected source gate.

`focused-source.json` and `focused-input.patch.gz` identify the first run's
exact compiler input. `final-source.json` and `final-input.patch.gz` identify
the final compiler input. The only compiler difference between these
inputs is a shorter comment. The final patch also updates the source gate.

The full verifier run passes all 1,047 fixtures in 115.094 seconds.
It resolves 823,319 direct calls and models all 350,096 bodies.
The corrected source gate passes in that run.

The pinned bootstrap reaches identical stages 2 and 3 at 14,256,432 bytes.
Their SHA-256 is
`eaa7bac0ffabd7d2d61078b47a81f78fba2204833196950a5704dd050e9e694c`.
All eleven driver sizes pass the unchanged strict 5% gate.
Formatting passes for all 1,176 tracked source files in scope.
Vet passes for both changed Go test packages.
