# Text API checks after the module driver update

The source commit is `af9fe92362083d4df4d33743e41333be6b985a88`.
The clean Go oracle and native compiler use source `acb3ebb2e1503970f61d587af88c5c985c074b45`.
The source certificate verifies equal compiler and library inputs across these commits.

All 2,495 Go checks pass across six packages without skips.
All 4,593 native checks pass without skips.
The bootstrap starts with the official stage0 image.

Stages two and three have equal bytes and hashes.
The final compiler hash is `7016e4d8981c88ccb251278aefb4febafc48480144b69fdc19e26a748584195a`.

All 385 text, primitive, and package matrix commands pass.
All 60 runtime allocation counts balance.
Both frontends check all 106 coreutils programs, for 212 more commands.

The installed compiler passes 33 checks with embedded library files.
These checks include 792 balanced trace events and 12 balanced runtime allocation counts.
Two Darwin images build.

Local Darwin execution is unavailable.
All 1,222 source format checks and all 11 strict size gates pass.
Go format and vet checks pass.

The profiles compare the current primitive API with the text API.
The encode and replace probe reduces allocations from 4,115 to four.
Its instruction count increases by 1.65 percent.

The case conversion probe reduces allocations from 90,162 to 72.
Its instruction count decreases from 64,439,868 to 58,657,442.
Reverse tests use 256, 512, and 1,024 clusters.

Their allocation counts decrease from 529, 1,043, and 2,069 to 13, 14, and 15.
The combined instruction count decreases from 33,961,693 to 33,375,766.
Each comparison retains equal output and the same benchmark source.
