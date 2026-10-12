# 2026-10-12 — op kinds named by constants

`ir.K_*`, `parser.is_reserved_struct_name`. Refs #8171, #8258. No emitted
byte changes.

## What changed

- **Op kinds the compiler names are constants.** The compiler wrote 283
  `kind_id("name")` calls to get an op kind's id, in op constructors and in
  per-op predicates such as `is_foldable_binary_kind`. Each call is a length
  switch and a chain of string compares, and the whole compiler's
  self-compile made about 2.7 M of them. `ir.fern` now declares a `K_<KIND>`
  constant for each of the 65 kinds named that way. A const read compiles to
  an immediate. `TestSelfHostIRKindConstants` pins each constant to
  `testdata/ir-kind-ids.txt`. `kind_id` stays for names that are data, such as
  an SSA binary's operator string.
- **`is_reserved_struct_name` answers a lowercase name at once.** It built its
  29-name list on every call and scanned it: 163 K calls, about 70 M Ir. Every
  reserved name starts with an uppercase ASCII letter, which the parity test
  now checks.

## Measured

The whole compiler under callgrind. Both compilers compiled the same frozen
copy of the tree, each on a warm memo of its own.

| | Ir |
|---|--:|
| before (5d4f79c, the branch with #12155's changes) | 72.857 G |
| both changes, and main's #11910 commits | 71.350 G (−2.07%) |

Most of the drop is not these changes. By module:

| | change |
|---|--:|
| `ssaunits` | −1,190 M |
| `ir` | −271 M |
| `__fern_str_eq` | −61 M |
| `util` | −59 M |
| `ssa_lift` | −33 M |

The `ssaunits` share is `needed_past_slot` (−407 M), `reaches_root`
(−605 M) and `reads` (−386 M), and it is not this change. The after build
included main's #11910 commits, which landed between the two builds. One of
them, f3df35f, gave the analysis an index of the reads anchored to each value.
Before it, `needed_past_slot` asked `reaches_root` about every value of the
function: 17.2 M checks. After it, `needed_past_slot` asks only about the values
anchored to the root: 881 K. So about 0.45 G (0.6%) of this row is the
constants and the reserved-name check, and the `ssaunits` drop is #11910's.

## Then: one pass in `reaches_root`

`reaches_root` scanned each value's chain twice: `reads` to find the root,
then `first_hop` for the projection before it. One pass now finds both. On
top of the row above it measured 71.350 G → 71.327 G (−0.03%): with the
anchored index there are only 881 K checks. Before it, when `reaches_root` ran
on every value, its second scan was `reads`' 329 M. The RC plans for
`checker.fern` and `fern.fern` are unchanged.

## Then: an operator's width types each operand once

`semsource.operand_width` typed the whole operator through the checker, which
types both operands, and `address_width` typed each operand again, up to five
subtree walks per operator node. `checker.binary_types` gives all three types
from one typing of each operand. On top of the rows above: 71.327 G →
71.117 G (−0.29%), and the checker calls `semsource` makes fell from 337 M to
106 M. A chain still types each level's subtree once, so it stays quadratic,
with a constant about a third of what it was.

## Measured and not kept: calls typed from their annotated operands

`annotate_typed` is bottom-up for every node but a call, which it typed with
`check_expr` on the whole call. A `check_call_known` that read the receiver
and argument types the annotation already had removed that, so
`annotate_typed`'s `check_expr` calls (310 M) went away. Yet `annotate_block`
measured the same inclusive cost (2,594 M before and after). Every ordinary
`check_call_expr` also built the empty known record, an `unsettled` `Typed`
with its unknown type and reason string, and that added 118 M to
`check_module_pass`. The whole compile measured +0.17% (71.117 G →
71.241 G), so the change was dropped.

## Measured and not kept: each anchored read's first hop, computed once

The first hop of a read towards its root does not depend on the take, so it
can be computed with the anchored index rather than per check. With 881 K
checks over the 22 K questions that reach the loop, `needed_past_slot` fell by
34 M and `anchored_of` grew by 27 M. That is −0.01% on the whole compile, so the
change was dropped. Most of `needed_past_slot`'s 322 M inclusive cost is
outside the anchored loop.
