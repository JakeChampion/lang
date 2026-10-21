# 2026-10-21 — call_ret_type finds the plain function through an index

`parser.call_ret_type`, `parser.MFns` (`pix`, `pat`), `parser.mfns_of`.
Refs #8171.

## The shape

Monomorphisation types a call's result through `call_ret_type`, which
found the callee by scanning every declaration of the module for the last
plain free function of the name, once per call it typed. The scan is
linear in the module's declarations and the number of calls grows with
them, so on the whole compiler's self-compile it was 323 M instructions,
thirteen times the `checker.fern` figure for a subject five times smaller.

## What changed

`MFns` already indexed the generic free functions by name. It now indexes
the plain free functions too, in declaration order (`pix`, a
`util.NameIndex` whose chains ascend, and `pat`, each entry's position in
`decls`), and `call_ret_type` walks the name's chain to its last entry,
which is the declaration the scan from the front reached last.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the phi-row slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 77.101 G | 76.770 G (−0.43%) |

The whole-compiler binary is byte-identical to the base compiler's.
