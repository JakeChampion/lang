# 2026-09-03 — native: a `Map[K, string[]]` value column, and what `get_or` hands back (#7910 (a))

`m = m.insert(i, [w(i), w(i + 1)]); acc = acc + m.get_or(i, []).len()`, 100
rounds, allocs/frees live — native, the oracle the self-host is measured
against, was leaking this itself:

| backend | before | after |
| --- | --- | --- |
| x86-64 | 600/200 **17600** | 600/600 `0` |
| arm64 | 600/200 **19200** | 600/600 `0` |
| wasm (bump, N=50 → N=5000) | 9008 → **880208** | bounded |

**Base.** The before/after rows above were measured against this branch's
base, 27e0ee4a4. The isolation table below was taken during the
investigation, one base earlier — the shape's verdicts are unchanged
between the two (the four headline probes measure identically on both),
but read its exact byte counts as of that tree.

Two leaks stacked, and the split is the useful part (x86-64):

| variant | before | column walk only | + get_or ownership |
| --- | --- | --- | --- |
| insert, `m.len()` only | 500/300 **12800** | 500/500 `0` | 500/500 `0` |
| insert, `m.has(i)` | 500/300 **12800** | 500/500 `0` | 500/500 `0` |
| insert, `match (m.get(i))` | 700/500 **12800** | 700/700 `0` | 700/700 `0` |
| insert, `m.get_or(i, []).len()` | 600/200 **17600** | 600/200 17600 | 600/600 `0` |
| insert, `var v: string[] = m.get_or(i, [])` | 600/200 **17600** | 600/300 16000 | 600/600 `0` |
| the same two on `Map[i32, i32[]]` | 400/200 **4800** | 400/200 4800 | 400/400 `0` |
| `Map[i32, S]`, `m.get_or(i, S {…}).k` | 500/200 **12800** | 500/200 12800 | 500/500 `0` |

12800 is the column — the array and its two strings — on every row that
never reads it back; the get_or rows add the retained value's second claim
and the fallback (4800 on the `i32[]` column is exactly those: a 32-byte
array and a 16-byte `[]`).

## Leak 1 — the column: `mapValHasDrop` had no arm for a string-element array

An array value routes through `arrElemStructDropName`, which names a deep
per-element walk for struct / enum / tuple / nested-array elements and
declines a `string` element — so a `string[]` value fell to
`__map_drop_values`' kind-2 release, one flat `__fern_arr_dec` per value:
buffer freed, every string in it stranded. `__drop_strarr` is a generated
one-argument drop over `__fern_drop_arr_str` (the release a `string[]` local,
field or payload already takes), and the column now walks through
`__drop_map_via___drop_strarr`. With that, a map whose column is never read
back is flat.

## Leak 2 — `get_or` on a counted-read column owned nothing

`__map_retain_val` retains a hit on a kind >= 2 column (arrays, structs,
enums) — the get / get_or read co-owns the value alongside the map. For
`m.get(k)` the rebuilt Option is reclaimed by `emitMapGetScrutineeReclaim`,
deep for exactly these kinds. For `m.get_or(k, d)` nothing consumed the
reference: `ownedCallResultType` admits user functions only, so neither the
temp form's post-read release nor a binding's exit sweep applied, and the
FRESH fallback `[]` stayed behind too — the generic call path keeps an
argument the result may alias.

The runtime now retains on a MISS as well (`__map_get_or_impl`,
`__map_get_or_keyed_impl`: a no-op for the kinds `__map_retain_val` ignores),
so the result carries a reference of the caller's own on both outcomes and a
fresh fallback temp is dead once the helper has read it. On that contract the
IR admits `__method_Map_get_or` on a counted non-string value in
`ownedCallResultType` and in `rhsTainted` — the ownership and the
free-eligibility verdicts, which must agree — and the get_or lowering gains
the arm the string value already had: stash a fresh fallback temp, call, end
the temp. A string value keeps its own per-ABI inline retain.

The struct-column `get_or` row (from the (b) isolation table) went clean on
native under the same change; that one had leaked 12800 with the default
struct temp and the retained value both stranded.

## Witnessed

`TestX86_64MapArrayValueColumnReclaim` / `TestArm64…` (leakcheck balance on
the temp and the bound read, FreeOn + `__rc_underflow_count()` on a two-key
column read back through get_or and get, bump-bounded N=50 vs N=5000) and
`TestWASMMapArrayValueColumnReclaim` (bump-bounded + underflow).

Not moved: a `Map[K, string[]]` reached with a string KEY on the two-word
ABIs or a struct key takes the boxed / keyed get_or paths, which keep the old
fallback handling — the counted result is owned there too (the admission is
by value type), only the fresh fallback temp is not yet ended.
