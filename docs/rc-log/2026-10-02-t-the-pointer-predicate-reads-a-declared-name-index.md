# 2026-10-02 — the pointer predicate reads a declared-name index

`checker.OwnFuncs`, `checker.ow_type_is_pointer`. Refs #8171. No emitted
byte changes: the stage0-built compiler before and after emits the fixed
older tree (`examples/self_host/fern.fern` at 1ae9cad, against that tree's
stdlib) and that tree's `checker.fern` byte for byte, and the
`selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0 differing,
against the previous entry's tree.

## What the profile named

`checker.ow_type_is_pointer` decided a spelling's shape and then scanned
the module's structs, two string compares each, and its enums and aliases
after: 132 k calls, 1.01 G, from the own-diagnostics passes asking it of
every parameter and result type of every function.

## What changed

`OwnFuncs` carries two name indexes beside its rows: `structs`, every
struct name and every enum that owns one, and `decls`, the enum and alias
names only the module form counts. `ow_type_is_pointer_name` and
`ow_type_is_pointer` take it and read the indexes; every caller already
had it in hand.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 builds from each tree, emitting the fixed older tree to x86-64
asm text. Both rows are built from the previous entry's tree and this change
on top.

| | before | this change |
|---|--:|--:|
| `checker.ow_type_is_pointer`, inclusive | 1.01 G | 0.15 G |

The round as measured also took the borrow registry owned through
`ssarc.caller_sigs` (163.64 G to 159.02 G in all, `caller_sigs` 3.91 G to
0.15 G). #11109 landed that same change first and records it in
2026-10-02-t-the-borrow-registry-is-stored-to-in-place, so this entry
carries only the predicate.

## Witnessed

The checker, planner, semantic, closure, method and lift set (the three
name lists of 2026-10-02-q, -r and -s together; 621 tests, green),
`make check-sources`, the lint ratchet, both emit identities and the
sweep.

## Next

`util.hash_bucket`'s emitted loop and `type_from_spelling`'s repeated
parses, which later entries take up.
