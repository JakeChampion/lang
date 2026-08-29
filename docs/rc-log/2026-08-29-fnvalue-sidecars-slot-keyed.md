# Two scans in one expression — and the probe that found a different bug

The fn-VALUE sidecars — `fn_dyn` (#5276), `fn_sig` / `fn_ret` (#6282/#6862) and
`closure_opt_rets` (#3457) — were `LowerState` side tables of `"<name>|<value>"`
rows read with `tagged_value_of`. They now live on `LocalInfo`, keyed by slot.

**No witnessed defect.** Recording that plainly at the top, because the probes
written for this change DID go red and it took an asm read to find out they were
red for another reason. That correction is the useful part of this entry.

## The hazard, which is real and structural

`slot_of` scans the frame **backward**, and says so in its own comment: *"so a
name resolves to its MOST-RECENT binding — the innermost lexical scope."*
`tagged_value_of` scans **forward** and returns the first match, and `lower_func`
seeds parameters before body locals — so it answers with the **outermost**.

Both are used in one expression, at the indirect call:

```
var icslot: i32 = s.slot_of(cid.name);        // the innermost binding
…
op_call_indirect_sig(c.args.len(), s.fn_value_sig(cid.name, false))  // the outermost
```

The operand comes from one binding and the funcref type from another. There is
no shape in which they can both be right, and the two sites are five lines apart.

## Why no probe could reach it, and how long that took to notice

Three programs — a nested `var g` shadowing a top-level fn-typed local, one per
sidecar — each diverged from the interpreter, each with a one-token rename
control that did not:

| | interp | self-host | rename control |
|---|---|---|---|
| shadowed signature | 86 | 88 (x86-64 AND wasm) | 86 |
| shadowed return | 55 | 64 | 55 |
| shadowed dyn positions | 37 | 134 | 37 |

Textbook evidence, and wrong. Re-keying the sidecars left **all three
unchanged**, and the emitted asm was byte-identical — which is the fact that
gave it away. If the inner slot had stopped resolving the outer's row, the emit
had to move.

It had not, because the shadowed lambda never reached the sidecars at all.
`try_lift_binding` param-lifts both bindings and `subst_fcall_expr` rewrites
every `g(…)` callee it walks past — recursing into `if` / `while` / `for` /
`match` bodies with no notion of scope. All three call sites emitted
`call __fn___lam_0`; `__fn___lam_1` was emitted and never reached. The answer
moved by exactly the difference between the two lambdas.

That is the same bucket #7253's first comment puts the #6191 / #6283 class in —
*"the AST pre-passes and the lift, which run before any slot exists"* — and it
is fixed in its own commit, with these three programs as its regression rows.

So the sidecar hazard remains unwitnessed. It converts anyway: it is a
first-match value lookup used in the same expression as a backward scan, and
#7253's own record is a list of classes that were latent until the leak masking
them was closed.

**The reusable half:** a probe that goes red is not evidence about the mechanism
you are changing. The check that separates them is cheap and I should have run
it first — *make the change, and see whether the emitted bytes move at all.*
Byte-identical output under a red probe means the probe is measuring something
else.

## What moved, structurally

`LowerState` loses four fields and gains one; `LocalInfo` gains four.

The one it gains, `fnval_seed`, is the hand-off across the single boundary where
a slot does not exist yet: `lower_func`'s pre-pass computes the signature facts
for top-level fn-typed locals **before any body lowering**, and `add_local` has
not run for them. So the pre-pass emits `"<site>|<dyn>|<sig>|<ret>"` rows and the
binding site stamps the slot it creates. Parameters need no hand-off — their
slots are the frame layout `lower_func` has just built, so they are stamped into
`locals0` directly.

Two binding paths consume the seed, because two paths bind a fn-typed `var`:
`bind_var_slot` and `lower_stmt_var_closure`, which does not route through it.
Missing the second would have silently unseeded every annotated
`var f: (i64) => boolean = |x| …` — the shape #6862 exists for.

## The admission is deliberately unchanged

Only the KEY moved. The pre-pass still walks the function's TOP LEVEL only, so a
fn-typed local declared inside a block is still unseeded and still falls back to
the arity-keyed `$fn<N>` funcref type. That fallback is correct for an all-i32
signature and wrong for a wide one, so a nested `var g: (i64) => i64` is a
separate open gap — reachable, not fixed here, and now at least not able to
inherit an unrelated binding's answer.
