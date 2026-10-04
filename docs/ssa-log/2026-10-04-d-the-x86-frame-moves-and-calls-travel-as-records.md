# 2026-10-04 — the x86 frame, moves and calls travel as records

`asm_ir` and `x86_native`, x86-64-linux. Slice 5 of #11305, refs #8171:
the rest of the x86 SSA emitter, the twin of the two arm64 slices before
it.

## The shape

After the label ids, the x86 SSA emitter still wrote most of its output as
text. `checker.fern` built for x86-64-linux emitted 532,237 records and
277,509 text lines, and a census that fenced the stack-op arms and the
hand-written runtime put 275,919 of those lines at the emitter's own
sites: 15 in the arms, 1,575 in the runtime. The shapes by count were
`movq N(%R), %R` 32,886, `popq` 28,519, labels 20,568, `pushq` 15,085,
`movq %R, N(%R)` 13,183, `xorl %E, %E` 13,048, `leaq .L(%rip), %R` 11,262,
`movq %R, %R` 9,094, `call sym` 8,692, `cmpl $N, N(%R)` 7,882, `ret` 6,508,
the indexed `movq N(%R,%R,8)` 9,207, and 25,000 `.cfi_*` lines. Only nine
record helpers existed on this side: the two-register moves and ALU ops,
the slot moves, the test forms and the branches by id.

The x86 assembler was 1.706 G of the 20.684 G stage-2 compile (8.2%), the
emitter 0.337 G more; the text parse (`x86_gas_prepare`), the line memo's
hash and the per-line `GasLine` structs were the part the records remove.

## What changed

- **The frame and the epilogue are records.** `ssa_frame` writes `pushq
  %rbp`, `movq %rsp, %rbp`, the saved registers' pushes and `subq $n,
  %rsp` through `ssa_push_r`, `ssa_mov_rr` and `ssa_alu_ir`; `ssa_xreg`
  now knows `%rsp`. `SsaFrame` carries the saved registers and whether the
  body is a buffered leaf, and `ssa_emit_epilogue` writes `leaq -8n(%rbp),
  %rsp`, the pops, `popq %rbp` (or `leave`) and `ret` as records at every
  return, the text for a buffered leaf, whose mark its frame replaces. The
  `.cfi_*` directives between them stay text.
- **Memory moves.** `ssa_mov_slot` is the `%rbp` case of `ssa_mov_rm`
  (a displacement off any base, at 64, 32 or 8 bits, the register named
  as its 64-bit form and narrowed in the text), with `ssa_mov_rm0` for the
  sites that spell `(base)` bare; `ssa_mov_rmi` and `ssa_movzb_mi` take the
  indexed forms (`x86_rec_mov_rmi`, `x86_rec_movzb_mi`); `ssa_alu_rm0` and
  `ssa_alu_im0` are the bare-base forms of the ALU memory ops. The field
  load, the three stores, the element reads and the element store in
  `ssa_mem_inst` go through them.
- **Constants.** `ssa_const_reg` writes the zeroing `xorl` through
  `ssa_zero`, `movl $n` and the sign-extending `movq $n` through
  `ssa_mov_ir` (`x86_rec_mov_ir`: B8+rd id, or REX.W C7 /0 id, the text
  arm's choices), and `ssa_imm_to`'s slot store through `ssa_mov_im`.
- **Pushes, pops, calls.** `ssa_push_operand` pushes a register or a slot
  (`x86_rec_push_r`, `x86_rec_push_m`), the host call's pops are
  `ssa_pop_r`, every `call sym` in the SSA path goes through
  `ssa_call_sym`, the `addq $n, %rsp` after a stack-ABI call through
  `ssa_alu_ir`, and `jae __fern_oob_abort` through `ssa_jump_sym`, the
  same pre-trimmed record line.
- **The inline templates** are record sequences with the text as each
  helper's fallback, their labels `asmcore.Lab`s minted where the template
  runs: the release entry guard, the string compare, the two array pushes
  (`arr_push_inline`, `arr_push_inline_u8`, with the count test and the
  tail shared through `ArrPushLabs`), and the uniqueness count test
  `ssa_rc_is_one`, which takes the base register instead of an operand
  string.

Every text fallback renders the text it did before, so `-emit asm` is
unchanged.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container, main at 6a190a39 against this change:

| | main | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.684 G | 20.616 G (−0.33%) |

A small gain, and the reason is the shape of the x86 assembler rather than
the emitter. Its line memo already parsed each distinct text line once, so
a repeated `popq %rbp` cost a hash lookup and a line copy, not a parse;
and a record still costs what a memoised line costs: one `GasLine` per
record, and `x86_gas_bytes` copying its bytes out of the words in every
relaxation round. In the module totals `x86_native` falls 94 M, the
emitter and `asmcore` rise 54 M for the record plumbing (`ssa_xreg`,
`lab_new`), and `x86_gas_bytes` alone rises from 120 M to 192 M with the
200,000 lines that moved to it. The next x86 step is therefore in the
assembler: a run of consecutive byte records as one line, laid down once
and re-copied as a span, so the 732,000 records become some tens of
thousands of runs between labels and branches.

Of checker's emitted lines, 732,206 travel as records and 77,540 as text,
from 532,237 and 277,509. The binaries are byte-identical on both targets
and `scripts/selfhost-emit-hashes` matches main on every (fixture, target)
pair.

## What is left

`leaq .L(%rip), %R` (11,262 lines: the string, constant and closure
addresses, a rip-relative fixup by name), the buffered leaf bodies, which
a capture keeps as text (about 10,000 lines of loads, compares and
returns), `movslq` (1,588), the `.cfi_*` lines, and the host-call arms
that push their result.
