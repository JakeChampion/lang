# The builtin folds skip a body that names no builtin

`embed.fold_assets` replaces each call of `__fern_assets` and its three forms with the embedded content.
`constfold.fold_target_name` replaces each call of `target_os` and `target_arch` with the name of the target.
Each fold rebuilt every function body through the map walk of `astwalk`.

Each fold changes only a call whose callee is an identifier with one of its names.
Each fold now tests a body with `astwalk.names_any` first, and a body that names none keeps its statements.
`fold_assets` adds an error only for a call that it changes, so the errors do not change either.

`TestSelfHostTargetNameFoldsOnTheModloadDriverX86_64` now also calls `target_arch` inside a lambda.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 29,633,314,520 |
| this change | 29,603,013,547 (−0.10%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
