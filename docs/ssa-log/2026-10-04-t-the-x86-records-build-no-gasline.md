# 2026-10-04 — the x86 records build no GasLine

`x86_native`, x86-64. Slice 4 of #11452, refs #8171.

## The shape

`x86_gas_prepare` turned every line into a `GasLine`, a struct of thirteen
fields, five of them strings. Records became `GasLine`s too:

- a run of byte records (`x86_bytes_line`);
- a branch to a label id (`x86_jump_line`);
- a label defined by id, a CFI directive, and a named record.

Most of those fields were empty for a record. Each record line was still
built, linked (`x86_gas_link_labels` rebuilt any line it gave a label entry),
read in the round, and released.

The prepared program is now a `GasText`:

- **`ops`:** one entry per line, three i32s each: a `GP_*` kind and two
  operands. A record is an op of its own. A byte run holds its range of the
  words, a branch its jump kind and label id, a label definition its id, a
  CFI directive its words offset, and a named record its words offset and the
  index of its symbol.
- **`lines`:** the `GasLine`s, built for text lines only. A `GP_TEXT` op holds
  a line's index.
- **`named`:** the symbols the named records reach.
- **`nids`:** how many label ids the records define, counted as the ops are
  built rather than by a walk of the lines.

A repeated text line used to append a copy of its memoised `GasLine`. Its op
now holds the memoised line's index, so the line memo keeps indexes rather
than lines.

The round dispatches a record op before it reads any line. A branch record
goes straight to `x86_gas_jump_id`, which also makes the refusal of a label id
nothing defines, rather than through the arms of `x86_gas_emit_op`. A named
record looks its symbol up in the label table where the link step used to.

`GasLine` loses `lid` and `run_end`, which only records used.
`x86_bytes_line`, `x86_jump_line` and the record kinds `GK_BYTES`,
`GK_LABEL_ID`, `GK_CFI` and `GK_NAMED` are gone.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, against #11522 rebased on main at 14cb7167. Both compilers build
`checker.fern` and `fern.fern` to byte-identical binaries, with and without
`-g` and under `FERN_SANITIZE`. `scripts/selfhost-emit-hashes` matches on all
2,001 rows:

| | before | record ops |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.098 G | 19.867 G (−1.15%) |
| `x86_gas_assemble_words`, inclusive | 986 M | 755 M |
| `x86_gas_prepare`, inclusive | 190 M | 155 M |
| `x86_gas_link_labels`, inclusive | 128 M | 6 M |
| `GasLine` release and drop | 176 M | 8 M |
| `x86_bytes_line` and `x86_jump_line` | 38 M | — |

## What is left

`x86_gas_prepare` still spends 155 M, about 100 M of it in its own loop. That
loop steps over two marker bytes in the text for every record to find the
record's op. The emitter could hand the ops over directly, and the text
would then hold only the lines that are text.
