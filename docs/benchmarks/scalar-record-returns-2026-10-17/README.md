# Scalar record returns, 2026-10-17

`results.json` compares compiler kernels on main (compiler SHA-256
`6ba6e1d017f1e95c8c6137f91ab78c38145f5d62c8760de1ab2d86b94902e624`)
with scalar record returns after integration (compiler SHA-256
`dc58f74584b2490ec619cfba527aa69c8b58c15be9cd9015279b2f497b3adb0f`).
The source commit, input hashes, executable hashes, every allocation count,
checksum and elapsed observation are in the JSON. The candidate's bootstrap
stages 2 and 3 are identical, 12,641,472 bytes.

| Program | Baseline allocations | Candidate allocations |
| --- | ---: | ---: |
| One million four-field returns | 1,000,000 | 0 |
| Three scalar-only callers of the real field25519 multiply | 30,000 | 0 |
| 64 RFC 7748 public keys | 107,520 | 107,520 |

All three programs have identical checksums across both compilers and all
three targets (x86-64 Linux, ARM64 Linux and WASI). The full X25519 workload
preserves its existing box donations; its ARM64 executable is byte-identical.
The field benchmark keeps both input records live across three independent
callers so it measures the returned box rather than reusing an input box.

Seven alternating x86-64 pairs have median candidate/baseline ratios 0.312,
0.895 and 0.954 respectively. Other local compiler builds overlapped these
observations. They are informational, not evidence of a whole-X25519 speedup.
Allocation and checksum comparisons are deterministic; no native ARM timing
claim is made here. ARM64 execution here uses QEMU, and WASI uses wasmtime.

Reproduce with the pinned toolchain on PATH:

```sh
python3 docs/benchmarks/scalar-record-returns-2026-10-17/reproduce.py \
  /absolute/path/to/baseline /absolute/path/to/candidate . /tmp/scalar-results
```

The regression suite separately checks sanitized execution and exact allocation
counts for one through four fields, full-width integers and usize, NaN and
signed zero, ownership cleanup, padding and tail calls, and boxed controls
(reference fields, five fields, finalizers, escaping values and function values).
Both explicit spread updates and inferred field donations retain their boxes.

The allocation-only repeat in `current-main-allocation.json` uses main
`3260020a0` (compiler `f5539d74a09bdf471614f8e84935cdce44a5273e6e46950ac804c3652a7f83b5`)
and the integrated candidate
`f395df09bc0d2fb9fa77c410384411708ac7fe941e7d56b3d6e3aeab6c5eb6b7`.
All eighteen executables are byte-identical to the corresponding earlier
outputs, and every allocation count and checksum agrees. This repeat records
no elapsed timings.

Integrating the existing ARM buffer-helper argument fix from #12590 (source
`714d7553d`) gives identical stage 2 and stage 3 at 12,699,520 bytes, SHA-256
`a0cd57950240bd9c171e7a7de7113972581c40e2d574e233834212b2da1d2013`.
The scalar, donor, helper, provided-symbol inventory and complexity tests pass
in 88.770 seconds. All eleven strict driver-size checks pass with complete
coverage and the existing 5% tolerance; the tracked baseline stays unchanged.
The fresh allocation-only repeat is in `upstream-arm-fix-allocation.json`.

`arm64-buffer-argument-repro.json` records a small actual x86-encoder consumer:
assembling `vpsrlw $3, %ymm1, %ymm2` must give bytes `c5 ed 71 d1 03`.
The program compiled for ARM64 crashes before the argument fix and exits 42
after it. Its x86 control exits 42 both ways and is byte-identical. The
permanent test executes both targets; ARM64 uses QEMU here. This tests the
misordered runtime-helper arguments without an HTTP workload.

## Integration with main cf53f2397

The integrated source `eebe7f11a` reaches identical stage 2 and stage 3 at
12,672,208 bytes, SHA-256
`c7d93477a830d5c5359bef48269cff32f54410416b689ea7b2dd3fd2d457fd9f`.
A separate checkout of main `cf53f239737ee9b330c2ccfa19d1ff81d165622d`
reaches identical stages at 12,643,888 bytes, SHA-256
`117549816eb802ee53b5a6450f69296b08848708704b913fc3d9e69ca2e9aaa3`.
The scalar-return change adds 28,320 bytes (0.22%). The tracked stage-2
baseline is reduced from 12,699,472 to the measured 12,672,208 bytes; the
other ten baselines and the 5% tolerance remain unchanged.

The focused scalar, donor, ARM helper, provided-symbol inventory and complexity
suite passes in 56.614 seconds. Discarded-call reclamation passes on all three
targets in 2.294 seconds. All eleven strict driver-size checks pass with complete
coverage. The raw logs and eighteen-row allocation-only comparison are archived
under `main-cf53-*` in this directory.

All targets preserve checksums. The generic scalar-return benchmark drops from
1,000,000 allocations to zero and field multiplication from 30,000 to zero.
Current main and the candidate both allocate 115,712 times for X25519; the
older 107,520 count above belongs to the earlier standard-library source.
This repeat records no elapsed benchmark timings.
