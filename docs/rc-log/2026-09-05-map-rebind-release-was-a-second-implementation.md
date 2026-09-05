# 2026-09-05 — the COW-rebind release was a second implementation

`appendMapDropChain`'s own header says what it is for:

> This is the one dispatch every map-drop site shares (the exit sweep's slot
> form, struct fields, tuple elements, closure captures), so the column
> coverage cannot drift between them.

`emitMapOverwriteDrop` — the release of the OLD handle at `a = <a COW copy of
a>` — did not use it. It hand-rolled the same three steps and always took the
generic `__map_drop_values` for the value column, so a struct (kind 4) or
string (kind 5) value column was never reclaimed there. That function is how
the coverage drifted.

So the fix is a deletion. `emitMapOverwriteDrop` is gone and its three callers
(two rebind sites in `ir.go`, the cow-threaded param sweep in `rc_insert.go`)
call `emitMapSlotDrop`, which is what the shared chain already was.

## What the narrow copy was for

#6227. The release may only cover the columns the copy CLAIMS, or it frees
storage the fresh handle still names — freeing the key column pulled the
strings out from under it. When this site was written no value column was
claimed. All of them are now: the wide-scalar one outright (#7114), the string
one by reboxing (#8390), the struct one by the retain that was always there
(#8420). The premise expired; the code did not.

## Measured

100 rounds of `map_cow_chain_reclaim_test.go`'s own chain functions,
`FERN_LEAKCHECK=1`, `live_bytes`, x86-64 / arm64 / wasm:

| chain | value | before | after |
|---|---|---|---|
| `str_chain` | `string` | 3168 / 83648 / 83968 | **0 / 0 / 0** |
| `struct_chain` | `Rec` | 6368 / 6400 / 9568 | **0** / 3200 / 3200 |
| `arr_chain` | `i32[]` | 0 / 3200 / 3200 | unchanged |

The string chain is the one that mattered. On a boxing ABI each copy allocates
a key cell AND a value cell per entry, and the round's own release freed
neither, so the leak was quadratic in the chain length — the same shape #6828
fixed for the key column, still open for the value one until now.

`arr_chain` is unchanged because its column was already walked, which is what
makes the attribution clean: what moved is exactly the two columns that were
skipped.

## The residual is a different bug

arm64 and wasm keep 3200 on the array and struct chains. It is one 32-byte
block per round (linear — 200 rounds reads 6400), it is in `arr_chain` whose
column the release always walked, and it is absent for string and scalar
values. That is #8432, pre-existing and not this.

The whole six-function program reads `allocs=686 frees=686 live=0` on x86-64
and 1280 on both two-word ABIs — exactly 40 blocks for the 20 rounds each of
`arr_chain` and `struct_chain`, the other four shapes contributing nothing.
That arithmetic is what says the residual is theirs.

## What the tests assert

`TestMapCowChainReclaimCensus` — absolute on x86-64, pinned at 1280 on the two
ABIs with a pointer to #8432, so closing that issue fails here and has to lower
the number deliberately. All three fail without this change (1856 / 5568 /
6496).

`TestMapOverwriteDropWalksTheValueColumn` replaces
`TestMapOverwriteDropLeavesSharedValueColumn`, which asserted the opposite on
the expired premise. It now checks the rebind guard's callees include
`__drop_map_str_values` for a string column and `__drop_map_via___drop_struct_Rec`
for a struct one, on both ABIs.

The rc corpus and all three leak gates pass **unchanged** — no over-release,
and no pinned baseline moved. That is the gate a widened release walk had to
clear, and it is why the change is safe rather than merely green.

## Not done

- **#8432** — the two-word array/struct residual above.
- The kind-4 aliased OVERWRITE still leaks (a different site: the pre-drop,
  still on the wrong side of the COW because the runtime helper is type-erased
  and cannot call the generated per-value drop).
