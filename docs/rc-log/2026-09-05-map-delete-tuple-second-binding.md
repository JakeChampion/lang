# 2026-09-05 — the delete tuple's map element was a second binding with no count

`m.without(k)` hands back a `(Map, boolean)` tuple. On the in-place COW branch
`__map_cow_inplace` returns the receiver's OWN handle, and
`emitMapDeleteReturningTuple` stored it as an owned tuple element. So two names
shared one refcount and both released it.

That is #8276's fault reached without a callee: one handle at rc 1, two
bindings. Not "two handles over one buffer" — which matters, because
`__drop_map_str_keys` guards on `__fern_rc_is_unique` of the HANDLE, and the
handle genuinely IS unique. Guarding on the buffer instead would not have
helped; what was missing was a count.

## What it cost

100 rounds, `FERN_LEAKCHECK=1`, `__rc_underflow_count()` in the exit code:

| shape | x86-64 | arm64 | wasm32 |
|---|---|---|---|
| `var st = m.without(k)`, m never rebound | exit 1 (underflow) | **SIGSEGV** | **trap**, OOB |
| `var m2: Map[…] = st.0` | exit 1 | **SIGSEGV** | **trap** |
| `m = st.0` (the reassign idiom) | 0, but 0 frees | 0, but 0 frees | 0, but 0 frees |

Row 3 is why this sat unnoticed. It does not crash — because it leaks. The map
is never released at all, so the second release never happens. The leak was
standing in for the missing refcount, which is also why `map_delete_*` cases
exit 0 while stranding six figures of bytes.

## The fix is a pair

Neither half works alone, and each fails in its own direction:

- **The COW-seam retain** (`computeMapCowBindSites`) reached
  `var (m2, ok) = m.without(k)` but not `var t = m.without(k)` — the spelling
  the corpus itself uses. Its doc said `without` "can ONLY be consumed by
  destructuring", which was never true. Granting the retain ALONE removes every
  crash, and doubles the leak: nothing returns the count, the map is pinned
  above rc 1 for the rest of the scope, and every later `__map_cow_inplace`
  takes the COPY branch instead of mutating in place — a whole table per
  mutation, none of them freed. That is the mechanism behind the field doc's
  "~1.8 kB an iteration" warning.
- **The projection credit** (`rhsTainted`) refused ownership to a Map read out
  of a tuple, on the grounds that crediting it segfaulted
  `map_delete_tuple_churn_free`. True — without the retain. Crediting ALONE is
  that segfault.

Together they balance: the seam gives the tuple's element a count, the
projection's destination takes one of its own, and both drops are matched.

## Measured

Per-shape corpus cases (#8486 split these out of one body, so a fix can be
attributed at all):

| case | before | after |
|---|---|---|
| `map_delete_i32_key_churn_free` | 144000 / 144000 / 96000 | **0 / 0 / 0** |
| `map_delete_bound_miss_churn_free` | 128000 / 152000 / 112000 | **0 / 0 / 0** |
| `map_delete_bound_reassign_churn_free` | 128000 / 152000 / 112000 | **0 / 8000 / 8000** |
| `map_delete_projected_self_assign_churn_free` | — | unchanged |
| `map_delete_destructure_churn_free` | — | unchanged |

Against the SINGLE case this same diff read 304000 -> 368000, a pure
regression. Same code, same behaviour; only the attribution changed.

## What the residue is, and is not

The 8000 on the two boxing ABIs is 16 B per delete **HIT**. The miss row is a
flat 0, which is what identifies it: it is the deleted entry's boxed key cell.
`__map_delete_keyed_impl` releases the value cell via `__map_free_val_cell` and
never releases the key's. Separate bug, and only measurable now — until these
cases reclaimed there was no clean baseline to see 16 bytes against.

## Still open

- **`m = m.without(k).0`** — a self-assignment whose RHS aliases the LHS. The
  counts balance for the result, but the assignment's overwrite-dec of the old
  binding hits the same handle and frees what is being assigned. Reclaiming the
  projected temp without fixing that is a use-after-free (`got 37, want 42` in
  `TestMapSwarProbe`); adding a second retain to cover it is functionally clean
  and leaks, for the pin-above-1 reason above. The fix is that `x = f(x)` must
  not dec the old value when the RHS can alias it — a general Perceus
  assignment question, not a Map one.
- **`var (m2, ok) = m.without(k)`** — already has the seam retain, so its
  residue is a third thing again.
- The deleted entry's key cell, above.

## A note on which gate caught what

Two wrong approaches got as far as a green leak gate:

- admitting the delete tuple in `ownedCallResultType` (8 callers, including the
  discarded `m.without(k);` and the call-arg stash) takes the corpus to
  112000/136000/104000 **and miscompiles Map on all three backends**;
- the same via `freshOwnedFieldContainerType` — narrower, same fault.

The leak gate was HAPPY with both and asked for the improvement to be banked.
Only the functional Map suites objected. Reach for `TestMapSwarProbe*`,
`Map[…]DeleteReturnsMapBool` and the wasm Map battery before believing a
leak-gate improvement in this area.
