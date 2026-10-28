# 2026-10-28 — the view memo lives on the union table

`checker.holds_view_aggregate`. Refs #8171.

## The shape

`holds_view_aggregate` asks if a type can carry a view. The answer walks
every struct and union that the type reaches. A memo kept the answers
for complete root queries, but the memo was a field of `Scope`.
`build_func_scope` makes a new `Scope` for each function, so each
function started with an empty memo. The self-compile made 36,342 walks,
at about 62 K Ir each.

## What changed

The memo is now a field of `UnionTable`. `module_env` makes one union
table for each module, and every function scope of the module shares
it. The walk reads only the struct and union tables of the scope. So
each function of the module gets the same answer for one type.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e. The base is main at b0241b2f3,
with #13782.

| subject | main at b0241b2f3 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 110.209 G | 108.049 G (−1.96%) |

The whole-compiler binary is byte-identical to the base compiler's.
