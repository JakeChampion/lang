# Mismatched allocation donors

On main `4fdf2e7ee`, the native reuse helper skipped release of mismatched
donors whose capacity plus three header words reached 65,536 words. The
ordinary array release already supports that tier. Both native backends now
use that release for mismatches, preserving the requested capacity across it.
Matching-capacity reuse and null-donor allocation keep their existing paths.

The ABI-level regression executes 72 probes: eight capacity pairs, three
normal/quarantine/sanitizer modes, both x86-64 calling conventions and ARM64's
stack entry. It checks the capacities immediately below and above the large
boundary, capacity matches, null donors and large replacements. It requires
balanced allocations/frees, zero live bytes, the requested capacity and a sole
owner. Normal release must allow the old capacity to reuse the donor's address;
quarantine must poison the donor and decline that reuse.

Boundary tests pass (2.031 seconds), as do broader reuse/differential/complexity
checks (76.561 seconds) and sanitizer/quarantine/heap-class checks (49.714
seconds). ARM64 executes under QEMU; these are correctness checks, with no ARM
timing claim.

Stages 2 and 3 match at 12,669,280 bytes, SHA-256
`d2accbb5aa9ffe177c683a8677a00ea085e56aeccfceb8c8cc1a6460e3f862e3`.
The unchanged main baseline is 12,669,440 bytes. All eleven strict size checks
pass with complete coverage and unchanged baselines/tolerance (82.148 seconds).
The bootstrap record includes the compiler/stdlib content fingerprint because
the fix was built before its source commit was created.
