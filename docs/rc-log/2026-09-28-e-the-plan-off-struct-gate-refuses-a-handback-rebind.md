# 2026-09-28 — the plan-off struct gate refuses a handback rebind (#10618)

AST lowering (`FERN_SEM_IR=`) with the rc plan off (`FERN_SELFHOST_RC_PLAN=0`)
only. The default configuration, with the plan on, is unchanged.

## What #10618 asked, and what was found

#10618 reported that `option_match_return` leaks with the plan off (60 / 20 on
x86-64) while the default balances it. It asked for two things: that the
plan-off route balance the shape, and that the conditional-release suite gain
a plan-off leg so the two routes cannot drift apart unnoticed.

The leak is not the cause the issue named. `cond_yielded_names` plays no part.
The off-plan struct gate, `body_unsafe_for_alias`, reads `Some(p)` as an
escape, while the plan's per-site verdict takes the counted-sink forgiveness
(#7345): the variant construction retains `p`. So `p` and its `xs` buffer are
refused with the plan off, which is a sound leak.

The plan-off route is a migration aid. `rc_plan_disabled` and
`SELFHOST-RC-PLAN-PROMOTION.md` define it as the family's old credit gate,
judged by the leak matrix and the underflow trap and never by byte identity.
The promotion plan deletes those gates once nothing consults them, and
`self_host_optstr_callarg_test.go` already pins a plan-off cell that "must
stay a LEAK". Porting the plan's counted-sink analysis into a gate slated for
deletion would contradict that. So this change does not widen the plan-off
gate for `Some(p)`. It pins the leak and adds the leg.

## The leg found an over-release

`TestSelfHostConditionalValueReleasePlanOffX86_64` runs every case with the
plan off, with the census and a `FERN_SANITIZE=1` leg. Its first run failed on
`row_handout`. The census balanced, but the sanitizer reported a
use-after-free (exit 124 against the interpreter's 59).

The statement was `b = idg(q[0])`: a generic identity hands back a row of `q`,
and the result is assigned to a struct local. With the plan off, `b` took the
struct credit. `reassigned_from_alias` reads every call rebind as a fresh box
("`s = mk()` ... is NOT an alias"), but `idg` returns its argument uncounted,
and it is listed in `handback_params` as `idg|0`. So `b` held a borrow of
`q`'s element. `q`'s `STRUCTARR` walk freed that element, and `b`'s release
touched it again. The non-generic `a = id(q[1])` is safe, because `id` is a
counted-handback member whose return retains. The plan refuses `b` already.

## Change

`reassigned_from_handback` reports a `name = f(..)` rebind where `f` hands an
argument back uncounted. The plan-off branch of `reclaimable_fresh_struct` now
refuses such a local, as the plan does.

## Measured (x86-64, AST lowering, plan off)

| case | before | after |
|---|---|---|
| `row_handout` | balanced census, sanitizer use-after-free | balanced, sanitizer clean |
| `option_match_return` | 60 / 20 | 60 / 20, pinned (`condPlanOffCensus`) |
| every other case | balanced | balanced |

The plan-on legs of the suite, and the other plan-off legs
(`TestSelfHostOptStrCallArg`, `TestSelfHostTupleStructFieldStore`,
`TestSelfHostTupleElemRetDup`), are unchanged.
