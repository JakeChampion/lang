# 2026-09-19 — a function value may live in an array

`2026-09-19-the-function-address-refusal-is-the-third-layer-of-one-wall.md`
named `ssasem.nests_func` as the wall and said lifting it was the work. It is
one line, and it does not work alone: the shape needs all three layers cleared
at once, which is why that entry measured three separate no-ops.

## The three

**`ssasem.nests_func`** rejected a function value as an array element. It no
longer does — `nests_func(a.elem)` where it read `holds_func(a.elem)`, so a
direct function element is admitted and an array of arrays of them is not.
Nothing else was needed for the release to be correct: `ssarc.drop_elements`
already walks each element through `drop_value`, which dispatches a function
type to `drop_captures`, and `ssasem.reference` already counts a function
value, so `has_children` already said the array had children to walk. The
verifier gate predated the #9637 work that made all that true.

**`ilc_expr_at`'s ExprArray arm** boxed lambda elements only when one of them
captured; an all-non-capturing array kept bare fn pointers on plain dispatch,
and a bare address is what `semsource.ident` refuses. Every lambda element is
boxed now. Four escalation conditions (#6555, #8163 and two more) already
forced the box when the array's value flowed somewhere that needed one; this
is the end of that trajectory rather than a new rule.

**`semsource.call`** took a bare name or a field access, so `v0[0](42)` was
"callee is neither a name nor a field". It now produces any function-typed
callee and hands the value id to `indirect_call`, which already dispatched
through an id rather than a name.

## What it bought

512 fernsmith programs, `GenMain(0..511)`, arm64-darwin:

| | before | after |
|---|---|---|
| produced whole | 291 of 512 | **348 of 512** |
| total refusals | 550 | 293 |
| verifier `function value is not an element` | 52 | **0** |
| `function address is not a closure value` | 24 | 1 |
| `is a function value main builds` | 210 | 86 |

**+57 modules.** The previous entry guessed 13 from "seeds this is the sole
refusal for" and said in the same breath that such a guess is unsound. It was
unsound in the other direction: clearing one layer frees declarations that
were queued behind the other two as well.

## Correctness

- **Differential, all 512 seeds**, semantic leg against the AST leg, exit code
  compared: **411 agree, 0 diverge**.
- **Leak**, an array of two capturing closures built and dropped per round,
  `FERN_LEAKCHECK=1`: `allocs=1000 frees=1000 live_bytes=0` at 200 rounds and
  `2000/2000/0` at 400, identical on both legs. The element walk reaches each
  box's captures exactly once.
- Driver size +0.097%, against a 5% drift gate.

## The uncompilable 101 are not new

The differential leaves 101 of 512 uncompilable on BOTH sides — a module the
semantic path refuses and whose AST fallback irlower's IR path then declines,
which is a whole-driver error rather than a divergence. Clean main gives the
identical `agree=411 diverge=0 uncompilable=101`, so this change moves none of
them either way.

Worth saying because one of them looks exactly like a regression this change
would cause. An array of arrays of closures called through a double index:

```fern
var rows: ((i32) => i32)[][] = [[((x: i32) => x + n)], [((y: i32) => y * 2)]];
return rows[0][0](35);
```

`FERN_STRICT_IR=1` names the bail "call of an indexed element", and the
tempting reading is that boxing pushed a double-indexed call onto env-first
dispatch that irlower cannot lower. It did not: `call_bail_tag` only LABELS a
bail, the callee is an `ExprIndex` whether the elements are boxes or bare
pointers, and irlower has never lowered that callee shape. The baseline count
is what settles it — the argument alone would not have.

## What is left

The tuple, Map and Cell arms of `nests_func` are untouched, and each refuses a
function value for the reason the array once did: nothing walks those slots
yet. `drop_tuple_fields` exists and `drop_children` already dispatches a tuple
to it, so the tuple arm may be the same one-line lift plus whatever
`semsource` needs for `t.0(...)` — which today is a separate refusal,
`unsupported call target: (((i32) => i32), i32).0`. Measure it; do not assume
it is one line because this one was.

The single remaining `function address is not a closure value` is a shape this
change does not reach, and `is a function value main builds` is still 86.
