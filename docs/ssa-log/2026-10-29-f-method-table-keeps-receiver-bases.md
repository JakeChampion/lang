# 2026-10-29 — the method table keeps the base of each receiver

`checker.MethodTable`, `Scope.receives` and the method lookups.
Refs #8171.

## The shape

To find a method for a receiver type, the checker compares the receiver
spelling of each method with that name. When the spellings differ, it
parsed the receiver spelling with `parser.parse_type_ref` to compare
its base, such as `Map` for `Map[K, V]`. A plain spelling returns at
once, but a generic one builds a full type reference. The checker
parsed these spellings about 270000 times in one compile of the
compiler, for 3474 methods in 155 tables.

## What changed

`MethodTable.bases` holds the base of each receiver spelling. The
table parses each spelling once, when it is built. `Scope.receives`
now takes the index of the method, and `trait_providers`,
`trait_spelled_method` and `has_method` read the stored base.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 3d1492c39. The base is main at 9295e6125.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 100.897 G | 100.460 G (−0.43%) |

The whole-compiler binary is byte-identical to the base compiler's.
