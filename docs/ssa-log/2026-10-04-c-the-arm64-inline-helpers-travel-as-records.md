# 2026-10-04 — the arm64 inline helpers travel as records

`asm_arm64_ir` and `arm64_native`, arm64-linux. Slice 4 of #11305, refs
#8171; the second part, after the frame, constants and address pairs.

## The shape

The arm64 SSA emitter's inline helpers were text templates: the string
compare, the array push and its byte form, the uniqueness test the rc
guards and the fused branches share, the bounds checks before an array or
string access, and the string view. Each wrote several lines into one
string with labels named through the function's prefix, and a bounds check
branched over its trap through the numeric local `1f`. With the frame and
constants gone to records they were most of the 203,316 text lines left in
checker's listing: `b.ne` to a named label 9,364 times, `cmp w, #n`
8,997, `ldur w, [x, #-8]` 7,395, `cmp x, x` 6,997, `ldr x, [x, x, lsl #3]`
6,395, `ldr x, [x]` 6,301, `tst x, #1` 5,919.

## What changed

Each template is a sequence of record helpers with the text it wrote as
each one's fallback: `ssa_ldst0` (`[base]`), `ssa_ldst_reg` (`[base, idx,
lsl #3]`), `ssa_ldrb`, `ssa_ldrb0`, `ssa_strb`, `ssa_ldrb_reg`, `ssa_lsl`,
`ssa_cmp_rr`, `ssa_addsub_rr`, `ssa_tst_imm` and `ssa_and_imm`, over new
encoders `arm64_rec_ldst_reg`, `arm64_rec_ldrb`, `arm64_rec_strb`,
`arm64_rec_ldrb_reg`, `arm64_rec_lsl_imm`, `arm64_rec_tst_imm` and
`arm64_rec_and_imm`, each the word the text arm encodes. The templates'
labels are `asmcore.Lab`s minted where the template runs, so their
branches and definitions travel by id; `ssa_trap_guard` writes a bounds
check's `b.<cond> 1f`, `b __fern_oob_abort`, `1:` through a label id when
records are on and the numeric local in text. The uniqueness test's join
label, which its callers still name, is minted the same way inside
`ssa_unique_flags`.

`ssa_rc_prim`'s answer moves, `ssa_release_entry`'s two exits and
`ssa_rc_guard`'s tag strip take the same helpers.

## Measured

`checker.fern` built for arm64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container, main at 9c8eb003 against the first
part and against both:

| | main | frame, constants, addresses | with the inline helpers |
|---|--:|--:|--:|
| stage 2, arm64 target, total Ir | 20.521 G | 20.007 G (−2.51%) | 19.203 G (−6.42%) |

The inline helpers alone are 4.02% of the first part's total. `arm64_native`'s
own instructions fall from 1.36 G after the first part to 0.72 G.

Of checker's emitted lines, 752,787 travel as records and 68,687 as text,
from 617,204 and 203,316 after the first part. The binaries are
byte-identical and `scripts/selfhost-emit-hashes` matches main on every
(fixture, target) pair.

## What is left

The `ldr Xd, =lit` pool loads, the `.cfi_*` lines, the stack-op arms and
the hand-written runtime, and the function labels, which are named.
