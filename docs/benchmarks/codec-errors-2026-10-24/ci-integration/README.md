# CI integration checks

The source commit is `073d9b48cbdfe817fe38eeb6d1c673a78c8f9e41`.
CI run `40066011748` found one coreutils caller that still expected `Option`.
The digest reader now handles the codec `Result`.

All 1520 selected coreutils cases pass with GNU 9.12. No case failed or skipped.
The first local run used an older GNU version and failed. Its log remains in the workspace.

The two CI performance lanes supplied 14 new baseline values.
Deflate instruction counts rose by 3.0% to 23.3%, depending on the operation and target.
Emitted code also grew. The performance logs and exact values are attached.
The baseline update preserves the existing tolerances.
The HTTP benchmark row is a measurement of this shared library change.

These edits do not change compiler or standard library sources.
The bootstrap and strict driver size evidence in the parent directory still applies.
The manifest records the hashes of each compressed artifact.
