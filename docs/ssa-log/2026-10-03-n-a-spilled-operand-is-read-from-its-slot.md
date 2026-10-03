# 2026-10-03 — a spilled operand is read from its slot

`asm_ir`'s `ssa_bin_in_place`, `ssa_fused_test` and `ssa_rhs`, x86-64
only. Refs #8171.

## The shape

Loads and stores to frame slots are 2.47 G of the 24.70 G instructions on
the stage-2 compile of `checker.fern`. Half of `ssa_lift.lift_impl`'s
1.15 G is that traffic: around 100 values are live across the calls in its
loop, and five callee-saved registers can hold only five of them. Of
those loads, 164 M fed a single compare or ALU op straight away. The
emitter loaded a spilled right operand into `%rcx`, or a spilled left
operand of a branch's compare into `%r11`, and the next instruction read
that scratch register and nothing else.

## What changed

x86 ALU ops and compares can take one memory operand. A spilled right
operand of an add, subtract, multiply, and, or, xor or compare is now read
from its frame slot (`addq -48(%rbp), %rax`). A branch's compare reads
a spilled left operand from its slot when the right one is an immediate
or a register (`cmpq $5, -48(%rbp)`). When both operands are spilled, the
left one still goes through `%r11`. `ssa_rhs` names the operand as the
instruction reads it: the immediate, the register home, or the slot. It
replaces `ssa_read_rhs` and the scratch-filling form of `ssa_rhs`.

arm64 has no ALU form with a memory operand and is unchanged.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at 624126a6 and from this change applied to it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 24.667 G | 24.448 G (−0.88%) |

`std/crypto`'s sha256, sha512, sm3, sha1 and md5 over the same 4 MiB
buffer, one program, digests unchanged: 1.720 G to 1.546 G instructions
(−10.1%).

Emitted bytes change on 271 of the 1,968 `selfhost-emit-hashes` rows, all
x86-64.

## What was tried first

The hottest loop in `lift_impl` keeps its counter in a slot. The allocator
gave the counter a register and then took it back for a temporary three
positions long, because the eviction compares reads per position covered.
Keeping that comparison for choosing the victim, but evicting only when
the victim's total weight is lower than the newcomer's, took stage 2 to
24.365 G (−1.22%). The digests went to 2.184 G (+27%). That is the case
#10615 fixed, so the rule stayed as it is. A value can only ever have one
home here, so evicting one gives up its register for its whole interval.
Splitting an interval around the temporary would give the counter and the
digests what each needs.

## Witnessed

`TestSelfHostSpilledOperandInPlace` is new. A function keeps eight values
live across the calls in its loop. The listing must hold an ALU op reading
a slot, a compare of a slot against an immediate, and a compare between a
slot and a register. No spilled operand may be loaded into `%rcx`, and the
program must exit 42 on every host target. Also run:
`TestSelfHostLeaAdd`, `TestSelfHostLeafFrame` and
`TestSelfHostImmutabilityGateX86_64`.
