Parent: #13687. This scope records an independently reproduced main failure.

The b42 main run fails `TestX86_64HeapBumpAbove2GiB` in job 122471883743.
Fresh main 23c5 reproduces that failure with two passing i64 controls and zero skips.
The probe allocates two arrays with 1,200,000,000 bytes each and rejects an arena bump above 2,500,000,000 bytes.

A separate current-compiler observation records 1,342,177,280 bytes after the first reservation and 2,684,354,560 bytes after the second.
The existing large-tier size class rounds each request to that reservation capacity.
The large-class emitter is byte for byte identical between earlier main 11c and current main 23c5.
The current failure does not prove a new allocator regression or a counter truncation.
These values describe arena reservations, not resident memory.

Predeclared correction:

- Preserve both 1,200,000,000-byte allocations and both live array-length checks.
- Preserve the explicit signed i64 counter, the above-2-GiB check, and the minimum logical-request check.
- Replace the stale 2.5-billion upper estimate with exact equality to 2,684,354,560 reservation bytes.
- Preserve successful exit code two and all failure handling.
- Require controlled signed-i32 truncation, a zero counter, and a one-byte incorrect counter to fail for their exact reasons.
- Change no production compiler, allocator, size class, workload, baseline, tolerance, or global validation gate.

Acceptance:

- [ ] Preserve fresh-main failure, exact reservation observations, and source/tool identities.
- [ ] Correct the stale fixture estimate with an exact reservation assertion and unchanged allocation workload.
- [ ] Pass the original i64 controls and meaningful counter-mutation controls without skips.
- [ ] Pass adjacent heap-counter and large-class integrity checks, format, build, vet, and selected source checks.
- [ ] Merge one READY PR normally at the exact full head and publish bounded evidence.

The two performance jobs in the same main run fail a separate strict size-report gate.
Their reports require separate cause analysis and do not authorize a baseline or threshold change here.
Actual CI recovery, M5, and epic acceptance remain open until their own gates pass.
