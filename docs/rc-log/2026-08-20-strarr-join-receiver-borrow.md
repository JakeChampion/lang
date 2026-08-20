# `xs.join(sep)` made its receiver escape

A `string[]` local that would otherwise be fully reclaimed leaked its buffer and
every element as soon as it was joined. 400 rounds of the churn harness, a pair
of compilers from the same commit:

| elements | x86-64 | arm64 | wasm |
| --- | --- | --- | --- |
| 1 | 102400 → **51200** | 102400 → **51200** | 89600 → **44800** |
| 4 | 377600 → **172800** | 377600 → **172800** | 345600 → **166400** |
| 8 | 742400 → **332800** | 742400 → **332800** | 688000 → **329600** |

The same array literal WITHOUT the join measures 0 on every backend and every
size, which is what said the join call itself was the whole difference.

## Cause

`strarr_expr_unsafe` treated any method call on the array by name as an escape,
with one exception hard-coded inline: `len`. The default is the right way round
— a method returning an element, a slice, or the array itself hands out a
lasting alias — but `join` belongs on the other side of it. `__fern_arr_str_join`
walks the elements building a fresh accumulator with `+` and stores nothing, on
every backend: the register one is Fern source (`asmcore.rt_src_arr_str_join`)
written as `var r = ""` then concat *precisely so it cannot alias*, and wasm's
`$__fern_str_join` copies bytes into a freshly boxed result.

`strarr_borrowing_method` now names that set — `len` and `join` — as the
array-method sibling of `str_borrowing_method`.

## The half this does NOT do, and the unsound version of it

What remains is the join RESULT: `var s = xs.join(sep)` is not credited as a
fresh string, so the joined box leaks. Measured alone (array owned by the
caller, so only the result can leak): 131200 on x86-64, 128000 on wasm — 328
bytes per round for a 302-char result, which is exactly its box plus data.

The obvious fix is unsound and was **written, measured, and reverted**. Adding
`field == "join"` to `str_local_binding_is_fresh` takes the register leak to 0 at
every size, and it also frees a live string: that function is deliberately
state-free, so the arm is purely syntactic, and a user-declared

```fern
function (h: Holder) join(sep: string): string { return h.name; }
```

types as a string and gets credited. Witnessed as a fault on both backends — exit
97 on x86-64 (a churn string is handed the freed box and the read-back fails) and
a trap on wasm — against a clean main.

That is the identical trap `2026-08-20-builtin-strarr-element-reclaim.md` hit for
`split` / `lines`, and it wants the identical answer: a name-level candidate plus
a binding-site confirmation of the RECEIVER's type, which is the only place that
knows it. `LocalInfo.strarr_builtin` is the pattern to copy. That is the next
increment.

The RECEIVER half in this entry needs no such gate, which is why it could land
first: the escape analysis runs over a slot already known to be `string[]`, and a
user method cannot be called on one.

## Trap worth keeping

The hazard probe was written BEFORE the change was believed — that is the only
reason the unsound arm did not ship. Its heap numbers were perfect (0 at every
size, on every backend) and every existing suite passed. Nothing in the byte
gates could have caught it, because over-releasing a live box makes the heap
delta *better*.

Test ceilings here sit between the fixed number and the parent's rather than at
zero, since the result box is still leaking. They cannot become a floor: the
follow-up only makes the number smaller.
