# 2026-10-28 — the RC verifier compares kind constants

`irverifyrc` in the IR verifier. Refs #8171.

## The shape

`irverifyrc.is_kind(op, name)` compared an op's kind with
`ir.kind_id(name)`, a lookup of the kind's name in a table. The verifier
asked it 13.4 M times per self-compile, for 17 kind names, and each
lookup cost about 100 Ir: 1.35 G Ir in all.

## What changed

Each test now compares `kind_tag` with the kind's `ir.K_*` constant. One
kind, `struct_set_shape`, has no constant, so its single test still calls
`ir.kind_id`. `is_kind` is deleted.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 8c68804.

| subject | main at 8c68804 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 136.055 G | 134.572 G (−1.09%) |

The whole-compiler binary is byte-identical to the base compiler's.
