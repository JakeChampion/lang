# A shared arm loads the word that only a copy needs

2026-11-14. All backends: the pass is on the register form. Follows
`2026-11-14-a-a-box-takes-no-spare-word.md`.

## The shape

`ins` of `bench/perceus/rbtree.fern` matches a node and then tests that the
node is unique. The match loads each field of the node above the test. A
unique node keeps its value where it is (`ssatokenkeep.keeps`), so the path
with the box does not read the value. Only the copy of a shared node stores
it. The load still ran for each node on the path of each insert.

## The change

`shared_loads` in `compiler/ssatokenkeep.fern` moves such a load. The load is
a plain load of a word of a box that a uniqueness test reads. Each reader of
the load is in one of two places:

- the shared arm of a test of that box
- a side that allocates for the null token of that box

Each shared arm gets a copy of the load. A side that allocates reads the copy
through a phi in the block where the two arms meet. No store to the box can
run between the old load and an arm.

## Measured

x86-64, Cachegrind, against the commit before:

| workload | Ir before | Ir after |
| --- | ---: | ---: |
| rbtree 420,000 | 722,002,974 | 704,430,033 |
| rbtree-ck 420,000 | 1,031,152,392 | 1,031,152,391 |
| deriv 8 | 104,788,135 | 104,788,131 |
| cfold 16 | 35,902,007 | 35,902,007 |
| nqueens 10 | 27,485,754 | 27,485,753 |

rbtree runs 2.4% fewer instructions. The answers are equal, and stage 2 of
the compiler equals stage 3.

## Traps

- `ins` of `bench/perceus/rbtree_ck.fern` does not change. Its loads are
  already in the two arms of the test, and a phi carries the value. A pass
  for that form was tried and did not find the form in the five workloads.
- The pass runs for each function with a reuse allocation, also when the
  keep pass changes nothing.
