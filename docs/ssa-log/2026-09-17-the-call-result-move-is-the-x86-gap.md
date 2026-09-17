# Where the x86-64 gap is now: the call result's move to its home

No slice landed from this either. It is the measurement that says what the
next one should be, taken on main after the call-argument staging of #9613.

## The two targets, whole compiler, `-emit asm`

| | flat | ssa | over flat |
|---|---|---|---|
| arm64 | 4,039,528 | 4,112,320 | +1.8% |
| x86-64 | 3,040,139 | 3,992,473 | +31.3% |

arm64 is nearly there. x86-64 is 952,334 instructions adrift, and almost
all of it is one instruction:

| `movq` class | flat | ssa | excess |
|---|---|---|---|
| register to register | 22,637 | 622,466 | +599,829 |
| frame to register | 372,233 | 699,395 | +327,162 |
| register to frame | 447,953 | 650,362 | +202,409 |

## Two thirds of the register-to-register traffic is one shape

405,771 of those moves take `%rax` to a value's home register. Attributing
them by what precedes them, over the 321,320 calls the compiler emits:

| | count |
|---|---|
| directly after a `call` | 10,352 |
| after a `call` and its caller-side stack pop | 265,621 |
| anywhere else | 129,798 |

So 275,973 of them — 68% of the shape, and 6.9% of the whole x86-64 build
— are a call's result being moved out of the register the ABI returns it
in, into the register the allocator gave it. arm64 carries the same shape
at a similar count.

## What it takes to remove them

The result has to be allocatable in `%rax` (x0 on arm64), so the store
after the call finds the value already home and emits nothing —
`ssa_store` already returns unchanged when the home is the register it was
handed.

That is not a one-line change, which is why this is a measurement and not
a slice. `%rax` is scratch, not a pool register: `ssa_reg` maps the pool to
`%rsi`, `%rdi`, `%r8`-`%r11` and the callee-saved set, and the emitter arms
use `%rax`, `%rcx` and `%rdx` as working registers, three at once in the
slice kernel. Putting `%rax` in the pool means finding a third scratch
outside it, which means taking one out — the pool stays the size it is, and
every arm that names `%rax` has to be read.

Membership alone would only place a call result in `%rax` by luck. The
allocator also needs to prefer the ABI's result register for a value
defined by a call, which is a hint `regalloc_linear` does not have today.

## What is NOT the gap

- Arithmetic. There are 18,737 register-form binaries in the whole
  compiler; routing them into their result's home was measured at +298
  instructions, i.e. nothing (`2026-09-17-selfhost-ssa-backend-whole-compiler.md`).
- The optimiser. 1.2% of output for 22% of compile time, and the obvious
  cheapening of it does not work
  (`2026-09-17-the-optimiser-costs-more-than-it-saves.md`).
- Operand staging, which #9613 already took: 414,042 instructions on
  x86-64 and 279,002 on arm64.
