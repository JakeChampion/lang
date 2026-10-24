# Supertrait path and target runner follow-up

The source commit is `4c88132ee99634eb836ae68dac1ad8cbce1bcc1f`.
Its public commit is `c6cce632632bd1bd8bb724c9ee119f36e8f87aad`.
The two commits have the same source tree.

Trait names accept dotted module paths.
They reject the associated-type separator `::` at the same position as the Go parser.
Associated-type projections within generic arguments retain their type syntax.

Imported trait tests use the shared target runner.
The runner selects native execution or the configured emulator.
Checks for required tools still apply.
The tests require balanced allocation counts and zero live bytes.
Examples that allocate nothing can pass this check.
Darwin remains a compile check on this Linux host.

All 236 focused test results pass without failures or skips.
The tests cover parse positions, native check positions and generic supertraits.
The published stage0 pin produces byte-identical stages 2 and 3.
Their SHA-256 is `569572b5db71afb78d557106927cd96133c2ed544d0ace3da86dad1b56758baf`.
All 11 strict driver size checks pass without baseline changes.
The changed parser file passes the canonical format check.

The earlier broad results apply to their recorded source scopes.
This follow-up records the changed native source and target tests.
The source manifest records all compiler and standard library Fern files.
