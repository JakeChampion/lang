# A map field is taken at its read

2026-10-07: `ssaunits.field_takeable`. #11119, the key/value core of FIP
experiment 3 (#9585).

## The shape

```fern
function step(own db: Db, k: i64, v: i64): Db {
  return Db { ...db, entries: db.entries.insert(k, v), n: db.n + 1 };
}
```

The field read borrowed the map, so `db` and the insert both held it, the
insert's uniqueness test answered "shared", and `unshared_map` rebuilt the
whole map before every insert. #11119 measured this against the native
pass's forced copy; the self-host has no forced copy, only the runtime test,
and the borrow was enough to fail it.

## The rule

A record field read already takes its slot when the record is the frame's
own and is read no further except through other fields (`payload_root`,
#11203). `field_takeable` admitted an array or a record; it now admits a map
too, whose slot is one counted word the take nulls the same way. The same
predicate gates a tuple's element and an owned array's element, so a map
there is taken as well. A shared record keeps its map, and the insert copies
as before.

## Measured

#11119's program on x86-64, `bench.allocs_after` over 1,000 updates of a
1,500-entry map:

| spelling | main | this rule |
|---|--:|--:|
| local, `m = m.insert(k, v)` | 2,000 | 2,000 |
| field, `Db { ...db, entries: db.entries.insert(k, v) }` | 3,004,000 | 2,000 |
| hoisted, `let m = db.entries; ... m.insert(k, v)` | | 2,000 |

The compiler's own `checker.fern` compile (`scripts/selfhost-alloc-bench`)
allocates 24,002,308 times under either tree: no compiler code rebuilds a
map field through a spread of an owned record. The 28.1M -> 24.0M drift CI
reported on this PR was already on main (#11764).

`TestSelfHostPayloadTakeIR` holds the shape on x86-64, arm64 and wasm
(`map-field-of-owned-record-updates-in-place`, 18,259 allocations before,
409 after, bound 450) and holds that a borrowed record keeps its map
(`map-field-of-borrowed-record-is-not-taken`).

## What is left

- A string field is still left to the borrow (`field_takeable`): its slot may
  hold a view or a literal at run time.
