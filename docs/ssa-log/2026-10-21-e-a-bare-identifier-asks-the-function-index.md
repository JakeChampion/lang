# 2026-10-21 — a bare identifier asks the function index

`asmcore.infer_expr_type`, the pre-codegen gate's typing of an identifier.
Refs #8171.

## The shape

An identifier that names no local may name a zero-argument function (a
`const`), and the gate found it by scanning every function declaration in
the program for a receiver-less, parameter-less one of that name. The scan
runs per identifier the gate types, so on the whole compiler's self-compile
it was 246 M instructions of the function's 230 M self cost (the rest of the
function is cheap): ten times the `checker.fern` figure for a subject five
times smaller. The previous entry's guess that `local_type_of` carried that
cost was wrong; the locals index it measured could not have helped.

## What changed

`EmitState.funcs_ix` already indexes the declarations by name, in
declaration order, as `top_level_fn` reads it. The walk is a chain walk
with the same test, so the first match is the same declaration.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (#12893's head) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 76.369 G | 76.252 G (−0.15%) |

The whole-compiler binary is byte-identical to the base compiler's.

## Measured and not landed: `fold_const_binaries` in one pass

`ir.fold_const_binaries` (644 M on the same compile, eight times the
`checker.fern` figure) restarts its scan from the front after every fold.
A single pass keyed on the arriving op, with the kept list built as it
goes, measured 76.369 G → 76.309 G (−0.08%) on the whole compiler and
15.978 G → 15.983 G (+0.03%) on `checker.fern`, both binaries identical:
the restarts are not where the cost is, and a rewrite of the pass is not
worth that much. Dropped.
