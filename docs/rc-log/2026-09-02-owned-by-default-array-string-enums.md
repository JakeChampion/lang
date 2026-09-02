# 2026-09-02 — owned-by-default reaches array- and string-carrying enums (#8056)

`isOwnedByDefaultType` admitted only string/array-free, uniform-box enums
(and structs / tuples), so a HAMT or vector node was a borrow inside every
update and its child array copied on every level of the unique path. The
gate now asks the question it meant to ask — is the deep drop WIRED
(`typeDeepDropWired`, the predicate the consumed-threaded promotion already
used) — and drops the uniformity requirement, since the exit sweep's tag
switch and a consuming arm both know the box size without it. Six further
things had to change for the unique path to actually fire, and each is a
trap the widening only made visible.

## The count must be transferred, not retained, to reach the callee unique

An owned-by-default argument was always retained at the call site
(`emitAliasInc` unless `moveSites`), and no analysis ever produced a move
site for a call argument — Perceus's dup elision existed only for explicit
`own` params (`ownCallMoveArgs`). So `t = with_in(t, ..)` handed the callee
rc 2, the consuming match took its shared branch, and everything copied.
`computeOwnedArgMoves` now moves a frame-owned ident that dies at the call
(`callArgDeaths`: the self-reassign and `return f(.., x, ..)` shapes, a
sole-use param outside loops) and nulls its slot; every later release of
the slot meets the null. `frameOwnsIdent` is deliberately not
`freeEligible` — that asks whether the frame may FREE the value; moving
needs only that it holds a reference — and excludes borrowed views and
their sources (`borrowedAlias` / `borrowSources`).

## An owned parameter tainted by an alias kept the caller's count forever

`var cur = root; cur = kids[i]` — the walk every tree lookup is written as —
taints `cur` (a reassign from a borrowed payload) and the backward alias
propagation carries that taint onto `root`. Under the borrow model that only
withheld a precise drop; under the owned model it withheld the exit release,
so `pvec.get_or` stranded one whole trie per call. `computeFreeEligible` now
tracks `escaped` (the uncounted sinks) separately, and an owned-by-default or
consumed param spends its count unless it escaped. `own` params keep the
old gate. This is the class the "sound leak" wording in the owned-model
comments described; with array-carrying types owned it stopped being rare.

## The escape analysis followed `var` initialisers only

`root = ins(root, .., k, v)` carries `k` into `root`, which is returned, but
`paramEscapesInFn` tainted only `*ast.Var` initialisers, so `k` read as
borrowable and the caller's fresh key temp was never released (one box per
insert with a struct key). An `*ast.Assign` to an ident now gets the same
slot-typed reachability test.

## `arraySetConsumed` skipped the sweep on every path

The consumed `.with` receiver was excluded from the exit sweep by NAME, so a
`return` before the `.with` on another path never released the buffer — 150
blocks on a 100-call probe, pre-existing on `main`, and what the
`Branch(bm, s + 1, kids.append(..))` early return in `__hm_insert` hit at
once when its binding became consumable. The consuming site now nulls the
receiver's slot (`arraySetConsumedSites`, emitArraySet) and the sweep runs
everywhere; `arraySetConsumedReinit` and its dominance analysis are gone
with it — the re-init drop meets the null too.

## `.append` on a borrowed match binding had no guard

`.with` on a binding of a non-consuming match forced the copy since #8043;
`.append` did not, and grew the caller's array in place when it had room
(interpreter 33, natives 34 on `append_on_borrowed_match_binding_copies`).
`borrowedBindings` is one set now, consulted by both.

## The grow bracket skipped owned-by-default positions and field arguments

The #4873 containment bracket exempted an owned-by-default position on the
grounds that the caller retains the argument — but the retain is on the BOX
and the field buffer inside stays at its own count, exactly the hole the
2026-08 audit measured (`var c = a.push(3); a.size()`). And a field-chain
argument (`push(h.b)`) was never bracketed at all. `bracketArgPath` resolves
a field chain to the root slot plus the field offsets; the owned-by-default
exemption is gone.

## Release code is cold: call it, never inline it

Two text regressions, one cause. With the sweep reaching every owned-array
function, `ir.Inline` copied `__drop_arr_enum_*`'s element walk into each
of its call sites — a 491-op function became 3,442 ops, and the three
collection benches' text went 2-3x. Generated drop helpers now carry
`InlineHintNever`, which also took the benches far BELOW their old
baselines (`ordmap_insert` 61,338 → 5,061 lines): the baseline had been
paying the same duplication for box-only enums. The self-host driver was
untouched by that fix — it exceeds `inlineMaxUnitOps`, so `ir.Inline`
never runs on it — and still grew 16% (5,659,727 → 6,572,602 lines): the
exit sweep inlined a many-variant enum's tag-switch drop at every exit of
every function owning one (`checker.check_expr` 2x, `asmcore.ty_tag`
25x). The sweep now inlines only a switch of at most `sweepInlineMaxArms`
(3) payload-carrying variants and otherwise calls the generated
`__drop_enum_<Name>` like the per-iteration sites always did. Calling it
for EVERY enum cost the benches 3-14% retired instructions (`ordmap_insert`
607M → 694M), which is what the bound is for: the driver's text ends at
4,370,342 lines, 23% BELOW the baseline, and the benches keep their
inline drop.

## A captured value is an alias too

The widening made `eval.Ctx { decls: Decl[] }` owned-by-default, and the
first closure that forwarded its captured `ctx` to such a param
(`examples/vcl/machine.fern`'s `interpreting` runner) freed the env's
reference on the callee's exit sweep: `needsRcIncOnAlias` admitted
`Ident` / `FieldAccess` / `Index` and declined `CaptureRef`, so the
call-site retain never fired for a capture. The second run of the closure
was a use-after-free — only `TestExamplesNoCrashX86_64` and the self-host
driver (which segfaulted on every compile) saw it; the rc corpus, both
leak gates and the fixtures were green. `CaptureRef` is now an alias like
the other three; `closure_capture_passed_to_owned_param` pins it in the
corpus.

## The ownership solver counted retains and releases over the whole body

`ssa.SolveOwnership` calls a parameter consumed when the body "releases
it without a matching retain", and it read both as booleans over the whole
function. A retain on one arm and a release on another was balanced. Under
the borrow model that shape was rare on a parameter; under the owned model
it is the ordinary one: `return a` on an owned parameter is a transfer inc
beside the exit sweep's drop, and the arm that rebuilds instead just drops
(`__rx_count`), or `var cur = it` retains the parameter and the exit sweep
drops both (`iter.filter`). The solver called each Borrowed, so every
caller that had MOVED its argument into the owned position — the slot
nulled, the exit drop landing on a constant 0 — read as still holding it,
and `TestX86_64CertifyAgreesWithTheLeakCensus` flagged eleven functions in
fixtures the runtime proves clean.

`demandsUnit` is now a per-path balance: retains against releases, moves,
hand-offs to a consuming callee, stores and closure captures, met by
minimum at joins, clamped so a net-release loop settles, worklist-driven
(a full sweep per pass was quadratic on the parser's largest functions:
the self-host differential went from 140 s to over 25 minutes before the
worklist, 75 s after). A path that ends below zero is a demand. Two
things are deliberately outside the count. The return: the typed lowering
pairs it with an inc either way, and the untyped `usize` helpers under
`core/map` return raw words, so counting it called `__map_get_or_impl`'s
fallback consumed and then held on the hit path. And a phi's non-carrier
edge: a phi the parameter's alias feeds on one edge and a reassigned
accumulator on another is released once at exit, and on the rebuilt path
that release spends the fresh value's unit, so the edge is credited with
it (any origin but a borrow — the `__fern_arr_push_grow` family is
unmodelled and every threaded array accumulator flows through it). The
self-host differential reads 95.10% agreement against main's 95.22%, with
the `i32[]` solver-only bucket unchanged.

## What it measures

Unique-path re-inserts (2,000 keys / 5,000 `.with`), fresh bytes over the
whole loop: `std/pvec` 1,168 → 0 B, `std/pmap` 224 → 0 B, `std/ordmap` 48 B
unchanged. `examples/bench/pvec_with` 1,118,676 → 642,593 allocations,
`pmap_insert` 693,584 → 438,649; retired instructions −47% / −57%. A
bare payloadless variant in argument or struct-field position
(`kids.with(sub, Empty)`) is a function-value reference to the self-host
lowering, which refuses the module; the library spells it through a typed
local (`var e: VNode[T] = Empty;`) as the rest of the stdlib always has,
and that local's sentinel inc/dec is the difference between these numbers
and the bare spelling (−52% / −59%). The
library shapes that reach the path are in `docs/PERSISTENT-COLLECTIONS.md`
("Shapes the library uses on purpose").

## Still open

- `closure_capture_passed_to_owned_param` is value-correct and both leak
  gates pin it at 64 B (x86-64) / 80 B (arm64): the pair and env of the
  closure local `run` its `main` calls in a loop, the shape
  `closure_local_passed_to_callee_released` records
  (`docs/rc-log/2026-09-02-persistent-collections-residual-leaks.md`).
- A consuming-match binding passed at its last use to an owned position
  (`var nl = __om_insert(l, ..)`) is retained, not moved — `callArgDeaths`
  admits only params and call-initialised locals for the sole-use shape, so
  the ordered map's descent is unique at the root only.
- A field read passed straight into a call (`f(v.root)`) is retained; the
  library takes the field out first. A struct-destructuring move is the
  compiler-side answer.
- `pset` / `ordset` and the `remove` / `union` / `filter` paths keep their
  pre-existing shapes; the `remove` key temp leak (100 blocks in the pmap
  probe) is the known "method-call temp" gap, unchanged.
