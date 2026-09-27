# Two quadratic scans in the typed path's own bookkeeping

2026-09-27 — `semrecords.records_error`, `ssarc.caller_rows` /
`ssarc.bare_indexed`. Refs #8171, #9415.

The profile in `2026-09-27-a-nominal-release-is-one-call.md` (110 G Ir for
`checker.fern` to a binary, native-built `bin/fern-selfhost`) named two costs
that grow with the square of the module rather than with its size.

## A record schema compared with every earlier one

`ssasem.analyze` verifies every function's schema table, and
`semrecords.records_error` compared each record against every record before
it: 9,096,528 `instance_error` calls and a `semtypes.equal` apiece, 2.2% of
the compile. Only two instances of one struct can collide or disagree, so a
record is now compared only with the earlier records of its name, chained by
`util.hash_bucket`. The chain runs in ascending order, which is the order the
pairwise scan reported in, so a table with several faults still names the same
one first. `record-duplicate-schema-apart` pins a duplicate separated from its
twin by a record of another name, which the chain has to skip over.

## `borrow_bare` rebuilt per produced function

`ssarc.caller_sigs` rewrote one function's rows and then re-indexed the whole
borrow registry by bare name. `semlower.substitution` calls it once per
produced declaration — 1,403 times on `checker.fern` — so the index was built
1,403 times over a registry that grows with the module: 4.95% of the compile.
Nothing reads `borrow_bare` between those calls (its reader is
`irlower`'s lowering), so the loop now applies `caller_rows` and reindexes once
with `bare_indexed`. `caller_sigs` keeps its behaviour for a single call.

## Measured

Both changes together, `checker.fern` to a binary, three interleaved pairs at
`3ad7d6c`: **14.34 s to 13.35 s (-6.9%)**. Emitted asm is byte-identical.

## Left alone, and why

`without_rows` and `counted_rows` (1.45% and 2.6%) filter a whole list registry
per produced function, the same quadratic shape. Batching them is not a pure
refactor: two methods sharing a bare name (two `len`s) currently erase each
other's rows in turn, and a batched filter would keep both. That wants its
own decision. `ssasem.analyze` is also recomputed for one function by the
inference, the planner, `bare_flags` and `ssaunits.verify` (7,923 calls,
10.1%), which is the next item.
