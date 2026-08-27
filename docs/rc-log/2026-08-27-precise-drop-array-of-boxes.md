# The precise drop took an array of BOXES down the scalar path

*2026-08-27* — #7610, the second of `enum_arr__param`'s two stacked causes, and
the one that flips the cell. Follows `2026-08-27-enum-array-counted-param.md`,
which closed the first and recorded that the cell would not move without this.

## The repro is two programs one token apart

```fern
var keep: E[] = mkv(7);        // 104/102, 80 live
var keep: E[] = mkv(seed());   // 104/104, clean
```

Everything else identical, native clean on both, the underflow guard 0
throughout. `mkv(1 + 6)` and `mkv(q)` are ALSO clean, and so is `mkv(7)` with a
post-loop read of `keep` — so the axis is not const-folding in general. It is
whether the local qualifies for the PRECISE DROP, and only the bare-literal,
no-later-use shape does.

That narrowness is what makes the shape dangerous rather than obscure: it reads
exactly like whichever rc bug is being worked at the time. Two suites warn about
it in their headers, and both cited #7364 — a closed, unrelated defect — so the
trap had no issue of its own until now.

## The emit says it plainly

At the same program point, the leaking shape emits

    call __fn___fern_arr_dec       ← box-only
    xorl %eax, %eax
    movq %rax, -8(%rbp)            ← slot ZEROED

where the clean one emits the `rc_is_unique`-gated element walk. The zeroing is
the whole mechanism: the box-only dec frees the buffer, and the nulled slot then
hides the elements from the exit sweep, which finds nothing to walk. No second
owner exists to reach them.

## The cause, and it is one line of predicate

The precise-drop branch reclaims a scalar-element array slot early:

```fern
if (dslot >= s.n_params && s.is_arr_slot(dslot)
        && !s.is_strarr_slot(dslot) && !s.is_arrarr_slot(dslot)) {
    s = s.release_zero(dslot, "__fern_rc_dec", "precise-drop-scalar-arr");
```

Its own comment states the rule correctly — "string[] (strarr) and nested arrays
(arrarr) stay excluded — their buffers hold pointers needing an element walk" —
and then names only those two. An `E[]` slot is `is_arr`, is neither a strarr nor
an arrarr, so it took the scalar path. `Inner[]` and `(T, U)[]` are the same
shape.

The exit sweep already knows the full set: it deep-frees arrtup, arrstruct and
arrenum slots in three dedicated loops, each commented "Excluded from the plain
is_arr sweep above". The precise drop's exclusion list should be exactly that
sweep's deep-free set, and it was missing three of the five. Fixed by asking the
sweep's own predicates — `slot_is_reclaimable_arrenum` /
`_arrstruct` / `_arrtup` — rather than by adding a fourth spelling of the
question.

## Measured

x86-64, `FERN_LEAKCHECK=1`. Native and the self-host agree on every exit.

| probe | native | self-host before | after |
|---|---|---|---|
| `mkv(7)` — the repro | 6, 104/104 | 6, **104/102** | 6, **104/104 clean** |
| `mkv(seed())` | 6, 104/104 | 6, 104/104 | 6, 104/104 |
| `mkv(1 + 6)` | 6, 104/104 | 6, 104/104 | 6, 104/104 |
| `mkv(q)`, q = 7 | 6, 104/104 | 6, 104/104 | 6, 104/104 |
| `mkv(7)` + post-loop read | 7, 104/104 | 7, 104/104 | 7, 104/104 |
| `Inner[]` twin | 6, 104/104 | 6, 104/102 | 6, 104/102 |

The struct-array twin is unmoved and that is expected: its FIRST cause is still
open — it has no counted-param tier, the `"DCNT:"` sibling being enum-only — so
the precise-drop fix alone cannot close it. It stays `struct_arr__param`'s slice.

## The matrix cell flips

`enum_arr__param` measured `clean clean` against a `leak` pin, which the gate
reports as a failure either way (TEST-GATES rule 13). Pin updated.

Matrix: **2 leaking cells of 35**, down from 3 — `str_arr__fieldread` and
`struct_arr__param`.

## Gates

- per-module emit-all fixpoint — green, 386 s, 0 skips, run in the FOREGROUND
  (a backgrounded `nohup` dies with its parent shell and leaves an empty log,
  which is not a pass — that nearly went unnoticed on the previous slice)
- all four matrices — the construction-retain one (flipped, re-pinned), the
  container-sink one, and both leak matrices — green, no other row moved
- the 21 array-of-boxes suites (ArrEnum / ArrStruct / ArrTup) — green, 116 s

The full 382-function shape-selected set for this path does not fit this box's
10-minute tool ceiling and is left to CI, which shards it. What ran locally is
the set that pins the shape actually changed.
