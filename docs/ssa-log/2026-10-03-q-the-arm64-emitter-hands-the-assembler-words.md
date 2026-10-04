# 2026-10-03 — the arm64 emitter hands the assembler words

`asm_arm64_ir` and `arm64_native`, arm64-linux and arm64-android. Slice 1 of
#11305, refs #8171.

## The shape

The arm64 emitter writes every instruction as GAS text, and
`arm64_gas_program` reads it back: it trims and splits each line, picks the
mnemonic's family, vets the operands, parses the registers, and only then
calls the encoder. Compiling `checker.fern` for arm64-linux, that round trip
was 5.24 G of the stage-2 compiler's 26.3 G instructions; the encoding itself
was 547 M of it.

## What changed

When the output is a binary, the emitter gets a byte buffer
(`EmitState.recs`). At the shapes generated code writes most it hands the
assembler the encoded word instead of the text, and leaves a marker line in
the text so the words stay in order with the text around them:

- `\x01` takes the next word.
- `\x02label` takes the next word, a branch with no displacement, and
  resolves `label` through the same fixup the text path uses.

The shapes are `mov` between registers; `ldr`/`str` at an immediate offset
(frame slots and fields); `ldur`/`stur`; `add`/`sub` and `cmp` with a
register or a 12-bit immediate; the heap-floor `cmp #16, lsl #12`; and
`b`, `bl`, `b.cond`, `cbz`/`cbnz` and `tbz`/`tbnz` to a named label.

Each `arm64_rec_*` takes the operands as the text spells them and returns the
word that text assembles to, built by the encoder functions the text path
calls. It returns -1 when the operands fall outside the cases it covers, and
the emitter then writes the text. So anything the text path would refuse,
such as a `d` register, `sp` where it is not a base, a numeric local label,
or an out-of-range offset, still goes through the text path and is still
refused. Inside a capture the emitter writes text, because a captured arm is
spliced as text.

`-emit asm` and arm64-darwin get no buffer and are unchanged.

## Measured

`checker.fern` built for arm64-linux by the stage-2 compiler under callgrind,
4-core x86-64 container. Stage 2 is built from main at 8733fb53 and from
this change applied to it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 26.107 G | 23.523 G (−9.9%) |

Before the branches were added, with only the moves, loads, stores, add/sub
and compares travelling as words, the total was 25.021 G (−4.2%).

The arm64 binaries are byte-identical. `scripts/selfhost-emit-hashes`
matches main for every (fixture, target) pair.

`TestSelfHostArm64WordsMatchText` emits a broad program both ways in one
process and requires identical code and data. 26,743 of its 63,052
instructions travel as words. A deliberately swapped `mov` encoding fails it
at byte 80.

## What is left

Label definitions, `mov #imm`, the prologue and epilogue `stp`/`ldp`, and
the hand-written runtime are still text. So are the x86 emitter and
assembler, which #11305 takes next.
