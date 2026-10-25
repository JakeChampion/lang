# Library cohesion and compiler integration checks

The source commit is `97f686adeee9baa851df24b6b3d7ee2621fcc910`.
It combines primitive protocols, text APIs, numeric APIs, JSON aliases, generic receivers, I/O results, and nested patterns.
The branch includes the current package visibility and byte-copy changes.

All 2,820 selected Go checks pass across seven packages without skips.
All 11,021 selected native checks pass across 217 test parents without skips.
All 5,786 selected GNU 9.12 parity checks pass without skips.
The parity commands use the recorded GNU executable and timezone data.
The targeted suite passes 30 checks.
The nested-pattern suite passes seven more checks.

All 1,041 matrix and caller commands pass.
All 123 runtime allocation counts balance.
The matrix covers both frontends and three executable targets.
It includes all eleven JSON import aliases and the two generic array fixtures.
The I/O matrix checks binary input, binary output, and repeated handle closure.
All 106 coreutils programs pass both frontends.

The installed compiler passes 100 commands with its embedded library.
All 33 embedded runtime allocation counts balance.
Its x86 programs record 7,698 balanced allocation trace events.
The other matrices retain 10,026 allocation trace events.
The matrices build 52 Darwin images, plus eleven images with the embedded compiler.
Local Darwin execution remains unavailable.

The bootstrap starts with the official stage0 image.
Stages two and three have identical bytes and hashes.
The final compiler contains 14,545,088 bytes.
Its SHA-256 is `628c9e6fb1eb4a0bd1923cd82f176199cf8269f5fdd069f56ddde1ec24dc0a68`.
The clean Go oracle records the source revision above.

All 11 strict driver size gates pass.
All 1,222 source format checks pass.
Go format, Go vet, feature census, and complexity checks pass.

## Numeric kernel measurements

The reference uses the text API source before the numeric parser change.
The reference source is `e568db05a0a054e898df83c5597aadabce26fe28`.
The reference compiler hash is `7016e4d8981c88ccb251278aefb4febafc48480144b69fdc19e26a748584195a`.
The profiles retain source hashes, compiler hashes, executable hashes, arguments, and raw instruction counts.
All four checksums agree across the two versions.

| Kernel | Reference instructions | Current instructions | Reference allocations | Current allocations |
| --- | ---: | ---: | ---: | ---: |
| Integer parsing | 695,221 | 946,106 | 0 | 0 |
| Fast float parsing | 1,875,829 | 1,907,573 | 0 | 0 |
| Exact float parsing | 969,642 | 970,014 | 1,732 | 1,732 |
| Grouped integer formatting | 9,606,033 | 4,694,929 | 57,344 | 16,384 |

The typed parsers retain instruction overhead against the older parsers.
The scalar return changes remove allocations from successful fast parser calls.
The capsule makes no general speed claim.

## Proof scope

The source patch covers code, tests, documentation, and module catalogs.
Earlier benchmark capsules remain in their recorded paths.
The manifest hashes every artifact in this capsule.
The capsule retains raw logs and validates every package terminal event.
Full CI must pass before merge.
