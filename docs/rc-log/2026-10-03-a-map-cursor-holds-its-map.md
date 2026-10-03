# 2026-10-03 — a map cursor holds a unit of its map

Self-host typed path, all three backends. Follows
`2026-09-25-a-map-cursor-is-a-unit.md`, whose first open item this closes.

## What changed

The cursor anchored its map: `map_iter` was a projection, so the frame kept
the map live while the cursor was. That only reaches as far as the frame, so
`build` refused any function whose result could carry a cursor ("cursor
result escapes its map"). With the AST lowering deleted, that refusal was a
compile failure for programs native accepts, such as
`function over(m: Map[K, V]): MapIter[K, V] { return m.iter(); }`.

The cursor now takes a unit of its map instead:

- `ssaunits.operation_supplies` counts `map_iter`'s receiver, so the planner
  moves the map's unit in on its last use and retains it otherwise.
  `ssasem.projects` no longer lists `map_iter`. The key and value reads are
  still projections of the cursor.
- A cursor has one child, its map. `ssarc.drop_cursor_map` reads it out under
  the cursor's uniqueness test and drops it as `Map[K, V]` before the cursor's
  own release frees the block.
- Where the map lives:
  - register backends: element 0 of the builtin cursor block, as before;
  - wasm: element 0, after moving the index from `+8` to `+12` in
    `[keys, vals, map, index]`;
  - `core/map`'s routed cursor: `it + 16`, read with `__mapiter_map_impl`.
    `__map_iter_impl` allocates 32 bytes rather than 24, and native's
    `MapIter` declaration gained the matching field, so its drop frees the
    same size.
- The escape refusal and `semtypes.holds_map_iter` are gone.

## Measured

`TestSelfHostMapIterOutlivesItsFrame` runs on x86-64, arm64 and wasm, each
under `FERN_LEAKCHECK=1`, eight rounds. It covers a cursor returned bare over
a map the caller passed in, one in a tuple, two in an array, and one passed
straight through. The census is balanced on all three (96 / 96 on x86-64).
Every cursor is drained after the caller inserted into its map, and the
cursors made before the insert do not see it.

The seven `TestSelfHostMapIterIsReclaimed` shapes are unchanged and still
balanced.

## Traps

- **Native shows inserts through a cursor.** Native's cursor stores the
  map's buffer with no count, so an insert made after `m.iter()` grows the
  same buffer. `let it = m.iter(); m = m.insert("b", 10);` then drains 11
  where value semantics say 1. The self-host answered 1 before this change
  and still does. Do not take native's figure as the oracle for a cursor
  program that writes to its map.
- E065 still rejects returning a cursor over a function-local map, in both
  checkers. That rule exists for native's uncounted cursor. The self-host's
  cursor would now be sound there, but lifting E065 is a language change, and
  native would have to keep it.
