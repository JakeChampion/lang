# 2026-10-21 — ow_variant_known asks a name index

`checker.ow_variant_known`, in the ownership walk. Refs #8171.

## The shape

Each identifier and field access the ownership walk judges asks whether its
name is a declared variant, and the answer scanned every struct declaration
in the module: 80 M instructions over 5,877 calls on the whole compiler's
self-compile, ten times the `checker.fern` figure for a subject five times
smaller.

## What changed

The ownership pass builds one `util.NameIndex` over the module's struct
names and carries it in `OwnCtx`; the question walks the name's chain with
the same test.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the field-spelling slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 75.163 G | 75.087 G (−0.10%) |

The whole-compiler binary is byte-identical to the base compiler's.
