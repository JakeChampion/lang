# Payload patterns on current main

The feature includes main at `7cd648eb7`.
Payload patterns and qualified struct patterns pass both checkers and interpreters.
Compiled x86-64, ARM64, and WASI runs pass strict lowering, sanitizer, and leak checks.
Seven invalid payload patterns keep their required diagnostics in both checkers.
The payload formatter case and the repository complexity gate pass.
The Go parser, checker, and monomorphization packages also pass.

Earlier bootstrap and size records retain their original source scope.
The new PR CI supplies full validation for this integration.
