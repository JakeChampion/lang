# 2026-09-19 — the function-address refusal is the third layer of one wall

`2026-09-19-the-sidecar-was-twelve-of-the-two-hundred-and-thirty-eight.md`
handed off `function address is not a closure value` as the next stage, with
"the three buckets that grew are where the freed declarations went. The next
measurement is theirs."

Taken, and it is not a stage. It is the outermost of three checks that one
shape meets in sequence, and clearing it moves the shape to the next one
without freeing a single module. **Nothing here landed.**

## The census on current main

`fernsmith.GenMain(0..511)`, each compiled by `bin/fern-selfhost` for
arm64-darwin with `FERN_SEM_IR_REPORT=1`, on `9c0bc5c11`:

| | rc-log entry before | measured here |
|---|---|---|
| produced whole | 285 | **291 of 512** |
| `unresolved result type` | 226 | **10** |
| `function address is not a closure value` | 30 | 24 |
| verifier: `function value is not an element` | 42 | 52 |

The `unresolved result type` collapse is the `bool` fix from
`2026-09-19-the-other-167-were-a-type-name-that-does-not-exist.md`. The
previous entry's own reproducer — a lambda returned from a nested function —
now produces 3 of 3 and runs. Re-derive before trusting any figure above.

## The shape reduces to three lines

```fern
function main(): i32 {
    var v0: ((i32) => i32)[] = [((x: i32) => x)];
    return v0[0](42);
}
```

A lambda in an ARRAY literal. Which container it is decides which refusal you
get, and the containers do not agree:

| position | verdict |
|---|---|
| `var f: (i32) => i32 = lambda` | produces |
| `apply(lambda, 42)` — call argument | produces |
| `Box { f: lambda }` — struct field | produces |
| `[lambda]` — array element | `function address is not a closure value` |
| `(lambda, 42)` — tuple element | `unsupported call target: (((i32) => i32), i32).0` |
| `Map[i32, (i32) => i32]` | `unsupported map shape` |
| `Fn(lambda)` — enum payload | verifier: `function value is not a field` |

Struct fields work and the other four containers do not, which is not four
bugs. `ssasem.nests_func` (ssasem.fern:1457) rejects a function value inside a
Cell, an array, a tuple, or a Map, and `field_placement_error` admits one as a
struct field. That asymmetry is the whole table.

## Two fixes, measured, both worth nothing

**Uniform env-boxing of lambda array elements.** `ilc_expr_at`'s `ExprArray`
arm boxes lambda elements only when one of them captures; an all-non-capturing
array deliberately keeps bare fn pointers on plain dispatch, and that bare
address is what `semsource.ident` refuses. Four escalation conditions (#6555,
#8163 and two more) already force the box when the array's value flows
somewhere that needs one, so making it unconditional looks like the end of that
trajectory.

    291 of 512  →  291 of 512

The 24 refusals go. `is a function value main builds` grows 210 → 276 and the
verifier bucket 52 → 72 by the same declarations. Zero modules freed, and the
AST path pays a `$wrap` trampoline per non-capturing element for it.

**Generalising the callee.** `semsource.call` accepts a bare name or a field
access; `v0[0](42)` is neither, hence `unsupported call target: callee is
neither a name nor a field`. `indirect_call` already dispatches through a value
id rather than a name, so the change is to produce the callee and hand over the
id — nine lines.

    291 of 512  →  291 of 512

Both together take the reproducer through all three layers in turn —
`function address` → `unsupported call target` → verifier `function value is
not an element` — and leave it refused. The program runs correctly at every
step, because the AST lowering stands the whole time.

Both reverted.

## The trap, which is the previous entry's trap again

**"Blocked by nothing else" is not a ceiling when the checks are layered.**
Of the 23 seeds hitting `function address`, 13 have no other refusal, and it is
tempting to read that as 13 modules waiting. It is not: those 13 have no other
refusal *at the stage they currently fail*. Clear that stage and they meet the
next, which is what the reproducer does in front of you.

The previous entry warned that a refusal count is not a queue of independent
defects. The same warning applies one level up, to the count of seeds a bucket
is the sole refusal for. The only honest ceiling is a census taken after the
fix — which is why there are three census runs above and no predictions.

## The next lead

`ssasem.nests_func`. Lifting it is the work: a function value has to be
placeable in an array, a tuple, a Map value and a Cell, which means the unit
planner and the RC lowering have to treat a boxed function value as the
reference it already is when it sits in a struct field. Struct fields are the
existence proof that the machinery can do it.

49 seeds hit the verifier rule today and 6 are blocked by nothing else —
and per the trap above, treat both numbers as where the wall currently stands
rather than as what lifting it buys. Measure after.

