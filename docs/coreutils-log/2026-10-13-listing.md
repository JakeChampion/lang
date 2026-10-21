# Listing widths and current contenders

Work for #8278. Base: `3243400380ec94cc3ff825d21b1894b8c5d93587`.
Host: Linux x86-64, AMD EPYC 9V74, five visible CPUs, kernel 6.18.44.
Both Fern binaries were built with `-O -target x86-64-linux` by the same
self-host compiler, SHA-256
`6c6c3e119cc7eeef6c27deaf96e2acb8c081103f80bb3146a5e12c537dc52af6`.
The changed sources are `coreutils/lib/listing.fern`; no compiler behavior
changed. Base binary: 376,280 bytes; changed binary: 376,344 bytes.

## Correctness and change

A directory with read permission but no search permission exposes a missing
case: GNU 9.12 prints `?` for unknown inode and block counts, but omits those
question marks from its column-width accounting. Fern counted them, changing
tab padding under `-iC`, `-ix`, `-sC` and `-siC`. The permission-boundary
corpus reproduces the discrepancy before the fix and agrees afterwards for
`ls`, `dir` and `vdir`, including different tab sizes and dereferencing.

`display_len` now calculates numeric/context prefix widths from the widths
already measured, instead of constructing formatted strings to measure them.
This also preserves GNU's unknown-count accounting. It does not remove stat
calls: GNU 9.12 still requires them for `-i` at this permission boundary.

## Wall time and other implementations

Versions: GNU coreutils 9.12, built from source with `--disable-nls
--without-selinux`; uutils 0.12.0, the current upstream x86-64 Linux musl
release; BusyBox 1.38.0, default configuration except its unrelated `tc`
applet disabled because current kernel headers lack CBQ definitions;
Toybox 0.8.15, default configuration. All are release builds.

Fixtures use `scripts/coreutils-bench.d/ls.sh`: 4,000 flat names, 1,500 mixed
entries, and a 40-deep tree. Each candidate first had stdout, stderr and
exit status compared with GNU on the same invocation under `LC_ALL=C`,
`LANG=C`, `TZ=UTC`. A differing candidate is excluded, not credited for a
faster different answer. Hyperfine 2.0.0 ran 5 warmups and exactly 100
measurements per accepted candidate, without a shell or concurrent heavy
tests. Times below are means in milliseconds; the raw exports retain
samples, deviations, process memory and CPU measurements.

| Workload | Fern base | Fern changed | GNU | uutils | BusyBox | Toybox |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 4,000 names | 2.948 | 2.849 | 2.818 | 5.270 | 6.653 | 5.991 |
| `-C -w 200` | 4.182 | 3.997 | 3.484 | differs | differs | differs |
| `-iC -w 200` | 10.586 | 9.949 | 7.993 | differs | differs | differs |
| `-sC -w 200` | 9.773 | 9.521 | 8.682 | 12.005 | differs | differs |
| `-isC -w 200` | 10.242 | 9.224 | 7.715 | differs | differs | differs |
| `-l` | 11.408 | 11.450 | 11.560 | 19.439 | differs | differs |
| `-F`, mixed entries | 1.669 | 1.677 | 1.868 | 3.348 | 2.800 | 3.027 |
| `-R`, deep tree | 0.759 | 0.735 | 1.204 | 2.742 | 3.285 | 1.660 |

The targeted `-isC` improvement is 9.9%; its mean ± deviation is
10.242 ± 0.934 ms before and 9.224 ± 0.807 ms after. Peak process RSS is
3,604,480 bytes on both. The unrelated `-l`/`-F` changes are small compared
with their deviations and are not claimed as wins. The table is a local
measurement, not proof of being the fastest implementation worldwide. GNU
still wins the numeric-column rows. BusyBox and Toybox do not beat GNU on
these matched workloads, but both beat uutils on `-F`; Toybox also beats
uutils on `-R`. They remain useful contenders, not replacements for the GNU
behavioral oracle. POSIX-focused
sbase and posixutils-rs and experimental assembly utilities were also
surveyed; no evidence established a faster GNU-compatible listing workload
there, so no speed claim is made about them.

## 2026-10-14 sbase follow-up

Sbase `c546c3a5724c81cee9a11d816a38ccdf17472129`, built with
`-O3 -march=x86-64-v3` against glibc 2.41, matches this fixture's plain-name
and `-F` output/status. Its column options fail and its long/recursive output
differs, so those modes are excluded. The same frozen Fern binaries and GNU
9.12/uutils 0.12.0/BusyBox 1.38.0/Toybox 0.8.15 references are retained; this
does not measure the later ownership pilot or current-main compiler changes.

Two idle-host rounds use five warmups and 100 samples per binary: first the
original command order, then reversed order for the two sbase-eligible modes.
Means in milliseconds:

| Workload / order | Fern changed | GNU 9.12 | uutils 0.12.0 | sbase |
| --- | ---: | ---: | ---: | ---: |
| names / original | 3.067 | 2.862 | 6.015 | 2.861 |
| names / reversed | 3.418 | 2.872 | 5.708 | 3.529 |
| `-F` / original | 1.692 | 1.880 | 3.441 | 2.361 |
| `-F` / reversed | 1.743 | 1.789 | 3.081 | 2.450 |

Sbase beats uutils on both eligible modes in both orders. It does **not**
establish a win over GNU: the first names means are effectively tied, and
the reversal loses. Order sensitivity and reference variance also prevent
using this rerun to strengthen earlier Fern/GNU speed claims. The numeric
controls are explicitly order-sensitive too:

| Workload / order | Fern base | Fern changed | GNU 9.12 |
| --- | ---: | ---: | ---: |
| `-sC` / original | 10.772 | 11.592 | 10.330 |
| `-sC` / reversed | 11.897 | 10.325 | 7.812 |
| `-isC` / original | 11.655 | 13.705 | 9.357 |
| `-isC` / reversed | 11.629 | 10.665 | 9.900 |

The changed binary loses to the base in one order and wins in the other;
GNU wins both numeric rows in both orders. The earlier observed 9.9% result
is therefore not a uniformly reproducible wall-time claim across sessions
and orders. The separately recorded allocation/instruction reductions do
not override these runtime results. No baseline is changed to hide them.
Raw samples, exclusions,
commands and binary identities are in `docs/benchmarks/ls-sbase-2026-10-14/`.

## Separate instrumentation

Same `-isC -w 200` workload, separate runs from wall-time measurements:

| Metric | Base | Changed |
| --- | ---: | ---: |
| Callgrind 3.27.1 instructions | 48,335,020 | 42,618,625 |
| `FERN_RC_TRACE=1` allocations | 159,930 | 127,931 |
| Allocated bytes | 10,329,296 | 9,278,104 |
| Peak live bytes | 2,857,592 | 2,857,592 |
| Live bytes at exit | 0 | 0 |

The traced outputs and Callgrind outputs match the ordinary build. Traced
allocations equal frees on both builds. This change removes formatting work;
it does not claim to eliminate the remaining sort, stat or layout costs.

## Reproduction and gates

Build a checkout of the base and the changed checkout using one compiler:

```
bin/fern-selfhost -O -target x86-64-linux -o ls-base BASE/coreutils/ls.fern BASE/internal/stdlib
bin/fern-selfhost -O -target x86-64-linux -o ls-new coreutils/ls.fern internal/stdlib
```

Create the benchmark fixtures by sourcing `scripts/coreutils-bench.d/ls.sh`
with `out` naming a scratch directory. For each matched invocation, run
`hyperfine --runs 100 --warmup 5 -N --export-json results.json`, naming each
binary with `-n`. Trace builds set `FERN_RC_TRACE=1` at compile time;
Callgrind uses ordinary uninstrumented binaries. Compressed raw exports are
in `docs/benchmarks/ls-2026-10-13/`.

Local gates: the full `ls`/`dir`/`vdir` GNU 9.12 corpus, deep-path and fixture
timestamp tests, the self-host parity leg for all three utilities, the WASM
smoke suite, and the Hyperfine schema regression. ARM64 `-isC` output was
also compared with GNU under QEMU 10.0.2. `ls` itself is not a WASM utility:
its existing target gate refuses native-only cwd, tty, filesystem-mode and
xattr facilities. QEMU timings are not performance evidence.

Hyperfine 2 stores labels, wall-time summaries and exit codes differently
from version 1. The benchmark script now normalizes that schema, preserves
the actual command line, and refuses an unknown schema. The regression
executes the script's own annotation and report code on both formats,
including a candidate that refuses the workload.
