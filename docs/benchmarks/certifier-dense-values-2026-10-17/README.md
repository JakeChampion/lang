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
