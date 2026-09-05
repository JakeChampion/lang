# 2026-09-05 — a boxed key switched off the fallback's release

`m.get_or(k, <a freshly allocated fallback>)` stranded the fallback, one block
per call, whenever the map's KEY was BOXED. That is two key kinds, not one: a
string key on a two-word ABI, and a wide scalar on wasm32, which boxes an i64
because it does not fit a 4-byte slot. The narrowing below used string keys,
so the first draft of this log framed the bug as string-only; it is not.

## Narrowed

Same program throughout, 100 rounds, `FERN_LEAKCHECK=1`, `live_bytes`,
arm64 / wasm / x86-64:

| shape | before | after |
|---|---|---|
| no read at all | 0 / 0 / 0 | unchanged |
| `Map[string, i32[]]`, `get_or(k, [0])` | **3200 / 3200 / 0** | 0 / 0 / 0 |
| `Map[string, i32[]]`, fallback hoisted to a `var` | 0 / 0 / 0 | unchanged |
| `Map[string, Rec]`, `get_or(k, Rec { … })` | **3200 / 1600 / 0** | 0 / 0 / 0 |
| **`Map[i32, i32[]]`**, `get_or(k, [0])` | 0 / 0 / 0 | unchanged |

The last row against the second is what identifies it: only the KEY type
differs, and the leak follows the key being boxed. Not the ABI, not the value
type, not the number of allocations the value makes. A fallback that is a `var`
rather than a temporary is owned by its scope and released there, so it never
depended on this path at all.

## The gate said something about the wrong argument

`internal/ir/ir.go`'s counted-read `get_or` arm existed for one purpose: a
FRESH fallback temp is dead once the helper has read it, on hit and miss alike,
so the arm stashes it (`stashOwnedArgTemp`) and ends it after the call
(`emitArgTempDrops`). Its own header records what it was worth — *"48 B per
`m.get_or(i, [])` on an i32[] column, the array and its strings on a string[]
one"*.

It was gated:

```go
… && !keyKind3 && !needBoxK && !needBoxV
```

`needBoxK` is a fact about the call's ARGUMENT SHAPE — a boxed key has to be
written into a cell for the call. It says nothing about the fallback. But
switching the arm off for a boxed key switched off the fallback's release with
it, and every map whose key boxes lost it — both kinds `needBoxK` covers.

So the fix is to stop conflating the two: the arm now runs with a boxed key and
boxes it the way every other read does — `boxIntoCellSlot` before the call,
`freeLookupBoxCell` after. That helper is exactly right here for the reason it
was written: the helper probes with the cell and retains nothing, so the cell
is transient and the key inside it is the caller's.

`needBoxV` stays excluded. A boxed VALUE is the wide-scalar path and a
different question.

## What it unblocks

`TestMapCowChainReclaimCensus` pinned 1280 on arm64 and wasm one commit ago,
attributed to this bug — 40 blocks for the 20 rounds each of `arr_chain` and
`struct_chain`, the shapes whose per-round `get_or` has a fresh fallback. It
now asserts an absolute census on all three backends, and the pin, its
attribution paragraph and the per-backend `wantLive` column are deleted.

That the pin was exactly right is the useful part. It was written as *"closing
#8432 fails here and has to lower the number deliberately"*, and that is what
happened: the census failed with `live_bytes=0, want 1280 … Lower: #8432 got
better — bank the new number here`. A leak gate that only catches regressions
would have gone quiet on the improvement instead.

## Why the corpus never saw it

The same reason `arr_chain` and `struct_chain` did and the other four chain
shapes did not: a fallback has to be a FRESH allocation at the call site. A
literal (`"?"`), a scalar, or a hoisted `var` costs nothing to strand or is
owned elsewhere. `str_chain` uses a literal fallback and `scalar_chain` has no
read at all, which is why the residual looked like it tracked the value type
when it tracked the fallback.

## The wide-key half, measured after the fact

pullfrog read the landed diff and pointed out that the gate is `needBoxK`,
not "is the key a string", so the fix reaches further than this log claimed.
Confirmed by measuring rather than by reading it back —
`Map[i64, i32[]].get_or(k, [0])`, wasm32, 100 rounds:

| | live_bytes |
|---|---|
| insert only, no read at all | 1600 |
| with the read, gate restored | 4800 |
| with the read, fixed | **1600** |

The fallback array was the whole difference, so the wide-key half is fixed by
the same change. arm64 and x86-64 hold an i64 in a pointer slot and never box
it, so they read 0 throughout.

The 1600 that survives is NOT this bug: it is there with no read in the
program, and it is the boxed wide-KEY cell `insert` stores, which the key
column walk does not reclaim on wasm32 (the #8276 neighbourhood). That is why
`TestMapWideKeyGetOrFallbackIsReclaimed` pins the read as a DIFFERENTIAL
against an insert-only baseline instead of an absolute census — it measures
this fix and stays honest while that separate gap is open.

## Not done

- **#8434** — `m.without(k)` stranding the whole map. Adjacent in the same
  file, unrelated in cause, and much larger.
- The `needBoxV` exclusion, which no measurement has been aimed at.
