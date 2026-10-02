# 2026-10-01 — a map with a wide column routes onto core/map

`ssarc.cell_column`, `box_key`, `cell_get_or`; core/map's `__map_cell`,
`__map_cell_load`, `__map_cell_free`, `__map_get_cell_impl`,
`__map_cell_column` and their `f64` twins; `__map_drop_strcols_impl`. Refs
#9608, fixes #10968.

## The shape

`Map[K, V]` with an `i64` or `u64` key, or an `i64`, `u64`, `f32` or `f64`
value, stayed on the runtime's association list under the typed lowering, so
every lookup scanned every entry. 4,000 / 8,000 / 16,000 inserts and lookups
on a `Map[i64, i64]` took 0.043 / 0.239 / 0.740 s on x86-64, and a
`Map[i32, i64]` the same. `u8` keys and values stayed there too.

## What changed

A wide key or value crosses core/map's pointer-wide slot as an eight-byte
cell, on every target: the typed lowering emits one IR for all three, so it
cannot keep a wide scalar in the slot on the natives and box it on wasm the
way native's IR does. The map is made with keyTag `2 | 8 << 8` and valTag
`8 << 8`, so core/map's existing cell machinery takes over: it hashes and
compares a key through its cell, frees a displaced value's cell on
overwrite, the removed entry's cells on delete, and every cell on clear.

On the lowering side, each op boxes a wide key once into a scratch cell
(`box_key`) and returns it afterwards, except an insert whose entry is new,
where the column keeps it — the entry count not growing is the overwrite
test the string key column already uses. `get_or` boxes its fallback, reads
the answer out of whichever cell comes back and returns the fallback's cell.
`get` reads through `__map_get_cell_impl`, which builds the `Option` out of
the cell in the reading compiler's own layout, as the column snapshots do.
A float crosses as its bits. The last reference to such a map goes through
`__map_drop_strcols_impl`, which now frees key and value cells as well as
releasing string columns; `__map_drop_boxes_impl` frees key cells too.

A `u8` column holds its byte in the slot like an `i32`, and snapshots
through the existing `__map_u8_column`.

## A native bug on the way

core/map's copy-on-write gave a copied buffer its own string key slots and
value cells but shared its key cells, so on wasm32, where native boxes a wide
key, a delete through an alias freed a cell the original map still read
(#10968). `__map_own_copied_cols` now copies key cells too.
`TestMapWideKeyCopyDelete` is the regression; its wasm leg exits 2 before the
fix.

## Measured

| inserts + lookups | before | after |
|---|---|---|
| 4,000 | 0.043 s | 0.004 s |
| 16,000 | 0.740 s | 0.005 s |
| 64,000 | — | 0.014 s |

The production row `a-map-with-a-wide-column-routes` runs every operation on
each new column type, with an aliased copy, on x86-64, x86-64 under the
sanitizer (nothing held at exit), arm64 and wasm, against native's answers.

## A trap

`__map_drop_strcols_impl` keeps its name although it now drops cells too: the
pinned stage0 compiler's own map drops call it, so renaming it left every
driver the pin builds with a dangling map release. The self-host map tests
then failed in unrelated functions — a std/string body the driver lowered to
an invalid SSA graph — with nothing naming the map.

## What is left of #9608

Struct and enum keys (`keyed_map_key`) still use the runtime, as do a
`usize` value column and a view value column.
