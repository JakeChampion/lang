# Parameter phi labels are data

The tail-recursion pass stores the function name in each parameter phi.
The pair-return pass treated that label as a function reference.
It therefore retained return boxes for direct tail-recursive calls.
The reference scan now classifies phi labels as data.
Actual function references keep their current checks and ABI.

The new fixture calls direct and indirect tail-recursive functions 50 times each.
Before the fix, both functions return boxes and the fixture counts 100 allocations.
After the fix, only the indirect function returns boxes and the count is 50.
The regression fails before the fix and passes after it on all three targets.
The default, optimized and sanitizer-enabled modes give 18 runs with zero live bytes.

The complete pair-return suite passes 27 cases in 39.697 seconds.
The scalar-record return suites pass in 20.409 seconds.
The primary suite completed before the compiler self-reproduction check.
The private candidate produces byte-identical stage-3 and stage-4 compilers.

The full fixed checker compile uses two stage-2 compilers from one stage-1 producer.
Compared with the prior LICM slice, instructions decrease from 25,261,399,930 to 25,239,904,272, or 0.085093%.
Calls to `__fern_arr_box` decrease from 20,526,860 to 20,516,468.
The generated private ABI changes, so the correctness gates are the e2e suite and compiler self-reproduction.

The [evidence](../benchmarks/pair-return-phi-labels-2026-10-27/README.md) records the frozen sources, producers, raw profiles, diagnostic probes and all regression modes.
This slice does not complete issue #4412.
