# Exact large reservations in the i64 heap-counter probe

Native child #13710 records a fresh main failure in the existing large-allocation probe.
The unchanged main test fails once, while both original i64 controls pass with zero skips.
The official b42 main job reports the same failing root and exit code.

The test requests two live arrays with 1,200,000,000 bytes each.
The allocator reserves each block through its existing large-tier size class.
A separate current-compiler probe observes 1,342,177,280 bytes after the first reservation and 2,684,354,560 bytes after the second.
The old upper estimate of 2,500,000,000 bytes rejects that correct reservation total.
The large-class emitter remains byte for byte identical between earlier main 11c and current main 23c5.

The correction replaces that estimate with exact equality to 2,684,354,560 bytes.
Both allocation sizes, both live array-length checks, and the successful exit code remain unchanged.
The explicit i64 counter, above-2-GiB assertion, and minimum logical-request assertion also remain unchanged.
The correction changes no allocator behavior, size class, production compiler, baseline, tolerance, or global gate.
The exact assertion also rejects a one-byte incorrect counter.

All seven selected tests pass with zero failures and zero skips.
They cover both i64 controls, the large-allocation probe, bounded reuse on three targets, and large-class integrity.
These observations do not qualify the whole ARM harness or the complete e2e suite.
The test commands retain their original workload and assertions outside the exact reservation correction.

Three controlled overlays must fail the corrected original root.
Signed-i32 truncation and a zero counter both trigger the original below-2-GiB error.
A one-byte incorrect counter triggers the exact-reservation error.
Every control returns the required nonzero test result, and the production test file remains unchanged.

The first control coordinator expected the generic error text for the signed-i32 case.
The Go test uses a specific case-one message instead.
The preserved log proves the required truncation failure.
The corrected matcher qualifies that completed observation without another run.
Only the two unfinished controls execute afterward.

Go build and vet pass for all packages.
The complete source-audit suite passes 219 tests with zero skips.
The modified file passes gofmt and diff checks.
The dense checkout contains all tracked files before those gates.

The successful test run precedes the source commit and retains actual source hashes.
The candidate provenance explicitly records the uncommitted feature state.
The committed test bytes must match the qualified candidate hash exactly.
The stock self-host harness uses its pinned bootstrap compiler and current sources.
The separate reservation probe retains the actual clean b42 CLI and binary hashes.

An initial output probe used a nonexistent io helper and failed before execution.
The corrected output probe uses the canonical print builtin without a compiler or allocator change.
Both source forms and compiler output remain preserved.
No code or test change derives from that exploratory API error.

The reported counter measures reserved arena growth, not resident memory or a physical/process bound.
The two performance jobs in the same main run fail a separate strict size-report gate.
This correction does not qualify those jobs or authorize a baseline change.
Actual main CI recovery and all M5 and epic acceptance gates remain open.
