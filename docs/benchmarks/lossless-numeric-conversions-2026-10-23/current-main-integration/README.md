# Numeric widening checks against the current compiler

The compiled and measured source is `ea25564792a15bd72354c56093dd320d1c7947f0`.
The source identity record names the packaged commit and dependency base.
Only dependency proof documents changed after measurement.
The compiler, Go code and test inputs retain their measured bytes.

All 2433 applicable Go cases pass across three packages. Three synthetic-AST cases skip.
All 1023 numeric property test cases pass without failures or skips.
They contain 765 backend comparisons: 180 differential, 504 exact float math and 81 regression checks.
All four feature census and complexity guard results pass with the limits unchanged.

Each frontend passes all 43 conversion checks.
The matrix passes 158 commands with eight Darwin builds and 24 balanced execution allocation checks.
Both formatters agree and preserve the result after another format pass.
Both predecessor checkers reject the representative conversion. Both candidates accept the same source.

The two new runnable fixtures have zero unpaired allocations with both compiler producers.
All four pointer traces match allocations and frees by address.
The scalar-only fixture has zero trace events. The other fixture proves that the trace is active.

The released stage0 pin produces byte-identical stages 2 and 3.
Their SHA-256 is `7d8c54ca815ab8be6b217c34aae334d8124142c56843fa9de155555277c7c818`.
All 11 strict driver size gates pass without baseline changes.
All 490 compiler and library Fern files pass the canonical format check.
Go format and Go vet pass.
