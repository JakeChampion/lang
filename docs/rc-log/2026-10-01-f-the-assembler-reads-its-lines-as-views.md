# 2026-10-01 — the assembler reads its lines as views, and a label is copied per dot

`x86_native.x86_gas_prepare`, `asmcore.sanitize_label`. Refs #8171.

## Four copies of every line

`x86_gas_prepare` split the module's text into one string per line
(`x86_str_split`), cut the comment off with another copy, trimmed with a
third (`x86_gas_trim` is `slice + ""`), and trimmed the operand text with a
fourth, before the label, mnemonic and operands were copied into the
`GasLine`. On the stage-2 compile of `checker.fern` that was 1.1% in
`x86_str_split`, 0.45% in `x86_gas_trim`, and 2.1 million of the 13.3
million `__fern_str_concat` calls.

The line is now a view of the text (`__memchr` for the newline,
`x86_gas_trimmed` for the trims) until its parts are kept: `label`, `body`,
`mnem` and `rest` are copied once each, the only copies the function
makes. `x86_str_split` stays for `.align`'s operand list.

`asmcore.sanitize_label` rebuilt every symbol one byte at a time, a
concatenation per character, 1.3 million calls at 25 characters apiece. It
copies per dot now: a name without one is handed back as it is, and
`module.name` is two slices and the separator.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container, both
rows on the same source. Stage 2 is the compiler the self-host compiler
builds from the same commit.

| | main (7b1ae33) | this change |
|---|--:|--:|
| native-built, total Ir | 69.95 G | 69.55 G (−0.6%) |
| stage 2, total Ir | 40.16 G | 39.56 G (−1.5%) |
| stage 2, `x86_str_split` self Ir | 441 M | 0 |
| stage 2, `x86_gas_trim` self Ir | 181 M | 57 M |
| stage 2, `sanitize_label` self Ir | 63 M | 8 M |
| stage 2, `__fern_str_concat` self Ir | 932 M | 780 M |

Byte-identical against a compiler built from main: the `checker.fern`
binary on x86-64, its assembly text on x86-64 and arm64, its wasm module,
the compiler's own assembly, the stage-2 compiler built from this source,
and all 1,959 `selfhost-emit-hashes` rows.

## Next

On the stage-2 profile after this: the emit arms test an operand's shape
with `x86_str_contains(op, "(")` on every relaxation round, 3.9 million
calls, and parse a memory operand per round (`x86_gas_parse_mem`, 1.5
million); both are properties of the text that `x86_gas_classify` could
settle once per line, if the arms took the `GasLine` rather than its
operand strings. `ssa_lift.lift_impl` at 2.7%, `str_eq` at 2.5%.
