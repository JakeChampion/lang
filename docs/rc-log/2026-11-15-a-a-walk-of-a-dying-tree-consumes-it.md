# A walk of a dying tree consumes it

2026-11-15. All targets. The change is in the ownership inference.

## The shape

On rbtree 420,000, Fern took 913,397 last-level cache misses and Koka took
396,409. The tree has 20 MB in Fern, which is 315,000 cache lines. Fern
touched each line three times: when it built the tree, when it folded the
tree, and when it released the tree. Koka touched each line twice.

`fold` returns a number, so it is a quiet cycle. The inference keeps the
parameter of a quiet cycle borrowed. A count on a tree that the caller keeps
has a cost on each node. Thus `main` owned the tree after the fold, and
released it with a second walk of cold memory. The Koka `fold` owns its tree
and releases each node after it reads the fields.

## The change

A quiet slot of a cycle is eager while each call in the module gives it a
value that dies at the call. An eager slot is consumed.

- `ownership.Escapes.eager` has one flag for each slot. `quiet_cycles` sets
  the flag of each quiet slot of a cycle.
- `ownership.kept_alive` reads the calls and the loop slots of one function.
  It returns each eager slot that gets a value with a later use, or a value
  that the function does not own (`owned_root`).
- `semsource.without_kept_alive` clears those flags in the fixpoint of
  `infer_rows`. The functions that read a changed row run again, and so does
  the function of the row.
- `quiet_slot` is false for an eager slot, so the rules of a consumed slot
  apply to it with no other change.

The flags only go from set to clear, so the fixpoint stops.

A function with a call that keeps its argument stays as it was. In cfold,
`eval(e)` runs one time on an expression that `main` reads again, so `eval`
borrows.

## Measured

Cachegrind, x86-64, with the small inputs:

| workload | Ir before | Ir after | LL misses before | LL misses after |
| --- | ---: | ---: | ---: | ---: |
| rbtree 420,000 | 635,643,778 | 628,293,768 | 913,397 | 598,082 |
| rbtree-ck 420,000 | 1,003,441,765 | 996,091,757 | 4,767,709 | 4,258,319 |
| deriv 8 | 112,697,949 | 112,697,949 | 400,800 | 400,800 |
| cfold 16 | 38,884,681 | 38,884,681 | 410,481 | 410,481 |
| nqueens 10 | 27,816,528 | 27,816,528 | 12,215 | 12,215 |

Best CPU time of seven runs, with no stack limit: rbtree 4,200,000 goes from
967 ms to 922 ms, and Koka takes 829 ms. rbtree-ck goes from 2974 ms to
2870 ms, and Koka takes 2060 ms.

Each of the 40 programs of `bench/` runs the same number of instructions or
fewer: `perceus_rbtree` 2.5% fewer, `perceus_rbtree_ck` 1.7% fewer and
`map_string` 0.6% fewer. Each of the 626 programs of `tests/stdlib` gives the
same output. Stage 2 and stage 3 of the compiler are the same file.

## Traps

- A specialised copy of a function keeps the name of the original in its
  loop slots. `owned_root` reads the row that the slot names, not the row of
  the copy.
- The argument of the fold in `main` is a phi, because the loop that builds
  the tree is inlined. A phi is owned when each of its other arguments is
  owned.
- `docs/rc-log/2026-09-21-owned-by-default-alone-is-a-regression.md` has the
  cost of a count on a tree that the caller keeps. The rule here needs each
  call to give a dying value for that reason.

What stays open:

- One function with a dying call and a call that keeps its argument borrows
  for both. A copy of the function for each mode can serve the two calls.
- A `Node` has 48 bytes and the Koka node has 32. Fern takes 598,082
  last-level misses on rbtree and Koka takes 396,409, which is the ratio of
  the two sizes.
