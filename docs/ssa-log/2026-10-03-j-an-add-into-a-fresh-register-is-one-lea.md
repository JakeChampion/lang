# 2026-10-03 — an add into a fresh register is one lea

`asm_ir`'s `ssa_bin_in_place`, x86-64 only. Refs #8171.

## The shape

x86's `add` and `sub` overwrite their left operand. When the allocator put
a sum in a register its left operand did not hold, the emitter copied the
operand there first: `movq %ra, %rd; addq %rb, %rd`. `util.hash_bucket`'s
loop shows why it happens. The accumulator is read again after the loop,
so its interval spans the loop body and the product `a * 31` cannot take
its register. Each round then copies the product into the accumulator
before adding the byte.

A per-instruction profile of an earlier stage-2 build compiling
`checker.fern` counts those copies at 63 M ahead of a register add and
26 M ahead of an add or subtract of a constant.

## What changed

`ssa_lea_operand` gives the address such an op computes, and the emitter
writes one `leaq (%ra,%rb), %rd` or `leaq k(%ra), %rd` in its place. A
subtract of a constant is a lea of its negation. The least 32-bit
immediate has no negation that fits a displacement, so subtracting it keeps
the two-address form. Nothing reads the flags an add leaves: branch fusion
reads flags only from a compare it emits itself.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at 31695b8d and from this change applied to it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 24.577 G | 24.492 G (−0.35%) |
| `util.hash_bucket`, self Ir | 487 M | 440 M |

Emitted bytes change on 348 of the 1,965 `selfhost-emit-hashes` rows, all
x86-64. Both sides refuse the same 252.

## Witnessed

`TestSelfHostLeaAdd` is new. `mix` is `hash_bucket`'s loop and `edges`
adds and subtracts registers and constants, including the least immediate.
The listing of either function may hold no copy followed by an add or
subtract into the copy's register, and must hold a lea. Both must compute
the values Go computes on every host target. Main fails the shape half.
Also run: `TestSelfHostSSA*`, `TestSelfHostBoundsElide*`,
`TestSelfHostArrPush*` and `TestSelfHostSemanticProduction`.
