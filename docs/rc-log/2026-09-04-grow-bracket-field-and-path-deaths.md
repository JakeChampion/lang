# The append cliff's top site is the #4873 bracket, not the append (#7397)

`.github/cliff-baseline.txt` puts ~88% of the self-host compiler's copying
appends on `irlower.LowerState.emit` and `checker.Scope.bind`, and
`fern -append-report` calls both of them **in place**. #7397 asks which upstream
retain puts `s.ops` at rc > 1 before control reaches the site. It is the #4873
caller-side containment bracket — `growBracketArgs` (`internal/ir/ir.go`) and
the `emitGrowBracket` pair around the call — retaining the LowerState
argument's `ops` buffer because the caller's binding survives the call.

## How it was found, and the one instrument that answered it

The gdb census in `docs/PERFORMANCE-AUDIT-2026-08.md` §4d.5 names the FUNCTION
whose append copied; it cannot name the reference that forced it. What does:
break at the copy-path counter bump, `finish` to get the fresh buffer, put a
hardware watchpoint on its rc word, and print a Fern backtrace at every change
until the next crossing on that same buffer. `rep/`-style scripts are throwaway,
but the shape is worth keeping —

```
break *<counter bump inside __fern_arr_push_grow_ptr>   # esi > 100 to skip warmup
finish ; set $buf = (char *)$rax ; watch *(int*)($buf-8)
```

Over 60 consecutive crossings compiling `lexer.fern`, the LAST rc change before
each was, in every case, an inc attributable to one of three call sites in
`irlower` — never a drop that failed to fire.

**A holder census at the crossing is what rules the alternative out.** Walking
every frame's LowerState arguments and comparing `box[0]` (the `ops` field) with
the copying buffer reports exactly ONE distinct box at rc 2. So the second
reference has no owner at all: it is the bracket's bare inc.

## Why the bracket was there and why it can go

The bracket exists because a callee's rc==1 in-place grow is unobservable
INSIDE the callee and fully observable to a caller that keeps its argument live
(#4873). `callArgDeaths` already withdraws it for a whole binding that dies at
the call. The self-host's threading defeats all four of its shapes at once:

- `lower_view_borrowed_parked`'s `s` is live after the call — but only for
  `s.for_wasm()` and `expr_is_str(_, s)`, neither of which can reach `ops`;
- `lower_expr_binary` reads `s` again in five branches, each of which RETURNS,
  so no two of them run on one path;
- `var sr: LowerState = eqR.state;` binds the state out of an `ArgStash`, and a
  local bound from a field access was excluded outright as a possible alias.

Three deaths, all in `callArgDeaths`:

1. **`markUnobservedParamFields`** — a field-granular death keyed
   `p.f`, the key `markSupersededFields` already uses, so `growBracketArgs`
   consults it with one added line. It rests on `computeParamFieldObs`, a
   whole-program per-parameter summary of the fields a function can reach.
   The summary's give-up state is what makes it sound: an occurrence that is
   neither `p.g` nor a bare-ident argument of a direct call answers `all`, so
   returning the parameter, spreading it into a literal, binding it to another
   name, or an indirect call all read as "every field". A callee reachable
   through a vtable is `all` on every parameter — the static name at the call
   site is not the body that runs.
2. **The path-last-occurrence shape** — `order.isLast` is TEXTUAL, and a read
   whose enclosing statement list returns before mentioning the name again is
   equally final. A `break` or `continue` that can escape the list withdraws it.
3. **`unpackInitLocal`** — `var q = h.f` where `h` is a call-init local, `h.f`
   occurs once in the body, and every other mention of `h` selects a different
   field. Nothing else in the frame names that buffer, which is the same
   argument `callInitLocal` already rests on.

`computeGrowParams` propagates all three: a field the bracket skips is a field
the callee may grow through, so the enclosing parameter becomes growable AT
THAT FIELD and its own caller brackets a surviving argument there. That is the
induction #4873 already runs on the whole-name death, and getting it wrong is
how a skipped bracket turns into a value-semantics divergence rather than a
win. `growParams` is now a per-parameter field SET rather than a two-bit mask,
which is what lets the propagation name one field instead of all of them.

## Measured

`scripts/cliff-bench`, `examples/self_host/checker.fern`, x86-64, at
9e2993249 and with the change:

| | bytes | crossings |
| --- | --- | --- |
| base (9e2993249) | 202,926,200 | 376,969 |
| with the three deaths | **177,651,472** (−12.5%) | 403,531 (+7.0%) |

Per site, from the gdb census over the same compile:

| site | base bytes / crossings | after |
| --- | --- | --- |
| `irlower.LowerState.emit` | 156,918,008 / 35,105 | 132,890,016 / 28,991 |
| `checker.Scope.bind` | 22,383,696 / 244,028 | 21,381,760 / 277,542 |
| everything else | 23,624,496 / 97,836 | 23,379,696 / 96,998 |

**The count rises while the bytes fall, and that is the shape to expect.** An
append that grows in place hands its buffer back at rc 2 (the identity
convention `__fern_arr_push_grow` uses), so the NEXT append on it crosses —
where a forced copy would have handed back a fresh rc 1 buffer. The trade is
one whole-buffer copy for a later small one, which is why `Scope.bind`'s mean
copy fell from 92 to 77 bytes while it crossed 14% more often.

`examples/bench` static instruction counts move five rows on x86-64: the three
persistent-collection benchmarks lose bracket pairs (−3.6% / −1.3% / −1.2%) and
the two `utf8_ingest` ones gain 417 instructions each (+1.3%), where the wider
growable-field propagation brackets a call that was not bracketed before. Every
`.ir` row moves less than its tolerance. On aarch64 the same corpus moves three
rows and all downward (ordmap_insert −166, pmap_insert −104, pvec_with −108).

## Traps

**The two instruments in #7397 no longer disagree at the top site, and the
issue's 267 MB is two re-pins stale.** #8172 and #8190 landed between the issue
and this work; #8190 in particular flipped `x86_native.x86_resolve`'s two
`a.unknown` appends from COPY to in place, which was 70.8 MB of the 273.7 MB
this work started from. Re-measure before quoting any figure in that issue.

**The static append report is not a proxy for the counters, in EITHER
direction.** At the tree this started from, `x86_resolve` was 26% of the bytes
AND flagged COPY by the report — the disjointness `docs/TEST-GATES.md` records
holds at the top site, not everywhere.

**`site` in an rc trace is the immediate caller of the runtime helper, and gdb
attributes the bracket's inc to the line of the CALL it wraps.** Both hot
`irlower` incs report the line of `var p = lower_view_borrowed(e, s);` rather
than any line that mentions `ops`; the bracket has no source line of its own.

**Removing one level's bracket relocates the copy rather than deleting it,
until every level on the path is dead.** The first slice landed alone read
−0.6% on the gate: the inner bracket went and `computeGrowParams` promptly gave
the outer call one, because the callee's parameter had become growable. Only
with all three deaths does the recursion's `s.ops` stay at rc 1 through a
descent. Measure the whole gate after each slice, not the site you aimed at.

## What is left, and what it is not

The residue is the §4d.5 ratchet proper. With the brackets gone, the last rc
change before a crossing is the PREVIOUS in-place grow setting the buffer to
rc 2 — the predecessor generation's `LowerState` box, held by a frame that has
not dropped it, is the second reference. That is "the fix removes a BOX, not a
retain", and it still needs struct-update reuse on a uniquely-owned receiver
(§4d.5's unsound prototype, `TestStructFieldAppendAliasDifferential`). Nothing
here moves it.

Pins: `internal/ir/param_field_unobserved_test.go` (both new tests fail at the
parent — the field death by verdict, the other two shapes with their rules
stubbed out).
