# The string[] field-read share — the construction-retain matrix reaches zero

*2026-08-27* — `str_arr__fieldread`, the last leaking cell of 35. Part of #5338.
Follows the five `__param` cells (`2026-08-27-enum-array-counted-param.md`,
`2026-08-27-precise-drop-array-of-boxes.md`,
`2026-08-27-struct-array-counted-param.md`).

## The cell

```fern
var q: P = P { f: mkv(i), n: i };
var p: P = P { f: q.f, n: i };      // f: string[]
```

800 allocs / 300 frees, 16 800 live, against native's 600/600. Every other field
kind was already counted at this position — `str__fieldread` through
`str_field_share_read`, `enum_arr__fieldread` through
`enum_arr_field_share_read`, `arr_i32__fieldread` and `struct__fieldread`
through their own retains. `string[]` was the one left out.

## It was NOT the share position

The obvious place to look is the struct-literal share predicate, and
`str_field_share_read`'s comment points there by saying the hoisted spelling is
already clean. Measured, that is a `string` fact: for `string[]` the hoisted and
inline spellings leak identically. Corrected in `bb8919a`, because it sends a
reader to the wrong layer.

The emit says where the block is. Against the `E[]` sibling of the identical
program:

| probe | `round` emits |
|---|---|
| `E[]` (clean) | … **`__struct_drop_P`×2**, **`__field_reclaim_P`**, `rc_is_unique` |
| `string[]` (leaking) | … **neither, at all** |

Neither holder is ever dropped. `strarrfld_scan`'s field-access arm marks
`<T>.<field>` for any read, so reading `q.f` refuses P's string[]-field reclaim
before any share position is reached.

## Why admitting it is sound

The same reason the bare-ident store beside it is, and the file already says so
about that one:

> Refusing it was the third leg of the circularity: unadmitted meant no retain,
> and no retain meant it could not be admitted.

That circularity was already broken here. The construction retains an array
field **unconditionally** — `is_array_type_name` covers `string[]` in the
`ExprStructLit` arm — and `str_arr__local` measures that retain working today.
So the new holder co-owns a COUNTED reference and its drop's dec balances against
it. The only thing missing was the admission.

Two ends are checked: the source field and the target field must both be
`string[]`, and the holder must be a bare ident, so the share has one
identifiable co-owner. `foo().f` is refused — no second holder to keep alive, so
the read is an ordinary escape.

The admitted value is also **not walked as a read**. Marking it would refuse the
SOURCE holder's reclaim, and a share needs both ends alive to balance.

## The over-release question, asked properly

This cell's failure mode is not the `__param` cells' leak. `str_field_share_read`
states it: "one box under two rc-aware k_str decs frees on the first and dangles
on the second." So the load-bearing case is `escaping_holder` — the source dies
inside a callee while the target is returned, and every element is read back
after 200 rounds of churn have recycled the freelist.

| probe | native | interp | self-host before | after |
|---|---|---|---|---|
| `inline_field_share` — the cell | 72, 600/600 | 72 | 72, **800/300** | 72, **800/800 clean** |
| `local_store_unchanged` | 71, 500/500 | 71 | 71, 700/700 | unchanged |
| `hoisted_bind_still_leaks` | 72, 600/600 | 72 | 72, 800/300 | 800/300, still refused |
| `escaping_holder` | **8**, 3000/3000 | **8** | **8**, 4400/3200 | **8**, 4400/3200, still refused |

`escaping_holder` answers **8** on all three engines before and after: no
dangling, no wrong answer, no segfault. It stays pinned at its leaking count —
an escaping holder is a different admission question, and the conservative
direction is kept. The target was also re-run under `FERN_SANITIZE=1` with
`FERN_RC_UNDERFLOW_TRAP=1` and `FERN_RC_FREE_DEBUG=1`: clean, no trap, no
quarantine hit.

`hoisted_bind_still_leaks` is the row proving the admission did not widen into
the local-BIND read, which is a separate question and still open.

## The matrix reaches zero

`str_arr__fieldread` measured `clean/clean` against a `leak` pin. Flipped.

**All 35 cells are now clean on both compilers.** The grid was the killer-drops
wave's burn-down list and the list is empty; its header now says so and reframes
it as a REGRESSION gate — a divergence appearing there again belongs to whatever
change introduced it.

## Gates

- per-module emit-all fixpoint — green, 257 s, 0 skips, foreground
- all four matrices — construction-retain (flipped, re-pinned + header), the
  container-sink one, both leak matrices — green, no other row moved
- the shape-selected set (TEST-GATES rule 13, applied to the shape changed:
  files declaring a `string[]` struct field and pinning leak accounting) — 21
  files, 38 test functions, green, 149 s
- every StrArr / ArrEnum / ArrStruct / field-share suite and the precise-drop
  suite — green
- the new suite, 4 cases

What remains in this area is the local-BIND read (`var tt = q.f; P { f: tt }`),
pinned above and still #5338's.
