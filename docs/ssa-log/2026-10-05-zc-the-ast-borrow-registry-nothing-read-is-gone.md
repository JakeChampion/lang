# 2026-10-05 — the AST borrow registry nothing read is gone

Self-host compiler, every target. Refs #8171.

## The shape

`semlower.substitution_of` called `ircore.wp_fn_sigs` on every compile. It
built two whole-program registries from the module's AST:

- the borrowability rows, from `fnsigs.borrowable_params_interproc` widened
  by `param_counted_of`;
- the string-field verdict, from `strfld_reclaim_ok_types_of`.

The AST lowering read them. Since its retirement, nothing did:

- `ssarc.caller_sigs` overwrote each produced callee's rows from its verified
  contract, and the CLI produces every function;
- the four emit entries took the registry as a parameter and never read it;
- the only remaining reader was `fnsigs.wp_fact_rows`, which folds the rows
  into the per-module cache keys. There, only the contract rows describe
  anything an emit depends on.

Building `checker.fern` for x86-64, arm64 and wasm with the empty registry in
place of `wp_fn_sigs` gives byte-identical binaries.

## The change

- `substitution` starts `caller_sigs` from the empty registry.
  `substitution_of` erases only the struct declarations it builds its table
  from, rather than the whole module.
- `wp_fn_sigs` is gone, and so is everything that only it reached: 457 of
  the 463 functions in `fnsigs.fern` (16,504 lines to 116), 17 in
  `irtables.fern`, and `astwalk.count_ident_stmts`.
- `FnSigs` keeps only `borrowable_params`, and `wp_fact_rows` folds only
  those rows.
- The four emit entries, `Gated` and the modload `View` drop the registry
  parameter and field that nothing read.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at 63d5d685 against this branch. The two compilers build
`checker.fern` for x86-64, arm64 and wasm, and main's `fern.fern`, to
byte-identical binaries. `scripts/selfhost-emit-hashes` matches on all 2,001
rows:

| | main | registry gone |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 18.894 G | 18.204 G (−3.65%) |
| `semlower.target_substitution`, inclusive | 7.46 G | 6.78 G |
| `ircore.wp_fn_sigs`, inclusive | 643 M | — |
| `parser.erase_view_module`, inclusive | 123 M | 81 M |
