# Floating-point semantics

Status: policy doc. Resolves IMPROVEMENTS.md #16.

## Summary

Fern's float types (`f32`, `f64`) follow IEEE 754 for **ordinary
arithmetic** but deliberately **under-specify** the edge cases:

| Behaviour                                  | Portable? |
|--------------------------------------------|-----------|
| `a + b`, `a - b`, `a * b`, `a / b`         | ✅        |
| Comparisons (`<`, `<=`, `==`, etc.)        | ✅        |
| Float-to-int truncation (in-range value)   | ✅        |
| Float-to-int truncation of NaN / out-of-range (saturating) | ✅ |
| Sign of finite zero results                | ✅        |
| Default rounding (round-to-nearest-even)   | ✅        |
| NaN production (any op that should produce NaN) | ✅   |
| Inf production (any op that should overflow / divide by zero) | ✅ |
| **NaN bit-pattern** (which exact qNaN)     | ❌        |
| **Sign-of-zero round-tripping** (-0.0 vs +0.0 through arithmetic) | ❌ |
| **Denormal handling** (subnormal → zero, or kept) | ❌ |
| **Non-default rounding modes** (no API to switch — `RNE` only) | n/a |

### Float-to-int conversion is saturating

`f as i32` / `i64` / `u32` / `u64` (and the f32 sources) **saturate**,
identically on every backend:

- `NaN` → `0`
- a value above the destination's max → its max (`INT_MAX`, or the
  all-ones `UINT_MAX` for the unsigned types)
- a value below the destination's min → its min (`INT_MIN`, or `0`
  for the unsigned types)
- everything in range → truncated toward zero, as before

This matches wasm's `trunc_sat_*` ops and arm64's `fcvtz*`; the
interpreter and x86-64 clamp explicitly to the same contract (x86's
`cvtt*2si` traps to `INT_MIN` on any invalid input, so the backend
fixes up `+overflow` / `NaN` with a compare). A conversion never
traps and never leaks a platform-specific sentinel.

"Portable" means: the result is identical across every backend
(`interp`, `arm64`, `arm64-darwin`, `x86_64`, `wasm`) for the same
source bytes and same inputs.

"Not portable" means: each backend gets to do what its hardware
gives it. Code that depends on these edges is non-portable by
spec — Fern won't add canonicalisation passes to make them agree.

## Why under-specify

Strict cross-backend bit-equality for IEEE edges requires:

- canonicalising NaN bit-patterns at every NaN-producing op (or at
  storage / function-call / printout boundaries — cheaper but still
  per-op cost on hot paths)
- emulating `-0.0` preservation through arithmetic on backends
  whose target ISA doesn't natively (rare in practice but real on
  some flush-to-zero modes)
- explicit denormal-handling shims on arm64 / x86 because the
  hardware default may differ from wasm

That's a real ongoing maintenance tax — and parity bugs caught in
review would block backend work. Fern's stated use cases (small
CLIs, edge HTTP) almost never care about NaN bit-payloads or
`-0.0`-vs-`+0.0` discrimination through arithmetic. The cost / value
ratio comes out against strict mode.

This is the same calculus that retired arm32 (see `CLAUDE.md` —
the "ARM32 was retired" note): when parity costs outpace the user
value, the responsible move is to scope down.

Fern is in good company here: C, C++, Rust, Go, Zig all under-specify
NaN bit-patterns. Strict-IEEE Fern would be the outlier, not the
default.

## What this means in practice

Safe to write:

```
function safe_div(a: f32, b: f32): f32 { return a / b; }
function clamp(x: f32, lo: f32, hi: f32): f32 {
    if (x < lo) { return lo; }
    if (x > hi) { return hi; }
    return x;
}
```

Don't write:

```
// Reading a NaN's exact bit-pattern — non-portable
var n: i32 = f32_bits(0.0f32 / 0.0f32);
return n;   // each backend may give a different qNaN payload

// Discriminating -0.0 from +0.0 after arithmetic — non-portable
var z: f32 = -1.0f32 * 0.0f32;
return f32_bits(z);   // sign bit not guaranteed across backends
```

Note that `(0.0f32 / 0.0f32) as i32` is now **portable** — a NaN
saturates to `0` on every backend (see the conversion table above).
It's reading the NaN's *bit-pattern* (via `f32_bits`) that stays
non-portable.

If you want a NaN/Inf to map to a sentinel of your own choosing
rather than the saturating default (`0` / `INT_MAX` / `INT_MIN`),
do the check at the source level with the IEEE NaN-test idiom and a
finite-range threshold:

```
function safe(x: f32): i32 {
    if (x != x) { return -1; }             // NaN check
    if (x > 1.0 * 1.0e38f32) { return -2; }    // Inf check (large finite)
    return x as i32;
}
```

`x != x` is the canonical IEEE NaN-test idiom: every backend that
implements ordinary float comparison honours it because NaN
inequality is part of the "portable" comparison guarantee above.

The NaN-test idiom runs on every backend (the interpreter handles
`*ast.FloatLit` and the parser accepts scientific-notation literals
like `1.0e38f32`). The differential oracle still excludes f32 from
its generated programs by policy (see below), but the snippets above
are supported everywhere, not just on the native backends.

## Printing and round-trip

The sections above cover float *arithmetic and conversion*. Float
*formatting* (`to_string`) and *parsing* (`parse_float`) have their own
contract, pinned here.

### `to_string` is shortest-round-trip, correctly rounded, and cross-backend-identical

`(f32).to_string()` and `(f64).to_string()` route through one shared
Fern implementation — `__float_shortest` / `__float_shortest_f32` in
`std/float.fern` — which is compiled per target from the same source. It
does all of its work in integer arithmetic over the IEEE-754 fields
(`f64_bits` / `f32_bits`), never in the float domain, so its output is
**byte-for-byte identical across interp / x86_64 / arm64 / wasm** for
every value. This is the contract, and it is enforced by the
`float_to_string_parity` fixture (`conformance/cases/`), which
runs one program on all four backends under exact-stdout comparison —
f32's only cross-backend formatting coverage, since it is excluded from
the differential oracle (below).

The algorithm is **Dragonbox** (Junekey Jeon's refinement of Schubfach).
For every finite non-zero float it produces the **unique shortest decimal
that parses back to exactly that float**, correctly rounded
(round-to-nearest-even), with no fallback path. That is the same contract
Go's `strconv` shortest formatting has, and the two agree digit for digit:
`(3.14f32).to_string()` is `"3.14"`, `(0.1 + 0.2).to_string()` is
`"0.30000000000000004"`.

Notation follows Go's `%v` / `strconv` `'g'`: scientific when the decimal
exponent of the leading digit is `< -4` or `>= 21`, fixed-point otherwise.

Guaranteed:

- **Shortest.** No decimal with fewer significant digits parses back to
  the same float.
- **Round-trip.** `parse` ∘ `to_string` is the identity for every finite
  value. That holds for Fern's own `parse_float`, which is correctly
  rounded (see below), as well as for any other correct parser such as
  Go's `strconv.ParseFloat`.
- **Correctly rounded**, including the asymmetric interval at a power of
  two, where the gap below the value is half the gap above it. That case
  is the one the previous exact-bignum formatter got wrong — it tested
  candidates against the symmetric interval everywhere, so `2^-1019`
  formatted as `1.780059086805761e-307`, one ULP below the value it was
  formatting. Pinned by `TestFloatShortestPowersOfTwoF64` / `...F32`
  (`internal/e2e/float_dragonbox_test.go`).

Still deliberately unspecified:

- **`Inf` / `NaN` spelling** is `"Inf"` / `"-Inf"` / `"NaN"`. Inf is
  detected via `n * 2.0 == n` (no `inf` literal), after sign extraction
  and a NaN pre-check.
- **`-0.0` prints as `"0"`** — the sign of zero is one of the
  deliberately non-portable edges, so it is not preserved through
  formatting.

#### Cost

Dragonbox is a table lookup plus one wide multiply per call, so it is
constant-work and allocates nothing beyond the result string. The tables
are the cost: 619 128-bit entries for f64 and 78 64-bit entries for f32,
~14 KB of static rodata, generated and verified against the upstream
dragonbox table by `cmd/floattablegen` (`go run ./cmd/floattablegen
internal/stdlib/std/float.fern` regenerates them). They ship as Fern
*string* literals rather than array literals, because a string literal is
rodata while a Fern array literal is executable code that would rebuild
the table on every call — the same encoding `std/unicode.fern` uses.

That replaced an exact-bignum digit generator which multiplied a growing
bignum once per binary exponent — 1074 limb passes for a subnormal — and
then ran up to 34 bignum comparisons to choose the digit count, allocating
an array at every step. Measured on x86-64, per conversion: **~215 µs →
~1.05 µs** on ordinary magnitudes (~205x), and ~14.5 ms → ~1.1 µs on a
subnormal-heavy mix. Allocation for 200 conversions, by
`__heap_bump_bytes()`: **66,704 → 9,840 bytes**.

`to_string_prec(prec)` is the fixed-width sibling: exactly `prec`
fractional digits, no trailing-zero trim, rounded half-away-from-zero.

### `parse_float` is correctly rounded

`(string).parse_float(): Option[f64]` (`std/string.fern`) accepts an
optional sign, integer and fractional digits, and returns `None` on empty
/ sign-only / no-digit / trailing-garbage input. It parses in the f64
domain (flipped from f32 alongside the #5363 default-width decision;
`std/json`'s number decoding now reuses it instead of carrying a private
f64 mirror).

It returns the **nearest f64 to the exact decimal value**, ties to even —
bit-exact with `strconv.ParseFloat` for every input, however many digits
it carries. Verified against `strconv` over a corpus that includes exact
decimal midpoints between adjacent doubles (the inputs that force the
round-half-to-even rule), the classic `2.2250738585072011e-308` strtod
bug, subnormals, and 1100-digit exact midpoints.

Two paths produce that result:

- **Eisel-Lemire** (`_el_parse`) is the fast path: a 128-bit power-of-five
  lookup and one wide multiply. By the Mushtak-Lemire result it is
  *provably* exact whenever the significand fits in 64 bits — at most 19
  decimal digits — so it needs no verification step for those inputs.
  Longer inputs are truncated to 19 digits and computed twice, once with
  the truncated significand and once with it incremented; when both ends
  round to the same double, every value between them does too, and the
  answer is exact.
- **`__decimal_to_f64`** is the exact fallback, taken only when those two
  ends disagree. It seeds a float estimate and refines it against exact
  big-integer midpoint comparisons, so its result never depends on the
  estimate being close. Measured over 9.5M inputs, **0.026%** reach it —
  and it is what the fast path's correctness *doesn't* have to cover.

Cost: the fast path is ~1.3 µs per parse against ~3.2 ms for the refinement
loop alone (~2,400x), measured on x86-64 over shortest-repr inputs. The
power-of-five table is 651 128-bit entries, ~14 KB of rodata, generated and
checked against the upstream fast_float table by `cmd/floattablegen`. It is
dead-code eliminated in programs that never call `parse_float`.

Because `to_string` is shortest-round-trip and `parse_float` is correctly
rounded, **`parse_float ∘ to_string` is the exact identity** on every
finite value — not a tolerance. Code may compare the result with `==`.

> Historical note: this section previously documented a mantissa
> saturating at 1e15 and a round-trip that held only "within a small
> relative tolerance (`≤ 0.001`)". Both described an older parser and were
> long stale; neither has been true since #5566 made `__decimal_to_f64`
> exact. The tolerance-based assertions still in the
> `float_to_string_parity` fixture are historical too — they pass, but
> they are much weaker than what the parser now guarantees.

### Default float width

**f64 is the default and primary float** (owner decision, #5363). An
unsuffixed float literal with no expected-type pressure settles to f64
(`ast.FloatType.NormalWidth`, mirrored by the interp and the IR
lowering's Width-0 → `OpConstF64` path), and `float` is a first-class
alias for f64 on both compilers — the native parser resolves it
contextually in type position (like `str`; it is not a lexer keyword,
so `float.pi()`-style module calls keep working), matching the
self-host checker's long-standing resolution. The old discrepancy
(native E064 on `float`, self-host silently f64) is resolved. f32
remains fully supported via explicit annotation (`var x: f32`), suffix
(`1.5f32`), or cast (`x as f32`); it is opt-in precision-narrowing,
never a default. Pinned by `TestFloatDefaultWidthF64`
(`internal/e2e/float_semantics_test.go`), `TestFloatAliasAndDefaultWidth`
(checker), and the `float-alias-ok` / `float-alias-mismatch`
self-host checker-codes fixtures.

## Generator + oracle implications

`internal/fernsmith` has two generation profiles (see
`Profile` in `fernsmith.go`):

- `ProfileFree` — free-form generation; f32 is in the type pool.
  Used by the parser-roundtrip fuzzer and the deterministic
  feature-coverage sweep. Those tests don't compare runtime
  output, so NaN-edge programs are fine.

- `ProfileRunnable` — drives the cross-backend differential
  oracle (`FuzzGenerate_ExecutionAgrees`, `TestDifferential_LangsmithMain`).
  f32 is **deliberately excluded from the type pool** because the
  oracle compares `main()`'s 1-byte return code across backends and
  the non-portable float edges (NaN bit-patterns, `-0.0`-vs-`+0.0`)
  would surface as mismatches if a generated program reinterpreted a
  float's bits. (Float→int *conversion* is now portable — it
  saturates — but the bit-level edges above still aren't.) This
  isn't a workaround — it's the policy applied at the generator
  level so the oracle stays a clean signal for real codegen bugs.

If a future feature needs the generator to exercise float code in
the runnable profile, the program-generator side (not the oracle)
is the right place to add the NaN/Inf-safe return path shown above.
The oracle deliberately stays simple.

## Hand-written float tests

The direct float e2e tests (`TestArm64Floats`, `TestWASMFloatArithmetic`,
`TestX86_64Floats`, `TestArm64FloatBitCast`, `TestWASMFloatCasts`)
exercise the portable subset only. Adding tests that assert specific
NaN bit-patterns across backends would contradict this doc — don't.

The `float_to_string_parity` fixture
(`conformance/cases/float_to_string_parity/`) pins the printing
+ round-trip contract above: one program, run across all four backends
under exact-stdout comparison (formatting parity) plus in-program
round-trip-within-tolerance assertions. Extend its value table when the
formatter changes; keep the printed values exactly representable in f32 so
the exact-match stays deterministic (put inexact values through the
round-trip path instead).
