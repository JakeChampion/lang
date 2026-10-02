# 2026-10-02 — a call result lent the parameter is a link of it

`ssarc.links_of`, `ssaunits.grow_rows`, `ssarc.bracketed`. Fixes #10962.

```fern
function f(b: i32[], v: i32): i32[] { return b.append(v); }
function g(b: i32[], v: i32): i32[] { b = f(b, v); return f(b, v + 1); }
```

`g` hands `b` to the first `f`, which grows the caller's buffer in place and
hands it back at the identity retain. The result held a unit of its own, so
the second `f` found the buffer at two counts, the caller's and that one, and
copied it whole. A loop that rebinds `b` from a call did the same: the phi
retained `b` at entry, and every turn copied.

## The rule

An append onto a borrowed parameter already had an encoding for this, the
link: a value that is still the parameter's box holds no unit, and any other
is a box of the frame's own. A call's result is now a link too, when the call
is lent the parameter (handed on, `ssaunits.hands`) or a link as the last
reader of its box (`call_root`). Right after the call, a result that is still
the parameter's box gives the callee's retain back (`settle_call_link`); the
caller's count keeps it live. A drop of a link releases only a box of the
frame's own (`drop_linked`), so a drop no longer removes a link.

The callee may grow the box in place only where the frame's own callers know
it may, so `grow_rows` follows a call's array operands back to the parameter
(`carried_params`) and gives a row for a value dying at a call to a callee
that grows that position (`dying_lent_rows`).

## A bug found on the way (#11023)

`pair(acc, acc)` lends one buffer to two borrowed parameters. `acc` dies at
the call, so the caller held no bracket, and `pair` handed one of them to a
callee that grew it in place under the other's reads: `b.len()` answered 3
where the interpreter and native answer 2. It predates this change. An
operand named twice is now bracketed though it dies at the call.
`TestSelfHostSemanticProduction/array-lent-twice`.

## Measured

`TestSelfHostGrowSoleOccurrenceX86_64`, `__arr_push_shared_count()` over 50
rounds; the interpreter answers the contents on every row:

| row | native | before | after |
|---|--:|--:|--:|
| L_two_calls_via_param | 0 | 44 | 0 |
| J_nested_call_arg | 49 | 44 | 0 |
| K_two_calls_via_local | 49 | 44 | 0 |
| M_call_then_inline_append | 49 | 44 | 0 |
| N_param_read_inside_loop | 0 | 45 | 0 |

L, J and K are one SSA graph, and M appends to the same link inline, so they
move together. Every row balances under `FERN_LEAKCHECK`.
`TestSelfHostSemanticProduction/call-result-link` covers a callee that hands
its parameter back unchanged, a link dropped on one arm, carried through a
loop with an early return, and read again after it is lent, each on x86-64,
the sanitizer, arm64 and wasm.
