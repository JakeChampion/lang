# View joins use the owner of projected storage

Issue #14867 records a valid byte-view join that the semantic verifier refused.
Each branch selected an array field from the same borrowed record.
The dependency graph named the branch-local field projections as storage roots.
Those projections did not dominate the join, although their common owner remained alive.

The storage-root walk now follows borrowed projections before it stops at an owned value.
These projections include copies, record fields, tuple fields, array elements, and variant payloads.
The dependency of the merged view names the retained owner.
Constructors, calls, and cell reads still establish their own roots.
The change adds no edge copies and preserves the strict verifier.

Runtime controls cover both branches, nested slices, records, tuples, arrays, variants, callbacks, and returned views.
They pass native x86-64 sanitizers with 6,400 balanced allocations and zero live bytes.
WASI execution and Android ARM64 shared-library compilation also pass.
A semantic control checks the exact owner dependency.
A negative control retains rejection of views over separate branch-local allocations.
The full CI verdict and native ARM64 execution remain separate checks.
