# 2026-10-02 — view erasure leaves a body that spells no view alone

`parser.erase_view_body`, `parser.spells_view`. Refs #8171. No emitted
byte changes: the stage0-built compiler before and after emits the fixed
older tree (`compiler/fern.fern` at 1ae9cad, against that tree's
stdlib) and that tree's `checker.fern` byte for byte, and the
`selfhost-emit-hashes` sweep is 1,965 rows per compiler with 0 differing,
against the previous entry's tree.

## What the profile named

`parser.erase_view_body` rebuilt every node of every function body to
rewrite the type spellings on it, `str` to `string` and `[T]` to `T[]`,
whether or not the body held one: 31.6 k bodies, 2.78 G, almost all of it
`astwalk.map_stmts` and `splice_stmts` constructing and releasing nodes
whose spellings came out unchanged.

## What changed

`erase_view_body` folds the body first, reading exactly the spellings
`erase_view_expr` and `erase_view_stmt` rewrite, and asks `spells_view` of
each: a `[` that opens a view, or the identifier `str` on its own. A body
that spells neither is handed back as it is; one that does is rewritten as
before.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 builds from each tree, emitting the fixed older tree to x86-64
asm text. Both rows are built from the previous entry's tree and this change
on top.

| | before | this change |
|---|--:|--:|
| total Ir | 153.09 G | 150.71 G (−1.6%) |
| `parser.erase_view_body`, inclusive | 2.78 G | 0.75 G |
| `parser.erase_view_module`, inclusive | 2.90 G | 0.87 G |

## Witnessed

The checker, planner, semantic, closure, method and lift set (the three
name lists of 2026-10-02-q, -r and -s together; 621 tests, green),
`make check-sources`, the lint ratchet, both emit identities and the
sweep.

## Next

`checker.type_from_spelling` parses and resolves 1.95 M spellings, 2.36 G,
most of them repeats within a module. `semrecords.verify` runs on every
`ssasem.analyze`, 2.07 G, finding each field's record by hash;
`semrecords.find_union` compares the type of every enum sharing a name,
4.1 M `equal` calls, most of them `Option` and `Result` instantiations.
`util.hash_bucket` is 4.7 G of its own instructions: the emitted loop
bounds-checks every byte load and sign-extends after every operation, 62
instructions per four bytes, which is the compiler's to fix in the IR.
