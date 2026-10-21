# Integer semantics

Status: [policy] decided integer semantics.

Fern's integer types (`u8`, `i32`/`u32`, `i64`/`u64`, and the
target-width `usize`) have **fully portable, never-trapping**
semantics: every integer operation produces the same result on every
backend (`interp`, `arm64`, `arm64-darwin`, `x86_64`, `wasm`) for the
same inputs, and none of them can crash or trap the process. The one
type whose width is the target's is `usize`: it is the same on every
backend of the same pointer width, and its rules are below.

This is the deliberate counterpart to `docs/FLOAT-SEMANTICS.md`: where
the float edges are under-specified, the integer edges are pinned.

## Wrapping arithmetic

`+`, `-`, `*`, and `<<` **wrap** (two's-complement modular arithmetic)
at the operand's width. There is no overflow trap. Unary `-x` is `0 - x`,
so on an unsigned type it wraps too.

```
255u8 + 1   == 0          // wraps at 8 bits
2147483647 + 1 == -2147483648   // i32
100000 * 100000 == 1410065408   // i32, mod 2^32
-(5 as u32) == 4294967291 // u32, mod 2^32
```

Shift counts are masked to the operand width: a 32-bit (or sub-i32)
shift uses `count & 31`, a 64-bit shift `count & 63`. Right shift `>>`
is arithmetic (sign-propagating) for signed types and logical
(zero-filling) for unsigned types.

```
(0 - 8) >> 33 == -4       // count 33 & 31 == 1; arithmetic shift
4294967288u32 >> 2 == 1073741822   // logical
```

## `usize` is the pointer width

`usize` is an address-sized unsigned integer: **64 bits on x86-64 and
arm64, 32 bits on wasm32**. Its arithmetic follows every rule in this
document at that width — `+`, `-`, `*` and `<<` wrap modulo
2^64 or 2^32, the orderings, `>>`, `/` and `%` read it unsigned, and a
shift count masks to 63 or 31. It has no negative values: a borrow below
zero wraps to the type's maximum.

```
(0 as usize) - (1 as usize)   == 18446744073709551615   // 64-bit
                              == 4294967295             // wasm32
(4294967295 as usize) + 1     == 4294967296             // 64-bit
                              == 0                      // wasm32
```

A `usize` literal can be anything up to `u64::MAX`; on wasm32 one past
`u32::MAX` does not fit and the wasm backend refuses it. A cast into
`usize` extends the source to 64 bits by its own signedness and wraps
that to the pointer width.

The interpreters model the pointer width of the machine they run on, as
`target_arch()` answers with it: `internal/oracle/interp` and `fern
-interp` are 64-bit, and the playground's wasm32 build of the
interpreter is 32-bit.

The conformance cases `usize_wraps_at_64_bits` and
`usize_wraps_at_32_bits` pin both widths.

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
  only runs on operands it can't fault on. A literal divisor needs
  neither guard and takes an unguarded sequence instead — a shift for a
  power of two, otherwise the multiply-high reciprocal at either width —
  in the compiler.
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
(-1) <<| 31      == -2147483648      // exactly MIN: nothing lost
200u8 <<| 1      == 255
3 <<| 32         == 2147483647       // count >= width saturates ...
0 <<| 32         == 0                // ... any non-zero value
```

`<<|` clamps exactly where `<<?` (below) answers `None`: when a shift
count is negative or at least the width, or when the shift loses bits.
A shift loses bits when the shifted-out bits are not all copies of the
sign bit, which is the same as saying `a * 2^n` falls outside `[MIN,
MAX]`. Zero never loses bits, so `0 <<| n` is `0` for every count. The
count is not masked the way `<<` masks it: `3 << 32 == 3`, but
`3 <<| 32` saturates.

The operators sit in the existing arithmetic tiers: `+|` / `-|` bind
like `+` / `-`, `*|` binds like `*`, `<<|` binds like `<<`. They are
**integer-only** — there is no string-concat form (unlike `+`), no
float form (floats already saturate to ±Inf), and no composite
operator overload. `usize` is
rejected (E009): where a usize clamps depends on the target, so the
program names the width it means by casting to `u32` or `u64` first.

Saturation never traps, so the no-exceptions contract above still
holds.

### How it's implemented

There is no saturating IR opcode. The compiler's lowering
(`irtables.sat_binary_ops`) expands each operator to a clamp over
ordinary IR ops, so every backend gets it for free; the oracle
interpreter computes the same clamp directly (`satArith` in
`internal/oracle/interp`). The tests are formulated as *pre*-checks
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

`<<|` post-checks too, by shifting the wrapped result back. The test
it uses, `lost`, is the same predicate `<<?` answers `None` on:

```
lost(a, b) ⇔ b < 0 || b >= width || ((a << b) >> b) != a

a <<| b →  !lost(a, b) ? a << b
         : a == 0      ? 0
         : signed && a < 0 ? MIN : MAX
```

`>>` is arithmetic for signed operands and logical for unsigned, so the
round trip restores `a` exactly when the bits shifted out, and for a
signed operand the new sign bit, were all copies of `a`'s sign. Only an
out-of-range count can make `lost` true for `a == 0`, which is why the
clamp needs the zero arm. The round trip is still computed for an
out-of-range count, where `<<` masks it, but its answer is discarded,
and no backend traps on a large shift count. Sub-i32 `u8` is the one
width where the shifted value has to be masked back into its 8 bits
before the round trip runs, since sub-i32 arithmetic otherwise stays in
a 32-bit lane.

### Known limitations

`+|` / `-|` / `*|` / `<<|` are not allowed inside a `const` initializer. Const
folding runs *before* the checker, so the only width available is the one the
declaration writes down — an UNDECLARED const's literal stays polymorphic and
settles at its use site — and a clamp bound cannot be guessed. The compiler
reports ``operator `+|` not allowed in integer constant expressions``.

A declared const's width IS available, and the wrapping operators fold at it:
`const W: i32 = (2147483647 + 1) / 2` is −1073741824, the same value the
expression produces as code, and a shift count masks to the declared width
(`& 31` for i32) rather than to 63. An undeclared const still folds in int64,
so it can disagree with the same expression written at a narrower width.

## Checked arithmetic (opt-in)

`+?`, `-?`, `*?`, `/?`, `%?`, `<<?`, and `>>?` are the **checked**
counterparts of `+`, `-`, `*`, `/`, `%`, `<<`, and `>>`: they evaluate
to `Some(result)` when the exact result fits the operand type and `None`
on overflow, so overflow becomes a value the caller handles rather than
a silent wrap. `/?` and `%?` additionally return `None` on a zero
divisor and, for signed operands, on the one overflowing division
`MIN / -1`. `<<?` returns `None` when the shift loses bits, which is
when `a * 2^n` does not fit (the `lost` test under the saturating
operators above). Both shifts treat a count that is negative or at
least the width as outside their domain and answer `None`, the way `/?`
treats a zero divisor, so `0 <<? 32` is `None` even though `0 << n` is
`0` for every `n`. Also additive surface — a program that never writes
`?` after an arithmetic operator is unchanged.

```
2147483647 +? 1  == None          // i32 overflows high
40 +? 2          == Some(42)       // fits
250u8 +? 10u8    == None           // u8 overflows
10u8 -? 250u8    == None           // unsigned borrow
84 /? 0          == None           // zero divisor, no trap
(-2147483648) /? -1 == None        // signed MIN / -1 overflow
85 %? 43         == Some(42)
1 <<? 32         == None           // shift count >= width
255u8 <<? 1      == None           // the top bit is shifted out
127u8 <<? 1      == Some(254)
1073741824 <<? 2 == None           // 2^32 does not fit i32
1 <<? 31         == None           // 2^31 does not fit i32 either
(-1) <<? 31      == Some(-2147483648)
256 >>? 4        == Some(16)
```

A right shift cannot overflow: `a >> n` is `a` divided by `2^n` rounded
toward negative infinity, and that always fits the type. Discarding the
low bits is what `>>` is for, so `>>?` answers `None` only for an
out-of-range count. For the same reason there is no `>>|`: with nothing
to clamp, it would differ from `>>` only on an out-of-range count, and
`>>?` already reports that case.

They sit in the same arithmetic tiers as the wrapping and saturating
operators: `+?` / `-?` bind like `+` / `-`, `*?` / `/?` / `%?` bind
like `*` / `/` / `%`, `<<?` / `>>?` bind like `<<` / `>>`. They
are **integer-only** — no string or float form, no composite overload —
and `usize` is rejected (E009), as for the saturating operators: whether
a usize overflows depends on the target. The result composes with the postfix `?`
operator, so `(a +? b)?` propagates `None` out of an `Option`-returning
function. The trailing `?` never collides with the postfix try `?`: it
only follows an arithmetic operator, never a completed operand.

Checked arithmetic never traps — `None` is a value — so the no-exceptions
contract above still holds. `+?` / `-?` / `*?` / `<<?` / `>>?` are the
operator form of the `checked_add` / `checked_sub` / `checked_mul` /
`checked_shl` / `checked_shr` stdlib methods, the way `+|` / `-|` / `*|`
are the operator form of `saturating_*`.

### How it's implemented

There is no checked IR opcode. The Go oracle's checker desugars `a +? b`
(`buildCheckedLowered`, spliced in via `Binary.CheckedLowered`) to an
`Option`-yielding block-expr — `{ let l = a; let r = b; let s = a <op> b;
if (overflowed) { None } else { Some(s) } }` — so the oracle interpreter
evaluates it through the ordinary `Option` / `if` / wrapping-arithmetic
paths. The self-host compiler, whose IR-subset has no
block-expression node, emits the same shape directly in
`irtables.chk_binary_ops`: the overflow predicate is exactly the clamp
condition `sat_binary_ops` tests (`chk_overflow`), OR'd across the two
saturation directions, and the outcome is `op_opt_none` / `op_opt_make`
instead of the clamp. The `Option` rides a default pointer slot (i32 on
wasm, 64-bit on the register backends).

The overflow predicate reads back the wrapped result rather than
comparing against literals, so the unsigned MAX — unrepresentable as a
signed literal — never has to appear:

```
unsigned a +? b  overflowed ⇔ s < a                       (carry out)
unsigned a *? b  overflowed ⇔ a != 0 && s / a != b
signed   a +? b  overflowed ⇔ (a^b) >= 0 && (s^a) < 0     (oracle desugar)
signed   a *? b  overflowed ⇔ a != 0 && (s / a != b || (a == -1 && b == MIN))
         a /? b  failed     ⇔ b == 0 || (signed && a == MIN && b == -1)
         a <<? b failed     ⇔ b >= width || (signed && b < 0) || (s >> b) != a
         a >>? b failed     ⇔ b >= width || (signed && b < 0)
```

The signed `*?` division round-trip agrees spuriously on exactly the
`(-1, MIN)` pair (`MIN / -1 == MIN` because division is total), so that
pair is added back explicitly. `/?` / `%?` share the failure predicate
above; on the self-host IR path their division is emitted only on the
`Some` branch, where the divisor is provably non-zero (and not `MIN / -1`)
so the raw op can't trap.

The remaining overflow discipline from #5542 — the `overflowing_*`
(result, overflowed) pair — has no operator form: `std/i32`, `std/i64`,
`std/u32` and `std/u64` carry `overflowing_add` / `overflowing_sub` /
`overflowing_mul` / `overflowing_div` / `overflowing_rem` (returning
`(T, boolean)`) as methods only.

Those same modules also carry the full checked family — `checked_add` /
`checked_sub` / `checked_mul` / `checked_div` / `checked_rem` /
`checked_pow` / `checked_shl` / `checked_shr`, all returning `Option[T]`
— alongside the `saturating_*` method forms. The operators above spell
the seven binary cases (`+? -? *? /? %? <<? >>?`; `checked_shl` /
`checked_shr` take an `i32` count and return exactly what `<<?` / `>>?`
return); `checked_pow` and the
unary `checked_neg` / `checked_abs` stay method-only. There is still no
aborting-on-overflow build mode, so the never-trapping contract at the
top of this document holds unconditionally.

## Conversions

Integer↔integer casts truncate (narrowing) or extend (widening) per
the destination's signedness; see `docs/FLOAT-SEMANTICS.md` for the
saturating float↔integer conversion contract. All conversions are
portable across backends.

## Testing

The cross-backend differential harness
`internal/testing/e2e/numeric_property_test.go` generates random programs over
the whole integer matrix (every width × signedness × operator,
including the division edges above) and asserts the interpreter and
every codegen backend agree. `TestNumericProperty_Regressions` pins
the specific edge programs; `FuzzNumericProperty` is the deeper search.
