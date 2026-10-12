# 2026-10-12 — a struct looked up once, and walked once

`parser.inst_struct`, `checker.type_holds_view`. Refs #8171. No emitted byte
changes.

## What changed

- **`inst_struct` finds its declaration through a name index.** Monomorphising
  asks it which declaration an instantiated spelling (`Box[i32]`, or the clone
  `Box__i32`) names. It scanned every struct declaration and built a
  `Name__` prefix string for each generic one. On the whole compiler that was
  26 K calls costing 522 M Ir. `MFns` now carries a `NameIndex` over its
  structs. The exact name and each `Base__args` split of the spelling are
  looked up in it, and the earliest declaration wins, as it did in the scan.
- **E082's walk enters each struct once (#12161).** `type_holds_view` kept its
  `seen` list per path, so a struct reachable by several paths was walked once
  per path. That is exponential in the depth of a type graph that shares a
  struct. Forty structs that each hold two fields of the next made the checker
  run for longer than 30 seconds. The walk now threads one list through, as
  native `typeHoldsView` does with its map. On the compiler's own
  sources, 124 capture checks had cost 92 M Ir.

## Measured

The whole compiler (`fern.fern` to an x86-64 binary) under callgrind. Both
compilers compiled the same frozen copy of the tree, each on a warm memo of
its own.

| | Ir |
|---|--:|
| this branch's base (66f9820) | 73.460 G |
| both changes | 72.857 G (−0.82%) |

## Trap

A first A/B of this pair measured +0.79%: 1.08 G more in `ssaunits`, which
neither change touches. The base compiler had been built from an older tree,
one missing a `ssasem` change that had since landed on main. Build the base
from the commit the change sits on (`git archive HEAD`), not from whichever
compiler is lying around, and check that its symbol list matches the new
compiler's outside the functions you changed.
