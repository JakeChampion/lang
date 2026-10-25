# Package visibility with the current JSON library

Status: measured reference for source `5e151dec3bc91b57d372eae9a9ab8df91e64df2c`.

This source combines the module driver visibility fix with main commit `c61e92a837387211575e57797c8eb254ef243205`. The main commit fixes the JSON decoder for minimum signed integers. Its library and test changes merge without conflicts.

The fresh Go run passes 2,461 checks across five packages. The JSON test runner passes all 30 assertions. Both frontends check and interpret that test. The x86, ARM, and WASI images execute all 30 assertions with balanced allocation counts. The JSON matrix has ten commands.

All 1,291 native checks pass. These checks cover the complete verifier corpus, enum boundaries, nested manifests, and module runtime execution. Feature census and the complexity gate pass. The three package matrices pass 195 commands. All 30 runtime allocation counts balance. Both frontends check all 106 coreutils programs.

The compiler with its embedded library passes nine commands from outside the repository. All three runtime allocation counts balance. The x86 program records 434 balanced allocation trace events. ARM and WASI use allocation counters.

The official stage0 builds three fresh stages. Stages 2 and 3 are identical at 14,466,048 bytes. Their bytes match the previous verifier measurement. All 11 strict driver size gates pass. All 1,218 source format checks pass. Go format and Go vet pass.

The certificate verifies unchanged compiler source, official pin, and size limits against the prior verifier source. The JSON library changed. This capsule records a fresh Go oracle, bootstrap, native checks, runtime matrices, and embedded library checks. The prior caller, comparator, and verifier capsules retain their recorded source scopes.
