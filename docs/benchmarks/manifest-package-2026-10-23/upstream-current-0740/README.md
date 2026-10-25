# Package visibility with current byte-copy optimizations

The measured source is `e48be89a26d3ca4325c4388a308e9ada810a35a1`.
It includes main commit `ba2b7a8cb69a75160b6d8a9e7fcfa289ea15789d`.
The merge retains the byte-copy changes and the current PEG grammar validation from main.

All 2,461 Go checks pass across five packages without skips.
All 1,435 native checks pass across two suites without skips.
These include the verifier corpus, module drivers, library documentation, and byte-copy runtime cases.

All 195 package matrix commands pass with 30 balanced runtime allocation counts.
Both frontends check all 106 coreutils programs, for 212 commands.
The JSON test passes ten commands across both frontends and three target runtimes.
Each runtime executes all 30 assertions with balanced allocation counts.

The compiler with its embedded library passes nine commands from outside the repository.
All three runtime allocation counts balance.
The x86 program records 434 balanced allocation trace events.
ARM and WASI use allocation counters.

The official stage0 builds three fresh stages.
Stages two and three are identical at 14,496,944 bytes.
Their hash is `5b658525c9adcf42eeae73375d9f4d7a247050a6cb9c27cde271a89f8a74033f`.

All 11 fresh strict size gates pass.
All 1,224 source format checks pass.
Go format, Go vet, feature census, and the complexity gate pass.
The source certificate identifies the complete measured compiler and library inputs.

The earlier capsules retain their recorded source scopes.
The prior caller checks include all 5,786 affected GNU 9.12 parity checks without skips.
Darwin execution requires CI on Apple Silicon.
Full CI must pass before merge.
