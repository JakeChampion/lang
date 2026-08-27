# The producer-call rebind — a string local that fell between two collectors

*2026-08-27* — `str__rebind__{read,unused}`, both leak-matrix arches. Part of
#5338.

## The cells

```fern
var x: string = mkstr("x");
x = mkstr("yz");
```

400 allocs / **0 frees**, 6400 live. Not a partial sweep — nothing freed at
all, neither the superseded box nor the final one.

## The recorded reason was wrong

The matrix note said:

> the `"STR:"` credit is single-bind only; a rebound string local is refused and
> never swept

That has not been true since #2649. The string-builder accumulator class
releases a rebound local: `emit_str_reclaim_store` frees the superseded box at
each reassignment and the scope-exit sweep frees the final one, both driven by
`slot_is_reclaimable_str` off the same `"STR:"` credit. And it does not require
the rebind to consume the local — `str_accum_reassign_ok`'s default arm admits
any RHS that does not leak the name, so a rebind to an unrelated fresh value
qualifies. Measured, before this change:

| probe | self-host |
|---|---|
| `var x = i.to_string() + "…"; x = i.to_string() + "…"` | 800/800 clean |
| `var x = i.to_string() + "…"; x = x + "…"` | 600/600 clean |
| `var x = mkstr("x"); x = mkstr("yz")` | **400/0** |

Three rebinds of the same class; only the one through a user function leaked.

## What it actually was

The class asks `str_local_binding_is_fresh`, which answers from the expression
alone — a concat, a string method, a known producer. `mkstr` is none of those
syntactically. That it returns a fresh box on every path is a *whole-program*
fact, and it is already proven: `str_fresh_ret_fns_of` builds the registry, and
`collect_str_fresh_ret_call_names` credits `var r = mk(..)` off it.

But that collector takes `reassigned` and skips every name in it — by
construction, since it grants the single-bind credit. So a reassigned local
initialised from a registry-proven producer fell between the two: the
accumulator collector could not see `mkstr` was fresh, and the registry
collector would not look at a reassigned name.

Neither credited it. Nothing swept it.

## The fix

The registry, threaded into the class that already had the machinery.
`str_accum_value_is_fresh` is the expression test OR the registry lookup, and
both ends ask it — the declaration in `collect_str_accumulator_names` and the
rebind in `stmt_accum_unsafe_for`. `str_fresh_ret` reaches both through
`reclaimable_names_of`, which already holds it for the sibling collector one
line away.

No gate was loosened. That is what makes this narrow: the class's escape
analysis, its alias rule and its non-fresh-rebind refusal are untouched, so
everything they refused they still refuse.

## Measurements

Every row answers identically on native x86-64, `bin/fern -interp` and the
self-host, before and after.

| probe | native | self-host before | after |
|---|---|---|---|
| `producer_rebind_read` — the cell | 68, SSO (0/0) | 68, **400/0** | 68, **400/400 clean** |
| `producer_rebind_heap` | 46, 200/200 | 46, **400/0** | 46, **400/400 clean** |
| `conditional_rebind` | 6, 700/700 | 6, 1400/800 | 6, **1400/1400 clean** |
| `loop_rebind` | 72, 1200/1200 | 72, 2400/800 | 72, **2400/2400 clean** |
| `self_consuming_rebind` | 31, **800/400** | 31, 1600/800 | 31, **1600/1600 clean** |
| `moved_out_return` | 23, 1000/1000 | 23, 2000/1600 | 23, **2000/2000 clean** |
| `single_bind_unchanged` | 51, SSO (0/0) | 51, 200/200 | unchanged |
| `refused_alias_before_rebind` | 16, 1000/1000 | 16, 2000/1200 | unchanged, refused |
| `refused_nonfresh_rebind` | 72, 800/800 | 72, 1600/800 | unchanged, refused |
| `refused_container_store` | 23, 1000/1000 | 23, 1800/1000 | unchanged, refused |

No exit 99 (underflow), no exit 100 (a value read back wrong), no 139. The
three refused rows and the four multi-round flipped ones each read their value
back after 200 rounds of churn have recycled the freelist. Every flipped row
was re-run under `FERN_SANITIZE=1` with `FERN_RC_UNDERFLOW_TRAP=1` and
`FERN_RC_FREE_DEBUG=1`: clean, no trap, no quarantine hit.

`self_consuming_rebind` is worth its row: **native leaks it** (800 allocs, 400
frees) and the self-host now does not. Native's answer is the same; its
accounting is not.

## The matrix rows

Four moved, all leak→clean, and nothing else in any of the four matrices did:

- `str__rebind__{read,unused}` on x86-64 — `clean leak` → `clean clean`
- the same pair on arm64 — `leak leak` → `leak clean`, the self-host now ahead
  of native-arm64, which still leaks the shape under #7446

`str_arr__rebind__{read,unused}` did **not** move. The `"SARR:"` family has its
own rebind machinery — the self-`append` and self-`.with` rebinds — so its
refusal is not the single-bind one either; what it lacked was the REBUILD form,
plus the assign-path branch to release the old value deeply.
`2026-08-27-strarr-rebuild-rebind.md` closes it.

## Gates

- per-module emit-all fixpoint — green, 0 skips, foreground
- `scripts/cliff-bench` — 458360 / 258145264, the campaign constant, unmoved
- the repo complexity ratchet — 411 / 17609 unmoved. The freshness disjunction
  is a named predicate rather than two inline `||`s, which is where the first
  spelling put two points.
- all four matrices — the four rows above re-pinned, no other row moved
- the shape-selected set (TEST-GATES rule 13, applied to the shape changed:
  files declaring a scalar `string` local and pinning leak accounting) — 101
  files, 196 test functions
- the new suite, 10 cases
