# Package visibility with current library documentation

The measured source is `7301c362ba8342e63a2e753eeb6ae5dff86f59e2`.
It includes main commit `65ab83f46ddfb18a2330a442c607bea4bb695aea`.
This update retains the Option and Result documentation from main.

All 2,461 Go checks pass across five packages without skips.
All 1,367 native checks pass without skips.
These include the verifier corpus, module drivers, library documentation tests, and playground library checks.

All 195 package matrix commands pass with 30 balanced runtime allocation counts.
Both frontends check all 106 coreutils programs, for 212 commands.
The JSON test passes ten commands across both frontends and three target runtimes.
Each runtime executes all 30 assertions with balanced allocation counts.

The compiler with its embedded library passes nine commands from outside the repository.
All three runtime allocation counts balance.
The x86 program records 434 balanced allocation trace events.
ARM and WASI use allocation counters.

The official stage0 builds three fresh stages.
Stages two and three are identical at 14,466,048 bytes.
Their bytes match the previous final compiler.

The compiler source, official pin, and size limits remain unchanged.
The source certificate verifies these inputs against the earlier driver size measurement.
All 11 strict size gates remain applicable.
All 1,218 fresh source format checks pass.
Go format, Go vet, feature census, and the complexity gate pass.

The earlier capsules retain their recorded source scopes.
The prior caller checks include all 5,786 affected GNU 9.12 parity checks without skips.
Darwin execution requires CI on Apple Silicon.
Full CI must pass before merge.
