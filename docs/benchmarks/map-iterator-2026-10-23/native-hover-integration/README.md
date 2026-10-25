# Integration of the native hover repair

The source identity record gives the measured and final source commits.
The only library change restores the `i32.to_string` doc comment.
All other compiler and library bytes remain unchanged.
Test and conformance files remain unchanged.
The record change passed all 18 native playground and 18 browser shim cases.

A fresh bootstrap from the released stage0 pin reproduces all three compiler images.
Stages 2 and 3 match byte for byte.
The archive retains each bootstrap log and all compiler hashes.
Earlier test and benchmark results retain their original source scopes.

The two Go source changes equal the output of the standard Go formatter.
All 98 focused cases pass without failures or skips.
All 2560 applicable Go cases pass across five packages. Three synthetic-AST cases skip.
The matrix passes 88 commands with 24 balanced runtime allocation checks.
All 18 profile commands pass with nine balanced runtime allocation checks.

Direct loops retain 60000 allocations for 10000 loops on each execution target.
The public snapshot protocol uses 420000 allocations for those loops.
All 11 strict driver size gates pass without baseline changes.
The archive retains all 214 execution output logs and inputs.
