# 2026-11-01 — a value that dies on an edge donates its box

`ssaunits.dying_unclaimed`, `carry_routes`, `served_by`, `ssarc.edge`. Refs
#13990.

## The shape

```fern
function (s: Set) retire(i: i32): Set {
  ...
  let cur: Set = Set { ...s, retired: s.retired + 1 };
  if ((st.id & 1) == 0) {
    let initial: i64 = cur.__initial(t);
    cur = Set { ...cur, closed: cur.closed.with(t, cur.closed[t] + 1) };
    if (target - cur.limits[t] >= 1) {
      cur = Set { ...cur, limits: cur.limits.with(t, target) };
    }
  }
  cur = Set { ...cur, list: cur.list.with(i, cur.list[i].__closed()) };
  return cur;
}
```

`__initial` has a branch, so the one-block rule keeps it as a call, and the
call borrows the first update whole. The split pass forwards the reads of
the second update to the fields of the first. It keeps the second update
only for the phi at the join.

The third update, inside the inner `if`,
names the second as its base, but reads none of its fields. So the second
update is read nowhere in that block, and the plan drops it on the edge
into the block. The carry only moved a value that dies at an instruction.
The third update built a fresh box on each round the inner `if` took.

Without the call, or without the inner `if`, the split pass removes every
update and the round allocates nothing.

## The rule

A value the plan drops on an edge now counts as dying in the block the edge
leaves, on that edge. That holds when the block the edge enters claims the
value's slot count.
The carry searches routes from the edge's target alone, and a source whose
edge reaches no target holds nothing. On an
edge that carries, the lowering keeps the box in place of the drop. The hold
comes after the phi arguments are loaded and before the other drops of the
edge. The
construction in the target block spends it as it spends a token carried
from an instruction.

The certifier checks the edge holds as it checks the instruction holds: the
value dies on that edge, and the token enters the target on every path.

## Measured

- `TestSelfHostEdgeDroppedDonor`: 64 rounds allocate 2 boxes against 34
  before. The sanitized run is balanced on all three targets.
- `TestSelfHostReuseCarryKeepsBlockDonor`: 200 reads allocate 8 boxes
  against 9, balanced on both compilers.
- `TestSelfHostSemanticSplitOff`: `gate_rounds` allocates 300 boxes against
  350. The `Gate` the `if` reassigns dies on the edge into the `if`, and the
  `Gate` built there takes its box.

- `TestSelfHostFieldReclaimWasm`: the `Acc` that `build` replaces each round
  dies on the back edge and is dropped to its fields, and the next `Acc` is
  built in its box. The test accepts that shape beside the release call.
- In the default configuration the serial probe of #13990 reads 46.7 a
  request against 46.8, and the mux probe 28.6 against 28.8.
- The instrumented compiler allocates 36,115,367 boxes on `checker.fern`
  against 36,126,501 on the base of this change, -0.03%.

## Two traps

A value dying on an edge into a block without a claim was listed at first.
Each such candidate ran the route search. The planner's own work then cost
68,000 allocations on `checker.fern`, against 55,000 saved in the code it
planned. Routes past the edge's target are left to a later entry.

The dying lists were rebuilt as locals and returned in one record. A record
built from locals is not a constant, and the one for an empty block cost one
allocation a block, 45,000 on `checker.fern`. The empty block returns the
constant record again.

## Source-census correction

The initial CI run at `21269681fb` fails the source census: 5,209 manual
increment sites exceed the unchanged ceiling of 5,205. Four new index loops
now use ranges over their fixed inputs. The route work queue still uses
its original loop because the queue can grow during the search.

The local census passes without a baseline change. All eight focused edge,
carry, split, shared-donor, tested-take and certification roots pass on Linux
without skips. Fresh full CI remains mandatory before merge.

## Box-lifetime regression controls

Two existing block-scope programs now report 500 balanced allocations instead
of 900. The five original fixture bodies remain byte-for-byte unchanged.
The expected free counts decrease only for the two programs that replace
the outer local. All other exact counts remain unchanged.

Two new controls retain the previous record across each replacement. An opaque
callee reads its scalar and array fields after the replacement. Both controls
still require exactly 900 frees. All seven hazard cases also check a nonzero
balanced census, zero live bytes, zero RC underflows, and sanitizer execution.

The stabilized compiler runs both original programs and both controls on
ARM64 Darwin, ARM64 Linux, x86 Linux, and Wasm. All sixteen processes balance
with zero live bytes. Every original reports 500 events, and every control
reports 900. Wasm supplies a leak census only. Four focused Linux roots pass
without skips.

No performance baseline or compiler rule changes accompany
this test correction. Full current-head CI remains mandatory before merge.

## Wasm field-reclaim controls

The field-reclaim test expected a call to the complete record release helper.
The edge carry instead drops the old fields and reuses the record box.
The shape check now requires a complete release or both field drop and box reuse.

Both original programs remain byte-for-byte unchanged.
The two-million-round program retains its 16,777,216-byte memory cap.
The shared snapshot still checks all three original array elements.
Both programs now also require nonzero balanced allocation counts and zero live bytes.

The previous compiler reports 4,000,000 and 10,000 balanced events.
The stabilized edge compiler reports 2,000,001 and 5,001.
All four processes have zero live bytes, and both capped runs complete.
The actual Linux test passes without skips.
These Wasm results supply a leak census, not native sanitizer coverage.

The correction changes no compiler rule or performance baseline.
Fresh full CI remains mandatory before merge.
