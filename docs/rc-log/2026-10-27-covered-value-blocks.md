# Covered value blocks and counter ownership

Issue #13679 fixes an instrumented compiler failure from nightly run 40477547285.
Job 122099388868 refused a string addition in `checker__check_module_pass`.
A reduced inferred string match compiled normally but failed with `-cover`.

Coverage inserted counter statements inside the parser result scaffold.
The checker uses that scaffold to infer match and if result types.
The final implementation retains the scaffold inside an ordinary value block.
Entry counters precede its evaluation, and branch bodies retain their counters.

A multiline control exposed a missing counter in the first candidate.
The original compiler reported source lines 2, 3, and 7.
Candidate `32d563abbc3e50fb190d503716870568eb5abf0f` reported only lines 2 and 7.
Its successful build, campaign, image checks, and allocation gate do not qualify the correction.
All first-candidate evidence remains in the proof directory with its original source identity.

The sanitizer controls also exposed an unreleased raw counter table.
The report now clears the global pointer and transfers the table to an owned string value.
Existing scope cleanup then releases its allocation through the established raw-storage ownership mechanism.
No new allocator intrinsic or validator exception is required.

The finite regression set checks inferred strings, tuples, owned arrays, if expressions, and explicit types.
It also checks taken and untaken early returns, exact multiline counters, copy-on-write behavior, and balanced heaps.
Wasm continues to reject coverage instrumentation explicitly.
The image suites exercise ordinary compilation with the current clean compiler.

The local coverage-guided campaign uses the existing 30-second default budget.
It does not qualify the nightly 25-minute campaign or general compiler correctness.
Pending, cancelled, and skipped CI remain separate from completed local checks.
No baseline, threshold, image API, or runtime codec dependency changes.

## Final local evidence

Source `82ba47cad60c55a2c0649147afd0ff40802e1c4d` precedes the final clean build and campaign.
The clean CLI reports `fern 82ba47cad60c`.
Its SHA256 is `6e95755acdba854fc94ec32aaa37d09dd8264bf4438b9952e37cc198bac5d9ab`.
The two-stage build and stage1 smoke exit successfully.

The focused test union has 31 passing nodes and no failures or skips.
Both unchanged complexity gates pass, with four passing nodes and no skips.
The final default-budget campaign completes 66 iterations and checks two discoveries against the oracle.
Its three test nodes pass without skips.
The report observes 4,624 of 253,314 counters, without a general coverage claim.

Each image target completes 276 TAP tests without failures.
The native, Wasm, and ARM runs have 78, 54, and 39 balanced zero-live censuses.
The ARM run uses 39 standalone executables under QEMU10.
This is not the whole Go ARM harness.
Source and compiler hashes remain unchanged within each run.

The unchanged allocation gate measures 35,059,803 allocations and frees against the upstream baseline of 34,951,426.
Its approximately 0.31% delta is within the existing tolerance and ceiling.
The report contains no RSS metric.
It provides no process-memory bound or performance comparison.

All 2,060 Fern formatting checks, Go build and vet, lint, test names, and CI self-checks pass.
The runtime module checker accepts five target sets, followed by successful feature and fixture source checks.
The runner tool inventory is a post-run observation.
The raw records distinguish that inventory from source and compiler immutability checks.

The [proof directory](../benchmarks/coverage-value-blocks-2026-10-27/) retains failures and superseded candidate observations.
The [validation record](../benchmarks/coverage-value-blocks-2026-10-27/validation.json) identifies each source and qualification phase.
