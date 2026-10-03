# 2026-10-02 — an immediate multiply reads its operand in place

`asm_ir.ssa_bin_in_place`, x86-64 only. Refs #8171.

## The shape

A multiply by a constant copied its left operand into the destination and then
multiplied there, through imul's three-operand form with the destination named
twice:

```
movq %rsi, %r9
imulq $1000003, %r9, %r9
```

In a hash loop the operand is the running hash, which stays live across the
loop in its linear-scan interval, so the product cannot take its register and
the copy runs once per byte.

## What changed

A multiply whose right operand is an immediate and whose left operand lives in
a register reads it where it lives: `imulq $1000003, %rsi, %r9`. A spilled left
operand still goes through the load. arm64's `mul` already names three
registers; there the constant is loaded from the literal pool inside the loop,
which is a separate change.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container, stage 2
built from main at 7ddc44f9 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 26.832 G | 26.736 G (−0.36%) |
| the five `*_bucket` hashes, self Ir | 1.403 G | 1.308 G |

Emitted bytes change: 116 of the 1,965 rows of the `selfhost-emit-hashes`
sweep differ from main, with the same 252 refused.

## Witnessed

`TestSelfHostOptimisationShapes` (with `imul_reads_operand_in_place`, whose
forbidden copy main emits), `TestSelfHostX86*`, `TestSelfHostSSA*`,
`TestSelfHostI32OverflowIR`, `TestFernFixturesSelfHostX86_64`
(`FERN_SELFHOST_FIXTURES=1`, 557 passing, no skips), the stage-2 build and its
compile of `checker.fern`, the lint ratchet and `make fmt-check`.
