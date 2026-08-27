# The string[] REBUILD rebind — an admission and a missing store branch

*2026-08-27* — `str_arr__rebind__{read,unused}`, both leak-matrix arches. Part
of #5338. The lead `2026-08-27-str-rebind-producer-call.md` left.

## The cells

```fern
var x: string[] = [mkstr("x")];
x = [mkstr("y"), mkstr("z")];
```

800 allocs / 200 frees against native's 200/200.

## Not the single-bind refusal either

The matrix filed these as "the `string[]` sibling of the `STR:` single-bind
refusal". Both halves of that are wrong. The `"SARR:"` credit is explicitly
**not** excluded on `reassigned` — its own comment says so — and it sanctions
two rebind forms already, the self-`append` and the self-`.with`. Measured
before this change:

| probe | self-host | native |
|---|---|---|
| `var x = [mk("x")]` (single bind) | 300/300 clean | 100/100 |
| `x = x.append(mk("y"))` | 600/600 clean | 200/200 |
| `x = [mk("y"), mk("z")]` | **800/200** | 200/200 |
| `x = mkarr(i)` | **1200/400** | 400/400 |

What was missing is the **REBUILD** form: a rebind to a value the local can
solely own, sharing nothing with what the slot holds. That is the same
freshness proof `collect_fresh_strarr_in_stmt` applies to the declaration — an
array literal whose every element is element-fresh (`[]` included), or a call to
a visible whole-program `"STRARR:"` producer — so `strarr_rebind_is_fresh` asks
it at the rebind instead. `name` must not appear in the value, or an element
read would be a lasting alias into the buffer the store is about to free.

The producer half needs the registry, which the walker does not carry; the
shadow filter needs the body, which it does not carry either. Both are resolved
once at the entry point into a visible-producer list, the way the string[]-field
admission filters its own producers, and that list is the one new parameter.

## Admitting it was half a fix, and half is not a fix

With the credit granted the cell went 800/200 → **800/600** and stayed leaking.
The emit says why. Two stores land in the slot:

| store | helper emitted |
|---|---|
| the declaration (`var x = …`) | `__fern_str_arr_free` — deep |
| the rebind (`x = …`) | **`__fern_arr_dec`** — shallow |

`lower_stmt_assign` had no branch for the class at all, so a rebound reclaimable
`string[]` fell through to `emit_arr_store`'s shallow dec: the buffer is freed
and its element pointers are dropped on the floor. The `var` re-declaration has
driven `emit_strarr_reclaim_store` since #4355; the assign path is the sibling
the rc-tuple and rc-enum rebinds each had to open for themselves, one element
kind over — and the comment above the rc-tuple branch describes this exact
failure in its own words.

Adding the branch takes the cell to 800/800. Neither half moves the row alone.

One detail cost a rebuild: the branch was first written
`if (!target_is_arr && slot_is_reclaimable_strarr(...))`, copied from the struct
branch below it. `target_is_arr` is true for every slot this class covers, so
the branch never fired and the numbers did not move at all.

## Measurements

Every row answers identically on native x86-64, `bin/fern -interp` and the
self-host, before and after.

| probe | native | self-host before | after |
|---|---|---|---|
| `literal_rebuild` — the cell | 51, 200/200 | 51, **800/200** | 51, **800/800 clean** |
| `producer_rebuild` | 51, 400/400 | 51, **1200/400** | 51, **1200/1200 clean** |
| `rebuild_to_empty` | 17, 200/200 | 17, 400/200 | 17, **400/400 clean** |
| `loop_rebuild` | 72, 3800/2800 | 72, 6000/3200 | 72, **6000/6000 clean** |
| `conditional_rebuild` | 72, 2300/2200 | 72, 3500/2700 | 72, **3500/3500 clean** |
| `alias_before_rebind` | 67, 2800/2800 | 67, 4400/3200 | 67, **4400/4400 clean** |
| `self_append_unchanged` | 51, 200/200 | 51, 600/600 | unchanged |
| `refused_array_escapes` | 72, 2800/2600 | 72, 4400/3200 | unchanged, refused |
| `refused_element_bound` | 57, 2800/2600 | 57, 4400/3200 | unchanged, refused |
| `refused_nonfresh_rebind` | 82, 2400/2200 | 82, 3600/2800 | unchanged, refused |
| `refused_self_element` | 72, 2400/2000 | 72, 3600/2800 | unchanged, refused |
| `refused_container_store` | 33, 2800/2600 | 33, 4200/2800 | unchanged, refused |

No exit 99 (underflow), no exit 100 (a value read back wrong), no 139. Every
churn probe reads its value back after 200 rounds have recycled the freelist.
Every flipped row was re-run under `FERN_SANITIZE=1` with
`FERN_RC_UNDERFLOW_TRAP=1` and `FERN_RC_FREE_DEBUG=1`: clean, no trap, no
quarantine hit.

`alias_before_rebind` is the row that proves the ARBITRATION rather than a
refusal. An alias bound before the rebind is not refused: the alias site earns
the same `"SARR:"` credit, so the store's free finds rc 2 and only decs, and
`__fern_str_arr_free` walks elements at whichever owner reaches rc 1. It reads
every element back after churn and goes clean.

## The matrix rows

Four moved, all leak→clean, and nothing else in any of the four matrices did:

- `str_arr__rebind__{read,unused}` on x86-64 — `clean leak` → `clean clean`
- the same pair on arm64 — `leak leak` → `leak clean`, the self-host now ahead
  of native-arm64, which still leaks the shape under #7446

## Gates

- per-module emit-all fixpoint — green, 0 skips
- `scripts/cliff-bench` — count 458360 unmoved; bytes 258145264 → 258145296,
  +32 on 258 M (+0.00001%), which is the compiler's own sources changing under
  a workload that scales with them
- the repo complexity ratchet — banked at 411 / **17618**. Two of those points
  are this change (one dispatch branch in `lower_stmt_assign`, one in
  `stmt_strarr_unsafe_for`, both the shape of the twelve sibling lines around
  them); the other seven were already on main, measured at 17616 with this diff
  stashed, and had been drifting under the recorded 17609.
- all four matrices — the four rows above re-pinned, no other row moved
- the shape-selected set (TEST-GATES rule 13, applied to the shape changed:
  files mentioning `string[]` and pinning leak accounting) — 61 files, 113 test
  functions
- the new suite, 12 cases

## What remains

The `opt_arr__rebind__unused` cell is the last of the rebind family, and its
note already says the release exists and only the no-match sweep half is
missing — the same shape as the store branch above, one payload kind over.
