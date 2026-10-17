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

A separate 24-run before/after probe uses the actual current-main compiler
(`c7a08b71fdf8e9150c2e15e3bbf4fc194698ffd47f61139af74a13cdbf43b2d2`)
and fixed compiler above. It reproduces the large mismatched-donor leak on
both x86-64 and QEMU ARM64, while small donors and capacity matches balance.
Every fixed case balances. All counts and diagnostic text are retained in
`before-after.json`; these runs use the sanitizer, not elapsed-time measures.

Fresh integration on main bbadbc3ff (source 18b830692) passes the combined
reuse, loop-reuse, sanitizer, semantic-differential, certification and
calling-convention suite in 115.594 seconds. Stages 2 and 3 are identical
at 12,693,904 bytes, SHA-256
8481cb259fe98e9695e294d719272c0aa964d3ebc0ecf812319860b8d8e9ac63.
All eleven strict size checks pass with complete coverage, unchanged
baselines and the same five-percent tolerance (84.538 seconds).
Raw integration evidence uses the main-bbad- prefix. Earlier before/after
measurements retain their original 4fdf2e7ee source and compiler hashes.

## Hardened ARM64 release frame

Source 457382f7a also signs the mismatch path's saved return address under
ARM64 hardening and authenticates it after the destructor returns. Null
and matching donors keep their existing paths. The ABI matrix now executes
80 probes, including all eight capacity cases under ARM64 hardening. With
PAC enforcement, flipping a bit in this frame's saved PAC makes the large
mismatch probe fault. Replacing authentication with signature stripping
restores its balanced normal return, proving that the fault comes from
authentication rather than damage to the address bits. The existing
hardening, image, backtrace and refusal tests also pass. The final focused
runs pass with both the pinned producer (3.765 seconds) and the self-built
stage-3 producer (3.777 seconds).

The follow-up reuse, loop reuse including WASI, certifier, C-call,
inventory and complexity suite passes in 59.303 seconds. Stages 2 and 3
match at 12,693,936 bytes, SHA-256
f80d5b3c0de1a56a97f3089a0cc98bf3bced4a66ca95c04fd39c1b7e2f415d3f.
Stage 1 differs; the full bootstrap metadata records all three hashes.
All eleven strict size checks pass with complete coverage and unchanged
limits (50.322 seconds).

The uninstrumented allocator probe is byte-identical before and after this
hardening follow-up on both native Linux targets. Commands, compiler and
program hashes, the initial missing-signature regression, final PAC controls,
and all bootstrap and size evidence are retained in hardened-main-bbad/.
The initial remove-signature control assumed an unsigned address could never
authenticate; a zero PAC can make that assumption false. Its self-hosted
failure is retained as stage3-producer-unsigned-collision.log. The final
bit-flip and stripping controls avoid that assumption. The earlier large-donor
leak comparisons retain their recorded source scope.
