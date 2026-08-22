# 2026-08-22 — the string[] credit, keyed on the binding (#7253 for "SARR:")

Third class through #7253's step 1, after the tuple family (#7272) and `"STR:"`
(#7292). This one was an OVER-RELEASE, where those two were leaks.

## The shape

```fern
if (i % 2 == 0) { var v: string[] = mk();  t = t + v.len(); }   // earns the credit
if (i % 2 == 1) { var v: string[] = base;  t = t + v.len(); }   // a bare alias
```

`slot_is_reclaimable_strarr` resolved `"SARR:"` / `"SARRB:"` through
`reclaim_slot_name`, so both `v` are one key. The fresh arm earns the credit, the
aliasing arm inherits it, and `__fern_str_arr_free` takes a buffer the caller
still owns.

|  | interp | native | self-host |
|---|---|---|---|
| alias from a PARAM | 34 | 34 `51/51/0` | **99** |
| alias from a struct FIELD | 34 | 34 | **99** |
| `i32[]` control | 51 | 51 | 51 |

`allocs=255 frees=255 live_bytes=0` while it underflowed. **The byte count is
useless for this bug** — a doubly-released block returns to the freelist, so only
`__rc_underflow()` dissents. Every probe in this area needs it.

## The witness is a rename, not a byte count

`param_rename` is the same program with the second local called `u`. It never
collided, so it was already correct at base, and it carries the SAME residual
leak (64 bytes, #7259) as the colliding version:

| probe | base | fixed |
|---|---|---|
| `param_alias` | **99** `255/255/0` | 34 `255/251/64` |
| `param_rename` | 34 `255/251/64` | 34 `255/251/64` |

So the assertion is pairwise: after the fix the colliding program measures
identically to the program that never collided. That is checkable without
deciding whether 64 is "correct", which it is not — it belongs to #7259 and moves
independently.

## Contract-only, not witnessed

`"SARRB:"` (the `.split()` / `.lines()` producers) moved with `"SARR:"` because
both feed one predicate and leaving half name-keyed would make it resolve two
credits two ways. **No probe distinguishes that half**: it needs `std/string`, and
the single-program driver does not load imports. Measured through the CLI instead
— base and fixed identical at `455/451/64`, all oracles agreeing. Carried on the
consistency argument, not on a measurement.

## Next lead

Probing three further name-keyed classes (`ARRARR:`, `STRARR:` via struct arrays,
struct-array) found no collision in this shape. That reading is a trap: see the
`#7335` entry — the collisions do not exist until something WIDENS the credit, and
the widening then hands it to same-named siblings. Site-keying is a prerequisite
for widening, not an independent cleanup.
