# Main merge after the separate wide machine proof

The branch incorporates main at `7aded8a6ca65677ee5b01db9dfe7ad95d3d50ebb` through merge `b8b83c0fffd5d8570c5d621636c49ee0a95095d1`.
PR #13747 adds a separate symbolic machine and its qualification records.
All 29 selected proof, driver, reference and support files match main byte for byte.
The old proof models, driver assertions, production instructions and mathematical domains remain unchanged.
The separate retained-driver integration remains incomplete under #13717.

The combined branch passes 30 stock machine, worker-protocol and import-free test nodes.
The run has zero failures and zero skips on x86-64, ARM64 and Wasm.
Source, feature, unchanged complexity and all 2,104 formatter inputs pass.
The fixed reference reproduces exactly, and all 12 protocol self-tests pass.
The records contain the actual process exit codes and commands.

The same producer rebuilds the raw compiler from the merged sources in 44.751 seconds.
Its bytes match the measured compiler from the phi predecessor slice.
Both hashes are `fb78dd106a460a9732db18e3d45b3f837aec9b132f3fe49488681403df6eea80`.
The producer hash is `1e0ad6be807a01bbf50a05535d46808d611816016ce388e687d44589c5dc7e52`.
This identity check supplies no new speed or self-reproduction result.

Both native performance jobs passed in CI run `40630000183` at head `b706d429963e035236d8e58bc11148e9211de443`.
Six map measurements show mixed changes within their tolerances.
The baseline pins now use those exact reported values, with every tolerance unchanged.
All six strict report comparisons pass.
Checker allocations and frees each remain 33,753,130 in that CI measurement.
These baseline updates supply no new optimization claim.

The archive retains the raw ZIP artifacts, extracted reports, gate logs and exact row differences.
It also retains the cancelled Lint log from that head.
Lint stopped during the `uv` download before repository checks ran.
That cancelled job does not qualify Lint, and the new published head requires its own complete applicable CI.
The architecture roadmap, retained-driver integration and full CI acceptance remain incomplete.
