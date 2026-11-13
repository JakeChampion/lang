# 2026-11-13 — a cons-tail loop carries the address of its hole

x86-64 and arm64. `ssaholeaddr.addresses`, run by `ssaprepare.with_selects`
after `ssatokenkeep.keeps`. Part of #15610.

## The shape

`ins` of the red-black tree workloads fills the left or the right child of
the node that the level before built. Its loop carries the box of that node
and the index of the hole (`docs/rc-log/2026-11-12-b`). Each fill tests the
box and then the index:

```
  b53:  brif (v266 == 0) b185 b186       v266: the box, v267: the index
  b186: brif (v267 == 1) b188 b189
  b188: st_at 16 v266 v404
  b189: st_at 40 v266 v404
  b187: ...
        br b41                           with v404 and the constant 1
```

A level ran the test of the index and a jump out of its arm. It also stored
the next index to a stack slot.

## What changed

`addresses` finds the two phis of such a loop. The index phi takes constants
only, and each use of it is the compare of one fill. Each use of the box phi
is a store of a fill or a null test.

The loop gets one phi for the address of the hole. Each edge into the loop
adds the offset that its index selects to its box. A fill is one store
through the address, and its two arms go away. A null test of the box
becomes a null test of the address. The two old phis have no use, and
`ssa.prune_dead` removes them.

```
  b53:  brif (v1300 == 0) b185 b186      v1300: the address
  b186: st_at 0 v1300 v404
  b187: ...
        v1302 = binary add v404 16
        br b41
```

The pass runs after the certifier. The address is a pointer into a box, and
the certifier counts boxes.

## Measured

x86-64, Callgrind instructions, against main at `04b4f337`. The answers and
the allocation censuses are equal on x86-64, and the answers on arm64 under
qemu. Stage 2 and stage 3 of the compiler are byte-identical.

| Workload | Size | main | this change | |
| --- | ---: | ---: | ---: | ---: |
| rbtree | 100,000 | 163,325,496 | 155,126,850 | -5.02% |
| rbtree-ck | 100,000 | 168,117,209 | 159,918,595 | -4.88% |
| deriv | 6 | 2,754,965 | 2,754,965 | 0 |
| nqueens | 8 | 1,165,486 | 1,165,486 | 0 |
| cfold | 12 | 2,182,468 | 2,182,468 | 0 |

## Not done

- A loop with three or more hole positions keeps its index. Its fills are a
  chain of tests, and this pass reads one test with two arms.
- The first level still tests the address against null. An address of a
  stack slot for the result can remove that test, and the register form has
  no such address.
