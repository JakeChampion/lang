# 2026-10-28 — the bare flow keeps only retainable reads

`ssaunits.units_of`. Refs #8171.

## The shape

`units_of` solves two liveness flows. The `bare` flow has no
dependencies. The unit search asks it about one kind of value only: a
retainable read. `holds_off` and `outliving` both test
`retainable_read` before they ask. The solve still kept a slot for
every value of the function.

## What changed

`retainable_reads` marks each retainable read. `units_of` gives the
marks to `ssalive.compute_checked_kept` for the `bare` flow. No value
has a dependency here, so the liveness of a marked value does not
change. The `direct` flow is unchanged, because the set of values that
it is asked about grows during the search.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e. The base is main at 753408b4c
with the sink and grow slices of the same pull request.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 103.993 G | 103.782 G (−0.20%) |

The whole-compiler binary is byte-identical to the base compiler's.
