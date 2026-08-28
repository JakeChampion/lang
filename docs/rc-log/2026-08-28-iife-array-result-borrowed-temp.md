# The IIFE temp carries an array result, borrowed

Closes #7686: a match-EXPRESSION whose arm hands back a bare ARRAY payload
binding did not lower at all —

```
FERN_STRICT_IR: rd (did not lower: immediately-invoked value block …)
```

This is an rc-log entry rather than a plain codegen one because the reason the
shape was excluded is a reclaim question, and the fix answers it by
deliberately declining to reclaim.

## Why it bailed

`iife_type_is_composite` admitted a leak-safe struct, a nominal enum or a
lowerable tuple, and nothing else. An array spelling fell through, so
`iife_match_composite_result_type` recovered `""`, no composite temp was
marked, and the bare payload arm then failed the scalar admission below it.

The intersection is what made it hard to see: match-expressions, arrays and
payload bindings all lower on their own. Five neighbouring shapes were already
clean (array literals, `if`-expressions, an array local or param, an i32 or
string payload, and the statement-form match).

## The ownership question, and the answer

The struct arm's existing comment sets the bargain: exclude what the IIFE path
cannot model so it "must leak cleanly". An array needs that bargain more,
because ONE temp can carry both ownerships at once — this issue's own repro is
exactly one of each:

```fern
return (match (e) { E.A(xs) => xs,   // BORROWED: the enum box still owns it
                    E.B    => [i] }) // FRESH: nobody else will free it
```

Marking the temp as an array slot is what drives the exit dec-sweep, and
sweeping the borrowed arm is the double-free class the ELB tier fences
(`element_handed_out`, a sanitizer-confirmed UAF in its direct form). So the
temp is marked ARRAY **and BORROWED**: `mark_borrowed_arr` is documented as
exactly this — "the exit dec-sweep's opt-out for an array binding that only
borrows a live owner's buffer" — and is already how the ELB tier treats a
payload binding.

The result: the array-ness is carried (so `v[0]` / `v.len()` on the bound
result resolve), the borrowed arm is safe, and the fresh arm leaks. Measured
400/100, 10400 live over 100 rounds against native's 300/300; the sanitizer
reports `leak 10400 bytes`, and **no over-release and no use-after-free**.
That is the polarity this gate is built on, and it turns "cannot compile" into
"compiles, correct, leaks one arm".

## Scope

`is_leaksafe_array_field` — the same predicate the destructure retain uses one
entry down, and right for the same reason: it is exactly the set the sweep
releases with a shallow `__fern_rc_dec`. A `string[]` or struct/enum-array
result stays OUT and still bails: their release is an element WALK, which a
borrowed temp cannot describe, and admitting one would need the fresh/borrowed
split resolved per arm rather than deferred.

Two smaller pieces rode along, both required by the widening rather than
optional:

- `iife_result_is_tuple` now rejects an array spelling. `"(" + "i32[]" + ")"`
  is a perfectly lowerable ONE-element tuple, so without the guard the array
  temp would have taken `mark_tuple_elems` and every later read would have
  resolved against the wrong shape.
- An array-LITERAL arm recovers its type (`iife_arr_lit_result_type`), read off
  the first element's tag. Without it the mixed repro still bailed: the payload
  arm recovered `"i32[]"` and the literal arm `""`, which the consensus check
  reads as "not uniformly typed". A mis-tag cannot miscompile — the consensus
  requires every arm to agree, so a wrong spelling fails to match the payload
  arm's declared type and bails, which is where the shape already was.

## Probes

`self_host_iife_array_result_ir_test.go`, x86-64 and arm64 legs, every case
under `FERN_STRICT_IR=1` (`runCaptureStrictIR`) — the answer alone cannot show
the shape stayed on the IR path, since a per-function bail reaches the same
exit code by another route. Every expected exit is `-interp`'s answer.

| case | exit |
|---|---|
| the repro (borrowed payload + fresh literal) | 3 |
| `f64[]` / `i64[]` payload | 3 |
| result bound then read (`v[0] + v.len()`) | 13 |
| **30-round loop returning `__rc_underflow_count()`** | **0** |
| six controls that lowered before (literals, if-expr, param arms, i32 / string payload, statement form) | unchanged |

The underflow row is the safe half: an over-release here balances the census
and is invisible to it, so the counter is the only witness.

## What remains

The `string[]` and struct/enum-array results still bail — the same boundary
the destructure retain draws, and for the same reason. Closing either needs
the per-arm fresh/borrowed split this entry defers, which is the real design
question #7686 names.
