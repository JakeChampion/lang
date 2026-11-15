# A drop reads the next node first

2026-11-15. All targets. The change is in the drop helper of a recursive
enum (`ssarc.drop_variant_chain`).

## The shape

rbtree-ck at 4,200,000 keeps one tree in five. The program has two phases.
It builds the trees, and then it releases them. A sampler that stops the
process each 0.5 ms gave the time of each phase:

| | build | release and fold |
|---|---|---|
| Fern | 1130 ms | 1410 ms |
| Koka | 1180 ms | 990 ms |

Thus the full gap to Koka was in the release. The instruction counts did not
show it. The two release phases run the same number of instructions. The
cache simulation gives them the same number of misses.

The drop helper of `Tree` goes down the left field with a call. It
continues with the right field in a loop. It read the right field and the
count of the right node after the call for the left field returned.

One node in two of 40 bytes is on two cache lines. The right field is on
the second line. Thus the helper touched the first line of each node while
it went down. It touched the second line while it came back up. The read of
the second line came after a call, so the processor did not start it early.

## The change

The helper now reads the next node before it drops the other fields:

1. It loads the field that the loop continues with.
2. It tests the count of that node, and it keeps the result.
3. It drops the other fields.
4. It continues with the next node, or it releases that node, by the result
   that it kept.

The test in step 2 is a load of the header of the next node. The processor
starts that load before the calls of step 3. It also starts the load of the
second cache line of this node.

The result is safe to keep. The node that holds the field has one count.
Thus no other owner can add a count to the next node during step 3. Step 3 can
lower the count of the next node when the two fields hold the same node. In
that case the result says that the node has more than one count. Step 4
then releases it with the usual release, which frees it.

A variant that continues through the last element of an array keeps its
order. Its next node comes from the array after the other elements drop.

## The certifier

The helper keeps the result of the test in a local, and each variant with a
next node has its own test. Thus the word that the branch reads is a join of
tests, and each test reads a different box. The certifier accepted a join
only when each input tested the same box, and only eight joins deep.

`rccertify.unique_word` now has two more rules:

- A join of words that test different boxes tests the value that joins those
  boxes. The two joins must be in the same block, and each input of the word
  must test the matching input of the value (`joined_unique`).
- A join can be at any depth. A word that is its own input through other
  joins tests nothing.

An enum with 20 variants has 20 joins between a test and the branch, so the
compiler's own `ast.Stmt` needs the second rule.

## Measured

rbtree-ck 4,200,000 on x86-64, best of 8 runs:

| | build | release and fold | total |
|---|---|---|---|
| before | 1378 ms | 1357 ms | 2735 ms |
| after | 1186 ms | 1182 ms | 2367 ms |

The release phase is 13% shorter. Koka takes 925 ms to 990 ms for the same
phase on this machine. The build phase did not change: its two times are in
the noise of the machine.

The helper runs more instructions, because it keeps the result in a
register. The instruction counts at the small sizes, before and after:

| | before | after |
|---|---|---|
| rbtree-ck 420,000 | 922.9 M | 939.7 M |
| deriv 8 | 98.3 M | 101.6 M |
| cfold 16 | 33.07 M | 33.43 M |
| rbtree 420,000 | 611.0 M | 611.0 M |

The times of deriv and cfold did not change.

## Tests

`TestSelfHostDropReadsTheNextNodeFirst` checks the order in the emitted
helper and runs a program on each target. The program drops three shapes: a next node with one count, a next node
with many counts, and a node with the same subtree in two fields. A second enum continues through an array.
