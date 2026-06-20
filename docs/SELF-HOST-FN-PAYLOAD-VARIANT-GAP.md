# Self-host gap: enum-variant construction with a function-typed payload

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
go run ./cmd/fern -target x86-64 -o /tmp/shc examples/self_host/asm_run.fern
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

## Root cause — two sub-gaps

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
