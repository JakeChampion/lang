# 2026-10-28 — the string constant hex is built as bytes

`ir.hex_text`, under `ir.op_const_str` and `ssarc.static_values`. Refs
#8171.

## The shape

A string constant op keeps its text as hex. For each byte of the text,
`hex_text` made two one-byte strings and added each one to the result. So each byte cost two string allocations and two copies. The
self-compile made 1.9 M of these concatenations, at about 410 M Ir.

## What changed

`hex_text` now appends the two hex digits to a `u8[]` and makes the
string once, with `string_from_bytes_unchecked`. `hex_digit` returns the
digit byte. `const_str_text` reads the hex back in the same way.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e. The base is this branch before
the change, main at 2cdae579d with #13785.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 106.369 G | 106.007 G (−0.34%) |

The whole-compiler binary is byte-identical to the base compiler's.
