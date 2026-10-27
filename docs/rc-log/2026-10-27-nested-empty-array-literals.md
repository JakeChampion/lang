# Nested empty array inference and diagnostics

Issue: #13666, a compiler prerequisite for #13665.

Clean main accepts and interprets `for xs in [[], [1]]`.
Its typed emitter refuses the same program with `unresolved array literal type`.
Reversed order, a written outer type, and an explicit argument context pass.
The first empty literal leaves an unresolved element type in the expression query.
The array preference rule does not adopt a compatible concrete type from a later element.

Mixed-element diagnostics also stop at the first unresolved element.
This lets incompatible nested arrays pass the checker and reach a later lowering refusal.
The repair chooses a concrete diagnostic anchor and checks every other element against it.
This anchor change affects diagnostic analysis only.
The original AST order remains the order for source evaluation and emission.

Nested comparisons require literal-aware checks rather than a shallow type-name comparison.
The diagnostic path now checks actual array children, view requirements, nominal arguments, and the existing numeric literal settlement rules.
Different generic arguments cannot pass only because their outer constructor names match.
The existing wide-literal pass now descends through array literals, so valid nested numeric-generic contexts retain their selected width.

The regression matrix checks empty-first order, nested depths, dynamic owned strings, generic records, numeric widths, explicit contexts, and source side effects.
Negative controls compare the complete diagnostic-code sequence with the unchanged Go oracle.
They include mixed primitives, nominal/generic records, array-depth mismatches, and ownership/view mismatches.
Compiled positive cases require nonzero balanced heap censuses and zero live blocks on native, Wasm, and ARM64/QEMU10.

The initial reduced matrix fails on main with 12 passing nodes, 42 failing nodes, and no skips.
The first repaired matrix passes 54 nodes.
Expanded generic controls expose permissive constructor settlement and missing recursion in wide-literal settlement.
Those failed observations remain separate from later passes.
The corrected ten-positive/seven-negative matrix passes 70 nodes without skips.
The added source-order control passes on all three compiled targets.

The 2,000-node Go checker baseline passes without skips.
Before the final width-recursion change, all six existing direct diagnostic suites and both unchanged complexity checks also pass.
That combined run fails only its new numeric-generic control, so it is not an overall pass.
Final qualification repeats the affected self-host checks after the last source change.

## Final qualification

Committed source `b38cca91520de044f7ebf81f7ea6523d1af38a12` passes 5,002 test nodes without failures or skips.
This run includes the final 74-node matrix, all six direct diagnostic suites, code-sequence checks, generic/partial enum controls, and both complexity checks.
The matrix covers 33 positive compiled executions with nonzero balanced heaps and zero live blocks.
Its 21 negative compiled cases match the complete Go diagnostic-code sequence.
The Go checker baseline separately passes 2,000 nodes without skips.

The clean standard two-stage build completes with actual make/version exit zero and a passing stage1 smoke test.
The CLI SHA-256 is `d12b97d792d5d66d6bd312cd6723100ecb85ec2609d9430705032e504e5c090b`.
The raw compiler SHA-256 is `5e13559941f9bafc791e5a7f1d295638b8661fa20b590ae0e57f542b7c6501a0`.
Compiler and stdlib source hashes remain unchanged through the build.

The unchanged default allocation gate reports 35,051,784 allocation and free events against baseline 34,951,426, a +0.29% change.
Every reported metric remains within the existing tolerance.
No RSS row appears because the environment has no delegated scratch cgroup.
These event counts do not establish a physical/process memory bound or runtime performance.

Full native/Wasm image scripts and 34 standalone ARM64 executables pass 242 TAP cases per target.
Their 73/49/34 censuses are balanced with zero live blocks.
All selected source and compiler hashes remain unchanged during these runs.
Standalone ARM execution does not qualify the whole Go ARM harness.
All 2,045 eligible Fern format checks, build, vet, lint, test names, CI self-tests, runtime bundle, protocol controls, and STE checks pass.

[The evidence directory](../benchmarks/nested-empty-arrays-2026-10-27/) preserves build, diagnostics, allocation, regression, and failure observations.
Intermediate failures remain failures and do not contribute to the final pass count.

The two uncommitted ICC evaluator files remain byte-exact during this compiler repair.
No application type annotation or syntax change substitutes for the cause fix.
No Go checker, runtime, validation threshold, benchmark baseline, or existing assertion changes.
