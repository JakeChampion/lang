# Package visibility with current byte-copy optimizations

The measured source is `aa05cf24dd36188ea2d7c1aa75435428e4ddc6f2`.
It includes main commit `4ba94d810e37a1875b07f3898c127c9594174a87`.
The merge retains the byte-view mismatch and LZ77 copy improvements from main.

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
Stages two and three are identical at 14,487,216 bytes.
Their hash is `488c0cd9e1e355d6728ecb28305540d0e6e8e4c73eb746815c8b95c2551b0206`.

All 11 fresh strict size gates pass.
All 1,218 source format checks pass.
Go format, Go vet, feature census, and the complexity gate pass.
The source certificate identifies the complete measured compiler and library inputs.

The earlier capsules retain their recorded source scopes.
The prior caller checks include all 5,786 affected GNU 9.12 parity checks without skips.
Darwin execution requires CI on Apple Silicon.
Full CI must pass before merge.
