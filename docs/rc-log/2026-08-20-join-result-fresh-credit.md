# The `xs.join(sep)` result is a fresh string

The other half of `2026-08-20-strarr-join-receiver-borrow.md`, which made the
joined ARRAY reclaimable and left the joined STRING leaking. 400 rounds of the
churn harness, three compilers — main before either half, main after the
receiver half, and this:

| elements | x86-64 | arm64 | wasm |
| --- | --- | --- | --- |
| 1 | 102400 → 51200 → **0** | 102400 → 51200 → **0** | 89600 → 44800 → **0** |
| 4 | 377600 → 172800 → **0** | 377600 → 172800 → **0** | 345600 → 166400 → **0** |
| 8 | 742400 → 332800 → **0** | 742400 → 332800 → **0** | 688000 → 329600 → **0** |

## The obvious fix, and why it is unsound

`str_local_binding_is_fresh` is the syntactic classifier every fresh-string
credit goes through, and adding `field == "join"` to it takes all nine cells to
0. It also frees a live string. That function is deliberately state-free — its
own header explains the two-gate split, with the caller's `expr_is_str`
supplying the type half — and `expr_is_str` types a call by its DECLARATION, so

```fern
function (h: Holder) join(sep: string): string { return h.name; }
```

is a string-returning `join` whose result aliases a field the receiver still
owns. Written, measured, reverted; the fault is witnessed on both backends —
exit 97 on x86-64 (a churn string is handed the freed box and the read-back
fails) and a trap on wasm — against a clean main.

Worth stating plainly: **no byte gate could have caught this.** Over-releasing a
live box makes the heap delta better, so the numbers were perfect and every
existing suite passed. It surfaced only because the hazard probe was written
before the result was believed.

## Fix

`join_strarr_init` reads the receiver's DECLARED type out of the body and
requires `string[]`, exactly as `tostr_scalar_init` (#6599) already does for
`<scalar>.to_string()` — that precedent exists because the identical problem was
solved there, and its comment says outright that the receiver-type test cannot
live in `str_local_binding_is_fresh`. The credit then rides the same three gates
as every other `STR:` name: not already collected, not reassigned, and
`body_unsafe_for_clo` proving non-escape. No new prefix, no new `LocalInfo`
field, no new reclaim gate.

Choosing this over the `LocalInfo.strarr_builtin` shape from
`2026-08-20-builtin-strarr-element-reclaim.md` was the one real design call.
That pattern carries the type confirmation on the SLOT because the split/lines
credit is about an array whose elements the reclaim walks, and the walk needs the
flag at emit time. Here the question is settled entirely inside
`reclaimable_names_of`, so the lighter precedent is the right one — a second
`LocalInfo` field would have been machinery for nothing.

## Limit, stated rather than assumed

Declaration-reading sees only annotated `var`s in the body. A `string[]` PARAM
receiver has no `var` to read, so `function f(xs: string[]) { var s = xs.join(…) }`
is refused and its result still leaks — 131200 on x86-64, measured. Sound, and
pinned by a correctness case (`strarr-join-param-receiver-live`) with no ceiling,
so the leak is recorded without becoming a floor.

Lifting it means giving `reclaimable_names_of` the parameter types. It takes ten
parameters already and none of them is a type table, so that is a shared-state
question rather than an eleventh argument.
