# 2026-09-03 — a map's struct value column with an rc field (#7910 (b))

`Map[i32, S]` with `S { name: string, k: i32 }`, one fresh `S` inserted per
round and read back through `match (m.get(i))`. The wide string is built by a
call (`w(i)`, branching on `i`) so nothing folds and nothing sits inline.

| backend | native | self-host before | self-host after |
| --- | --- | --- | --- |
| x86-64 | 600/600 `0` | 800/400 **16800** | 800/800 `0` |
| arm64 | 600/600 `0` | 800/400 **16800** | 800/800 `0` |
| wasm | — | 700/600 **6000** | 700/700 `0` |

**Base.** The before/after rows above were measured against this branch's
base, 27e0ee4a4. The isolation table below was taken during the
investigation, one base earlier — the shape's verdicts are unchanged
between the two (the four headline probes measure identically on both),
but read its exact byte counts as of that tree.

Two leaks were stacked on the row, and the isolation table separates them
(x86-64, 100 rounds, allocs/frees live):

| variant | native | before | after column walk | after grow release |
| --- | --- | --- | --- | --- |
| all-scalar `Q`, insert-built, no read | 300/300 | 600/400 **4800** | 600/400 4800 | 600/600 `0` |
| `S` with a string field, insert-built, no read | 400/400 | 700/300 **16800** | 700/500 4800 | 700/700 `0` |
| `A` with an `i32[]` field, insert-built, no read | 400/400 | 700/300 **13600** | 700/500 4800 | 700/700 `0` |
| `S`, insert-built, `match (m.get(i))` reading `s.name.len()` | 600/600 | 800/400 16800 | 800/600 4800 | 800/800 `0` |
| `S`, insert-built, `match (m.get(i))` reading `s.k` only | 600/600 | 800/400 16800 | 800/600 4800 | 800/800 `0` |
| `S`, LITERAL map `Map { 1: S {…}, 2: S {…} }`, no read | 400/400 | 700/500 **12000** | 700/700 `0` | 700/700 `0` |

The read kind never moved a number: the get / match / scalar-read rows all
sit on the no-read row's figure. What moved is the column, and then the grow.

## Leak 1 — the column: 96 B/round, the struct box and its string

`map_val_box_one_dec` admitted a value column to the "MAPVA:" credit only when
one `__fern_arr_dec` per element was the WHOLE release — a scalar-element array
or an all-scalar struct. A struct with a string or array field was refused
outright, so the column fell to the shallow `__fern_map_free`: box, string box
and string data all stranded, 96 B/round.

The credit now admits any declared struct `nested_field_deep_drop_ok` accepts
(`map_val_struct_deep_type`), and `emit_map_buffers_free` puts one call ahead of
the `_va` free: `__map_vals_struct_drop_<T>(box) -> box`, a per-type helper
each backend writes over its own map layout — the raw `{keys@0, vals@8}` pair
on x86-64 and arm64 (the value column handed to the existing
`__struct_arr_elems_drop_<T>` walk), the rc-headered `cap@4 / vals@12 / used@16`
box on wasm (occupied slots walked under `$__fern_rc_is_unique`, each through
`$__struct_drop_<T>`). The `_va` free's one-dec walk then takes the emptied
boxes as before. The three bodies exist because the map layout is the one
thing the backends do not share; the admission and the call are in irlower
once.

Where a field is not admitted by the whole-program string-field scan
(`strfldok:<T>`), `__struct_drop_<T>` leaves it and the row reads leak, not
underflow — the same refusal every struct local already gets.

## Leak 2 — the grow: 48 B/round, the two empty buffers `Map {}` starts with

`FERN_RC_TRACE` named the residue after leak 1 closed: two 24-byte blocks per
round from one site inside `__fern_map_new` — the empty keys and vals buffers
the first insert grows out of and never frees. A struct column is a flag-1
(raw-alias) column in `map_kv_elem_flag`, so the type-level `map_owncols` bit
stays clear and the insert takes the leak-only push: `values()` on such a column
hands out the raw buffer, and the reclaim-on-grow push would free it under a
live `var vs = m.values()`.

That reason is about a READ the body may or may not contain, so the credit
that answers it is per LOCAL, not per type. A reclaimable map ("MAP:") whose
body never calls `keys()` / `values()` / `iter()` on it and never `for`s over it
is now also credited "MAPOWN:" (`map_has_col_alias_read`), and `map_site_owncols`
sets the op's owncols bit at an insert whose receiver carries it. The MAP:
credit already refuses an aliased, escaping or iterated map, so nothing but the
map holds its buffers on those maps.

The all-scalar `Q` row above was carrying this leak all along; the credit it
had covered the boxes and not the grow.

## Witnessed, and what is not

The leak-matrix rows `map_struct_strfield__insert__match`,
`map_struct_strfield__literal__len` and `map_struct_arrfield__insert__len` pin
the three shapes on x86-64 (with the sanitize leg) and arm64;
`TestSelfHostMapStructColumnWasmIR` runs the same three programs on wasm and
requires a balanced census. All three read `clean clean`.

Not moved, found alongside: `m.get_or(i, S { name: "", k: 0 }).k` on this map
leaks on NATIVE too (500/200, 12800 B) — the default-argument temp — and a
struct value SHARED with a live local (`var s0 = S {…}; m = m.insert(i, s0)`)
leaks 9600 on native against 4800 self-host. Both are outside (b) and neither
is a column-walk gap.
