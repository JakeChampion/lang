# Four more workloads

2026-11-15. The benchmark harness, not the compiler. It continues
`2026-11-15-e-a-tree-walk-is-unrolled-once.md` for #16171.

## The workloads

The comparison has four more workloads, each with twins in Koka, Lean and
Roc and an answer that the harness checks. Their inputs come from one
generator of pseudo-random values, from the seed 42.

- `msort`: a merge sort of a list of a million values. The split puts the
  elements into two lists in turn. The merge is `Cons(x, merge(...))`.

- `qsort`: a quick sort of the same list. The head is the pivot. The two
  parts are sorted and appended.

- `treemap`: a tree of depth 20 with items, as in `binarytrees`. The run
  adds one to each item 20 times. A tree with one count can be rebuilt in
  place.

- `pmap`: a red-black map with a key of two words, compared in order. The
  run inserts a million keys, then looks up the same keys and a million
  other keys.

The answers of the two sorts come from a sort in Python. The answer of
`treemap` comes from a closed form: the sum of the items of a tree is
linear in the item of its root. The answer of `pmap` comes from a
dictionary.

## Measured

The harness at full size with 4 repeats in the balanced order, x86-64. Each
cell is the median processor time in milliseconds, with the best run in
brackets:

| workload | Fern | Koka | Lean | Roc |
|---|---|---|---|---|
| msort | 506 (477) | 538 (517) | 1856 (1813) | 875 (847) |
| qsort | 670 (627) | 679 (622) | 1851 (1705) | 1099 (1052) |
| treemap | 700 (669) | 723 (705) | 818 (764) | 1128 (1117) |
| pmap | 2354 (2172) | 3006 (2952) | 5904 (5797) | 3744 (3621) |

The peak resident memory in MB:

| workload | Fern | Koka | Lean | Roc |
|---|---|---|---|---|
| msort | 37 | 37 | 115 | 37 |
| qsort | 37 | 37 | 84 | 37 |
| treemap | 66 | 69 | 73 | 108 |
| pmap | 70 | 73 | 85 | 65 |

Fern is first on three workloads, and level with Koka on `qsort`. The
census of each Fern run is balanced, with no live bytes at the exit.

## Tests

`test_bench_perceus.py` checks each oracle against a known output at a
small size, and checks that each workload has a Roc twin.
