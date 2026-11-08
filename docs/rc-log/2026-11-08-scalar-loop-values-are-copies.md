# Scalar loop values do not borrow resource handles

The return-borrow analysis marked every for-each element as borrowed.
A loop that appends copied bytes to an owned array then lost the freshness of its result.
Both checkers rejected that result as an argument to another owned parameter.
Issue #14746 records the minimal nested-call example.

The analysis now recognizes scalar elements from known array and view parameters.
A slice of the same iterable retains that fact.
The Go checker also recognizes scalar index results after for-each expansion introduces an internal iterable alias.
The self-host checker retains the original for-each form for this analysis.

Handle-bearing elements retain their borrowed status.
Tests preserve E051 for an array of records that hold readers.
The complete Go checker suite passes.
Go and self-host diagnostics agree on native and WASI checker drivers.
Native and WASI execution of the copied-byte example also pass.
