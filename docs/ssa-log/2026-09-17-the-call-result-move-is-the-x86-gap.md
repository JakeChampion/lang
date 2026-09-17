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

That is not a one-line change, and reading the arms it would touch says
the obstacle is bigger than "find another scratch register".

`%rax` is scratch, not a pool register: `ssa_reg` maps the pool to `%rsi`,
`%rdi`, `%r8`-`%r11` and the callee-saved set, and arm64's to `x9`-`x15`
and `x19`-`x28`. The ABI result register is excluded from both because
every arm uses it as its WORKING register, not merely as a spare: the
binary arm forces its destination to `%rax` / `x0` and loads its left
operand there, the unary arm does the same, and the memory and call
kernels name it throughout. A value homed in that register would be
destroyed by the next arithmetic instruction of any kind, so putting it in
the pool alone produces a miscompile rather than a saving.

The slice is therefore to stop using the ABI result register as the
emitters' working register — move the arms onto a scratch that is not it,
on both ISAs — and only then put it in the pool. On arm64 the choice is
constrained: `x8` carries the syscall number, and `darwinize` keys its
Mach-O rewrite off the literal `ldr x8, [sp], #16`.

Membership alone would also only place a call result there by luck. The
allocator needs to prefer the ABI's result register for a value a call
defines, which is a hint `regalloc_linear` does not have today.

## How big step one is, and the trap in counting it

Counted over the 67 `ssa_*` / `emit_ssa_*` functions, which are 912 lines:

| | sites |
|---|---|
| arm64, `x0`-`x3` | 166 |
| arm64, `w0`-`w3` | 26 |
| x86-64, `%rax` | 106 |
| x86-64, `%eax` | 26 |
| x86-64, `%al` | 14 |

**A register's 32-bit and 8-bit names do not contain its 64-bit name**, so
a search for `x0` misses 25 `w0` sites and a search for `%rax` misses 40
spelled `%eax` or `%al` — a quarter of the work on each ISA, invisible to
the obvious search. A missed site is a silent miscompile rather than a
build error, and on arm64 doubly so: a `w` write zeroes the upper half of
whatever register it names, so the wrong-register write destroys more than
it writes.

The arm64 float names (`d0`, `d1`, `s0`, 46 sites) are separate registers
rather than views, but `fmov` ties them to the integer ones, so the pairs
move together.

Scope step one from all three name widths, not from the 64-bit one.

## What is NOT the gap

- Arithmetic. There are 18,737 register-form binaries in the whole
  compiler; routing them into their result's home was measured at +298
  instructions, i.e. nothing (`2026-09-17-selfhost-ssa-backend-whole-compiler.md`).
- The optimiser. 1.2% of output for 22% of compile time, and the obvious
  cheapening of it does not work
  (`2026-09-17-the-optimiser-costs-more-than-it-saves.md`).
- Operand staging, which #9613 already took: 414,042 instructions on
  x86-64 and 279,002 on arm64.
