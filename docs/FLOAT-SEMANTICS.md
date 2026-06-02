# Floating-point semantics

Status: policy doc. Resolves IMPROVEMENTS.md #16.

## Summary

Lang's float types (`f32`, `f64`) follow IEEE 754 for **ordinary
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
spec — Lang won't add canonicalisation passes to make them agree.

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
review would block backend work. Lang's stated use cases (small
CLIs, edge HTTP) almost never care about NaN bit-payloads or
`-0.0`-vs-`+0.0` discrimination through arithmetic. The cost / value
ratio comes out against strict mode.

This is the same calculus that retired arm32 (see `CLAUDE.md` —
the "ARM32 was retired" note): when parity costs outpace the user
value, the responsible move is to scope down.

Lang is in good company here: C, C++, Rust, Go, Zig all under-specify
NaN bit-patterns. Strict-IEEE Lang would be the outlier, not the
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
