# 2026-10-02 — the assembler stops scanning by byte, the renderer stops concatenating by digit

`x86_native.x86_str_split`, `x86_gas_prepare`, `x86_gas_is_xmm`,
`x86_gas_has_paren`, `x86_gas_atoi`; `util.i32_to_string`, `i64_to_string`.
Refs #8171. No
emitted byte changes: the `selfhost-emit-hashes` sweep, which hashes the
assembled ELF and so has the assembler inside it, is 1,965 rows per
compiler with 0 differing against a compiler built from main at 00735cad.

## What the profile named

On the stage-2 compile of `checker.fern`, `x86_gas_assemble` is 2.62 G Ir
inclusive, 6.6% of the compile, and its text handling was the self cost:

- `x86_str_split` 449 M, splitting the emitted text on `\n` by comparing
  and bumping one byte at a time, where `x86_str_indexof` already found a
  needle's first byte with `__memchr`.
- `x86_str_indexof` 727 M, 362 M of it under `x86_str_contains`: 1.53 M
  calls from `x86_gas_is_xmm`, which asked whether a token contains `xmm`
  to decide whether it names a register, and 2.4 M from the mov and ALU
  families asking whether an operand holds a `(`.
- `x86_gas_prepare` materialised every line two or three times: the
  split's copy, a second for a comment-stripped line, and `x86_gas_trim`'s
  for the trimmed one, 734 k lines through a `string[]` it then freed.
- `x86_gas_atoi` 145 M plus 462 k `__fern_str_concat` calls, one per `$`
  it stripped by copying the token.
- `util.i32_to_string` 70 M plus 1.07 M `__fern_str_concat` calls: one
  allocation per digit, from `asm_ir.ssa_reg`, `ssa_slot`, `ssa_join`
  and the rest of the emitters.

## What changed

`x86_str_split` jumps to the separator's first byte with `__memchr`, as
`indexof` does, and compares the rest only there. `x86_gas_prepare` no
longer splits at all: it scans the text for `\n` with `__memchr`, reads
each line as a view, strips the comment and trims by index on the view
(`x86_gas_trimmed`), and copies the line once. `x86_gas_is_xmm` tests
the prefix `x86_gas_xmm` decodes, `xmm` behind an optional `%`, so a
symbol that happens to carry `xmm` is no longer a register. The operand
test is `x86_gas_has_paren`, `__memchr` for `(` directly. `x86_gas_atoi`
indexes past a `$` instead of copying. Both renderers count their digits,
fill a `u8[]` of that length, and build the text once with
`string_from_bytes_unchecked`.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container,
stage-2 compilers built from main (539e4e6, whose compiler sources are
00735cad's) and this change on it.

| | main | the search and renderer cuts | with `prepare` over views |
|---|--:|--:|--:|
| stage 2, total Ir | 39.79 G | 39.10 G (−1.74%) | 38.97 G (−2.04%) |
| stage 2, `x86_str_split` self Ir | 449.1 M | 72.7 M | 0 |
| stage 2, `x86_str_indexof` self Ir | 727.5 M | 365.8 M | 365.8 M |
| stage 2, `x86_str_contains` self Ir | 31.7 M | 0 | 0 |
| stage 2, `x86_gas_is_xmm` self Ir | 9.2 M | 57.6 M | 57.6 M |
| stage 2, `x86_gas_mov_rm` + `x86_gas_alu_family` self Ir | 158.8 M | 271.3 M | 271.3 M |
| stage 2, `x86_gas_atoi` self Ir | 144.7 M | 107.2 M | 107.2 M |
| stage 2, `x86_gas_prepare` self Ir | 180.1 M | 180.1 M | 203.2 M |
| stage 2, `x86_gas_trim` + `x86_gas_trimmed` self Ir | 230.7 M | 230.7 M | 224.1 M |
| stage 2, `util.i32_to_string` self Ir | 70.4 M | 90.3 M | 90.3 M |
| stage 2, `__fern_str_concat` self Ir | 757.8 M | 673.3 M | 614.7 M |
| stage 2, `__fern_str_free` self Ir | 341.6 M | 309.5 M | 286.7 M |
| stage 2, `__fern_alloc` self Ir | 1,116.3 M | 1,104.4 M | 1,120.9 M |

The rows that rise are the byte tests the callers now do themselves
(`__memchr` has no symbol of its own and lands on its caller), the
renderers' digit count and buffer fill, and `prepare`'s own scan; the rows
that fall are the searches, the copies, the concatenations and the frees
they replace.

## Witnessed

`TestSelfHostX86Gas` (with new rows for `is_xmm` as a prefix test, `atoi`
past `$`, `has_paren`, and `split` on empty fields, a trailing separator,
a two-byte separator and no separator), the rest of `TestSelfHostX86Gas*`,
`TestSelfHostX86Capstone`, `TestSelfHostX86TableRowsMatchNative`,
`TestSelfHostIRStrengthPeephole` (whose readability rows round-trip both
renderers at the width edges), `TestSelfHostSemanticSourceRC`, the lint
ratchet, and the emit-hash sweep. The renderers were also run on wasm
with the i32 and i64 edge values.

## Next

`x86_gas_trim` still copies 1.4 M operands and label tails through
`slice + ""`; every call site hands it a slice, so the copy is the one that
makes the owned string `GasLine` keeps, and the cut is a `GasLine` of views
into the emitted text rather than a cheaper trim. `x86_str_indexof` keeps
366 M, all from `x86_gas_prepare`, `x86_gas_parse_mem` and
`x86_gas_comment_start`, one search per line or operand.
