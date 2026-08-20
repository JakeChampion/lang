# A nested Option/Result with a scalar inner payload was never reclaimed

One level of Option is fully reclaimed. Nesting one inside another dropped the
reclaim **entirely** — `frees=0`, not merely incomplete.

## What was measured

x86-64, `FERN_LEAKCHECK=1`, churn at 200 rounds:

| shape | native | before | after |
|---|---|---|---|
| `Option[Option[i32]]`, `Some(Some(i))` | 0 | `allocs=400 frees=0 live=16000` (80 B/round) | `frees=400 live=0` |
| `Option[Result[i32, i32]]`, `Some(Ok(i))` | 0 | `frees=0 live=16000` | `frees=400 live=0` |
| `Some(_)` — no arm binding | 0 | `frees=0 live=16000` | `frees=400 live=0` |
| `Option[Option[i32[]]]` | 0 | `frees=0 live=24000` (120 B/round) | unchanged — still refused |
| `Option[Option[string]]` | 0 | `frees=0 live=16000` | unchanged — still refused |
| `Option[i32]` / `Option[i32[]]` (controls) | 0 | 0 | 0 |

`live_bytes == 0` with `allocs == frees`, not merely flat: both boxes are
released every round, exactly as native does. All eight cases agree with
`bin/fern -interp` AND the native x86-64 backend on the answer, on all three
backends, with `__rc_underflow_count() * 100` folded into every exit code.

## Mechanism: THREE gates in series

Each was found by instrumenting the gate. Reasoning got the first one right and
would have stopped there — the fix moved nothing until all three were open, and
each intermediate build measured *identical* to the parent.

1. **`rcpayload_option_cand` produced no candidate.** It knows array, `string`,
   `string[]` and struct payloads; a payload that is itself an `Option[..]` /
   `Result[..]` matches none of them.
   ```
   PROBE opt name=o variant= ptype=          # before
   PROBE opt name=o variant=Some ptype=Option[i32]
   ```
2. **`opt_arm_binding_escapes` then called the arm's nested `match (inner)` an
   ESCAPE.** `binding_escapes_arm` uses `body_unsafe_for`, which flags any bare
   ident, and a match SCRUTINEE is a bare ident. #6127 gave the option LOCAL a
   match-borrow reading; the arm BINDING never got one.
   ```
   PROBE opt2 arms_use=0 dead_after=1 escapes=0 binds_esc=1
   ```
3. **`blockable` excluded it.** That flag marks payloads whose drop is COMPLETE
   on its own — string / `string[]` / leak-safe array — and it is what
   `lower_block`'s per-nested-block pass requires. A loop-body local is seen by
   exactly that pass, so a candidate that clears (1) and (2) is still skipped.

## The fix

`type_is_scalar_union` — `Option[<scalar>]`, or `Result[<scalar>, <scalar>]` —
names a box that owns **no pointer at all**, so one `__fern_rc_dec` is its
complete release whichever variant it holds. That is already what
`opt_payload_freefn` answers for such a ptype, which is why this is
admission-only: no new emitter, no new release helper.

Result needs BOTH arms proved. The drop runs after the match and does not read
the tag, so an `Err` arm carrying a pointer would be stranded by the very dec
that fully releases a scalar one.

Two guards carry the soundness:

* `opt_arg_is_direct_ctor` — the inner must be CONSTRUCTED here (`Some(x)` /
  `Ok(x)` / `Err(x)` / `None`). `var inner0 = Some(i); Some(inner0)` aliases a
  local whose own scalar-Option reclaim already frees that box, and this drop
  would be the second.
* `binding_escapes_arm_scrut` relaxes the SCRUTINEE position and nothing else,
  so `keep = inner` in the arm still escapes. It is sound only here because the
  inner box's payload is scalar: the nested match's own binding is a COPY that
  cannot outlive the arm. An rc inner payload gets no such reading.

## What is left, measured

* **The rc-inner half — 120 B/round, still open (#7218).**
  `Option[Option[i32[]]]` and `Option[Option[string]]` need a two-level drop:
  spill the inner box, release ITS payload, free the inner box, then the outer.
  That needs a routing marker through `OptRcFrees` (`pfrees` / `stys` carry one
  release string today) and a fresh proof for the inner arm's binding, which is
  an rc value and CAN outlive its arm where a scalar cannot. Both shapes are
  pinned as REFUSED in the hazard table, so widening has to be deliberate.
* **`Some(None)` plus a nested match bails the whole function (#7217)** —
  `FERN_STRICT_IR: f (did not lower: `match`)`, identical before and after this
  change. `some_opt_type` recurses into the payload to recover
  `Option[Option[i32]]` from the construction and has no `None` case, so the arm
  binding never gets its `mark_opt_type`. Routing the same `None` through an
  annotated local makes it lower, which is what isolates it. A lowering bug, not
  a reclaim one.

## Trap carried forward

The instrument-the-gate discipline is what this entry is really about. Three
separate builds measured byte-identical to the parent while each was strictly
closer to the fix, and no amount of reading would have distinguished "the
predicate is wrong" from "a later gate refuses anyway". An `eprint` at each gate
took ~22 s per rebuild and answered it in one run each time.
