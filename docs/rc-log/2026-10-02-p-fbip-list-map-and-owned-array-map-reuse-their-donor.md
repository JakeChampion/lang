# An fbip list map and an owned array map reuse their donor

2026-10-02 — `ssaunits.payload_root` / `named_after`, `ssarc.donor_slots`,
`ssarc.in_place_maps` / `map_in_place`, `semlower.pure_rows`. Closes #11073.

## What was wrong

Two programs native compiles and runs without allocating were refused by the
self-host's E068:

- `TestWASMFbipMapZeroAlloc`: `fbip function map_inc(own xs: List)` rebuilding
  each `Cons` from the matched one. "struct/enum construction "Cons" at op 43".
- `TestWASMFipOwnedMapZeroAllocAndSharedDonorImmutable`:
  `fip function twice(own xs: i64[]) { return xs.map(...); }`. The self-host
  had no in-place map (R7 of `docs/REUSE-CONTRACT.md`).

## The fbip map: three gaps, one shape

The self-host turns `map_inc` into a loop (tail recursion modulo cons), so the
cons block reads `h` and `t` out of `xs`, builds the new `Cons` over a hole, and
passes `t` to the loop header as the next `xs`. Native has no loop here and
pairs the match's box with the construction directly (R4).

1. **The payload was never taken.** `t` leaves the block on the back edge, and
   in the flow derived before the take a value anchored to `xs` keeps `xs`
   live out. `payload_root` asked `live_out_has` for an enum, so it declined,
   and `xs` was released at the edge after the construction, with `t`
   retained. It now asks `needed_past`, as a record field already did.
2. **`needed_past` saw the header as a later reader.** `named_after` walked
   from the cons block back into the loop header, which reads the PHI `xs` —
   a new value. The walk now stops at the block that defines the value; only
   a phi operand there reads the old one.
3. **The flow was stale for the take.** "Under `needed_past`,
   `sibling-slot-survives-the-take` freed none of its 43 blocks" is why the
   enum arm kept `live_out_has` (2026-09-24). That was the flow, not the
   predicate: a take whose box was live out of the read's block now sets
   `reflow` (`PayloadTakes.crossing`), so the box dies at the read, as a
   taken record field's does. `TestSelfHostPayloadTakeIR` holds that case.

With the take, `xs` dies at the read of `t`, ahead of the construction, and
the pairing could hold it as a token — except that `List` has variants of two
field counts, so `union_slots` had no count to match. `donor_slots` reads the
count off the variant a payload read in the same block names: the read only
runs where the tag test chose that variant. `recipient_slots` gives a variant
construction its own variant's count where the union has none. The runtime
still compares the block's real size (`__fern_alloc_reuse`); the static count
only decides whether pairing is worth emitting.

## The owned map

`ssarc.in_place_maps` marks a `__arrm_map__` call whose receiver is a
consumed parameter dying at the call with nothing supplied from it, whose
element function is a capture-free lambda that reaches no effect, whose
element type is an unchanged 64-bit integer, and whose result is not linked
to a borrowed parameter. `map_in_place` lowers it to native's loop:
`sole_owned_base` (one uniqueness test, a copy when shared), then
`buf[i] = f(buf[i])`, the receiver's drop becoming the result's unit.

The effect half is `semlower.pure_rows`, native's `effectfulFuncs` over the
typed rows: a call to a builtin in either caps table, a call through a value,
or an unknown non-`__` callee marks a row, and marks spread to callers. It is
computed only for a module with a `__arrm_map__` call.

The element and the call result stay on the operand stack under the store's
box and index: a frame's scratch slots are typed once per function, and an
i64 in one collides with a narrow use of the same slot elsewhere.

`FERN_SELFHOST_NO_REUSE=1` turns R7 off with the rest of the reuse layer.

## Measured

`FERN_LEAKCHECK=1`, x86-64, the two test programs without their annotations
(the base compiler refuses them with them):

| program | before | after |
| --- | ---: | ---: |
| 100 maps of a 50-cell list | 5,050 allocs, exit 999 | 50 |
| the same, plus a shared 5-cell list mapped once | 5,060 | 60 (the 5 copies are the shared cells) |
| 200 maps of a 64-element i64 array, plus a shared 4-element one | 1,005, exit 92 | 7 |
| a receiver read after its map (R7 declines) | 2 | 2 |

Every row balanced, live 0.

## The trap

The pinned stage0 (c891ebc) refuses a local reassigned inside a nested loop as
the argument to an `own` parameter (E051, "not a borrowed one") where the
current checker accepts it. A driver that compiles under `make selfhost-cli`
then fails every `internal/e2eselfhost` test that builds one.
`stage0 -check examples/self_host/fern.fern internal/stdlib` takes 7 s and
catches it before a suite does.

## Gates

`TestSelfHostFipInPlaceReuse` (`internal/e2eselfhost/
self_host_fip_inplace_reuse_test.go`): both programs and a live-receiver
control on x86-64, arm64 and wasm, under `FERN_STRICT_IR`, oracle-checked.
`TestSelfHostCompilePathEnforcesFipBudget` keeps an i32 map, which R7
declines on both compilers, refused by E068.
