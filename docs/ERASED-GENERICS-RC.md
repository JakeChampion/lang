# Reference counting an erased type variable

The semantic source boundary refuses the `astwalk` fold family — 547 of the
1,439 functions it still refuses, the largest single leaf by a factor of
two. The refusal is not a missing form. It is that no sound unit rule for
an erased accumulator exists under the conventions the language has today,
and choosing one is a language decision with a measurable cost attached.
This file states the three candidates and what each costs, so the decision
can be made rather than deferred again.

`docs/SELFHOST-SEMANTIC-SOURCE.md` holds the census that ranks the leaf;
this file holds only the rule.

## The shape

    pub function fold_stmt_nodes[T](st: ast.Stmt, acc: T,
                                    visit_stmt: (ast.Stmt, T) => T,
                                    visit_expr: (ast.Expr, T) => T,
                                    descend: (ast.Expr) => boolean): T

and every one of its callers threads the accumulator by REPLACEMENT:

    acc = visit_stmt(st, acc)

Two facts about that line decide everything below.

**An erased `T` is a raw machine word.** The self-hosted compiler's
per-module emit path runs no monomorphiser (`astwalk.fern`, `map_expr_acc`),
so one body serves every instantiation. `erased_passthrough_safe` and
`erased_widenable` in `irlower.fern` are the whole of what the backends will
do with such a word, and both admit only a value that is PASSED THROUGH to
`return` — never one the body uses. The accompanying invariant is that an
erased slot is pointer-shaped or i32 and may not carry a bare wide scalar.
Nothing at runtime distinguishes the pointer case from the i32 case, so
`__fern_rc_dec` on an erased word decrements an integer at one
instantiation and a refcount at another.

**A function value lends its arguments and owns its result.** That is not
a convention this boundary invented: a function type spells no `own`, and
`irlower.make_wrap_named_func` enforces it by stripping `own` from a
counted array parameter when it builds the trampoline, so the trampoline
re-acquires at its own direct call to the consuming target.

Put together: each step of the fold is handed a unit of its own from the
visitor and still holds the previous one, which nothing but the fold can
see. Calling the erased result a unit makes the fold release a word it does
not own at a scalar instantiation. Calling it no unit leaks it at the
caller. There is no third answer available at the erased word itself.

A second fact rules out answering this per-callee. The visitors the
compiler actually passes disagree with each other, and one of them
disagrees with ITSELF: `parser.fwd_scan_expr` returns the `acc` it was lent
on one path and a fresh `FwdScan { ...acc, hit: true }` on another. No
annotation on the visitor can say which, because the answer is
path-dependent.

## Option 1 — a consuming accumulator

Let a function value CONSUME its erased argument and return a unit.
`acc = visit_stmt(st, acc)` then has exactly one live unit at every point: the
old one is moved into the call, the new one comes back, and the fold
releases nothing. Consuming and producing are both no-ops on a scalar, so
the rule is sound at every instantiation without the body knowing which it
has.

What it costs: a function type needs an owning-parameter spelling, and
`make_wrap_named_func` must stop stripping `own` for it. Every visitor is
recompiled under the new convention — the identity visitor becomes an
identity move, which is free, but a visitor that returns a fresh box must
now drop the one it consumed, which it does not do today.

It also settles the path-dependence above without annotating anything:
`fwd_scan_expr`'s `return acc` is an identity move and its
`FwdScan { ...acc, hit: true }` drops the consumed `acc`, both under the
same rule.

What it does not cover: a fold that reads its accumulator after passing it,
or passes it to two visitors, needs a retain it still cannot emit. No
current fold does either, so this buys the whole 547 — but it buys the
replacement shape only, and a later fold of a different shape is refused
again.

## Option 2 — box every erased value

Give a value bound to an erased type variable a uniform boxed
representation with a refcount header, so `__fern_rc_dec` is
unconditionally valid. This is the Perceus answer and it makes the fold
rule disappear rather than solving it.

What it costs: every scalar crossing a generic boundary allocates.
`erased_widenable` and the wasm i64 widening behind it exist precisely to
avoid that and would be deleted. Against fast-startup command-line tools
and the freestanding targets in `docs/BARE-METAL-PLAN.md` this is the
expensive option, and the cost lands on programs that never touch a fold.

## Option 3 — a drop function beside the erased word

The CALLER of a generic knows the instantiation. Pass a release function
pointer alongside each erased type variable — null for a scalar — and have
the fold call it on the intermediate it abandons. Sound, and no scalar is
boxed.

What it costs: a generic function's ABI grows one word per erased type
variable, and the indirect-call table is keyed by ARITY alone, so the extra
word changes the key for every generic reached through a function value.
That is the same table whose all-i32 typing already rejected a widened
trampoline with `indirect call type mismatch`.

## Where this sits

Option 1 is the smallest change that buys the measured leaf and the only
one that costs nothing at runtime; it is also the one that constrains what
a future fold may look like. Option 3 generalises and charges an ABI word.
Option 2 generalises furthest and charges every generic call in the
language.

None of the three is reachable from inside the semantic source boundary,
which is why the leaf is recorded as refused with a reason rather than
worked around. A tracking entry admitting the fold under a rule that is
wrong at one instantiation would be a use-after-free in the compiler, not
a leak.
