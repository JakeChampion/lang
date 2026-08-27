# The rebound Option[i32[]] with nothing consuming it — an empty quadrant

*2026-08-27* — `opt_arr__rebind__unused`, both leak-matrix arches. Part of
#5338, and the last of the rebind family.

## The cell

```fern
var x: Option[i32[]] = Some([i, i + 1]);
x = Some([i + 2, i + 3, i + 4]);
```

400 allocs / **0 frees**. Nothing released at all — the signature of a credit
that was never granted, not of a release half-wired.

## The note said the opposite

> rebound Option[i32[]] with no consuming match; the match cell is clean

read as "the release exists and only the no-match sweep half is missing". The
first clause is the finding and the reading is backwards. Measured:

| probe | self-host |
|---|---|
| single bind, no match | 200/200 clean |
| rebind + consuming match | 400/400 clean |
| rebind, no match | **400/0** |

The release works. The admission was absent.

## The family is a 2×2 and one cell was empty

|  | consumed by a match | not consumed |
|---|---|---|
| **reassigned** | `collect_fresh_optarr_names` | *nothing* |
| **not reassigned** | the consuming-match analyses | `collect_unmatched_optarr_names` |

`collect_fresh_optarr_names` requires `index_of_str(reassigned, name) >= 0`
**and** a sole top-level match after the declaration.
`collect_unmatched_optarr_names` requires `index_of_str(reassigned, name) < 0`.
A rebound local with nothing matching on it satisfied neither, so no collector
credited it and no sweep saw it.

The two classes stay disjoint on the `reassigned` axis alone — which is exactly
what the comment they share relies on — so filling the quadrant inside
`collect_fresh_optarr_names` cannot double-free anything.

## The no-match proof is NOT the sibling's

This is the part worth recording, because getting it wrong produced a **wrong
answer** rather than a leak.

`collect_unmatched_optarr_names` may lean on the plan's frame-escape verdict
(`plan_fe` membership plus not-a-scrutinee and not-alias-bound) because its
locals are bound once. The first version of this change asked the same question
for the rebound quadrant, through a `opt_unmatched_esc_ok` helper extracted from
both collectors. The plan grants a local the callee `return`s:

| probe | native | interp | self-host |
|---|---|---|---|
| `refused_option_escapes` | 42 | 42 | **25** |

No underflow, no segfault, no leak — just a different answer, because the frame
freed a box the caller still held and the read came back from recycled memory.

The rebound quadrant therefore asks `body_unsafe_for` **directly**. That is the
same reason the matched branch beside it carries `name_escapes_outside_stmt`.

## Measurements

Every row answers identically on native x86-64, `bin/fern -interp` and the
self-host, before and after.

| probe | native | self-host before | after |
|---|---|---|---|
| `rebind_unmatched` — the cell | 17, 300/300 | 17, **400/0** | 17, **400/400 clean** |
| `loop_rebind` | 25, 1400/1400 | 25, 2000/400 | 25, **2000/2000 clean** |
| `conditional_rebind` | 25, 900/900 | 25, 1000/400 | 25, **1000/1000 clean** |
| `rebind_matched_unchanged` | 68, 300/300 | 68, 400/400 | unchanged |
| `single_bind_unchanged` | 17, 200/200 | 17, 200/200 | unchanged |
| `refused_option_escapes` | 42, 1000/1000 | 42, 1200/400 | unchanged, refused |
| `refused_two_matches` | 51, 1000/1000 | 51, 1200/400 | unchanged, refused |
| `refused_payload_escapes` | 42, 1200/1200 | 42, 1400/800 | unchanged, refused |
| `refused_alias_bind` | 28, 1200/1200 | 28, 1200/400 | unchanged, refused |
| `refused_match_before_rebind` | 39, 1000/1000 | 39, 1200/400 | unchanged, refused |

No exit 99, no exit 100, no 139. Every churn probe reads its value back after
200 rounds have recycled the freelist. The three flipped rows were re-run under
`FERN_SANITIZE=1` with `FERN_RC_UNDERFLOW_TRAP=1` and `FERN_RC_FREE_DEBUG=1`:
clean, no trap, no quarantine hit.

## The matrix rows

Two moved, both leak→clean — `opt_arr__rebind__unused` on each arch — and
nothing else in any of the four matrices did. The rebind family is now closed:
`str__rebind`, `str_arr__rebind` and `opt_arr__rebind` all read `clean` on both
compilers.

## Gates

- per-module emit-all fixpoint — green, 0 skips
- `scripts/cliff-bench` — 458360 / 258145296, unmoved
- the repo complexity ratchet — banked at 411 / **17608**, DOWN 10. The escape
  gate duplicated in the two unmatched collectors is one named predicate now,
  and the match analysis moved out of the collector into
  `optarr_rebound_credit_ok`.
- all four matrices — the two rows re-pinned, no other row moved
- the shape-selected set (TEST-GATES rule 13, applied to the shape changed:
  files mentioning `Option[` and pinning leak accounting) — 38 files, 63 test
  functions
- the new suite, 10 cases

## What remains

Eight cells in `selfhost-leak-matrix.txt`, none of them a rebind. Four are the
tuple family, two are alias-consumed-by-match denials the notes call sound, one
is the `for`-in element binder (`#7292`/`#7356`), one the `opt_str` call-arg
floor.
