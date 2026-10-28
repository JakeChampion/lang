# Retain the boxed ABI in ownership proof fixtures

The record-return pass removes a box when every caller reads the result as fields.
Several ownership proof fixtures assumed a boxed result but gave every caller only field reads.
Their physical proof then refused the new return ABI before it reached the intended control.
The fixtures now pass the result to a separate whole-record consumer.
The ownership proofs retain their original boxed shape and rejection checks.

The typed storage driver also keeps the field-only case.
It requires an explicit refusal for that split-return ABI.
The runtime selection control now requires the additional consumer declaration by name.
The retained native wrapper uses the same whole-record boundary.

The separate retained-machine recovery remains open under #13717.
The legacy decoder and machine do not cover the current 16 GiB runtime guard.
A private prefix correction reaches the next decoder refusal, so a fixture change alone cannot repair that proof.
That diagnostic patch remains unadopted, and this repair preserves the legacy dependency control.

Two release probes used only constant arrays and now allocate no heap blocks.
The generic update probe now keeps a dynamic heap string across the update.
The field-argument probe now carries dynamic 64-bit array values and checks them after each return.
Both retain the nonzero allocation, balanced reference count and zero live-byte requirements.
The element-beside-array case now records 12 allocations instead of 44, because 32 result wrappers disappear.

The first typed-storage failure exits through its assertion helper, which bypasses normal cleanup.
Its leak report does not independently establish a reference-count defect.
The preserved before and after executions show the assertion failure and the corrected zero-live-byte run.

CI also exposed two separate compiler defects.
The Darwin guard-page handler used its context register before assignment, as recorded in #13736.
The x86-64 runtime did not export the heap-base symbol for per-module heap metrics, as recorded in #13737.
Both defects have execution regressions and production fixes in this PR.
The [repair evidence](../benchmarks/pair-return-ci-repair-2026-10-28/README.md) retains the raw failures, commands and validation logs.

The performance profiles retain their named source snapshots and common producers.
These correctness repairs do not supply a new compiler-speed measurement.
Native Darwin signal delivery still requires current-head macOS CI.
The architecture roadmap in #4412 remains incomplete.

The remaining CI logs exposed the same heap-base export defect on ARM64.
Both native runtimes now export the symbol, and both per-module counter executables pass.
The IR registry fixture now covers `prctl_query` in its name, constructor and exact census controls.
The operation keeps ID 483.

The branch classifier mistook a literal-pool barrier for a block with only a jump.
The classifier now recognizes the jump immediately before `.ltorg`.
Separate controls retain rejection of an actual block with only a jump.
The DEFLATE round trip passes on both native targets.

The playground omitted its selected target from the `FERNBUILD1` record, as recorded in #13739.
It now sets that field through the build switches.
The output comparison retains complete byte equality for each form.
