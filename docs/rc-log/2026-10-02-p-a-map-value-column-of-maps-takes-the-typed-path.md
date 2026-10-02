# 2026-10-02 — a map value column of maps takes the typed path

`ssasem.boxed_map_value`. Fixes #11071.

## The refusal

`Map[i32, Map[i32, i32]]` was refused as "unsupported map shape" on every
target, so `TestWasmMapSetCountedStoreBalanced`'s nested-map program did not
compile through the self-host. `boxed_map_value` left a map-typed value out of
the columns of boxes, on the grounds that a map's box carries no count on the
register backends, so a read of one would have nothing to retain.

That premise was out of date. Every map representation the self-host emits is
counted at `box - 8`:

- the backend runtime map: `__fern_map_new` takes its box from
  `__fern_arr_box` on x86-64 and arm64, and `$__fern_map_new_k` from
  `$__fern_str_box` on wasm. The `__fern_map_free` family releases one unit:
  a decrement while the box is shared, the columns and the block for the last.
- core/map's handle: `map_new_impl` stores rc 1 at `m - 8`, and
  `__map_drop_impl` decrements while it is shared.

`docs/SELFHOST-SEMANTIC-SOURCE.md`'s description of a map's unit already said
so. The old reason survived only in comments (`boxed_map_value`,
`ssarc.routed_box`, `ssaunits.tuple_take_field`) and in that doc's list of
refused shapes, all of which this change corrects.

## What changed

The `!is_map(v)` term is gone from `boxed_map_value`, so a map value column is
a column of boxes like any other: `counted_map_value`, value kind 3, released
through `__sem_release_Map[..]` (the `_vf` members on the runtime map,
`__map_drop_boxes_impl` on core/map's), retained on a read as any box is,
and copied with a unit of each entry by `ssarc.unshared_map` when a shared
outer map is mutated. No runtime or backend code changed.

## Measured

Self-host CLI, `FERN_LEAKCHECK=1`, on x86-64, arm64 (qemu) and wasm32-wasi
(wasmtime); every answer matches `fern -interp`:

| program | before | after |
|---|---|---|
| #11071's nested-map read, 200 rounds | refused | 1000 allocs = 1000 frees, live 0 |
| round trip (read, mutate, overwrite, `without`, iterate, `values()`), 20 rounds | refused | 762 = 762 |
| struct-keyed outer over core/map maps, and the reverse, 10 rounds | refused | 570 = 570 (wasm 580 = 580) |
| insert into a shared outer map, both representations, 10 rounds | refused | 370 = 370 |

`FERN_SANITIZE=1` is clean on x86-64 and arm64. The programs are
`TestSelfHostMapOfMaps{X86_64,Arm64,Wasm}`.
