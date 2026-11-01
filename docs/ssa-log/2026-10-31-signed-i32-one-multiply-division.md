# Measured 2026-10-31: a signed i32 constant divisor takes one 64-bit multiply

**What changed.** This applies to a signed i32 division or remainder by a
constant that is not 0, ±1 or a power of two. It used the Hacker's Delight
reciprocal at 32 bits. That reciprocal needs 33 bits for 38% of the divisors
from 3 to 1000. The code then added the dividend back after the multiply.

The x86-64 and arm64 backends now use the form from Daniel Lemire's post on
signed 32-bit division (2026). The dividend is sign-extended to 64 bits and
multiplied by m = sign(d) * (floor(2^s / |d|) + 1), with
s = 31 + ceil(log2 |d|). The quotient is the product shifted right by s,
plus the sign bit of the product.

|m| is less than 2^32 and |x| is at most 2^31, so the product fits in 64
bits. The bound is Theorem 1 of Lemire, Bartlett and Kaser, "Integer
division by constants: optimal bounds" (Heliyon, 2021).

The derivation is `ir.derive_magic_s32`. `ir.derive_magic_s` is now 64-bit
only, because no backend asks it for 32 bits.

| backend | before, on the dependency chain | after |
|---|---|---|
| x86-64 | `imull`, `addl`, `sarl`, `shrl`, `addl` | `movslq`, `imulq`, `sarq`, `addl` |
| arm64 | `smulh`, `add`, `asr`, `add` | `mul`, `asr`, `add` |

On x86-64 the rounding term (`shrl $31` of the dividend, or `shrq $63` of
its negation when d < 0) is made beside the multiply. On arm64 the dividend
is already held sign-extended, so no `sxtw` is necessary. The constant also
needs 32 bits instead of 64. The wasm backend keeps `i32.div_s`, because
the engine lowers it.

**Measured** on a 2.8 GHz Xeon (x86-64), stage0 `stage0-20261031-59ba13e`
against this change, 4e8 iterations, best of 3 runs:

| loop | before | after |
|---|---|---|
| `h = h / 7 + k`, each division depends on the last | 1.45 s | 1.17 s |
| `t += (i - c) / 7 + (i - c) / 365`, independent divisions | 1.06 s | 0.90 s |

The dependent loop goes from about 10.4 to 8.2 cycles for each iteration.
This agrees with the 10 to 8 cycles that Lemire reports on Emerald Rapids.

**Gates.** `TestSelfHostConstDivisorShapesX86_64` pins the x86-64 shapes,
and `TestSelfHostConstDivisorArm64` pins the arm64 shapes. Both run the
`i32-one-multiply` oracle case, which compares 17 constant divisors against
the runtime division on 17 dividends from INT_MIN to INT_MAX. A local sweep
of 1991 divisors (every non-power-of-two from ±3 to ±1000, and the extremes)
on 150 dividends gave no mismatch on x86-64 or on arm64 under qemu. A
multiplier rounded down instead of up failed it on both.
