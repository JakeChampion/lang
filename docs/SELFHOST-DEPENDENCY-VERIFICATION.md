# Verify lifetime dependencies before ownership planning

Part of #8920's self-hosted typed-IR migration, following the dependency-aware
SSA liveness solver. A dependency can keep an actual container alive only if
that value exists and is available on the edge where the borrowed value is
used. A binding name, a former initializer or a sibling branch's value cannot
supply that obligation.

`ssadeps.fern` checks definitions and dependencies over the existing SSA graph.
It reuses `ssalive.input_error` for shape and edge validation, constructs
definition sites and reachable-block dominators, and checks ordinary operands,
return/condition operands and phi inputs at their exact predecessor edges.
Same-block definitions must precede their uses. Dependency sets must be
transitively complete and cannot contain self-dependencies or cycles.

Unreachable blocks still receive structural and definition checks; dominance
checks apply to reachable uses and executable predecessor edges. Invalid
parameter positions, duplicate/undefined definitions, misplaced phis and
unavailable dependencies are errors. Non-contiguous block ids and reordered
block storage remain valid.

The `analyze` entry verifies these obligations before computing live sets.
On rejection it returns an error and empty live matrices. It preserves the
graph and its operands. This is value-availability verification, not a proof
of exact semantic types, projection shape, reference counts or uniqueness.

## Executable coverage

The shared graph serializer supplies valid nested projections, branch-local
phi inputs, loop back edges and rebound values. Invalid cases cover incomplete
transitive roots, self/cyclic dependencies, wrong-edge anchors, a branch-local
root used after a join, a forward same-block dependency, duplicate definitions,
undefined operands and phis following ordinary instructions. Each case also
checks graph preservation and that a failed analysis exposes no live sets.

The bootstrap compiler builds and runs each case separately. The production
self-host CLI compiles the same verdict corpus together for x86-64, ARM64 and
Wasm, comparing every diagnostic and successful verdict. The original native
SSA liveness-oracle and cross-target suites remain part of the combined gate.

The combined suite passed in 119.658 s without skips. All 13 verifier cases
ran under bootstrap and in the self-compiled corpus on all three targets;
the original 20 liveness cases also passed. Lint-all and diff checks passed.
Full CI and current-head review remain pending before merging.

## Remaining ownership boundary

Production ownership lowering is not connected to this entry yet. Exact shared
semantic types and projection/effect contracts must be validated next, followed
by counted-unit planning and its independent verification. Only then can the
production pre-RC phase replace the corresponding AST ownership consumers.

The two existing enum-root leak tests remain unchanged merge blockers. Neither
this verifier nor liveness computation alone repairs the parent/child counting
contract. No optional compiler route or size-baseline change is introduced.
