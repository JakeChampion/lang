# 2026-09-16 — a column of boxes is released by its own function

`Map[string, JsonValue]`, the object column of `std/json`, refused every
importer of the module ("unsupported map shape", 58 corpus sites): the
runtime's map free family walked a string column and a column of string
arrays, and knew no other value shape.

**The shape.** A map's value column may now hold boxes of any shape the
plan admits — a record, a union, an array of anything but strings, a
tuple — as value kind 3. The map owns one unit per entry, as it
does for a string column; what differs is who knows how to release one.
The physical lowering emits `__sem_release_<T>` beside the drop helpers,
one body per column type a function mentions: the release a frame gives
a unit of the type, with the uniqueness gate, the children walk and the
count. The runtime never learns the shape. Its `__fern_map_free_vf` and
`_ksvf` members take that function as a second argument and call it on
every live value before freeing the buffers; `__fern_map_set` takes it
beside the flag bits (a register on x86-64 and arm64, a table slot and
`vdeep` 2 on wasm) and calls it on the value an overwrite supersedes, so
the entry the free never reaches is released where it is dropped. The
op names the function in its string field, which a kind 3 map never
needs for a derived key equality. A column of function values stays
refused, since a function value is lent everywhere here and owned
nowhere, and so does a column of maps, whose box carries no count for a
read to retain.

**The get.** A `get` over a counted column was refused ("map get of a
counted value column", 25 sites once the shape was admitted): the
runtime copies the entry into the Option box without a retain, and the
box is released as any Option, payload and all. The lowering now retains
the payload on a hit, so the box owns one unit of it, as the plan
already said it did.

`json_object`, `derive_from_json`, `derive_from_json_i64` and
`json_error_location` produce whole and match native under the leak
check on all three targets.

## Pinned

`TestSelfHostSSAPhysicalRCRejects` checks a column of i32 arrays plans,
its free is the `_ksvf` member handed the release as a function value,
the release is among the function's helpers, and its insert carries value
kind 3 and the release's name; a column of function values still refuses.
`TestSelfHostSemanticSourceRC` runs `map_vrec` (a record column:
overwrite, `get_or`, `get` hit and miss), `map_venum` (a union column
keyed by integers, overwritten with a bare variant) and `map_varr` (an
array column) on all four targets under the leak check; the print golden
carries `keyed_recs`.
