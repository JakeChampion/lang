# 2026-10-29 — the settle pass finds struct fields by an index

`parser.settle_module` and `parser.settle_field_type`. Refs #8171.

## The shape

The settle pass gives each value the type that its place expects. For
each field value of a struct literal, `settle_field_type` searched the
list of struct declarations of the module from the start. The merged
module of the compiler holds hundreds of structs, and the pass runs on
it once and on each module once.

## What changed

`settle_module` now builds a `SettleStructs` once for each module. It
holds the struct declarations and a `util.NameIndex` of their names.
`settle_field_type` finds the struct through the index. The index
gives the first declaration with the name, as the search did.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 3d1492c39. The base is the
associated-constant slice of the same pull request.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 102.133 G | 101.956 G (−0.17%) |

The whole-compiler binary is byte-identical to the base compiler's.
