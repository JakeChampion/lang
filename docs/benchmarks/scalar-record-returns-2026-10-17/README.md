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
