The bare-frame fixture expected one trap stub, but the current array-box helper also requires its mandatory allocation-size abort stub.
This change requires exactly the existing string stub followed by the allocation-size stub.
It preserves every frameless-function, link, exit-134, and backtrace assertion.
Production emitters, runtime sources, standard-library sources, workloads, baselines, thresholds, and helper guards remain unchanged.

Validation:

- Clean main reproduces the original failure with zero skips.
- The focused correction passes with zero skips.
- The complete abort-test prefix passes 71 nodes with zero failures and zero skips.
- Missing, renamed, and duplicate stub controls each fail only the exact expected assertion.
- All 2,084 format inputs, Go build and vet, SourceLint's 219 nodes, and supporting workflow checks pass.

The diagnostic controls change only the inspected assembly view and preserve the linked assembly file and runtime code.
The final evidence-only commit preserves actual failures, control results, and source identities.
Actual CI recovery, the wider retained-machine proof, and all actual M5 and epic acceptance gates remain open.

Refs #13723.
