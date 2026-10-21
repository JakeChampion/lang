# 2026-10-21 — the live type names are a name index

`treeshake.ts_live_type_names` and `ts_add_type_words`. Refs #8171.

## The shape

The tree shaker closes the set of type names a kept body or signature
mentions over the fields and payloads of those types, and kept the set as a
list: every word added and every owner asked about scanned it. The set grows
with the program, so each probe did; `index_of_str` under the shaker was 132 M
instructions on the whole compiler's self-compile.

## What changed

The set is a `util.NameIndex`, probed with `has` and grown with `added`; the
two callers that asked whether an owner is live ask it the same way.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the replace-all slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 74.585 G | 74.486 G (−0.13%) |

The whole-compiler binary is byte-identical to the base compiler's.
