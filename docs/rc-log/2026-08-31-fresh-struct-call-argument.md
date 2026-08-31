# A fresh struct handed straight to a call argument is freed after the call

*2026-08-31* — self-host only; both oracles answer 53 on every row.

## Where it came from

The certifier's first self-host run (#7851) reported **7,132 findings against 0
over the conformance corpus**. Classifying them by defining op and by use:

| axis | top classes |
|---|---|
| kind | `call` 6,654, `alloc` 410, `make_closure` 59, `invalid` 9 |
| callee | `__str_concat` 1,041, `ir__op_load_local` 927, `ir__op_store_local` 596, `__str_slice` 482 |
| **use** | **`__method_irlower__LowerState_emit` 4,287**, `add` 3,763, `__method_asmcore__EmitState_write` 541 |

The use histogram is the one that named a shape: `p.emit(ir.op_load_local(i))` —
a struct built by one call and handed to the next — accounts for 4,287 of the
7,132 on its own.

## Traced end to end before anything was claimed

A five-line program with the same shape, measured with `FERN_LEAKCHECK=1` at 100
and 200 rounds:

| argument | free callee | METHOD callee |
|---|---|---|
| `Op { … }` literal | 200/200 clean | **200 allocs / 100 frees** |
| `mkop(i)` producer call | **200/100** | **200/100** |
| bound to a `var` first | clean | clean |

Exactly 2.0x per doubling on every leaking cell — leaked per EVALUATION, so
unbounded, not a bounded per-object loss. The `var`-bound row is the position
that already worked, which is what separates this from the construction-retain
matrix: all of its cells bind first.

## Two gaps, one per axis

**The method axis.** `lower_call_struct_method` and `lower_call_prim_method`
carried the STRING stash (`stash_fresh_str_arg`) and no struct one, and
`lit_arg_callees_expr`'s method arm collected only `ExprString` — so
`call_arg_borrowable` answered false at every method callee and the seed the
lowering would have consulted was never written. This is the half of #7576 that
never reached a method: the same defect shape as #7259, a consumer looking up a
key the producer never wrote.

**The producer-call axis.** `lower_call_named`'s call-argument arm admitted the
`"ARR:"` and `"STRARR:"` registries only. A struct producer had no arm on either
path, though `lit_arg_callees_expr` had been seeding `ExprCall` callees all
along — the seed was there and nothing consumed it.

`stash_fresh_struct_arg` / `free_stashed_struct_args` now hold both shapes once,
and all three call paths route through them. The release ladder is the
discarded-statement arm's, unchanged: deep field drop first while the box still
owns its fields, then the box dec; a scalar-only struct takes the dec alone.

## Safety

The gate is the one the string and array arms already use, and the two shapes it
must refuse are pinned as tests: `keepf(o) -> o` hands the box back out and
`wrapf(o, k) -> Box { o, k }` moves it into a field. Neither is borrowable, so
no stash fires and both keep their prior safe leak. They assert the ANSWER and
`__rc_underflow()` rather than leak counts — removing the gate shows up as a
wrong answer or an underflow, never as a number, and the counts would read
BETTER for the broken build.

## What it did not fix, and why that is a different slice

Certifier findings over the self-host: **7,132 → 7,125**.

Seven. The shape that dominates the census is `LowerState.emit`, which does
`s.ops.append(op)` — it STORES its argument, so it is correctly not borrowable
and no stash may fire there. The counted-retain admission is the path that
covers a storing callee, and its struct tier does not exist:
`borrow_reg_with_counted` says so in as many words — *"The struct-array one is
still uncounted: admission is BY TYPE, and its tier is its own slice."*

So the 4,287 are not a certifier false positive and they are not this fix's to
take. They are the struct tier of `param_counted_of`, named as pending before
the certifier existed, and now measured.
