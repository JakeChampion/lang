# 2026-10-04 — the arm64 frame, constants and addresses travel as records

`asm_arm64_ir` and `arm64_native`, arm64-linux. Slice 4 of #11305, refs
#8171; the first of two parts over what the arm64 SSA emitter still wrote as
text.

## The shape

After the label ids landed, `checker.fern` compiled for arm64-linux emitted
820,520 lines, of which 289,205 were still text and took the assembler's
line parse at about 2,800 Ir each: about 800 M of the stage-2 compile. By
shape, the largest were the function frames (`stp`/`ldp` of x29 and x30 and
the callee-saved pairs, `mov x29, sp`, `sub sp, x29, #n`, `ret`: about
45,000 lines), `mov Xd, #imm` (19,566), the `adrp` and `add :lo12:` pairs
that take a label's address (11,214 each), and the inline helpers' text
templates (string compare, array push, the uniqueness test, the bounds
checks), which the second part takes.

## What changed

- The frame record, the callee-save pushes and the epilogue are records:
  `ssa_save` and `ssa_emit_epilogue` write `stp`/`ldp` with pre- and
  post-index (`arm64_rec_pair`), the single `str`/`ldr` with writeback
  (`arm64_rec_ldst_wb`), `mov sp, x29` and `mov x29, sp` (the `add #0` form
  `arm64_rec_mov` now takes sp as), `sub sp, x29, #n` (sp in
  `arm64_rec_addsub_imm`) and `ret`. The `.cfi_*` directives between them
  stay text. `SsaFrame` carries the saved registers instead of the epilogue
  as a string.
- `mov Xd, #imm` from a decimal one movz or movn carries is a word
  (`arm64_rec_mov_imm`), at the constant loads and the `__fern_arr_box`
  argument.
- `adrp Xd, label` and `add Xd, Xd, :lo12:label` are two named records
  (`\x02`): the assembler sorts a named record by its word, a branch to the
  label's fixup as before, an adrp or the unshifted 64-bit add immediate to
  the page-fixup queue the text arms feed (`arm64_named_kind`).
- `fr_sub_imm` writes its small form through `ssa_addsub_imm`, so the
  frame's `sub sp, sp, #n` is a word too.

Every text fallback renders the text it did before, so `-emit asm` and
arm64-darwin are unchanged.

## Measured

`checker.fern` built for arm64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Stage 2 is built from main at 83727b0c
and from this change applied to it:

| | main | this change |
|---|--:|--:|
| stage 2, arm64 target, total Ir | 20.468 G | 19.955 G (−2.51%) |

`arm64_native`'s own instructions fall from 1.75 G to 1.36 G of the compile.

Of checker's emitted lines, 617,204 travel as records and 203,316 as text,
from 531,315 and 289,205. The binaries are byte-identical and
`scripts/selfhost-emit-hashes` matches main on every (fixture, target)
pair.

## What is left

The inline helpers' templates and the labels they define by name, the
`ldr Xd, =lit` pool loads, the stack-op arms, and the `.cfi_*` lines, which
are a quarter of what is still text.
