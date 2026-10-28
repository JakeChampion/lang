# 2026-10-28 — each emission unit reuses the kind table

`asmcore.unit_leaves` and `irverifygate.gate`. Refs #8171.

## The shape

`EmitState` builds the operation kind table one time and keeps it in
`kinds`. Each emission unit then built the table two more times:
`unit_leaves` built one, and the IR verify gate built one. The whole
compiler has about 150 units, one for each runtime module. So the table
was built about 300 times, for about 0.4% of the self-compile. For a
small program, the builds were about 10% of the compile.

## What changed

`unit_leaves` and `gate_unreported` take the table as an argument. The
x86-64 and arm64 emitters give them `EmitState.kinds`. The Wasm emitter
builds the table one time for its one gate. `Gate.pops` is deleted,
because `Gate.kinds.pops` holds the same pop counts.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 3d1492c39. The base is the same tree with
the replay-row and counter-check slices of the same pull request.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 104.273 G | 103.825 G (−0.43%) |
| a five-line program | 143.6 M | 128.8 M (−10.3%) |

The whole-compiler binary is byte-identical to the base compiler's.
