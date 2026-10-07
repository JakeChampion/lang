# 2026-10-07 — the sole proof settles its links, not every instruction

`ssaunits.sole_boxes` and `ssaunits.fresh_rows`. Refs #8171. Builds on the
previous entry. No emitted byte changes.

## What changed

The proof itself had grown to 132 M on a `checker.fern` compile, inclusive,
over about 3,700 runs. Two costs dominated.

- Its fixpoint walked every instruction of the function each round, and
  for each call asked the fresh-row table again by name: 131 k lookups,
  17 M on their own. The proof now records, in one pass, the operand each
  operation passes its box on from and the links the fixpoint settles: a
  pass link from operand to result and a phi link between operand and phi.
  `settled` then iterates over those links only.
- `fresh_rows` proved every unnamed row again in every round. A row can
  only join a later round through a call to a row named in the round
  before, so each round now proves again only the rows that call one.
  Rows that do not return arrays are never tried.

## Measured

`checker.fern` (at 2e084b79) built for x86-64-linux under callgrind by
production compilers, no `-g`, each side's stage 3 built as before. The
baseline is the previous entry's branch at 650f6a9fc. Both stage 3s rebuild
themselves byte for byte, and the two compile `checker.fern` for x86-64,
arm64 and wasm, and `fern.fern` for x86-64, to the same bytes.

| | before | this change |
|---|--:|--:|
| total Ir | 15.292 G | 15.264 G (−0.19%) |
| stage 3 size | 10,768,496 | 10,773,792 |
