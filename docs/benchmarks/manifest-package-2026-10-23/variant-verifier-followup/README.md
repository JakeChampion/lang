# Module driver enum visibility

Status: measured reference for source `1c984f682e28f6932a8ae2b4f752338616be8b79`.

The module drivers now apply the enum visibility rules used by the main compiler. The shared loader resolves manifest roots and directory roots. Embedded library modules use the library package. A public constructor takes precedence when a hidden enum has the same constructor name.

The new boundary test covers public and package enums. It includes nested manifests with identical names. The checker rejects the package constructor across that boundary. The verifier accepts each valid case and models its lowered bodies.

All 20 module runtime checks pass.

All 1,264 verifier and visibility checks pass. All seven focused boundary checks pass. The complete verifier corpus includes 1,078 programs. The three package matrices pass 195 commands. All 30 runtime allocation counts balance. Both frontends check all 106 coreutils programs.

The compiler with its embedded library passes nine commands. Linux and WASI programs execute with balanced allocation counts. The x86 program records 434 balanced allocation trace events. The other targets use the allocation counter.

The official stage0 builds three fresh stages. Stages 2 and 3 are identical at 14,466,048 bytes. All 11 strict driver size gates pass. All 1,218 source format checks pass. Go format and Go vet pass. The complexity gate passes with its unchanged limits.

The certificate records unchanged Go frontend, generator, library, and coreutils source bytes against the previous public head. The earlier caller and comparator capsules retain their source scopes. Their results include 2,461 Go checks and 5,786 GNU parity checks. The current capsule adds a fresh compiler, verifier corpus, package matrices, caller checks, and embedded library checks.

The first extra module runtime run failed two builds when the local disk filled. Its raw log is retained. The final run uses a cache on the temporary filesystem. Compiler binaries, Go oracles, and the official stage0 remain available at their recorded paths.
