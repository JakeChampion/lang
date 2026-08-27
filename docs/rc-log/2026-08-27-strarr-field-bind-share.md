# The string[] field-read BIND — the hoisted spelling of the inline share

*2026-08-27* — `var tt: string[] = q.f; P { f: tt }`. Part of #5338, and the
row `2026-08-27-strarr-field-share-read.md` left pinned as
`hoisted_bind_still_leaks` when it closed the inline cell earlier the same day.

## The cell

```fern
var q: P = P { f: mkv(i), n: i };
var tt: string[] = q.f;             // f: string[]
var p: P = P { f: tt, n: i };
```

800 allocs / 300 frees, 16 800 live, against native's 600/600 — the identical
numbers the inline spelling measured, because it is the identical block. The
emit says so:

| probe | `round` emits |
|---|---|
| inline (already clean) | `__struct_drop_P`×2, `__field_reclaim_P`, `rc_is_unique` |
| hoisted (leaking) | **neither, at all** |

`strarrfld_scan` marks `<T>.<field>` for any read, and a `var` initialiser is a
read, so `q.f` refused P's string[]-field reclaim before either holder was
dropped.

## What the bind needs that the inline position does not

In `P { f: q.f }` the read is consumed where it is made, so admitting it proves
itself: the construction retains an array field unconditionally, and the new
holder co-owns a counted reference. Through a local that no longer holds. `tt`
is an ordinary `string[]` local; the field scan sees none of its later uses,
and an unchecked admission would let `return tt[0]` outlive the holders' deep
free — the over-release this area's failure mode actually is, not a leak.

So the bind is admitted only when `tt` reaches **nothing but** that store.

`strarr_unsafe_for_alias` is that proof, and it was already written: it is the
classifier the local `"SARR:"` credit applies to the identical question, and
its `sfld_ok` carve-out exists for exactly the struct-literal share this needs
forgiven — the comment there already describes the shape. So the admission is
two lookups and a call, not a new walker:

- collect the site keys of `var p: T = T { …, <string[] field>: tt, … }`
  binds directly in the statement list (`strarrfld_share_bind_stores`);
- refuse unless there is at least one, and `strarr_unsafe_for_alias` is clean
  for `tt` over that list with those keys forgiven.

`borrowable` is passed empty rather than the real registry, which does not
exist at admission time. It can only refuse a call-argument borrow, never grant
one, so the direction is safe — and `refused_call_argument` pins that it costs
a real shape.

A store nested inside an `if` / `while` / `match` body is not collected, so the
walk flags it and the bind is refused. Conservative, and the reason the
collector only needs the flat list.

## The over-release question

Five shapes escape or mutate, then read every value back after 200 rounds of
churn have recycled the freelist. All five stay **refused** and pinned at their
leaking counts, and all five answer identically on native x86-64,
`bin/fern -interp` and the self-host:

| probe | native | interp | self-host before | after |
|---|---|---|---|---|
| `bind_then_store` — the cell | 72, 600/600 | 72 | 72, **800/300** | 72, **800/800 clean** |
| `escaping_holder_now_clean` | 8, 2800/2800 | 8 | 8, 4400/3400 | 8, **4400/4400 clean** |
| `fresh_local_bind_unchanged` | 71, 500/500 | 71 | 71, 700/700 | unchanged |
| `refused_element_escapes` | 8, 2800/2800 | 8 | 8, 4400/3400 | unchanged, refused |
| `refused_array_escapes` | 8, 2800/2800 | 8 | 8, 4400/3400 | unchanged, refused |
| `refused_element_bound` | 40, 2800/2800 | 40 | 40, 4400/3400 | unchanged, refused |
| `refused_appended` | 68, 800/800 | 68 | 68, 1100/1000 | unchanged, refused |
| `refused_iterated` | 43, 600/600 | 43 | 43, 800/300 | unchanged, refused |
| `refused_call_argument` | 72, 600/600 | 72 | 72, 800/300 | unchanged, refused |

No exit 99 (underflow), no exit 100 (a value read back wrong), no 139. Both
flipped rows were re-run under `FERN_SANITIZE=1` with
`FERN_RC_UNDERFLOW_TRAP=1` and `FERN_RC_FREE_DEBUG=1`: clean, no trap, no
quarantine hit.

## The row that moved further than the pin predicted

`escaping_holder_now_clean` is the bind spelling of the inline cell's
`escaping_holder`, which is pinned **leaking** — the source dies inside the
callee while the target is returned. The inline position refuses it, because
there the read is admitted on the store alone and nothing looks at what happens
to the holder afterwards. Through the bind, the walk is *already* looking at
every use of `tt`, and it can see the local reaches only the returned holder's
field. So the stricter admission is the one that admits more here, which is the
opposite of the direction one expects and worth saying out loud.

It is the load-bearing soundness case either way, and it reads all four
elements back after churn on all three engines.

## Gates

- per-module emit-all fixpoint — green, 240 s, 0 skips, foreground
- `scripts/cliff-bench` — 458360 / 258145264, the campaign constant, unmoved
- the repo complexity ratchet — 411 / 17609 unmoved. The read walk selects its
  expression through `strarrfld_bind_read` rather than branching in the
  `StmtVar` arm, which is where the first spelling put two points.
- the shape-selected set (TEST-GATES rule 13, applied to the shape changed:
  files declaring a `string[]` struct field and pinning leak accounting) —
  22 files, 39 test functions
- all four matrices — construction-retain, container-sink, both leak matrices
- the new suite (9 cases) and the sibling it re-pins

## What remains

Nothing in this shape. The construction-retain matrix reached zero earlier
today and the hoisted spelling was the one row outside it still pinned leaking
in the same file. The three narrowings above are deliberate and each has a
pinned row: a nested store, a call-argument borrow, and any other use of the
bound local. Widening the first two wants the borrowable registry visible at
admission time, which is a different change.
