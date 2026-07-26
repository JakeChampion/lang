# Integer semantics

Status: policy doc.

Fern's integer types (`u8`, `i32`/`u32`, `i64`/`u64`, and the
target-width `usize`) have **fully portable, never-trapping**
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

## Saturating arithmetic (opt-in)

`+|`, `-|`, `*|`, and `<<|` are the **saturating** counterparts of `+`,
`-`, `*`, and `<<`: instead of wrapping, they clamp to the operand
type's `[MIN, MAX]`. They are additive surface — the wrapping default
above is unchanged, and a program that never writes `|` after an
arithmetic operator behaves exactly as before.

```
2147483647 +| 1  == 2147483647       // i32 clamps high
(-2147483648) -| 1 == -2147483648    // i32 clamps low
100000 *| 100000 == 2147483647
250u8 +| 10u8    == 255              // clamps at the u8 max
10u8 -| 250u8    == 0                // unsigned clamps low at 0
1 <<| 31         == 2147483647       // i32 shift clamps high
(-2) <<| 31      == -2147483648      // ... and low
200u8 <<| 1      == 255
```

The operators sit in the existing arithmetic tiers: `+|` / `-|` bind
like `+` / `-`, `*|` binds like `*`, `<<|` binds like `<<`. They are
**integer-only** — there is no string-concat form (unlike `+`), no
float form (floats already saturate to ±Inf), and no composite
operator overload. `usize` is
rejected (E009) because its clamp bounds are target-width-dependent and
so aren't expressible in the target-agnostic IR; cast to a fixed-width
integer first.

Saturation never traps, so the no-exceptions contract above still
holds.

### How it's implemented

There is no saturating IR opcode. Both the native lowering
(`internal/ir.(*builder).satBinary`) and the self-host one
(`irlower.lower_sat_binary`) expand to a clamp over ordinary IR ops, so
every backend gets it for free. The tests are formulated as *pre*-checks
against the type's MIN / MAX rather than as post-hoc overflow-flag
reconstruction, which makes one shape work at every width — including
sub-i32 `u8`, whose wrap mask never has to run because a saturated
result is in range by construction:

```
signed   a +| b →  b > 0 && a > MAX - b ? MAX
                 : b < 0 && a < MIN - b ? MIN : a + b
unsigned a -| b →  a < b ? 0 : a - b
unsigned a *| b →  a != 0 && b > MAX / a ? MAX : a * b
```

Signed `*|` is the one shape a pre-check can't express cheaply (four
sign quadrants), so it post-checks the *wrapped* product with a
division: `a != 0 && (s / a != b || (a == -1 && b == MIN))`. The second
term is needed precisely because division is total here — `MIN / -1`
yields `MIN`, so the round-trip spuriously agrees on exactly that pair.

`<<|` post-checks the same way, and for a related reason: its
negative-side pre-check bound would be `ceil(MIN / 2^c)`, and an
arithmetic shift only yields the floor —
`-1 <<| 31` must clamp to MIN, yet `MIN >> 31` is `-1`, so
`a < MIN >> c` would wrongly report no overflow.

```
a <<| b →  s := a << b; (s >> b) == a ? s
                        : signed ? (a < 0 ? MIN : MAX) : MAX
```

The count is masked exactly as `<<` masks it (`& 31` / `& 63`), so
`3 <<| 32 == 3`, and the shift back uses the same masked count. `>>`
is arithmetic for signed operands and logical for unsigned, which is
what makes the round-trip value-preserving in each signedness. Sub-i32
`u8` is the one width where the shifted value has to be masked back
into its 8 bits before the round-trip runs, since sub-i32 arithmetic
otherwise stays in a 32-bit lane.

### Known limitations

`+|` / `-|` / `*|` / `<<|` are not allowed inside a `const` initializer. Const
folding runs *before* the checker, so no operand width — and therefore
no clamp bound — is known at that point; the compiler reports
``operator `+|` not allowed in integer constant expressions`` rather
than guessing one.

The remaining overflow disciplines from #5542 are available as
**library** surface rather than operators: `std/i32`, `std/i64`,
`std/u32` and `std/u64` each carry `checked_add` / `checked_sub` /
`checked_mul` / `checked_div` / `checked_rem` / `checked_pow` /
`checked_shl` / `checked_shr` (returning `Option[T]`) and
`overflowing_add` / `overflowing_sub` / `overflowing_mul` /
`overflowing_div` / `overflowing_rem` (returning `(T, boolean)`),
alongside `saturating_*` method forms. There is no operator spelling
for the checked disciplines, and no aborting-on-overflow build mode —
the never-trapping contract at the top of this document still holds
unconditionally.

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
