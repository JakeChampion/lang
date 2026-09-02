# 2026-09-02 — the `stored_struct` gap is a struct's ENUM FIELD, not call taint

## What the gap list said, and why it was wrong

`enum_scalar__callarg__stored_struct` is the only self-host row in either leak
matrix, and its note named the mechanism:

> callee returns the arg inside a struct LITERAL, so native now credits the
> result as a box of its own (findReturnsFreshBox) and reclaims it; the
> self-host compiler carries its own copy of that taint rule and still leaks.

Both halves are false, and one experiment kills each.

**Native does not use `findReturnsFreshBox` here.** Returning `q` empty from it
outright — before the fixpoint runs, so no function is ever credited — leaves
the cell at `allocs=200 frees=200 live_bytes=0`, exactly as before. Whatever
reclaims this shape natively, it is not that analysis.

**The call is not the trigger.** The same struct built INLINE in `main`, with
no callee anywhere in the program, already leaks:

```fern
struct H { e: E, n: i32 }
var e: E = A(i);
var h: H = H { e: e, n: i, };     // no call at all
```

| variant                                     | native  | self-host          |
|---------------------------------------------|---------|--------------------|
| struct of scalars only                       | 100/100 | 100/100 clean      |
| struct holding an **array** field            | 200/200 | 200/200 clean      |
| struct holding an **enum**, built inline     | 200/200 | 200/**101**, 3,960 B |
| the matrix cell (same, via a returning callee) | 200/200 | 200/**0**, 8,800 B |

An array field is clean on both sides; a scalar-only struct is clean. The gap
is the **enum field specifically**: the self-host's struct drop never releases
it. 101 of 200 frees in the inline form is the 100 enums plus one — the
STRUCTS are what go unfreed there.

The call form is strictly worse (0 frees, not 101): routing the struct through
a callee loses the enum's own reclaim as well. That is a second effect on top
of the first, and this entry does not explain it.

## Why this cost a round

The note was written from the shape of the cell rather than from a measurement
of it, and it reads plausibly — a callee that stores its argument into a
returned literal IS the shape `findReturnsFreshBox` exists for. A subset port
of that analysis into `irlower.fern` (`freshbox_ret_fns_of`, a knock-out
fixpoint over returns that are struct/tuple/array literals, consulted from
`rc_fe_rhs_tainted`'s user-call arm) compiles, is sound, and moves the matrix
**not at all** — 133 clean/clean and the same single gap, before and after. It
was reverted rather than landed: an analysis whose stated purpose is disproven
and whose effect is unmeasured is surface, not progress.

`CLAUDE.md` already says to verify tracker state against the code because
issues here have repeatedly lagged reality. A gap list is a tracker.

## Next lead

Find where the self-host's struct drop walks fields and why an enum-typed field
is skipped where an array-typed one is not — `v_arrstruct` is the clean control
that says the walk itself works. Then explain the inline (101) versus call (0)
difference separately; the two may share a cause but nothing here shows it.

The four arm64 rows reading `leak clean` are native-arm64 `#7446` gaps where
the self-host is AHEAD, and are untouched by this.
