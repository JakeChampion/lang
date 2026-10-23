# 2026-10-22 — builtin_sig finds its row by binary search

`checker.builtin_sig` and the declaration check that asks it. Refs #8171.

## The shape

`builtin_sig` scanned all 242 rows of `builtin_sigs()` for each name,
about 5 K Ir a call, 21 K calls per self-compile. 13 K of them came from
the declaration check, which asks whether a free function is named like
a builtin. It parsed the row it found, `type_from_name` per type, only to
discard the type.

An array literal of constants is already a static box. So the cost was
the scan, not a rebuild of the table, and a top-level `const` array
costs the same.

## What changed

The rows are now in byte order, and `builtin_row` finds a name's row by
binary search. `(` sorts below every byte of a name, so the first row
not below `name(` is the one that starts with it, if any does.
`builtin_sig` parses only that row. The declaration check asks
`builtin_row` whether a row exists. `TestSelfHostBuiltinRowsAreSorted`
pins the order, because a row out of place is not found.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is one frozen copy of main at ddc5b11.

| subject | main at 4426eac | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 74.092 G | 73.988 G (−0.14%) |

The whole-compiler binary is byte-identical to the base compiler's.

## What is left

The declaration check also builds `registered_intrinsics()` and scans it
for each free function.
