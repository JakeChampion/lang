# 2026-11-01 — the split pass folds the phis it makes

`seminline.split_constructions`, `drop_unread`. Refs #13990.

## The shape

```fern
function (s: Streams) __retire_done(i: i32): Streams {
  ...
  let cur: Streams = Streams { ...s, retired: s.retired + 1 };
  if (!cur.__is_local(st.id)) {
    let initial: i64 = cur.__initial_streams(t);
    cur = Streams { ...cur, closed: cur.closed.with(t, cur.closed[t] + 1) };
    if (...) {
      cur = Streams { ...cur, limits: ..., limits_dirty: ... };
    }
  }
  cur = Streams { ...cur, list: cur.list.with(i, cur.list[i].__closed()) };
  ...
  return cur;
}
```

With `__initial_streams` kept as a call, `cur` is lent to it whole. The first
update sits in the block that reads the fields of `s`, and reuses the box of
`s`. Each later update reuses the box of the one before it.

Spliced, `cur` is read apart only. The split pass replaces the update chain
with one value per field, and one phi per field at each join. Then
`drop_unread` removes the updates. Of the 30 phis at each of the two joins,
27 had the same value on every edge. The last update kept those phis as its
fields.

The sink pass read the `list` field of `s` again in each block before a
join. `Streams` has a `string` field, and with one the plan drops `s` on the
edges into the joins. No construction follows on those edges, and the carry
only moves a value that dies at an instruction. The last update built a fresh
box on every call. Without the `string` field, the plan drops `s` at the
re-read, and the carry serves the last update.

## Two changes

The split pass now folds the trivial phis after its rounds
(`ssa.prune_trivial_phis`). The fields of the last update are then the reads
of `s` itself. The `list` field is taken in the block of that update, and
the update reuses the box of `s`.

`drop_unread` also removes a chain of updates read only through one another
in one round. The update that survives keeps the base of its chain. Before,
the update that survived named a removed value as its base. Its base is now
the value at the root of the removed chain, through copies and phis. That
holds when every edge reaches the same root and the root is in scope. In
every other case the update has no base.

## Measured

- `TestSelfHostSplicedJoinRebuild`: 64 rounds allocate 3 boxes against 67
  before. The sanitized run is balanced on all three targets. The program
  splices three free functions with a branch into the method, because the
  one-block rule keeps a method with a branch as a call.
- With every method spliced, the serial probe of #13990 reads 47.7 a request
  against 48.7. The census row `streams__Streams.__retire_done` under
  `conn__Conn.__ack` read 1.00 a request and reads 0.
- In the default configuration the serial probe reads 46.8, unchanged. The
  instrumented compiler allocates 36,126,501 boxes on `checker.fern` against
  36,176,921 on main on this host, -0.14%.

## Next lead

With every method spliced, the serial probe reads 1 a request above the
one-block rule. The bisect of #13990 names `streams.Stream.__acked_send` and
`streams.Stream.__sent` at half each.

## Void-return correction

The first CI run at `7063559dbc` aborts during fractal, showcase and multicall
compilation. Semantic SSA uses return value `-1` for a void return.
`ssa.term_moves` used that sentinel as an index after the split pass pruned
a trivial phi. It now preserves a return without a value.

The new SSA driver checks phi removal, argument substitution, both void and
valued returns, and a nontrivial phi. The old code exits 134 on this driver.
The correction passes on ARM64 Darwin, native ARM64 Linux, x86 Linux through
QEMU, and Wasm. All four process censuses have nonzero balanced allocations
and zero live bytes. The Wasm sanitizer supplies a leak census only.

Fractal, showcase and multicall compile again. All 15 focused split,
ownership and certification tests pass with zero skips. The corrected
compiler reproduces itself byte for byte. Lint passes. Fresh full CI,
with the original performance limits, remains a prerequisite for merge.

## Tested-take shape correction

CI at `ea5d1abe85` fails the exact assembly count in
`TestSelfHostTestedTakeShapeX86_64`: `retire` has 9 uniqueness sites, versus
the expected 10. Its execution and balanced heap census still pass.
The split-phi correction lets the final `Tab` update reuse its donor after
the join. The former assembly has one fresh box site and one reuse site.
The corrected assembly has no fresh box site and two reuse sites.

The test now requires exactly 9 uniqueness sites and no fresh box site in
`retire`. Its `lock` count stays exactly 4. Unique and shared execution and
the heap census remain mandatory. Native ARM64 Darwin runs of the unchanged
fixture report 631 balanced events with the main compiler and 431 with the
corrected compiler. Both finish with zero live bytes.

The unchanged fixture also passes native ARM64 Linux, x86 Linux through QEMU,
and Wasm with 431 balanced events and zero live bytes on each target.
These finite controls do not establish a general allocation or timing bound.
