# 2026-10-12 — a shared callee splices where its box dies

`seminline`: `grown_shared`, `returned_only`, `takes_apart`,
`returns_boxes`, `max_reuse_shared_insts`.

## The shape

```fern
Bin(l, nk, nv, r, s) => {
  ...
  if (c < 0) {
    let nl: OrdNode[K, V] = __om_insert(l, k, v);
    return __om_balance(nl, nk, nv, r);
  }
  ...
}
```

std/ordmap's insert takes a node apart and rebuilds it through
`__om_balance`, which both insert paths call. The node dies before the
call and a reuse token cannot cross one, so the node `__om_balance` built
was always a fresh box. `ordmap_insert` (4,000 keys, then every key
re-inserted 20 times into a map nothing else holds) allocated 890,828
times: every re-insert rebuilt its whole path in new boxes.

## The rule

The inliner splices a function called from up to two places into each
caller, keeping the original, when every return is a construction. Three
things kept `__om_balance` out:

- It returned its rotations' calls until the rotations, each called once,
  were spliced into it. Whether a function was shared was decided once,
  before those splices, so it was never shared. `grown_shared`
  reconsiders a rewritten body each round, as `grown_leaves` already did
  for leaves.
- `returns_boxes` asked every return to be a construction directly, not
  through the phi that joins one per arm, and counted only records,
  tuples other than pairs and nested constructions as boxes. A variant
  with payloads is the box a caller's dying node can be reused for.
- A shared callee was spliced only where its caller reads the result
  apart, which `__om_insert` does not. It is now also spliced where the
  caller returns the result and nothing else (`returned_only`) and takes
  apart a box of the type the callee returns (`takes_apart`): the caller
  rebuilds the box it took apart, through the callee, so the box can die
  where the callee's construction is built. Such a callee may be up to 500
  instructions (`__om_balance` is 421 with its rotations in it); one read
  apart keeps the 100 it had. Taking a box apart alone was too loose: a
  caller that only inspects a node it keeps also qualified, the splices
  cascaded, and pvec_with's code grew sixfold for nothing.

## Measured

`TestX86_64OrdmapReuse` (and arm64, wasm, interpreter) re-inserts every
key of a 500-key map R times, census balanced:

| | main | this change |
|---|--:|--:|
| unique map, 2 rounds | 12,874 | 987 |
| unique map, 8 rounds | 35,044 | 987 |
| map kept each round, 2 rounds | 13,378 | 1,989 |
| map kept each round, 8 rounds | 37,060 | 4,995 |

A map kept across each round is still copied, and keeps its values.

| benchmark allocations | main | this change |
|---|--:|--:|
| ordmap_insert | 890,828 | 7,984 |

ordmap_insert retires 24% fewer instructions on x86-64 and its code grows
38% (95% on wasm), the two copies of `__om_balance`. The other Perceus
and collection benchmarks allocate as before; map_int, map_probe_chain
and pmap_insert shrink about 1% in code. The compiler binary is 0.4%
smaller, it compiles itself in the same time, compiling `checker.fern`
allocates 0.03% fewer, stage 3 is byte-identical to stage 2, and a
sanitizer stage 1 compiles `checker.fern` clean.
