# In-place element writes in the interpreter (`arr.with(i, v)`)

Why the interpreter's `T[]` carries an owner count, what that count is
allowed to get wrong, and how to prove it did not lie. Decision record
for #7287, split out of #6395; the map counterpart is
`docs/INTERP-MAP-COW-PLAN.md`.

## The problem

`builtinArraySet` allocated a fresh buffer and copied the whole array on
every `arr.with(i, v)`, so `a = a.with(i, v)` over n elements was O(n²)
in the interpreter and O(n) in every compiled backend, which take the
refcount-1 in-place branch of `__fern_arr_cow_inplace`. Since the
interpreter is the differential oracle, that caps how large a corpus case
can be — which looks like a decision about test scope rather than a
property of one implementation.

`.append` was fixed differently (#6395/#7289) and the fix does not carry
over. Growth writes at index `len`, a slot **outside** the receiver's own
range, and the interpreter marks unallocated spare capacity — "this slot
still carries the mark" proves no other array value covers it. `.with`
writes at an index **inside** the range, which every other value over the
buffer also covers, so there is no structural argument: it is in-place-safe
exactly when nobody else holds the buffer.

## The property that decides the design

**For arrays, unlike maps, the in-place decision is not observable.**

`m.set(k, v)` hands back the receiver and mutates it, so whether a map
copies is visible through the receiver binding: an over-count breaks
`m.set(1, 10); m.len()`, which is why `docs/INTERP-MAP-COW-PLAN.md` had
to rule out every conservative approximation and get the count exactly
right. `arr.with(i, v)` never writes to the receiver's binding — it
returns a value — so **copying is always semantically correct**. An
over-count costs speed and nothing else.

That inverts the risk the issue feared. Only an *under*-count can corrupt,
so every site that is unsure can simply count, and the discipline does not
have to be exact — only sound. That is what makes the counted design
tractable in an interpreter that has no ownership pass.

## The audit

Every site in `internal/interp` that stores a `Value`, and whether the
retain/release discipline the map COW path already runs would be correct
if arrays consulted it. Line numbers are from the commit that closed
#7287.

Sites that already owned their store:

| Site | Where |
| --- | --- |
| `var` / match-arm / `for` / `let` binding | `env.declare` |
| block exit | `env.releaseScope` |
| `x = v`, `a[i] = v`, `s.f = v` | `evalAssign` via `retainReplacing` |
| closure parameter bind | `callClosure` (goes through `declare`) |

Holes — every one of them an under-count for arrays:

1. **Function parameters are not owned.** `callFunc` binds
   `e.vars[p.Name] = args[k]` directly, deliberately bypassing `declare`.
   For maps that matches the backends: probing `f(m)` with `p = p.insert(…)`
   inside, the interpreter and x86-64 both report the caller's map mutated.
   For **arrays it does not** — the same probe on `p = p.with(0, 9)` gives
   19 (a copy) on both, because the backend dups an array whose caller is
   still live. So an array reached through a parameter was uncounted, and
   `p = p.with(i, v)` at count 1 would have written into the caller's
   buffer. `callClosure` was already inconsistent with `callFunc` here.
2. **A map's in-place `set` stores without counting.** `builtinMapSet`
   writes `t.vals[idx] = args[2]` and returns the same `*Map`, so the
   assignment that follows sees the same value going out as came in and
   its retain and release cancel. The stored value's new path through the
   map is never counted. Confirmed by probe: `m = m.insert(1, a)` left
   `a` at one owner, and a later `a = a.with(0, 9)` would then have
   corrupted the map's copy. `delete` and `clear` drop entries without
   releasing them, in the same way.
3. **Container literals do not own their elements.** `ArrayLit`,
   `TupleLit`, `StructLit`, enum variant construction, `MapLit`,
   `Cell.set` and the element `push`/`with` store all placed values
   without counting. Under the map path's recursive walk this is covered
   *transitively* whenever the container itself reaches a binding — but a
   container that stays an unowned temporary leaves its elements
   uncounted.
4. **Slice views were invisible.** `a[lo:hi]` produced a second Go slice
   header over the same buffer with no relationship the counter could
   see. This is also a **pre-existing interp/native divergence**, found by
   this audit and left alone: with `var s: [i32] = a[1:3]` live,
   `a = a.with(1, 9)` prints 2 through the view on the interpreter and 9
   on every backend, because a view is a borrow that holds no reference
   and the owner's write lands in the buffer it is looking at. The
   interpreter's answer is now pinned deterministically (a view counts as
   an owner, so the write copies) rather than depending on whether the
   in-place path fired.
5. **Argument temporaries.** `evalCall` evaluates arguments into a Go
   slice; a later argument can be a block expression that reassigns the
   binding an earlier one came from. Same for the base of an `Index` and
   the source of a `SliceExpr` across the bound expressions.
6. **Closure capture is by reference**, so a captured array is counted
   only while its defining scope lives; `releaseScope` gives the count
   back though the closure can still read it.

The first five are closed (below). The sixth is a genuine residual and is
listed under "What this does not cover".

## The rule

`Array` is a view over a backing buffer plus a pointer to that buffer's
header, which carries its **owner count**. Every view of one allocation —
a `[lo:hi]` slice, the shorter array an in-place append grew from — shares
the header, because an element write through any of them is visible
through all of them.

> `arr.with(i, v)` writes into the receiver's buffer **iff the count is
> zero**, and copies otherwise.

Zero, not one, because the in-flight receiver reference is itself counted.
What makes the count reach zero for the shape that matters is the **move**:
`x = <rhs>` releases the slot's array reference *before* evaluating the
right-hand side, since the slot is about to stop holding it. That is what
Perceus does at a last use, and it is why `x = x.with(i, v)` finds the
buffer unshared — and, because a parameter takes ownership of its
argument, why `x = f(x)` does too, one call deep and inside the callee.
Nothing can reach the old value uncounted in between: an alias made during
the right-hand side binds (and so counts), and an argument already
evaluated is held by `evalCall`.

The buffer's header carries a second number, `paths`: how many times
`adjustRC` has counted this array into the **Map** rc, which is how many
owning routes reach its elements through it. An in-place element write
hands exactly that many paths from the element it replaces to the one it
stores — the assignment that follows cannot, because it sees the same
array going out as came in and its retain and release cancel. Without it,
`a = a.with(0, m)` on an array of maps leaves `m` looking unshared and the
next `m.insert` mutates the copy the array holds: 99 in the interpreter
where every backend prints 59.

The owner count is **not recursive**, unlike the map rc that `adjustRC`
walks: a container's own count says nothing about its elements, so every container
that takes an array element counts it directly. That keeps a store O(1)
rather than O(size of the value) — which matters, because parameters and
argument temporaries are now counted, and an O(n) walk per call would
reintroduce the quadratic it removes by another route. Container stores
are **sticky**: the interpreter never tears a container down, so the count
is never given back and an array that has ever been in one is copied by
`with` from then on. That is the status quo for those arrays, and it is an
over-count, which is free.

## Why not a persistent vector

The alternative (#7287 option B) is to drop the flat representation for a
32-way trie or a rerooted array: O(log n) or amortised O(1) writes with no
uniqueness signal at all, and no way to corrupt. It was rejected on the
cost to **reads**, which are the interpreter's hot path and are not
implicated in the bug: every `arr[i]` would become a trie descent or a
reroot check, on every interpreted program, including the long
`e2eselfhost` and conformance runs. A rerooted array additionally makes
any Go slice taken from the buffer stale after a reroot, which is a
sharper version of exactly the aliasing hazard the counted design is
audited against. Paying a broad read regression to avoid a one-sided,
testable risk was the wrong trade.

## The safety net

`FERN_INTERP_ARRAY_COW` selects one of three modes, so nothing has to take
the fast path's word for it:

- **unset** — the counted in-place write.
- **`copy`** — never write in place. This is the cross-check baseline: the
  oracle's answer must not depend on whether the optimisation fired, so
  any program that differs between this and the default is a miscount.
- **`verify`** — write in place, but first walk every binding of every
  live scope for a value covering the buffer. The slots of the assignments
  in flight still hold their value at that point (they released their
  count but have not been overwritten), so those are allowed for by name;
  anything beyond them is an under-count and the write is refused with an
  interpreter error rather than performed.

`TestFeatureDifferentialInterpArrayCOW` (`internal/e2e`) replays the whole
`TestFeatureDifferential` corpus through all three and requires identical
output; `TestArrayWithValueSemantics` (`internal/interp`) runs the aliasing
table under all three. Deleting one `storeArray` call makes the default
mode print 99 where every backend prints 19, `copy` still print 19, and
`verify` name the program — which is the check working.

## What this does not cover

- **Values held only in a Go temporary of an enclosing evaluation** that
  `evalCall` / `Index` / `SliceExpr` do not hold. `verify` cannot see
  them either, since it walks scopes.
- **A closure outliving the scope it captured from.** The capture is by
  reference and the scope gives its count back at exit, so an array read
  only through such a closure is undercounted. Reaching it requires
  binding it out of the closure, which counts it again.
- **An error unwound between the move and the store.** `x = <rhs>`
  restores the count if the right-hand side fails, but a `?` that unwinds
  past the assignment from further out does not come back through it.
- The three known interp/native divergences the audit's probe corpus
  found — the slice view above, and two block-expression evaluation-order
  shapes (`h(a, { a = a.with(0, 9); 1 })` and `a[{ a = a.with(0, 9); 0 }]`)
  where a backend reads the mutated buffer and the interpreter reads the
  old one. All three predate this change and are unchanged by it; the
  interpreter's answer in each is now pinned by an owner count rather than
  by which branch `with` happened to take.

## Measured

`fern -interp`, 4-core x86-64 container, 2026-09-04. n appends followed by
n `a = a.with(j, j)` writes:

| n | before | after |
| --- | --- | --- |
| 2 000 | 78 ms | 12 ms |
| 4 000 | 262 ms | 21 ms |
| 8 000 | 1 255 ms | 24 ms |
| 16 000 | 5 539 ms | 39 ms |

`examples/proposals/prime_gaps.fern`, the app #7287 named: **5 527 ms →
734 ms**. A 200 000-element append-then-read-five-times loop is 893/989 ms
before and 941/949 ms after — no read regression outside noise.
