Current-main reversal integration experiment: rejected as a general speedup

Base: c78ff2894784ac34c73445ae2d5508e44bc493c5.
Both fixture binaries use ONE compiler, built by verified stage0
stage0-20261009-823f7b1 from that main's compiler sources and baseline stdlib.
The compiler SHA-256 is
afeadb8b4fc7189e93971634762d055dfd83acbb78eae44c8e478f1ae9041506.
Stage0 SHA-256:
9e94047df7120c0527840ee34235f4bbb88c309466c890d41e4d655955fbd80a.
Baseline/candidate release fixture SHA-256:
7442e43c78d558fca65d1df5600e8c729ad4131fce5e87001dc4bf0a290ca423,
72708624d87bb74ef8e41b0878bfa73c2e5cf882663e91582a7a847bbbd8bd24.
The only fixture-compilation difference is std/array.reverse from reversal.patch.gz
(uncompressed SHA eb72b4e07040e02dda00aa96cb6c062acd7c1859d90218e14947d763cd9adb7d).
No compiler fast path, sole-box rule, dynamic-call bracket or prerequisite
implementation is replaced. Baseline source stays unchanged.

Use the unchanged reverse.fern fixture in ../array-reverse-2026-10-13:
2000 reversals of 32 or 10000 i32 elements; the shared mode retains and checks
the prior array. Compile each with the same driver, -O -target x86-64-linux,
and its respective stdlib. No heap/RC instrumentation is enabled for timing.
All eight binary/argument controls exit zero with empty stdout and stderr.

Hyperfine 2.0.0 -N --warmup 5 --runs 100, both command orders, on an idle
Linux x86-64 cloud worker, LC_ALL=C LANG=C TZ=UTC. Raw JSON gzip exports retain
every sample, exit status, wall/user/system time and peak resident memory.
These are native timings, not QEMU timings. summary.json.gz reproduces means.
Positive percentages below mean SLOWER, negative percentages mean faster.

Case                    baseline ms   candidate ms   candidate change
32 unique, base first       0.251689       0.198424          -21.2%
32 unique, new first        0.261145       0.216017          -17.3%
32 shared, base first       0.252533       0.229675           -9.1%
32 shared, new first        0.239019       0.209078          -12.5%
10000 unique, base first   23.690018      27.887885          +17.7%
10000 unique, new first    23.387896      28.285333          +20.9%
10000 shared, base first   22.521223      26.947365          +19.7%
10000 shared, new first    22.555828      24.515671           +8.7%

Small cases include process startup. Large shared base-first candidate has
high variance (standard deviation 10.003 ms); the reversed order is still
slower. Small wins do NOT justify a general reversal speedup or changing
the recorded baseline. This candidate is not submitted as a performance PR.
Its allocation gains do not override the measured large-array slowdown.

Current-main correctness gates: all seven focused top tests (18 backend/
resource legs plus the 19-case structural suite) pass in 25.575 s. All 27
selected borrowed-with/dynamic-call/alias/physical-RC/semantic/reuse/FIP
gates pass in 198.066 s, no skips. Discovery, real terminal events, source
SHA-256 and explicit coordinator exit zero were audited. Vet, Go formatting
and Fern formatting pass. This is targeted integrated-head evidence, NOT
a full integrated-suite/CI pass. The older full frozen-source validation
and its 32.7% timing result are separate experiments, not this compiler.

The correct follow-up is to understand the guarded-update cost without
restoring the retired unconditional sole-result inference or weakening
alias, unit-plan, allocation or verifier checks. A future candidate needs
its own correctness gates and controlled measurements before being proposed.

Separate Callgrind 3.27.1 Ir-only profiles of GNU-as-linked assembly copies,
large unique case (not timing inputs), report 336321009 baseline instructions
and 390332997 candidate instructions (+16.1%). RC header-write attribution:
baseline increment/decrement/zero stores 0/0/26016; candidate 2000/2000/16.
Those stores are not all attempted RC operations. The candidate's hot loop
includes element bounds checks, borrowed-root identity checks and runtime
uniqueness tests. Allocation reduction alone does not remove those costs.
The saved compressed instruction exports, matched profiling binaries and
summaries reproduce this count with ../array-reverse-2026-10-13/summarize-profile.py.

A subsequent spelling experiment places out[j] directly in the first with
instead of binding a right local. It produces a BYTE-IDENTICAL optimized
release binary (same candidate SHA above), so it is discarded without claiming
an optimization or manufacturing another timing result. The original candidate
source is restored; the archived patch and baseline remain unchanged.
