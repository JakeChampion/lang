# Codec and byte API integration on current main

The final source commit is `e8742d205`.
Its compiler and library sources match `78fc3098a7a7f262df7fc6d977ab9baf098ea066`.
The final commit changes one fractal caller to pass a byte view.
The base is `76623a46dcb027602363addcadbbfbc340f66929`, which includes main at `5e637120b`.
The parent validation record retains the earlier source and proof hashes.

The merge preserves the new PNG filters and compression options.
The fast DEFLATE entry point and its helpers accept borrowed byte views.
The existing balanced compressor retains its policy.
Caller migrations preserve the existing protocol error channels.
This work does not implement networking issues.

The codec, SemVer, and UUID matrix passes 131 commands across thirteen programs.
A second matrix passes 44 commands across four byte API fixtures.
Both checkers and both interpreters agree on those fixture results.
All programs compile for x86-64 Linux, ARM64 Linux, WASI, and ARM64 Darwin.
All 51 Linux and WASI execution censuses have equal allocation and free counts and zero live bytes.
Darwin execution requires the macOS CI runner.

The byte fixtures cover borrowed and owned inputs, single bytes, checksum seeds, and multibyte fill characters.
The minimum-i32 seed fixture also preserves actual i64 and u64 arithmetic.
The byte API contract suite has twelve test events and no failures or skips.
The tuple and record comment regression suite has four test events and no failures or skips.

The final corpus check passes all 526 example, benchmark, and library programs.
The codec family suite passes 36 test events without failures or skips.
The PNG, fractal library, source census, complexity, and error contract suite passes thirteen test events without skips.
The documentation and conformance census suite passes three test events.
The method index matches the library sources.

The pinned compiler builds three stages from the integrated source.
Stages two and three match byte for byte.
Their SHA-256 is `f6ed9d1264993ef6d231ce452a17eda29ab048e411c428131956e7886423f880`.
All eleven strict driver sizes pass the unchanged baselines from main.
The largest increase is 0.1 percent.

Go vet and Go format pass.
The canonical Fern format check passes all 1,200 source files.
`artifacts.json` records 27 artifact hashes and the source for each artifact.
The source inventory contains 496 compiler and library file hashes and matches the bootstrap digest.
The matrix log archive contains all 350 stdout and stderr files.
`change.patch.gz` contains the complete change from the base and passes a reverse-application check.
