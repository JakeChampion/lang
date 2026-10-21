# String transform allocation sizes

Empty native `repeat` and all-deleting `replace` results allocated one
payload byte but recorded a logical length of zero. Release derives the
allocation size from that length, so allocation and release disagreed.
The runtime now returns the empty literal for these results and allocates
exactly the payload length for nonempty results.

A nine-case probe compares reproduced compilers before and after the fix.
The parent's empty-result cases report three allocations, three frees and
eight live bytes. The corrected cases report two allocations, two frees
and zero live bytes. Nonempty and no-match controls preserve their allocation
counts and remain balanced. Checking block counts alone misses this defect.

Both native and WASM replacement helpers now compute the expanded length
in 64 bits before narrowing or allocating. The native helper rejects lengths
above the signed 32-bit string limit. WASM reserves the same header and
allocator headroom as its repeat helper. The overflow regression uses two
64 KiB inputs whose replacement would produce 4 GiB. Native exits with the
allocation-size diagnostic; WASM hits the explicit `unreachable` guard.
Previously WASM failed with an out-of-bounds access. The strengthened test
requires the guard trap and rejects an out-of-bounds fault.

An empty repeat also returns without looping over the requested count.
WASM retains its existing, balanced empty-string box representation; the
native helper uses the empty literal.

The initial validation on `3ad0b7e2d` covers zero and negative counts,
empty input repeated
2,147,483,647 times, Unicode, retained aliases, concatenation, and nonempty
controls. The matrix passes on x86-64 Linux, ARM64 Linux and core WASM;
the Darwin test passes in 41.080 seconds. Full Linux unit tests and every
lint gate pass. Programs built by the reproduced compiler pass all ten
alias/value cases on Darwin and core WASM with zero live bytes, and reject
the oversized replacement before copying output.

The bootstrap uses `stage0-20261001-c891ebc`, pinned ARM64 Darwin SHA-256
`3346bfdb2486d006e63af244738d055c0368b1d9c1915ab42ec9be6225823d27`.
Stage 1 takes 35 seconds; stages 2 and 3 take 27 and 14 seconds and are
byte-identical. Both occupy 12,811,441 bytes, with SHA-256
`a518ac78b011ab3eb8ae4ddc1f66a04d04269d74cac00333956743a74b229595`.

The size comparison uses the same probe and standard library, before and
after this runtime change, based on diagnostics integration `3ad0b7e2d`.
The before compiler has SHA-256
`5c35f0360b43e387ff9fc1775e6c12dba8b0a02465b6488f9aec8e1a235aed77`.
The intervening diagnostics test repair did not change compiler sources.

| Artifact | Before | After |
| --- | ---: | ---: |
| Compiler file | 12,811,425 B | 12,811,441 B |
| Compiler code | 11,017,712 B | 11,018,032 B |
| Compiler data | 993,816 B | 994,072 B |
| Darwin probe file | 49,713 B | 49,713 B |
| Darwin probe code | 18,228 B | 18,336 B |
| Darwin probe unwind information | 3,268 B | 3,284 B |
| Core WASM probe | 13,046 B | 13,079 B |

The added code implements the empty-result branches and checked lengths;
the compiler also carries the new WASM instruction text. Both probe versions
produce identical output for a nonempty Unicode input. No size baseline
was changed.

Integration with main `1e402c138` passes the native/WASM transform matrix
in 74.347 seconds, every lint gate, and the Darwin regression in 38.740
seconds. A fresh bootstrap takes 36, 28 and 15 seconds for stages 1, 2 and
3. Stages 2 and 3 are byte-identical at 12,811,649 bytes, SHA-256
`96e080e9f7f3d08a5335f312b4f98deed03a796a5bcce9effb66830b1ed5817a`.
The reproduced compiler passes all ten alias/value probes on Darwin and
core WASM with zero live bytes, and both oversized replacement guards.
The size table above isolates the runtime correction before this integration;
the newer compiler also includes upstream changes. Full integrated unit
and target checks will run in CI before merge.

The subsequent integration with main `d46426a01` includes upstream fixes for
the seccomp job's missing compiler sources and counted-pointer ownership in
the snapshot corpus. The full ARM64 backend comparison passes in 146.653
seconds. Direct x86-64 flat/SSA executions agree on stdout, stderr and all
seven snapshot assertions. The map ownership census passes across native
and WASM targets in 23.928 seconds. Seccomp execution still requires the
fresh x86-64 Linux CI run; the local ARM64 host skips that gate.

Two remaining timezone integration repairs add `std/tz` to the docs sidebar
and replace the obsolete fixed-offset lookup fixture in the primary matrix
with the new transition/TZif suite. Its interpreter and both primary native
targets pass. The repaired integration passes the full unit suite, all lint
gates, the transform target matrix, and Darwin regressions (38.648 seconds).

This fresh bootstrap takes 35, 27 and 14 seconds. Stages 2 and 3 match at
12,811,697 bytes, SHA-256
`d81f52b72a8b3505880d6ad2549acd426f2938a3ff8a617863886ab9d64ea6df`.
Its actual Darwin/core-WASM transform probes remain balanced and both
overflow guards pass. The original size table continues to isolate this
runtime fix; later compiler sizes include upstream changes. Current-head CI
and review remain merge requirements.

The PR branch then advanced externally to `b6fa32476`, adding newer compiler
coverage and SSA changes. The combined tree preserves those changes and
passes the transform matrix (73.298 seconds), timezone matrix (22.376
seconds), every lint gate and Darwin regression (39.501 seconds). A fresh
bootstrap takes 36, 29 and 15 seconds; stages 2 and 3 match at 12,861,377
bytes, SHA-256
`44f78472a119fc30239f5d6babf96fc54cbfe2d83cb1d3836e7045dfc510fb89`.
Actual transform alias/value probes remain balanced and both overflow guards
pass. The full integrated unit and target suite is pending CI on this newer
base; the preceding full local pass applies to `d46426a01` integration.
