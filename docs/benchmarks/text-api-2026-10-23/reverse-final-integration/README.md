# Text API and allocation measurements

Status: measured reference for source `c2f3af1b24b65d908f895f8f2ac8598bd02ed932`.

String operations use bytes. UTF-8 operations belong to `std/utf8`. Unicode properties and case operations belong to `std/unicode`. The fixtures check the owner of each operation and reject the removed aliases.

The Unicode generator shares the grapheme boundary scan between three functions. Reverse stores byte offsets and copies the byte ranges in reverse order. It preserves malformed input bytes and allocates no result for empty input. The public grapheme array operation retains its output behavior.

All 2,495 Go checks pass across six packages. All 67 selected library checks pass. All 4,586 native checks pass. No checks in these runs are skipped. The text, primitive, and package matrices pass 385 commands. All 60 runtime allocation counts balance.

The compiler with its embedded library passes 33 commands from a directory outside the repository. Twelve runtime allocation counts balance. Four x86 programs record 792 balanced allocation trace events. Both frontends build the updated Unicode fixture for Darwin. Darwin execution requires Apple Silicon.

The official stage0 builds three fresh stages. Stages 2 and 3 are identical at 14,495,200 bytes. All 11 strict driver size gates pass. All 1,222 source format checks pass. Go format, Go vet, feature census, and the complexity gate pass.

The probes use the same formatted source and compare output bytes before and after the change. The combined encode and replacement probe reduces allocations from 4,115 to four. Its instruction count increases from 1,184,346 to 1,203,945. This is an allocation improvement with a 1.65 percent instruction cost.

The five case operations reduce allocations from 90,162 to 72. Their instruction count decreases from 64,439,868 to 58,657,442. Reverse reduces allocations from 529, 1,043, and 2,069 to 13, 14, and 15 at the three input sizes. Its instruction count decreases from 33,961,693 to 33,375,766. These are deterministic instruction measurements from Callgrind, rather than elapsed time measurements.

An optional broad Go command used three incorrect package paths. Its unsharded formatter corpus also reached the default ten-minute timeout. That run is retained as a failed attempt and contributes no passed gate. The final targeted Go, library, native, format, and matrix runs above are complete.
