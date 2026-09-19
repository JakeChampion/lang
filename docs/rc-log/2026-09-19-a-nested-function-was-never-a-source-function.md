# 2026-09-19 — a nested function was never a source function

`2026-09-19-the-sidecar-was-twelve-of-the-two-hundred-and-thirty-eight.md` left
its reproducer refusing at `function address is not a closure value`, and said
the next measurement belonged to whoever took that bucket. Here it is.

## The pair that names the cause

Two programs differing only in where `pick` is written:

```fern
function pick(b: i32): (i32) => i32 { return (x: i32) => x + 1; }
function mk(): i32 { var g: (i32) => i32 = pick(3); return g(4); }
```

```fern
function mk(): i32 {
    function pick(b: i32): (i32) => i32 { return (x: i32) => x + 1; }
    var g: (i32) => i32 = pick(3);
    return g(4);
}
```

The first produces 4 of 4. The second produced 1 of 4.

## What it was

`irlower.desugar_lambda_returns` rewrites a return-position lambda to a binding
plus a return of that binding, so the closure-lift pipeline gives it a unique
`$cloN` and an env-first box. Without it — its own comment says so, from #5266 —
a no-capture return is hoisted to a bare `__lam_N` address and the caller
dispatches env-first through it. That used to be a SIGSEGV; on the typed path it
is a refusal, `semsource.ident` declining a function-typed name.

It runs on the module's source functions as they enter the worklist, and
deliberately not on the bodies lifted out of lambdas, because #5281 found that
running it over a lambda that returns a lambda regressed `curry`.

A nested `function` declaration is a source function that the parser desugars to
a bound lambda. `try_lift_binding` hoists that lambda to `__lam_N`, and from
there it is indistinguishable from a lambda someone wrote — so the pre-pass
every other source function gets, it does not get.

## The fix, and why it is four lines

The information the exclusion needs already exists at the point it is lost:
`ExprLambda.origin` is the field the parser uses to say which construct it
synthesised a lambda for. `e_lambda_fn` — the only place a nested function
declaration becomes a lambda — now stamps `ORIGIN_NESTED_FN`, and
`try_lift_binding` runs the desugar over the body when it sees that stamp.
`curry` carries no origin and keeps its path.

## What it bought

| program | before | after |
|---|---|---|
| nested `pick`, no capture | 1 of 4 | 4 of 4 |
| nested `pick`, capturing | 1 of 4 | 4 of 4 |
| top-level `pick` (control) | 4 of 4 | 4 of 4 |
| generic clone, nested fn returning a fn | 0 of 3 | 4 of 4 |
| cross-module, nested fn returning a fn | 1 of 4 | 4 of 4 |

The capturing form is the one worth naming. It refused at `unsupported
expression`, a different leaf from the one this fix was aimed at, and the same
change clears it. Two rows of the histogram, one cause — the inverse of the
mistake the previous entry made, which was reading one cause into one row.

## The trap the origin sets

An origin is not a free label. `checker.e044_expr` used `origin.len() == 0` as
its proxy for "a lambda the PROGRAMMER wrote" — right when every origin marked a
parser-synthesised IIFE, wrong the moment one marks a construct the programmer
did write. Stamping the nested-function lambda silently switched E044 off for
every nested `function`, and the self-host checker accepted a capture the Go
checker rejects. Found in review, not by a gate: no corpus row exercised E044
through a nested declaration, so the checker differential had nothing to
disagree about.

The rule now reads `parser.is_written_lambda_origin`, which says what the check
means instead of encoding it in a length, and the corpus carries the row that
was missing. Before adding an `ORIGIN_*` value, the question to ask is which
sites treat the empty origin as a fact about the lambda rather than as the
absence of a label.

Writing the predicate down then showed that the old test had been wrong about a
second origin all along: a `use` callback is a lambda the programmer wrote, and
native reports E044 for a capture inside one, but `origin.len() == 0` excluded
it. The predicate admits `ORIGIN_USE` too, with the three corpus rows to pin it
— the two captures native reports, and a suspect declared AFTER the `use`, which
lives inside the callback body and must not read as a capture.

`ORIGIN_IIFE_SCOPED` looks like a third case and is not: the defer lowering
stamps it after the checker has run, so no checker rule ever sees that value, and
the self-host already agrees with native on a hand-written IIFE. Verified before
changing anything — the predicate says so rather than listing an origin that
cannot reach it.

Admitting the third syntax then exposed what the rule had been doing all along.
`e044_lambda_check` asked `stmts_mention`, which is purely syntactic and descends
INTO nested lambda bodies, so a suspect whose only appearance was a nested
binder's own parameter read as a capture. Since every coded diagnostic is a build
gate, that is not an over-report in a diagnostic — it is the self-host compiler
REJECTING a program native accepts. It was already true on main for a bare lambda
and for a nested `function`; admitting `ORIGIN_USE` added a third syntax to it.

The rule asks what a lambda CAPTURES now: `astwalk.collect_lambda_idents`, the
shadow-aware free-variable walk the capture consumers already share, which binds
params and threads `var` declarations in source order. Three more corpus rows,
one per syntax. A false positive that rejects valid code outranks the widening
that surfaced it, so the fix is the walk rather than the admission.

## The instrument, found while chasing what did not move

One shape kept refusing: the SELF-recursive nested function, at `unresolved type
of binding $binding$0$rec: ` with an empty spelling. I filed it, proposed a fix
from the message alone — the binding is given no type, so stamp it — and
prototyped that fix. It changed nothing.

The empty spelling was `type_detail` of an UNRESOLVED type, not an absent one.
Reducing further, to a self-recursive nested function returning a plain `i32`,
gave a different refusal: `call target has no semantic contract:
$binding$0$rec`, on a program with no function-returning anywhere in it. A
binding production has already removed, still being called.

`semsource_census_run` does not run `parser.module_with_builtins`, though every
real driver does. So it never runs `hoist_local_funcs_module`, the pass that
lifts a self-recursive local to a top-level declaration — nor `desugar_prepass`,
`lower_defers_prepass`, the three `monomorphize_*` passes, or
`inline_callonly_fn_values`. With `module_with_builtins` restored, both
self-recursive shapes produce whole, with or without this change:

| program | census as shipped | census + `module_with_builtins` |
|---|---|---|
| self-recursive nested fn, returns `i32` | 0 of 2 | 2 of 2 |
| self-recursive nested fn, returns a function | 1 of 3 | 3 of 3 |
| nested fn returning a fn, not recursive (control) | 1 of 4 | 1 of 4 |

The control is this entry's own defect, and it moves identically under both
pipelines — which is what makes the first two rows instrument rather than
coincidence. #9773 is closed as an artefact; the instrument is #9778.

This change's own numbers were re-measured under the restored pipeline and hold,
which was worth doing rather than assuming: the fix is in `try_lift_binding`,
which both pipelines run.

## The trap under the trap, and the measurement that shrank it

A refusal message is not a diagnosis, and a diagnostic driver is not the
compiler. Having found the second, I immediately wrote that every number steering
this effort was a lower bound by an unknown margin and the bucket ranking might
not hold either — and then measured it. 512 fernsmith programs, censused with the
driver as shipped and with `module_with_builtins` restored:

| | as shipped | + `module_with_builtins` |
|---|---|---|
| declarations measured | 509,527 | 511,063 |
| declarations produced | 496,592 | 498,128 |
| refusal leaves | 12,935 | 12,935 |
| distinct buckets | 139 | 139 |
| buckets whose count moved | — | none |

Not one bucket moves. The prepass adds three producing declarations per program
and changes nothing else. fernsmith does not generate nested `function`
declarations or self-recursive locals, so the corpus never had the shapes the
missing passes resolve. The distortion is real — a self-recursive nested function
goes 0 of 2 to 2 of 2 — and it is invisible to this corpus.

So the instrument bug cost a wrongly-filed issue and a wrong paragraph, not the
roadmap's numbers. Worth fixing for what it prevents next time; not a
re-measurement.

That is the third guess in one session that measurement cut down: the sidecar was
12 of 238 rather than all of it, the self-recursive shape was the instrument
rather than the language, and the instrument was narrower than the alarm I raised
about it. The pattern is not carelessness about any one of them — it is reaching
for the implication before running the experiment that bounds it.

What the sweep did surface, unmeasured and unclaimed: 91% of every refusal in the
corpus is three buckets, `call target has no semantic contract` on
`__fern_rc_inc` (7,168), `__fern_str_dec` (4,096) and `__map_hash_seed` (512).
`unresolved result type`, which two entries of this log have been chasing, is
396. Whether those three are a real gap or another artefact is the next
measurement, and this entry is not going to guess at it.

Twice now in three entries the error has been the same: read one refusal message,
infer a cause, act on the inference. The message names where the producer
stopped. It does not name why, and it does not promise the program reaching the
producer is the program the compiler lowers.
