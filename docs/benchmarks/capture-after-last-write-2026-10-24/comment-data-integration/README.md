# Capture checks with the current library comments

The final source commit is `5d4dd36f62d792e27c57db48e180ad444eff0b41`.
The measurements use `fd6dae1372db4886ca574645370b80eb98583a87`.
The only difference is one empty line in `compiler/lift.fern`.
All three compiler images match byte for byte across that change.
The identity record retains both source commits.
The owner comment cleanup and the two required library comments remain in the source.

All 41 focused cases pass without failures or skips.
The Go interpreter and source checks pass in both packages.
They run 566 cases: 563 pass and three synthetic-AST cases skip.

The Go oracle records its source revision and a clean worktree.
All 33 matrix commands pass for both interpreters and all target builds.
All nine runtime allocation counts balance with zero live bytes.

One pair checks each of three Map sizes: 25000, 50000 and 100000 items.
The baseline uses 50016, 100017 and 200018 allocations.
The candidate uses 15, 16 and 17 allocations.
One compiler pair emits the exact final compiler bytes.

These are additional checks. Earlier repeated measurements retain their original scope.

The released stage0 pin builds byte-identical stages 2 and 3.
Their SHA-256 is `3abcae01dc938975608d1bb34765a9abff968b94146332c37a1a81a72c0cb5e6`.
All 11 strict driver size checks pass without baseline changes.
The canonical Fern format check, Go format check and Go vet pass.
The manifest records all 497 compiler and library source files.
The archive retains all 66 execution output logs.
