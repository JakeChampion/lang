# A captured dyn value is two words on wasm

2026-09-23. wasm, and the natives' argument cell. #10075.

```
function later(l: dyn Label): () => i32 { return () => l.a(); }
```

On wasm the closure answered 5 where the natives answered 2.

## Cause

A wasm dyn value is the inline pair `[data, vtable]`. The closure
conversion and the IR already lay a dyn capture out as 8 bytes, and the
closure body loads it back as the pair (`payloadLoadOpFor`). The wasm
backend's env writer (`emitClosureMakeAlloc`) sized and stored every
capture other than a string as one word. So it popped only the vtable
into the env, left the data word on the stack, and every capture after
it sat 4 bytes short of where the body read it. A dyn capture followed by
a string and another dyn trapped with an out-of-bounds table access.

`isPairCapture` now names both two-word captures, a string and a dyn
value, for the slot size, the scratch valtypes, the pop and the store.

## Found on the way

- **The wasm closure drop never released a dyn capture.**
  `genClosureDropThunk` had a dyn arm for the natives only, so the unit
  MakeEnv retained leaked with every closure. It now releases through
  `dynSlotDrop`, the helper the struct and tuple drops already use, which
  covers both layouts. `hasRcCapture` asks the same helper, so a closure
  whose only counted capture is a dyn value is dropped through its thunk
  on wasm too, not the generic env drop.
- **A coerced dyn argument beside a string leaked its cell on the
  natives.** `inferParamRetainSummary` treats a dyn parameter as holding no
  heap, and credits such a parameter only when every pointer parameter is
  counted. A string parameter the closure captured withdrew the credit, so
  `later(x, s)` never released the 16-byte cell it built for `x`. A dyn
  value alone was released by coincidence. A coerced argument's cell and
  concrete unit are the call's own however the callee is summarised,
  because a callee that keeps a dyn value takes a unit of its own
  (`2026-09-23-c-…`, `2026-09-23-d-…`). The direct and indirect call
  paths now stash it through `dynCoercedArg`.

## Measured

A closure capturing `(dyn, i32, string, dyn)`, built six times in a
helper:

| | before | now |
|---|---|---|
| wasm | out-of-bounds table trap | 7 |
| x86-64 `-sanitize` | 16 B live per trip | balanced |
| arm64 `-sanitize` | 16 B live per trip | balanced |

The alias churn's wasm leg, excluded until now, holds its heap flat.
`TestDynCaptureBesideOtherCaptures`, and the wasm legs of
`TestDynCaptureStoredPastTheBorrow` and
`TestDynAliasStoredPastTheBorrowBounded`, fail without the change.
