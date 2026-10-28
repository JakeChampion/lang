# Archive Go proof fixtures

Issue #13732 records a package discovery failure on main `a3296b1a5694a7d456bb3d8d526d7bf381a09b8d`.
Three raw Go diagnostics under the wide arithmetic proof directory depend on their original harness package.
The Go build treated that artifact directory as a separate package and reported undefined symbols.
The gzip archives preserve all original bytes and SHA-256 values.

The archive manifest links each old path and Git blob to its new archive.
The original merged worktree and Git objects retain the uncompressed files.
No compiler, runtime, library, harness, mathematical criterion, reference row, baseline, threshold, or validation selector changes.

Current arithmetic checks passed on all three targets, plus the fresh Go oracle.
Each image suite passed 326 TAP tests with balanced zero-live censuses.
The full source gate remains open because the feature census exceeds its unchanged ceiling by two increments.
Issue #13733 records that separate repair.

The arithmetic helpers remain separate from the retained-machine model.
Actual CI recovery and all actual M5 and epic gates remain open.
