# 2026-10-07 — a join reads the old record only on its own edge

`ssaunits.named_after`, which decides whether a record is read again after
one of its fields is taken at its read.

## The shape

```fern
while (...) {
  if (line.len() == 2) {
    a = bump(a);
  } else if (line != "skip") {
    a = A { ...a, xs: grow(a.xs, line) };
  }
}
```

`grow` takes its buffer `own` and is spliced into its one caller. The read
of `a.xs` may take the field when `a` is read after it only through its
other fields. The walk that answers that went forward from the read block
and, at the join after the `if` chain, found a phi taking `a` from the arm
that skips the update. It counted that as a read of `a`, whichever edge the
walk had arrived by. It had arrived from the updating arm, whose edge hands
the join the new record, so the old one was never read again on that path.

Refused, the field stayed the record's: the spliced loop's first append
found a second count on it and copied the whole buffer, once per call.

## The rule

A phi reads an operand only on the edge that operand comes in by. The walk
now goes edge by edge: entering a block from `pred`, a phi there counts as a
read when its operand for `pred` is the record. Every other instruction
counts as before, as does the stop at the record's own definition.

## Where it was

The in-process x86-64 assembler: `x86_gas_assemble_pass_prepared` updates
`.rodata` through `X86Asm { ...a, rodata: x86_gas_ascii(a.rodata, line) }`
in one arm of its directive chain, and `x86_gas_ascii` is spliced. Each
`.ascii` line copied the whole `.rodata` built so far.

Found with gdb, not with a probe. A probe printing the cliff counter inside
the pass changed liveness, and the copies vanished from the probed build
(`docs/LOCAL-DEV-LOOP.md` warns of this); a `-g` build compiled the pass
differently too and showed none. Breaking on the two `incq
__fern_arr_push_shared(%rip)` sites of the runtime's push slow path in the
stripped production binary, where the helper has set up `%rbp`, the
caller's return address is at `8(%rbp)` and the length copied in `%r14`.
One return address took 1,821 of the copies; its instruction sequence, found
in the `-emit asm` listing, sits in the spliced `.ascii` loop.

## Measured

x86-64, each side's compiler built by itself:

| | main | this change |
|---|--:|--:|
| cliff bytes compiling `checker.fern` | 343,158,088 | 75,047,896 |
| cliff count compiling `checker.fern` | 242,066 | 238,346 |
| cliff bytes compiling the compiler | 46,561,817,160 | 456,311,624 |
| wall time compiling the compiler (mean of 3) | 31.6 s | 27.2 s |

The stage 3 the new compiler builds rebuilds itself byte for byte.

`TestSelfHostRecordFieldTakeAtArmJoin` pins the shape: `joined` allocates
12 times over 3,000 rounds where main allocated over 1,000, and `kept`,
whose skipping arm hands the join the record the field was read from, keeps
the field the record's.
