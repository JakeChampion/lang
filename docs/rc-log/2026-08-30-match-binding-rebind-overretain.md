# Assigning a match binding over-retains it (2026-08-30)

The conformance leak census landed 134 leaking fixtures and 66,570
unpaired allocations. Following its site attribution down to a minimal
shape found one cause behind a large share of them.

The short version: `cur = v`, where `v` is a match-destructured binding,
leaves the value with a refcount of **2** where it should be 1. It is an
over-retain, not a missing release — and the route to that conclusion
ruled out three plausible readings on the way, each by measurement.

## The minimal reproduction

Twenty lines, no stdlib iterator, no generics:

```fern
function pick(n: i32): Option[i32[]] {
    if (n < 5) { return Some([1, 2, 3]); }
    return None;
}
function main(): i32 {
    var cur: i32[] = [0];
    var i: i32 = 0;
    var go: boolean = true;
    while (go) {
        match (pick(i)) {
            Some(v) => { cur = v; i = i + 1; },
            None => { go = false; },
        }
    }
    return cur.len();
}
```

**5 unpaired allocations of 6.** `fern-sanitizer: leak 160 bytes in 5
blocks`.

## It is not the match arm — it is the binding

The obvious reading is "an assignment inside a match arm skips the
overwrite-release". Measured, that is wrong. Three neighbouring shapes
are all clean:

| shape | unpaired / allocs |
| --- | --- |
| `while { cur = [7,8] }` | 0 / 6 |
| `while { if (…) { cur = [7,8] } }` | 0 / 6 |
| `while { match { Some(v) => cur = [7,8] } }` | **0 / 11** |
| `while { match { Some(v) => cur = v } }` | **5 / 6** |

The third and fourth rows differ only in the right-hand side. So the
overwrite-release is skipped specifically when the RHS is a
**match-destructured binding** — not when the assignment merely sits
inside a match arm.

## It is not a skipped release either

The natural next hypothesis was that a gate on the dec-on-overwrite
excludes this case — move-on-destructure marking the binding moved, say,
so the assign path treats the whole assignment as a move and skips
releasing the old value.

**Measured, that is wrong too.** Instrumenting the array branch of
`b.assign` (ir.go:16804) and compiling both shapes prints identical
gates:

```
h_match_rebind (LEAKS)  name=cur freeElig=true selfReassign=false selfPush=false moved=false
i_fresh        (clean)  name=cur freeElig=true selfReassign=false selfPush=false moved=false
```

The gate passes in both, so the dec-on-overwrite **is** emitted for the
leaking shape. The leak is therefore not a missing release.

That inverts the diagnosis. What is left is an extra RETAIN on the
destructure path: if matching `Some(v)` incs the payload and the
subsequent `cur = v` incs again as an alias, one dec cannot balance two
incs, and the box survives with a count of 1 per iteration — which
matches 5 leaked allocations over 5 iterations.

## Confirmed: the surviving box has a refcount of 2

`__rc_get` reads a live refcount, so the hypothesis is directly
testable. Returning it instead of the length, over a `u8[]` (the type
`__rc_get` accepts):

| shape | `__rc_get(cur)` | unpaired |
| --- | --- | --- |
| `while { match { Some(v) => cur = v } }` | **2** | 3 |
| `while { cur = [1,2,3] }` | **1** | 0 |

The value that should be solely owned is held twice. So this is an
**over-retain**, not a missing release: two incs against one dec on the
match-destructure path, and the box survives with a count of 1 after the
dec.

That is the direction #7782 was opened about — the one nothing gates,
which is consistent with its having survived this long.

## How it was found

Site attribution, not guesswork. The census records an alloc site per
unpaired allocation; mapping those addresses through the symbol table
named the functions:

| fixture | top sites |
| --- | --- |
| regex_captures_assert (54,713) | `__fern_arr_cow_inplace` 12,282, `__fn_regex____rx_addthread` 11,734 |
| generic_fnarg_typevar (1,850) | `__fn___method_iter__ArrayIter__i32_next` 1,100 |

The first reading — that the array grow/COW helpers leak — was **wrong**:
a loop of `append` past capacity and a `.with` on a shared array both
measure clean. The iterator attribution was the useful one, and led to
`core/iter`:

```fern
pub function filter[T, I: Iterator[T]](it: I, keep: (T) => boolean): T[] {
    var cur = it;
    while (go) {
        match (cur.next()) {
            Some(t) => { …; cur = t.1; },
            None    => { go = false; },
        }
    }
}
```

`cur = t.1` is exactly the shape. Measured directly:

| program | unpaired / allocs |
| --- | --- |
| `iter.filter(iter.of(xs), λ)` | 11 / 14 |
| `iter.map(iter.of(xs), λ)` | 7 / 10 |
| `iter.of(xs)` alone | 0 / 2 |

Every eager adapter in `core/iter` — `filter`, `map`, `enumerate` — is
written on this loop, so any program using an iterator pipeline leaks
one iterator state per element.

## Corroboration

`FERN_RC_TRACE` pairing and `FERN_SANITIZE` agree exactly on every case
measured here, as they did across the census: 11/7/0 blocks against
11/7/0 unpaired, and 5 against 5 on the minimal shape.

## Not fixed here, deliberately

## The extra op, and what it is not

Diffing the lowered IR of the two shapes leaves exactly one op:

```
match_binding (LEAKS)   op[58] rc.inc __fern_rc_inc      + 3x __fern_arr_dec
fresh_literal (clean)                                      3x __fern_arr_dec
```

One alias-inc, on the `cur = v` assignment. The obvious conclusion is
that this inc is the bug.

**It is not.** An ordinary local aliased in exactly the same position
gets the same inc and is correctly balanced:

| shape | `__rc_get(cur)` | unpaired |
| --- | --- | --- |
| `while { var other = […]; cur = other }` | 1 | 0 |
| `while { if (…) { cur = other } }` | 1 | 0 |
| `while { match { Some(v) => cur = v } }` | **2** | **3** |

`computeMovedLocals` explains why the inc is there and why it is right:
move-on-alias only fires for a TOP-LEVEL `y = x` whose read of `x` is
x's last occurrence, because the sweep-exclusion is global and an alias
nested in control flow might not run on every path. Its doc says so
outright — "aliases inside control flow keep their inc" — and the two
clean rows above are that rule working.

So the inc is correct. What an ordinary local has and a match binding
does not is the balancing half: **the source's own release**. `other` is
swept at scope exit; `v` is not.

## Only assigning the binding OUT breaks it

Matching and merely using the binding is clean, and so is ignoring it:

| shape | unpaired / allocs |
| --- | --- |
| `Some(v) => { total = total + v.len(); }` | 0 / 3 |
| `Some(v) => { i = i + 1; }` | 0 / 3 |
| `Some(v) => { cur = v; }` | **3 / 6** |

There is no enum box to blame. Grouping the trace by allocation size
shows **one 32-byte block per iteration and nothing else** in both
programs — `Some([1, 2, 3])` does not box the Option separately, so the
payload array is the only allocation on this path.

That makes the accounting exact:

- **used only** — payload starts at 1, is never inc'd, and the arm-end
  release takes it to 0. The lowered IR carries one `__fern_arr_dec`,
  which is that release. Clean.
- **assigned out** — payload starts at 1, `cur = v` incs to 2, and the
  next iteration's dec-on-overwrite takes it back to 1. It never reaches
  0. One leak per iteration, which is the 3 measured.

So the arm-end release that balances the used-only case is **absent**
when the binding is assigned out, while the alias-inc is still emitted.
Two half-mechanisms disagreeing: the suppression treats the assignment
as a move, the inc treats it as an alias.

It is **not** the documented safe leak for ineligible enums either:
`enumRcPayloadsEligible` excludes only enums transitively containing a
Map, and `ast.EnumRcPayloads` is on, so `Option[u8[]]` takes the counted
path.

## One iteration is enough

The bug does not need the cross-iteration overwrite at all. Varying how
many times the loop runs:

| iterations | `__rc_get(cur)` | allocations |
| --- | --- | --- |
| 1 | **2** | 2 |
| 2 | 2 | 3 |
| 3 | 2 | 4 |

The count is already 2 after a **single** arm execution. So the
imbalance is entirely within one pass — `Some([1,2,3])` allocates at 1,
`cur = v` incs to 2, and nothing in that arm decs it. The
dec-on-overwrite in later iterations is a red herring; it only ever
takes the count from 2 back to 1.

That is the tightest statement of the bug: **the alias-inc has no
counterpart inside the arm.**

## Why the move repair is plausible and still unverified

`computeMovedLocals` declines to move an alias nested in control flow
because its sweep-exclusion is global: an alias that might not run on
every path strands or double-counts the source. A match-arm binding
looks different — `bindingSlotScoped` returns a `restore` closure that
puts the name back after the arm, so `v` is unreadable afterwards and
there is no later path to strand.

But the same function reuses an existing slot of matching shape rather
than allocating per arm, so the SLOT outlives the name and is shared
across iterations. Whether a move-marking is sound against a slot the
exit sweep may also touch is exactly the question those guards exist to
answer, and it is not answered here.

So: the measurement is airtight, the repair is not. Suppressing the inc
where the binding is dead after the arm is the shape to try; proving it
holds against the reused slot is the work.

## Where that leaves the fix

The asymmetry is the finding: a pattern binding that holds a reference
is not released the way an ordinary local is, so an alias-inc taken
against it has nothing to cancel.

The two halves have to agree. Either the assignment is a move — and then
the inc must go, exactly the balanced inc+dec elision `computeMovedLocals`
already performs for top-level aliases — or it is an alias, and the
arm-end release must stay.

The move reading is the safer one to implement: removing an inc whose
matching release is already removed cannot over-release, which is the
argument `computeMovedLocals` makes for its own case. But which
suppression fires here, and whether it holds on every path out of the
arm, is not yet established — and `computeMovedLocals`'s own guards
exist because a move that does not run on every path strands or
double-counts. So this stops at the diagnosis: eleven measured shapes,
one op, and an accounting that adds up.

## The move repair was tried, and it is unsound

`markMatchBindingAliasMoves` — mark the assign a move site when the RHS
is a match-arm binding at its last occurrence, so the alias-inc is
skipped and the reference transfers. It reuses the existing
`b.rc.moveSites[node]` mechanism rather than adding one.

It works on the repro: `__rc_get(cur)` goes 2 → **1**, unpaired 3 → **0**,
and all three controls stay clean. `internal/ir` stays green.

**Then `TestArm64SSABackendDifferential` segfaults.**

```
examples/proposals/unidiff.fern
  one build CRASHED and the other did not —
  flat: signal: segmentation fault, ssa: exit status 0
```

289 agree, **2 diverge**, against 291 / 0 before. A use-after-free, which
is exactly the failure the caution was about: the argument for safety was
"a binding is swept by nobody, so there is nothing to cancel the inc
against", and that is **false for some shapes** — something does release
those bindings, and removing the inc over-releases.

Reverted. What it leaves behind is worth more than the patch would have
been:

- The repair direction is not merely unproven, it is **refuted**, with a
  named counterexample to work against.
- The soundness argument is refuted with it: bindings are not uniformly
  unswept. Any future attempt has to say WHICH bindings are swept and
  why, rather than assuming none are.
- The gate that caught it is the arm64 flat-vs-SSA differential, not
  anything rc-specific — worth knowing, since the rc suites and the leak
  census were all green on the broken compiler.

That last point is the sharpest: **the census, `internal/ir`, and the rc
e2e suites all passed a compiler that segfaults a real program.** A leak
gate cannot see an over-release; only running the program can.

The census now checks for a signal death, since it already compiles and
runs all 453 fixtures and was throwing the exit status away. Re-running
it against the broken compiler says plainly what that buys: **nothing,
for this instance.** No conformance fixture crashes; the crash was in
`examples/proposals/unidiff.fern`, which the census does not cover. The
check closes the SHAPE of the gap, not that occurrence of it, and
widening the corpus to `examples/` is what would close both.

## What this does not reach, either

The census is unchanged by the fix — still 134 leaking fixtures, 66,570
unpaired allocations — because no conformance fixture uses the bare
`cur = v` shape.

An earlier revision of this note claimed the `core/iter` adapters leak
*because of this bug*. **That attribution is wrong.** They are written on
`cur = t.1`, a tuple PROJECTION, and measured alone that shape has a
different signature:

| shape | `__rc_get(cur)` | unpaired |
| --- | --- | --- |
| `Some(v) => cur = v` (this bug) | 2 | 3 |
| `Some(t) => cur = t.1` (the adapters) | **1** | **4** |

The projection ends with the refcount CORRECT and leaks the **tuple
boxes** instead. Two defects in adjacent syntax; the `iter.filter`
11-of-14 measurement belongs to the second.

## The projection leak, localised

Site attribution through the symbol table names it: of the four leaked
boxes, **three are allocated in `step`**, one per call — the tuple
`Some((n + 1, [1, 2, 3]))` builds. The payload array is fine; `cur` ends
up solely owning it.

And the trigger is narrow. Three neighbouring shapes are clean, each
differing by one thing:

| shape | unpaired |
| --- | --- |
| `Some(t) => { i = t.0; cur = t.1; }` | **4** |
| `Some(t) => { i = i + 1; }` (binding unused) | 0 |
| `Some(t) => { i = t.0; }` (scalar field only) | 0 |
| the same, on a `(i32, i32)` tuple | 0 |

So it is **projecting an RC-TRACKED field out of the binding** that
strands the box — not destructuring, not matching, not tuples. Pinned by
`TestTupleProjectionFromMatchBindingLeaksX86_64`, which carries the three
clean neighbours alongside the leaking case so the boundary is part of
the gate rather than a note beside it.

### It is the ARRAY that leaks, not the tuple box

An earlier revision of this note, and the first version of the test's
comment, said the **tuple box** leaks. **That was wrong**, and it was
wrong for a reason worth recording: `step` allocates two 32-byte blocks
per call, and the claim rested on reading their addresses in order.

Growing the array literal from 3 elements to 20 settles it by size:

```
alloc#1 site=…40103d size=0x20  freed
alloc#2 site=…401099 size=0x30  LEAKED
```

A two-field tuple's box does not depend on the array's length, so the
site that GREW is the array — and it is the one that leaks. The tuple
box is freed every call.

So `__drop_tuple_…` runs, frees the box, and does **not** release the
pointer field projected out of it. The field sits one count high, the
dec-on-overwrite takes it back to one, and it never reaches zero.

### The drop is emitted; it just does not reclaim

Diffing the lowered IR across the three shapes rules out the obvious
next guess — that the leaking shape skips the tuple's drop:

| shape | ops in `main` |
| --- | --- |
| pointer projected (leaks) | `__drop_tuple_…` ×1, `rc.inc` ×1, `rc.dec` ×2 |
| scalar field only (clean) | `__drop_tuple_…` ×1 |
| binding unused (clean) | `__drop_tuple_…` ×1 |

All three emit the drop. The leaking one differs by an extra **inc** and
two decs. So the box's drop runs and finds a count above zero — the same
family as the bare-Ident bug, an alias-inc without a matching release,
but on the TUPLE BOX rather than the payload.

What is not established is which inc that is, and it should not be
guessed at: the last attempt to suppress an inc on reasoning rather than
evidence segfaulted `unidiff`.

## Root cause of the projection leak: the wrong dec helper

The inc/dec tracer (`FERN_RC_TRACE`, extended to emit `i`/`d` events)
settles it, once its pointers are paired correctly — `a`/`f` name the
block, `i`/`d` name the object 16 bytes above it:

```
LEAKED block=0x0000 size=32: alloc  dec
LEAKED block=0x0040 size=48: alloc  inc  dec  dec
LEAKED block=0x0070 size=48: alloc  inc  dec  dec
freed  block=0x0020 size=32: alloc  free  alloc  free
```

**The counts balance and reach zero, and the blocks are still not
freed.** So this is neither a missing dec nor an extra inc — the whole
family of explanations pursued so far is the wrong family.

`emitRcDecRuntime`'s own doc says why:

> Phase-1 simplification: on rc == 1 the helper still decrements to 0
> instead of calling a type-specific drop handler + freelist push. **The
> bump allocator leaks**; Phase 3 introduces the real freelist and Phase
> 1e introduces the drop handlers.

`__fern_rc_dec` does not reclaim, by design. Arrays reclaim through
`__fern_arr_dec`, which frees on the last reference.

And the projection shape routes the array to the wrong one. The IR diff
across the three shapes shows it directly: the leaking shape emits
`rc.dec __fern_rc_dec` twice and **no** `__fern_arr_dec`, while both
clean shapes emit neither.

So the defect is a **routing** one, not an accounting one: a projected
array-typed value is released through the generic helper, which
decrements it to zero and leaves the storage stranded. Three
independent readings agree — the trace, the helper's own contract, and
the emitted IR.

That reframes the fix. Nothing needs to move an inc or add a dec, which
is what the segfaulting attempt did; the dec site needs to select the
typed helper for an rc-tracked element type, the way every other release
site already does.
