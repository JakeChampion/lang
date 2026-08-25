# The moved-payload dec-skip applies to a hand-over, not to every escape — killer-drops slice 14

An arm that binds an rc payload and stores it out reclaimed the box but not the
payload: **300 allocs / 200 frees** over 100 rounds against native's 300/300. A
borrow-only call argument was worse, **200/100** against 200/200.

## The rule that was too wide

`match_moved_rc_payloads` puts a (variant, field) into the MOVED set whenever an
arm binds that payload to a name that ESCAPES the arm, and
`emit_enum_variant_payload_drops` then SKIPS the field's dec — on the stated
theory that "the binding inherits the box's counted reference".

`binding_escapes_arm` is `body_unsafe_for` over an EMPTY borrowable registry, so
a return, a store to an outer local and a call argument all produce the same
verdict. Only the first hands the reference over:

- **a store RETAINS** — one `__fern_rc_inc`, verified in the emitted asm — so the
  payload has two counted owners and the drop's dec would land on that second
  claim rather than on zero. Skipping it strands one.
- **a borrow-only callee takes no claim at all**, so the box is the payload's sole
  owner and the skip suppresses its only dec.

## Why the fix is a narrowing and not the deletion it looked like

Emptying the moved set outright takes all four escape kinds to N/N on the counts.
Every row reads as a clean win. Three of them are broken:

| shape | empty moved set | native |
|---|---|---|
| array payload, store to outer | 300/300 | 300/300 |
| array payload, borrow-only call arg | 200/200 | 200/200 |
| `return xs`, conditional and unconditional | exit **99** | exit 40 / 5 |
| STRING payload, store to outer | value **33** | value 24 |
| nested STRUCT payload, store to outer | exit **139** | exit 9 |

Two facts the counts cannot express, and one wrong explanation worth recording
because it was the first one reached.

The RETURN escape genuinely hands the reference over — the self-host IR path has
no return-transfer inc, so the value leaves owning the payload and a dec here
over-releases it.

The other is the payload TYPE, and the reason is NOT that the drop helpers are
unguarded. `__fern_str_free` reads rc at box-8 and only frees at rc==1; its own
comment says it is safe to call on a shared box. What differs is the escaping
STORE. Counted on the emitted asm: an `i32[]` payload's `keep = xs` emits one
`__fern_rc_inc`; a `string` payload's and a nested struct's emit none. Those
aliases are UNCOUNTED, so the box stays sole owner and a dec at the match frees a
value the alias still reads.

So the skip is retained for a return escape and for any payload whose escaping
store takes no counted claim. It is given up only where the store retains —
today exactly the scalar-element array payloads — `moved_skip_applies`.

Worth stating plainly, because it nearly went the other way: the deletion was the
conclusion the leak counts supported, and it was recorded as the plan before the
exit codes and a value probe contradicted it. `N/N` on every row is what a
correct fix looks like AND what removing a load-bearing dec-skip looks like.

## Results

| shape | before | after | native |
|---|---|---|---|
| array payload stored out | 300/200 | **300/300** | 300/300 |
| same, read back after three fresh arrays | value 9 | value **9** | 9 |
| array payload borrowed by a callee | 200/100 | **200/200** | 200/200 |
| same, read back after churn | value 9 | value **9** | 9 |
| guarded arm, payload stored out | 350/150 | **350/350** | 350/350 |
| same, read back after churn | value 96 | value **96** | 96 |
| 16-element payload stored out | 60/40 | **60/60** | 60/60 |
| return escape (guard) | 200/200 | 200/200 | 200/200 |
| conditional return (guard) | 250/200 | 250/200 | 250/250 |
| string payload (guard) | 140/100 | 140/100 | 20/20 |
| struct payload (guard) | 140/60 | 140/60 | 140/140 |

The guarded-arm row moved without being aimed at.
`consumed_rcpayload_enum_frees` refuses any candidate mixing a guard with a
NON-EMPTY moved set (`guarded_move`); the set is empty for that shape now, so the
candidate is admitted and the free fires. #7509 pinned it at 350/150 as a gap.

Every reclaiming row has a VALUE probe with allocation churn after the match,
against native's answer, because counts and `__rc_underflow_count()` are both
blind to a use-after-READ (`2026-08-25-field-reclaim-shared-box.md` records
shipping one that passed both AND `FERN_SANITIZE=1`). All three backends agree.
The sanitizer reports no use-after-free or double-free on any of the twelve
shapes — only leak lines on the three that keep the skip.

All 83 rc probes in the scratchpad set were run through the before and after
compilers: six rows moved, all of them above, and every exit code is unchanged.
The self-host still compiles itself under `FERN_STRICT_IR=1`.

## Two gaps left, pinned as gaps

- **A CONDITIONAL return** — 250/200 against native's 250/250. Half the rounds
  fall through, so the drop runs on a path where nothing took the payload and the
  retained skip leaks there. Closing it needs a per-PATH verdict rather than a
  per-binding one; dropping the skip instead over-releases the return path.
- **String and nested-struct payloads** stay on their sound leak because their
  escaping store emits no retain. The drop helpers are NOT the place to chase that
  — they already read the refcount, and this note's first draft said otherwise.

  Nor is it the arm-binding position. What the store is matters, and the split is
  REASSIGN vs BIND, measured over 20 rounds with no match anywhere near it:

  | shape | self | native |
  |---|---|---|
  | `var keep: P = p;` (bind, struct) | 40/40 | 40/40 |
  | `var keep: string = s;` (bind, string) | 40/40 | 0/0 |
  | `keep = p;` onto an existing local (struct) | **80/0** | 80/80 |
  | `keep = s;` onto an existing local (string) | **40/0** | 0/0 |

  The alias BIND is counted for both types. Reassigning an rc-typed local
  reclaims NOTHING — neither the new value nor the orphaned old one — and that is
  a distinct shape from anything the construction-retain matrix covers: its
  `local` rows are compound binds (`var q = P { f: mkv(..) }; var p = q;`), which
  is why `struct__local` reads clean while the reassign form here leaks outright.
  (The matrix header warns to measure the halves of a `local` row separately
  before attributing it, and that warning is what caught this.)

  Native reports 0/0 on the two string rows because it const-folds the concat
  away entirely — those rows show only that it leaks nothing, not a retain
  comparison. Probes: `gs1.fern` / `gs2.fern` (reassign), `gs3.fern` / `gs4.fern`
  (bind).

Pinned across x86 / arm64 / wasm by
`internal/e2eselfhost/self_host_moved_payload_skip_test.go`, whose four
`*_keeps_the_skip` rows are the guards: each one, with the skip removed, is an
over-release, a use-after-free, or a segfault.
