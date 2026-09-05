# The string map-value column gets its own claim on a COW copy (#8354)

`__map_cow_inplace` and `__map_clone` build the copy's kv buffer with one
`__memcpy`, which is shallow over the pointer-shaped columns.
`__map_own_copied_cols` then walks the live entries and gives the copy its own
claim on each. It claimed the key column and the wide-scalar value cells
(#6827) and left the string value column shared — so both handles reached
rc 1 independently, both ran `__drop_map_str_values`, and whichever dropped
first took the other's cells and buffers with it.

Two entries, one overwritten under an alias and one never written, 200 rounds,
both handles read back:

| | wasm32 | arm64 | x86-64 |
|---|---|---|---|
| before | **134**, out-of-bounds read | **killed by a signal** | **97**, wrong answer |
| after | 0 | 0 | 0 |

The wasm fault address is the tell: a memory fault at `0x656b2d61`, which is
`a-ke` — the freed key's own characters, read back as a pointer.

## Recognition was the blocker, and a kind is the answer

The claim itself was already written. `__map_own_str_slot` (was
`__map_own_key`) is exactly the operation a string VALUE slot needs, for the
same reason a key slot needs it: on a two-word ABI the slot holds an
unrefcounted cell that the column's drop frees outright, so a second owner
needs a cell of its own; on a single-word ABI the slot is the data pointer and
the inc is the whole claim. It is key-specific in name only, and now serves
both columns.

What was missing was telling a string value apart at runtime. It shared
valKind 1 with every other unreclaimed pointer (tuple, generic enum, slice,
runtime handle), so **string values are now kind 5**.

## The `>= 2` guards, and the one that bit

Introducing a kind above 3 is not free: `>= 2` is the runtime's shorthand for
"reclaimed", and kind 5 joins it whether or not each site wants it. Every
guard had to be re-read rather than assumed.

| site | with kind 5 |
|---|---|
| `__map_own_copied_cols` `retainVals` | excluded — 5 takes the new `ownVals` claim instead |
| `__map_retain_val` | excluded — `emitMapGetRebox` already retains a string on every ABI, so a second inc double-retains every read |
| `__map_values_impl` | **excluded** — see below |
| `mapSetValueCounted` | the string arm now runs FIRST; its answer is ABI-specific and `>= 2` would shadow it |
| `mapGetHandsCountedValue` | included, and its separate string test retired as redundant |
| `mapValTag` | unchanged — a non-array kind carries no size byte |

`__map_values_impl` is the one that was missed on the first pass and caught by
`map_string_column_escape_churn_free`, which segfaulted on arm64. Its
`valKind >= 2` arm returns a single-pointer-stride snapshot
(`__map_ptr_column`), and it sits *above* the string-shaped arm — so kind 5
fell into it and `m.values()` on a `Map[string, string]` read two-word string
cells at pointer stride. The same shadowing hazard as `mapSetValueCounted`,
one arm further down, and the reason the guard audit has to be exhaustive
rather than sampled.

## Not closed

The aliased OVERWRITE still leaks the value it replaces — 3 blocks a round on
x86-64, 2 on arm64 for the probe above. Two things have to hold for that to
close, and neither is this change:

- the IR's overwrite pre-drop is gated off when the handle is shared (it runs
  *before* the set's own `__map_cow_inplace`, so releasing there would free
  what the other handle names), and
- `__map_dec_value`, the runtime overwrite-dec that runs *after* the COW and
  is therefore sole-owner-correct by construction, is a no-op for a string
  column.

The second is where the release belongs. It needs `__fern_str_dec` and
`__fern_cell_free` exposed to Fern source — neither is in `FuncSigs` today,
though `__fern_rc_inc`, `__fern_arr_dec` and `__fern_drop_arr_ptr` are, and
for exactly this reason — plus a way to tell the two string ABIs apart in
`map.fern` for the dec direction. Landing it would also retire the IR-side
pre-drop widening and `emitMapPredropSoleOwnerGate` for strings.

The **struct** value column (kind 4) is still shared, and is the residual
#8354 keeps: its deep drop is the IR-generated `__drop_map_struct_<T>`, so
claiming it needs the same per-field walk in the inc direction rather than a
recognition test.

## Gates

`mapAliasedTwoEntrySrc` on all three backends (fails on each without the
claim: x86-64 97, arm64 signal, wasm trap). Full `internal/e2e` Map suite
(354 tests, 0 skips), the rc correctness corpora, leak gates, conformance leak
census and the arm64 high-heap corpus, full `internal/ir`, the lint
complexity ratchet, and `make check-sources`.
