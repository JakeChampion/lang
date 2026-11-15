# A store takes an immediate

2026-11-15. x86-64. Follows
`2026-11-15-b-an-enum-with-no-field-is-its-index.md`.

## The shape

A constructor moved the tag of its shape to a register and then stored the
register in the header. A store of a constant field did the same. Each pair
was two instructions.

## The change

`ssa.imm_operands` has the parameter `stores`. With it, a constant that is
the value of a store of 4 or 8 bytes stays an immediate
(`ssa.stored_value`). The x86-64 backend sets it, and `ssa_mem_inst` writes
`movl $imm, off(base)` or `movq $imm, off(base)` (`ssa_mov_im`).

`ssa_shape_plan` keeps the tag of a shape as an immediate when a store takes
it. Before, only a compare could take it.

The arm64 backend does not set `stores`. It has no store of an immediate.

## The models

The constructor models read the text of a constructor:

- `ssacapacityisa.constructor_decode` accepts the store of an immediate.
- `ssaconstructorbound.shape_tag` reads the tag from that store.
- `ssaconstructormachine.finished` compared each register at the return.
  `%rsi` held the tag there. It now holds the second argument of a
  constructor of two fields, and its first value for a constructor of one.
- The controls of `ssaconstructorbounds`, `ssaconstructormachines`,
  `ssaconstructorisas` and `ssaallocationchains` change the new store.

## Measured

Cachegrind instruction counts, x86-64, with the small inputs:

| workload | before | after | change |
| --- | ---: | ---: | ---: |
| rbtree 420,000 | 628,293,768 | 604,841,163 | -3.7% |
| rbtree-ck 420,000 | 996,091,757 | 970,881,875 | -2.5% |
| deriv 8 | 112,697,949 | 112,155,504 | -0.5% |
| cfold 16 | 38,884,681 | 38,525,789 | -0.9% |
| nqueens 10 | 27,816,528 | 27,745,452 | -0.3% |

Koka runs 593,463,215 instructions on rbtree 420,000. The compiler binary
has 28,960,800 bytes, down from 29,054,272. Stage 2 and stage 3 are the same
file.

## Traps

- The constructor machine fixes the value of each register at the return. A
  change of the emitted text that frees a register changes that value.
- A control that names an instruction of the old text stops with
  `mutation present`. Read the text of the store from the output, because
  the tag is a number in it.
