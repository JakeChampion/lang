# CI controls after liveness map reuse

Status: [record] two CI repairs from 2026-11-09.

PR #14874 CI run 42656602514 reported two failures outside the liveness suites.
Both failures reproduce on main at `97af82fcbe`.

The root-certificate test counted `0 as i64` inside nonzero timestamps that end in zero.
The test now reads complete numeric values before it counts zero dates.
The generator output and distrust policy stay fixed.
The roots-generator suite passes.

The new page-table lookup used `size = size >> ...`.
The compound assignment removes one modern-form finding.
The standard-library count returns from 388 to its ceiling of 387.
The ceiling stays fixed.
The module source check and format check pass.
The modern-form rule and ratchet tests pass with a fresh compiler in 48.731 seconds.
No case skips.

The repository owner merged PR #14874 at `3faced4627` before its full CI verdict completed.
This record does not report a green full suite for that head.
