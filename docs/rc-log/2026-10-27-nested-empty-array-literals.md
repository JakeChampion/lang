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
The added source-order control and final broader qualification remain in progress.

The 2,000-node Go checker baseline passes without skips.
Before the final width-recursion change, all six existing direct diagnostic suites and both unchanged complexity checks also pass.
That combined run fails only its new numeric-generic control, so it is not an overall pass.
Final qualification must repeat the affected self-host checks after the last source change.

The two uncommitted ICC evaluator files remain byte-exact during this compiler repair.
No application type annotation or syntax change substitutes for the cause fix.
No Go checker, runtime, validation threshold, benchmark baseline, or existing assertion changes.
