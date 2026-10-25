# Manifest packages and public re-exports

This record covers #12721, #13077, and #13096.
The measured source is `c3bfb4eb98787ace2a720ca97f273e38d7a91cd8`.
The dependency source is `b15d4e7e10ddaec58bba62c4d3d8d8c41c06e632`.

Both loaders identify a package by its nearest manifest path.
Subdirectories share that package until another manifest starts a new package.
Identical package names do not join separate manifest trees.
Without a manifest, the directory defines the package.
The standard library is one package across `std` and `core`.

Enum constructors and qualified patterns follow their enum's visibility.
Public declarations retain precedence when names coincide.
Both loaders check every public re-export before they combine module declarations.
A public re-export admits public declarations and keeps variants under their enum.
Invalid directives report E087 at the directive for unused names and chains too.

The migration covers 75 library declarations.
These comprise 48 Map helpers, three integer helpers, two string helpers, and three float tables.
They also comprise five buffer helpers, 13 JSON helpers, and one vector node enum.
The eight float and buffer helper exceptions from the original record are now private to the library package.
All 619 float table assertions remain intact and pass within that package.
Generated JSON derive methods retain access to the library's private helpers.

All 2,440 Go cases pass across four packages without skips.
All 50 focused native cases pass without skips.
The float table test also passes.
The published stage0 pin produces identical stages 2 and 3 at 14,461,808 bytes.
Their SHA-256 is `76e5bda0e8ad3330d4f52beb3a0f348612bb198775bc34e348c71ac7eb0492bf`.
All 11 strict size gates pass without baseline changes.

The three matrices pass 195 commands with 30 balanced allocation counts and zero live bytes.
They cover both interpreters, native Linux, ARM64 Linux, and WebAssembly.
The re-export matrix also builds four Darwin images, without local Darwin execution.
Refused builds produce no output artifact.
The embedded CLI passes five public API and private access checks.
The library fixture's allocation trace has 434 events and zero unmatched allocations.

All 1,204 standard source files pass the format check.
Go format, Go vet, the feature census, and the complexity limit pass.
The seven migrated library documents retain the executable tokens from the private feature source.
The literate library's prose and chunk structure remain intact.
The source patch excludes benchmark records.
The capsule records exact source files, inputs, commands, logs, and hashes.
