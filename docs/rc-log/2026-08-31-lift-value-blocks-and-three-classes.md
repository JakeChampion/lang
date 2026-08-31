# The certifier was walking 78% of the program (2026-08-31)

`docs/rc-log/2026-08-31-certifier-reaches-zero.md` closed with three
bounds on what zero meant. The third — *"360 functions still fail to
lift"* — turned out to be the one hiding defects, and all three of them
were in the ownership signature table rather than in the walk.

## One refusal, 1157 functions

Every post-battery lift failure over the conformance corpus is the same
error, and nothing else:

| | lifted | failed | coverage |
| --- | --- | --- | --- |
| raw `ir.LowerWith` | 5316 | 0 | 100.00% |
| after the native pass battery | 4159 | **1157** | **78.24%** |

1127 are `BlockTypeI32`, 30 `BlockTypeI64`. The producer is `ir.Inline`
(`inline.go:452`): a callee with a mid-body `return` is spliced inside a
wrapper `OpBlock` carrying the callee's result type, and each translated
return becomes an `OpBr` that carries the value out. `ir.Inline` runs
twice in the battery.

`OpIf` has merged a value across its arms since the lift was written;
`OpBlock` refused one outright. `endBlockScope` needed the same phi
merge `endIfScope` already had, and `brSource.stackTop` was **already
being recorded** for a non-void `OpBlock` target at both `OpBr` and
`OpBrIf` — only the scope-closer was missing.

> lift failures **360 → 0**, functions walked **727 → 1087**.

The one case with no precedent to copy: when every path leaves through
`OpReturn` the post block is unreachable, and a value-producing block
still owes the operand stack a value or every later op reads one too
few. It is defined **in that block**, not via `undefValue` — entry
dominates the reachable blocks and this one is not among them, which
`Verify` says out loud if you try.

## The 360 that reappeared carried 46 findings

The gate demands zero against fixtures the runtime census pins clean, so
the newly-visible fifth of the program was a real correctness check
rather than a bookkeeping one. It reported 23 functions, 46 values.

Three causes, each traced to one instance end to end before anything was
claimed about the population, and each fixed rather than filtered.

### 1. A callee that stores a fresh box and returns it too (21 of 23)

`__map_grow_keyed` allocates `newBuf`, does `__store_ptr(m, newBuf)`,
and returns `newBuf`. Nothing retained it, so the store is what the
map's ownership rests on and the return hands back a **borrow** of what
the map now owns. `ownership_returns.go` classified it `ReturnOwned`, so
every caller accounted for a unit it never acquired; `__map_set_keyed_impl`
drops the result and read as leaking it.

`classifyValue`'s load case already declines to call a field read owned,
in as many words — *"claiming it owned would tell every caller to
release something it never acquired"* — and the fresh case simply never
asked the mirror question. It does now, and the answer is UNKNOWN rather
than borrow: the value really is a borrow, but of a container reachable
from a parameter rather than identical to one, and `ReturnBorrowedFrom`
can only name a parameter position.

Renames are deliberately not followed. Storing `__fern_rc_inc(p)` gives
the container a unit of its own and leaves p's with the function, so
only a store of the value ITSELF transfers.

Cost: 1537 → 1558 unplaced call results. 21 proofs withdrawn, and they
were wrong.

### 2. A parameter released through a loop-carried phi

`nested_call_temp` — `sum(dup(dup(build(6))))`. The runtime pairs all
18 allocations; the walk reported two leaked.

`dup(xs) = Cons(h, dup(t))` is TRMC'd into a loop, so the parameter
becomes the first incoming of a loop-carried phi whose other incoming is
the tail loaded from the previous box:

```
b2  v5 = phi(v1_param, v18_tail)
b9  __fern_rc_is_unique v5
b12 __fern_box_free v5        # unique
b13 __fern_rc_dec v5          # shared
```

`aliasesOf` does not cross phis, and rightly so — a phi's incomings are
usually different objects, which is exactly the case here. But the
release is real: whichever edge the parameter arrives by, the phi's
value on that edge IS the parameter, so a release of the phi releases
it. `certify.go`'s `phiFeeds` already states that rule from the other
side; `unitCarriersOf` is the alias-set half of it, opted into by the
solver alone in the same way the return edge is.

Without it `dup` reads Borrowed and tells every caller it still owns an
argument the callee consumed — the direction that costs a leak, and
`ownership_solve.go`'s own doc had already priced this limitation at
three parameters. It is worth more than three.

### 3. `__closure_drop_` was in no drop table

`main` in `closure_capture_shared_cell` calls
`__closure_drop___closure_lambda_1(env)` and the walk saw nothing: the
name is in neither `generatedDropPrefixes` nor `generatedDropNames`, and
not in `rcUnmodelled` either, so the call was not even poisoned.

It does not start `__drop_`, which is how it came to be missed by a list
whose every other member does. The thunk `rc_insert.go` synthesises ends
unconditionally with `__fern_closure_drop(arg0)`, so it releases
argument 0 exactly like the rest of the family, and its result axis
(`RcResultOperand`) is right for the same reason.

The asymmetry it removes: `elide.go` already rewrites some thunk calls
to the generic `__fern_closure_drop`, so the identical release was
understood under one spelling and invisible under the other.

## The trap under it, which is not fixed

The solver is blind to the whole synthesised-thunk family, and the
rcsigs table was masking it. `__drop_struct_*`, `__drop_enum_*`,
`__drop_tuple_*`, `__drop_closure_value` and `__drop_arr_closure` all
declare their pointer parameter as `ast.NumberType{}`
(`rc_insert.go:2365, 2474, 3224, 3306, 3401, 3676`), so
`IsPointerWidth()` is false, `ParamAddrs[0]` is false, and phase A
short-circuits at `if !sig.Pointer[i]` without ever asking
`demandsUnit`. Verified by patching `ParamAddrs[0]` after the lift: the
solver derives `consumed` on its own the moment it is allowed to look.

`__closure_drop_` was simply the member the table did not cover. The
next generated drop that is added and forgotten reproduces this exactly.
Fixing the declared type closes the class but moves `widthOfAstType`
from 32 to pointer width for those thunks, which reaches backend
parameter widths — filed rather than ridden along.

## What zero means now

Same three bounds, one of them retired:

- **Corpus-bounded.** Still 323 conformance fixtures on x86-64, not the
  self-host compiler.
- **Path-bounded.** Still: the census observes the path each fixture
  takes.
- **Coverage-bounded.** 0 lift failures and 1087 of 1087 functions
  walked. What is left is 1558 `UnitUnknown` call results and 5253
  poisoned roots, and the gate's floors are what keep those honest — the
  lift floor is re-banked from 55% to 99%.

Nothing here changes emitted code. Every consumer of the two tables
touched is an analysis or a verifier; the rc corpus per-case byte pins
and the conformance census are unmoved.
