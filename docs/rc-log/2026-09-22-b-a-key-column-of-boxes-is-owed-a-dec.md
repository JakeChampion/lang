# A key column of boxes is owed a dec per key

2026-09-22. #9966. The AST lowering's map free released a struct- or
enum-keyed column's *buffer* and never the key boxes in it.

## The measurement

Two programs identical but for the key type, four rounds of twelve inserts,
self-host, `FERN_LEAKCHECK=1`, `x86-64-linux`:

| key type | allocs | frees | live_bytes |
| --- | --- | --- | --- |
| `i32` | 36 | 36 | 0 |
| `Coord` | 84 | 36 | **2304** |

48 stranded boxes = 12 keys x 4 rounds, 48 bytes each. Not one key was
reclaimed. After the fix both rows read `84 / 84 / 0` and `36 / 36 / 0`, on
x86-64 and arm64 alike.

## Why it was only the keys

The map free is a family whose member is chosen from three credits, and only
one of them was ever about the key column:

- `MAPVS:` a fresh-string VALUE column
- `MAPKS:` a fresh-string KEY column
- `MAPVA:` a fresh one-dec-BOX VALUE column

There was no box-KEY credit, so a struct key column fell to whichever member
carries `kfree = "arr_dec"` — the shallow buffer free.

The emitters made this cheap to fix, because the key free was already a
parameter with only two values ever passed:

```fern
s = emit_ir_map_free_variant(s, "_va",   "arr_dec",      "arrarr_free",  "mapfva");
s = emit_ir_map_free_variant(s, "_ksva", "str_arr_free", "arrarr_free",  "mapfksva");
```

`arrarr_free` — sole-owner gate, one `__fern_arr_dec` per element, then the
buffer — is exactly what a box key column wants, and it had never appeared in
the `kfree` position. Four new members (`_ka`, `_kavs`, `_kava`, `_kavsa`) are
one line each per register backend.

## Two protocols, not one

The register backends and wasm own a key differently, and the difference is
what made this two fixes rather than one.

**The register backends never retain a key on insert.** A credited column's
units come only from keys that were FRESH, which is precisely what
`map_column_args_fresh` proves. That is why the deep free needs a credit at
all, and why an aliased key must decline it: the frame still owns that box.

**wasm counts per insert.** `$__fern_map_set` inc'd a borrowed key when
`kis == 1` and `$__fern_map_release` released one under the same test, so a
struct key (`kis == 2`) was neither retained nor released. Balanced for a
borrowed key, and a leak for a fresh one, whose unit went into the column and
was never given back. Both gates widen to `kis != 0` together; widening only
one would have turned the leak into an over-release.

## The trap this sets

`map_arg_binding_is_fresh` is purely syntactic. A variant construction reads
as an ordinary call, so `Tag.TagLo(i)` and a function *returning* a `Tag` are
the same shape — and the callee may still hold what it returned. Only the
struct table separates them, so the predicate had to take one: `structs`
threads through the six functions of the freshness chain exactly as
`str_fresh` already did.

Getting this wrong is silent in the direction that matters. Crediting a
non-fresh key emits a dec on a box the frame still owns, and the frame's own
release then underflows — which is why every case in
`self_host_map_box_key_reclaim_test.go` reads `__rc_underflow()`, and why the
aliased-key case is a test rather than a comment.

## What this does not reach

The three struct-key conformance fixtures still leak, and not because of the
key column: they read the map through `keys()` or iterate it, so they get no
`MAP:` reclaim credit at all. `map_struct_key_grow` frees 22 of 96
allocations — the whole map, not twelve keys. That is the pre-existing scope
of AST-path map reclaim, unchanged here.

A key struct whose own fields are counted also stays on the shallow free.
`map_key_box_column_ok` claims an all-scalar struct or enum and nothing else,
which is the same contract `map_val_box_column_ok` already states for values
("what this credit claims, it reclaims in full").

The typed path still refuses a struct-keyed map (#9962). The four members
added here are what its `map_release_name` will select once it does.
