# 2026-08-22 — a name-keyed credit cannot be widened safely (#7335, #7253)

The reusable part of this entry is the ordering rule, not the leak.

## The leak

`var v: i32[][] = mk()` never released either inner array when `mk` returned a
LOCAL rather than the literal itself:

```fern
function mk(): i32[][] { return [[1,2],[3,4]]; }                      // clean
function mk(): i32[][] { var a: i32[][] = [[1,2],[3,4]]; return a; }  // leaks
```

Same caller. 200/400/800 rounds: `live_bytes` 16000 / 32000 / 64000 — 80 B/round,
unbounded, against 0 on native and interp. `allocs` matched native exactly and
`frees` was exactly one third of it at every count: the outer buffer reclaimed,
both inners stranded, every round.

`collect_fresh_arrarr_names` already had a producer-call arm. What refused the
callee was `opt_fresh_ret_fns_of`, which proves "every return is a fresh arrarr
literal" SYNTACTICALLY — so one extra statement disqualified it. The fix resolves
`return <ident>` through `arrarr_row_effective`, which already carried exactly the
needed consumption proof one level down for ROWS (declared earlier in this
statement list from an array literal, unmentioned between declaration and use,
and this is its last use).

Returning a local is the form real code has: anything that builds rows before
handing them back cannot use the literal form. The refused case was the common one.

## The rule this proved

**Widening a reclaim credit while it is name-keyed converts a leak fix into an
over-release.**

The widening alone, measured:

| probe | base | widening only | + site-key |
|---|---|---|---|
| `aa_flat` | `600/200` 16000 | `600/600/0` | `600/600/0` |
| `arrarr` | 34 | **99** | 34 |
| `strarrarr` | 34 | **99** | 34 |

`"ARRARR:"` resolved through `reclaim_slot_name`. The credit the widening newly
granted a fresh binding was inherited by a same-named aliasing sibling in another
`if` arm, which then freed a buffer the caller still owned. Site-keying the class
(#7253's treatment) first makes the same widening correct.

Two things to carry forward:

- **"This only adds decs/credits, so it cannot regress anything" is false**, and
  this is the second time in one day it was false. The byte counts stayed clean
  through the double-free — `allocs == frees` at `live_bytes == 0` — because a
  doubly-released block goes straight back to the freelist. Only `__rc_underflow()`
  dissented.
- **Site-keying is a prerequisite for widening, not a parallel cleanup.** The
  collisions do not exist until something widens the credit, so probing a
  name-keyed class and finding it clean does NOT mean it is safe to leave. That
  applies to #7259, whose fix is a widening of the `strarrfld` admission, and to
  the ~18 classes on #7253's list that are still name-keyed.

## Still open

`var a: i32[][] = [[..]]; var v: i32[][] = a;` in ONE body still leaks 16000. The
literal is credited to `a`, and reading it into `v` disqualifies it — an
escape-gate question rather than a registry one. Pinned by
`local_alias_still_leaks`, asserted on the exit code only: the point is that it
must not start OVER-releasing while it waits, which is the direction a careless
widening takes it.
