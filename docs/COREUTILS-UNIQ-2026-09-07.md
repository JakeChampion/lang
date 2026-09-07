# Uniq: compare buffered ranges without materialising slices

Part of #8278, adopting the existing cross-backend `__mismatch` primitive from
#8791. Two case-sensitive comparisons now operate directly on the read buffer.
The length guards, cheap last-content-byte discriminator, retained key lifetime
and case-folding path are unchanged.

## Measurement

Native ARM64 Linux on Apple M3 Pro, 12 visible CPUs, kernel
6.12.76-linuxkit, glibc 2.39. GNU coreutils 9.4 and Rust uutils 0.11.0.
Baseline source `376ecdf53`, candidate that revision plus the two comparison
changes in `coreutils/uniq.fern`. Both use the same Go-hosted Fern compiler and
`-O -target arm64-linux`. The raw report records binary and input SHA-256 hashes.

All inputs and Fern/uutils binaries reside in a Linux-local Docker volume.
`LC_ALL=C LANG=C TZ=UTC`; identical inputs, stdout to `/dev/null`, no shell
or pipeline in the timed command. Stdout digest, stderr and exit status must
agree across the four binaries before measuring each workload. This supplements
the GNU oracle corpus rather than replacing it.

Ten rounds rotate implementation order; discard the first three and report
seven samples as mean plus/minus sample standard deviation, in milliseconds.
Timing includes process startup and Python process supervision. No competing
test suite ran during measurement. No QEMU timings are used.

A 2,000-line pilot completed in 0.254 s. The same pipeline, changing only the
line count, completed at 200,000 lines in 2.518 s and 2,000,000 lines in
23.396 s. The smaller run had noisy or negligible differences on several
workloads; its [raw results](benchmarks/coreutils-uniq-2026-09-07-small.json)
are retained alongside the [larger run](benchmarks/coreutils-uniq-2026-09-07.json).

| Workload, 2,000,000 lines | Fern before | Fern after | GNU 9.4 | uutils 0.11.0 |
|---|---:|---:|---:|---:|
| Groups of four duplicate lines | 72.768 +/- 10.894 | 59.117 +/- 4.034 | 49.327 +/- 1.279 | 30.646 +/- 1.194 |
| Same groups, `-c` | 81.216 +/- 1.259 | 73.740 +/- 7.702 | 69.710 +/- 3.112 | 33.836 +/- 0.906 |
| Same groups, `-d` | 69.849 +/- 4.692 | 59.060 +/- 3.343 | 51.210 +/- 2.484 | 30.631 +/- 1.365 |
| Same groups, `-f1 -c` | 171.769 +/- 3.344 | 157.488 +/- 8.028 | 77.057 +/- 1.926 | 40.936 +/- 1.381 |
| Distinct short integers | 68.684 +/- 1.547 | 68.733 +/- 1.141 | 33.196 +/- 0.382 | 35.545 +/- 0.745 |
| Distinct short integers, `-u` | 69.539 +/- 4.228 | 69.060 +/- 1.854 | 33.483 +/- 0.585 | 37.111 +/- 3.301 |
| Distinct 44-byte path-like lines | 118.795 +/- 14.011 | 97.951 +/- 4.160 | 175.809 +/- 4.825 | 42.629 +/- 0.952 |

Duplicate and path-like workloads improve; distinct short integers are
essentially unchanged. The latter usually fail the existing last-byte guard
before reaching the changed comparison. Fern remains slower than uutils on
every measured workload and slower than GNU except the path-like workload.
The result supports this focused adoption, not an overall-fastest claim.
Native x86-64 speed remains unmeasured.

## Reproduction and validation

In baseline and candidate worktrees, compile with the same Fern compiler:

```sh
/bench/fern -O -target arm64-linux -o /bench/uniq coreutils/uniq.fern
```

Retain the baseline as `/bench/uniq-before` and candidate as `/bench/uniq`.
Extract official uutils 0.11.0's `aarch64-unknown-linux-gnu` release so its
multicall executable is `/bench/uutils/coreutils`. GNU 9.4+ must be installed
in `/usr/bin`. The [experiment script](benchmarks/coreutils-uniq-2026-09-07.py)
requires a disposable Linux-local `/bench` directory or volume and overwrites
its generated inputs and report names.

```sh
uv run --script docs/benchmarks/coreutils-uniq-2026-09-07.py 2000 uniq-run
uv run --script docs/benchmarks/coreutils-uniq-2026-09-07.py 200000 uniq-run
uv run --script docs/benchmarks/coreutils-uniq-2026-09-07.py 2000000 uniq-run
```

The expanded native GNU and self-hosted oracle corpus passed (13.245 s),
including comparisons whose last byte matches while an earlier byte differs,
NUL/high bytes, kernel boundaries, key widths and records crossing the 64 KiB
read boundary. `make lint-all` and the full `go test ./... -timeout=60m` suite
passed (exit 0): coreutils 215.319 s, e2e 1621.425 s, e2eselfhost 117.354 s.
This is the native ARM64 Linux suite, not a claim that every separate x86-64
self-host CI lane on main is green.
