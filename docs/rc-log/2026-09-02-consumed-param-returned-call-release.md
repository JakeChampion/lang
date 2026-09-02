# A consumed param named in a RETURNED CALL is superseded, not handed back

`2026-09-02-identity-return-counted.md` made every array-producing call result an
owned reference and gave the rebind store the matching release
(`rebind_call_same_dec`). It left one site on the old convention:
`emit_consumed_param_exit`, the sweep that releases what the frame owns at a
return.

That sweep skips a consumed-threaded array parameter whose name appears anywhere
in the returned expression, on the grounds that the value may BE that buffer or
alias it. Correct while a returned call handed its result back uncounted. Once it
carries a count of its own, being an ARGUMENT of the returned call is no longer
the handing-back case — it is the same supersession the rebind store one
statement earlier already releases for.

## The shape

```fern
function f(b: i32[], v: i32): i32[] { return b.append(v); }
function g(b: i32[], v: i32): i32[] { b = f(b, v); return f(b, v + 1); }
```

`b = f(b, v)` grows, so the store takes the different-pointer branch: flag := 1,
and from here the frame OWNS that buffer. `return f(b, v + 1)` pushes in place and
takes the identity retain, so the result leaves at rc 2 — the frame's own
reference plus the callee's. The caller releases one. The other is never
released, and it is not a leak that shows up as a leak: the buffer stays live
under the caller's slot at rc 2, so the caller's next in-place append reads it as
shared and copies the whole thing, every iteration.

Measured on the `growSoleCases` accumulator (50 rounds, two appends per round),
`__arr_push_shared_count()`:

| row | native | before 4715983 | 4715983 | this fix |
|---|---|---|---|---|
| L_two_calls_via_param | 0 | 0 | 44 | 0 |

The count is the whole symptom. The accumulator's length and endpoints are checked
before the counter is read on every row, so the wrong-answer failure mode was
never live here — the buffer was correct and copied.

## The rule

`emit_consumed_param_exit` still skips a param the frame is handing back, and now
asks the same question the rebind store asks about what "handing back" means: a
returned expression that is a direct call to a named free function carrying this
param as an argument supersedes the frame's reference (`rebind_call_same_dec`),
so the release stands. A bare `return p` is unchanged — no call, no count, the
frame's reference IS the returned one. So is `return p.append(v)`: a method call
is not a named free function, and nothing yet says what a same-pointer result
from one carries.

## What this does NOT close

`J_nested_call_arg`, `K_two_calls_via_local` and `M_call_then_inline_append` stay
at 44 and their pins moved to it. Those are the argument-temp class — the inner
call's result is a counted temp that nothing releases before the outer append
reads the buffer — and no consumed-param flag is involved, so this sweep has
nothing to release. Native copies on all three too (49), so the two compilers now
agree that a copy happens and differ only in the tally; before this they
disagreed about whether to copy at all, with the self-host doing fewer.

`docs/rc-log/2026-09-02-consumed-array-arg-temp.md` is the slice that takes them to 0. When
it lands, those three pins move with it — one of them moving on its own is a
regression, which is why they are pinned at a number rather than left loose.
