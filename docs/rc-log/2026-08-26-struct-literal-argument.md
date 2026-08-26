# A struct literal passed as a call argument is freed after the call

*2026-08-26* — self-host only; native was clean throughout.

## The leak

`take(P { … })` — a temporary nothing else can reach — leaked its box and every
rc field it owned. Even a SCALAR-ONLY struct, whose release in statement
position is a single `__fern_rc_dec`:

| unbound struct literal used as | before | native |
|---|---|---|
| a discarded STATEMENT | clean | clean |
| a call ARGUMENT, scalar-only `S{a,b}` | **100 allocs / 0 frees** | 100/100 |
| a call ARGUMENT, array field `A{xs,k}` | **200/0** | 200/200 |
| bound first: `var p: S = S { … }` | clean | clean |

It leaks per EVALUATION rather than once, which is what separates it from the
construction-retain matrix's remaining cells — and it is invisible to that
matrix, because all 35 of its cells bind the literal to `var p` first. That is
the one position which already worked.

## Two missing pieces, and either alone is a no-op

The mechanism was already here. `lower_call_named` stashes a fresh literal
argument in a scratch local and frees it after the call, with arms for string
literals, scalar-array literals, `"ARR:"` / `"STRARR:"` producer calls and the
consumed-append temp. What was missing:

1. **The stash arm** for `ExprStructLit`, releasing with the discarded-statement
   arm's own two shapes — scalar-only takes the bare box dec, a type with
   reusable rc fields takes `__struct_drop_<T>` first, while the box still owns
   its fields, then the dec.
2. **A `"BORROW:"` row to consult.** Those rows are NARROW-SEEDED on purpose:
   `lower_func` seeds only the callees `lit_arg_callees_expr` saw carrying a
   literal argument, so the list stays tiny. A struct literal was not in that
   census, so `call_arg_borrowable` answered false.

Adding (1) without (2) measures as **no change at all** — which is exactly what
it did on the first attempt, and is worth knowing before concluding the arm is
wrong. A `DBGSL` trace at the decision point said `NOTREL` and named the cause
in one run.

## Safety

The gate is the borrowability test the string and array arms already use. A
callee that KEEPS the argument must not have it freed underneath, and both such
shapes stay refused and keep leaking, deliberately: `keep(p) -> p`, and
`wrap(p, i) -> Box { a: p, n: i }`.

Removing the gate puts `callee_wraps_param` at **self-host exit 99 — an rc
underflow — while native exits 3**, at a flat 300 allocs / 300 frees.

And once more the census favours the broken build: the same edit makes
`callee_returns_param` read a clean 200/200 where the correct compiler reads
200/0. Only a wrong-answer probe separates them, which is why
`field_handed_out_uaf` exists.

## The admission that looks unsafe and is not

`grab(p) -> p.xs` hands the array FIELD back out of the temp, and the caller then
deep-drops that temp. It measures clean (200/200), and the churned read-back
probe agrees across 200 rounds on all three oracles: the field read RETAINS, so
the deep drop decs rather than frees. Verified rather than assumed — the emitted
code shows `__struct_drop_A` really does run there.

String / enum / map / tuple / option fields keep `struct_fields_reusable` false
and so keep the documented safe-leak floor the statement arm states. Nothing here
widens it.

## Still open

The third position — an intermediate FIELD READ, `(S { … }).a` — is unchanged at
100/0. It has no existing stash to extend and is its own slice.

## Verification

`internal/e2eselfhost/self_host_struct_lit_arg_test.go`, 7 cases. Every want
confirmed against BOTH oracles. `TestSelfHostStage2FixpointArm64` green (119 s);
the targeted rc set green (264 s), including both construction matrices against
their pinned files.
