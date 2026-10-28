# Two-field records use the pair return path

The compiler profile identifies 233,920 box calls from `semsource.define` during one complete checker compile.
That function returns a record with a reference field and an integer field.
The pair-return pass accepted an equivalent tuple and excluded the record.
The pass now admits two-field records through its current part checks.
It reuses the current pair representation and extraction paths.

The physical contract metadata describes the reference units in both pair positions.
The scalar-lane rewrite applies only to eligible scalar records.
This preserves a reference second field when the primary result is a wide integer.
The certifier refused the first private prototype because it omitted these contract updates.

The new record fixture decreases allocations from 425 to 100 on all three targets.
Function-pointer and whole-record controls retain those 100 allocations.
Finalizer and view controls retain their allocations and execute all finalizers.
The heap-alias fixture decreases allocations from 100 to zero and retains its source aliases.
The default, optimized and sanitizer modes give 54 before and after runs with zero live bytes.
The self-compiled candidate repeats all 27 after cases with the same results.

The primary pair-return, scalar-record return, instrumented return and physical-contract suites pass in 146.020 seconds.
The candidate then produces byte-identical stage-3 and stage-4 compilers.
The supplementary heap-alias repository cases pass in 2.440 seconds.
Source checks, source lint, ratchets, vet and test-name checks pass.

The common-producer stage-2 comparison adds 0.011085% instructions and 6,048 box calls for the complete frozen checker compile.
It prices the new analysis without the new record ABI in the compiler's own machine code.
It does not establish a stage-2 compiler speed gain.

The separate bootstrap observation records 563,559 fewer boxes and 0.170265% fewer instructions than the prior self-reproduced compiler.
That observation uses a different producer, so it is not the primary A/B result.
The `semsource.define` site retains its 233,920 box calls.
The [evidence](../benchmarks/record-pair-returns-2026-10-27/README.md) retains the raw profiles, producer hashes, controls and validation logs.
This slice does not complete issue #4412.
