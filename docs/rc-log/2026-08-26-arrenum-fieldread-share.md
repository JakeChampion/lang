# The enum-array field READ is a counted share

*2026-08-26* — closes `enum_arr__fieldread`, the last cell of the enum-array
group and the worst single cell on the construction-retain matrix (600 allocs /
300 frees against native's 600/600). Matrix: 10 leaking cells → 9.

The diagnosis this implements is
`2026-08-26-construction-retain-remaining.md` §`enum_arr__fieldread`. It was
right about the shape of the fix and complete about the two markers; it did not
have the two preconditions below, which only measurement produced.

## The shape

```fern
var q: P = P { f: mkv(i), n: i };   // q owns the E[] buffer
var p: P = P { f: q.f, n: i };      // the read aliases it — uncounted
```

`q.f` lowers via `struct_get` to the source box's buffer, so `p` co-owns it. The
credit pass had marked BOTH holders box-only (`"NODEEP:"`), each for its own
correct reason while the read is uncounted: `p` because
`struct_lit_unretained_borrow_field`'s exclusion list has no enum-array entry, so
the field value reads as an un-retained borrow; `q` because
`optstruct_body_moves_field` counts a field read in a struct-literal field as a
positive MOVE position. Both verdicts flip together the moment the read is
retained, and neither may flip without it.

## One predicate, three sites

`enum_arr_field_share_read` decides, and the retain in the `ExprStructLit`
lowering plus the two marker flips in `bind_var_slot` all ask it. That is the
safety argument: the retained set and the flipped set are identical *by
construction*, not because three separate conditions happen to line up. The
markers are not taught to the two pre-pass predicates instead, because those run
before the slot types the retain depends on exist — an exclusion wider than the
retain flips a marker with no inc behind it, which in this class is a double
free.

The retain arm sets only `fav_alias_inc`, never `fav_ok`, so it never reaches the
admission gate's `s.fail()`. That matters: `is_enum_array_field_type` bottoms out
in a negative test a `pub type A | B` union passes, so `parser.Stmt[]` — and with
it every statement block in the compiler — is inside this class.

## Two preconditions, both found by measuring

**A base spread mints an uncounted third owner.** `T { ...base }` copies every
field pointer into a fresh box with NO inc. Two rc-gated walks against a count of
two is one release too many: `respread` measured **exit 99 at a flat 700 allocs /
700 frees, live_bytes 0** — the census reading perfect while
`__rc_underflow_count` fires.

Gated by FIELD TYPE rather than by holder name, unlike the arrstruct and arrenum
credits' `*_share_holder_respread`. Those know their holders; this one decides
mid-expression, where the dangerous base can name a local with no slot yet:

```fern
var p: P = P { f: q.f, n: i };   // decided here — `p` has no slot
var z: P = P { ...p, n: i + 2 }; // and `p` is the base
```

`LowerState.spread_sites` carries both questions' rows, seeded once per function
by `spread_sites_of`: `"N:<base>"` for the by-name question, one `"FT:<type>"`
per field the spread type declares for the by-type one. It replaced
`body_spreads_any` / `expr_spreads_any`, whose expression walk also missed
`ExprTuple`, `ExprSlice`, `ExprMapLit` and `ExprFString`.

**`"NODEEP:"` and `"FLDCHECKED:"` are two arms of one verdict.** They come from a
single `if (mv_moves) … else if (mvsty != "") …`, and a block-scoped slot
deep-drops only on the second witness (#6127). Dropping NODEEP alone left `p`
with neither marker:

| shape | before | with NODEEP dropped only | with the witness written |
|---|---|---|---|
| `var p: P = P { f: q.f … }` at top level | 600/300 | 600/600 | 600/600 |
| the same inside `if (i >= 0) { … }` | 600/300 | **600/300** | 600/600 |

Two programs differing only in braces, one clean and one leaking its entire
payload. Flipping a verdict means writing the other arm, not just revoking the
one you have.

## What stays refused

`moved_ret` — `return P { f: q.f, n: i }`. There is no bind, so no marker flip,
and the inc goes with the move (#6726). Measures exactly as before the slice
(600/200), deliberately.

## Verification

`internal/e2eselfhost/self_host_arrenum_fieldread_share_test.go`, 8 cases. Every
`want` was confirmed against BOTH oracles — `-interp` and the native x86-64
backend agreed on each — never read off the self-host run under test.

The pairing was proven load-bearing by disabling the retain while keeping both
marker flips: every case goes to **exit 99 with allocs == frees and live_bytes
0**. This is the class where the census lies, so that is the check that counts.

`TestSelfHostStage2FixpointArm64` green (120 s) — mandatory before pushing any
exit-sweep credit change, since it is the only gate that has caught a
whole-compiler miscompile the x86-64 set missed (#7548).
