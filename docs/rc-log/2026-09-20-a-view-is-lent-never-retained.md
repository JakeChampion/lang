# 2026-09-20 — a view is lent, never retained

#9802, filed as latent: `ssaunits.owned_values` owned every reference-typed
phi without reading its operands, the only known way to reach a phi with a
borrowed-parameter operand was the tail-recursion rewrite, and that pass
declined any function with a view parameter to stay clear. The issue's own
correction said the operand-based fix covered only half — a phi merging a
borrowed view with a FRESH one is owned by any rule, and the borrowed edge
then supplies a retain that is a no-op against a release that frees.

The other half is not latent. It is four lines of source:

```fern
var t: string = "abcde" + "fghij";
var s: str = slice_unchecked(t, 0, 5);
var v: str = s;
var i: i32 = 0;
while (i < 3) { v = slice_unchecked(t, 5, 10); i = i + 1; }
return v.len() + s.len() + u.len();
```

| leg | `FERN_SANITIZE=1` |
|---|---|
| semantic, before | **use-after-free** (touched a quarantined block), exit 124 |
| AST | answers 14, leaks 96 bytes in 4 blocks |

The phi merging `s` with the loop's fresh view is owned; `s` stays live
past the loop, so the entry edge supplies it by RETAINING — a no-op on a
view's immortal box — and the round that replaces the phi's value releases
`s`'s box from under `s`. A probe of the planner (a throwaway eprint of
each phi's ownership, not a knob) showed both operands owned: nothing about
the phi's OPERANDS is wrong here, the retain is.

## Two rules

**A phi is unowned when every operand is lent whole** — a borrowed
parameter, or another phi that is — to a fixpoint. Such a phi holds nothing
its edges could supply. Any other operand keeps it owned, and the first cut
of this entry learned why the hard way: a phi merging PROJECTIONS of owned
records went unowned too, lost the retain that kept the projected child
alive past its parent's release, and `TestSelfHostSemanticSourceRC` found
the use-after-free on all four targets. The operand-based rule from the
issue is right for parameters and wrong for anchored children, and only
the suite said which.

**A view is lent, never retained.** `ssaunits.plan` refuses any function
whose plan has a step retaining a view (`view_retain_error`), whatever put
the supply there — a phi edge, a construction, a container write. `ssarc`'s
#9328 note is the same asymmetry at the call bracket, restricted there to
arrays; this is the planner-wide statement of it. The AST lowering stands
for the refused function, leaking where it leaks, answering what it
answers.

With the first rule in place, `ssasem.tail_recursion` no longer declines a
function with a view parameter: a tail call passing a view of anything is
already declined per block, so a view parameter's phi merges parameters
passed whole and is unowned.

## Measured

| shape | before | after |
|---|---|---|
| the loop above, semantic leg | use-after-free | refused, `a view is lent, never retained`; AST answers 14 |
| `walk(s: str, i)` tail-recursing on its view, depth 100000, wasm | produced leg exit 134 (stack) | exit 55 on both legs; native allocs=3 frees=3 |
| `view_walk` in the tail-recursion test | depth 2000, declined | depth 400000, a loop, `still=5 underflow=0` |
| compiler's own sources | 8614 of 8614 | 8614 of 8614 |

On the 512-program fernsmith census, x86-64, each program built on both legs
and run: 451 of 508 whole before and after, 499 agree, 0 diverge, and the
new refusal appears in no program's report. Fernsmith writes no view
locals, so the rule costs the corpus nothing either; the loop above is the
one measurement of what it refuses.

## Trap

**"Latent" was a claim about the one path anyone had walked.** The issue
reasoned that a phi with a borrowed-parameter operand needed the
tail-recursion rewrite, and it did; the mixed phi it went on to describe
needed nothing but a loop and a local. Each entry in this log that says a
shape cannot arise from source should come with the four-line program that
tried.
