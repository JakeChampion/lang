# 2026-09-26 — the `?` failure edge releases what its expression staged (#8719)

A `?` inside a larger expression leaves through the owned-local exit sweep,
which sees declared locals only. What the enclosing expression had already
evaluated for a consumer that had not run yet — an aggregate's staged field,
a call's earlier argument, a string op's stashed left operand — was abandoned
on Err. Native only; the self-host AST lowering keeps the same gap (#8882).

## The register was already there

`builder.pendingScrutineeDrops` already carried a release owed for an extent
whose normal end an early exit branches past (a match's fresh scrutinee box),
and every exit already replayed it: the exit sweep for `return` and `?`, and a
loop-depth-bounded replay for `break` / `continue`. It is now `pendingDrops`,
and the construction sites register into it:

| site | what it registers | for how long |
|---|---|---|
| `spillAggregateOperands` (struct / tuple / array literal) | each staged operand's slot | until the box is bought |
| `emitEnumNew` hoisted payloads | each payload slot (EnumRcPayloads) | until the box is bought |
| direct-call argument loop | a stashed temp or lent view; an `own` / owned-by-default argument tee'd into a slot when a LATER argument can exit early | until the call |
| indirect-call arguments | the same, through the callee expression | until the call |
| string concat / `==` / ordering | the stashed left operand | until the right operand is done |

A concat whose right operand can exit early no longer grows its left temp in
place (`consumeLeftTemp`): the temp is stashed so the exit can reach it.

No ops are emitted unless an exit actually drains an entry, except the
argument tee, which is gated on `lastEarlyExitOperand`.

## The analysis half

An operand placed AFTER the `?` — `Pair { b: g(c)?, a: x }`,
`take_last(g(c)?, x)` with `x` an `own` param — was claimed as moved for the
whole statement, so the Err edge's sweep skipped a local nothing had consumed.
`identsAfterEarlyExit` names the identifiers source-ordered after a whole `?`
(or `return` / `break` / `continue`) in the same expression;
`markConstructionMoves` refuses them, and `walkDominatingExprs` no longer
offers them, which is why the own-arg and consuming-match claims are now made
at the identifier rather than at the call or match holding it.

The 2026-09-05 entry said refusing the claim "would add an inc/dec pair and
fix nothing". That holds for an operand evaluated BEFORE the `?`, which the
register now releases. It does not hold for one evaluated after: there the
refusal is the fix.

## Found on the way: the Ok path of a string payload

`mks(c) + gs(c)?` leaked on the OK path on main: a fresh `Result[string, _]`
box is shallow-freed once the payload is out and the string moves out with its
reference (`reclaimableTryScrutinee`), but `isOwnedStringTemp` had no `TryOp`
case, so the borrowing concat never released it. It has one now.

## Measured

arm64-darwin and wasm, `FERN_LEAKCHECK=1`, the three new corpus cases, 20
rounds alternating Ok and Err:

| case | main | after |
|---|---|---|
| `try_failure_edge_releases_staged_operands` | 700 / 630 / 2240 B | 700 / 700 / 0 |
| `try_failure_edge_releases_pending_arguments` | 520 / 450 / 2240 B (arm64), 540 / 470 (wasm) | balanced |
| `try_failure_edge_releases_string_operands` | 600 / 530 / 3360 B (arm64), 490 / 420 (wasm) | balanced |

The issue's rows 1 and 3 (`t1` / `t3`, 50 rounds): 125 / 100 / 800 B on main,
125 / 125 / 0 after, both targets. `-sanitize` on arm64-darwin over all 18
probe shapes: no over-release, `live_bytes=0`.
