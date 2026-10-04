# 2026-10-04 — the .cfi_* directives travel as records

`asm_ir`, `asm_arm64_ir`, `x86_native`, `arm64_native` and `asmcore`, both
targets. Slice 7 of #11305, refs #8171.

## The shape

After the frame, the helpers and the x86 byte runs, the text lines left in
checker's listing were mostly unwind directives: 30,936 `.cfi_*` lines of
the 68,939 text lines on arm64, 27,437 of the 77,540 on x86. Each one went
through the assembler's text path: a trim, a bucket hash and a memo lookup
on x86, the line cut and operand split on arm64, then `cfi_directive`,
which split the operands again, trimmed each, and looked the register name
up in the profile table to reach the one DWARF rule the directive stands
for.

## What changed

A directive is the `\x06` record: one eight-byte word holding the kind,
the DWARF register number and the offset, written by `asmcore`'s
`EmitState.cfi`. Both emitters write every directive of the register path
through one `ssa_cfi`, which records when records are on and writes the
text it wrote before when they are off; `ssa_dwarf` maps the register name
(the DWARF column order on x86, `xN` and `sp` on arm64). The buffered x86
leaf bodies still carry their directives as text, since a captured body is
text by construction.

Both assemblers take the record in `cfi_record`, which reaches the same
`cfi_add_rule` and `cfi_proc_directive` the text path reaches, through a
`cfi_offset_rule` the `.cfi_offset` text arm now shares: the data-alignment
check, the refusal of a positive factor and the extended form for a large
column are written once. A record outside `.cfi_startproc`, outside
`.text`, or of a kind the recorder does not know is a refusal naming it,
as a malformed directive is. The x86 assembler gives the record its own
line kind, `GK_CFI`, applied in each round at the code offset the round
has reached, so relaxation moves a rule with the instruction it follows
exactly as the text line did.

## Measured

`checker.fern` built by the stage-2 compiler under callgrind, 4-core
x86-64 container, main at 809e2ad4 against this change:

| | main | with cfi records |
|---|--:|--:|
| stage 2, arm64 target, total Ir | 19.295 G | 19.222 G (−0.37%) |
| stage 2, x86-64 target, total Ir | 20.523 G | 20.430 G (−0.45%) |

`arm64_native` falls from 717 M to 660 M: `cfi_directive` (10 M),
`cfi_trim`, `cfi_reg_num` and `cfi_split_ops` (18 M together) leave, and
`arm64_gas_line_cut` halves to 14 M; `cfi_record` is 1.8 M. `x86_native`
falls from 1,545 M to 1,504 M: `cfi_directive` (18 M), `cfi_trim`,
`cfi_split_ops` and `cfi_reg_num` (23 M) leave, `x86_line_bucket` drops
from 17 M to 11 M; `cfi_record` is 3 M. The rest of each gain is the
string work the emitters no longer do for the directive text.

Of checker's emitted lines, 784,290 travel as records and 38,051 as text
on arm64 (from 753,195 and 68,939); 766,817 and 43,123 on x86 (from
732,206 and 77,540). The 315 `.cfi_*` text lines left on x86 are in the
buffered leaf bodies and the runtime; the 48 on arm64 are the runtime's.
The binaries are byte-identical on both targets, their `.eh_frame`
included, and `scripts/selfhost-emit-hashes` matches main on every
(fixture, target) pair.

## What is left

On x86, the 11,262 `leaq .L(%rip)` lines, the buffered leaf bodies,
`movslq`, and the function labels by name; on arm64 the `ldr Xd, =lit`
pool loads, the function labels, and the spill `str`/`ldr` through `[sp]`.
Behind those, the x86 assembler's per-round byte copy and line table, which
the previous entry costs out and which a single-pass core removes.
