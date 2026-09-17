# Spill the longer interval, not the one being looked at

`regalloc_linear` is a linear scan, and it was missing the half of the
algorithm that decides WHICH value goes to the frame. With no register
free it spilled the value it was looking at, so one long-lived value could
hold a register across a whole function while a hundred short ones went to
the frame.

It now takes the register from the active interval that ends latest, when
that end is later than the current value's, and spills that one instead.
The two pools stay apart: a value live across a call may only sit in a
callee-saved register, so the search is confined to the matching class.

## The numbers

Whole compiler, `-emit asm`, against the same main:

| | flat | ssa before | ssa after | after, over flat |
|---|---|---|---|---|
| arm64 | 4,051,791 | 3,913,741 | 3,815,776 | **-5.8%** |
| x86-64 | 3,048,987 | 3,795,946 | 3,798,189 | +24.6% |

97,965 instructions off arm64, 2.5% of its build, taking the register path
from 3.4% to 5.8% below the stack machine. x86-64 moves by 2,243, which is
0.06% and in the noise.

The split is the register budget: arm64 allocates over 8 caller-saved and
10 callee-saved, x86-64 over 6 and 5. With eleven registers and the lift's
phi-per-local-per-merge value counts, nearly everything spills either way
and there is little for a better choice to do. Compile time does not move
either: `checker.fern` is 7,466 ms against 7,430 ms, best of three.

## Where the remaining x86-64 gap is

Not instruction selection. Of the 746,959 instructions x86-64 emits over
the stack machine, the frame traffic is over half: 627,750 loads into the
working register and 604,968 stores out of it, against the stack machine's
237,216 and 404,553. Almost every value is spilled.

Two things that are NOT the answer, both measured rather than assumed:

- **Folding a spilled operand into the instruction.** x86-64 can take a
  memory operand, and neither backend does. Counting the sites: 51,705
  frame loads reach `%rcx`, and only 1,197 of them immediately feed a
  binary that could have folded them, 1,111 of those a `cmpq`. The right
  operand of a binary is nearly always register-resident, so there is
  nothing to fold.
- **A better spill choice**, which is this entry: worth 0.06% there.

What is left is the register budget itself, which is not something the
allocator can decide.
