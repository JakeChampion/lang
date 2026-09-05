# 2026-09-05 — the overwrite release moves to the far side of the COW

`var snap = m; m = m.insert(k, v2)` leaked the value the set replaced. The
release was in the IR, emitted just before the set — and the set's first act is
`m = __map_cow_inplace(m)`, so the release ran while the buffer was still
shared. That ordering leaves only two options at the site, and both are wrong:
release anyway and free storage `snap` still names, or gate on
`__fern_rc_is_unique(m)` and reclaim nothing. #8272 chose the gate, after the
ungated form turned out to be a live SIGSEGV on x86-64 and a silent wrong
answer for kind 4. #8421 is what the gate cost.

`__map_dec_value` is the other side of the same COW: `__map_set_keyed_impl`
reaches it *after* the copy, so the buffer and — since #8390 — its string value
cells are that handle's alone. A kind-5 arm there needs no ownership test of
its own, and reclaims on the aliased path as well as the sole-owner one.

## Measured

200 rounds, `FERN_LEAKCHECK=1`, both handles read back and dropped.

| probe | | wasm32 | arm64 | x86-64 |
|---|---|--:|--:|--:|
| aliased overwrite | before | 9600 | 9600 | 12800 |
| aliased overwrite | after | **0** | **0** | 6400 |
| aliased two-entry | before | 9600 | 9600 | 19200 |
| aliased two-entry | after | **0** | **0** | 12800 |

Answers were right throughout; this was a leak, not corruption.

The two-word ABIs go to a perfect census. x86-64 halves and stops there,
because a **third** defect sits in the same program: #8277 strands the map's
KEY buffer once per map on any read with an aliased key. Isolated here, on
x86-64, 200 rounds:

| shape | live_bytes | per round |
|---|--:|--:|
| build + insert + **0 reads** | 0 | 0 |
| build + insert + 1 read | 6400 | 32 |
| build + insert + 3 reads | 6400 | 32 |
| key 14 → **72** chars, 1 read | 19200 | 96 |
| value 16 → **72** chars, 1 read | 6400 | 32 |

Flat in the number of reads and in the value length, linear in the KEY length,
zero without a read — #8277's recorded signature exactly, and present with no
alias and no overwrite anywhere in the program. It was untouched by this work
and was the whole x86-64 residual; it closed the same day, and the numbers
above now read 0.

## What the tests assert

wasm32 and arm64 assert an absolute census: `allocs == frees`, `live == 0`.

x86-64 could not, while #8277 was open: pinning its number would have banked
that leak, so it asserted the DIFFERENCE instead — the same program with the
alias and the overwrite taken out strands exactly as many bytes. **#8277 closed
the same day** (`2026-09-05-map-read-key-taint.md`), and all three backends now
assert the census directly; the two baseline programs and the differential
helper are gone.

Every assertion fails on the pre-fix compiler.

## How the runtime knows whether to free a cell

The release is one spelling on both ABIs — `__fern_str_dec(v as string)`,
where `usize → string` is a two-word load where slots are boxed and a plain
reinterpret where they are not. What differs is the cell: a boxing target
leaves a dead `(data, len)` box behind and a single-word one never had it.

The runtime cannot work that out for itself. The INC direction can — that is
`__map_own_str_slot`'s rebox-identity trick, `(slot as string) as usize == slot`
— but it allocates a cell to answer, which is precisely the wrong direction for
a release. So `map_new` records it: the kind-5 value tag's high bytes carry the
cell's byte size, 0 when the slot IS the value, exactly as a boxed wide scalar
(kind 0) already carried its own. It is the size `boxIntoCell` allocated, so
the `__free` that reads it back matches its `__alloc` — which is a better
contract than the canonical `__fern_cell_free`, whose 16 is hardcoded and
which x86-64 correctly does not have at all.

That last point is why `__free` and not `__fern_cell_free`: the branch is dead
on x86-64 but the call is still emitted, so reaching for the canonical helper
would have meant giving x86-64 a cell-free it has no use for. `__free` is a
builtin every backend already provides, and `__map_free_val_cell` next door
frees the wide-scalar cells the same way.

## Two guards this rests on

- **`__map_drop_values` must keep skipping kind 5.** That column is walked by
  the IR-generated `__drop_map_str_values`, and `__map_dec_value` is now no
  longer a no-op for it — so widening the early return (`vk0 != 2 && vk0 != 3
  && cellBytes == 0`) would release every string value twice. Kind 5 reads 0
  from `__map_val_cell_bytes`, which is why the string cell size went into a
  SEPARATE accessor rather than that one.
- **No pre-drop may come back beside it.** Both would release the same value,
  and the pre-drop's sole-owner gate would not stop it: they are on opposite
  sides of the COW and both run on the sole-owner path.
  `TestLowerMapStringValueOverwriteHasNoPreDrop` pins the absence, on all three
  ABIs and with a boxed key as well as a raw one.

## Erased

Both IR string pre-drops (100 lines), the transient key-cell box the two-word
one needed to reach `__map_lookup_val`, and the two tests that pinned them.
`emitMapPredropSoleOwnerGate` stays for kind 4 alone.

Beyond removing the gate, the runtime placement covers three shapes the
pre-drops declined outright: a struct key (`keyKind3`), a boxed key on the
native single-word path, and an `m` or `k` that `exprSafeToReevaluate` refused.

## Not done

- **Kind 4 (struct / enum) values** keep the pre-drop and the gate, so an
  aliased overwrite of a struct value still leaks. The runtime helper is
  type-erased and cannot call the generated per-value drop; closing it means
  threading a drop function into the set, which is a bigger change than this.
- **`emitMapOverwriteDrop`** — CLOSED the same day by #8431, which deleted it:
  it was a second implementation of the map-drop chain, and routing its three
  callers through the shared one is what widened the walk. The measurement
  below is what made that attributable. Original note follows.

- ~~`emitMapOverwriteDrop` still skips the kind-4 and kind-5 value walks.~~
  The measurement that was blocked on this leak HAS now been made, since
  #8277 landed the same day and took the last confounder out of the program:
  100 rounds of the COW chain, `live_bytes`, x86-64 / arm64 / wasm —

  | chain | walked today? | | | |
  |---|---|--:|--:|--:|
  | `arr_chain` (kind 2) | yes | 0 | 3200 | 3200 |
  | `str_chain` (kind 5) | no | 3168 | 83648 | 83968 |
  | `struct_chain` (kind 4) | no | 6368 | 6400 | 9568 |

  The walked column is clean; the un-walked ones are the whole leak, and a
  boxing ABI's string chain is quadratic in its length for the same reason
  #6828 fixed on the key column. It should widen — #8431.
- **`__map_delete_keyed_impl`** frees only a kind-0 cell (`__map_free_val_cell`),
  so a delete strands an array or string value; it also mutates without a COW.
  Neither is touched here, and neither is new.
- The **self-host** lowers `__fern_str_dec` as a no-op, joining its
  `__fern_arr_dec` / `__fern_drop_arr_ptr` siblings in the map runtime's
  documented safe-leak mode. Its backends have no rc-guarded string dec —
  `__fern_str_free` frees a fresh non-escaping string outright, which is not
  what a shared column slot wants. Goal 2's RECLAIM side.
