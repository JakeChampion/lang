# 2026-10-12 — the analysis says what borrows a value

`ssasem.Anchored`, `anchored_of`. Refs #11910, the third slice: the reads
that borrow from a unit.

## The shape

The hold rule built the dependency chains read backwards, for each value
the reads anchored to it, in a private index (`ssaunits.anchored_to`), and
`needed_past_slot` asked the same thing a slower way: it looped over every
value of the function to find those whose chain reaches the box.

`ssasem.Analysis` now carries `anchored`, built with the chains, and the
unit analysis rebuilds it over the chains the takes and holds cut
(`units_analysis`). The hold rule reads it (`outliving`, `wanted`), and
`needed_past_slot` walks the values anchored to the box instead of every
value. The model `docs/RC-DESIGN.md` §1.3 states now has each of its parts
on the analysis: the holder (the walk's units), the reads that borrow from
it (`anchored`), what reads a value (`uses`), and where it is live (`flow`).

## Measured

`checker.fern`, `semsource.fern`, `ssaunits.fern` and `seminline.fern`, from
main, compile to byte-identical x86-64 assembly with the compiler built from
main and with this one. The compiler compiling `checker.fern` allocates
23,534,117 times against main's 23,529,673 (+0.02%), the index built for
every analysis.
