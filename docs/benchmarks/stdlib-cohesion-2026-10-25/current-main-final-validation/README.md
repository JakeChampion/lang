# Library contracts and constant evaluation

The measured source is `42008aeb1b1d5f11c7d4380aa70c04c1e6e80961`.
It includes main commit `07f79a2fc90ddae2c3696c251e2ed04cd9d5dc39`.
It combines primitive protocols, text APIs, numeric APIs, JSON aliases, generic receivers, I/O results, nested patterns, and constant evaluation.
The package visibility and byte-copy changes are included.

All 2,823 selected Go checks pass across seven packages without skips.
All 1,387 targeted native checks pass across thirteen test parents without skips.
They include all 185 constant checks and the complete verifier corpus.
The feature census and complexity limits remain unchanged.

All 1,041 matrix and caller commands pass.
All 123 runtime allocation counts balance.
The matrix covers both frontends and three executable targets.
It includes all eleven JSON import aliases and the two generic array fixtures.
The I/O matrix checks binary input, binary output, and repeated handle closure.
All 106 coreutils programs pass both frontends.

The compiler passes 100 commands with its embedded library.
All 33 embedded runtime allocation counts balance.
The x86 programs record 7,698 balanced allocation trace events.
The other matrices retain 10,026 allocation trace events.
The matrices build 52 Darwin images, plus eleven images with the embedded compiler.
Local Darwin execution remains unavailable.

The bootstrap starts with the official stage0 image.
Stages two and three have identical bytes and hashes.
The final compiler contains 14,566,352 bytes.
Its SHA-256 is `5878a841ea43dc2f22c549b3314073a3a16543ef852411b1b45e7926f522c041`.
The clean Go oracle records the measured source revision.

All 11 strict driver size gates pass.
All 1,223 source format checks pass.
Go format and Go vet pass.
The source certificate verifies each compiler and library input.

## Numeric kernel measurements

The reference uses the text API source before the numeric parser changes.
The checksums match for every kernel.

| Kernel | Reference instructions | Current instructions | Reference allocations | Current allocations |
| --- | ---: | ---: | ---: | ---: |
| integer | 695221 | 946106 | 0 | 0 |
| float-fast | 1875829 | 1907573 | 0 | 0 |
| float-exact | 969642 | 970014 | 1732 | 1732 |
| group-format | 9606033 | 4694929 | 57344 | 16384 |

The integer parser costs about 36% more instructions in this measured kernel.
The fast float parser costs about 1.7% more instructions.
The group separator formatter reduces both instructions and allocations.
These measurements do not claim a general speed increase.

Earlier capsules retain their recorded source scopes.
They include 11,021 native checks and 5,786 GNU 9.12 parity checks without skips.
The Make bootstrap and distcheck results retain their recorded source scope.
Full CI must pass before merge.
