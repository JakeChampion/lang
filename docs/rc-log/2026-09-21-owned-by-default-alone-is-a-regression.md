# Owned-by-default alone is a regression

2026-09-21 — a measurement, not a change. Sizing the self-host port of
`docs/OWNERSHIP-INFERENCE-PLAN.md`'s Slice 2. Refs #9891, #4451.

## The question

#9891 needs a reference parameter the body consumes to be COUNTED rather than
borrowed — `semsource.mode` makes one counted only where it is declared `own`.
Native ships this as Slice 2 (owned-by-default) with Sub-slice 2d (borrow
inference) on top. 2d is described there as an optimisation. The question was
whether the self-host could take Slice 2 first and 2d later, which is how
native staged it.

## The measurement

A throwaway compiler with `semsource.mode` returning `counted_mode()` for every
reference parameter — no escape analysis, nothing else changed — against
`examples/bench`, callgrind, x86-64, retired instructions.

| | baseline | counted params | |
| --- | ---: | ---: | ---: |
| `ordmap_insert` | 320,479,237 | 609,348,610 | **1.901x** |
| `sort_strings` | 181,128,542 | 200,091,572 | 1.105x |
| `pmap_insert` | 583,555,103 | 643,946,064 | 1.103x |
| `enum_match` | 151,875,079 | 163,800,075 | 1.079x |
| `utf8_ingest_validated` | 21,903,189 | 23,483,966 | 1.072x |
| `utf8_ingest_unchecked` | 18,127,789 | 19,402,766 | 1.070x |
| `pvec_with` | 681,006,132 | 707,194,677 | 1.038x |
| `ascii_scan` | 38,401,218 | 39,224,718 | 1.021x |
| `string_slice` | 70,920,088 | 72,300,088 | 1.019x |
| 19 rows | — | — | 1.000x–1.004x |
| `closure_call` | 135,534,082 | 135,312,068 | 0.998x |

All 29 exit codes match the baseline, so the convention itself is sound. **Not
one row improves.** The self-host compiler still bootstraps under it and its
leak census balances (`allocs=1591402 frees=1591402 live_bytes=0`), so this is
a cost, not a break.

## Why

The counted convention adds a caller-side retain and a callee-side release to
every reference parameter. Where the callee does not consume the argument, that
pair is pure traffic. `enum_match` is the clearest case:

```fern
function weigh(n: Node): i32 {
  match (n) {
    Leaf(v) => { return v * 2; },
    Label(s) => { return s.len(); },
    ...
```

`n` never leaves `weigh`. 900,000 calls, ~13 extra instructions each. A fresh
temp passed to a borrowed parameter is already reclaimed by the caller's
arg-temp path, so counting it buys no reclamation either — it just moves who
frees it, and charges an inc/dec for the move.

`ordmap_insert` at 1.9x is the shape worth understanding before the port lands;
whatever makes it that much worse is what borrow inference has to get right.

## What it settles

Slice 2 and Sub-slice 2d have to land **together** in the self-host. Native
could stage them because 2a was narrow (rc-eligible enum parameters in non-TRMC
functions); the blanket form measured here is not a staging of that, it is the
widened end state without its optimisation.

It also rules out the cheap substitute. A purely syntactic criterion — "counted
when the body matches the parameter and binds a reference payload" — is local,
order-independent, and needs no call graph, which is attractive because the
self-host builds contracts per declaration. But `enum_match` binds `Label(s)`
where `s` is a string, so the rule catches the reader and keeps the 7.9%. The
distinction that pays is whether a payload is CONSUMED or only READ, and that
is an escape property.

## Where it goes

`semsource.contracts(mod, scopes)` is already a fixpoint (`while (grown)`) over
every declaration in the flattened module, and it is the one table both the
callee's production and every call site read — so the def side and the call
site cannot disagree by construction, which is the property native has to
maintain by convention across `paramOwnedByDefault` and
`calleeParamOwnedByDefault`. A second fixpoint phase there is where the escape
analysis belongs.

Two constraints found on the way. Stdlib-to-stdlib import cycles are allowed
(`std/i32` ↔ `std/string`, docs/PRELUDE-TO-MODULES.md), so contracts cannot be
ordered bottom-up and the analysis has to be a fixpoint rather than a walk. And
the self-host does NOT need native's `addressTakenFuncs` rung (#7307): a
counted parameter is spelled in the function TYPE (`ssasem.closure_type` builds
`typeinfo.own_flags` from the same modes), so a call through a function value
reads the mode off the type and emits the retain, where native's
`OpCallIndirect` has no callee name to key one on. The `dyn` vtable rung
(#6465) is covered today by `decl_param_mode` borrowing every receiver
unconditionally — which is the rule the port changes, so that one needs care.

## The other half of #9891

Separately measured: the receiver must be counted too. With the payload take
(#9902), a counted `__pv_with_in` node AND a counted receiver, `pvec_with`
drops from 476,249 array clones to 0 and from 680,946,612 retired instructions
to 219,238,239 — 3.69x native to 1.19x. Any two of the three are inert; the
full table is on #9891.
