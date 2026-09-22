# The defer return temp takes every return type

2026-09-22. A function with a `defer` whose return type had no literal zero
kept its module on the AST lowering. The last corpus case held by a defer,
`defer_block_form`, moves to the typed path. Closes #9575.

## What it was

The defer desugar routes every `return E` through one shared temp:
`__defret = E; <cleanup>; return __defret;`. The temp was typed only where
the return type had a zero the desugar could spell, which after `ExprZero`
meant the scalars, `string` and the arrays; everything else declared it at an
untyped `0`. A function returning a record, a tuple, an enum or an `Option`
then assigned into an i32 slot ("replacement type does not match its
binding"), and one returning a `Result` had no expected type for `Err(...)`
to name its variant through ("call target has no semantic contract: Err"),
which is what held `defer_block_form` (4 declarations).

The restriction was there because a record-returning function with a defer
segfaulted on the typed path when the temp was first typed. That no longer
reproduces: a record, a tuple, a variant, an `Option`, a `Result` through an
`errdefer`, and a record built from two deferred locals all answer the same
on both legs and hold the sanitizer at zero.

A function-typed return had a second gap on top: the temp's annotation was
written as a bare spelling, so the checker could not resolve it ("unresolved
type of binding __defret: not yet checked"), and on the AST lowering the
caller of such a factory bare-called the returned box as code, a SIGSEGV on
the fallback rather than a refusal.

## What it does

`parser.dl_defret_decl` declares the temp at the zero of any annotated return
type (`dl_zeroable`), carrying the scope's `ret_fn_ret` /
`ret_fn_param_types` sidecars so a function type resolves; `dl_defret_zeroable`
is gone. `lower_defers_body` takes the two sidecars from its three callers
(a source function, an ordinary lambda, a scoped IIFE).

On the AST lowering, a fn-typed declaration started at its zero is bound a
closure local from its annotation (`clo_init` in the `StmtVar` lowering), and
`body_binds_closure_local` counts it as a box, so `closure_ret_fns_of` lists
the factory and its callers dispatch env-first.

## Measured

x86-64, typed path: `defer_block_form` 4 of 4, exit 60. Corpus census 518 →
**519** of 598 produced whole; exactly that case moves.

Two production rows (`TestSelfHostSemanticProduction`), each with an absolute
leak pin on the sanitizer leg, 200 rounds:
`defer-return-temp-at-a-type-with-no-literal-zero` (record, tuple, enum,
`Option`, `Result` through `errdefer`, two deferred records; 7 of 7, 2800
allocs and 2800 frees) and `defer-in-a-closure-factory` (a capturing, a plain
and a declaration-bound lambda returned past a defer; 7 of 7, 400 and 400).

## What it does not reach

The native backend answers the record shape differently: `var p: P = P { a:
3 }; defer p = P { a: 9 }; return p;` gives 9 there where the interpreter
and both self-host legs give 3, and the first production row above segfaults
under it. Filed as #10027; the rows compare the typed path against the AST
lowering and do not consult native.
