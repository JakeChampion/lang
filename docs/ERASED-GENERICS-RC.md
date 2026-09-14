# Reference counting an erased type variable

An erased type variable is ONE MACHINE WORD — pointer-shaped or i32 — and
nothing at run time tells the two apart, so a generic body may emit neither
retain nor release on one: `__fern_rc_dec` on an erased word decrements a
refcount at one instantiation and an integer at another. That is the whole
constraint, and the rule below is the one the semantic source boundary
implements.

`docs/SELFHOST-SEMANTIC-SOURCE.md` holds the census; this file holds the rule.

## The shape

    pub function fold_stmt_nodes[T](st: ast.Stmt, acc: T,
                                    visit_stmt: (ast.Stmt, T) => T,
                                    visit_expr: (ast.Expr, T) => T,
                                    descend: (ast.Expr) => boolean): T

and every one of its callers threads the accumulator by REPLACEMENT:

    acc = visit_stmt(st, acc)

Two facts about that line decide everything below.

**An erased `T` is a raw machine word.** `internal/monomorph` clones a
generic per instantiation on the native pipeline, but the self-hosted
compiler's per-module emit path runs no monomorphiser at all
(`astwalk.fern`, `map_expr_acc`), so there one body serves every
instantiation. `erased_passthrough_safe` and `erased_widenable` in
`irlower.fern` are the whole of what the backends will do with such a word,
and both admit only a value that is PASSED THROUGH to `return`. The
accompanying invariant is that an erased slot is pointer-shaped or i32 and
may not carry a bare wide scalar.

**A function value lends its arguments it does not SPELL consuming.** A
function type carries a per-parameter `own` mask — `(ast.Stmt, own T) => T` —
and every slot it does not mark is lent. `irlower.make_wrap_named_func`
mirrors the target's modes exactly, so a bare name reached through a
trampoline hands the caller's unit straight through rather than re-acquiring
at the trampoline's own direct call.

Under a LENDING convention each step of the fold would be handed a unit of its
own from the visitor and still hold the previous one, which nothing but the
fold can see. Calling the erased result a unit makes the fold release a word
it does not own at a scalar instantiation. Calling it no unit leaks it at the
caller. There is no third answer available at the erased word itself, which is
why the slot has to be consuming instead.

A second fact rules out answering this per-callee. The visitors the compiler
actually passes disagree with each other, and one of them disagrees with
ITSELF: `parser.fwd_scan_expr` returns the `acc` it was lent on one path and a
fresh `FwdScan { ...acc, hit: true }` on another. No annotation on the visitor
can say which, because the answer is path-dependent.

## The rule: a consuming accumulator

**An erased position is CONSUMING.** The caller gives up its unit at the call
and takes back the unit the call returns, so `acc = visit_stmt(st, acc)` has
exactly one live unit at every point: the old one is moved into the call, the
new one comes back, and the fold releases nothing. Consuming and producing are
both no-ops on a scalar, so the rule is sound at every instantiation without
the body knowing which it has. It settles the path-dependence above without
annotating anything: `fwd_scan_expr`'s `return acc` is an identity move and its
`FwdScan { ...acc, hit: true }` drops the consumed `acc`, both under the same
rule.

Where it lives:

- `typeinfo.TypeErased` is the erased variable as a TYPE of its own, distinct
  from the unknown a checker failure produces. `semtypes.word` says where one
  machine word is admissible — a value, a parameter, a result, a
  function-signature slot — and `semtypes.concrete` stays false for it
  everywhere else, so an array element, a record or variant field, a map
  position and an operator operand each refuse on their own account. That is
  why `util.append_all[T](into: T[], …)` and `astwalk.map_expr_acc`'s
  `(ast.Expr, T, boolean)` result stay refused: releasing a container walks its
  slots, and an erased slot names no drop.
- `semsource.erase` resolves the checker's unresolved type variables to that
  type, structurally, so a function-typed parameter's own erased slots resolve
  with it. A HOISTED closure body counts as erased-generic too: the lift copies
  the enclosing generic's spellings and declares no type parameter of its own,
  because the arity-keyed indirect-call table fixes that body's ABI.
- `semsource.mode` gives every erased parameter `counted_mode`, and
  `ssaunits.mode_error` refuses any other mode there.
- `ssasem.instantiate` makes a contract carrying an erased position a TEMPLATE
  that the call site instantiates: binding is structural and left to right, so
  a function-typed parameter's erased slots bind through it, and one variable
  binds one type for the whole contract.
- `ssaunits.call_value_supplied` is the ABI: an indirect call lends every
  reference it is handed EXCEPT at a slot the function type spells CONSUMING —
  `own T`, or an erased variable, which is consuming on its own account.
  `ssasem.closure_type` builds that mask from the body's declared modes, so
  the type a box hands out and the body inside it cannot disagree.

### What proves it, rather than assuming it

An erased unit is planned like any other and then held to one extra rule:
`ssaunits.erased_move_error` refuses a function where any supply of an erased
value is a RETAIN, or any drop names one. A retain is what the planner chooses
for a value still live at its own consumption — read after being passed, or
passed to two calls — and a drop what it chooses for one abandoned unconsumed.
Neither has an emission here, so either refuses the whole function rather than
being miscompiled at some instantiation. Every other erased supply is a MOVE,
which emits nothing at all, so the plan is the same whether the instantiation
is a pointer or a scalar.

`internal/e2eselfhost/self_host_semsource_test.go` pins both directions: the
print golden carries `refused_reread`'s "erased value is live across its
consumption" and `refused_abandon`'s "erased value is abandoned unconsumed"
beside a produced `fold_two`, and the executable fixtures run erased folds —
straight, nested through a second generic, and carried through a loop phi — on
four targets under `FERN_LEAKCHECK` and the sanitizer, with a heap value held
across the fold and read back after churn so an over-release is a wrong ANSWER
and not just a balanced count.

### The REFERENCE instantiation, and what it took

A variable bound to a reference is admitted exactly when every position it
occupies CONSUMES it: the contract's own counted parameter, or a slot a
function type spells `own`. One lending occurrence refuses the whole
instantiation — the unit would reach it and nothing would release it —
which is what `ssasem.instantiation_error` decides, from the CONTRACT rather
than from the binding alone.

That took the convention end to end rather than inside this boundary:

- A function TYPE spells its consuming slots, so a consuming function value
  and a lending one have different types and neither reaches the other's
  position. The mask is part of the type everywhere it is compared — the
  checker's assignability and inference, `semtypes.equal`, and `bind_within`,
  which reads the flag against the slot the ARGUMENT carries so `own T` at a
  variable bound to a SCALAR stays vacuous.
- `own` on a scalar is normalised OUT of the type (`typeinfo.own_flags`
  against the parameter types, re-applied on substitution and erasure),
  because a scalar carries no unit to hand over. That is what lets one
  generic body serve a scalar and a reference instantiation at once.
- The visitors declare it: `own acc: T`, which makes `return acc` an identity
  MOVE and `FwdScan { ...acc, hit: true }` a construction that drops the acc
  it consumed. Both fall out of the parameter's mode with no annotation on the
  body.
- The trampoline mirrors the target's modes, so a bare NAME at a consuming
  slot hands the unit through.
- The AST lowering reads the mask off the callee's TYPE at an indirect call,
  so the overwrite-dec, the transfer claim and the compensating retain agree
  with the callee the same way they do at a direct call. Before that they
  could not: `apply(eat)` for `eat(own xs: i32[])` freed the argument TWICE
  and `apply(eat, a)` leaked it, neither spellable now.

Measured on the compiler's own sources: 6597 produced functions before,
7090 after, and the leaf that read **erased instantiation carries a unit**
(693 functions) is gone.

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

Either would also buy the reference instantiation the consuming rule refuses,
which is the measured reason to keep them on the table.
