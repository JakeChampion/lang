# 2026-10-21 — a struct literal's field spelling comes through the struct index

`parser.lit_field_spelling` and `me_field_spelling_of`, the monomorphiser's
expected type for a struct literal's field value. Refs #8171.

## The shape

Each field value of each struct literal asks for the field's declared
spelling, and both functions found the struct by a scan of every struct
declaration in the module, the second scan after the first had already
found it. The per-instruction profile of the whole compiler's self-compile
put 5.7 M iterations of each scan, over 200 M instructions, inside code the
debug info attributes to `mono_call_expected`.

## What changed

`MFns.snames` already indexes the structs by name; both lookups walk its
chain, in declaration order, so the first struct with the field, and the
first struct of the name, are the ones the scans found.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the binary-search slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 75.474 G | 75.163 G (−0.41%) |

The whole-compiler binary is byte-identical to the base compiler's.
