# 2026-10-22 — four name tests stop at the first byte

`semreach.absorb_inst`, `ssasem.is_dyn_call`, `checker.builtin_sig` and
`parser.is_destr_marker`. Refs #8171.

## The shape

Four name tests did a full compare, or a hash, where the first byte
already answers.

- `absorb_inst` asked `named` about every instruction of every executed
  block, and most instructions carry no name. Each asked the index for
  the empty string: a hash and a bucket walk, 1.4 M per self-compile,
  104 M Ir under `reach`.
- `is_dyn_call` took a four-byte slice of the name and compared it as a
  string, 2.7 M times, 102 M Ir of its own.
- `builtin_sig` tested each of 242 signature rows with a prefix compare
  per call, 28 K calls, 3.6 M compares, 267 M Ir inclusive.
- `is_destr_marker` ran four prefix compares per type name, 2.1 M calls.

## What changed

An empty name reaches nothing, so `absorb_inst` does not ask. `is_dyn_call`
reads the four bytes. `builtin_sig` skips a row whose first byte differs
from the name's before the compare. `is_destr_marker` answers no for a
name that does not start with `@`. Each test answers as before.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is one frozen copy of main at ddc5b11.

| subject | #12960 at 868c0b1 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 74.397 G | 74.186 G (−0.28%) |

The whole-compiler binary is byte-identical to the base compiler's.

## What is left

`builtin_sig` still scans its rows. A table the checker builds once per
module, or rows sorted for a binary search, can end the scan.
`raw_floor_find` builds the 40 raw-floor signatures as records on every
call whose name has one of its three prefixes, and drops them again.
That is 41 K builds per self-compile, 135 M Ir inclusive. A static name
list, consulted before the records are built, can remove most of that.
`util.str_has_prefix` keeps 200 M Ir of self time over its other
callers.
