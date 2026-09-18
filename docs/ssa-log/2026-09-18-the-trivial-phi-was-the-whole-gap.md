# Folding trivial phis: a million instructions on both targets

The production lift emits a placeholder phi per live local at every merge
and every loop header. Most locals are untouched on the paths that meet
there, so most of those phis have one distinct operand and are that
operand. Nothing removed them, and they reached the allocator as values,
where the only choices are a register or a frame slot.

Folding them first is worth more than every emitter change so far put
together.

## The numbers

Whole compiler, `-emit asm`, against the same main:

| | flat | ssa before | ssa after | after, over flat |
|---|---|---|---|---|
| arm64 | 4,043,922 | 3,815,776 | **2,805,064** | **0.69x** |
| x86-64 | 3,043,396 | 3,798,189 | **2,812,651** | **0.92x** |

1,010,712 instructions off arm64 and 985,538 off x86-64, a quarter of each
build. **x86-64 is now below its stack machine too**, which it has never
been: the register path was 1.25x before this and is 0.92x after.

The linked compiler follows: 12,796,289 bytes against the stack machine's
18,030,593 on arm64-darwin (0.71x), and 11,629,936 against 14,504,088 on
x86-64-linux (0.80x).

Compile time improves as well — `checker.fern` through the register path
is 7,154 ms against 7,466 ms — because fewer values is less to allocate
and less text for the in-process assembler to parse.

## Why it is this large

The allocator can choose between spilling a value and not spilling it. It
cannot choose not to have the value. A trivial phi is a value the program
does not contain, and the lift was making one per local per merge, so the
interval set was several times the size of the real one. Every spilled
trivial phi cost a store where it was defined and a load at every use,
which is exactly the frame traffic
`2026-09-17-the-call-result-move-is-the-x86-gap.md` measured and could not
explain: 627,750 loads into the working register and 604,968 stores out of
it, against the stack machine's 237,216 and 404,553.

It also explains why the register budget barely mattered
(`2026-09-17-the-register-budget-is-not-the-x86-gap.md`, seven registers
worth 1.7%): with that many surplus values, no budget was going to be
enough.

## The rule

A phi is trivial when its operands, ignoring references to itself, are all
one value. The self-reference exclusion is what catches the loop-header
case, where the phi reads `[init, itself]` because the body leaves the
local alone. Folding runs to a fixpoint, since folding one phi can make
the next trivial.

## Checks

- The backend gate on arm64-darwin.
- 520 conformance programs on arm64-linux natively and 522 on x86-64-linux
  under qemu, each on both backends: same exit code and same output on
  every one.
- The fixpoint: stage one and stage two each emit assembly for `fern.fern`
  itself, byte-identical at 90,785,217 bytes.
- The complexity ratchet and the source lint.

## What this opens

x86-64 now meets the size condition its default flip was held to. Whether
to take that flip is a decision, not a measurement.
