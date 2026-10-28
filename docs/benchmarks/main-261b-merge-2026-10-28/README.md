# Main compiler and UTF-8 integration

The branch merges main at `261b2188a66db8e19a0105a9738befb2f9c8fd37` after `d7d1b2a3dcbbe176cde7169fe0fd3c7fa6fe9c28`.
The main delta changes 107 paths with constant parameters, expected variant types, UTF-8 errors, HTTP errors, owned-value replay and a stage-0 update.
The merge retains exact main contents or deletions for 102 paths.
Four baseline files differ only in comments.
The successor-count patch remains identical to the prior qualified branch patch.
The owner replay change remains intact.

The three baseline conflicts retain the newer owner measurements.
Two allocation rows, two cliff rows and 101 self-host rows match main.
Every metric row and tolerance in all four baseline files matches main.
The archives retain all conflict stages and the resolution proof.
These owner measurements are not new measurements of this branch.

The owner stage-0 pin uses source `7ea82a96e0234149bb1af14e50d1c1e7f22bdb0e` and release `stage0-20261028-7ea82a9`.
The tests unset the historical `STAGE0` override and use the current pin.
Historical producers and their raw measurements remain separate.
The new compiler source requires a fresh profile before another optimization choice.

The compiler matrix passes 5,861 nodes in 337.517 seconds, with zero failures.
The 19 skips cover native OS-floor cases on Wasm.
Their exact names match the prior expected skip list.
The matrix covers semantic production, SSA unit replay, type resolution, checker diagnostics, UTF-8 streams and native formatter parity.

Compiler, source-lint and parser packages pass.
The unsharded Go formatter corpus reaches its default 10-minute timeout during the corpus type check.
That attempt is not a pass.
The raw timeout and the initial wrapper stop on expected skips remain in the evidence.
The focused Go formatter checks pass three nodes, and repository ratchets pass four nodes.
The full formatter corpus remains unqualified until all four supported corpus shards complete.

The two new conformance fixtures run with sanitizers on x86-64, ARM64 and Wasm.
All six runs return their exact expected output, balance allocations and report zero live bytes.
The current public compiler checks all 2,110 formatter inputs successfully.
The records include commands, source hashes, output hashes and runtime censuses.

The archive manifest records raw and compressed hashes with byte counts.
Every compressed file passes a decompression identity check.
GitHub marks PR #13718 ready for review, while issue #4412 and full retained-machine qualification remain incomplete.
Applicable current-head CI and complete issue acceptance remain merge requirements.
