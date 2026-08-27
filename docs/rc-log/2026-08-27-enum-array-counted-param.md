# The enum-ARRAY counted-param tier, and the cell it does not flip

*2026-08-27* — `enum_arr__param`, the fourth of the five construction-retain
`__param` cells. Follows `2026-08-27-enum-counted-param.md`, which closed
`enum__param` and left the two array-of-boxes cells as their own slice.

## The predecessor's question, answered

That record measured the two array-of-boxes cells and found they do NOT withhold
the release the way the closed cells did — they release in all three positions,
with the SHALLOW `__fern_arr_dec` where the deep element walk is owed. It framed
the open question as "why the array-of-boxes deep credit is refused for a slot
the callee STORES".

The answer was already written down, in `borrow_reg_with_counted`'s own comment:

> the ESCAPE walker threads the borrow registry and nothing else, so a NAMED
> local passed at a counted position read as an escape and lost its scope-exit
> release entirely … This closes the `string` and `string[]` cells; the enum and
> array-of-boxes ones are not counted by either tier, **which admits BY TYPE**.

So the machinery was complete and the type was simply never admitted. What the
slice needed was a tier, not a mechanism.

## Why a fourth key rather than widening "ACNT:"

`ACNT:`'s credit routes a plain `arr_dec`. An array of BOXES owes an element
walk, and the argument-temp stash at `lower_call`'s array-literal arm consumes
`arr_param_counted_at` to emit exactly that plain dec. Folding enum-arrays into
`ACNT:` would hand that site a credit whose release is the wrong depth.

`DCNT:` is the deep class, five bytes like its three siblings because
`borrow_reg_with_counted` slices the key at a fixed width. All four still merge
into one `CNT:` flag string, which is correct: at a call argument the question is
whether THIS position is counted, and what the caller then RELEASES is decided by
its own local's type, not by which tier proved the store.

## The two guards, and which one was already there

- **The element walk must EXIST.** `enum_arr_elems_walk_ok` is the same
  predicate the backends ask before emitting `__enum_arr_elems_drop_<E>` at the
  `arr_dec` site. Crediting a walk nothing emits would leave the leak in place.
- **No element may be handed out.** This is the hazard the `ELB:` tier was built
  for, and it needed no new code: `arrparam_use_ok` credits `p[i]` only for
  `SCNT:`, so any element read disqualifies the param outright. The vocabulary
  answers it, not a second flag.

The `ELB:` flag itself is the WRONG question at this position and could not have
been widened to cover it. It asks whether the callee touches an element; a callee
that stores the whole array touches none yet keeps a reference, so it refuses.
The reference it keeps is a counted one.

## Measured

x86-64, `FERN_LEAKCHECK=1`, `mkv(seed())` so the local stays live. Every want
confirmed against BOTH oracles — native x86-64 and `bin/fern -interp` agreed on
every exit — never read off the self-host run.

| probe | native | interp | self-host before | after |
|---|---|---|---|---|
| `counted_store` — the cell | 6, 104/104 | 6 | 6, **104/102** | 6, **104/104 clean** |
| `callee_returns_param` | 3, 4/4 | 3 | 3, 4/2 | 3, 4/2 refused |
| `callee_extracts_element` | 9, 4/4 | 9 | 9, 4/2 | 9, 4/2 refused |
| `callee_stores_element` | 9, 104/104 | 9 | 9, 104/102 | 9, 104/102 refused |

Only the target cell moved; the three refusals are byte-identical before and
after, so none of them passes for a reason unrelated to this tier. The leaked
pair is the one element box and its `i32[]` payload — the buffer was always
freed, which is what distinguishes this from the borrowed-argument slice.

`callee_stores_element` is the one that makes the element guard load-bearing
rather than decorative: the callee stores an ELEMENT, and that store IS counted
for the element, so a tier asking only "is this a counted store?" would admit it
and the caller's walk would then free a box the holder still references.

## The matrix cell does NOT flip, and that is the finding

`enum_arr__param` stays `leak`. It has **two causes stacked**, and this slice
closes one. The cell builds its local from `mkv(7)` — a CONSTANT producer
argument — which makes the local dead and moves its release to a box-only site:
the const-fold trap the borrowed-arg suite documents in its own header, having
cost real time there. That suite cites #7364 for it, but #7364 is closed and is
a different defect (a string-payload enum local never swept, fixed by #7371), so
the trap was untracked until now: it is #7610, filed with both probes. With this
fix the identical program over `mkv(seed())` is 104/104 while the constant shape
is still 104/102.

The cell flips when #7610 lands. Recorded in the matrix note so the next reader
does not re-derive it — and it is why this change
moves no pinned row in either leak matrix. The gate for it is the new suite.

## Gates

- per-module emit-all fixpoint — green, 372 s, 0 skips
- the shape-selected set (TEST-GATES rule 13, applied to enum-ARRAY: files
  declaring an `enum`, an array type, and pinning leak accounting) — 47 files,
  103 test functions, green, 357 s
- `TestSelfHostLeakMatrixX86_64` + `TestSelfHostLeakMatrixIRArm64` — green, no
  row moved
- the construction-retain matrix, the container-sink matrix, the counted-param
  release suite, and both borrowed-arg siblings (arrenum + arrstruct) — green
- the new suite, 4 cases

Matrix: **3 leaking cells of 35**, unchanged — `str_arr__fieldread`,
`enum_arr__param` (one of two causes closed), `struct_arr__param`.

The struct-array twin is untouched: its own escape walker, its own tier, its own
slice — the way the arrstruct and arrenum halves of every earlier slice went.
