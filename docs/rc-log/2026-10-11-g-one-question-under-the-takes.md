# 2026-10-11 — one question under the takes

`ssaunits.needed_past_slot`, `read_after_direct`. Refs #11910, the first
slice: the read-after walks the take rules kept apart.

## The shape

Three rules asked whether a box is still needed past a point other than
through the slot a read takes, and each had its own walk: the payload take
(`needed_past`, `reached_past_take`, `read_later_in_block`, `anchored_after`,
`reaches_root`, `read_reaches_root`), the tuple take (its own block scan and
value loop in `tuple_take_field`) and the field steal and grow
(`reaches_past_field` and the same two loops in `projection_root`). The
#11860 bug sat in exactly this seam: `named_after` read a phi as reading
every operand whichever edge entered, and only one of the three noticed.

The walks were one question with three allowances. `needed_past_slot` asks
it once: a later read of the box in the block (a read of another slot let
through, a second write to the same array slot counted), a value anchored to
the box that is used after the point and reaches it other than through the
taken slot, or a block that can run later naming it (`named_after`). The
allowances are its parameters, stated at each call: `strict` refuses any
liveness out of the block (the tuple take, whose box must die in the block
because its slot is nulled untested); `own_root` counts the instruction's
own operands, the box among them (the steal and grow, asked at a use of the
field, where a call handed the record beside its field reads the record
whole, and the field value used again later keeps the slot); `through_take`
lets a value through that reaches the box only via the taken value (the two
takes, which move the slot out). `reaches_root` is the one hop test under
them.

Beside `used_after`, the anchored read-after, `read_after_direct` is the
same question over the direct relation, the flow over `a.parents` alone,
which the hold rule's `dying_reads` asked inline. `docs/RC-DESIGN.md` §1.3
now states the model the rules share and which question each asks.

## Measured

`checker.fern` compiled by the compiler before and after emits byte-identical
x86-64 assembly: the three allowances reproduce the three walks exactly, so
the differences between them are now readable as three flags rather than
three functions. Whether the tuple take's `strict` and the steal's refusal
of a value reaching the box through the taken field are needed is a question
the flags make askable; neither was changed here. `ssaunits.fern` is 88
lines shorter.

## What is left

The rules that are not read-after questions keep their own walks: `sole_box`,
`carried_params`, `phi_ownership`, `anchored_to`, `only_element_reads`, the
grow table's fixpoint, and the reuse tables (#11909). #11910 stays open for
them.
