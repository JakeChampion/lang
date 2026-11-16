# A tree walk is unrolled once

2026-11-15. All targets. The change is in the semantic inliner
(`seminline.unrolled_all`). The comparison gains a sixth workload.

## Binary trees

`bench/perceus/binarytrees.fern` is the binary-trees workload of the
Computer Language Benchmarks Game, in the form where each node holds an
item. The two children of a node take the items `2i - 1` and `2i`, so no
compiler can make the two subtrees one shared tree. Lean 4.34.1 does
exactly that with the form that has no item: `node (make d) (make d)` is
one call, and the run used 17 MB where a tree of depth 21 needs more than
100 MB.

The twins in Koka, Lean and Roc keep the same order of work. The harness
checks the output against a closed form: a tree of depth 0 with item `i`
checks to `i`, and a deeper tree checks to `i - 1`.

## The shape

At depth 21, Fern took 18.8 s and Koka took 16.5 s. The two ran the same
count of instructions. Fern made 27% more reads and writes of memory.

Half of the calls of `check` are for a `Nil` child. Each of those calls
saves five registers, tests the tag, calls the release of the static box
of `Nil`, and restores the registers. Koka compiles `check` as
`unroll_check`: its body is in each of its own two calls once, so a
`Nil` child is a test and no call.

## The change

`unrolled_all` splices the body of a function into each of its own calls
one time, when all of these conditions are true:

- The function calls itself two times.
- It returns a value with no count, so the change does not touch a
  function that builds a tree.
- It has 60 instructions or fewer, and no loop.
- Its entry ends with a branch, and one side gets to a return through
  jumps with no call.

The copies call the function, so each call is now for a child that has
children of its own. The sites are spliced from the last to the first,
because a splice keeps the blocks before its site where they are.

## Measured

Binary trees at depth 21, x86-64, best processor time of 3 runs:

| | time |
|---|---|
| Fern before | 18.78 s |
| `check` unrolled by hand in the source | 16.88 s |
| Fern after | 16.39 s |
| Koka | 16.58 s |

The harness at depth 21 with 4 repeats in the balanced order. Each cell is
the median processor time, with the best run in brackets:

| | Fern | Koka | Lean | Roc |
|---|---|---|---|---|
| time | 16.80 s (16.55 s) | 16.88 s (16.56 s) | 28.08 s (27.57 s) | 91.85 s (91.29 s) |
| peak memory | 258 MB | 263 MB | 265 MB | 427 MB |

Instructions of the other workloads at the small sizes:

| workload | before | after |
|---|---|---|
| rbtree 420,000 | 581.5 M | 579.0 M |
| rbtree-ck 420,000 | 901.8 M | 886.2 M |
| deriv 8 | 107.4 M | 103.8 M |
| cfold 16 | 34.42 M | 32.45 M |
| nqueens 10 | 24.09 M | 23.23 M |

The compiler is 63 KB smaller.

## Not done

A function that the change does not unroll still saves its registers
before its first test. A walk with a leaf case that returns at once can
test before the saves. A hand-edited listing of `check` gave 3% to 5%.

## Tests

`TestSelfHostRecursiveWalkIsUnrolledOnce` reads the listing of three
functions. A walk that consumes a tree calls itself four times, and so
does a walk that borrows a tree. A function that builds a tree keeps its
two calls. The program runs on three targets under `FERN_LEAKCHECK`.
