# Self-host gap: enum-variant construction with a function-typed payload

> **RESOLVED on the IR path — x86-64 (#4364) and wasm (#4722).** The
> analysis below was written against the legacy **SSA / AST emitter** path
> (`asm_run.fern`). The production path is now the **IR path**
> (`irlower.fern` → `asm_ir` / `wasm_ir`, `use_ir` default-true), and it
> lowers fn-payload variant construction + `match`-bind + indirect-call
> correctly on **both** the x86-64 and wasm backends. The intervening
> closure-conv work (#4354 + the CLOSURE-CONV slices) made a function value
> in payload position lower as an ordinary pointer-sized payload, composing
> with variant construction.
>
> **Every shape works on both backends** — named-fn, capturing-closure,
> recursive `Step`, generic `Box[T]`, and the exact `Future[T] = Ready(T)
> | Pending(i32, (i32) => Future[T])` shape all route `module: IR` and
> match the interp oracle. The x86 IR path (`asm_ir_run`, the AST emitter's
> `use_ir` dispatch) does not monomorphize the generic enum — it lowers
> `Fut[T]` directly, deriving each `match`'s enum from its arm variant
> names — so the generic and recursive shapes lowered there all along.
>
> **The wasm leg (#4722)** required one fix, because the wasm IR driver
> *does* monomorphize the generic enum (`Fut[T]` → `Fut__i32`, needed for
> element-method dispatch, #3893), renaming its variant structs
> (`Rdy`→`Rdy__i32`). The nested `match (cont(tok))` on the closure-call
> scrutinee then needs monomorphization to rewrite its arm patterns to the
> mangled names, which needs `me_scrutinee_type` to recover `cont(tok)`'s
> type. That was blocked because the fn payload's return type (`Fut[T]`)
> was **dropped at parse**: `parse_type_name` only kept a *bare-struct*
> return (`is_struct_ret_name` rejects any bracketed/generic spelling), and
> `StructFieldDecl` had no `fn_ret` slot. The fix (#4722): broaden the
> parse capture to retain a nominal (possibly generic) enum return
> spelling, add `fn_ret` to `StructFieldDecl`, preserve/substitute it
> through flatten + monomorphize, and have `me_bind_pat_vars` bind the
> payload to an encoded `fn => <ret>` spelling that `me_scrutinee_type`
> recovers. The `type_name` stays exactly `"fn"`, so the register backends
> and the ~26 `== "fn"` codegen checks are byte-identical; the modload
> fixpoint (mmc == gen2 == gen3) holds.
>
> Pinned by `internal/e2eselfhost/self_host_fn_payload_variant_ir_test.go`:
> both the x86-64 and wasm legs exercise all five shapes. The
> `c.args.len() == 1` single-payload guard in the legacy AST emitter
> (`asm.fern`) is unchanged, but that path is retirement-bound
> (docs/SELFHOST-SSA-ALWAYS.md) and no longer on the fn-payload route.
> The rest of this document is retained as the historical root-cause
> record.

Root-cause findings for a self-hosted-compiler gap that blocks the
async runtime (`std/task`) on the self-host path. Minimal repros
included. Surfaced while porting the `concurrent`/`spawn` desugar
(docs/ASYNC-IMPLEMENTATION-PLAN.md) to the self-hosted frontend.

## Symptom

`std/task`'s core type is a recursive enum whose payload is a
function value:

```fern
pub enum Step {
    Done(i32),
    Wait(i32, (i32, Reactor) => (Step, Reactor)),
}
```

Compiling any program that **constructs** `Wait(tok, resume)` through
the self-hosted compiler emits an asm file that **fails to link**:

```
undefined reference to `__fn_Wait'    // the variant constructor
undefined reference to `__fn_cont'    // calling a closure read from the payload
```

i.e. the compiler emits `call __fn_Wait` — treating the variant
constructor as a call to an ordinary function named `Wait` — instead
of building the variant box. The Go compiler handles this correctly
(the runtime is validated there on interp / x86-64 / arm64 / wasm).

## Minimal repros (piped through the self-host x86-64 driver)

Build the driver, then feed each program on stdin:

```
go run ./cmd/fern -target x86-64-linux -o /tmp/shc examples/self_host/asm_run.fern
/tmp/shc < prog.fern > prog.s && cc -nostdlib -static -o prog prog.s && ./prog
```

| # | Program (constructed variant) | Result |
|---|---|---|
| A | plain closure `var f = makeAdder(5); f(37)` | ✅ 42 |
| C | 1-arg non-fn variant `Val(10)` | ✅ 10 |
| E | **multi-arg** non-fn variant `Two(10, 32)` | ✅ 42 |
| D | fn-payload variant declared + **matched** + only `Empty` constructed | ✅ 5 |
| B | fn-payload variant **constructed** `Fn(10, add)` | ❌ link: `undefined __fn_Fn` |

So: plain closures work; plain multi-arg variants work; declaring,
parsing, and *match*-binding a fn-payload variant works. **Only the
construction of a variant whose payload includes a function value
fails.**

```fern
// B — the failing case, self-contained:
enum Box { Fn(i32, (i32) => i32), Empty }
function main(): i32 {
    function add(x: i32): i32 { return x + 1; }
    var b: Box = Fn(10, add);                 // emits `call __fn_Fn`
    match (b) { Fn(n, f) => { return n; }, Empty => { return 0; } }
}
```

## Which path B takes (confirmed)

B's emitted asm is stack-machine style (`pushq`/`popq`/`movq -8(%rbp)`),
i.e. it went through the **AST emitter**, not SSA — the SSA path bailed
on the function-typed payload and fell back. So the proximate
`call __fn_Fn` is emitted by the AST emitter's generic-call path.

## Root cause — layered sub-gaps

1. **SSA path (`ssa.fern` `build_func`) bails on a function value used
   as a variant-payload argument.** Multi-arg variant construction
   itself works on the SSA path (repro E goes through SSA and links),
   so the bail is specifically triggered by the function-typed
   argument (`add` / `resume` as a value in payload position). The
   bail falls back to the AST emitter.

2. **The AST emitter only constructs *single*-payload variants.**
   `examples/self_host/asm.fern:848`:

   ```fern
   if (asmcore.is_enum_variant(name, s) && c.args.len() == 1) {
       // … build the variant box …
   }
   ```

   The `c.args.len() == 1` guard means any **multi-arg** variant that
   reaches the AST emitter is *not* recognized as a constructor and
   falls through to the ordinary-call path → `call __fn_<Variant>` →
   undefined symbol. (Multi-arg variants normally never hit this path
   because SSA handles them; the fn-payload bail in (1) is what
   routes them here.)

The proximate link failure is (2); the reason a multi-arg variant
reaches the broken (2) at all is (1).

3. **Calling a closure read back from a variant payload** also fails:
   the `sh_step` repro additionally emits `undefined reference to
   __fn_cont` for `cont(41)` where `cont` was `match`-bound from a
   `Wait(tok, cont)` payload. The AST emitter treats the call to the
   match-bound `cont` as a direct named-function call rather than an
   indirect closure call. So even with construction fixed, the
   resume-the-continuation step needs the call site to recognize a
   match-bound function-typed payload as a closure value.

### The variant model (why this is layered, not a one-liner)

The self-host checker (checker.fern:674) models an enum variant `V(…)`
as a struct carrying a **single `__ev` marker field** — multi-payload
variants ride a tuple in that one field. So a faithful AST-emitter fix
isn't "store N flat payload slots"; the construction box layout must
match whatever the `match` side reads back for a multi-payload variant
(a tuple in `__ev`), and the match-bind + indirect-call (sub-gap 3)
must agree. Getting construction, match-extraction, and
indirect-call mutually consistent across the AST emitter is the real
scope.

## Fix directions (for the SSA-migration epic, #2691)

The retirement-aligned fix is **(1)**: make `build_func` treat a
function value in variant-payload position as an ordinary
pointer-sized payload (it already handles closures as locals — repro A
— and multi-arg variant boxes — repro E; this is composing the two).
Then fn-payload variants construct on the SSA path and never reach the
AST emitter. Sub-gap (2) is in the AST emitter, which is slated for
retirement once SSA reaches parity (docs/SELFHOST-SSA-ALWAYS.md), so
broadening its variant-construct guard to multi-arg is a lower-value
stopgap — worth it only if a fn-payload program must compile before
(1) lands.

Either fix wants a `match`-on-an-async-`Step` regression in the
self-host audit (the construction → store → match-bind → indirect-call
round-trip), mirroring the runtime's actual shape.

## Impact

Until (1) lands, `std/task` and anything built on it
(`concurrent`/`spawn`, the scheduler, `select`) compile and run only
on the **Go compiler**, not the self-hosted compiler. The Go-side
async work is unaffected; the self-host *parser* port of the
`concurrent`/`spawn` desugar is blocked on this codegen gap (a parser
port would emit `task.run` / `Wait` calls that don't link).
