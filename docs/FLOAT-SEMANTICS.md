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
| Sign of finite zero results                | ✅        |
| Default rounding (round-to-nearest-even)   | ✅        |
| NaN production (any op that should produce NaN) | ✅   |
| Inf production (any op that should overflow / divide by zero) | ✅ |
| **NaN bit-pattern** (which exact qNaN)     | ❌        |
| **Sign-of-zero round-tripping** (-0.0 vs +0.0 through arithmetic) | ❌ |
| **Denormal handling** (subnormal → zero, or kept) | ❌ |
| **Float-to-int truncation of NaN / out-of-range** | ❌ |
| **Non-default rounding modes** (no API to switch — `RNE` only) | n/a |

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
// Comparing NaN bit-patterns — non-portable
var n: i32 = (0.0f32 / 0.0f32) as i32;
return n;   // each backend gives a different byte

// Discriminating -0.0 from +0.0 after arithmetic — non-portable
var z: f32 = -1.0f32 * 0.0f32;
return (1.0f32 / z > 0.0f32) as i32;   // not guaranteed across backends

// Returning a NaN as an `i32` exit code — non-portable
function main(): i32 {
    return (0.0f32 / 0.0f32) as i32;
}
```

If you need portability across NaN/Inf edges, do the check at the
source level using the IEEE NaN-test idiom and a finite-range
threshold:

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

Today the interpreter and parser have some narrowing gaps that
prevent the NaN-test idiom from running cross-backend in the
diff-oracle (the interp doesn't yet handle `*ast.FloatLit`; the
parser doesn't yet accept scientific-notation literals like
`1.0e38f32`). Both are tracked separately. Until those land,
treat the NaN / Inf snippets above as documentation of the
*intended* portable form — supported on the native backends,
not yet through the differential oracle.

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
  oracle compares `main()`'s 1-byte return code across backends,
  and any NaN/Inf result would be a non-portable mismatch by
  policy. This isn't a workaround — it's the policy applied at
  the generator level so the oracle stays a clean signal for
  real codegen bugs.

If a future feature needs the generator to exercise float code in
the runnable profile, the program-generator side (not the oracle)
is the right place to add the NaN/Inf-safe return path shown above.
The oracle deliberately stays simple.

## Hand-written float tests

The direct float e2e tests (`TestArm64Floats`, `TestWASMFloatArithmetic`,
`TestX86_64Floats`, `TestArm64FloatBitCast`, `TestWASMFloatCasts`)
exercise the portable subset only. Adding tests that assert specific
NaN bit-patterns across backends would contradict this doc — don't.
