# A box call names its size

2026-11-16. x86-64 and arm64. The change is in the emitters and the runtime.

## The shape

Each call of `__fern_rec_box` gave the number of words of the box in a
register. That number was a constant at almost each call. The routine
then did these steps for each box:

- It tested that the number was in the range 1 to 255.
- It computed the address of the free list from the number.
- On x86-64, it saved and restored `%rcx`, because a light call changes only
  `%rax`.

These are the costs of the two box routines, as a part of all the
instructions, with Cachegrind on x86-64:

| workload | `__fern_rec_box` | `__fn___fern_box_dec.u` |
|---|---:|---:|
| nqueens 11 | 5.0% | 3.7% |
| deriv 8 | 8.2% | 6.1% |
| cfold 16 | 11.3% | 8.0% |
| rbtree-ck 420,000 | 4.6% | 4.0% |

## The change

A call that knows the number n, from 1 to 255, calls `__fern_rec_box_n`
(`asmcore.sized_rec_box`). That routine has the address of the free list and
the size of the block as constants. It takes the head of the free list, or
moves the bump pointer, and writes the header. A bump that leaves its 64 KiB
line, or the end of the heap, goes to `__fern_rec_box` with n. The output has
a routine only for each n that a call names.

- x86-64: the routine changes `%rax` and `%rdi`. The light call permits both,
  so the register allocator does not change. The call has no argument move.
- arm64: the routine changes x0 to x3, as `__fern_rec_box` does. Its bump uses
  x0 as the second scratch register, so it does not save x4 on the stack.

Three calls take the routine when the size is a constant:

- the block of a record, a variant or a tuple
- the allocation for a null token
- the fresh path of a reuse

A build that counts or profiles each allocation keeps the general routine. A
later need of the build can stop the direct routines. Then the routine of a
size only gives n to `__fern_rec_box`.

## The models

- `ssacapacityisa` decodes `call __fern_rec_box_n` for n from 1 to 255 in its
  canonical form.
- `ssaboxroutines.sized_allocation` takes the routine only when its lines are
  exactly the lines that it names, and when `__fern_rec_box` passes its own
  model.
- `ssaconstructormachine` takes a call of the routine of the size of the box
  in place of `__fern_rec_box` with that size in `%rdi`. After that call,
  `%rdi` is not known.
- The controls name the actual call of the constructor. A call of the routine
  of another size, a missing or changed routine, and a store of `%rdi` after
  the call all fail. A call of `__fern_rec_box` with the right size in `%rdi`
  still passes.

## Measured

Cachegrind on x86-64, the same answers before and after:

| workload | before | after | change |
|---|---:|---:|---:|
| nqueens 10 | 23,227,920 | 22,659,528 | -2.4% |
| deriv 8 | 107,269,596 | 103,477,888 | -3.5% |
| rbtree 420,000 | 578,975,134 | 575,618,860 | -0.6% |
| rbtree-ck 420,000 | 921,507,367 | 899,385,423 | -2.4% |
| cfold 16 | 32,670,459 | 31,189,815 | -4.5% |

The answers on arm64 under qemu are the same before and after.

Processor time on x86-64, with the runs of the two binaries in turn, best run
in milliseconds:

| workload | before | after | runs |
|---|---:|---:|---:|
| nqueens 13 | 942 | 903 | 21 |
| deriv 10 | 985 | 951 | 21 |
| cfold 20 | 136 | 135 | 15 |
| rbtree-ck 4,200,000 | 1828 | 1800 | 7 |
| rbtree 4,200,000 | 798 | 725 | 9 |

The noise of this machine is about 3% at the best run. Only rbtree moves by
more than the noise.

## Not done

`ssa.fresh_boxes` can change a null-token allocation of a constant size into
the block of a box. The constants of the token and of the size then die. That
version ran 1.7% fewer instructions on cfold 16.

But the linear scan then gave
the register of a loop value of `cfold` to another value. The loop then read
that value from its slot. cfold 20 took 10% more time at each alignment of the
code. Thus the emitter calls the routine of the size from the null-token
allocation. The allocator sees the same instructions as before.

## Open

`__fn___fern_box_dec.u` reads the tag again and tests the count again,
although the inline release tested the count before the call. A release that
knows the variant can give the block to its free list directly.

The null-token allocation still makes its token and its size, which nothing
reads after the change. Their removal waits for an allocator that keeps the
register of the loop value of `cfold`.
