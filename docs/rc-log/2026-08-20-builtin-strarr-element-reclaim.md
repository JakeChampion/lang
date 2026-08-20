# `s.split(sep)` / `s.lines()` element boxes leaked

A `string[]` bound from one of the two BUILTIN string producers was swept with
the shallow buffer-only dec, so every element box the runtime made for it
leaked. 400 rounds of the churn harness, two compilers from the same commit:

| shape | x86-64 | wasm |
| --- | --- | --- |
| `base.split("-")` (18 parts) | 172800 | 204800 → **67200** |
| `base.lines()` (1 line) | 9600 | 57600 → **9600** |
| both in one frame | 182400 | 262400 → **76800** |

## Cause

`collect_fresh_strarr_names` credits a `string[]` local only when it can see
each element as a fresh expression — an array literal of fresh strings. A
CALL's result is not one, so `var parts = base.split(sep)` fell through to
`__fern_rc_dec` and the elements were never walked.

The elements of a split/lines result are nonetheless exclusively owned: the
runtime helper makes them and stores them into that array and nowhere else.
That is a different warrant from the per-element one, hence a different class.

## Fix

A `SARRB:` credit, collected by method NAME (`split` / `lines`) and gated at the
reclaim site by BOTH halves:

- `strarr_unsafe_for` — the non-escape half, unchanged from the `SARR:` class
  and name-level like the collector, so it applies as-is.
- `LocalInfo.strarr_builtin` — the TYPE half, recorded at the binding site,
  which is the only place that knows the receiver is a string.

The split is forced by the shape of the passes. `reclaimable_names_of` is a
name-level walk over statements; it sees `recv.split(sep)` with no type for
`recv`. A user method named `split` on another type answers to that name, and
its result may alias boxes the receiver still owns.

`chars` is deliberately absent from the family: it is declared `i32[]`, so there
are no element boxes to walk.

## The type gate is witnessed by fault, not only by emission

The shape it stops:

```fern
struct Holder { xs: string[] }
function (h: Holder) split(sep: string): string[] {
    var out: string[] = [];
    out = out.append(h.xs[0]);
    out = out.append(h.xs[1]);
    return out;
}
```

`parts` never leaves the caller's frame, so the non-escape half holds and only
the receiver's type stands between the name and a reclaim of boxes `keep` still
owns. Built three compilers from one commit — main, the fix, and a variant with
the name matched and the type confirmation dropped:

| compiler | wasm | x86-64 |
| --- | --- | --- |
| main | ok | ok |
| fix | ok | ok |
| name-only, no type gate | **99** (rc over-release) | **97** (churn string handed the freed box) |

The first attempt at this witness read as a null result and was not: an
`h.split()` that returns `h.xs` DIRECTLY (rather than a fresh array of its
element boxes) trips a separate pre-existing over-release — see the leads below
— which fires identically on all three compilers and hides the signal. The
aliasing has to be in the ELEMENTS, with the array itself fresh.

## The register half is not done

x86-64 and arm64 do not move, and the reason is structural rather than a
missing credit — the credit fires there too, upgrading `__fern_arr_dec` to
`__fern_str_arr_free` (checked in the emitted asm). On the register backends
`split` yields zero-copy VIEWS over the source: each element is a 24-byte box
carrying the immortal rc sentinel, and `__fern_str_arr_free`'s per-element
`__fern_str_free` skips an immortal rc **by contract** — that skip is what keeps
a view from freeing bytes it does not own.

Freeing the boxes needs the `__fern_str_view_free` treatment applied per
element: a view-aware sibling of `__fern_str_arr_free` in both register
backends, emitted only for this class (where the array provably owns its element
boxes exclusively). 172800 → ~0 for the 18-part split, 9600 → ~0 for `lines`.
That is the next increment.

The register ceilings in the test suite are therefore deliberately SLACK —
regression gates on today's number, not a floor the follow-up would have to
argue with.

## Leads found on the way, none of them this change's

Three pre-existing self-host divergences surfaced while building the witnesses.
All three reproduce identically on main and on the fix; none is caused or
worsened here.

1. **A method returning a struct's `string[]` field over-releases.**
   `function (h: Holder) get(): string[] { return h.xs; }`, bound to a local in
   the caller and called twice, ticks the underflow counter on the second call.
   Self-host x86-64 and wasm; the native compiler is clean. No observable
   corruption over 200 rounds with churn, so the unbalanced dec is landing on a
   box whose rc floor is guarded — detected, not yet located.
2. **A split result escaping its frame dangles on the register backends.**
   `function parts_of(pre: string): string[] { var base = w(pre); return
   base.split("-"); }` — the elements are views over `base`, which the frame
   frees on the way out. Native reads back correctly; self-host x86-64 reads
   corrupted element data. This one is a WRONG ANSWER, not a leak, and it is the
   same view-lifetime question the register half above has to answer.
3. **`.chars()` yields string elements on the self-host path.** `"abcdefgh"
   .chars()` prints `97, 98` under the native compiler and `a, b` under the
   self-host one, despite the declaration being `i32[]`. Read as raw `i32` it
   prints heap addresses, and one probe segfaulted.
