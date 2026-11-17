# A short drop loop waits for line buffers on a wide processor

2026-11-16. x86-64, one machine. No code changes. This entry records a
measurement and a decision.

## The shape

The drop loop of `2026-11-16-h` keeps no flag. rbtree-ck at 420,000 runs 12%
fewer instructions with it. But rbtree-ck at 4,200,000 took about 2% more
processor time than the build before it. The cache simulation and the branch
simulation of Cachegrind gave the same number of misses for the two builds.

## What the drop does in rbtree-ck

The program keeps one tree in five and releases the kept trees at the end.
A kept tree shares most of its nodes with the tree after it. Callgrind at
420,000 counted the paths of the helper of `Tree`:

- 84,000 calls take apart 2,093,258 nodes. That is 25 nodes for each call.
- 2,009,258 of those nodes have a left child with one count and a right
  child that another tree shares.
- No node waits on the stack of the drop.

Thus the loop goes down one path. For each node it lowers the count of one
shared child, frees the node and continues with the other child. The shared
child is cold memory. The path is a chain of loads, and the count updates do
not depend on each other.

## Measured

The machine is a Xeon at 2.10 GHz. Each core has 48 KiB of first-level data
cache and 2 MiB of second-level cache. Each value is processor time in
user mode, in milliseconds, with the runs of the binaries in turn.

The code of the two helpers was put in the same program. `as` and `ld` made
the program from the assembly of the new build. Only the helper of `Tree`
came from the old build. 30 runs:

| rbtree-ck 4,200,000 | best | first quartile | median |
|---|---:|---:|---:|
| old build | 1387 | 1476 | 1550 |
| new build | 1476 | 1532 | 1565 |
| new build, old helper | 1355 | 1443 | 1484 |

The same replacement in the other workloads, median of 20 or 30 runs:

| workload | old helper | new helper |
|---|---:|---:|
| deriv 10 | 934 | 876 |
| cfold 20 | 130 | 136 |
| nqueens 13 | 830 | 820 |
| rbtree 4,200,000 | 688 | 696 |

Only deriv and rbtree-ck move by more than the noise, and they move in
opposite directions.

A sampler stopped the process each 250 microseconds, four runs for each
helper. With the new helper, 5965 of 20,020 samples were in the helper and
the free routine. With the old helper, 4987 of 18,802 were. In the two helpers, the
samples are at three loads: the field of the node, the header of the child
with one count, and the header of the shared child.

The new helper runs 30 instructions for each node of this path. The old
helper runs 77. The new helper was then padded with `nop` instructions at
the end of each node. 24 runs:

| `nop` instructions for each node | first quartile | median |
|---:|---:|---:|
| 0 | 1479 | 1533 |
| 6 | 1449 | 1514 |
| 12 | 1458 | 1489 |
| 24 | 1365 | 1427 |
| 40 | 1380 | 1424 |
| 64 | 1374 | 1447 |
| old helper | 1381 | 1415 |

## The cause

The helper with 24 `nop` instructions does the same work as the helper with
none, and it is 7% faster. Thus the work of the new helper is not the cost. The cost is
the number of nodes that the processor has in flight at one time.

The container gives no hardware counter. Thus the next statements are an
inference from the test with `nop` instructions, not a measurement.

- The cache sizes match the Sapphire Rapids class. That class has a window
  of about 500 instructions and 16 fill buffers for the first-level data
  cache.
- With 30 instructions for each node, the window holds about 17 nodes. Each
  node has one slow miss for the count of its shared child. Thus the slow
  misses can fill all the buffers.
- The chain of the path also needs a buffer for each node. When the buffers
  are full, the chain waits, and each wait delays the start of the next slow
  miss.
- With 54 to 77 instructions for each node, the window holds 7 to 9 nodes.
  The chain then always finds a buffer.

## Not the cause

Each of these changes to the new helper gave the same time as the new helper:

- the start of the loop on a 32-byte boundary, and six different offsets of
  the full helper
- the test of the header of the shared child before the test of the other
  child
- the free of the node inline, with no call
- a call of `__fn___fern_box_dec.u` in place of `__fn___fern_box_free.u`
- a prefetch of the header and of the second line of the next node
- a second read of the header of the next node, and the two stores that the
  old helper made into the node

The early read of `2026-11-15-b` is thus not the cause.

## The decision

The helper does not change.

- The loss is 5% on one workload, on a processor with a window of this size.
  deriv gains 6% on the same processor.
- A processor with a smaller window holds fewer nodes of the short loop than
  its buffers permit. There the short loop gives more misses at one time,
  not fewer. The arm64 targets are in this group.
- A fixed number of `nop` instructions is correct for one window size only.

## Open

The count updates of the shared children do not depend on the path. A drop
can put each shared child on a list while it goes down the path. A second
loop then lowers the counts. The path then has no slow miss, and the second loop
has nothing but independent misses. This is not measured.

## How to repeat the comparison

- `fern -g` gives a symbol table, so a sampler can name the function of each
  sample.
- `fern -target x86-64-linux FILE` writes assembly that `as` and `ld` accept.
  A function of one build can replace the same function of another build in
  that text.
- Without a hardware counter, a time difference of 5% needs 30 runs for each
  binary, with the runs in turn.
