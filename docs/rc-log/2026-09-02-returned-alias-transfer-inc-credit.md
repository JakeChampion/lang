# 2026-09-02 — `returnsFreshBox` credits the return-transfer inc

## What was wrong

`returnsOwnBox` refused every returned ALIAS — a bare ident, a field read, an
index — and said why in its own comment:

> A PARAMETER is never in that set, which is the whole safety property:
> `return p` hands back the caller's box.

That premise had stopped being true. The Return lowering emits a transfer inc
for every returned alias whose type is `rcTrackedSlotType`, and
`needsRcIncOnAlias` — the predicate gating it — matches on the expression's
SHAPE (`*ast.Ident`, `*ast.FieldAccess`, `*ast.Index`) and its TYPE, never on
whether the aliased base is a local or a parameter. The one shape that skips
the inc, move-on-return for a bare owned local, excludes that local from the
exit sweep instead, which is the same transfer without the traffic.

The analysis was simply behind the codegen. `rhsTainted` is the only consumer
of `returnsFreshBox`, so the consequence was narrow and expensive: every caller
binding such a result kept the conservative call-result taint and never dropped
it.

Witnessed, not argued. Lowering `pick(r, i) -> r.names[i]` and `pickp(s) -> s`
at ptrW=8 emits `rc.inc __fern_rc_inc` in both, while `findReturnsFreshBox`
reported false for both.

## The change

`returnedAliasIsRetained(fn, pairForm, trmcFuncs)` is true when the function's
return type is `rcTrackedSlotType` and the function is neither pair-form nor
TRMC. `returnsOwnBox` takes it and credits `*ast.Ident` / `*ast.FieldAccess` /
`*ast.Index` returns with it.

Pair-form and TRMC are excluded because each rewrites the return before the inc
is reached — the pair-form ABI pushes (tag, payload) and returns early, TRMC
turns the return into an accumulator store. Refused by name, not reasoned
about.

`freshLocalsIn`'s call passes `retained=false`: the inc is emitted at RETURN
sites only, so an assignment's RHS earns nothing from it.

`findReturnsFreshBox` moved below `findTrmcFuncs` in `LowerWith` so the set is
available. Nothing in `findTrmcFuncs` reads the fresh-box map, so the order is
free.

## The inc is only half the accounting — a bare parameter is refused

Crediting a bare returned PARAMETER leaked `std/url` outright, and the
mechanism is worth stating because it is the same asymmetry #7995 is about.

`query_parse` threads a map through an accumulator:

```
m = __query_pair(m, s, pair_start, i);
```

`__query_pair` ends `return m` on its own parameter, so the return inc fires.
The caller's rebind is where the balancing dec would go, and the consumed-param
ownership flag starts at 0 — *this slot still holds the caller's borrow* — so
the rebind deliberately declines it. Nothing balances the inc, and the map
gains a reference per pair.

Measured on `url.query_parse("a=1")`, the smallest form that shows it:

| | allocs | frees | live bytes |
| --- | --- | --- | --- |
| before | 5 | 5 | 0 |
| bare-parameter credit | 5 | 2 | **256** |
| parameter refused | 5 | 5 | 0 |

A PROJECTION of a parameter keeps the credit, and the distinction is not a
carve-out: the protocol governs whether the callee released the parameter's own
reference, and `r.names[i]` is a different object the callee never owned, so no
protocol can decline to release it.

## Measured

Self-host driver compiling `input4.fern` to x86-64, `FERN_LEAKCHECK=1`:

| | allocs | frees | live bytes |
| --- | --- | --- | --- |
| before | 14,138 | 9,638 | 313,312 |
| after | 14,117 | 10,220 | **297,600** |

**−15,712 B (−5.0%), +582 frees**, compiler output byte-identical (md5
`57fc177b6d4d5142c806440a6b7807f4` both sides).

The change emits **no new instructions**. The incs were already there; only the
analysis moved.

Reduced probe (`mixed.fern`) — a function returning a fresh construction on one
path and `r.names[i % 3]` on the other, 60 rounds, `__rc_underflow_count()`
folded into the exit: 7 frees / 448 B before, 19 / **0** after. Exit 0 on
x86-64 and under `-interp` as the oracle.

Where the win comes from: crediting only `FieldAccess` and `Index` moves the
driver **zero** bytes (313,312, unchanged). The whole −15,712 B is the
local-ident arm — accumulator folds that end `return out`.

## Still on the table

The unrefused credit — bare parameters included — reads **261,488 B** on the base it was
measured against, another −35,792 on top of this. It is not available yet because it needs the exclusion
narrowed from "any parameter" to "a parameter this callee threads under the
consumed-param protocol", which is `computeConsumedParams` generalised beyond
the array projection #7995 added. That is the next increment, not this one.

## Banked

The conformance leak census moved on 16 rows, all downward — 40,830 to
**32,727** unpaired allocations across 456 fixtures, 92 of them still leaking.
The regex corpus carries almost all of it, because a matcher is built out of
exactly the shape this credit unblocks:

```
regex_captures_assert  35247 -> 28151
regex_replace_groups     493 -> 420
regex_named_groups       531 -> 511
regex_wordbound          231 ->  67
regex_ops                146 ->  17
regex_replace_edge        58 ->   7
```

The file's header block was stale from earlier banking rounds (it still
claimed 44,852 and a 37,295 regex_captures_assert); it now states the measured
figures.

`mixed_return_param_projection_is_owned` is pinned at 880 B on arm64 and 0 on
x86-64. That is a pre-existing two-word-ABI gap in the shape, not something the
credit introduced: the same case reads **1,264 B** on arm64 with the credit
removed, so the credit improves it by 384 B and the pin records what is left.

## Trap

`go build ./...` does not refresh `bin/fern`; use
`go build -o bin/fern ./cmd/fern`. `FERN_LEAKCHECK` is EMIT-time, so a baseline
built without it prints no leakcheck line at all rather than a zero — that
silence cost one measurement round here.

Do not `git stash` (or `git reset --hard`) while a suite is running:
`internal/e2e` shells out to `go build` and reads the live working tree, so a
mid-run change silently measures the wrong compiler.

`TestPairFormPayloadKeptWhenCallReturnsTheArgument` moved from
`releasesAfterMatch` to `releasesBoundPayload`. Its binding escapes into a
local, and that local now reclaims its own prior value — a release of the
DESTINATION, which the coarse "any dec anywhere" predicate cannot tell from the
arm dropping the binding. Its sibling at the same shape already used the
precise helper; behaviourally the case gains one free (12,816 to 12,800 B) with
the underflow counter at 0, so nothing was released that should not have been.
