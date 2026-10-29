# 2026-10-29 — the runtime emission settles in one pass

`emit_entry_runtime_settled`, `emit_ir_runtime_settled` and
`asmcore.settles`. Refs #8171.

## The shape

The x86-64 and arm64 emitters emit the runtime to a fixed point. The
loop emitted the runtime again each time a pass added a need. But a new
need changes the output only if the pass asked for it before the pass
added it. Most new needs come from a helper that the pass emits later.
Then the second pass gave the same text.

## What changed

During a runtime pass, `has_need` records each name that it does not
find. `need` uses a lookup that records nothing. `asmcore.settles` keeps
the pass unless the pass added a need that it asked for before. Then the
emitter emits the runtime again, as before.

Three needs made the pass of the whole compiler restart:

- The reads of a file, a directory or a host name need
  `__fern_utf8_valid`, which needs `utf8_blocks`. The new `utf8_valid`
  need puts this in `runtime_need_deps`, before the `utf8_blocks` block.
  It replaces a 14-name condition in each backend.

- On x86-64, a clock helper wrote its own `__c_call2` shim after the
  shim block. The shim block now adds the shim need when the vDSO clock
  is in use, as arm64 does.

- On x86-64, runtime helpers that the pass emits after the heap block
  need leaf trap stubs. The trap stubs now come at the end of the
  runtime.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 3d1492c39. The base is main at 4d77957c5.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 103.169 G | 102.935 G (−0.23%) |
| a five-line program | 148.97 M | 147.56 M (−0.94%) |

The arm64-linux, arm64-darwin and wasm32-wasi outputs are byte-identical
to the base compiler's. The x86-64 outputs have the same size, with the
trap stubs at a different address.
