# 2026-10-21 — x86 names bucket by the string-hash kernel

`x86_native.x86_name_bucket`, under the label, mnemonic and line tables.
Refs #8171.

## The shape

The x86 assembler's three name tables bucketed a name by a byte loop in
Fern, run once per lookup and once per insertion; label names are long
(`__fn_checker__…`), so the loop was most of a lookup: 76 M instructions
on the whole compiler's self-compile, 64 M of them under
`x86_gas_label_lookup`, sixteen times the `checker.fern` figure for a
subject five times smaller.

## What changed

The bucket is `__str_hash` over the name, the kernel every other index
uses, reduced modulo the same 4093. Which bucket a name lands in reaches no
output: each chain keeps definition order and every lookup compares names.
`x86_line_bucket`, over a view the kernel does not take, keeps its loop; the
memo it keys is built and read through it alone.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the splice-table slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 76.152 G | 76.107 G (−0.06%) |

The whole-compiler binary is byte-identical to the base compiler's.
