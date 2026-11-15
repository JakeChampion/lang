# A drop makes one call for each level

2026-11-15. All targets. The change is in the drop helper of a recursive
enum (`ssarc.drop_chain_fields`). It continues
`2026-11-15-b-a-drop-reads-the-next-node-first.md`.

## The shape

After the earlier change, the release phase of rbtree-ck at 4,200,000 took
1210 ms, and Koka took 940 ms. The two programs ran the same number of
instructions in that phase.

A sampler that stops the process each 0.5 ms put about 600 of 1900 samples
of the phase on the instruction after a return. The drop helper released a
field of its own type through the release helper. The release helper tests
the count and calls the drop helper again. Thus each level of a tree made
two calls.

A tree of rbtree-ck has 22 levels, which is 44 nested calls. The return
predictor of the processor holds about 16 return addresses. The processor
mispredicted most of the other returns. The free of Koka has no call for a
level: it keeps the way back in the nodes that it frees.

## The change

The helper releases a field of its own type inline. It tests the count, and
it calls itself only for a node with one count. A level is one call. A field
of a different type still goes through its release helper.

A hand-edited listing gave the result before the change to the compiler.
The edit put the test of the release helper in the place of the call. The
release phase went from 1226 ms to 994 ms.

## Measured

rbtree-ck 4,200,000 on x86-64. The release phase is the best of 8 runs, and
the run is the best processor time of 9 runs:

| | release and fold | run |
|---|---|---|
| before the two changes | 1371 ms | 2693 ms |
| the next node first | 1210 ms | 2568 ms |
| and one call for each level | 980 ms | 2237 ms |
| Koka | 940 ms | 2097 ms |

The helper has the test of the release helper inline, so it runs more
instructions. The instruction counts at the small sizes:

| | before | after |
|---|---|---|
| rbtree-ck 420,000 | 939.7 M | 942.0 M |
| deriv 8 | 101.6 M | 103.3 M |
| cfold 16 | 33.43 M | 33.77 M |
| rbtree 420,000 | 611.0 M | 611.0 M |

The times of deriv and cfold did not change.

## Not done

A tree with more than about 16 levels on one side still overflows the return
predictor. A drop that keeps the way back in the freed nodes has no calls.
It also needs no stack for a deep tree. That is a larger change to the
helper and to the certifier.

## Tests

`TestSelfHostDropReadsTheNextNodeFirst` now also checks that the drop helper
of `Tree` has no call of the release helper of `Tree`.
