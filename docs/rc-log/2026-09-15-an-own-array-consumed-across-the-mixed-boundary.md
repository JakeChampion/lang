# An `own` array consumed across the mixed boundary

The first half of #9407. The self-host compiler built through the semantic
lowering emitted the wrong assembly for `lexer.fern`, and `FERN_SANITIZE=1` on
the produced compiler aborted with a use-after-free. The abort was this
defect; the wrong assembly turned out to be another, silent under the
sanitizer (`2026-09-15-a-lent-view-is-copied-for-a-callee-that-keeps-it.md`).
`FERN_RC_TRACE=1` paired the touched block with its history:

| event | site | caller | one above |
|---|---|---|---|
| allocated | `__fern_arr_push` | `astwalk.ident_of` | `collect_idents_stmt$wrap1` |
| freed | `__fern_arr_push_owned` | `collect_idents_stmt$wrap0` (the produced `assign_target_of`) | `fold_stmt_nodes[string[]]` (a produced instance) |
| touched | `irlower.grow_ident_census` | | |

So: an AST-lowered caller (`grow_ident_census`) moved its accumulator into
`collect_idents_stmt`'s `own` parameter, which passed it straight on to the
produced instance, whose consuming push reclaimed the superseded buffer. The
AST caller then released its old binding on the rebind — a second release of
a unit it had transferred.

## Why the AST lowering does that, and why it does not bite AST against AST

The AST lowering implements a real move only for a scalar-element `own` array
(`own_scalar_arr_positions`: the caller clears its slot, the callee cleans the
buffer up). A pointer-element `own` array has no element-unit protocol there:
the callee never frees the superseded buffer (the load-bearing leak in
`__fern_arr_push`) and never releases the parameter at exit, so the caller's
rebind release after the call is what frees it. Two frames releasing one unit
is the same defect in the all-AST program of #9409, where it is silent because
the second release lands on a buffer the callee leaked rather than freed. A
produced body honours the declared contract — the push reclaims, the exit
releases — and the caller's release becomes a use-after-free.

## What landed

The contract, in the registries an AST caller already reads: a produced free
function's counted array parameters are `own_consumed_positions` rows
(`ssarc.consumed_array_rows`, an instance keyed by its template's name since
that is the name the call spells), closed over the AST-lowered functions by
`irlower.own_consumed_positions_of` — one that passes its own `own` array
parameter on to a consuming position in a dying shape (`grow_dying_passes_stmt`:
`return g(p)`, `x = g(p)`) no longer holds the buffer either, so its callers
transfer theirs too. At a consuming position the caller does what it does at a
scalar one: an owned local moves and its slot is cleared, a borrowed one buys
the callee its reference, a fresh temp is not freed after the call
(`callee_consumes_own_array`).

With no produced body the set is empty and the AST output is byte-identical,
which the fixpoint is built on.

Twenty lines reproduce the boundary (`own-forwarded-into-produced` in
`TestSelfHostSemanticProduction`, whose skip leg keeps the two forwarders on
the AST lowering): all-AST and all-produced were clean, any AST frame between
`census` and the produced instance was a use-after-free under the sanitizer,
and every configuration answers 62 now.

## Measured

The sanitized produced compiler compiles `lexer.fern` without an abort. Its
assembly still differed from the AST build's by the same 150 lines, which is
how the second defect was told apart from this one.

## What it leaves

- #9409, the all-AST double release. A pointer-element `own` array is still
  "borrowed with the caller releasing after" between two AST-lowered frames,
  which is sound only while the callee leaks; the semantic lowering's
  consumption is the fix, function by function.
- A produced instance calling an AST-lowered callback through a function value
  (`skip=visit` on the reproducer) is not pruned, since `prune` reads direct
  call names, and leaks the superseded buffer the AST callback abandons: 328
  bytes against the AST build's 232 on the reproducer. A leak, in the safe
  direction, and the same gap as any produced caller of an AST callee.

## Traps

**Peak and exit code are not a measurement of a compiler.** Every earlier
figure for the produced compiler read those two; the wrong assembly had exit
0 and a lower peak. Diff the emitted text against the AST build's.

**The sanitizer's abort names the toucher, not the freer.** The rc trace's
`f` line for the pointer in the abort's registers is what names the frame that
released it, and with `FERN_RC_TRACE_DEEP=1` the frame above that, which is
where the produced instance was.

**A refused body can still be on the path.** Both frames on the sanitizer's
stack were AST-lowered (`uninstantiated generic`); the produced body was two
calls down, reached through a template whose INSTANCE the AST caller names by
its mangled symbol.
