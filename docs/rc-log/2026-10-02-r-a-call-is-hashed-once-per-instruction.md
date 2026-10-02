# 2026-10-02 — a call is hashed once per instruction

`ownership.consumed_at`, `fnsigs.strarr_visible_producers`. Refs #8171. No emitted byte changes: the
stage0-built compiler before and after emits the fixed older tree
(`examples/self_host/fern.fern` at 1ae9cad) and `checker.fern` byte for
byte, and the `selfhost-emit-hashes` sweep is 1,965 rows per compiler with
0 differing, against the previous entry's tree.

## What the profile named

- `ownership.consumed` found a call's row by name, hashing the name, once
  per argument of the call: `call_carried`, `lent_values` and the two
  `semsource.inferred_*` rewrites asked it in a loop over the arguments.
  1.8 M lookups, 1.49 G in `find`, most of it `util.hash_bucket`.
- `fnsigs.strarr_visible_producers` filtered the whole-program producer
  list against a function's bound names and parameters, per function and
  per producer: 3.3 M checks over 32 k functions, 0.81 G, though almost no
  function binds a producer's name.

## What changed

- `consumed_at` is `consumed` for a row `find` already answered; the four
  loops find the row once and ask it per argument.
- `strarr_visible_producers` takes the producers' index and hands the list
  back unchanged when no bound name or parameter is a producer, which is
  the common case; its two callers index the producers once.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 builds from each tree, emitting the fixed older tree to x86-64
asm text. Both rows are built from the previous entry's tree and this change
on top. The change as measured also rolled `checker.sig_bucket_from` four
bytes a step (1.53 G to 1.45 G of the total); #11124 replaced that hash
with a one-byte loop the bounds-check elision takes before this landed, so
that part is not in the commit.

| | before | this change |
|---|--:|--:|
| total Ir | 168.22 G | 166.90 G (−0.8%) |
| `ownership.find`, inclusive | 1.49 G | 0.90 G |
| `ownership.call_carried`, inclusive | 1.15 G | 0.99 G |
| `fnsigs.strarr_visible_producers`, inclusive | 0.81 G | 0.21 G |
| `fnsigs.strarrfld_borrowed_elem_marks`, inclusive | 2.39 G | 1.96 G |

## Witnessed

The checker, borrow, planner and semantic set (`TestSelfHostChecker*`,
`TestSelfHostSemantic*`, the own diagnostics tests, `Bracket*`, `Grow*`,
`Lent*`, `Handback*`, `FieldAppend*`, `InPlace*`, `ArrPushCliff*`,
`SharedCount*`, `TestSelfHostSSALift*`, `TestSelfHostSSAUnits*`,
`TestSelfHostSSAPhysicalRC*`, `TestSelfHostAllocCountMatrixX86_64`,
`TestSelfHostIRPerModuleDriver`, `TestSelfHostFixtureSourcesCheck`,
`TestSelfHostFeatureCensus`, and every test naming string arrays, borrows,
reclaims or forwarders; 469 tests, green), `make check-sources`, the
lint ratchet, both emit identities and the sweep.

## Next

`checker.sig_name_bucket`'s callers: `UnionTable.variant_chain_head` asks
it 1.7 M times and `Scope.lookup_sig` and `has_sig` 2.3 M between them,
each hashing a name the caller often holds for several lookups.
`ownership.same_cycle` still hashes both names per call (1 M calls).
`fnsigs.push_str_unique` dedupes its accumulators by scan (0.99 G), the
`strarrfld_scan` family most of it, and `strarr_own_frame_of` builds a
frame twice per function (0.57 G). `checker.Scope.lookup` scans the flat
scope newest first, building an error string on each of its 1.4 M misses.
