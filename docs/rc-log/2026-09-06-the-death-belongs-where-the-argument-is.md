# 2026-09-06 — the death belongs where the argument is, not at the top of the value

`x = f(…, x, …)` marks x dead at that call, so #4873's containment bracket is
skipped and the append grows in place. Nest the call one level and the death was
not marked at all:

```
while (i < n) { c = emit(emit(c, i), i); i = i + 1; }         // 1923 ms -> 2 ms
while (i < n) { c = c.memit(i).memit(i); i = i + 1; }         // 1983 ms -> 1 ms
while (i < n) { c = emit(c, i); c = emit(c, i); i = i + 1; }  //    1 ms
```

20000 iterations, `-O`, x86-64-linux, one struct with an `It[]` field. The
method chain is the same thing spelled with a receiver, and the third row is the
same work written out, so it is neither about methods nor about the number of
emits.

`callArgDeaths`' self-reassign shape read `st.Value.(*ast.Call)` and asked
`markOnce` whether x was among THAT call's direct arguments. In
`emit(emit(c, i), i)` the outer call's first argument is a call, so the count
was zero and the inner call — the one that actually takes the cursor — was never
considered.

`markOnce` now takes the expression whose evaluation is the name's last chance
to be read (an assignment's value, a returned expression, or the call itself)
and finds the call inside it that takes the name. The exactly-once test it
already applied over one call now spans the whole expression, which is both the
soundness guard and what makes the site unambiguous: at most one call can take a
name that appears once.

## Why the boundary sat where it did

The fourth row of the same measurement is the tell:

```
function pair(c: C, v: i32): C { return emit(emit(c, v), v); }
while (i < n) { c = pair(c, i); i = i + 1; }                  //    1 ms
```

Identical nesting, already linear. Inside `pair` the cursor is a PARAMETER at
its last occurrence, so the last-occurrence shape reaches the inner call. In a
loop body it cannot — `repeating` excludes every call reachable from a loop,
because there a textual last read is many dynamic reads. The self-reassign shape
is the loop-safe one, and it was the one stopping at the top level.

## The rest of the space is flat

Twelve spellings of the cursor idiom were measured to find this one. The other
eleven were already linear and stay linear: method receiver, `if`/`else` arms
both rebinding, a nested struct field (`s = Outer { ...s, cur: emit(s.cur, i) }`),
`for … in`, a recursive accumulator, two cursors threaded in one loop, an array
element rebuilt with `.with`, a callee with an early-return guard, a tuple
destructure, a cursor through a lambda, and the one-statement baseline. That is
the map #8498 and #8633 were each one point of; nothing else in it is quadratic.

## Witnessed

- `internal/ir/call_arg_death_last_use_test.go` — `nested_in_loop` admitted,
  `nested_twice` (the value names the cursor twice) refused.
- `internal/e2e/append_borrowed_param_test.go` — `nested-call-rebind` on both
  native backends, which reads 23 with `computeGrowParams`' death propagation
  removed. Same containment leg as #8631 and #8660; it is the part of this
  family that has to be witnessed rather than argued.
