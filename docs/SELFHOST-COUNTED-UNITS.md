# Counted units over typed semantic SSA

Part of #8920's self-hosted ownership migration. `ssaunits.fern` proposes and
independently checks an abstract reference-unit plan over `ssasem.Func`.
Explicit parameter modes distinguish scalar values, borrowed references and
counted references. A counted reference may share its allocation; no decision
in this pass establishes uniqueness or authorizes destructive reuse.

The supported unit types are owned strings, arrays, tuples and schema-resolved
records, recursively, plus scalar values. Views and opaque nominal, callable,
map and dynamic-object
contracts are rejected. The semantic verifier still checks exact types,
definitions, dominance and projections first. Calls and cleanup effects are
outside the current semantic vocabulary and cannot silently receive a plan.

Every reference-bearing record field is a stored occurrence, just like an
array or tuple element. A fresh record owns its own unit even when it was
constructed from a borrowed record's projections. The parameter's unit mode
does not determine the constructed value's ownership. Record field types are
checked across the finite schema table, so recursive nominal definitions do
not cause recursive compiler expansion. Physical lowering still refuses them.

## Proposed plan

Each reachable block has an entry step, a step for each ordinary instruction,
and a return or successor-edge step. Parameter definitions and phis are
handled by the block-entry invariant. The plan uses existing SSA value ids and
block ids; a step's point identifies an instruction position or an explicit
entry/return/edge point. Phi supplies identify their exact predecessor edge
and successor instruction position.

Each reference-bearing stored occurrence needs its own supply. When two
fields store the same value, the planner retains the earlier occurrence and
may transfer one existing counted unit into the last occurrence if that unit
is dead. Borrowed values always need an acquired supply. Every acquisition
precedes every transfer; then the semantic operation creates its result; then
drops execute. These phases are requirements for eventual physical lowering.

Backward liveness includes verified container dependencies. Returning a
borrowed child acquires its independent reference before releasing a dead
counted parent. Returning a dead counted value transfers its unit. Unused
parameters and phi units are dropped at entry; unused constructed results are
dropped after construction. Branch edges discard units that their successor
does not need, while each reference phi receives an independent incoming unit.

## Independent verification

The verifier reconstructs semantic analysis from the function and explicit
parameter contracts. It does not trust cached owned/live sets or the planner's
last-use decisions. It starts each reachable block with the counted units
required by its live-in values, phis and entry parameters, and replays reads,
supplies, transfers, creations and drops. Every edge must establish exactly
the successor invariant. This inductive check covers arbitrary loop iterations
without bounded path enumeration. Every return must supply its declared
reference result and leave no counted units behind.

The checker rejects missing, duplicate or extra steps, wrong supply values or
slots, invalid modes, repeated transfers, reading a moved value or a projection
after its anchor was dropped, drops without units, leaked exits and incorrect
edge invariants. A changed graph or parameter mode must be checked again.
The public planner returns a successful plan only after this replay succeeds;
a rejected plan exposes no steps.

Tests pin decisions for nested borrows, counted/borrowed parameters, repeated
stored occurrences, branch phis, reordered loop blocks and unused parameters.
Mutation tests require rejection of corrupt proposals, including changing a
parameter contract after planning. The same corpus runs with bootstrap and
the actual self-host CLI for ARM64, x86-64 and Wasm. This validates the abstract
protocol, not physical runtime RC or performance.

Replay separates supply validation against the original state, moves, result
creation and drops. Operation, return and edge checks then enforce their own
obligations without using the planner's last-use decisions. This preserves
diagnostics and keeps the combined stack within the repository's unchanged
complexity limit. Type admission accepts only the representations with defined
counted contracts, without a wildcard source arm.

Validation uses `scripts/devbox go test ./internal/e2eselfhost
-run '^TestSelfHostSSAUnits' -count=1 -v` for the unit-plan corpus and its
parameter-mode and unsupported-layout rejection checks. The same corpus is
compiled by the self-host CLI for each target. Repository checks are
`scripts/devbox make lint-all` and `scripts/devbox go test ./internal/lint`.
Full current-head CI and review remain required before merging.

## Remaining production work

No production lowering consumer is switched by this prerequisite. Verified
variant schemas and guard proofs, call/cleanup contracts, expanded physical RC
lowering and production typed-value import remain required before deleting
the replaced AST ownership analyses. A caller's signature alone cannot certify
the counted-return ABI of its callees; future call support needs closed-module
verification. No optional compiler route or size baseline change is introduced.
Main's two enum-return leaks were repaired by #8990 with exact heap-balance
assertions. Those regressions are also checked after integrating the parent.
