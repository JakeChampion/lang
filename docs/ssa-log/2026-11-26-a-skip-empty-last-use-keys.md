# Skip position keys when the last-use index is empty

Part of #11534 and #8171.

The ownership checker builds one last-use index per function on the first lookup.
The local check `ow_local_last_use` and the argument check `ow_dies_after_copies` can each look up two entries for one identifier.
They now read the index once. If it is empty, they return before they convert the line and column to a string.
With a nonempty index, the checks use the same keys and evaluate the same ownership rules.
The index still distinguishes copies, borrowed values and field selections.

## Stage-2 measurements

I built both compilers through stage 2 from `2bc2f3f187fa`.
Each compiler processed the same base sources on x86-64 Linux with verification on and the certifier memo off.
Callgrind counted the instructions. A negative change means fewer instructions.

| Workload | Base | Branch | Change | Percent |
| --- | ---: | ---: | ---: | ---: |
| Compile the fixed checker | 57,263,293,264 | 57,262,983,980 | -309,284 | -0.0005% |
| Check the fixed checker | 15,113,648,344 | 15,113,340,918 | -307,426 | -0.0020% |
| Check the last-use local fixture | 26,934,391 | 26,932,630 | -1,761 | -0.0065% |
| Check the destructured local fixture | 11,245,842 | 11,245,575 | -267 | -0.0024% |
| Check the borrowed field fixture | 141,388,422 | 141,386,695 | -1,727 | -0.0012% |
| Check the borrowed-string control | 27,223,284 | 27,223,647 | +363 | +0.0013% |

The control generates 60 functions. Each passes a borrowed string to the same reader three times.
The fixed checker compile used 72,723,728 heap allocations on the base and 72,723,454 on the branch.
Both runs finished with zero live bytes.

## Verification

Both stage-2 compilers reproduced themselves. All 6,222 compile outcomes matched. All 4,449 emitted binaries were identical.
All 518 expected-error fixtures gave identical JSON diagnostics.
The fixed checker binaries matched on six targets. The HTTP component and the assembly outputs for `arm64-freestanding` and `x86-64-freestanding` also matched.

All 41 ownership and match programs passed on x86-64 Linux, ARM64 Linux and WASI.
All 123 binary pairs matched. The ownership probes passed 36 allocation-balance checks and three x86-64 sanitizer checks.

The x86-64 census for `ow_guard_call` refuted two of three planted imbalances and weakened one. The base refuted all three.
The total refuted count fell from 5,344 to 5,343. The weakened count rose from 227 to 228. The missed count stayed at 227.
