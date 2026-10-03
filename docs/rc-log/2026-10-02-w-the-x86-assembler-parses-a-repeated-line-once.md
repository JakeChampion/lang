# 2026-10-02 — the x86 assembler parses a repeated line once

`x86_native.x86_gas_prepare` (`LineMemo`, `x86_line_memo_lookup`,
`x86_line_memo_added`, `x86_line_memoable`), `GasLine.memo`, `GasText`,
`x86_gas_assemble_pass_prepared` (`mat` / `mend`, `x86_copy_bytes`).
Refs #8171. No emitted byte changes: the `selfhost-emit-hashes` sweep is
1,965 rows per compiler with 0 differing against a compiler built from
main at 173f70a4, and the `checker.fern` binaries are byte-identical.

## What the profile named

`x86_gas_assemble` was 3.86 G of the 31.12 G stage-2 compile of
`checker.fern`, 12.4%: the emitter writes text and the assembler parses
it back, about 2,600 instructions a line. The text has 758k lines, 599k
of them instructions. Of the 422k instruction lines that name no label,
8,612 are distinct, and the 2,000 most common are 97% of them (`movq
%rbx, %rax` 6,762 times, `cmpq $0x10000, %r11` 6,419, `ret` 5,799, …).
Every one was comment-stripped, trimmed, split, classified
(`x86_gas_classify_with`, 371 M) and encoded (`x86_gas_emit_op`, 859 M)
as if new, in both relaxation rounds for the lines the replay cache
(`EncCache`) does not keep and in the first for the rest.

## What changed

`x86_gas_prepare` keys a memo by the raw text between two newlines,
before the comment strip: a hit appends the memo's `GasLine` and
parses nothing. A line enters the memo when it has no label prefix and
`x86_line_memoable` admits it: not a branch to a label (`jcc`, `jmp`,
`call name`), and no operand through `%rip`, the lines whose encoding
reads the label table per round and whose text is mostly unique. Every
repeat shares the one `GasLine`, whose `memo` field is its index, and
`x86_gas_prepare` returns the lines with the memo's size (`GasText`).

In the recording round, a line the replay cache would keep (it queued no
fixup, branch, unknown, rodata or bss) also records its bytes by memo
entry, and a later repeat copies those bytes from the code buffer
(`x86_copy_bytes`) instead of encoding its text again, taking the same
replay-cache entry as if it had. The replay round is as it was.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 173f70a4 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 31.12 G | 30.23 G (−2.87%) |
| stage 2, `x86_gas_assemble` inclusive Ir | 3.86 G | 2.96 G |
| stage 2, `x86_gas_prepare` inclusive Ir | 1.49 G | 1.10 G |
| stage 2, `x86_gas_emit_op` inclusive Ir, calls | 859 M, 787k | 318 M, 381k |
| stage 2, `x86_gas_classify_with` inclusive Ir, calls | 371 M, 599k | 49 M, 193k |
| stage 2, `x86_line_memo_lookup` inclusive Ir | 0 | 378 M |
| stage 2, `x86_copy_bytes` inclusive Ir | 0 | 109 M |

The first version inserted a parsed line under its comment-stripped
text while looking it up under the raw text, so a repeated line
carrying a `#` comment never hit; keyed on the raw text at both ends,
the same compile stacked with `2026-10-02-z` measures 30.168 G to
30.104 G.

## Witnessed

`TestSelfHostX86*` (47 tests: the encoders, the gas corpus, the ELF
runs, relaxation, the AVX2 kernels, the capstone and native-table
checks), `TestSelfHostCLIX86_64`, `TestSelfHostCfi*`, the lint ratchet, `make
fmt-check`, and the emit-hash sweep.

## Next

The memo lookup is 500 Ir a line (378 M for 758k): `x86_line_bucket`
reads the raw line byte by byte through a bounds check, since its loop
is `while (i < n)` and the parser's elision takes only `while (i <
s.len())`; `x86_name_bucket` has the same shape. The assembler is still
2.96 G: `x86_gas_prepare` 1.10 G (the labels, 160k lines, and the 177k
instruction lines naming a label take the full parse, as do the 8.6k
distinct memoable lines and the few thousand branch and %rip lines
among the rest), `x86_gas_link_labels` 274 M,
`x86_relax_settle` 326 M, releasing the `GasLine` list 174 M.
