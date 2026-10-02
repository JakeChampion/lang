# 2026-10-02 — the assembler classifies each mnemonic once

`x86_native.x86_gas_prepare`, `x86_gas_classify`. Refs #8171. No emitted
byte changes: the `selfhost-emit-hashes` sweep is 1,965 rows per
compiler with 0 differing against a compiler built from main at
9cc02c45, and the `checker.fern` binaries the two stage-2 compilers
emit are byte-identical.

## What the profile named

`x86_gas_prepare` was 1.96 G of the 35.38 G stage-2 compile of
`checker.fern`, and `x86_gas_classify` 965 M of that: about 1,640
instructions for each of the 588,514 instruction lines, most of it a
chain of some forty mnemonic tests (`x86_gas_is_branch`,
`x86_gas_mov_size`, `x86_gas_alu_op`, …), each a run of string
compares against a table, tried in order until one takes the line. The
module uses about 150 distinct mnemonics, so the same chain ran about
4,000 times per mnemonic with the same answer.

## What changed

The classification is split at its reads of the operand text. The
mnemonic-only tests run in `x86_gas_mnem_verdict`, which keeps their
order and records, as a `MnemVerdict`, the arm each of its three
stretches selects (before the operand split, after it, and the last
three) with the suffix width as it stood. `x86_gas_classify_with` then
applies the verdict to one line, with the five tests that read the
operands (`vzeroupper` and a fixed-encoding op want no operands, the
comma split, the shift-by-immediate form, the source/destination trims)
where they stood. `x86_gas_prepare` decides the verdict once per
distinct mnemonic, indexed through the label-name buckets
(`MnemIndex`); `x86_gas_classify`, the encoder fixtures' entry, runs
both halves.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 9cc02c45 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 35.38 G | 34.89 G (−1.39%) |
| stage 2, `x86_gas_prepare` inclusive Ir | 1.96 G | 1.47 G |
| stage 2, `x86_gas_classify` / `_with` inclusive Ir | 965 M | 364 M |
| stage 2, `x86_mnem_lookup` inclusive Ir | 0 | 87 M |
| stage 2, `x86_gas_mnem_verdict` inclusive Ir | 0 | 0.14 M |
| stage 2, `__fern_str_eq` self Ir | 591 M | 559 M |

The verdict itself is 0.14 M for the whole module: the chain runs 150
times, not 588 k. The 87 M the lookup costs is a hash of the mnemonic
and one compare per line; a numeric mnemonic code carried by the line
would remove it, but no emitter hands the assembler one.

## Witnessed

`TestSelfHostX86Gas*`, `TestSelfHostX86Capstone`,
`TestSelfHostX86TableRowsMatchNative`, `TestSelfHostAsmLoad*`,
`TestSelfHostSemanticSourceRC`, the lint ratchet, `make fmt-check`,
and the emit-hash sweep. The Capstone and table-row suites compare
every classified arm's encoding against the native assembler, so a
verdict that selected the wrong arm for any mnemonic would show there.

## Next

`x86_gas_prepare` is 1.47 G: `x86_str_indexof` 310 M (the label colon,
the mnemonic space, the comment hash and the quote are each a scan),
`x86_gas_parse_mem` 291 M, `x86_gas_link_labels` 271 M, `x86_gas_trim`
268 M and `x86_gas_top_comma` 107 M. The operand text is still decoded
again by every arm of `x86_gas_emit_op` in each round; a GasLine that
keeps the register numbers and the parsed memory operand would take
that out of the rounds. Elsewhere, self cost: `ssa_lift.lift_impl`
1.10 G, `__fern_alloc` 1.04 G, `util.hash_bucket` 0.81 G.
