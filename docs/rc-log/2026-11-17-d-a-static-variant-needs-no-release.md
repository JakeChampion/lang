# A static variant needs no release

2026-11-17. x86-64 and arm64. The release of a value that a variant test
fixes as a variant with no field is now no code.

## The shape

In `check` of binarytrees and in `bump` of treemap, the `Nil` arm released
its value. `Nil` has no field, so each construction of it is a constant
(`ssaunits.constants`), and the value is a static box with an immortal
count. The release tested that count and did nothing. It ran once for each
leaf.

## The change

- `ssaunits.empty_sites` finds the blocks that only one edge of a
  `variant_is` test enters, where that edge fixes the value as a variant with
  no field. `ssaunits.empty_variant` limits this to an enum of boxes with no
  finalizer that is not an immediate enum.
- `ssaunits.known_static` says that a block is one of these blocks, or that
  one of them dominates it. `ssaunits.static_on_edge` says the same of an
  edge, for a drop on the edge.

The lowering keeps the release, so the certifier, the credit checkers and
wasm see the release that they know. It marks the release
`ir.static_release` (`ssa.STATIC_RELEASE`). The x86-64 and arm64 emitters
write no code for it. wasm calls the helper, which keeps a static box.

Each construction of the variant must be the static box. `__send_copy` made
a box on the heap for such a variant, so `send_copy_deep` of the conformance
leak census leaked its copy of `Open`. The copy helper now gives back the
same static box (`send_static`).

The first form left the release out. The certifier then found a unit that
nothing released, for example in `interp__io_error_value`.

## Measured

Instructions on x86-64 under cachegrind, against main at `50625de8a8`,
before #16352. The output of each run is the same as main's.

| workload | #16371 | this change |
|---|---:|---:|
| binarytrees | -7.4% | -19.2% |
| treemap | -10.5% | -18.0% |
| msort | -1.0% | -2.9% |
| rbtree-ck | -1.2% | -2.1% |
| deriv | -2.0% | -2.0% |
| cfold | -1.8% | -1.8% |
| pmap | -0.3% | -0.5% |
| rbtree | -0.5% | +0.2% |
| qsort | +0.7% | +1.6% |

Processor time on x86-64, user time, median of alternating runs:

| workload | main | this change |
|---|---:|---:|
| binarytrees, depth 19 | 3216 ms | 2766 ms (-14%) |
| treemap, depth 20 | 638 ms | 691 ms (+8%) |
| msort, 1e6 | 771 ms | 782 ms |
| deriv, 9 | 130 ms | 127 ms |
| cfold, 20 | 130 ms | 131 ms |
| rbtree, 4.2e6 | 758 ms | 746 ms |
| rbtree-ck, 4.2e6 | 1614 ms | 1661 ms |
| qsort, 1e6 | 827 ms | 843 ms |

treemap does 18% fewer instructions, and its cache misses under cachegrind
are the same as main's. It still takes 8% more time. Remove only the call in
the `Nil` arm of `bump` from the listing of main, and that listing is 11%
slower than main. So the loss is not in this change.

The cause is probably in the front end of this Skylake-derived Xeon. The
option `-mbranches-within-32B-boundaries` does not change it. To move the
tag load above the stores does not change it either.

## Next

- Find what makes the shorter `bump` slower on this processor, for example
  with the counters of another machine.
- The register allocator gives a loaded value the register of another phi
  of the same edge, which makes a cycle in `append` of qsort.
