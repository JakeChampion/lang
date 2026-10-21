# SIMD generic kernels: `Lanes`, `Dual`, `DoubleDouble` and the lane types

Status: [plan] — accepted 2026-10-21. Slices land in order; each one's
state is in §7. This document revises `ATLAS-PLATFORM-PLAN.md` §1.2 (the case
against a portable vector type) in §4, and records two language decisions
taken for it: associated constants ship now (§3.1); const generics follow the
lane type as their own project (§3.2).

## 1. What this buys

One numeric kernel, written once against a small trait, that the compiler
turns into:

- scalar code on `f64` and `f32`;
- two-lane or four-lane SIMD on `f64x2` / `f32x4`, which every target
  guarantees (NEON, AVX2's lower half, wasm `v128`);
- its own first derivative, by calling it on `Dual[T]` instead of `T`
  (forward-mode automatic differentiation);
- a result carrying about 106 bits of precision, by calling it on
  `DoubleDouble[T]` (compensated arithmetic).

The prior art is Thermite (Rust): a `SimdElement`-style trait hierarchy, a
`Dual` type, a double-double type, and a vectorised special-function library,
all generic over one trait. Two of its mechanisms do not transfer and are
deliberately not copied:

- **Runtime instruction-set dispatch.** Thermite picks SSE2 / AVX2 / NEON at
  run time. Fern's targets declare their baselines (`CLAUDE.md` ▸ Targets):
  AVX2 on x86-64, Advanced SIMD on arm64, `v128` on wasm, with no cpuid check
  anywhere. A lane type here is selected at compile time by the target, and
  runtime dispatch stays off the table.
- **`Vec<const N: usize, T>`.** Fern has no const generics. The lane count is
  carried by concrete types behind one trait, with an associated constant
  naming it (§3).

## 2. What already exists

| Need | State |
| --- | --- |
| Nominal traits, `impl Trait for Type`, monomorphised bounded generics | shipped (`TRAITS.md`) |
| Parametric impls with bounds (`impl[I: Iterator[T], T] Iterator[T] for Filtered[I, T]`) | shipped (`core/iter.fern`) |
| Associated types, supertraits | shipped (`ASSOCIATED-TYPES.md`) |
| Operator overloading on structs: `+ - * / %` and unary `-` desugar to `add` / `sub` / `mul` / `div` / `rem` / `neg` by name | shipped (#2706) |
| `Add` / `Sub` / `Mul` / `Div` / `Neg` / `Zero` / `One` and the `Num` supertrait, implemented for every built-in number | shipped (`std/num`) |
| `exp`, `log`, `sin`, `cos`, `tan`, `atan2`, `expm1`, `log1p`, `cbrt`, `pow`, … on `f64` in pure Fern, bit-identical on every backend | shipped (`std/float`), concrete in `f64` |
| SIMD in the compiler | fused whole-loop kernels only: `__memchr`, `__count_byte`, `__ascii_run`, `__inner_mul_add_f64`, … (`ATLAS-PLATFORM-PLAN.md` §3) |
| A SIMD value type | none |
| Associated constants on traits | none before this plan |
| Const generics | none |

So the trait layer, the operator layer and the scalar math are in place, and
the library half of the plan (§5) needs no compiler change beyond §3.1. The
compiler half (§6) is the lane type.

## 3. Language decisions

### 3.1 Associated constants (ships in this series)

```fern
trait Lanes: num.Num + num.Neg {
    type Elem;
    const LANES: i32;
    function splat(x: Self::Elem): Self;
    …
}
impl Lanes for f64 {
    type Elem = f64;
    const LANES: i32 = 1;
    …
}
function chunk[T: Lanes](n: i32): i32 { return n / T::LANES; }
```

- A trait declares `const NAME: Type;` (abstract) or `const NAME: Type =
  expr;` (a default every impl may omit). An impl binds `const NAME: Type =
  expr;`. The impl must bind every abstract constant, may not bind one the
  trait does not declare, and the type must match the trait's with `Self`
  substituted. Those are the associated-type rules, applied to a value.
- A constant is read as **`Base::NAME`** in expression position: `Self::NAME`
  inside an impl or a trait default, `T::NAME` through a bound, `f64::NAME`
  on a concrete type. `Base::NAME` with no argument list is the only
  expression form that `::` introduces; `Base::f(…)` keeps meaning the
  associated call `Base.f(…)`.
- Implementation, in both compilers: an associated constant is a nullary
  associated function marked `const`. The self-host already desugars a
  module-level `const` that way (`parser.fern` ▸ `parse_const_decl`), so the
  conformance check, the `T.f()` dispatch through a bound, the monomorphiser
  and the interpreter all already handle the shape; the parser adds the
  `const` forms in trait and impl bodies and the `Base::NAME` read, the
  checker refuses a `const` bound by a `function` or vice versa, and the
  formatter prints the written forms back. After monomorphisation the read is
  a call to a one-literal body, which the inliner folds.
- A constant's type is any scalar; a string or array constant is not refused,
  but a lane count is what the plan needs.

### 3.2 Const generics (follow-on, after the lane type)

Decided 2026-10-21: const generics are wanted, as the project **after** the
lane type lands, with `Vec[N, T]` as their first consumer. They are not built
first because they are a type-system project of their own in both compilers
(a value-kinded parameter, inference from a lane type's width, instance
naming in `ssasem.instance_name`, array sizing), and the portable 128-bit tier
has exactly two float lane types, which a trait over concrete types carries
without them. When they land, `f64x2` and `f32x4` become spellings of
`Vec[2, f64]` and `Vec[4, f32]`; the `Lanes` surface does not change.

### 3.3 The portable width is 128 bits

Every target guarantees a 128-bit vector register file. The lane types are
therefore `f64x2` and `f32x4`, with `i32x4` / `i64x2` / `u8x16` as later
additions through the same machinery. x86-64's AVX2 gives 256 bits; an
`f64x4` is either an x86-only type or a pair of `f64x2` on the other targets,
and it is not in this plan. Thermite's "widest native vector" alias has no
target-conditional type to resolve against here and is not copied.

### 3.4 The precision policy is a type

Thermite tunes each special function with a `Policy` (precision versus speed,
whether to handle every edge case). Fern has no associated constants with
default values on type parameters and no const generics, so a policy is a
**second type parameter** bounded by a small trait whose members are
associated constants: `exp[T: Lanes, P: Policy](x: T): T` with `P::PRECISE:
boolean` and friends. `std/simd` ships `Precise` and `Fast`.

## 4. Revising `ATLAS-PLATFORM-PLAN.md` §1.2

§1.2 ruled a first-class vector type "the expensive design" because both
native backends were stack machines over 8-byte operand slots: every f64
entered and left the vector register file inside one op, so a 128-bit value
had nowhere to live. That was true when written. It is no longer the shape
of the native emitters:

- the SSA backend is the only emitter on the native ISAs
  (`SELFHOST-SSA-BACKEND.md`). It allocates registers over pools, keeps an
  f64 in its float register across ops, phis and selects, and moves f64
  parameters into float registers at the `.r` entry;
- the register file a 128-bit lane type lives in **is** that float register
  file: xmm on x86-64, V on arm64. No third register class is needed.

What a lane type still costs, and the plan pays:

| Piece | Where |
| --- | --- |
| A scalar type of width 128 (`TypeLanes { elem, count }`) with no refcount | `typeinfo.fern`, `checker.fern`, the Go checker and oracle |
| IR ops: `vsplat`, `vadd`, `vsub`, `vmul`, `vdiv`, `vfma`, `vsqrt`, `vmin`, `vmax`, `vabs`, `vneg`, `vfloor`, `vcmp_*` to a lane mask, `vselect`, `vload`, `vstore`, `vextract`, `vreduce_add` | `ir.fern`, target-agnostic |
| 16-byte spill slot and reload in `ssa.regalloc_linear` | `ssa.fern` |
| Call ABI: a lane argument or result in a vector register (`q0`–`q7` / `xmm0`–`xmm7`) on the `.r` entry, the stack entry pushing two words | both native lifts |
| wasm: `v128` locals, `f64x2.*` / `f32x4.*` | `wasm_ir.fern` |
| Interpreter: the oracle's and `interp.fern`'s value for a lane, so the differentials run | `internal/oracle/interp`, `interp.fern` |
| Capabilities: each new builtin classified in the four tables | `platforms.fern`, `caps.fern`, and the two Go packages |
| Rule 5 of the kernel contract stays: every op has a scalar reference and a differential against it | tests |

What does **not** change: the fused-intrinsic contract for whole-loop kernels
(`memchr` and its family) is unaffected, and remains the right shape for a
kernel whose vector never crosses an IR value boundary. The lane type is the
widening §1.2 said it leaves available.

## 5. `std/simd`

One module, `internal/stdlib/std/simd.fern`, importing `std/num` and
`std/float`.

```fern
pub trait Lanes: num.Num + num.Neg {
    type Elem;
    const LANES: i32;
    function splat(x: Self::Elem): Self;        // every lane = x
    function lane(self: Self, i: i32): Self::Elem;
    function fma(self: Self, b: Self, c: Self): Self;   // self * b + c, one rounding where the target has it
    function sqrt(self: Self): Self;
    function abs(self: Self): Self;
    function floor(self: Self): Self;
    function min(self: Self, o: Self): Self;
    function max(self: Self, o: Self): Self;
    function lt(self: Self, o: Self): Self::Mask;
    function select(mask: Self::Mask, a: Self, b: Self): Self;
    function to_bits(self: Self): Self::Bits;   // for exponent manipulation in exp / log
    function from_bits(b: Self::Bits): Self;
}
```

As built (slice 3): there is no `Elem`, `Mask` or `Bits` associated type. A
constant enters a kernel as an f64 (`T.splat(2.0)`), which an `f32` or a
narrower lane converts; a float literal cannot adapt to a projection the
call site has not yet bound, so `splat(x: Self::Elem)` could not take `2.0`.
A comparison yields a mask in the type itself (one lane 1.0 or 0.0) and
`select` reads it, so a branch in a scalar kernel (`if (x > limit)`) becomes
a `select` in the generic one, which is what makes the same body serve both.
The bit-level primitives the vector kernels need (`to_bits`, lane-wise
shifts) arrive with the lane type in slice 5, where there is a type to test
them on; until then `ldexp` and `round` cover the argument reductions.

- **Scalar impls**: `impl Lanes for f64` and `impl Lanes for f32`, one lane,
  delegating to `std/float`. Every generic kernel runs today through these.
- **`Dual[T: Lanes]`** `{ v: T, d: T }`: `impl[T: Lanes] num.Add for
  Dual[T]` and the rest, so `a * b + c` on duals is plain source, and
  `impl[T: Lanes] Lanes for Dual[T]` so a kernel takes a dual. A `Dual` of
  an `f64x2` differentiates two points at once.
- **`DoubleDouble[T: Lanes]`** `{ hi: T, lo: T }`: Dekker's two-sum and
  two-product, with `fma` where the trait provides one. About 106 bits on
  `f64`.
- **`Math`**: `exp`, `log`, `sin`, `cos`, `tan`, `tanh`, `atan`, `expm1`,
  `log1p` and `pow`, a trait each type implements for itself. The scalars
  adopt `std/float`'s methods (an impl adopts a method the type already has,
  so `impl Math for f64 {}` is the whole binding); `Dual` applies the chain
  rule to the inner type's functions; `DoubleDouble` reduces the argument
  and sums a series at its own precision, seeded by the inner type's own
  result where a Newton step doubles it (log, atan, sqrt). The lane types
  implement `Math` with the bit-level kernels ported from `std/float`
  (slice 5); writing those against `Lanes` before a lane type exists would
  leave them untested on the only type they are for.

**Performance, measured.** A `Dual` or `DoubleDouble` op builds a record per
operation. `docs/benchmarks/simd-evaluators-2026-10-21/` runs one kernel
(fourteen `Lanes` operations and one `exp` per element) over 20,000 doubles
on x86-64: the scalar takes 20 ns per element with no allocation, the dual
324 ns with 21 allocations, the double-double 3,610 ns with 323. The answers
are right; the records are materialised, because `seminline` splices a leaf
built from literals and not one built from its parameters' fields, so
nothing reads a `Dual { … }` construction apart. The fix is in the compiler
(#12931), not at the call site, and takes both evaluators to the arithmetic's
own cost (about 3x and 20x the scalar).

Gates: `tests/stdlib/simd_*_test.fern` through `std/test`, each with a Go
gate in `internal/testing/e2e/test_runner_test.go`; the differential against
`std/float` over a sweep of arguments per kernel; the e2e compiled legs on
x86-64 and wasm.

## 6. The lane type

The pieces are the table in §4, built in this order so each is gated as it
lands: the type and the interpreters first (so a program using `f64x2` runs
under `-interp` and the differential exists), then the IR ops and the wasm
lowering (the simplest backend, and the one whose `v128` locals need no
register allocator), then the two native lifts, then `impl Lanes for f64x2`
and `impl Lanes for f32x4` in `std/simd`.

`internal/testing/e2ecompiler` is the primary gate for the lifts; the fixpoint
is secondary, since it is blind to a stable miscompile (`TEST-GATES.md`).

## 7. Slices

| # | Slice | State |
| --- | --- | --- |
| 1 | This brief | this PR |
| 2 | Associated constants, both compilers | this PR |
| 3 | `std/simd`: `Lanes`, `Math`, scalar impls, `Dual`, `DoubleDouble`, benchmark | this PR |
| 4 | Lane types `f64x2` / `f32x4`: type, interpreters, IR ops, wasm, x86-64, arm64, capability tables | this PR |
| 5 | `impl Lanes for f64x2` / `f32x4`; array fusion picks the lane instance for a packed `f64[]` map | this PR |
| 6 | Const generics; `Vec[N, T]` | follow-on project |
