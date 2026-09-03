# 2026-09-03 — self-host: a `Map[K, string[]]` value column (#7910 (a))

The same shape the native entry of this date measured, now on the self-host:
`m = m.insert(i, [w(i), w(i + 1)]); acc = acc + m.get_or(i, []).len()`, 100
rounds, allocs/frees live, against native's `0` on every backend after its own
(a) commit.

| backend | before | column free | + default temp |
| --- | --- | --- | --- |
| x86-64 | 1000/400 **25600** | 1000/900 2400 | 1000/1000 `0` |
| arm64 | 1000/400 **25600** | 1000/900 2400 | 1000/1000 `0` |
| wasm | 800/500 **13600** | 800/700 1600 | 800/800 `0` |

**Base.** The before/after rows above were measured against this branch's
base, 27e0ee4a4. The isolation table below was taken during the
investigation, one base earlier — the shape's verdicts are unchanged
between the two (the four headline probes measure identically on both),
but read its exact byte counts as of that tree.

Isolation (x86-64):

| variant | before | after |
| --- | --- | --- |
| insert, `m.len()` only | 800/500 **18400** | 800/800 `0` |
| insert, `m.has(i)` | 800/500 **18400** | 800/800 `0` |
| insert, `match (m.get(i))` | 900/600 **18400** | 900/900 `0` |
| LITERAL map, `get_or(1, []).len()` | 1000/400 25600 | 1000/1000 `0` |
| `Map[i32, i32[]]`, `get_or(i, []).len()` | — | 800/800 `0` |
| insert, `var v: string[] = m.get_or(i, [])` | 1000/600 **20800** | 1000/802 **14256** |

## The column: refused because one dec is not a string[]'s whole release

`map_val_box_one_dec` admitted a value column only where one `__fern_arr_dec`
per element released the element whole — a scalar-element array or an
all-scalar struct box — and a `string[]` value (pointer elements) was refused
outright, so the column fell to the shallow `__fern_map_free` and every value
leaked its buffer and both strings: 184 B per round on the register backends.

The credit (`map_val_box_column_ok`) now admits a `string[]` column whose every
inserted value is an array literal of literals or registered fresh-string
producers — the freshness question is by column KIND now ("s" string, "b"
box, "a" string array), threaded through the one set of walkers the string
and box columns already shared, with the fresh-string registry for the "a"
question — and `emit_map_buffers_free` routes it to `__fern_map_free_vsa` /
`_ksvsa`. On x86-64 and arm64 those are the map-free family's two new members
with `__fern_strarrarr_free` as the value walk (each value whole through
`__fern_str_arr_free`); on wasm `$__fern_map_release_vsa` is the release with
`$__fern_arr_dec_ptr` as its per-value call, the body factored out of
`map_helpers` so the two cannot drift. Need-gated on the three like the box
pair.

## The default: a fresh `[]` with no owner

The residue after the column was one 24-byte block per lookup on every
backend — `get_or`'s `[]`. The self-host helper hands the value back as a
BORROW (no retain on a hit; the asm helper "hands out the raw value"), so the
consumer never releases the result, and the fresh default had no release
either: on a hit an orphan, on a miss the result itself.

The default now lives in an `is_arr` scratch slot: after the call, when the
result is not the default (a hit), the slot is released and zeroed; when it
is (a miss), the slot keeps it until the next lookup through this site or the
exit sweep frees it — after the consumer has read it. Admitted for the empty
literal only: an element-carrying literal would need the element walk and a
struct literal its field drop.

## Not moved

- `var v: string[] = m.get_or(i, [])` — the BOUND read (1000/802 above): the
  binding's read-side retain and its sweep do not net to zero for a string[]
  value; native reclaims it after its get_or-ownership change, the self-host's
  "counted read" model is the slice that would.
- A `Map[K, P[]]` (struct-element array) column keeps the shallow free: the
  value walk would be `__fern_arrarr_free` per value under a per-type element
  field walk, a different helper.

## Witnessed

Leak-matrix rows `map_strarr__insert__get_or` and `map_strarr__literal__len`
(x86-64 with the sanitize leg, arm64) and `TestSelfHostMapStrArrColumnWasmIR`
on wasm with a balanced census.
