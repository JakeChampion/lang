# 2026-10-02 — the planner reads the live-out set it already has

`ssaunits.anchored_after`, `ownership.carried_values`,
`checker.Scope.binding_index`. Refs #8171. No emitted byte changes: the
stage0-built compiler before and after emits the fixed older tree
(`compiler/fern.fern` at 1ae9cad, against that tree's stdlib) and
that tree's `checker.fern` byte for byte, and the `selfhost-emit-hashes`
sweep is 1,965 rows per compiler with 0 differing, against the previous
entry's tree.

## What the profile named

- `ssaunits.anchored_after` asked, for every value of the function, whether
  it is live out of the block and then whether it reaches the root, once per
  candidate take per round of `payload_takes`: 41 k calls walking every
  value, 2.39 G, almost all of it `live_out_has` answering no.
- `ownership` hashed a call instruction's callee three times and its own
  function's name once per call: `same_cycle` found both rows, then
  `call_carried` found the callee's again, and a slot phi asked `consumed`,
  which found it once more: 2.1 M `find` calls, 0.90 G.
- `checker.check_ident_expr` scanned the scope for a name with `lookup`,
  built the miss's reason string, and on the function-value path scanned it
  again with `is_bound`: 1.4 M miss strings, 0.45 M second scans.

## What changed

- `anchored_after` walks `ssalive.live_out_ids` of the block, the set the
  flow already holds, and asks `reaches_root` of each.
- `carried_values` finds the function's own row once; `call_carried` and
  `slot_carried` find the callee's row once and hand both to `same_cycle`,
  which now takes rows. `consumed`, which only the slot phi asked, is gone;
  `consumed_at` stays.
- `Scope.binding_index` is the scan, returning the binding's index; `lookup`
  and `is_bound` read it, and `check_ident_expr` asks it once, reads the
  binding's type by index, and builds the miss reason only when it answers
  the miss.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 builds from each tree, emitting the fixed older tree to x86-64
asm text. Both rows are built from the previous entry's tree and this change
on top.

| | before | this change |
|---|--:|--:|
| total Ir | 155.51 G | 153.09 G (−1.6%) |
| `ssaunits.anchored_after`, inclusive | 2.39 G | 0.37 G |
| `ssaunits.payload_takes`, inclusive | 2.90 G | 0.89 G |
| `ownership.find`, inclusive | 0.90 G | 0.50 G |
| `checker.check_ident_expr`, inclusive | 1.12 G | 1.04 G |

## Witnessed

The checker, planner, semantic, closure, method and lift set (the three
name lists of 2026-10-02-q, -r and -s together; 621 tests, green),
`make check-sources`, the lint ratchet, both emit identities and the
sweep.

## Next

`lift.binding_param_is_fn` folds the whole function body for the `var`
declarations of one name, once per call argument the module table does not
answer: 103 k folds, 1.45 G. The fix is a declaration index built once per
function, but the enclosing declaration reaches `ilc_expr_at` through nine
signatures and `iife_scope_fd` rebuilds it inside an iife, so that is a
round of its own. `semrecords.find_union` compares the type of every enum
sharing a name, 4.1 M `equal` calls (0.6 G), most of them `Option` and
`Result` instantiations; `semrecords.verify` runs on every `analyze`,
2.07 G, with `resolved` finding each field's record by hash;
`ir.const_propagate` is 2.0 G and `parser.erase_view_body` 2.8 G, neither
yet read.
