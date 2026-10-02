# 2026-09-29 — a parameter handed out through a local is copied at the call (#10680)

Typed path (`semsource.fern`, the `handers` set that decides which callees
are handed a copy of a lent `str` view instead of the frame-resident retag).

## Found by

The self-host test drivers moved from the Go x86-64 backend to the pinned
stage0 compiler. The pin-built `asm_run` and `asm_ir_run` drivers then
segfaulted on every program with a string-payload match inside an `if` or a
`while`; the Go-built drivers never had. Native's `slice_unchecked` copies,
so a view stored past its frame is only observable in self-host-built code.

gdb on a symbolised driver: the fault was in `util.hash_bucket` under
`irverify.index_row` reading `op.str`, a box with rc −1 inside `lower_func`'s
frame, which three later calls had reused.

## Change

`hands_back` decided whether a callee keeps a lent argument by reading only
its `return` expressions. `emit_opt_payload_drop_via` in `irlower.fern` is

```
let st: St = s;
st = st.emit(ir.op_call_direct(freefn, 1));
return st;
```

so its `return st` names no parameter, it was not in `handers`, and its caller
lent `freefn` as a view. `hands_back` now collects the body's `let` and
bare-name assignment bindings (`bound_exprs`, through
`astwalk.fold_stmt_spine`, so nested blocks and the `if` a `defer` desugars to
are included) and closes the parameter names over them to a fixpoint before
reading the returns: a name bound from an expression that hands a current
name out joins the set. The set only grows, so no declaration leaves
`handers`; the direction that moves is retag → copy.

Binders `bound_exprs` does not collect: `for` variables, match and `if let`
payloads, tuple destructures, `use` continuations. No store through one of
them in the compiler's own sources keeps an uncopied view today.

## Witness

`TestSelfHostSemanticProduction/lent-view-kept-through-a-local`: `add`
binds `st` from its parameter, rebinds it from a call that stores the lent
`name`, and returns it; `build` passes `slice_unchecked(src, 6, 11)`; `main`
reads the stored item after three calls have reused the frame. Reverting the
`semsource.fern` hunk alone gives `with = "1|", without = "0|"` on both x86-64
legs: the AST oracle answers 0, the semantic path 1, and the production report
still says 6 of 6.

## Measured (x86-64, pin-built drivers)

Every row of `.github/selfhost-driver-sizes.txt` moved +0.01 to +0.05% for
the copies the widening adds; `asm_ir_run.fern` built by stage0 took 28.9 s /
4.10 GB against 28.6 to 29.0 s / 4.10 GB before the change, same output size.
Since the drivers are the pin's output, the fix reached them only through a
pin refresh (`stage0-20260929-32d575e`), which is the cost a codegen fix the
compiler's own source depends on now carries.
