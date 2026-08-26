# `E.A(items)` took none of the payload retains `A(items)` takes

#3720's use-after-free, still live on the spelling its fix never reached.

## The reproducer

```fern
enum E { A(i32[]), B }

function make(i: i32): E {
    var items: i32[] = [i, i + 1, i + 2];
    var e: E = E.A(items);
    return e;                       // items' exit sweep frees the buffer this points at
}

function clobber(i: i32): i32 {
    var junk: i32[] = [7777, 7777, 7777];
    return junk[0];                 // reuses the freed buffer
}
```

`xs[0]` reads 7777. 100 of 100 rounds disagreed with native. Changing `E.A(items)`
to `A(items)` — nothing else — gives the right answer on every round.

## Why one spelling and not the other

The two ctor spellings reach `lower_expr` on different arms: `A(args)` on the
ident-callee arm, `E.A(args)` on the field-access arm. Each had its own arg loop.
The ident arm's loop carried two Perceus alias-incs — the array payload (#3720)
and the enum payload (#6720). The field-access arm was added later, to stop a
qualified construction making the module IR-ineligible, and lowered its args
straight into `op_struct_make` with no retain at all.

Nothing measured them against each other, so the gap survived both fixes that
were written for it.

## The fix is one loop, not two patched ones

`lower_variant_ctor_args` now lowers the arguments and emits the retains for both
arms. Copying the two incs into the second loop would have fixed this instance
and left the next one to be found the same way — this is the second time the two
spellings have been caught apart (`variant_unqual`, #7526, was the first) and both
times the finding was accidental.

The shared loop also reads field types through the resolved decl INDEX, which is
what the qualified arm already did and the bare arm did not. Its reason applies to
both: two enums may declare the same variant name, and the by-name lookups answer
for whichever was declared first.

## What measured, and what did not

| probe | before | after |
|---|---|---|
| `variant_arr_uaf_qual` | **exit 100**, 300/200 | exit 0, 300/100 |
| `variant_arr_uaf_unqual` | exit 0, 300/100 | unchanged |
| `variant_enumpay_qual` | exit 0, 180/0 | unchanged |
| `variant_enumpay_unqual` | exit 0, 180/0 | unchanged |

The array retain is the measured fix. The **enum** retain now reaches the
qualified arm too and demonstrably fires — the emitted asm gains its `rc_inc` —
but no probe here separates the two spellings on it: the shape leaks either way
(0 frees), so the extra inc is not observable in the census. It is carried over
for parity, not because a probe demanded it. A probe that does separate them
would need #6720's consuming walk over a borrowed tail; the recursive-sum shape
tried here reads through a borrow and never reclaims.

Every spelling pair now measures identically: array-UAF, enum-payload, moved
struct payload, live struct payload.

## Unrelated, and still open

`variant__live` / `variant_unqual__live` stay `leak` (300/0). That is the
counted-share half: the enum ctor's alias-inc covers array and enum payloads but
not STRUCT boxes — the same array-only gating `lower_opt_make_payload` had before
#7528, one container over. Widening it is the next slice, and needs the same
three parts that one did: the retain (skipped at a move site, since #7517
populates move sites for enum ctors and the moved cells already work by elision),
a stamp so `struct_box_sink_stored_expr` reads a retained site as a counted share
rather than an uncounted sink, and an rc-gated release on both owners.

That widening was written and then backed out of this change: it is inert without
the credit half, and shipping an inert retain would have made the correctness fix
harder to read.
