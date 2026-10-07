# 2026-10-07 — a view map value column is a string column

`ssarc.routed_value`, `ssarc.release`. Refs #10867, #9608. Follows
2026-09-30-k-a-view-map-value-read-takes-a-fresh-box.

## Before

`routed_box` excluded `str`, so `Map[_, str]` stayed on the runtime's
association list while every other value column ran on core/map's hash table.
The reason given in #10866 was that a view box carried the immortal count, so
core/map's retain on a read or a copy-on-write counted nothing and the reader's
release freed the map's entry.

That reason no longer holds. Since STRING-COUNTED-VIEWS-2026-10-02 a heap view
descriptor starts at a count of one with the `BORR` marker: a retain counts it,
and its last release frees the descriptor alone. The only immortal view boxes
left are a frame's (`ssaunits.frame_views`), which an insert never stores
because a store keeps the box past the instruction, and literals, which are
never freed. What an insert hands the column is therefore a unit like a
string's: a counted descriptor, a counted string retagged as a view, or a
literal.

## Change

- **A view value column routes as a string column** (core/map valKind 5).
  Reads retain the entry as a string column's do; an overwrite releases the
  superseded entry through `__map_dec_value`; `without` releases the removed
  one; a copy-on-write retains each through `__map_own_str_slot`; the last
  drop walks them through `__map_drop_strcols_impl`, or the keyed closure for
  a keyed key column. No new value kind or intrinsic was needed.
- **A routed map's release takes the map alone.** `release` passed the runtime
  map's per-type release functions (`map_release_fns`) to any map whose value
  was a box by `boxed_map_value`, which a view is. Routed, that put a second
  argument on `__map_drop_strcols_impl`. x86-64 and arm64 ignore it; wasm
  either rejected the module (`values remaining on stack at end of block`) or,
  in a smaller program, never released the map: 168 bytes in 3 blocks live at
  exit for one insert.
- The runtime map's view branches (`own_view_hit`, the `__fern_str_own` reads,
  the fresh-box `values()` and copy) are deleted: no view column reaches it.

## Measured

Insert n = 8,000 view values, then look each up four times, x86-64, best of 5:

| value column | before | after |
|---|---|---|
| `Map[i32, str]` | 423 ms | 4 ms |
| `Map[i32, string]` | 4 ms | 4 ms |

`TestSelfHostRoutedScalarMaps/view_values`: views into an arena string, into a
literal and a counted string held as a view, under integer, view, `i64` and
keyed key columns; overwrite, `without`, an alias's insert and `without`, a lent
receiver's insert, `get` / `get_or` hit and miss, `values()`, `keys()`,
iteration, `cleared` and a map in a struct field, followed by 64 allocations
that would reuse any box released early. Interpreter's output on x86-64, arm64
and wasm32-wasi, `FERN_SANITIZE=1`, 329 to 333 allocations and as many frees,
and no `__fern_map_find` in the program. The test now requires the leak census
line on every target, not only x86-64; the wasm leak above was invisible
without it.

The two #10701 programs in `TestSelfHostSemanticProduction` answer as before on
all three targets with balanced censuses (14 and 27 allocations), now on
core/map.

## Trap

**The reason a shape was excluded can expire.** #10866's exclusion was right
when written and stopped being necessary soon after, when counted view
descriptors landed for an unrelated reason. Re-derive an exclusion from the
current runtime before building the workaround it implies; the new value kind
the issue proposed would have duplicated valKind 5.
