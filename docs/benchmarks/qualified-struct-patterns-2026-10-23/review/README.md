# Review checks

The grammar now includes the existing opaque-struct modifier.
The differential gate derives all 2,403 parseable sources.
The new fixture has a census row.
A fresh run through the census trace helper measures zero unmatched allocations.
The census-row gate also passes.

The formatter parity case covers qualified records in parameters, let, for, if-let, and match expressions.
It passes against both formatters.
The marker parser now returns the struct-match flag with its other fields.
The checker no longer reads that marker prefix itself.

The qualified-pattern and complexity suites pass in 29.049 seconds.
All seven opaque-field refusals still report one E021 per checker.
The formatter parity case passes in 1.691 seconds.
A fresh bootstrap and complete size report will check the marker representation change.
