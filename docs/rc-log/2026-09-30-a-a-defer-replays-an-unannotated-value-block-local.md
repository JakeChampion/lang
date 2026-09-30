# 2026-09-30 — a defer replays an unannotated value-block local (#10496)

Typed path (`semsource.fern`: `escaping_bindings`, `list_var_uses`,
`bind_var`); AST lowering (`irlower.fern`: `vb_arms_defer`).

The defer desugar lifts a binding a deferred action names only when it has an
annotation to zero it at, so `var q = [1, 2, 3]` inside a value block stayed
put and the function-exit replay named a symbol the typed path never bound.
The typed path refused `main`. The AST fallback then hung the compiler (the
issue's program) or SIGSEGVed, because `vb_arms_defer` saw only a top-level
defer and moved the tail local out from under the replay.

A `var` used outside the statements that follow it now escapes as a pattern
binding already did: probed for its type, prebound at zero, overwritten by its
declaration. `vb_arms_defer` uses the nested `armed_defer_flags` walk.

## Witness

`vblock_defer_unlifted_local` in `TestSelfHostVblockClosureRelease*`, both
lowerings, on x86-64, arm64 and wasm: 101 as the interpreter, balanced census.

## Trap

The first version handed the generic `astwalk.fold_stmt_nodes` an untyped
`[]` accumulator, which the self-host checker cannot type:
`TestSelfHostChecksItsOwnSources` went red, and the `-check` fallback hint
blamed `embed__load` (#10752). The scan is now one `uses_in` fold per
statement list with a `util.NameIndex` lookup per use, linear in the list.

## Cost

The self-host compiling `examples/self_host/fern.fern` on x86-64, two runs
each on this 4-core container: main 156.1 s and 160.8 s, this change 160.1 s
and 157.5 s. The difference is within the noise.
