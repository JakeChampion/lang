# Dense certifier value IDs, 2026-10-17

This comparison is for source `46a3e723a700988f4892c479c32538bcc92d1217`,
before subsequent main integration. The baseline compiler is
`6ba6e1d017f1e95c8c6137f91ab78c38145f5d62c8760de1ab2d86b94902e624`;
the candidate is `4220ee09ee8910b186d675e8ac4d208b9a41ab750897c34d23d15e58dc9baef3`.
The latter reaches a 12,599,936-byte self-compilation fixed point, 4,096 bytes
larger than the baseline compiler. Memoization is disabled in every process.

Compiling the same checker source to x86-64 assembly takes 23,807,859,262
instructions before and 23,403,855,856 after, a 1.70% reduction. Cachegrind
3.24 counts instructions with cache and branch simulation disabled. The two
assembly outputs are byte-identical.

The entire native compiler has identical output binaries and full diagnostic
reports before and after, on x86-64 and ARM64 Linux. The embedded playground
compiler supplies the WASI comparison; both its executable and report also
match exactly. `results.json` records every hash. The native reports contain
7,589 lines each and the WASI reports 7,010.

Seven alternating whole-compiler timing pairs have median ratio 0.9872.
The candidate's three cold enabled/disabled pairs have median ratio 1.442:
the 15% certification-overhead target is still unmet. Other compiler builds
and tests overlapped some early observations, so no elapsed-time improvement
is claimed. Every timed output has the fixed-point hash.

The instrumented checker compilation in `allocations.json` balances all
allocations and frees with zero live bytes. Certification disabled gives
25,939,339 allocations for both compilers. Enabled gives 34,730,072 before
and 35,173,670 after (+1.28%). The additional graph-copy allocations buy
smaller repeatedly scanned tables; they are a cost of this implementation.
All four assembly outputs match byte-for-byte.

`original-mutant.json` reintroduces the original #12135 lowering defect by
changing `supplies(... built_kept ...)` to `supplies(... kept ...)`. The corrected
certifier exits 5 with the original value number and spent-unit diagnostic;
disabling certification compiles it. The resulting unsafe program is never
executed. The source is archived only to reproduce this rejection test.

Six sparse graph regressions cover unused allocation results, parameters,
branches, token assumptions, loops and an inactive terminator value field.
That last case caught an earlier prototype: the certifier counts that field
as a use even when it is inactive. Compaction preserves it and does not
change the certifier's algorithm. Old prototype timing hashes are not evidence
for this corrected implementation.

Reproduce the report/output comparisons, instruction counts and paired timings
with the pinned toolchain and Valgrind on PATH:

```sh
python3 docs/benchmarks/certifier-dense-values-2026-10-17/reproduce.py \
  /absolute/path/to/baseline /absolute/path/to/candidate . /tmp/dense-results
```

The script takes several minutes and can resume the same compiler/source
comparison from its completed parity rows. Use an idle host for timing.

After integrating the scalar ABI branch and main 3260020a0, source
`41bfc9cf314b9c83e6c8e7c7ead5d032d2d58b49` reaches an identical stage-2/stage-3
fixed point of 12,703,568 bytes, SHA-256
`4e9a2d4780364cf334a18f95f8731ac54d06eda8130119e0d14cd234d5b2e9fe`.
The combined RC-certifier, scalar-return, donor, inventory and complexity suite
passes (32.383 seconds). All eleven strict driver-size checks pass with complete
coverage (123.952 seconds); the raw report is archived here.

`integrated-parity.json` compares that compiler against the integrated scalar
baseline `f395df09bc0d2fb9fa77c410384411708ac7fe941e7d56b3d6e3aeab6c5eb6b7`
on the same final sources. Both native compiler outputs, the WASI playground,
and all three full reports match exactly. Native reports have 7,641 lines,
WASI 7,067. No earlier timing is relabeled as this integrated comparison.

After integrating the existing #12590 ARM buffer-helper argument fix, source
`abfcd5b383b6f88b8adf3c7d0cb119d76ac565bb` has identical stage 2 and stage 3,
12,703,616 bytes, SHA-256
`7eb44bdde788f66c0db3b859d5e998a112d9fd9e2bcdc38873d0c36bf517ee7a`.
The combined certifier, scalar-return, donor, actual ARM buffer-helper,
provided-symbol inventory and complexity tests pass in 44.932 seconds.
All eleven strict size checks pass in 51.689 seconds, with complete coverage
and the existing 5% tolerance.

`after-arm-fix-parity.json` compares against scalar baseline
`a0cd57950240bd9c171e7a7de7113972581c40e2d574e233834212b2da1d2013`
on those same sources. Both native compiler binaries, the WASI playground,
and all three complete reports match byte for byte. Report lengths remain
7,641 native and 7,067 WASI lines. This integration adds 4,096 bytes to that
baseline; the performance and allocation observations earlier in this file
retain their original compiler and source hashes.


After starting from merged main `2c23674c2`, source `793ae506e` reaches identical
stages 2 and 3 at 12,676,304 bytes, SHA-256
`58764bc1865144e57dbd1b1be3f8a0a199206e7a97636fe6c06e7cc4e73cfb49`.
The baseline compiler is current main's fixed point
`c7d93477a830d5c5359bef48269cff32f54410416b689ea7b2dd3fd2d457fd9f`;
its compiler sources were checked identical to the pre-squash integrated build.
The candidate adds 4,096 bytes. Focused certification, scalar-return, donor,
ARM-helper, inventory and complexity tests pass in 51.518 seconds; formatter
interpreter regressions pass in 103.664 seconds.

All eleven strict driver-size checks pass with complete coverage, keeping every
baseline and the 5% tolerance unchanged. The first warm-driver invocation built
the drivers but set the wrong report environment variable, so the strict gate
correctly failed for missing measurements. Repeating with
`FERN_DRIVER_SIZE_REPORT` records all eleven rows and passes (34.622 seconds).

`main-2c23-parity.json` records fresh byte-identical executables and complete
certification reports on x86-64 Linux, ARM64 Linux and the WASI playground.
The native reports have 7,633 lines and WASI 7,060; the reports are identical
between compilers for each target, including all original diagnostic IDs.
No elapsed performance observations are added by this integration repeat.
