# Current Map iterator checks

The source commit is `ac9bd97b29315fca1d987d701b2b74c3e878567c`.
The dependency base is `6896921ec4dd6d1bd1ee841e680754ee3184a02b`.
The native compiler and library bytes match checkpoint `79603b41e6a075afd9a4915d1042822ca40e3280`.
Earlier evidence in the parent directory retains its original source scope.

`Map.iter()` returns an owned `iter.ArrayIter[(K, V)]` snapshot.
The iterator supports the same protocol and adapters as other collection iterators.
Copies keep separate positions. Later Map updates do not change a snapshot.
The retired cursor type, methods, runtime helpers and compiler operations are removed.
Direct Map loops keep their separate column walk.

All 4965 compiler regression cases pass without failures or skips.
The run covers all 109 selected parent tests and their subcases.
It includes Map ownership, checker diagnostics, generic supertraits, the semantic registry and type keys.
The feature census and complexity limits pass without changes.
The test executable was built from `b2978886df6779768c893927325a237b44a102aa`.
The external fixtures use the canonical iterator names from `7688b8525902c5c46996e5b069647d2b01dcbe7d`.

The later Go loader correction removes the retired cursor helper exemption.
All applicable cases in the 3020-case Go sweep pass after that correction.
Eleven synthetic-AST cases skip. All twenty selected runtime cases pass without failures or skips.
The fresh oracle comes from the final source commit with clean VCS metadata.
The final matrix repeats all 88 commands with that oracle.

Eight fixtures pass both checkers, both interpreters and all four target builds.
All 24 runtime allocation counts balance with zero live bytes on x86-64, ARM64 and core WASI.
Darwin is a compile check.
The fixtures cover snapshot returns, records, strings, wide values, empty maps and nested tuple adapters.
Import-aware checker tests keep the argument-error cases for source methods.
A parser-only checker cannot load those method signatures.

Both compilers use identical direct-loop source and library files in the allocation comparison.
Each program visits sixteen pairs in 10000 loops.
The baseline and candidate both allocate 60000 times on all three execution targets.
The candidate public iterator snapshot path allocates 420000 times for the same loop count.
This supports the separate direct loop path.
All nine profile executions balance allocations with zero live bytes.

The published stage0 pin produces byte-identical stages 2 and 3 at 14399008 bytes.
Their SHA-256 is `a4cfd1112e8600cc835d1bfc3a4208115cd370833bdf1c214b980fd597ae462c`.
All 11 strict driver size gates pass without baseline changes.
Go vet, Go format and all 1201 canonical Fern format checks pass.

The manifest records 497 source file hashes and 27 evidence artifacts.
The source patch passes a reverse-application check.
The archive contains all 212 execution output logs and both exact profile input files.
