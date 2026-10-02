# 2026-10-02 — the branch relaxation fixpoint is found on offsets, not by re-assembling

`x86_native.x86_relax_settle`, `x86_gas_assemble`, `X86Asm.text_aligned`.
Refs #8171.

## Four whole assemblies

The self-host assembler relaxes branches by re-assembling the whole text with
the previous round's decisions until they stop changing. Compiling
`checker.fern`, that was four rounds of about 570,000 instructions each:
9.4% of the stage-2 compile, every round re-encoding the same bytes.

Within `.text` only two things change size between rounds: a relaxable branch
(a short `jmp` grows 3 bytes to `E9 rel32`, a short `jcc` 4 bytes to
`0F 8x rel32`) and alignment padding. The first round emits every relaxable
branch short and records each one's offset, form and target. With no
alignment in `.text`, a later offset moves by exactly the growth of the long
branches before it, so `x86_relax_settle` runs the same test
`x86_relax_grow` applies, on those moved offsets, until no decision changes.
One more round is assembled with the result, and the loop's own check
confirms it: if that round's decisions differed, the loop would carry on as
before.

`x86_gas_align` sets `text_aligned` when an alignment directive reaches
`.text`, and such a program keeps the round-by-round loop. The compiler never
emits one there (every `.align` it writes is in `.rodata` or `.data`).

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container, both
columns on the same source.

| | main (22b54fd) | this change |
|---|--:|--:|
| native-built, total Ir | 66.88 G | 63.48 G (−5.1%) |
| stage 2, total Ir | 37.72 G | 36.26 G (−3.9%) |
| relaxation rounds assembled | 4 | 2 |

Byte-identical against a compiler built from main: the `checker.fern` binary,
the stage-2 compiler built from this source, and all 1,965
`selfhost-emit-hashes` rows.

`TestSelfHostX86GasRelaxation` is new: the native assembler's relaxation cases
(GNU as's bytes) and two cascades where a later branch's growth pushes an
earlier one out of range, for `jmp` and for `jcc`. It passes on the assembler
before this change as well.

## Found on the way

Porting the native cases found that the round-by-round loop pins every
out-of-range branch at once. That reaches the least fixpoint only while
distances can only grow; a pad in `.text` can absorb an earlier branch's
growth, and GNU as then keeps the later branch short where the self-host
assembler makes it long (#11001). Compiled programs are not affected. The fix
is gas's stretch rule on `x86_relax_settle`'s model, which then replaces the
loop.

## Next

#11001; then on the stage-2 profile, `ssa_lift.lift_impl` and `str_eq`.
