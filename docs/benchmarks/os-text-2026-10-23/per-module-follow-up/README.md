# Byte API integration follow-up

Per-module compilation now includes the byte directory helper in its runtime roots.
Byte payloads use a shared modifier and retain canonical IR operation tags.
The `op_*` constructor convention remains intact.
Asset collection now reports an error when it cannot read a directory.

The compiler source is `14395d0eeb43d7df0894a594c3b3b534725a48ac`.
The later test source is `4cf99931a22d65f665f6266c43b182929dcdc7c7`.
Only the added interpreter regression differs between these scopes. Compiler, library, and pin files are unchanged.

All 335 selected integration cases pass without skips.
The separate raw-byte regression also passes on x86-64 and ARM64 Linux.
It checks invalid filenames, directory handles, and link targets through separately linked interpreters.
The 38-command CLI matrix passes. All nine allocation counts balance with zero live bytes.
The Go oracle retains its recorded source, `3048da4e51442f2baa9e8b045f5003329d72d214`.

All three bootstrap stages are identical at 14,454,192 bytes.
Their SHA-256 is `9161f9860e0abbf5d4d4d8e26a9e3e511788a7f34601caf165d8ec98fb26a3a0`.
The published stage0 pin remains unchanged.
All 11 strict driver size gates pass without baseline changes.
The feature census and complexity limits pass. Go vet and format checks pass.

The capsule records 498 compiler and library files, exact source scopes, and artifact hashes.
The source patch excludes benchmark evidence files. Earlier proof capsules retain their original scope.
