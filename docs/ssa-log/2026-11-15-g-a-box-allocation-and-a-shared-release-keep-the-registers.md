# A box allocation and a shared release keep the registers

2026-11-15. x86-64, and the release on arm64 too. Follows
`2026-11-15-f-a-branch-compares-the-low-half.md`.

## The shape

The register allocator took each call as a call that changes each caller
register. A value that lived across a call took a saved register. When the
five saved registers of x86-64 were in use, it took a slot. In `ins` of
`bench/perceus/rbtree_ck.fern`, 20.4% of the instructions that ran were
reads and writes of slots. `k` and `v` lived in slots for the whole
function, because they lived across two calls:

- the allocation of the box of a new leaf, `__fern_rec_box`, which changes
  `%rax` alone since `2026-11-14-f-a-small-box-takes-its-block-directly.md`
- the release of a node that another tree holds, `__sem_release_Tree`, which
  only takes 1 from its count

## The change

Two new kinds of call in `ssa.cold_positions`:

- A light call (`ssa.COLD_LIGHT`): the block of a record, a variant or a
  tuple, and the allocation for a null token, which goes to the same
  routine. On x86-64 it changes `%rax`, and its argument moves write `%rsi`
  and `%rdi` (`LIGHT_CLOBBERS`). A value that lives across it keeps each
  other register, and the call saves nothing.
- An inline release (`ssa.inline_release`): `__fern_rc_dec`, and the helper
  release of a record or a variant, which `ssarc.drop_value` marks with
  `ir.box_release`. Each is a call that saves the registers it changes, as a
  call in a cold block does.

A build that counts allocations keeps the old kind for a box
(`direct_boxes`). arm64 keeps it too, because its bump routine changes `x4`
to `x15`.

The emitter tests the count of an inline release (`ssa_rc_dec`). A shared
box loses 1 from its count. Only a unique box calls the helper, with the
live registers saved around that call. The helper has no entry past its
tests, so the call goes to its `.r` entry.

## The models

- `ssaconstructormachine` took a constructor only when each register had its
  value of the entry at the return. A constructor now keeps its fields in
  `%r8` and `%r9` across `__fern_rec_box`. The model now reads `%rax` and the
  registers that a call keeps: `%rbx`, `%rsp`, `%rbp` and `%r12` to `%r15`.

The model of the routine:

- The model said that `__fern_rec_box` leaves its saved `%rdi` below the
  stack pointer. The direct path of the routine pushes `%rcx` there. The word
  is now not known, and a read of it makes a refusal.

The credit models and the controls:

- `ssa.plain_imm` takes the mark `ir.box_release` as no mark, so the credit
  models read a marked release as a release.
- The controls of `ssaconstructormachines` and `ssaallocationchains` find
  the stores of the fields in the text, and do not name their registers.

## Measured

x86-64, Cachegrind, with the small inputs, against `7b7b5597`:

| workload | Ir before | Ir after | change |
| --- | ---: | ---: | ---: |
| rbtree 420,000 | 611,039,623 | 589,861,436 | -3.5% |
| rbtree-ck 420,000 | 922,943,661 | 885,460,119 | -4.1% |
| deriv 8 | 98,346,228 | 98,237,545 | -0.1% |
| nqueens 10 | 23,645,628 | 23,610,750 | -0.1% |
| cfold 16 | 33,071,091 | 32,666,437 | -1.2% |

The D1 misses of rbtree-ck do not change: 3,626,919 before and 3,626,463
after. `ins` of rbtree-ck has 277 instructions that address the frame,
down from 307.

The times of the full inputs on this container moved by 20% from one run to
the next. The best of twelve runs of rbtree-ck went from 2488 ms to 2456 ms.
The answers are equal on x86-64 and on arm64 under qemu. Stage 2 of the
compiler equals stage 3.

## Traps

- The register numbers of the x86-64 allocator are not the machine numbers:
  0 is `%rax`, 1 is `%rsi` and 2 is `%rdi` (`ssa_caller_reg`). A mask with
  the bits of `%rcx` and `%rdx` built a `tr` that crashed.
- A copy of each spilled value at its first read between two calls did not
  help `ins`. The copies took registers that other values held, and 3 more
  values went to slots. The stretches between the calls of `ins` use each
  register already.

What stays open:

- arm64 can take the light call when its bump routine saves the registers
  that it changes.
- `k` and `v` of rbtree-ck still live in slots: the recursive call of `ins`
  and the release of a unique box change each register.
