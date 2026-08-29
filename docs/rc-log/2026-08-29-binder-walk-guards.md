# Two guards counted `var` only, and one of them was load-bearing

`body_declares_name` and `local_decl_count` are both hand-rolled "what does this
body bind" walks in `irlower.fern`. Both saw a `var` (and `local_decl_count`
did not even see a `for` binder). Neither read a match pattern, a
comma-joined destructure, or `for (k, v)`.

`astwalk.collect_bound_stmts` is the complete version — one definition of the
question, pinned against `ast.NodeKinds` by `walk_exhaustive_test.go`, splitting
the comma-joined forms and reading `binding` / `at_binding` /
`extra_bindings`. Both are now three lines over it, and the two hand-rolled
copies are deleted.

## `local_decl_count` was a live miscompile, in a guard added the same day

`try_lift_binding` uses it to refuse the lambda lift when a name means two
things — the fix for the shadowed-lift miscompile earlier on #7253. That fix
was incomplete: `subst_fcall_expr` rewrites `f(…)` inside `for` and `match`
bodies, and the counter did not look at their binders.

```fern
var base: i32 = 10;
var f = function(n: i32): i32 { return n + base; };
var acc: i32 = f(1);
var fns: ((i32) => i32)[] = [(y: i32) => y * 100, (y: i32) => y * 200];
for f in fns { acc = acc + f(2); }        // <- the loop binder shadows
return acc;
```

| | interp | before | after |
|---|---|---|---|
| the `for` binder shadow | **99** | **35** | refused, see below |
| the rename control (`g`) | 99 | 99 | 99 |

35 is `11 + 12 + 12`: every element's call ran the hoisted `__lam_0(2, base)`
instead of the array's own lambdas. The count said 1, so the lift proceeded.

## The honest part: the class now BAILS rather than lowering

Declining the lift is correct, and it is all this change does. It is not
sufficient to make the shape work: the lambda then falls to the escaping-closure
path, which has no hoisted `main$clo` for a var-bound lambda whose name is later
rebound, so the module refuses with

```
FERN_STRICT_IR: main (did not lower: lambda: no lifted `main$clo` for the escaping closure)
```

Three variants were tried — a capturing lambda, a capture-free one, and a called
match payload binder — and all three bail. So **every program in this class
trades a wrong answer for a named refusal.** That is the direction the project
asks for (a bail is a bug report, not a silent fall-through) and it is strictly
better than 35, but it is a capability gap, not a completed feature.

Closing it needs the rewrite to become scope-aware rather than the lift to
decline — `subst_fcall_expr` / `subst_fcall_stmts` threading a `bound` set the
way `box_rewrite_*` now does, AND `try_lift_binding`'s leftover-`fname` check
becoming scope-aware in step, since after a scope-aware substitution the
shadowed occurrences remain and would decline the lift anyway. Two walks, and a
feature rather than a fix: the shape has never lowered correctly.

## Blast radius: none

| | before | after |
|---|---|---|
| `selfhost-emit-hashes` rows that differ | — | **0 of 1563** |
| fixtures the compiler REFUSES | 146 | **146** |

Not one byte moves and not one fixture newly bails — the refusals are the
pre-existing set. The corpus contains no program of this shape, and neither do
the compiler's own sources.

## `body_declares_name`'s half is inert, and that was measured

Instrumented to report whenever the narrow walk said "not declared" while the
complete binder set said otherwise:

| corpus | divergences |
|---|---|
| `conformance/cases` (514 fixtures) | **0** |
| the compiler's own sources (~190k lines) | **0** |

So this half is a latent hazard, not a live one, and it is fixed because the
direction is free: all nine call sites use it to VETO a name-keyed registry hit,
so a guard that sees more can only refuse more credits — a leak at worst, never
an over-release.

**The parameter half is still open and is the larger one.** Six of the nine
sites do not check parameters at all, and `body_declares_name` cannot: it takes
`stmts`, not the enclosing `FuncDecl`. A static upper bound over the trees —
functions whose parameter shadows a same-file module-function name — is 17 in
`examples/self_host`, 1 in the stdlib and 0 in `conformance/cases`, none of them
shown to reach a producer credit. Closing it means threading a param list
through six signatures, which wants its own diff and a witness first.
