# Go-compiler gap: generic-variant inference through a function payload — RESOLVED

Root-cause findings (and the fix) for a type-inference gap that blocked
a *generic* `Task[T]` reactor — the reason `std/reactor` used to ship
duplicated `IoStep`/`run_io` (i32) and `IoStepStr`/`run_io_str`
(string) twins instead of one generic `IoStep[T]`/`run_io[T]`
(docs/ASYNC-IMPLEMENTATION-PLAN.md Phase 1c). Go compiler only; the
self-hosted compiler has its own, separate variant-payload gap
(docs/SELF-HOST-FN-PAYLOAD-VARIANT-GAP.md, #3552).

**Status: fixed.** `std/reactor` is now a single generic `IoStep[T]`
with `run_io[T]` / `run_io_deadline[T]`; the `*Str` twins are gone.

## Symptom

Constructing a generic enum variant whose type parameter is determined
by a **non-leading payload** — in particular a function-typed payload
whose result is the type parameter — inferred the wrong type argument:
`T` came out as the leading argument's type instead of the one the
payload actually pins.

```fern
enum Box[T] { Mk(i32, (i32) => T), Nil }

function f(): Box[string] {
    return Box.Mk(7, (x: i32) => "hi");   // INFERRED Box[i32], not Box[string]
}
```

```
error[E002]: return type mismatch: function returns Box[string]
             but expression is Box[i32]
    return Box.Mk(7, (x: i32) => "hi");
```

A variant whose single payload *is* the function (`enum Box[T] { Mk((i32) => T) }`)
inferred correctly; the bug needed a concrete payload (here the leading
`i32`) **before** the function payload.

## Actual root cause

The bug was **not** in the first-pass variant-constructor unify (which
was correct — instrumenting `sub` after the unify loop showed
`sub = map[T:string]`, i.e. `Box[string]`). It was in the
**post-settle refresh**, `postSettleType`'s `*ast.Call` case
(`internal/checker/checker.go`). After `settleNumeric` widens
literals, that case recomputed the enum's type arguments by **pairing
the type-arg slot `i` positionally with constructor argument `i`**:

```go
for i := range et.Args {
    newArgs[i] = postSettleType(x.Args[i], et.Args[i]) // WRONG mapping
}
```

For `Box[T]` (one type param) the loop took `et.Args[0]` (the `T` slot)
and recomputed it from `x.Args[0]` — the leading **i32 literal** — so a
`NumberLit` settled to width 32 re-derived `T = i32`, clobbering the
correct `Box[string]` from the first pass. The positional assumption
only holds when type param `i` is filled by constructor arg `i`
(`Some(x)`, `Ok(x)`); it breaks whenever a parameter is pinned by a
payload at a different position.

## The fix

Make `postSettleType` a method on `*checker` and refresh the variant
type args by **re-unifying** each (now-settled) constructor argument
against its *declared* payload type — exactly what the first pass does,
but with the widened literals — then rebuild the `EnumType.Args` in
type-param order. A first-pass-seeded substitution keeps payload
positions that don't pin a parameter (and nested shapes) intact, so
the legitimate refreshes still work:

- `var o: Option[i64] = Some(1)` — literal widened to i64, `T`
  refreshed to i64. ✅
- `Result[T, E]` `Ok(v)` — `T` refreshed from the settled `v`, `E`
  preserved from the first pass. ✅
- `Box[T] { Mk(i32, (i32) => T) }` — `T` recovered from the function
  payload, leading i32 literal no longer captures it. ✅ (the fix)

## Tests

- `internal/checker/checker_test.go`: `TestVariantTypeParamFromFnPayload`
  (function-payload-pinned `T` for both `T=string` and `T=i32`, plus a
  leading literal that itself widens from the destination).
- `internal/e2e/reactor_socket_test.go`: `TestReactorFanoutBodies` now
  exercises the generic `IoStep[string]` reactor end-to-end on x86-64 +
  arm64 (the string-result fan-out that previously needed `IoStepStr`).
