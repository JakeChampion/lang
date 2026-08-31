# The last class was a `lea`, not an allocation (2026-08-31)

`2026-08-30-result-axis.md` left one class the certifier still reported
against the runtime oracle: `make_closure`, 102 of 109 findings over the
census-clean fixtures, and *"genuinely open in both directions"* —
either the walk was missing a transfer or lowering was missing a drop,
since closure reclamation is on `docs/TEST-GATES.md`'s live gap list.

Neither. It was a third thing.

## The trace

One flagged function, read end to end per the rule #7787 extracted:

```
v14 = make_closure                                   ; 32 bytes at rc=1?
v15 = call "__method_Box_map__i32__i32", v11, v14     ; borrowed
v16 = const_int 0
v17 = call "__fern_rc_dec", v16                       ; decs NULL, not v14
```

That reads as a plain leak: a closure allocated, handed to a callee that
borrows it, and a drop emitted against the wrong operand.

**The runtime says otherwise.** `generic_method_typeparams` is pinned at
**0 unpaired allocations**, and re-running it under `FERN_RC_TRACE`
confirms 0. So the reading was wrong, and the aggregate would have been
wrong with it — which is the whole reason for the rule.

## What `ssa.OpMakeClosure` actually is

Three IR ops lift to it, and the lift says so at the third:

> A bare function value is a zero-capture closure: it produces the same
> `{fn_idx, env_ptr=0}` cell an `OpMakeClosure` with no captures would
> (see `internal/ir/inline_zero_capture.go`, which rewrites one to the
> other). Lift it to `OpMakeClosure` with zero captures so it derefs
> identically to a real closure through `OpCallIndirect`.

`ir.OpConstFunc` is a **`.rodata` cell** — `lea rax, [rip +
__closure_cell_<name>]`. `ir.InlineZeroCaptureClosures` rewrites a
zero-capture heap closure into one *precisely to avoid the allocation*.
So the pass had already done its job; the lift then re-spelled the
result as `OpMakeClosure` for dispatch uniformity, and `UnitsOf` read
every one of them as a fresh allocation.

The `const 0; rc_dec` beside it is the same non-allocation seen from the
other side: there is no unit, so there is nothing for the drop to name.

## The fix, and why it is at the lift

A static cell and a zero-capture heap closure are indistinguishable
downstream — same kind, same `Str`, no captures either way. The lift is
the only place that knows, and it was discarding the fact. `Op.StaticCell`
records it; `UnitsOf` files a marked cell with the enum sentinels and the
vtables, as an address in `.rodata` that nothing can release.

Merging the kinds stays right: every consumer that dereferences a
closure wants them uniform. Only reference counting needs them apart.

| census-clean fixtures | flagged |
| --- | --- |
| before | 2.05% — 15 of 730 |
| after | **0.96% — 7 of 729** |

## Every named class is now closed

| class | cause |
| --- | --- |
| `enum_sentinel` | a static `.rodata` cell read as a unit |
| `alloc`, `call` | a unit threaded through a loop is disposed of under the PHI's name |
| `make_closure` | `ir.OpConstFunc` lifts to `OpMakeClosure`, and that cell is `.rodata` too |

Three of the four are the same mistake in different clothes: **an
address that cannot be freed, counted as a unit.** The fourth is its
mirror — a unit that can be freed, under a name the walk was not
watching.

## What is left, and why it is not a class

7 findings in 7 functions, all a consumed parameter the walk never sees
released, one each rather than a shape repeated across fixtures. That is
what a residue looks like once the classes are gone. It is either the
solver marking a parameter consumed that is not, or a leak the census
cannot see because the fixture does not take that path. Deciding needs
one traced end to end — and on this analysis, nothing else counts.
