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

The direction is established — an extra retain — but WHICH inc is
doubled is not. Two candidates sit on this path: the match destructure
alias-inc for `v`, and the assign alias-inc for `cur = v`. Either alone
is correct; together they double.

Removing the wrong one converts a leak into an over-release, which is a
use-after-free rather than slow growth, so this stops at the diagnosis.
What it leaves is narrow: a 20-line repro, three mechanisms ruled out by
measurement, and a refcount that says exactly what is wrong.
