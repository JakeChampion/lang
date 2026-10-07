# 2026-10-07 — the schema walk starts from what names a schema

`semsource.complete` and `semsource.schemas`, which find every record and
enum a function's values reach. Refs #8171. No emitted byte changes.

## What changed

A `checker.fern` compile handed this walk 344 k types, about 175 for each of
1,969 functions: every SSA value's type, the result, and every contract
type. It walked them three times: `schema_of`, then `func_types_in` and
`dyn_types_in`. Three costs went:

- **Scalar roots.** A scalar or a string names no schema, function type or
  dyn type. Only the other types now enter the walk, in the same order. That
  leaves 288 k.
- **The enum lookup scanned the memo.** Records were found through a name
  index. Enums went through `find_enum`, a linear scan of every enum the
  module had met so far, 167 k times. The memo now keeps an enum name index
  too, and `find_named_enum` walks the name's chain as `find_named` does for
  records.
- **An empty field queue.** `named_by_schemas` ran after every root, though
  only a root that added a record or an enum gives it fields to queue. It is
  now skipped otherwise.

## Measured

`checker.fern` (at 2e084b79) built for x86-64-linux under callgrind by
production compilers, no `-g`, each side's stage 3 built by its own stage 2.
The baseline is main at f009e7a36 with the #11799 fix (e0ba0b691). Both stage
3s rebuild themselves byte for byte. The two compile `checker.fern` for
x86-64, arm64 and wasm, and `fern.fern` for x86-64, to the same bytes.

| | before | this change |
|---|--:|--:|
| total Ir | 15.433 G | 15.400 G (−0.22%) |
| stage 3 size | 10,954,584 | 10,955,160 |

The scalar filter alone measured 15.412 G.
