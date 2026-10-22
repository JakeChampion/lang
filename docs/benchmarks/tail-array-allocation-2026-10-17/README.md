# Tail calls to the native array allocator, 2026-10-17

An immediately returned raw_arr_box now uses the native allocator's tail-call
path. This applies to both native selectors, including saved registers,
stack arguments and ARM64 hardened epilogues. Calls with work after allocation
retain their ordinary call. Profiling retains ordinary calls for stack capture.
The allocator has an integer ABI independently of any user function named
raw_arr_box; a regression exercises that collision with a live saved value.

This is independent of the rejected typed allocation-reuse experiment. The
handwritten reuse helpers and their ABI are unchanged.

Source ef9210850 starts from main 4fdf2e7ee. Targeted native execution covers
GCC and the in-process writer, both architectures, sanitizers, ARM64 hardening,
exact allocation/free counts and the existing reuse and profiling regressions;
it passes in 86.517 seconds. The collision fixture fails with the initial
experimental float-mask lookup on ARM64, and succeeds on unmodified main and
with the corrected integer allocator ABI. The before log is retained as
negative test evidence, not an accepted build.

All three bootstrap stages are identical at 12,669,440 bytes, SHA-256
 dc4cefc16f5987336b93a1ceaf9867602d9c03dcb5952ab977c4e0e06a8fb32d.
All eleven strict size checks pass with complete coverage and unchanged
baselines/tolerance (98.139 seconds).

The fixed probe calls a non-inlined allocation wrapper one million times,
varies capacity from one to eight, drops each array and checks a length sum of
4,500,000. Cachegrind reports 57,001,272 baseline versus 52,001,272 candidate
instructions: exactly five fewer instructions per call (8.77% of this small
probe). The old wrapper saves a return address and calls/returns; the new
wrapper jumps to the allocator after restoring its frame. This is an
instruction-count result for this probe, not a native elapsed-time or
whole-compiler speed claim. No native elapsed timings were collected.

Run reproduce.py with baseline compiler, candidate compiler, stdlib directory
and output directory. It records source/compiler/binary hashes, verifies the
checksum and writes full Cachegrind profiles. FERN_BENCH_CPU selects a core.
Source and proof hashes above refer to the recorded main, not later integration.

Refs #2649 and #12548; neither epic is complete from this change alone.

## Integration with main bbadbc3ff

Source b6f1f51ee reapplies the allocator tail-call change to main bbadbc3ff.
Allocator tail calls, semantic tail recursion, unique-tail release, reuse
and loop reuse including WASI, CPU/allocation profiling, C-call, inventory
and complexity checks pass in 153.515 seconds.

All three bootstrap stages match at 12,694,048 bytes, SHA-256
04b99b0690bb7a440673eb1f37aa14b3ce55b024cd40250f04ae6330c592b272.
All eleven strict size checks pass with complete coverage and unchanged
limits (82.054 seconds). Raw integration evidence is in main-bbad/.
The earlier instruction-count result retains its exact original source
and compiler scope; this integration makes no new performance claim.
