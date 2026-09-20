# 2026-09-20 — the classifier and the representation have to agree

`2026-09-19-a-function-value-may-live-in-an-array.md` made `ilc_expr_at` box
EVERY lambda element of an array literal, capturing or not, so that the
semantic source never meets a bare fn pointer. It left
`array_elem_is_env_boxed` — the ONE question the rest of the lowering asks to
learn whether a binding is on the env-first dispatch ABI — answering "boxed"
for a capturing lambda only. Main went red on all three targets.

## The shape

```fern
function main(): i32 {
    var xs: ((i32) => i32)[] = (if (true) { [((x: i32) => (x + 3i32))] } else { [((y: i32) => y)] });
    return xs[0i32](1i32) & 63i32;
}
```

An `if` in value position yielding arrays of NON-capturing lambdas.
`hoist_value_iife` lifts the `if` to `__lam_0`, whose arms `ilc_expr_at` now
boxes (`__lam_0$wrap0`, `__lam_0$wrap1` — the `$wrap` trampolines are in the
refusal report). The binding `xs` in `main` is classified through
`iife_arm_arrays_boxed_elems`, which asks `array_elem_is_env_boxed` of each
arm element, and a non-capturing lambda answered NO. So `xs` stayed a plain
fn-pointer array and `xs[0](1)` dispatched a box as a bare address:

| leg | x86-64 | wasm |
|---|---|---|
| semantic (refuses `main`, AST stands) | SIGSEGV | exit 134 |
| AST (`FERN_SEM_IR=`) | SIGSEGV | exit 134 |

Both legs, because the semantic path refuses the module — `binding xs
declared ((i32) => i32)[] holds a semantic value of i32`, the `$iife`
contract gap the 2026-09-19 entries already name — and the AST lowering is
what runs.

## The fix

`array_elem_is_env_boxed` answers true for any `ExprLambda`. The special-case
line in `ilc_expr_at` that forced `has_cap_lam` for a lambda element goes,
because the classifier now says the same thing and there is one place that
answers the question again — which is the property `#8151`'s note on
`iife_arms_are_arrays_with_boxed_elem` asked for: "the SAME one #5071's
single-literal rule asks — so the two cannot disagree". They disagreed for
one day.

Both legs answer 4 on the reproducer after the change.

## The trap

**A representation change is not landed until every classifier that
describes the representation says the new thing.** The 2026-09-19 change
boxed the elements at the one site that builds them and measured the
semantic path's gain (+57 modules), the differential (411 agree, 0 diverge)
and a leak check — and every one of those was green, because every one of
them ran a module the semantic path produced, or a direct array literal. The
shape that broke needs the AST lowering AND an IIFE, and the differential's
"uncompilable on both sides" bucket is where a crash of that kind hides:
exit 139 on both legs agrees with itself.

`array_elem_is_env_boxed` has a doc comment that said, in so many words, "a
NON-capturing lambda element on its own is absent by design". A change that
contradicts a comment's design statement has to edit the comment, and the
edit is what would have surfaced the callers.
