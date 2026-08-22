# The last name-keyed credits — and the end of `reclaim_slot_name`

The final block of #7253 step 1. After tuple (#7272), `"STR:"` (#7292),
`"SARR:"` / `"ARRARR:"` (#7253, #7335), the bare-name struct credit (#7349) and
the Option family (#7356), this converts everything that was left and **deletes
`reclaim_slot_name`**: nothing in `irlower.fern` resolves a reclaim credit by
name any more.

## The block was 8 tags by the inventory and 12 in the code

Three of the extras are the reason to read the writers rather than the list:

- **`"RCENUM:"`** is appended against the SAME entry as `"RCENUMS:"` —
  `collect_fresh_rcenum_names` emits one value and the crediting loop tags it
  twice. Converting only the sweep half would have left one tag keyed by site
  and its twin keyed by name, off one string. It also reads `slot_name` EXACTLY
  where its twin reads the retirement-aware form, so it needed the explicit
  `slot_name_retired` refusal #7349 introduced.
- **`"ARRSTRUCTA:"` / `"STRUCTARRA:"`** ride their parents' keys as sub-tags,
  like `"OPTARRERR:"` did — and their `"A|"` marker rows are `"A|"` + the same
  key, so the self-lookup that separates append-built from literal-built stays
  key-to-key and gets sharper for it.
- **`"DYN:"` / `"DYNCAND:"`** were not on the list at all, and were the last
  consumer of `reclaim_slot_name`. Leaving them would have kept the function
  alive with one caller, so the stated outcome — retire the mechanism — would
  not have been reached.

## Three severities from one defect

Every class had the same bug. It produced three different signals:

| class | collided | rename control | oracles |
| --- | --- | --- | --- |
| `ARRTUP:` | **99** `650/450` 8000 | 18 `650/250` 16000 | 18 |
| `ARRSTRUCT:` | **99** `650/450` 8000 | 18 `650/250` 16000 | 18 |
| `STRUCTARR:` | **99** `400/200` 9600 | 18 `400/200` 9600 | 18 |
| `STRUCTARRA:` | **99** `450/250` 7200 | 34 `450/250` 7200 | 34 |
| `ARRENUM:` | **99** `750/550` 5200 | 68 `750/350` 10400 | 68 |
| `SCENUMS:` | 37 `150/100` 2000 | 37 `150/50` 4000 | 37 |
| `DYN:` | 73 `150/0` 6000 | 73 `150/50` 4000 | 73 |

- **Fault** (five classes): the aliased source is released elsewhere, so the
  stray dec is a double free and `__rc_underflow_count()` says so.
- **Latent** (`SCENUMS:`): the class leaks its own source, so the stray dec lands
  on a box nothing else claimed. Its leak GREW from 2000 to 4000 here, and that
  is the fix — removing a release that was never owed exposes the leak it was
  masking.
- **Denial** (`DYN:`), which is new: `tagged_value_of` returns the FIRST match,
  so the aliasing binding's entry SHADOWED the credited one and suppressed a
  release that was owed. Base leaked 6000 against the control's 4000 — the
  collision made things worse, not better, in the opposite direction from the
  latent form.

`STRUCTARR:` and `STRUCTARRA:` are the rows where the census is useless twice
over: collided and control read the SAME `allocs/frees/live_bytes` and differ
only in the exit code.

## The probe that could not have shown a presence

`arrenum_collide` needed three attempts. An `enum E { A(i32), B }` version of the
identical program earns no credit at all — the class exists for an rc payload —
so it measured "collided and renamed agree" while the bug was fully present, and
its positive control leaked identically either way. Only `A(string)` reaches the
credit, and then the collision is an immediate exit 99.

Two other classes were probed the same wrong way before the table settled. The
rule that caught all three: **an absence is only publishable when the probe could
have shown a presence** — check the positive control fires before believing the
collide row.

## What moved

13 collector pushes across 6 collectors (three of them marker rows — `"A|"`,
`"S:"`, and the `"#<Enum>"` infix), 12 credit writers, 11 readers onto one shared
`slot_credit_at`, and 4 gate loops that read list entries taking
`reclaim_site_name` back out. `opt_credit_at` generalised into `slot_credit_at`
rather than copied.

**Three enum reuse emitters bind their recipient outside `bind_var_slot`** —
`emit_enum_cross_reuse`, `emit_inarm_match_reuse`, `emit_enum_donor_reuse` — the
same gap #7272 and #7349 hit on the tuple and struct siblings. Each takes the
site key as a parameter now, so the compiler enumerated the call sites instead of
a grep.

## Non-vacuity

`internal/e2eselfhost/self_host_final_credit_site_key_test.go`, 22 cases x 3
backends. Reverting `irlower.fern` fails **17** subtests: all seven collide rows
on x86-64, and only the five FAULTING ones on wasm and arm64, because those legs
assert exit codes and the latent and denial forms do not move one.

```
arrtup_collide exited 99, want 18 (99 = rc underflow: a same-named local
inherited another binding's reclaim credit)
scenum_collide: leakcheck: allocs=150 frees=100 live_bytes=2000 — want frees=50
dyn_collide:    leakcheck: allocs=150 frees=0   live_bytes=6000 — want frees=50
```

The eight `credited_*` rows pass either way and are the silent-half guard: a site
key that resolves to nothing denies the credit, which no exit code would show.
All eight still balance at `live_bytes 0`.

## Found alongside, not this path

A sibling-block rc-payload enum probe (`enum R { Full(i32[]), Empty }`, one arm
fresh and one aliasing a live local) **segfaults the compiled program on the
self-host** — identically before and after this change, and at exit 139 rather
than any rc counter. Native and interp both answer 37. Pre-existing, unrelated to
the keying, and filed separately; recorded here because it is why the `RCENUM:`
collision has no row in the table above.
