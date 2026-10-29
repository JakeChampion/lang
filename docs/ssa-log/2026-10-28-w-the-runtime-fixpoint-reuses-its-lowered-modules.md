# 2026-10-28 — the runtime fixpoint reuses its lowered modules

`emit_entry_runtime_settled` and `emit_ir_runtime_settled`. Refs #8171.

## The shape

The x86-64 and arm64 emitters emit the runtime to a fixed point. A
runtime helper can need another helper, so the first pass can find new
needs. Then the emitter discards the first pass and emits the runtime
again with the larger set. Each pass loaded and lowered each runtime
module again. `rtmodule.load` and `runtime_module_lowering` are functions
of the roots of the module only.

## What changed

`EmitState.runtime_units` keeps each runtime module that the emission
loaded and lowered, by its roots. `emit_runtime` uses a kept module when
it has one. The fixpoint gives the kept modules to the next pass.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 3d1492c39. The base is the same tree with
the replay-row, counter-check and kind-table slices of the same pull
request.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 103.825 G | 102.700 G (−1.08%) |
| a five-line program | 128.8 M | 118.2 M (−8.2%) |

The whole-compiler binary and the small program's binary are
byte-identical to the base compiler's.
