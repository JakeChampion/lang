# The register budget is not x86-64's gap, and the two SSA backends agree

`2026-09-17-spill-the-longer-interval.md` closed by saying x86-64's
remaining distance from its stack machine was the register budget: six
caller-saved and five callee-saved against arm64's eight and ten. That is
wrong, and the experiment that says so is one line.

## The experiment

arm64's pool cut to x86-64's budget, whole compiler, `-emit asm`:

| arm64 pool | instructions |
|---|---|
| 8 caller-saved + 10 callee-saved | 3,815,776 |
| 6 caller-saved + 5 callee-saved | 3,881,399 |

Seven registers are worth 65,623 instructions, **1.7%**. At x86-64's budget
arm64 still emits 0.96x its stack machine's output. If the budget were the
cause of x86-64's 1.25x, this is where it would have shown.

## What is actually going on

The two register backends agree to within half a percent. It is the two
stack machines that differ:

| | flat | ssa | ssa over flat |
|---|---|---|---|
| arm64 | 4,043,922 | 3,815,776 | 0.94x |
| x86-64 | 3,043,396 | 3,798,189 | 1.25x |

3,815,776 against 3,798,189 — the register path emits essentially the same
number of instructions for the same compiler on both ISAs, which is what a
target-independent allocator over a shared lift should do.

The stack machines are a million apart, and the reason is one instruction:
x86-64's pushes a frame slot directly, 422,141 times in this build, where
arm64's has no such form and needs an address and a store. `pushq
-N(%rbp)` is why the x86-64 stack machine is compact, not why the register
path is loose.

## What that means for the target

"Smaller output than the stack machine" is a harder bar on x86-64 than on
arm64, because the bar itself is lower there — 3.04M against 4.04M for the
same program. Closing it means the register path getting BELOW a stack
machine that stages an operand in one instruction, not catching up to a
peer that is behind.

So the remaining work on x86-64 is not "find more registers". Nothing in
`docs/ssa-log/` has yet found where those 750,000 instructions are in a
form that suggests a fix; the movq class table in
`2026-09-17-the-call-result-move-is-the-x86-gap.md` is the place to start
again, read with this entry in mind.
