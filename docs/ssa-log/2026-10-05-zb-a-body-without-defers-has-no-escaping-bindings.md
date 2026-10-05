# 2026-10-05 — a body without defers has no escaping bindings

Self-host typed lowering, every target. Refs #8171.

## The shape

`semsource.escaping_bindings` finds the bindings used outside the scope that
binds them: a match arm's pattern bindings, a `for` variable, a `let` used
outside the statements after it. It runs once per function, from
`semsource.initial`, and walks the body five times: `list_var_uses`, three
`fold_stmt_nodes` passes, and `uses_in`. Several of those re-walk nested
blocks from each enclosing one.

Its own comment says why such a use can exist at all. Lexical resolution gives
every binding a symbol of its own, so the only source is the defer desugar's
replay of an action into another exit. The desugar gives every scope it
lowers, the function's and each lambda's, a `__dfa_tryall` flag
(`parser.lower_defers_body`).

`escaping_bindings` now asks first whether any scope of the body declares that
flag (`defer_lowered`, one walk), and returns no bindings when none does.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at ec644225 against this branch. The two compilers build
`checker.fern` for x86-64 and arm64, and `fern.fern`, to byte-identical
binaries. `scripts/selfhost-emit-hashes` matches on all 2,001 rows, defer
fixtures included:

| | main | defer-lowered first |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 18.850 G | 18.693 G (−0.84%) |
| `escaping_bindings`, inclusive | 171 M | 13.8 M |

None of the 1,874 bodies compiling `checker.fern` uses `defer`, so the
remaining cost is `defer_lowered`'s walk.
