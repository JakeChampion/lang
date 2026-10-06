# 2026-10-06 — the table merge looks a record up by name

`seminline.with_tables`, the merge of a spliced callee's record and enum
tables into the caller's. Refs #8171. No emitted byte changes: the compiler
before and after builds `checker.fern` for x86-64, arm64 and wasm, and
`fern.fern` for x86-64, byte for byte.

## What changed

Each splice copies the callee's records into the caller's table, skipping
the ones already there. The skip was `has_record`, a scan of the caller's
whole list with `semtypes.equal` on every entry, for every callee record,
on every splice: the type comparison ran a million times on a
`checker.fern` compile, the single largest caller of `equal` by a factor
of four. The table has carried a name index since the records got one
(`semrecords.Records.names`), and `semrecords.find` walks the name's chain
and compares shapes only there, so the merge now asks `find`. A callee's
table holds each schema once, so the records it adds need no check against
each other; they are appended in one pass and the index rebuilt once, as
before. Enums go through `find_enum`, which compares names before shapes.
`has_record` and `has_enum` are deleted.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler from main at 6c40a309.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 17.200 G | 17.093 G (−0.62%) |
| `semtypes.equal`, self | 144.9 M | 73.7 M |
| `semtypes.named_args_equal`, self | 42.8 M | 23.8 M |
| `seminline.splice_calls`, self (the merge loops inline into it) | 72.6 M | 59.5 M |
| `semtypes.is_never`, self | 8.5 M | 2.5 M |
| `semrecords.find`, self | 8.4 M | 9.9 M |

Nothing else moves by more than two megainstructions; the hash and chain
walk the lookups now pay come to about 2 M.

## What is left

The same merge compares environment and dyn rows pairwise with two
`equal`s each (`with_rows`), and `ssasem.find_contract_in` scans the
caller's contracts by name, 27 M on this compile; neither list has an
index. The enum table is a list with no index either: `find_enum` is
15.6 M of name comparisons. The rest of `equal`'s 74 M is the record
lookups themselves (`semrecords.find` and `find_named`), `ssasem`'s
verifier errors and `semsource.widen`.
