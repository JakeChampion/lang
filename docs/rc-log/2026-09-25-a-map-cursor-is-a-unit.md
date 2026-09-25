# A map cursor is a unit

2026-09-25. Self-host typed path, all three backends. Closes #9562's self-host
half.

## What changed

The cursor was lent: a bare `__fern_alloc` block the frame never counted
(`2026-09-21-a-map-cursor-reads-the-columns-it-points-at.md`). It is now owned.

- x86-64 and arm64 build it with `__fern_arr_box(2)`: map at data+8, index at
  data+16, with the rc word at data-8 where every other box keeps it.
- wasm builds its `[keys, vals, cursor]` block with `$__fern_arr_box(12)`.
  `$__fern_mapiter_free` frees the two snapshot arrays on the last unit, then
  the block. The snapshots retain nothing, so a shallow free is the right one.
- The planner counts it. It left `unit_free` and joined `hands_out`, and it
  stays in `ssasem.projects`, so a cursor is a fresh unit that still anchors its
  map. `slice` already had that shape. `ssarc.release_name` answers
  `__fern_mapiter_free`, which the register backends map to `__fn___fern_arr_dec`.

## Measured

x86-64, `FERN_LEAKCHECK=1`:

| program | before | after |
|---|---|---|
| `examples/tests/json_roundtrip_test` | 192 B live | 3811 / 3811, 0 B |
| `conformance/cases/audit_std_json` | 16 B live | 153 / 153, 0 B, stdout matches |

`TestSelfHostMapIterIsReclaimed` covers five shapes, each run for 8 rounds, on
x86-64, arm64 and wasm: `for`-in, `for`-in with `break`, a bound cursor, a fresh
cursor passed as an argument, and an aliased one. All of them have a balanced
census.

## Still open

- A function that returns a cursor is still refused ("cursor result escapes its
  map"), so its module stays on the AST lowering. That lowering never releases a
  cursor, and it also types an unannotated `var o = over(m)` and a `MapIter`
  parameter as `i32`. Both are AST-only and go when that lowering is retired.
- A typed module containing `__rc_underflow()` falls back to the AST lowering,
  because the intrinsic has no semantic contract. A typed-path test therefore
  cannot use it as its double-free check. The census balance serves instead.
