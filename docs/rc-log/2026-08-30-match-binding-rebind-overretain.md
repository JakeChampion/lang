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

So the enum box's release does dec its payload correctly. The imbalance
appears only when the binding is assigned out — where the alias-inc is
emitted AND, on the evidence of the refcount, the box's payload-dec no
longer lands.

It is **not** the documented safe leak for ineligible enums:
`enumRcPayloadsEligible` excludes only enums transitively containing a
Map, and `ast.EnumRcPayloads` is on, so `Option[u8[]]` takes the counted
path.

## Where that leaves the fix

The asymmetry is the finding: a pattern binding that holds a reference
is not released the way an ordinary local is, so an alias-inc taken
against it has nothing to cancel.

Both repairs carry the same risk in opposite directions — releasing a
binding that turns out not to own, or suppressing an inc something else
depends on, each converts a leak into a use-after-free. So this stops at
a diagnosis narrow enough to act on: one op, one asymmetry, and four
measured shapes that bound it.
