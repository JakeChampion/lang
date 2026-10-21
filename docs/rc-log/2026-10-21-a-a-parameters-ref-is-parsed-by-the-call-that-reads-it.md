# 2026-10-21 — a parameter's ref is parsed by the call that reads it

`checker.bound_arg_accepts`, `generic_own_callback_accepts`,
`generic_scalar_own_callback`. Refs #8171.

## The shape

`bound_arg_accepts` decides whether an argument serves a parameter: unknown,
a literal that settles, a view borrow, a coercion, or, on a generic call, an
`own T` callback slot bound to a scalar, which reads the parameter's
`TypeRef`. Its callers handed it that ref for every argument of every call.
A generic function signature keeps its refs parsed (`FuncSig.refs`), but a
plain one does not, so `sig_param_ref` parsed the parameter's spelling
again on each call, and the two method-call sites parsed theirs with
`parser.parse_type_ref` outright. On the stage-2 compile of `checker.fern`
that was 125,470 parses, about 42 M instructions, plus the ref's release,
for a reading that returns false before touching the ref whenever the call
bound no type variable.

## What changed

`bound_arg_accepts` takes no ref. The own-callback reading is asked after it
declines, and only by a call that bound a type variable: a function call
through `sig_param_ref`, which a generic signature answers from its parsed
refs, and a method call through `generic_own_callback_accepts`, which parses
the spelling then. The predicates are pure, so asking the callback reading
last changes no answer.

## Measured

Stage-2 compile of `checker.fern` under callgrind, warm certifier memo, both
compilers built by themselves from their own tree, compiling one frozen copy
of main at 2210347; the base carries the kind-table slice of the same day:

| | Ir |
|---|--:|
| base | 16.114 G |
| this | 16.036 G (−0.48%) |
| `sig_call_type` inclusive | 465.1 M → 392.6 M |

The `checker.fern` binary is byte-identical, and so is every row of the
`selfhost-emit-hashes` sweep over the conformance corpus on all three
targets.

## Leads

What `sig_call_type` still pays per call: `check_expr` on each argument
(95 M), which the diagnostic walks have typed already; `call_type_bindings`
on a generic call (81 M over 10,318 calls); `coercible` and
`callable_assignable` inside `bound_arg_accepts` (93 M).
