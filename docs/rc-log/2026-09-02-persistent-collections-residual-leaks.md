# 2026-09-02 — the persistent collections' residual leaks (#8057)

`docs/PERSISTENT-COLLECTIONS.md` listed what the sanitizer still showed on
`std/ordmap` / `std/pvec` after #8043. Four of its five numbers were one leak,
and one of its shapes was already clean. Native x86-64, `-sanitize`, blocks
at exit; every "after" is 0 except the closure-local row, which stays where it was.

| probe | before | rule that declined |
|---|---|---|
| `m = m.update(k, (v: i32) => v + i)`, 100 iterations | 302 | `freshOwnedRcTempType` had no `MakeClosure` arm |
| `apply(i, (v: i32) => v + i)`, 16 iterations | 33 | same |
| named nested fn / lambda local passed to a callee, 4 rounds | 16, **still 16** | ElideClosurePair downgrades the sweep's thunk to `__fern_closure_drop`; the fix UAFs the self-host, see below |
| `a.union(b)`, 1,000 + 1,000 keys | 960 | `returnsOwnBox` refused `Tip => return t` |
| `a.filter(...)`, 1,000 keys | 1,368 | same |
| `s.concat(mk(2))`, 4 rounds (struct temp to a pointer-returning method) | 8 | `paramCountedRetain` refused `other.tail[0]` and `other.get_or(...)` |
| `a.union(b).len()`, 100 + 100 keys | 45 | none — those were the union nodes |
| `pvec.with` below the tail, snapshot held | 0 | none — the doc's 326 was stale |
| `examples/tests/ordmap_test.fern` | 562 | 7 remain, see below |
| `examples/tests/pvec_test.fern` | 4 | 4 remain, see below |
| `examples/tests/ordset_test.fern` | 14 | 4 remain: string-keyed nodes, and the lambda `filter` wraps |
| `examples/tests/pset_test.fern` | 3 | 1 remains: a string-element `filter` |
| `examples/tests/pmap_test.fern` | 105 | 103 remain: 100 `Coarse { … }` key temps handed to `remove`, 2 string-valued leaves |

## The rebuild leak, and what the trace does not say

Every unpaired node in the union / filter traces carried an `a` line and
nothing else: never retained, never released. The site was `insert_min` or
`join` and the caller `join` or `union`, which is where the node was BUILT,
not where it was dropped on the floor. That frame was `__om_filter` /
`__om_union` binding `var fl = __om_filter(l, pred)`: the callee returns its
bare parameter on the `Tip` arm, `returnsOwnBox` refused every bare parameter
return, so the binding kept the conservative call taint and the exit sweep's
ineligible path only flat-dec'd it — a node `join` did not keep was never
freed.

The refusal was written for a BORROWED parameter (the query_parse regression
its comment records). An owned-by-default parameter is a different contract:
the caller retains the argument, the callee's sweep releases it under
`is_unique`, and the Return lowering's transfer inc is therefore the caller's
own reference. `findReturnsFreshBox` now takes the lowering's own ownership
ladder (`paramVerdictFacts`, the same `paramVerdict` the builder reads) and
credits exactly those parameters; a borrowed one stays refused.

## Closures

A lambda in argument position was never a temp the stage-(b) reclaim knew: the
`MakeClosure` arm of `freshOwnedRcTempType` is new, and the release goes
through `__drop_closure_value` — the pair's own drop-fn pointer — because a
temp has no local name to route a per-closure thunk through. That reader also
keeps the slot out of ElideClosurePair, so the pair the drop dereferences is
real.

A closure LOCAL passed to a callee is a separate hole, and it is NOT closed.
Its slot cannot elide (the call is not a canonical reader), and the pass then
downgrades the sweep's `__closure_drop_<name>` to `__fern_closure_drop` —
"captures of such (rare) closures leak". A callback is not rare. Routing the
downgrade through `__drop_closure_value` (the pair's own drop-fn pointer)
clears the probe, both native leak gates and the whole native rc corpus — and
the self-host compiler built with it dies compiling `iter_test.fern`,
`option_combinators_test.fern` and `result_combinators_test.fern`:
`fern-sanitizer: use-after-free` inside `__drop_struct_checker__Scope`, on
the overwrite of the scope `build_func_scope` returns in `check_module`. A
closure a `Scope` field still holds was released through that path first, so
some owner of it is uncounted; bisecting the four changes with a build-time
toggle isolated this one (the other three each leave the driver clean). The
second owner is not identified — the tracer reports incs and decs on the
data pointer and allocs on the block address, so a per-pointer history needs
that offset normalised before it says anything — and the downgrade is left as
it was, with the shape pinned at 384 B on both gates.

A pointer-returning callee (`update` returns the map) admits the closure temp
per position, which needs `paramCountedRetain` to credit a function-typed
parameter: one that is only ever CALLED. That arm was missing, and its absence
also cleared every scalar position of the callee.

## Counted retain: bindings are projections

`paramProjectionsSafe` refused any parameter that was a match scrutinee, which
is every tree function there is. A payload binding is an uncounted alias of
the parameter's interior, so the analysis now tracks the bindings of every
match over a tracked name and holds them to the same rules; the scrutinee
occurrence is then a non-retaining read. Scalar element reads (`p.tail[i]`,
`xs[i]` on a tracked binding) and an element read straight into a counted
position are credited alongside. `TestVariantPayloadStoreIsCountedRetain`'s
`scrut` row flipped from refused to credited; its bindings are unused there.

## What remains, measured

- `ordmap_test`, 5 blocks: string-valued / string-keyed nodes found by
  `match (__om_find(m.root, k))` in `get` / `get_or`. `__om_find` returns the
  node with the transfer inc and `reclaimableMatchScrutinee` refuses a
  scrutinee whose arms bind pointers, so the reference is never released. The
  i32-valued twin (`m6h`) is clean — the enum is owned-by-default there.
- `pvec_test`, 2 blocks: `s.concat(v.slice(0, 2))`. `concat`'s `other` is
  refused because `to_array` reaches `__pv_into_elems(xs, i + 1, acc)`, and
  `inferParamCountedRetain`'s least fixpoint cannot ground a parameter a
  function passes to itself. A greatest fixpoint would credit it; that is a
  change to the summary's contract, not to a rule.

## Banked

`pair_form_enum_temp_as_argument` 1,312 → 288 on both native leak gates.
(`string_closure_capture_aliased` reads 0 with the reverted closure-local
release and stays at its 16 / 48 pins without it.)

## Traps

- A leaked block with only an `a` line in the trace was never touched again;
  its `site` / `caller` name the producer, and the frame that owed the free is
  the one that BOUND the producer's result. Read the callers of the caller.
- The doc's "method-call temp used as a receiver" bullet named `a.union(b).len()`;
  a plain struct-returning method with a scalar consumer (`m4`) was clean
  before any change. Reproduce the shape without the library before naming
  a rule.
- The Fern `Map` type spells insertion `insert`, and a struct literal
  cannot infer `map_new(4)`'s type arguments — bind it to a typed `var`
  first. Both cost a unit-test round.
