# 2026-10-28 — the generic walk finds a method by its index

`parser.method_decl_at` in the generic-struct walk (`ms_call`). Refs
#8171.

## The shape

`method_decl_at` found the method that a call `obj.field(...)` names. It
scanned every function in the bundle for a method called `field`. The
method must have the receiver type of `obj` and no type parameters. The walk asks once for
each call, and each scan read the whole `decls` list. The scans cost
about 5.1 G Ir in the self-compile.

## What changed

`MFns` now holds `mix`, a name index over the methods without type
parameters. It also holds `mat`, the position of each in `decls`. `mfns_of` makes
them in the same pass that makes `pix` and `pat`. `method_decl_at` walks
the chain of `field` in `mix` and returns the first entry whose receiver
type matches. Each index chain keeps the order of `decls`, so it finds
the same method that the scan found.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e, with #13768. Main does not compile the
earlier subject, main at 8c68804. Main now requires an `unsafe` block
around a raw builtin.

| subject | main at 293eaa55e | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 125.239 G | 120.119 G (−4.09%) |

The whole-compiler binary is byte-identical to the base compiler's.
