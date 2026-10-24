# Map iterator checks with final byte filesystem APIs

The measured source is `44e75d1104c7dd416e9e30667fc21f4012117d7e`.
It includes the final OS runtime roots, canonical IR modes, and asset error checks.
All 431 selected integration cases pass across two packages without skips.

Eight Map fixtures pass 88 commands with both interpreters and four target builds.
The inherited OS byte matrix passes 38 commands.
The matched profiles pass 18 commands.
All 42 runtime allocation counts balance with zero live bytes.

Direct loops allocate 60,000 times for 10,000 loops before and after the Map change.
The owned iterator path allocates 420,000 times for that loop count.
The baseline compiler is the final OS compiler at `14395d0eeb43d7df0894a594c3b3b534725a48ac`.

The published stage0 pin produces identical stages 1, 2, and 3 at 14,424,640 bytes.
Their SHA-256 is `467e1cda355088c8e3a2fd4cd20155a4575b3d5eff1156bcaadf9be2b42a1c20`.
All 11 strict size gates pass without baseline changes.
The unchanged feature census and complexity limits pass.
Go vet, Go format, and compiler and library format checks pass.

The Go oracle retains its recorded source, `85018cd7080613844ba1acabcca8ae9ff7f7f129`.
Only four test files differ in the Go source between these scopes.
The earlier Go suite retains its five-package scope with 2,561 passes and three synthetic AST skips.
Earlier capsules preserve those logs and their source identities.

The capsule records 498 compiler and library files and 294 inputs and output logs.
The source patch excludes benchmark evidence files.
Darwin builds pass. No local Darwin execution is claimed.
