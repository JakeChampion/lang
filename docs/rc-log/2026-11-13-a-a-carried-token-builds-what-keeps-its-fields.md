# 2026-11-13 — a carried token builds what keeps its fields

All targets. `ssaunits.best_claim_of`, used by `served_by`. Part of #15610.

## The shape

`balance_left` of the red-black tree workloads is spliced into `ins`. Its
third arm builds two nodes:

```
Node(_, lx, kx, vx, rx) => Node(Black, Node(Red, lx, kx, vx, rx), k, v, r)
```

Two tokens reach the block. The token of the node that `ins` matched is
carried from an earlier block. That node owns `k`, `v` and `r`. The token of
the result of the recursive call dies in the block. That result owns `lx`,
`kx`, `vx` and `rx`.

`served_by` gave the carried token to the first construction of its slot
count in the block, which is the inner node. The local token then got the
outer node. Each node took a box that held nothing of it, so the block
stored ten fields.

## What changed

`best_claim_of` gives the carried token to the construction that keeps the
most fields of its donor: an operand that is a read of the donor at the slot
of the operand. The first construction stays the choice when no other one
keeps more.

The outer node now takes the carried token and keeps `k`, `v` and `r`. The
inner node takes the local token and keeps `lx`, `kx`, `vx` and `rx`.
`ssatokenkeep` then removes the stores of the kept fields on the side where
the token is the box.

## Measured

x86-64, Callgrind instructions, against main at `910ea591`. The answers are
equal on x86-64, on arm64 under qemu and on wasm32-wasi, and the allocation
censuses are equal. Stage 2 and stage 3 of the compiler are byte-identical.

| Workload | Size | main | this change | |
| --- | ---: | ---: | ---: | ---: |
| rbtree | 100,000 | 171,290,804 | 163,325,496 | -4.65% |
| rbtree-ck | 100,000 | 175,982,539 | 168,117,209 | -4.47% |
| deriv | 6 | 2,754,965 | 2,754,965 | 0 |
| nqueens | 8 | 1,165,486 | 1,165,486 | 0 |
| cfold | 12 | 2,182,468 | 2,182,468 | 0 |

The register form of `ins` has 86 stores in blocks that do not allocate. It
had 106.

## Traps

- `cfold` at depth 12 stops in wasmtime with a stack trap. It does the same
  with the compiler of main at `a509fb08`, so the trap is not from this
  change.
