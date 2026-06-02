# Integer semantics

Status: policy doc.

Fern's integer types (`i8`/`u8`, `i16`/`u16`, `i32`/`u32`, `i64`/`u64`,
and the target-width `usize`) have **fully portable, never-trapping**
semantics: every integer operation produces the same result on every
backend (`interp`, `arm64`, `arm64-darwin`, `x86_64`, `wasm`) for the
same inputs, and none of them can crash or trap the process.

This is the deliberate counterpart to `docs/FLOAT-SEMANTICS.md`: where
the float edges are under-specified, the integer edges are pinned.

## Wrapping arithmetic

`+`, `-`, `*`, and `<<` **wrap** (two's-complement modular arithmetic)
at the operand's width. There is no overflow trap.

```
255u8 + 1   == 0          // wraps at 8 bits
127i8 + 1   == -128
2147483647 + 1 == -2147483648   // i32
100000 * 100000 == 1410065408   // i32, mod 2^32
```

Shift counts are masked to the operand width: a 32-bit (or sub-i32)
shift uses `count & 31`, a 64-bit shift `count & 63`. Right shift `>>`
is arithmetic (sign-propagating) for signed types and logical
(zero-filling) for unsigned types.

```
(0 - 8) >> 33 == -4       // count 33 & 31 == 1; arithmetic shift
4294967288u32 >> 2 == 1073741822   // logical
```

## Division and remainder never trap

`/` and `%` are **total** — defined for every pair of operands,
including a zero divisor and the signed `INT_MIN / -1` overflow. None
of them faults (no `SIGFPE`, no wasm trap):

| expression            | result      |
|-----------------------|-------------|
| `x / 0`               | `0`         |
| `x % 0`               | `x`         |
| `INT_MIN / -1`        | `INT_MIN`   |
| `INT_MIN % -1`        | `0`         |
| everything else       | the usual truncated quotient / remainder |

```
10 / 0   == 0
10 % 0   == 10
(-2147483648) / -1 == -2147483648    // i32 wrap, no overflow trap
(-2147483648) % -1 == 0
```

This is the "well-defined, no exceptions" contract: a Fern program
never aborts on a division edge, so callers don't need to guard
divisors. If you want a different policy (e.g. a sentinel for division
by zero), test the divisor at the source level first.

### How it's implemented

arm64's `sdiv` / `udiv` already give exactly this (zero divisor → 0,
`INT_MIN / -1` wraps, no trap), so it's the reference. The other
backends match it:

- **interp** returns `0` / the dividend for a zero divisor; Go's `/`
  and `%` already define `INT_MIN / -1`.
- **x86-64** branch-guards `idiv` / `div` (which raise `#DE` on both a
  zero divisor and the `INT_MIN / -1` overflow) so the hardware op
  only runs on operands it can't fault on.
- **wasm** routes `div` / `rem` through guarded runtime helpers
  (`__fern_idiv_*` / `__fern_irem_*`) that sanitise the divisor before
  the trapping instruction and `select` the contract result.

## Conversions

Integer↔integer casts truncate (narrowing) or extend (widening) per
the destination's signedness; see `docs/FLOAT-SEMANTICS.md` for the
saturating float↔integer conversion contract. All conversions are
portable across backends.

## Testing

The cross-backend differential harness
`internal/e2e/numeric_property_test.go` generates random programs over
the whole integer matrix (every width × signedness × operator,
including the division edges above) and asserts the interpreter and
every codegen backend agree. `TestNumericProperty_Regressions` pins
the specific edge programs; `FuzzNumericProperty` is the deeper search.
