# Primitive protocol checks with current package and byte-copy changes

The source commit is `90ada84f0329462cd1bd55c586c157a68a2dea22`.
This source includes the package visibility fix and current byte-copy changes from main.
The Go oracle records this clean source revision.

All 2,479 Go checks pass across five packages without skips.
All 4,593 native checks pass without skips.
All 345 contract and package matrix commands pass.
All 54 runtime allocation counts balance.
Both frontends check all 106 coreutils programs, for 212 more commands.
The installed compiler passes 26 checks with its embedded library.

The bootstrap starts with the official stage0 image.
Stages two and three have equal bytes and hashes.
The final compiler contains 14,516,496 bytes.
Its hash is `eb2d4f35eb59aee709ebcda447c4c9da54b071681d19e1630e2bab37172d5930`.

Eight Darwin images build.
Local Darwin execution is unavailable.
All 1,222 source format checks and all 11 strict size gates pass.
Go format and vet checks pass.

The benchmark uses equal source and output across both versions.
Instruction counts decrease from 29,937,444 to 29,667,649.
The profiles record source revisions and binary hashes.

The capsule retains the complete logs and checks their hashes.
Earlier capsules retain their recorded source scopes.
