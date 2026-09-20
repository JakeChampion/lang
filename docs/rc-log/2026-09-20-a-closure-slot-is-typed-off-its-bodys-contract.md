# 2026-09-20 — a closure slot is typed off its body's contract

The `declared fn` bucket: after the destination work the census had 18
programs left, and five of them refused a hoisted value block — `gen_f0$iife3`,
`main$iife1`, `main$iife6` — with `unresolved result type: declared fn`, and
two more a slot inside one, `unresolved type of binding $lamret$1`.

The shape is a value block whose arms are lambdas:

```fern
var f: (i32) => i32 = (if (c) { ((x: i32) => x + base) } else { ((x: i32) => x * k) });
```

`hoist_value_iife` declares the block a `fn`-tagged `$iife` function and
`desugar_lambda_returns` binds each arm's lambda to a `$lamret$N` slot, which
the lambda lift then fills with a closure constructor, `__mkclo$<body>(caps)`.
The checker types that constructor as nothing: the slot had no annotation,
and the `fn` tag's sidecars are empty for an if/match-expression, because the
arm's lambda spells no result type for `returned_lambda_contract` to read.
So the declaration's result was unresolved, and a slot inside it too.

The contract table knows. The hoisted body `<nm>$cloN` has a contract by the
time the `$iife` declaration's is read (the fixpoint fills what is missing
and reads again), and `ssasem.closure_type` is what a closure over it hands
out: the body's promise minus the environment parameter. `closure_slot_type`
answers that for a `__mkclo$` initialiser, unchecked until the body's contract
lands; `declare` uses it for a binding the checker left untyped, and
`inferred_result` for a synthesised declaration returning such a slot. The
previous entry's `RetSig` threading was half of this: the slot is declared
with the enclosing declaration's signature when there is one, and an
if/match-expression IIFE has none, so `ret_sig_of` now declares nothing
rather than a bare `fn` the checker cannot type.

Seeds 066, 254, 298, 309 and 427 produce whole. The stale paragraph in
`docs/SELFHOST-SEMANTIC-SOURCE.md` saying the rest of the map surface is not
admitted goes with this change: `without`, `cleared`, `keys`, `values` and
`for (k, v) in m` have been produced since #9825.

## A record literal is the struct it names

`unsupported record literal`, three seeds (052, 226, 369), reduced to a
record literal handed to a template with a value block among its fields:

```fern
var v: Xyz = pick(c, (Xyz { n: 636, valid: (if (d) { pick(true, false, true) } else { false }) }), (Xyz { … }));
```

`record` read the literal's type off the checker, and the checker leaves
such a literal untyped when a field holds a value block over a template
call. `record_schema` already admits only a struct without type
parameters, so the literal's type is the struct it names and nothing else;
`record_type` builds it from the schema, for the plain literal and the
`...base` update alike, and the checker's reading is not consulted. Seeds
052 and 369 produce whole; 226 moves to `map unit is not shared`.

## Census

| binary | whole | agree | diverge |
|---|---|---|---|
| main after #9826 | 491 / 509 | 500 | 0 |
| closure slots | 496 / 509 | 500 | 0 |
| record literals | 498 / 509 | 500 | 0 |

Only the named seeds change between rows. The compiler's own sources produce
whole (8629 of 8629).
