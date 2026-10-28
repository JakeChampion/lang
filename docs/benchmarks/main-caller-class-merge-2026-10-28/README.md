# Main merge after caller-class qualification

The branch incorporates main at `64896fed6fc9288db3618f44fae2bac7bfe03fbf` through merge `b72381ec50d4390a703d17096a4a61deae25cb2f`.
PR #13752 adds standalone caller-class adapters and their qualification records.
All 28 selected reference, proof and support files match main byte for byte.
All 1,234 prior compiler and standard-library files retain their exact bytes.
The merge adds five standalone Fern files and changes no prior compiler source.

The combined branch passes 30 caller-class, worker-protocol and import-free test nodes.
The run has zero failures and zero skips on x86-64, ARM64 and Wasm.
The generated driver checks all 1,566 exact reference cases on each target.
The reference and generated source reproduce exactly, and all eight collector self-tests pass.
Source, feature, unchanged complexity and all 2,109 formatter inputs pass.

The same producer rebuilds the merged raw compiler in 42.670 seconds.
Its bytes match the measured compiler from the phi predecessor slice.
Both hashes are `fb78dd106a460a9732db18e3d45b3f837aec9b132f3fe49488681403df6eea80`.
The identity record retains the producer hash and complete command.
This check supplies no new speed or self-reproduction result.

The old caller models, driver assertions, domains and residues remain unchanged.
The new adapters refine the prior intervals at current capacity boundaries.
Retained-driver integration and machine call summaries remain separate work under #13717.
These standalone controls do not establish complete retained-machine recovery.

The [prior merge evidence](../main-wide-machine-merge-2026-10-28/README.md) records the separate machine checks and six exact CI baseline updates.
The baseline tolerances remain unchanged.
This merge supplies no new optimization measurement.
The new published head requires complete applicable CI, and issue #4412 remains incomplete.
