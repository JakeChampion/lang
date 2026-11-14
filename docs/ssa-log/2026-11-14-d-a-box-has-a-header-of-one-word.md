# A box has a header of one word

2026-11-14. x86-64 and arm64. Follows
`2026-11-14-a-a-box-takes-no-spare-word.md`.

## The shape

Each block had a header of two words: the capacity, then the reference
count. The count uses 32 bits, so the high half of its word was free. A record,
a variant and a tuple do not grow, and the release is the only reader of their
capacity. A red-black `Node` took 64 bytes. Koka uses 48.

## The change

A box has a header of one word. The low half is the reference count. The high
half is the number of words of the box, which is never zero. An array and each
other block keep the header of two words, with zero in that high half.

The allocation:

- `lift_block_make` in `compiler/ssa_lift.fern` marks the `ARR_BOX` of a
  record, a variant and a tuple with `str == "box"`.
- A marked `ARR_BOX` calls `__fern_rec_box` with the number of words n of the
  box. The routine asks `__fern_arr_box` for a capacity of n - 2. That block
  has n + 1 words.
- The routine returns the address of the second word after the old header.
  It writes the count 1 and n in the word below that address.

The release:

- A count decrement of a value that can be a box calls `__fn___fern_box_dec`
  (`helper_symbol` on each backend).
- For a unique box the routine writes the capacity n - 2 in the header word.
  It writes a count of 1 in the first word of the box and adds 8 to the
  pointer. The block then has the header of an array, and
  `__fn___fern_arr_dec` takes it.
- Each other block goes to the array release unchanged.

The reuse test compares the high half of the header with the number of
words. `__fn___fern_alloc_reuse` also accepts a token that has the header of
an array with a capacity of one less.

The text of `__fn___fern_arr_dec` and `__fern_arr_box` does not change, so the
machine models of the two routines do not change.

A box of one word keeps the block of an array: a capacity of n - 2 does not
exist for it. A build that traces, profiles, quarantines or hardens each
allocation also keeps the old header (`compact_boxes`). The sanitizer is such
a build. A build with the leak census has the short header. The wasm backend
does not change.

## The count word of an array

The release reads the high half of the count word of each block that it gets.
On arm64, `__fern_arr_box` and two array routines wrote the count with a store
of 32 bits. A block from a free list then kept the bytes of its last use in
the high half. The release took such an array for a box. The compiler
built for arm64 stopped with `allocation size out of range`
(`TestSelfHostSSAPhysicalRCIRArm64`). Those four stores now write 64 bits, as
the x86-64 routines did.

## The models

- `compiler/ssaboxroutines.fern` accepts a new routine only when each line
  is the line that the file names. The model of the array routine below it
  must also accept that routine.
- `ssaconstructormachine` models the call of `__fern_rec_box`. The result is
  8 bytes into the block. The header word holds 1 and the number of words.
  `%rdi` holds the number of words at the return.
The other models:

- `ssasmallheap`, `ssasmallcallermachine`, `ssacreditorigin` and
  `ssacreditreplace` use the new routine names and a block of n + 1 words.
- `compiler/drivers/ssaboxroutinecontrols.fern` changes one line of a routine
  at a time, and each model must refuse each change.

## Measured

x86-64, this container. Best CPU time of seven runs, in milliseconds, and
peak resident memory:

| workload | before | after | Koka | Lean | memory before | memory after |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| rbtree 4,200,000 | 859 | 824 | 680 | 1857 | 260 MB | 226 MB |
| rbtree-ck 4,200,000 | 2577 | 2557 | 1680 | 3283 | 1840 MB | 1608 MB |
| deriv 10 | 1310 | 1176 | 868 | 1076 | 712 MB | 566 MB |
| cfold 20 | 214 | 164 | 222 | 230 | 198 MB | 184 MB |
| nqueens 13 | 810 | 766 | 825 | 1229 | 156 MB | 126 MB |

The Koka time of cfold is with no stack limit. At the default limit of 8 MB
the Koka program stops with a segmentation fault after 83 to 93 ms. An
earlier version of this table gave that time.

With no stack limit for each program, the best CPU times of seven runs are:
rbtree 765 against 676 for Koka, rbtree-ck 2452 against 1902, deriv 1310
against 915, cfold 186 against 222, and nqueens 887 against 818.

Cachegrind, with the small inputs:

| workload | Ir before | Ir after | D1 misses before | D1 misses after |
| --- | ---: | ---: | ---: | ---: |
| rbtree 420,000 | 704,430,500 | 712,829,992 | 1,324,520 | 1,106,834 |
| rbtree-ck 420,000 | 1,031,152,858 | 1,083,094,850 | 6,252,935 | 5,531,033 |
| deriv 8 | 104,788,598 | 114,073,720 | 814,622 | 644,392 |
| cfold 16 | 35,902,474 | 39,562,978 | 831,360 | 681,067 |
| nqueens 10 | 27,486,220 | 28,907,702 | 101,589 | 82,040 |

The instruction counts go up because each allocation and each release of a
box goes through one more routine. The cache misses go down by more than
that costs.

The compiler compiles itself with a peak of 2533 MB, down from 3035 MB. Stage
2 and stage 3 are the same file.

## Traps

- Give each program the same stack limit before a comparison, and read its
  exit status. `bench/perceus/COMPARISON.md` has the policy.
- On deriv 10, Fern runs 6.6 billion instructions and Koka runs 7.5 billion.
  Fern takes 33.0 million last-level cache misses and Koka takes 26.5
  million. An `Add` node has 32 bytes in Fern and 24 in Koka, because the
  shape has its own word. That word is the next gap.

- The two new routines are wrappers. An allocation path and a release path
  that write the short header directly can remove the added instructions.
  Such paths need their own machine models. The models of the array routines
  treat the high half of the count word as not known.

More traps:

- A block that was a box has a count of 1 in its first word after the release
  prepares it. Code that reads a released box sees that word, not the shape.
- `__fn___fern_box_dec` reads the high half of the header of each block that
  it gets. Each routine that makes a block for that release must write zero
  there, or a number of words. A string has a marker there, and a string does
  not go to that release.
- The sanitizer and the quarantine do not run the short header. The leak
  census and the machine models do.
