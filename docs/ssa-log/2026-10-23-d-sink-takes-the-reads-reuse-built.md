# 2026-10-23 — the sinking takes the reads reuse built

`semsink.sink` in the semantic lowering. Refs #8171.

## The shape

`semsink.sink` runs `reuse` and then `sink_rereads` on each function.
Each of them built a `Reads` with `reads_of`: the projection tree, the
dominator tree and the liveness of the graph. The self-compile made
1,915 calls from `reuse` and 1,814 from `sink_rereads`, about 1.1 G Ir
in all, most of it in `ssalive.compute`.

## What changed

`reuse` gives `sink_rereads` the `Reads` it built when it did not change
the graph. Only the `at` field of a `Reads` depends on the caller, so
`sink_rereads` replaces that field and builds nothing else. When `reuse`
replaces a read, the graph changes, and `sink_rereads` builds its own
`Reads` as before.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is one frozen copy of main at ddc5b11.

| subject | #13036 at 00aa037 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 77.453 G | 77.236 G (−0.28%) |

The whole-compiler binary is byte-identical to the base compiler's.
