# 2026-09-28 — an enum a closure call returns carries a count (#10577)

AST lowering (`FERN_SEM_IR=`) only. The typed lowering was already balanced on
every shape here, and still is. Stacked on #10585.

## The cause

A call through a function value, such as `g(w)`, had no fresh-producer verdict.
The "RCE:" registry and the "ENUM:" registry both key on a named callee, so an
rc enum a closure call returned was never released in any of these positions:

- lent to a borrowable parameter (`run(g(w), g)`);
- bound (`var e: S = g(n)`);
- rebound in a loop (`cur = g(w)`).

Whatever the payload was, the chain leaked.

## What changed

All in `examples/self_host/irlower.fern`.

- **The callee side.** Every body the lift hoists for a function value
  (`closure_body_name`: `$wrap` and `$clo`, reached through the value, and the hoisted no-capture lambda `__lam_` — a call argument, an IIFE callee, or a local that is only ever called — which is called by name and is named so the verdicts never leave one out) returns a declared enum `E`. Each
  such body is now an "ENUM:" member (`counted_closure_enum_rows`), and it
  retains any return it holds no count for (`ret_enum_closure_uncounted`).
  These returns hand over their own count and are not retained:
  - a construction;
  - a counted call;
  - an `own` parameter;
  - a returned "fresh" local, tagged "ERETOWN:" by `enum_ret_alias_rows`.

  A borrowed parameter is still retained by `ret_enum_param_handback`, and an
  "ERETALIAS:" local by `ret_enum_alias_uncounted`. So a call through a function
  value of result `E` always hands its caller one count. The registry records
  this as "CLOENUM:E".
- **Fresh chains.** "CLORCE:E" is written when every closure body of result `E`
  returns a fresh chain: a construction, or a forwarding call to an "RCE:"
  member of `E` (the new `fwd` list of `body_has_nonqualifying_rcenum_return`).
  Such a call is then an "RCE:" call, and a string payload gets the deep
  release.
- **Unknown results.** Neither row is written when some closure body's result
  is unknown (`closure_result_known`): a type variable, an alias, or no
  annotation on a body that returns a value. The body of a generic `mkid[T]`
  lambda returns `T`, so it may return any enum while declaring none.
- **Where the verdict is read.** `closure_enum_sigs` extends, per function, the
  two registries the statement-level readers resolve by callee name. Each
  function value a function binds once is added, as a parameter or as an
  annotated local: `ENUM:g`, and `RCE:g|E` too under "CLORCE:". This is how the
  following see a closure call with no change of their own:
  - the "ECALL:" binding credit;
  - the "RCE:" binding and rebind credits (`all_assigns_fresh_rcenum`);
  - the hoisted match scrutinee.

  The lowering-time readers go through `closure_enum_call_type`, which reads
  the value's declared result off its slot:
  - `enum_member_call_type`, and through it the "ECALL:" bind and the discard;
  - `fresh_enum_call_arg_type`, the argument stash.
- **Payload bindings.** A match binding of a fn-typed variant payload now
  records the payload's declared result (`mark_fn_value_sidecars`). A call
  through the binding, as in #9841's `f(w)`, is typed and counted like a call
  through a local.

## Measured (`FERN_LEAKCHECK=1`, AST lowering, allocs / frees)

| program | before | after |
|---|---|---|
| the issue's probe (string payload, lent) | 15 / 3 | 15 / 15 |
| `string`: lent, bound, rebound; lambda, trampoline, capture | 165 / 20 | 165 / 165 |
| `array`: the same with an `i32[]` payload | 277 / 30 | 277 / 277 |
| `closure`: #9841's chain, recursed, bound and rebound | 290 / 50 | 290 / 290 |
| `shared` (guard): the closure returns a captured enum | 30 / 20 | 30 / 20 |
| #9841's own probe | 29 / 2 | 29 / 2 |
| #9841's probe written as a recursion | 29 / 8 | 29 / 29 |

x86-64 and wasm agree on every row. The `FERN_SANITIZE=1` x86-64 builds report
only the guard's leak. `TestSelfHostClosureCallEnum{X86_64,Arm64,Wasm}` holds
every program to the interpreter's answer under both lowerings. All must
balance except the `shared` guard, pinned at 30 / 20. `fresh_local` pins the
fresh-chain verdict for a returned local (it leaks 145 / 84 without
`rcenum_ret_value`), and `lam_local` the `ERETOWN:` hand-over for a `__lam_`
body. `named_local`, a free function returning a local bound to a direct
construction, balances with or without that function's `RCE:` grant, since the
function is already an `ENUM:` member.

## Not covered

- #9841's own probe still reads 29 / 2. `run` starts its loop from
  `var cur: Step = s`, a parameter alias. That shape leaks with a direct call as
  well (15 / 0 for a string payload), because the alias makes the parameter
  unborrowable and holds no count of its own. Filed as #10588.
- The guard's 10 leaked blocks are the enum each closure captures. A capturing
  closure passed as an argument never releases an rc capture (#10587). The same
  program leaks the same 10 without this change.
- A value lent into a call through a function value (`g(keep)`) reads as
  escaping, so its credit is refused and it leaks (#10591).
- The verdict is whole-program. One closure body with an unknown result turns
  it off for every enum.
