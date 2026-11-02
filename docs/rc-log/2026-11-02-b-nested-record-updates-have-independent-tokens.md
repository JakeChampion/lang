# Nested record updates have independent tokens

Issue #12795 identifies two costs in nested record updates.
An outer keep blocks an inner keep when both use one token slot.
A projection after a loop can also preserve a decoder reference and force a copy on each step.

The lowering now assigns a slot to each simultaneous keep interval.
The region retains the planner row identity for each donor and field witness.
Ordinary and classified rows admit independent overlapping identities.
Tested rows retain their extra guards, and the concrete ownership certifier remains strict.
Carried boxes stay in their donor locals and use a separate transient slot when necessary.

The sink pass recognizes record update bases as well as array update bases.
It preserves the earlier record projection when the later update replaces a consumed descendant.
Reads of another record field do not force a projection after the loop.
The array guard, dominance proof, and descendant-spend proof remain intact.

The regression fixture has a connection with 25 fields, including seven counted fields.
It checks nested, decoder-first, scalar-only, direct, constant-loop, and runtime-loop updates with unique and shared inputs.
It validates the old shared snapshot and requires constant unique allocations at both loop scales.
Separate drivers check interval assignment, rejected collisions, and donor and field identities.

The earlier private ARM64 profiles show that nested shape A matches direct shapes C and H after the repair.
At 10,000 steps, each records 902,159 instructions inside the step function.
The cross-block shape P changes from 10,002 allocations to three.
These earlier measurements retain their original source scope.
Current-main qualification and full CI must pass before merge.
