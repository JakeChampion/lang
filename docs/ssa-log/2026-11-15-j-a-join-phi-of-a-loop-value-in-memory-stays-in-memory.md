# A join phi of a loop value in memory stays in memory

2026-11-15. All native targets. Follows
`2026-11-15-i-a-call-of-the-function-itself-reads-the-bits-of-its-parameter.md`.

## The shape

The insert of rbtree-ck builds its result with tail recursion modulo cons.
The root of the result is a phi of the loop (`v428`), and the allocator puts
it in a spill slot. On each level, a phi of a join (`v494`) is the new root
on the first level and the old root on the other levels. Only the loop phi
reads it.

The join phi got a register. Thus each level loaded the root from its slot
into the register and stored it back to the slot on the edge of the loop:

```
mov -0x40(%rbp),%r15
...
mov %r15,-0x40(%rbp)
```

The root changes only on the first level.

## The change

After the linear scan, a phi in a register goes to memory when these
conditions are true (`spilled_phi_cycles`):

- Only phis read it, and each of those phis is in memory.
- One of those phis is also an operand of the phi.

The hints of the slots then put the phi in the slot of its mate. The edge
of the loop and the edge of each later level have no move. The first level
stores the new root in the slot.

## Measured

Cachegrind instructions, x86-64, scale 400,000:

| Workload | Before | After |
|---|---:|---:|
| rbtree | 553,317,911 | 550,057,803 (-0.6%) |
| rbtree-ck | 833,039,374 | 829,779,248 (-0.4%) |

deriv, nqueens and cfold give the same binaries. The compiler built with the
change builds itself again byte for byte.

## Tests

`ssa_cold_calls_run` has three new checks of `spilled_phi_cycles`:

- A join phi that only the loop phi in memory reads goes to memory.
- A join phi that a return reads keeps its register.
- A join phi of a loop phi in a register keeps its register.

The first check fails without the change.
