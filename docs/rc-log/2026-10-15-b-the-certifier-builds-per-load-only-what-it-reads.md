# 2026-10-15 — the certifier builds per load only what it reads

`rccertify.escape_fixpoint`, `escape_paths`, `stores_of`, `takes_of`.
Refs #12181. No emitted byte changes, no change to any verdict.

## The shape

Under callgrind, `escape_fixpoint` was the largest piece of `origins`:
6.1 G inclusive on a cold compile of the compiler. Each call allocated a
flag per value of the function for the candidate phis, which cost 1.18 G
in `__fern_alloc_u8` alone. Most loads have no candidate phi. Each
`escape_paths` call also recorded every edge it took, which only the
fixpoint over candidates reads.

Two other pieces were also built more than once:

- `stores_of` ran twice per walk, once in `origins` and once in
  `put_backs`.
- `taken_root` searched the blocks reached from the store's block once per
  null store, even when the block holds several.

## What changed

- With no candidate phi, `escape_fixpoint` keeps no flags, and
  `escape_paths` records no edges.
- `walk_of` builds one `Stores` and hands it to both `origins` and
  `put_backs`.
- `block_takes` finds the blocks reached from a block once for all of its
  null stores.

## Measured

One cold compile of `compiler/fern.fern` (`FERN_RC_CERTIFY_MEMO=0`) under
callgrind, on the same source tree:

| | Ir |
|---|--:|
| main | 144.244 G |
| this change | 143.091 G (−0.8%) |

Under both compilers, `FERN_RC_CERTIFY=report` prints the same 7,497 lines
on the whole compiler. The emitted binaries are identical.

**Measured and not landed:** an index per root of the blocks that read it,
and of the loads off it, for `used_after` and the take search. It replaced
a scan of every instruction on the dominator chain per null store. Building
the index's two tables, each the size of the function's values, for every
walk with a take cost more than the scans: 145.009 G against main's
144.396 G on the tree that held it (+0.4%).

## Pinned by

The search for the load a null store takes from had no case before this.
`TestSelfHostRcCertifyCases` gains two:

- `take-from-a-dominating-load`: the load is in a block that dominates the
  store's. Ignoring dominating blocks fails it.
- `take-for-the-load-read-after`: of two loads before the store, the older
  is the one a later block reads. Ignoring reads in later blocks fails it.

The shell cases cover the fixpoint with no candidate. The token-reuse cases
cover it with candidates.

## Next

The token walks are still most of the certifier: per token, `walk_of`,
`settle` and liveness all run again over the region the token reaches. A
2,667-token sample of the compiler showed every token's pair of walks
clears at least one slot in all but 12 cases, so skipping walks is not the
lever. The cost is the region work itself.
