# 2026-09-20 — a synthesised declaration's tag is a guess

`2026-09-19-the-other-167-were-a-type-name-that-does-not-exist.md` ended on
the 59 `$iife` owners refusing `unresolved result type: declared fn`, and
said the plumbing should land before any inference. The plumbing landed
(`coarse_fn_tag_without_contract`, one PR later). This entry is the
inference, and a census taken first showed the larger bucket was not that
one.

## The census, x86-64, 512 fernsmith programs

Compiled with `bin/fern-selfhost` at each step, `FERN_SEM_IR_REPORT=1`, and
every program ALSO built on both legs and run, exit codes compared:

| step | whole of 508 | agree | diverge |
|---|---|---|---|
| main + the env-box classifier fix (#9823) | 422 | — | — |
| a synthesised declaration's result is its body's | 446 | 499 | 0 |
| + the checker infers an unannotated lambda's result | 446 | 499 | 0 |
| + the contract table built to a fixpoint | **451** | 499 | 0 |

Four compile on neither leg (E042 from the generator) and eight more fail on
both; five fail on the AST leg alone and compile on the semantic one.

## Step one: `if_expr_rt` said i32

The leaf that outnumbered `declared fn` four to one:

```
__lam_0: return type: declared i32, returns boolean
main: binding $binding$0$v declared boolean holds a semantic value of i32
```

```fern
function gen(): boolean { return true; }
function main(): i32 {
    var v: boolean = (if (true) { gen() } else { false });
    …
```

A value-position `if` is a 0-arg IIFE whose `ret_type` is
`parser.if_expr_rt`'s reading of the first arm's SYNTAX — a literal's
suffix, a string, a boolean — and `"i32"` for anything it cannot classify:
a call, a record literal, a nested match. The AST lowering is fine with that
(an 8-byte slot is an 8-byte slot, the comment on `if_expr_rt` says as
much), `infer_ret_types_module` only ever WIDENS the guess to a 64-bit
type, and the checker never verifies it because the desugar runs first. The
semantic source read the tag as a declaration and refused the body for
contradicting it, then refused the caller for reading the contract.

`semsource.result_type` now treats a declaration the lowering synthesised —
`__lam_N`, `$iife` — as carrying a guess: the body is the authority when it
resolves, the tag stands when it does not. +24 modules.

## Step two: the lambda's own result

The `declared fn` leaf reduces to a match-expression whose FIRST arm is
itself a match-expression, both yielding unannotated lambdas:

```fern
var f: (i32) => i32 = (match (v0) {
    Active => (match (v0) { Active => ((d: i32) => d), … }),
    Inactive => ((c: i32) => c * 2),
    …
```

The inner IIFE returns a lambda whose result the checker left unresolved,
because nothing annotated it. `check_expr`'s lambda arm now reads the first
value-carrying `return` of the body in the body's own scope, so an
unannotated lambda has the function type its body gives it — everywhere the
checker is asked, not only at this boundary. `first_returned` and
`bare_return` moved from `semsource` into the checker to do it, since the
walk was the checker's already in all but file.

On its own this step bought **nothing** on the census: the outer IIFE
returns the CALL of the inner one, and a call is typed from the callee's
declaration, which says `fn`.

## Step three: the table to a fixpoint

`semsource.contracts` built the module's contract table in one pass, in
declaration order, and a hoisted body sits after the one that hoists it. A
synthesised declaration returning the call of another now reads the
callee's contract off the table, and the table is built until a pass adds
nothing. +5 modules, and the reducer above goes from 4 of 8 to 8 of 8.

## What is left

230 refusals, of which 138 are `is a function value <f> builds` and 21 `no
semantic contract` — both downstream of some other refusal in the creator.
The leaves: `unsupported iterable: Map` (7), `unsupported map shape` (6),
`declared fn` (5, one of them a lambda whose body returns a GENERIC call
the checker types as the erased unknown), `Option` declared without its
argument (3). None is a lambda gap any more.

## Trap

**A step can be right and buy nothing, and the census is the only way to
know which.** Step two is a real fix in the right place and it moved no
number; the previous entries' warning against predicting a bucket's worth
holds for a fix's worth too.
