# Mandatory stubs in the bare-frame fixture

Fresh main failed the bare-frame test because its stub assertion expected only the string abort stub.
The current array-box helper also requires the allocation-size abort stub for its mandatory size guard.
The old stub assertion was the only reported failure.
The unchanged frameless-function, link, exit-134, and backtrace checks still executed.

The fixture now requires exactly `__fern_str_oob_abort.l1:` followed by `__fern_alloc_size_abort.c:`.
An arbitrary second stub or larger count does not satisfy the assertion.
Every original frame and runtime assertion remains unchanged.
The production emitters, runtime, standard library, helper guard, workload, baselines, and thresholds remain unchanged.

The focused candidate passed with zero skips before the source commit.
Three diagnostic overlays require missing, renamed, and duplicate stubs to fail the exact stub assertion.
These overlays change only the inspected assembly view, not the linked assembly file or runtime code.
The complete existing abort-test prefix passed 71 nodes with zero skips after the source commit.

The [evidence record](../benchmarks/bare-frame-stubs-2026-10-27/README.md) preserves the original failure and all three required control failures.

Issue #13723 tracks this fixture repair under #13687.
Actual CI recovery and the wider retained-machine proof remain open.
All actual M5 and epic acceptance gates remain open.
