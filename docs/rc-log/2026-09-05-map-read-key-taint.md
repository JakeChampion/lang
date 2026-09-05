# 2026-09-05 — a Map read looked like it might keep your key

On the native single-word string ABI, `m.get(k)` with an ALIASED key — a
`var k` the caller still owns, rather than a fresh concat at the call site —
stranded the map's key buffer. One block per MAP, sized by the key, flat in
the value length and in the number of reads. arm64 and wasm32 were clean.

The issue (#8277) guessed at read-side key handling adding a reference. It is
the other direction: nothing adds one, the caller's own release is taken
away.

## What the tables said

`freeEligible`, for the same function with and without one read:

| | wasm32 | arm64 | x86-64 |
|---|---|---|---|
| build + insert | `k,m,stem` | `k,m,stem` | `k,m,stem` |
| build + insert + one read | `k,m,stem` | `k,m,stem` | **`m,stem`** |

A string local's scope-exit release is gated on that table — an ineligible
string is skipped entirely, never touched — so losing `k` from it loses the
`__fern_str_dec` that would have taken the buffer from rc 1 to 0. The op
counts show it directly: adding the read ADDS two decs on the two-word ABIs
(the result temp, the lookup key cell) and REMOVES one on x86-64.

The accounting, per round:

| | rc after |
|---|---|
| `var k = stem + "-key"` | 1 |
| `m.insert(k, v)` — the counted key store incs | 2 |
| map drop, `__drop_map_str_keys` decs | 1 |
| `k`'s scope exit — **suppressed** | 1, stranded |

## Why the table dropped it

`computeFreeEligible` carries a conservative taint (#4174 follow-up): on the
native single-word ABI a string ident passed to a user function may be
RETAINED by the callee — stored into a container that flows back out — which
the intraprocedural analysis cannot see, so it is not reclaimed caller-side.
A leak at worst, never a use-after-free.

`__method_Map_get` / `_get_or` / `_has` / `_delete` reach that arm. They are
not user functions and they retain nothing: `__map_lookup_keyed` gets to
`__map_hash_str` and `__map_eq_str` and no further, and neither moves a
count. `__method_Map_set` never had the problem — it is handled earlier, by
the counted-store arm that models the retain it really does.

The exemption already existed. `copyingBuiltinArgs` lists the builtin
arguments a callee neither retains nor can alias out through its result, and
its header said the Map reads were deliberately absent because "their results
alias the receiver's interior". That is true of the RECEIVER, and of
`get_or`'s FALLBACK — which IS the result on a miss. It is not true of the
KEY, and the table is keyed by argument POSITION precisely so the three can
be told apart. It just held one position per callee.

So: positions per callee rather than one, and the Map reads join at
position 1.

## Measured

200 rounds, `FERN_LEAKCHECK=1`, x86-64, `live_bytes`:

| shape | before | after |
|---|--:|--:|
| aliased key, `get_or` | 6400 | **0** |
| aliased key, `get` | 6400 | **0** |
| aliased key, `has` | 6400 | **0** |
| aliased key 72 chars, `get_or` | 19200 | **0** |
| aliased overwrite (#8421's probe) | 6400 | **0** |
| aliased two-entry (#8421's probe) | 12800 | **0** |

Answers unchanged throughout, and `allocs == frees` on every row after.

## Why nothing caught it

`rcCorpus` has carried `map_string_keys_churn_free` — a `Map[string, string]`
that inserts an aliased key and reads it back — since long before this. It ran
green throughout. Three conditions have to coincide, and that case misses two
of them:

| condition | `map_string_keys_churn_free` |
|---|---|
| the key needs a HEAP buffer | `"key"` is 3 bytes — SSO-inline, no buffer |
| it must be a RUNTIME concat | `"k" + "ey"` folds to a literal; its data-8 sentinel makes `__fern_str_dec` a no-op, and nothing is allocated at all |
| it must be an ALIAS at the read | ✓ — the one it does have |

Measured on x86-64 without the fix, 200 rounds, same program otherwise:

| key | allocs | live_bytes |
|---|--:|--:|
| `"k" + "ey"` (short, folded) | 800 | 0 |
| `"k" + "ey-that-is-far-past-seven-bytes"` (long, still folded) | 800 | 0 |
| `stem + "ey-that-is-far-past-seven-bytes"` | 1000 | **9600** |

Either miss alone is enough to hide it, which is the same reason
`TestX86_64MapStringColumnReclaim`'s own probe could not see it — the issue
records that its key is a fresh concat, and the fresh/alias axis is the third
condition.

`map_aliased_key_read_free` is the new corpus case that meets all three, and
it fails the x86-64 leak gate by 9600 bytes with the four table entries
removed.

## What it unblocks

The x86-64 map-string probes could not assert a byte census while this was
open: `2026-09-05-map-overwrite-release-after-the-cow.md` had them compare
against a baseline with the alias and the overwrite removed, because pinning
an absolute number would have banked this leak. They now assert the census
directly, on all three backends, and the two baseline programs and the
differential helper are deleted.

Every one of those assertions fails with the four table entries removed.

## Not done

- The taint itself is unchanged for genuine user functions, which is where it
  belongs: the analysis still cannot see a callee that stores a string
  argument into something it returns. `paramCountedRetain` is the mechanism
  that narrows it per-callee, and it already exempts the counted case.
- Nothing here touches the two-word ABIs, which never took the taint.
