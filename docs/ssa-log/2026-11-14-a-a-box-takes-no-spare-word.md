# A box takes no spare word

2026-11-14. x86-64 and arm64. Follows
`2026-11-13-g-the-arena-asks-for-huge-pages.md`.

## The shape

`__fern_arr_box(n)` makes a block of 24 + 8n bytes: the capacity, the
reference count, the word of an array length, and n words. A record, a
variant and a tuple put their first word in the word of the array length. But
they asked for n equal to their number of words, so each block had one word
that nothing read or wrote. An `Add` of two fields took 48 bytes and a red-black
`Node` took 72. Koka uses 24 and 48.

## The change

A box asks for one word less than its number of words.

- `lift_prod_make` in `compiler/ssa_lift.fern`: a record of k fields asks
  for k, and a tuple of k elements asks for k - 1.
- `__fn___fern_alloc_reuse` and the inline reuse test, on both backends: the
  caller still gives the number of words of the box. The entry compares the
  capacity of the token with one less, and asks `__fern_arr_box` for one
  less.

The free side reads the capacity from the block, so it needs no change. The
wasm backend sizes a box in bytes and had no spare word.

The machine models that give a box capacity now use the new value: 1 and 2 for
the two test records, with blocks of 32 and 40 bytes.

## Measured

x86-64, this container. Best CPU time of seven runs, in milliseconds, and
peak resident memory:

| workload | before | after | Koka | Lean | memory before | memory after |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| rbtree 4,200,000 | 877 | 874 | 598 | 1763 | 292 MB | 260 MB |
| rbtree-ck 4,200,000 | 2399 | 2418 | 1865 | 2889 | 2074 MB | 1840 MB |
| deriv 10 | 1524 | 1182 | 886 | 1160 | 858 MB | 712 MB |
| cfold 20 | 287 | 241 | 222 | 231 | 212 MB | 196 MB |
| nqueens 13 | 904 | 844 | 754 | 1166 | 188 MB | 156 MB |

The Koka time of cfold is with no stack limit. At the default limit of 8 MB
the Koka program stops with a segmentation fault after 83 to 93 ms. An
earlier version of this table gave that time.

Cachegrind at 420,000 inserts: the D1 misses of rbtree-ck go from 6,918,165
to 6,252,933. Those of rbtree go from 1,421,807 to 1,324,771. The instruction
counts do not change.

The compiler compiles itself with a peak of 3035 MB, down from 3152 MB. Stage
2 and stage 3 are the same file.

## Traps

- The times of one run on this container move by 20% or more. Use the best
  of several runs.
- cfold keeps 128 MB of stack: `cfold` has a frame of 128 bytes and recurses
  1,000,000 deep. That is the next gap on that workload.
- The header still has 16 bytes. The reference count uses 4 of them.
