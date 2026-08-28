# The str wave's precondition, measured and pinned

The sanctioned wave order puts str/tuple last in the rc-plan promotion's
step 2, "behind the co-extensive retain gate and dup-at-extract". With
dup-at-extract landed, this entry records what a scoping pass found when it
asked whether the str half is now unblocked. It is not, and the reason is
worth a pin rather than a paragraph.

## The divergence

Native's `computeFreeEligible` carries a blanket taint for a single-word
string passed as a call argument to a user function: the callee may retain
it somewhere the intraprocedural analysis cannot see, and freeing it
caller-side then dangles the retained copy. The comment records the case
that bought it — a codegen helper storing a string arg into an array field
of the struct it returns, where the caller's `str_dec` recycled the box and
corrupted nested control-flow codegen.

That taint has exactly one exemption, `paramCountedRetain`: a callee that
keeps the param only through COUNTED constructions holds a reference of its
own, so the caller's release is balanced. The lexer's eight `*_tok` helpers
are why it exists.

The self-host's ported plan has **neither half**. `rc_fe_walk_expr`'s call
arm escapes only variant ctors, IIFEs and the direct set/append/push/with
method sinks, and `rc_fe_run`'s param seed skips string params outright.

## Measured, not inferred

Two shapes agree (both compilers grant), which is why this was invisible:
a read-only callee and a struct-literal-storing callee are both vacuously
or explicitly counted-exempt. The third shape separates them:

```
function stash(acc: string[], s: string): string[] { return acc.append(s); }
function f(): i32 {
    var v: string = "hi" + "!";
    var xs: string[] = [];
    xs = stash(xs, v);
    return xs.len();
}
```

| | native | self-host |
|---|---|---|
| `freeEligible` | *(empty — both tainted)* | `v,xs` |
| `lastUses` | *(empty)* | `v=2,xs=3` |

An append sink is not a counted construction, so native's taint stands and
the plan grants straight through it.

## Why it blocks the str wave specifically

While the str release families stay CREDIT-gated, this costs nothing: their
own escape walkers (`body_unsafe_for_clo_alias` and friends) refuse a call
arg regardless of what the plan thinks. Routing them through the plan is
what would consume the plan's verdict — and there the plan is WIDER than
native on the one channel native added a blanket taint to defend. That
converts a leak into a dangle, which is the direction the promotion doc
says never to take a family.

The tuple families are unaffected: their gates ask about tuple locals, not
string args.

## What this pin is for

`TestSelfHostRcPlanDiff` case `string-call-arg-taint`. Pinning both sides
makes the gap a named burn-down item that drift on either compiler will
catch, the same way the elemret pin carried dup-at-extract until the port
landed and flipped it to an anchored agreement.

Retiring it: port native's taint into `rc_fe_walk_expr`'s call arm, using
the existing `param_counted_of` `"SCNT:"` registry as the untaint — the
self-host already has the exemption half, only the conservative taint is
missing.

## Also recorded

The scoping pass confirmed the OTHER precondition IS satisfied for both
families: the alias-bind retain is keyed on the credit predicate
(`slot_is_reclaimable_str` / `_tuple` / `_rctuple`), the same position the
struct wave was in when its retain side "needed no edit". So the tuple half
of the wave is genuinely unblocked; only str waits on the taint port.
