# A declined threaded param owns its slot by flag

2026-09-24. Native. #10237.

## What was wrong

```fern
struct Ctx { f: (string) => i32, n: i32 }
function steps(ctx: Ctx): Ctx {
    ctx = step(ctx);
    return ctx;
}
```

A reassigned borrowed param of a struct, tuple, enum, array or string type is
promoted by `computeConsumedParams`: the caller retains it on entry, and that
retain balances the overwrite dec the reassignment emits. The promotion
declines a type with no wired deep drop (`typeDeepDropWired`: a closure or Map
field), and the Assign catch-all emitted the overwrite dec anyway. So
`ctx = step(ctx)` released a reference `steps` was never given, and the
caller's own drop took the box to -1. `-sanitize` reports a double free. The
same struct with an `i32` field in place of the closure is clean.

The self-hosted compiler found it when `asmcore.EmitState` gained a
function-typed field. `CheckCtx { st: EmitState }` is threaded through
`ctx = check_stmt(.., ctx)`, and the natively built compiler corrupted its
heap in `asmcore.check_func` on any program with two functions.

## The fix

A declined param carries the ownership bit a consumed-threaded array param
already has (`ownFlagName`), recorded as `rcState.flagThreadedParams`:

- The overwrite releases the old value only once the frame has replaced the
  incoming borrow. A value the frame owns is deep-dropped
  (`emitOwnedSlotDrop`). A same-pointer RHS takes the flat dec that balances
  the count it added.
- The exit sweep releases the slot when the bit is set.

Map params stay on their own bit (`cowMapParams`).

## Measured

Two rc-corpus cases, on x86-64, arm64 and wasm, and each exits 1 on main (the
underflow counter):

- `closure_field_struct_param_threaded_by_reassignment`, reassigned in a loop
  and called with zero trips as well.
- `map_field_struct_param_threaded_by_reassignment`.

Both reclaim everything under the leak gate. Before the owned value was
deep-dropped, the loop case leaked one 32-byte box per replaced iteration.

## Next lead

A struct with a closure field is still not deep-drop wired, so its locals take
the leak-mode drops. The generated `__drop_struct_*` already releases a closure
field through `__drop_closure_value` on every backend, which is what admitting
`*ast.FuncType` to `typeDeepDropWired` would rest on. The self-host's runtime
helper routing needs it: with the field on `EmitState` and this fix alone, the
natively built compiler takes 16.5 s and 1,000 MB on `checker.fern` against
12.0 s and 654 MB.
