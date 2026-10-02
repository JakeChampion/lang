# 2026-10-02 — branch relaxation settles the way GNU as does, pads included

`x86_native.x86_relax_settle`, `x86_relax_pass`, `x86_relax_seen`,
`X86Asm.pads`, `X86Asm.pad_labels`. Fixes #11001. Refs #8171.

## The case it got wrong

The self-host assembler pinned every out-of-range branch long at once. That
reaches the least fixpoint only while distances can only grow, and an
alignment pad in `.text` breaks that: an earlier branch's growth can be
absorbed by the pad, which brings a later branch back into range. For

```
A: 140 nops; jle A; jno L; 120 nops; .p2align 4; L: ret
```

GNU as and the native assembler emit `jno` short (`717c`); the self-host
assembler emitted it long.

## Change

`x86_relax_settle` now settles sizes as GNU as does, ported from the native
assembler's `relaxOnce` (`internal/native/x86_64/relax.go`), on the first
round's layout:

- passes over the branches and pads in offset order, each branch judged
  against the layout as it stands mid-pass;
- a target behind the branch at this pass's position, one ahead at the last
  pass's position plus the growth so far, except across a pad, which absorbs
  it (gas's regions);
- each pad re-measured at the offset the pass has reached;
- a label written directly after a pad stays on the pad's far edge, which a
  pad empty in the first round needs.

A round records what that takes: each `.text` pad's offset, width, alignment,
maximum skip and position among the branches (`X86Asm.pads`), and each label
that directly follows a pad (`X86Asm.pad_labels`).

Every text now takes the settle, so the round-by-round loop, its pass cap and
`X86Asm.text_aligned` are gone. The second round is checked against its own
layout; a mismatch is an `unknown` entry, so drivers fail rather than write
the bytes.

## Tests

`TestSelfHostX86GasRelaxation` gains the case above and one where a label
bound to a pad that is empty in the first round must stay on the pad's far
edge: the first jmp's growth widens the pad to five bytes and puts the label
out of the second jmp's reach. Both expectations are the native assembler's
bytes.

## Measured

Byte-identical against main: the `checker.fern` binary, the stage-2 compiler
and all 1,965 `selfhost-emit-hashes` rows; the compiler emits no alignment in
`.text`, so this changes only hand-written assembly.

| | main (9cc02c4) | this change |
|---|--:|--:|
| stage 2, total Ir | 35.38 G | 35.43 G (+0.13%) |

The cost is the settle's own bookkeeping, a pass over the branches and pads
judging each against a mid-pass layout rather than one test per branch; it is
what handling pads correctly takes.
