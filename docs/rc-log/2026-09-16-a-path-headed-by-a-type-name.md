# 2026-09-16 — a path headed by a type name

With the value block leaf closed, `unbound name is not a semantic value`
sat at 22 corpus sites. The refusal named no name, so the first change
was to make it carry one: every site was a TYPE name at the head of a
path — `E.A(7)`, `Point.make(3, 4)`, `Wrap.default()`, `Option.Some(1)` —
not a module constant, which the parser substitutes ahead of both
lowerings.

## What it was

`call` routed a field-access callee to `method_call`, which produced the
receiver first; `field` routed a bare `E.B` to the record projection,
which produced the container first. Either way the head reached `ident`,
which found no binding and no zero-parameter contract of that name. The
match side refused the same spelling outright: `pattern_shape_error`
answered "unsupported qualified pattern" for `A.Zed =>`, six sites.

## What changed

`type_head` recognises a path head that names a declared struct, a
declared union or a builtin union and is shadowed by no binding. A call
under such a head is a variant construction when the call's type names
that union and the field its variant, else the associated function keyed
`Type.name` in the contract table — the key `decl_key` already gave an
impl's receiverless function — called as a direct call. A bare path is
the variant literal. In a pattern, a head equal to the scrutinee's union
is dropped (`unqualified`), for the arm's test and for the totality
count alike; a head naming anything else is still refused.

## A guard that short-circuits

The corpus run on that change turned up `produced graph fails semantic
verification: missing predecessor edge` on `Sm(0) when true`, and the
cause was in the guards that landed the same day. The parser folds a
literal payload into the guard as `__pl == 0 && true`, and `&&` opens
blocks of its own, so the guard's branch leaves from the `&&` join, not
from the arm's block; `next_preds` named the arm's block. `guard_gate`
now returns the block its branch leaves from and the next arm's test
lists that one. The verifier caught it, so the function was refused and
its module fell to the AST lowering; nothing was miscompiled.

Corpus, whole modules produced: 365 → 374; modules on the AST lowering
156 → 147.

## Pinned

`TestSelfHostSemanticSourceRC` runs `guarded_and` (a guard with `&&` over
a string payload, taken and not taken), `qualified_pick` (`Qe.Qb`,
`Qe.Qa("abc" + "d")`, `Option.Some(k)` and `Qe.Qa(s) =>` patterns) and
`assoc_make` (an impl's associated function returning a struct with a
string field) on arm64, x86-64, the sanitiser and wasm under the leak
check. The print golden carries `guarded_and` and `qualified_arm`, which
had pinned the refusal.

## Traps

**`Option.Some(m)` is not a pattern.** Native's module loader reads a
dotted pattern head as a MODULE and rejects `Option.Some(m) =>` with
"unknown module Option in variant pattern"; the self-host checker lets it
through and its AST lowering then bails on the match. The expression
form `Option.Some(k)` is accepted by both. The fixture uses the
expression and matches the plain `Some(m)`.
