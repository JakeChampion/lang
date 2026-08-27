# The enum counted-param tier, and the guard it needs that strings do not

*2026-08-27* — `enum__param`, the third of the five construction-retain
`__param` cells. Follows `2026-08-27-counted-param-position.md`, which closed
`str__param` and `str_arr__param`.

## The remaining three cells were TWO causes, not one

The predecessor record said the remaining cells shared the callee-STORE cause.
Measurement at `91f4a9cb7` says only ONE of them does. Reading every call `main`
emits, rather than inferring:

| probe | calls in `main` |
|---|---|
| `pm_str` (closed) | mkv, **__fern_str_free**, round, underflow, __fern_str_free x2 |
| `pm_strarr` (closed) | mkv, **__fern_str_arr_free**, round, underflow, __fern_str_arr_free x2 |
| `pm_enum` | mkv, round, underflow — **nothing else** |
| `pm_enumarr` | mkv, **__fern_arr_dec**, round, underflow, __fern_arr_dec x2 |
| `pm_structarr` | mkv, **__fern_arr_dec**, round, underflow, __fern_arr_dec x2 |

`enum__param` withholds the release entirely — the closed cells' cause exactly.

The two array-of-boxes cells do NOT withhold. They release in all three
positions, the loop rebind and both exits, precisely where the fixed `string[]`
case releases. The difference is the helper: `__fern_arr_dec` is the shallow
buffer dec, `__fern_str_arr_free` the rc-gated ELEMENT WALK. So the caller holds
its credit but not the DEEP class, and the question there is why the
array-of-boxes deep credit is refused for a slot the callee STORES — not why the
argument reads as an escape. Its own slice.

## The tier

`param_counted_of` refused the enum position purely BY TYPE: `SCNT:` admits
`string`, `ACNT:` scalar-element arrays and `string[]`. An enum is neither, and
nothing else about it was being asked.

`"ECNT:"` is the third tier, the same shape as the string one — the construction
retains the payload box, the holder's field drop decs it, so the caller keeps the
claim its own creation owes. Gated on the holder ROUTING field reclaim for the
same reason the string slot is: a literal whose type carries no arm of the right
type never gives the retain back.

`enum_result_cannot_alias` is the result guard. It refuses ANY enum result, not
merely the param's own type: the tier is name-keyed and type-blind, so it cannot
tell `E` from another enum, and the conservative reading costs a leak rather than
an over-release.

## The guard strings do not need

A string has no interior. An enum does, and a callee that DESTRUCTURES the param
could hand the payload out uncounted — after which the caller's release would
dangle it. That hazard has no analogue in the tier this was modelled on.

It was already closed, by a rule written for other reasons:
`arrparam_use_ok_stmt` walks a match SCRUTINEE at `counted=false`, so a bare
`match (p)` on the param disqualifies it outright. The callee cannot destructure
the enum at all. Verified rather than read — `callee_hands_out_payload` returns
a payload from inside the match and measures 202/201, matching native's leak
exactly, with the credit refused.

## Measured

All four probes agree across BOTH oracles and the self-host run:

| probe | verdict |
|---|---|
| `pm_enum` — the cell | **102/102 clean**, was 102/100. Native 102/102. |
| `e1` callee returns the enum | refused, 2/0 both compilers |
| `e2` callee hands the payload out | refused, 202/201, native identical |
| `e3` holder ESCAPES, payload read back after churn | **6900/6900 clean where NATIVE leaks 900 objects** |

`e3` is the one that can fail on behaviour: the holder is returned, so the retain
is live past the frame that built the enum, and every payload is read back after
20 churn frames have recycled the freelist. It returns 100 on a wrong walk and 99
on a double free, and does neither.

## Gates

- `TestSelfHostStage2FixpointArm64` — green, 92 s.
- The shape-selected set, WIDENED to fixtures declaring an `enum` as well as a
  string-field struct (TEST-GATES rule 13, applied to the shape actually being
  changed rather than the previous slice's) — 121 files, 250 tests, green, 403 s.
- The counted-param suite, now 12 cases, and both construction matrices — green.

Matrix: **3 leaking cells of 35** — `str_arr__fieldread`, `enum_arr__param`,
`struct_arr__param`.
