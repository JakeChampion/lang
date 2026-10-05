# 2026-10-05 — a module with no array map skips the scale rewrite

Self-host typed lowering, every target. Refs #8171.

## The shape

`ircore.scale_f64_maps_module` rewrites `xs.map(|x| x * K)` over an `f64[]`
into the `__scale_f64` kernel. It ran `astwalk.map_stmts` over every function
of the module, which rebuilds each statement and expression it passes, to find
calls named `__arrm_map__<elem>`.

Such a call names a function that monomorphisation cloned into the same
module. A module with no `__arrm_map__` function has no call to rewrite, and
`checker.fern` has none.

`has_array_map` checks the function names first. Without a match, the module
is returned as it came.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at ba445c24 against this branch. The two compilers build
`checker.fern` for x86-64, arm64 and wasm, and `fern.fern`, to byte-identical
binaries. `scripts/selfhost-emit-hashes` matches on all 2,001 rows:

| | main | name check first |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 17.987 G | 17.951 G (−0.20%) |
| `scale_f64_maps_module`, inclusive | 25.2 M | 0.05 M |
