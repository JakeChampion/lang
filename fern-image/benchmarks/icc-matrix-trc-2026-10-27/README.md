# Matrix/TRC composition qualification

Native child #13683 declares its criteria before implementation.
PR #13684 supplies source `581469fb562de3c41890722a986b65e49726f644` and the later evidence commit.
The source commit precedes all three final immutable campaigns.
See [the API contract](../../docs/icc-matrix-trc.md) for scope, criteria, and limitations.

`campaign-summary.json` reports the actual results and raw record hashes.
The new raw records are `../results/icc-matrix-trc-{native,wasm,arm64}.json`.
Every target checks 2,824 cases through 353 fresh workers with nonzero balanced zero-live heaps.
The sample records agree exactly across targets.
All primary criteria and the 1,264 applicable secondary CMM comparisons pass without tolerance changes.

The host reruns the existing offline CMM before each Fern campaign.
Neither Wasm nor ARM workers execute CMM code.
The records identify formats, flags, intent, library, versions, profile, and all selected source/tool hashes.
The frozen profile supplies the only CMM comparison set.
Other models receive all primary mathematical checks.

`full-{target}.json` and logs retain complete regression commands, exits, counts, and source inventories.
`committed-go.*`, `complexity.log`, and `aux.*` retain the other checks.
The complete native/Wasm scripts include their existing smoke, negative, ownership, and format checks.
Standalone ARM evidence covers 40 executables, not the whole Go ARM harness.

The compiler remains the clean source-82 binary because compiler and stdlib implementation bytes match this source.
`compiler-provenance.json` identifies its prior successful standard two-stage build and current binary hashes.
No redundant compiler rebuild qualifies this app-only change.

`initial-wasm.*` preserves the actual failed fixture-preopen run.
It reports zero passing groups, six failures, and a balanced heap.
`wasm-preopen-fixed.*` records the corrected runner against the same module with unchanged source and binary hashes.
There is no compiler or core numerical defect claim from that runner failure.

The other initial unit records use uncommitted feature files.
`provisional-summary.json` identifies the separate earlier native campaign and its full external record hash.
These development observations do not qualify the later source-pinned campaigns.
No historical published record is replaced or relabeled.

Local qualification is complete.
Pending, queued, failed, cancelled, or skipped substantive CI does not count as a pass.
The parent ICC, M5, and epic acceptance gates remain open.
