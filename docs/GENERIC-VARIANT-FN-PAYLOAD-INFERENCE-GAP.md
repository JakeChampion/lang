# Go-compiler gap: generic-variant inference through a function payload

Root-cause findings for a type-inference gap that blocks a *generic*
`Task[T]` reactor — the reason `std/reactor` ships duplicated
`IoStep`/`run_io` (i32) and `IoStepStr`/`run_io_str` (string) twins
instead of one generic `IoStep[T]`/`run_io[T]`
(docs/ASYNC-IMPLEMENTATION-PLAN.md Phase 1c). Go compiler only; the
self-hosted compiler has its own, separate variant-payload gap
(docs/SELF-HOST-FN-PAYLOAD-VARIANT-GAP.md, #3552).

## Symptom

Constructing a generic enum variant whose payload is a **function
returning that enum** infers the wrong type argument: `T` is left at
the i32 default instead of being recovered from the function's return
type (or the expected type).

```fern
enum Step[T] { Done(T), Wait(i32, (i32) => Step[T]) }

function start(s: string): Step[string] {
    function resume(w: i32): Step[string] { return Done(s); } // OK: Step[string]
    return Wait(1, resume);   // INFERRED Step[i32], not Step[string]
}
```

```
error[E002]: return type mismatch: function returns Step[string]
             but expression is Step[i32]
    return Wait(1, resume);
```

An explicit annotation doesn't rescue it either:

```fern
var node: Step[string] = Wait(1, resume);
// error[E003]: cannot assign Step[i32] to variable of type Step[string]
```

The `Done(s)` variant (payload is `T` directly) infers `Step[string]`
correctly — only the **function-typed payload** path misbinds.

## What works vs not (spiked on the Go x86-64 backend)

| construct | result |
|---|---|
| `Step[i32]` via `Wait(5, resume)` (resume: `(i32)=>Step[i32]`) | ✅ 21 — compiles + runs |
| `Step[string]` via `Wait(1, resume)` (resume: `(i32)=>Step[string]`) | ❌ infers `Step[i32]` |
| `Done(s)` (string) — simple `T` payload | ✅ `Step[string]` |

So the `T=i32` case only works *by luck* (the misbound default happens
to match); any non-i32 instantiation through a function payload fails.

## Where it lives

- Variant-constructor inference: `internal/checker/checker.go` ~8793–8864
  (the `isVar` branch of the `*ast.Call` case). It unifies each arg
  against the declared payload type —
  `c.unifyType(vr.payloads[i], at, sub)` (~8818–8827) — then builds the
  result `EnumType.Args` from `sub[p]` per type-param (~8843–8862).
- `unifyType` already handles `FuncType` (params + result) and generic
  `EnumType` (pairwise Args): `internal/checker/checker.go` ~5335–5403.
  So the *machinery* to bind `T` from `resume`'s `(i32)=>Step[string]`
  through to `Step[T]`'s `T` exists — meaning the bug is that `sub[T]`
  ends up i32 anyway: either `at` (the checked type of the bare
  function-name arg) isn't `(i32)=>Step[string]` at this point, or the
  FuncType→EnumType→ParamType unify isn't binding `T` for this shape.
  Pinning which needs instrumenting `sub` after the unify loop.

## Fix direction

Two complementary angles:
1. **Expected-type-directed inference:** seed `sub` from the
   destination/annotation (`Step[string]`) before/with the arg unify,
   so a function-typed payload that can't bind `T` from the arg alone
   still resolves. (The result construction already returns a
   parameterless `EnumType{Name}` when `!complete` to let `assignable`
   flow context in — but here it returns `complete` with the wrong
   `T`, so context never gets a chance.)
2. **Verify the function-name arg's checked type** carries
   `(i32)=>Step[string]` into the unify (vs a defaulted/erased form).

## Impact / why it matters

Until fixed, a generic stackless-task type (`Task[T]` / `IoStep[T]`)
can't be expressed, so each result type needs a hand-duplicated
reactor (`run_io` for i32, `run_io_str` for string, …). The fix folds
those back into one generic reactor and removes the duplication — and
generally unblocks generic enums whose variants carry continuations
(the stackless-CPS shape this whole concurrency design is built on).
A checker regression test: the `Step[string]` repro above should
type-check and run.
