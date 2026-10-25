# Primitive protocol checks after the module driver update

The source commit is `460129e95d676f30cef6a3e8ea94da58872f24ac`.
The clean Go oracle and native compiler use source `493562d18cd679d2ff4721b6dd9eeca48898c41b`.
The source certificate verifies equal compiler and library inputs across these commits.

All 2,479 Go checks pass across five packages without skips.
All 4,593 native checks pass without skips.
The bootstrap starts with the official stage0 image.

Stages two and three have equal bytes and hashes.
The final compiler hash is `aeefc6fe2bb67c2bca03b8a4b6c0694234335d85aac54fc96e750e4358a143c6`.

All 345 contract and package matrix commands pass.
All 54 runtime allocation counts balance.
Both frontends check all 106 coreutils programs, for 212 more commands.

The installed compiler passes 26 checks with embedded library files.
Eight Darwin images build.
Local Darwin execution is unavailable.

All 1,222 source format checks and all 11 strict size gates pass.
Go format and vet checks pass.

The benchmark retains equal source and output across both versions.
Instruction counts decrease from 29,937,444 to 29,667,649.
The profiles record both source commits and all binary hashes.

The capsule retains the complete logs and checks their hashes.
