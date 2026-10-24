# Imported cast type checks

Imported casts now qualify their target types through the module rewrite context.
The same rule applies to ordinary casts and checked downcasts.
The rewrite targets only the type in a cast.

The measured source is `feedc9050efb5017d83423c8b782ba24e9f66f5c`.
The regression source is recorded in the identity file.
Only dependency evidence changes between these scopes.
Compiler, library, Go code, and test input bytes remain identical.

The three probes cover array cells, Option cells, and checked downcasts.
The prior native compiler refuses all three probes. The Go frontend accepts them.
The fixed compiler executes all three on x86-64, ARM64, and WASI.
All 36 selected native cases and all 2,066 Go cases pass without skips.
The unchanged feature census, complexity limits, and canonical IR checks pass.

The final CLI matrix passes 33 commands across both checkers and interpreters.
All nine compiled allocation counts balance with zero live bytes.
The cell probes exercise heap allocations. Scalar downcasts can need no heap allocation.
All three Darwin builds pass. No local Darwin execution is claimed.

The published stage0 pin produces identical stages 2 and 3 at 14,449,360 bytes.
Their SHA-256 is `b71a022817dc777f2639236e097241074deb59faa7a612e68dc5a1fb04bab4c9`.
All 11 strict size gates pass without baseline changes.
Compiler and library format checks, Go format checks, and Go vet pass.

The capsule records 498 compiler and library files and 84 inputs and output logs.
The source patch excludes benchmark evidence files.
