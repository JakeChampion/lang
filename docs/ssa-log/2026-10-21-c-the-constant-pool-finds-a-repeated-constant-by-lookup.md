# 2026-10-21 — the constant pool finds a repeated constant by lookup

Both native backends. `asmcore.add_const_agg`, `EmitState.const_agg_ix`.
Refs #8171.

## The shape

A static aggregate constant goes into the unit's pool once, so
`add_const_agg` compared the packed text of each new one against every
entry already there. The packed texts share long shape-label prefixes, so
a compare ran deep before it decided, and the pool grows with the unit: on
the whole compiler's self-compile the scan was 287 M instructions, 24 times
the `checker.fern` figure for a subject five times smaller.

## What changed

`EmitState` keeps a `util.NameIndex` over the pool beside it, grown with
each entry, and `add_const_agg` asks it: one hash of the packed text and at
most one compare.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the call_ret_type slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 76.770 G | 76.707 G (−0.08%) |

Less than the scan's self cost: the hash reads every byte of the packed
text, where the scan's compares stopped at the first byte past the shared
prefix. The whole-compiler binary is byte-identical to the base compiler's.
