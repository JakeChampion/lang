# 2026-09-02 — `match (h.e)` disqualified its struct from the field reclaim

`enum_scalar__callarg__stored_struct` is the only self-host row left in either
leak matrix. Two independent leaks are stacked on it. One is fixed here; the
other is still open, and the change that closes it on the matrix segfaults the
compiler, so the row keeps its `clean leak` verdict.

## The shape

```fern
enum E { A(i32), B(i32) }
struct H { e: E, n: i32 }
function wrap(e: E, i: i32): H { return H { e: e, n: i, }; }
// main: var e = A(i); var h = wrap(e, i); match (h.e) { … }
```

Controls, all native / self-host, `allocs`/`frees`, x86-64:

| variant                                              | native  | self-host (before) |
|------------------------------------------------------|---------|--------------------|
| struct of scalars only                                | 100/100 | 100/100 clean      |
| struct holding an **array** field, field read         | 200/200 | 200/200 clean      |
| struct holding an enum, field **never read**          | 200/200 | 200/200 clean      |
| struct holding an enum, **scalar** field read         | 200/200 | 200/200 clean      |
| `var t: E = h.e;` then `match (t)`                    | 200/200 | 200/200 clean      |
| **`match (h.e)`**                                     | 200/200 | 200/**101**, 3,960 B |
| the matrix cell (the above, via `wrap`)               | 200/200 | 200/**0**, 8,800 B |

What leaks is the ENUM box: growing `H` from two fields to eight leaves the
figure at 3,960 B exactly, so it does not scale with the struct.

## Leak 1 — the scrutinee marked its own field unsafe

`structfld_reclaim_ok_types_of` collects the field NAMES read in a position that
could outlive their box, and refuses `__field_reclaim_<T>` to any struct type
with an unsafe struct- or enum-typed field. Its statement walk had one
asymmetry:

```fern
ast.StmtVar(v)   => { a = structfld_safe_operand(v.init, a, borrowable); },       // borrow
ast.StmtMatch(m) => { a = structfld_collect_unsafe(m.scrutinee, a, borrowable); } // unsafe
```

A `var` initialiser is a transient borrow; a match scrutinee was not. So
`match (h.e)` marked `e` unsafe, which disqualified **H itself**, and the
generated `__field_reclaim_H` carried no arm for the enum field at all — no
`.Lfr_H_dec0` block. That is the entire difference between the leaking and the
bound form, whose drop sequences otherwise agree instruction for instruction
through both `__struct_drop_H` calls.

A scrutinee outlives nothing an arm does not own in its own right, so it is the
same borrow the `var` case already is. One word.

Closes the inline shape: `w_inlineenum`, `v_inline` and the eight-field
`sz_big` all reach 200/200, sanitizer-clean. The call form stays at 200/0.

## Leak 2 is still open, and the credit for it segfaults the compiler

`freshbox_ret_fns_of` — the subset port of native `findReturnsFreshBox` —
does close the call form on the leak matrix: `cell` goes 200/0 to 200/200,
clean on BOTH matrix legs, and the whole x86-64 matrix reads 134 clean/clean.

It also segfaults the compiler.

```
stage2-fixpoint-arm64
  gen2 (self-host-built aarch64, under qemu): signal: segmentation fault
  FAIL sort_wider   FAIL float_math   FAIL process_assertions   PASS lexer
```

Bisected locally, one case, same tree twice:

| tree                                    | `sort_wider` |
|-----------------------------------------|--------------|
| scrutinee-borrow + freshbox             | FAIL, 125 s  |
| scrutinee-borrow only                   | **ok**, 121 s |

So the leak-2 credit is the unsound half and is not landed. Leak 1's fix is,
and stands on its own: it closes the inline shape and leaves the call form at
200/0, which is why `enum_scalar__callarg__stored_struct` keeps its
`clean leak` verdict.

## The prediction that was wrong

Asked which half was unsound before bisecting, the answer given was the
scrutinee-borrow change: it widens the admission gate whose own comment warns
that under-counting reads is what "re-open[ed] the use-after-free #6148 was
reverted for", and marking a match scrutinee safe marks one FEWER thing unsafe
— exactly that direction.

The reasoning is sound and the conclusion was wrong. `freshbox` widens
free-eligibility for CALL RESULTS, and that is what breaks at compiler scale.
The correct half would have been reverted by luck and the wrong one by
argument.

## Why the matrix could not see it

Both matrix legs, census and `FERN_SANITIZE`, are green on both arches with
`freshbox` applied. The cells are small generated programs; the compiler
compiling itself is not, and only the stage-2 fixpoint runs that.

This is the SECOND gate escalation on this one row. The scrutinee release
passed the census and was caught by the sanitize leg. The freshbox credit
passed both legs and was caught by the stage-2 fixpoint. Neither is a
sufficient gate for a reclaim-widening change on its own.

## Three explanations that were wrong, and how each died

- **The call taint rule alone.** The original note said native reclaims this via
  `findReturnsFreshBox`. Returning that analysis's result set empty leaves the
  cell at 200/200 — native does not use it here. (It is still half the fix on
  the SELF-HOST side, which is not the same claim.)
- **The struct drop.** The never-read control is clean 200/200, and
  `__struct_drop_H` is called the same number of times in leaking and clean
  forms. `emit_struct_field_drops` was never at fault.
- **Releasing the scrutinee.** `lower_stmt_match` stores the scrutinee in a
  `$mscrut` scratch local that nothing releases, so releasing it looks right: a
  four-line gate beside `match_scrut_is_map_get` takes the inline form to
  200/200 with `__rc_underflow_count()` at 0 on every cell. Under
  `FERN_SANITIZE` it raises **use-after-free (touched a quarantined block)**
  where the unfixed baseline does not. The read is a borrow; the box stays owned
  by the struct.

## Standing rule from this one

Validate under `FERN_SANITIZE` **and** run the stage-2 fixpoint before claiming
a reclaim-widening change works. The census leg passed that
use-after-free: 200/200, underflow 0, every previously-clean control still
clean. Only the quarantine leg — where nothing is recycled and a touched freed
block traps — caught it. A green census plus a zero underflow counter is not
evidence of soundness.

`TestSelfHostLeakMatrixX86_64` runs both legs per cell and says so itself: *"a
latent defect the census could not see; fix it, never pin it."*

## What is still not explained

Why the any-tainted-arg rule is the binding constraint for the returned STRUCT
rather than for the enum inside it. Leak 2 is closed by observation, not by an
account of the taint's path.

The four arm64 rows reading `leak clean` are native-arm64 #7446 gaps where the
self-host is AHEAD, and are untouched.
