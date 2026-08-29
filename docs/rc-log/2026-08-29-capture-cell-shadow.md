# The capture cell rewrote every binding of the spelling, not just the capture

A capture the lambda WRITES is boxed into a one-element cell (#2850/#5394):
`var n: i32 = 0` becomes `var $cell$n: i32[] = [0]`, every read of `n` becomes
`$cell$n[0]`, and every write becomes `$cell$n = $cell$n.with(0, v)`. That is
what gives a captured scalar by-reference semantics.

`box_rewrite_expr` / `box_rewrite_stmt` did the rewrite by matching a bare
`ExprIdent` against a flat `boxed: string[]`, and recursed into `if` / `while` /
`for` / `match` arms / `defer` **and into lambda bodies** with no notion of
scope. So every OTHER binding of the spelling was rewritten to the cell as well.

## Three shapes, all measured

| | interp | self-host, before | after |
|---|---|---|---|
| `for n in xs` over a boxed `n` | 6 | **9** | 6 |
| `(n: i32) => n + n` beside a boxed `n` | 21 | **3** | 21 |
| `match (…) { Some(n) => … }` beside a boxed `n` | 44 | **5** | 44 |

None crashed. Each returned a plausible wrong number.

The second is the worst of the three and not only for the value: the lambda's
body became `$cell$n[0] + $cell$n[0]`, so it ignores its argument **and gains a
capture it never had**. `lambda_captures` on the rewritten node now reports
`$cell$n`, which changes the lift decision (`try_lift_binding` threads a capture
argument that is not in the source) and the env-box layout in `make_clo_func`.

A fourth shape — a nested `var n` re-declaration — is guarded and unwitnessed;
the probe for it measured 55 on every side.

## Declining is NOT the fix here, and the probe said so

The first attempt reused the `try_lift_binding` precedent: refuse to box a name
the function binds more than once. `box_param` went to 21 (correct) and
`box_forin` went from 9 to **5** — still wrong, now in the other direction.

Of course it did. Without the cell the capture reverts to a construction-time
snapshot, so `bump()` returns 1 twice instead of 1 then 2. Declining is exactly
what boxing exists to prevent; unlike the lift, whose decline costs only an
optimisation, this one costs the semantics. The interpreter oracle is what said
so — a hardcoded expectation would have been "fixed" at 5.

So the rewrite is scope-aware instead: a `bound: string[]` threaded through all
three functions, extended at each scope that introduces a binding —

- a `for` binder, for the body but not the iterable (which is still outside);
- an arm's pattern binders, for that arm's body **and its guard**;
- a lambda's parameters, for its body;
- a `var` re-declaration, for the rest of its statement list.

and the cell DECLARATION is matched on its binding site (`first_var_site`,
first-in-source, which is the binding `cap_type_at` resolved the capture's type
against) rather than on the name, so a re-declaration is not rewritten into a
second `$cell$n` typed from the first one's entry.

## Where this came from

It was found by auditing the other AST pre-passes after the lambda lift's own
scope blindness turned out to be a live miscompile — #7253's first comment puts
this class in "the AST pre-passes and the lift, which run before any slot
exists", and the audit's prediction for each of these three shapes matched what
they then measured.

Two more from the same audit are NOT fixed here and are worth carrying:

- **`lambda_has_no_captures`** (`irlower.fern`) still forgives a free ident that
  names any module function, with no enclosing-scope check — the exclusion
  `lambda_captures` had removed for exactly this reason (#6191), never applied
  to its sibling. A local shadowing a module function's name would be resolved
  against the module inside the hoisted body. My probe for it did not reach the
  predicate (interp and self-host both 15), so it is a reading, not a
  measurement.
- **`parser.fnv_rewrite_stmt`** stamps `type_name: "fn"` using
  `fnv_name_called_in_block(fd.body, …)` — always the whole function body, never
  the enclosing block — so a nested `var f = mk` that is never called is stamped
  because a sibling `f` is called elsewhere. Wrong TYPE on the slot.
