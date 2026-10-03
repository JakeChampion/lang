# 2026-10-02 — the stack verifier reads pop counts from a table

`ir.op_pops`, `irverifygate.Gate`, `checker.ow_call_may_borrow`,
`fnsigs.strarrfld_borrowed_elem_marks`. Refs #8171. No emitted byte
changes: the stage0-built compiler before and after emits the fixed older
tree (`examples/self_host/fern.fern` at 1ae9cad, its bindings spelled
`let`, against that tree's stdlib) and that tree's `checker.fern` byte for
byte, and the `selfhost-emit-hashes` sweep is 1,965 rows per compiler with
0 differing.

## What the profile named

- `ir.op_pops` answered from a chain of predicates, each an or-chain over
  kind tags, ending in the variadic kinds. `call_direct`, the commonest op
  the stack verifier meets, fell through every one of them: 1.33 G over
  2.2 M calls, about 600 instructions an op.
- `checker.ow_call_may_borrow` asked whether a callee is a declared struct,
  then whether it is a declared variant, each by scanning the module's ~600
  struct declarations: 1.40 G over 63 k calls. The second question is the
  first narrowed.
- `fnsigs.strarrfld_borrowed_elem_marks` walks every body twice and built
  each function's ownership frame for each walk (0.57 G), though the frames
  differ only in the store registry. Its node step rebuilt the accumulator
  box for every expression node, and the second walk asked a ~100-name list
  whether a callee owns a storing parameter, per node.

## What changed

- The fixed counts are `ir.kind_pops(t)`. `ir.pops_by_kind` tabulates them
  for every id below 1024 and `ir.op_pops_by` reads the table, then the
  immediate for the five variadic kinds, as `op_pops` does. `KindTable`
  carries the table, built once per module; the lift reads it there, and
  the verifiers' per-program inputs travel together as `irverifygate.Gate`
  (the callee-arity index and the table) in place of the bare index.
- `OwnFuncs.ctors` is a name index over every declared struct and variant;
  the result-borrow walk and the two owned-construction checks read it.
  `ow_struct_known` had no callers left.
- The borrowed-element scan keeps the first walk's frames and spreads the
  registry onto them for the second; the node step hands back the
  accumulator unchanged for a node that adds no row; the frame carries the
  storing functions as a name index, and its `stores` field, which nothing
  read, is gone.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 builds from each tree, emitting the fixed older tree to x86-64
asm text. "main" is 946c7c7e (#11149 on main at 7ddc44f9).

| | main | this change |
|---|--:|--:|
| total Ir | 145.96 G | 142.88 G (−2.1%) |
| `irverifygate.verify_problems`, inclusive | 2.29 G | 1.02 G |
| `irverifystack.verify_stack_fn`, inclusive | 1.76 G | 0.50 G |
| `checker.ow_result_borrows`, inclusive | 2.54 G | 1.22 G |
| `checker.ow_call_may_borrow`, inclusive | 1.40 G | 0.07 G |
| `fnsigs.strarrfld_borrowed_elem_marks`, inclusive | 1.95 G | 1.46 G |
| `fnsigs.strarr_own_node`, inclusive | 0.93 G | 0.72 G |

The trap: the first cut of the node step returned the accumulator early but
added a second `has_str` over the storing functions per call, and measured
0.99 G against main's 0.93 G until the list became an index.

## Witnessed

The checker, planner, semantic, closure, method and lift set of the earlier
entries together with every IR-verifier, lift-admission, string-array,
borrow, reclaim and forwarding test in `internal/e2eselfhost` (709 tests):
green apart from
`TestSelfHostSemanticProduction/a-string-boxed-through-an-impl-for-str-is-produced`,
which main fails from #11136 and #11149 fixes. `make check-sources`, the
lint ratchet, both emit identities and the sweep.

## Next

`semrecords.verify` re-checks each function's record table (about 31
records) for each of 12 k functions, 2.04 G, most of it `resolved` and
`concrete` per field. `checker.call_diags_binary` re-runs `check_expr` on
each operand at every level of a binary chain.
