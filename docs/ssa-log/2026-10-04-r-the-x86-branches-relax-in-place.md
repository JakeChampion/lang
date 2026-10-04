# 2026-10-04 — the x86 branches relax in place

`x86_native`, x86-64. Slice 3 of #11452, refs #8171.

## The shape

The assembler relaxed branches in two rounds:

1. The first round laid every relaxable branch out short.
2. `x86_relax_settle` worked out from that layout which branches must be
   long.
3. A second round encoded the whole program again with those decisions. A
   replay cache (`EncCache`) copied the bytes of the lines no decision could
   change.
4. A check confirmed the second round asked for the same decisions it was
   given.

The second round cost 323 M Ir compiling `checker.fern`. That was measured by
dropping it from a build and comparing.

Now there is one round:

- **One round.** It lays the code down short and stops before resolving
  (`x86_take`).
- **Settle.** `x86_relax_settle` returns its settled layout: the first round's
  events and the positions, sizes and branch forms of the pass that settled
  them.
- **Rewrite.** When any branch must be long, `x86_relax_apply` rewrites the
  code to that layout. Each long branch takes its rel32 form, and each pad
  its settled width, filled as `x86_gas_align` fills one.
- **Move what is recorded at an offset.** Labels, fixups, the rip-tail
  offsets, `.loc` rows and CFI offsets all move with the code:
  - An offset moves past every event that starts before it.
  - A branch's own rel8 fixup moves with the branch, and becomes a rel32
    fixup past the long opcode.
  - A pad the first round laid out empty shares its offset with whatever
    follows it. So a label written right after a pad keeps its binding to
    the pad's far edge (`pad_labels`). Per pad, `pad_marks` counts the
    `.loc` rows, CFI rules, FDE starts and closed FDEs recorded before it, so
    the rows written after it move with it.
- **Resolve once.** `x86_patch` resolves the fixups a single time.

`EncCache`, `AsmRound`, `x86_replay_bytes`, `br_long`, `x86_relax_grow` and
the boolean helpers they needed are gone. A layout the settle got wrong would
now leave a rel8 out of range, which `x86_patch` refuses as it always has.

`TestSelfHostX86RelaxMovesRows` pins the empty-pad case: a `.loc` row and an
FDE start written before the `.p2align` stay in front of the pad, and a rule,
a row and the label written after it move to its far edge. Its first run
caught the FDE ends. An FDE's end is appended when it opens and set when it
closes, so the count of ends recorded so far has to leave out the open one.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at 07615458 against this branch. Both compilers build
`checker.fern` and `fern.fern` to byte-identical binaries, with and without
`-g`, and under `FERN_SANITIZE` and `FERN_RC_FREE_DEBUG`:

| | main | one round |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.397 G | 20.145 G (−1.24%) |
| `x86_gas_assemble_words`, inclusive | 1,325 M | 1,072 M |
| the rounds (`x86_gas_assemble_pass_prepared`) | 593 M | 211 M |
| `x86_relax_apply` | — | 100 M |
| resolve (`x86_take` and `x86_patch`) | 148 M | 73 M |

Labels are placed mostly in ascending offset order, so `x86_relax_apply`
looks each one up by stepping from the last label's event
(`x86_events_near`). A binary search per label cost 46 M of a first version's
128 M.

## What is left

`x86_relax_settle` still costs 301 M. Of that, `x86_relax_targets` takes a
binary search per branch (`x86_events_before`, 53 M), and `x86_relax_layout`
rebuilds its struct three times per event. Slice 4 of #11452 then takes the
record lines out of `GasLine`.
