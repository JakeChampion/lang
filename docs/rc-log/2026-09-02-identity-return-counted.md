# A call result that is the caller's own buffer is counted (#6644 distcheck, slice 4)

`2026-09-02-erased-accumulator-promotion.md` gave the walker frames a typed
accumulator, and the ownership flag from the slice before it then applied —
and `lexer.fern` still left 66,551 blocks under `__fern_arr_push`. The
promoted clone's every `return acc` now carried the bare-parameter
return-transfer retain (#4357), and the frame above it, rebinding
`out = fold_stmt_nodes(stmt, out, …)`, saw the same pointer come back and did
nothing. rc 2, and the next in-place append copied the whole buffer, and the
superseded one was never released: exactly one leaked buffer per grow, as
before, by a different route.

## The convention that was missing

Native's rule (`emitConsumedArrayOverwriteDec`) is that an rc-tracked
right-hand side hands the slot an OWNED reference, so a result equal to the old
pointer carries exactly one extra count and the rebind releases it. Native's
runtime makes every producer obey that: its in-place push bumps the count to 2
and the receiver's consuming dec brings it back. The self-host's push leaves
the count alone, so its producers disagreed with each other:

- `return p` (a bare borrowed array param) retained — a second reference
  leaves the frame while the caller's slot holds the first;
- `return p.append(v)` in place returned the same buffer with NO retain;
- a callee whose every call site was the self-move `x = f(.., x, ..)` skipped
  the retain by inference (#6074), because the caller's release was
  pointer-guarded and the count ratcheted otherwise.

A caller therefore could not know what a same-pointer result meant, and the
pointer guard was the only safe choice — which is the ratchet.

## The rule now

Every array-producing call result is a counted reference, and a rebind from a
call releases the old reference whether or not the pointer changed:

- **`lower_call_append`**: an in-place push on a borrowed parameter's buffer (or
  a borrowed match binding's) retains the result, guarded on identity with the
  receiver — a grow or a shared-receiver copy already hands back a fresh rc 1
  buffer. A declared `own` param is excluded, as in the bare return.
- **`emit_arr_store_from` / `emit_consumed_param_store`** take `same_dec`: at
  `x = f(…)` the old reference is released on a same-pointer result too
  (`rebind_call_same_dec`). For a flagged parameter that release ignores the
  flag — it cancels the callee's retain, not a reference this frame owns.
- The one exception is a declared `own` position holding `x`: the caller moved
  its reference in and the callee hands it back uncounted. `FnSigs.own_positions`
  ("fn|idx") carries that; a callee reached through a function value cannot
  declare `own`, so an indirect call releases.
- **#6074's inference is gone**: `move_arr_params_of`, the `mv_*` scan, and the
  per-module override in `wasm_ir`. Its job — keep a self-move chain at rc 1 —
  is done by the caller's release now, uniformly, and without the caller having
  to know which callees skipped their retain.

Only a direct call to a named free function is treated this way; a method call
or a value block keeps the pointer guard, since no rule says what a same-pointer
result from those carries yet.

## Measured

Reduced fold probe: unchanged at 336 of 386 (the flag alone closed it — this
slice keeps it closed once the leaf's identity return is counted).

`checker.fern`, leak-check-instrumented builds, `-emit asm`:

| compiler | allocs | frees | live_bytes |
|---|---|---|---|
| stage0 — native-built | 19,645,369 | 12,987,673 | 441 MB |
| stage1 — flag + promotion | 20,102,293 | 5,094,478 | 3,078 MB |
| stage1 — this slice | 19,972,656 | 5,137,670 | 2,962 MB |

`lexer.fern`: 934,293 allocations, 236,633 frees, 70.0 MB live.

Small again, and this time the attribution says why with numbers rather than
a mechanism. A gdb breakpoint on the grow allocation inside `__fern_arr_push`
in the traced stage1, recording each new buffer and its Fern caller, paired
against the trace's frees: of the 12,997 buffers `assign_target_into` grew,
12,578 survive — one per walk. The generations in between are freed now (the
419 that died are the second and later grows of walks over blocks with more
than four assignments); what survives is the LAST buffer of each walk, the one
`body_assign_targets` returns and its caller hands straight to
`util.index_of_str` as an argument. That is the argument-temp class native
closed in `2026-09-02-consumed-array-arg-temp.md`, and with `body_assign_targets`
running at the head of three release analyses on every nested block it is
13,405 buffers on `lexer.fern` alone. Its own slice.

The sanitized stage1 (`FERN_SANITIZE=1`) compiles `lexer.fern`, `parser.fern`
and `checker.fern` with no use-after-free and no over-release; the first
version of this slice had one, and it is worth keeping: the promoted
`fold_stmt_pruned` passes `(s: ast.Stmt, a: T) => a` as its statement visitor,
the monomorphiser substituted only the lambda's BODY, so the lifted identity
lambda kept an untyped `a` and returned it without the retain — and the frame
above released a count that was never added. `subst_expr` now substitutes a
lambda's parameter and return annotations too.
