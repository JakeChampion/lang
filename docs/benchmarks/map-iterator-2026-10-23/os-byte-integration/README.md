# Map iterator checks after the OS byte integration

`Map.iter()` returns an owned snapshot through the Iterator protocol.
Copies retain separate positions. Later Map updates do not change an earlier snapshot.
The old public cursor and its runtime operations are removed.
Direct Map loops retain their separate column walk.

The measured source is `85018cd7080613844ba1acabcca8ae9ff7f7f129`.
The later integration is `aedd40abf595377c7de1c3054b0ea260ee5a3e62`.
Only the three literate-library test repairs and their evidence differ between these scopes.
The identity record verifies unchanged compiler, library, and pin files.

All 98 focused cases pass across two packages, without skips.
All five broader Go packages pass: 2,561 cases pass and three synthetic AST cases skip.
The feature census and complexity limits pass without baseline changes.
Go vet, Go format, and all compiler and library format checks pass.

The Map matrix passes 88 commands and 24 allocation checks.
The inherited OS byte matrix passes 38 commands and nine allocation checks.
All allocation counts balance with zero live bytes. Both interpreters and all four target builds pass.
Darwin binaries are built but cannot run on this Linux host.

The matched allocation profile passes 18 commands and nine allocation checks.
Both compilers use identical direct-loop source and library files.
Each direct-loop run allocates 60,000 times for 10,000 loops, before and after this change.
The owned iterator path allocates 420,000 times for the same loop count.
The baseline compiler is the measured OS compiler from source `3048da4e51442f2baa9e8b045f5003329d72d214`.

The published `stage0-20261024-9eddfb5` pin produces three identical local bootstrap stages at 14,424,592 bytes.
Their SHA-256 is `d949947ae8ae714ba44dc12d7a4bb22f41166ee4f3aed1b1f62a9c00be122a31`.
All 11 strict driver size gates pass without baseline changes.

The capsule records 498 compiler and library files, exact source scopes, and each artifact hash.
It also retains 294 input and output files. The source patch applies to the integrated OS predecessor.
Earlier proof capsules retain their original scope.

The source patch excludes benchmark evidence files. It retains all compiler, library, test, and fixture changes.
