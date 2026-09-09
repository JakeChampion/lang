# Dependency-aware lifetime dataflow in Fern

Part of #8920's self-hosted typed-IR ownership migration. The remaining enum
payload-return failures require independent child references and correctly
timed parent releases. Historical freshness and escape markers cannot supply
that contract: a fresh local can be rebound to a borrowed parent, and a
non-borrowable parameter can still have a live caller.

`ssalive.fern` implements the dependency-aware SSA liveness algorithm already
used by the native semantic ownership pipeline. It consumes the existing
`ssa.SFunc` graph and explicit transitive dependencies indexed by value id.
It does not rebuild a graph from AST syntax or inspect emitted RC helper calls.

Each use of a projected value also uses its declared container dependencies.
This includes return/condition operands and phi inputs on their exact incoming
edges. Phi results are definitions in their own block. Backward dataflow grows
live sets monotonically to a fixed point, independent of block storage order.
Block positions index the result matrices; block ids may have gaps. Flat
scalar matrices hold the sets without allocating a nested row per block.

Malformed dimensions, value indices, phi arity and CFG edge metadata produce
an error. This is structural validation, not a semantic certificate. The
caller must verify typing, dominance, and availability of every transitive
dependency at every use. The graph and its operands remain unchanged.

## Executable oracle

The test harness constructs one graph for each case and serializes that same
graph and dependency map for the Fern solver. It compares every live-in and
live-out bit with native SSA `ComputeLivenessWithDependencies`. Serialization
maps native value ids to the self-host's zero-based ids and uses non-contiguous
block ids. Cases cover nested projections, branch-local phi inputs, loop back
edges with reversed block storage order, and a rebound binding's distinct
values. The solver must also preserve the graph's printed representation.

Tests execute the Fern module compiled by the bootstrap compiler and by the
production self-host CLI for x86-64, ARM64 and Wasm. Invalid-metadata cases
require rejection instead of quietly dropping a dependency. These are dataflow
correctness tests, not performance benchmarks.

On main `f1b98cea9`, all 20 cases passed without skips in 93.439 s: four
native-oracle graph cases, four invalid-input cases, and the four graph cases
self-compiled for each of x86-64, ARM64 and Wasm. Lint-all passed. The two
pre-existing enum-return failures were reproduced separately on the same main
in 15.844 s and remain merge blockers. Full CI and review are still required.

## Remaining production integration

This solver is a prerequisite, not an ownership cutover. Production lowering
does not yet consume its result. Checked semantic projection/effect metadata,
independent counted units on phi edges, acquire-before-transfer/drop ordering,
borrowed call anchors, counted-return obligations and independent unit
verification must be connected before RC lowering can use these live sets.

The corresponding AST ownership consumers must then be replaced and removed.
The two existing enum-root leak assertions remain unchanged; this module alone
does not fix them or complete #8874/#8920. No optional compiler route or size
baseline change is introduced.
