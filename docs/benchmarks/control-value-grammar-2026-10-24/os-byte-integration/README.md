# Grammar integration with byte filesystem APIs

The compiler source is `6ac7c8eeb8665041c1deb83944d1b3b3bce0fb7b`.
The integrated source is `78350882a8999033008422ff6c62861bc5cfc6d6`.
Their compiler, library, and pin files are identical.
The later merge adds an interpreter regression and OS evidence files.

The native formatter preserves explicit returns in nested constructor matches.
It also removes the synthetic return after a branch that already returns.
All 105 native formatter cases pass. The new nested match case also passes.
These cases compare Go output, check format stability, and check the formatted source.

The selected Go suites pass 3,418 cases across seven packages.
Eleven synthetic AST and interpreter cases skip by design.
The earlier focused compiler suite passes 563 cases at `afdbeacb42b579a13c577ff8aa622e2846b6d012`.
That suite retains its original source scope.

Ten fixtures pass 190 commands with both checkers, interpreters, and formatters.
They also build for both Linux targets, WASI, and Darwin.
All 30 runtime allocation counts balance with zero live bytes.
All 54 diagnostic checks preserve error codes across both checkers.
Darwin images are built but are not executed locally.

The published stage0 pin produces identical stages 1, 2, and 3 at 14,441,104 bytes.
Their SHA-256 is `639ca8c3b7d5a62ba75d05d0442ceaa9114a276296108663a638bbf30caca7a9`.
All 11 strict size gates pass without baseline changes.
Compiler and library format checks, Go format checks, and Go vet pass.

The capsule records 498 compiler and library files and 535 inputs and output logs.
The source patch excludes benchmark evidence files.
Earlier capsules retain the timed-out full printer corpus attempt and its exact scope.
