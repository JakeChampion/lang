# 2026-10-04 — the x86 relax settle stops searching

`x86_native`, x86-64. Follows slice 3 of #11452, refs #8171.

## The shape

After the relaxation went in place, `x86_relax_settle` was the largest piece
of the x86 assembler left, at 301 M Ir compiling `checker.fern`. Four things
in it were overhead rather than work:

- **`x86_relax_layout` rebuilt its struct up to three times per event.** It
  now appends to local arrays and builds the struct once.
- **`x86_relax_targets` binary-searched for each branch's target**
  (`x86_events_before`). A rel8 target is a few events from its branch, so
  the search now steps from the branch's own event (`x86_events_near`), as
  `x86_relax_apply` already does for labels.
- **A label bound to a pad's far edge found the pad by a linear scan of the
  events** (`x86_index_of`). The layout now records each branch's and each
  pad's event (`br_ev`, `pad_ev`), which `x86_relax_apply` used to rebuild for
  itself.
- **Each settle pass copied the last pass's prefix sums** (`x86_ints_copy`).
  A pass reads the old sums and rewrites the new ones from the front, reading
  only entries it has already written, so the two arrays now swap instead.

`x86_index_of` and `x86_ints_copy` are deleted.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, against the #11514 head (2ba95c19). Both compilers build
`checker.fern` and `fern.fern` to byte-identical binaries, with and without
`-g`:

| | #11514 | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.184 G | 20.098 G (−0.43%) |
| `x86_gas_assemble_words`, inclusive | 1,072 M | 986 M |
| `x86_relax_settle`, inclusive | 301 M | 218 M |
| `x86_relax_targets`, inclusive | 92.8 M | 44.1 M |
| `x86_relax_layout`, inclusive | 48.0 M | 25.0 M |
| `x86_ints_copy` | 12.7 M | — |

The #11514 total reads 20.184 G here against 20.145 G in its own entry
because that branch was rebased onto a newer main between the two
measurements.

## What is left

`x86_relax_pass` is now most of the settle, at 144 M. Slice 4 of #11452 takes
the record lines out of `GasLine`.
