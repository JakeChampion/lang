# 2026-10-03 — the array-push grow copy moves 64 bytes a round

`asm_ir`'s `__fern_arr_push` and the self-host assembler's VEX forms,
x86-64 only. Refs #8171.

## The shape

The SSA emitters inline a push that has room, so `__fern_arr_push` is
called only to grow: 5.84 M times to give an empty array its first four
slots and 3.42 M times to double a full one, on the stage-2 compile of
`checker.fern`. Each grow copies the old elements into the new box. The
copy moved four elements a round through two 16-byte SSE moves, ten
instructions a round, and that loop ran 31.4 M rounds: about 330 M Ir,
1.3% of the compile.

## What changed

The copy now moves eight elements a round through two 32-byte AVX2 moves.
The remaining count is biased by one round up front, so each round ends
in a `subq; jge` and costs eight instructions. At most one round of four
through the 16-byte moves follows, then the one-at-a-time tail.
`vzeroupper` follows the ymm loop. AVX2 is inside the x86-64-v3 baseline,
so nothing dispatches.

The self-host assembler gains the store direction of `vmovdqu`
(VEX.256.F3.0F 7F /r), which no kernel had needed before.

`__fern_str_concat`'s copies are `rep movsb`, which callgrind counts once
per byte. Replacing them with size-classed moves was a 10% wall-clock
loss on x86-64, so they stay.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at 31695b8d and from this change on it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 24.577 G | 24.412 G (−0.67%) |
| `__fern_arr_push`, self Ir | 577 M | 412 M |
| stage 2, wall clock (mean of six alternating runs) | 5.20 s | 5.16 s |

The wall-clock difference is within run-to-run noise, but it points the
same way as the instruction count. Emitted bytes change on 257 of the
1,965 `selfhost-emit-hashes` rows, all x86-64.

## Witnessed

`TestSelfHostArrPushCopy` is new. A program grows arrays to every length
from 1 to 70, so each doubling copies 4, 8, 16, 32 and 64 elements. It
pushes onto a shared copy at every length, which copies every remainder of
eight, and reads back every element. The listing's `__fern_arr_push` must
hold the ymm store and `vzeroupper`. `TestSelfHostX86GasVexGroundTruth`
pins the store form's bytes against GNU `as` in four register mixes.
