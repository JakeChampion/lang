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
