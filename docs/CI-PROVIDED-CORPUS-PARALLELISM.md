# Parallel provided-call verification

`TestSelfHostIRVerifyProvidedCorpusClean` loads and lowers every conformance
fixture, including its imported modules, and checks that direct-call targets
resolve. Native CI run 34979953900 spent 101.45 seconds in this test. Its
serial process loop is independent work suitable for multiple cores.

## Change and coverage

Each fixture is a parallel subtest with a separate staging directory. All
sibling Fern modules travel with the entry point, and each directory contains
links to the same read-only standard library. The compiled verifier is also
shared read-only. Each child writes its own result slot; the parent aggregates
only after the synchronous `cases` group finishes. That group also ensures
the top-level elapsed time includes all children for CI shard weighting.

Every verifier subprocess reserves 5 GiB from the existing process-wide
memory budget. Go's `-parallel` setting and that budget both bound concurrency.
The reservation also coordinates with expensive driver builds elsewhere in
the test process. It is a scheduling reservation, not an OS memory limit.

The full run retains the minimum of 400 fixtures and 2,000 resolved calls,
the two named deliberately invalid fixtures, and rejection of any unexpected
dirty or unexpectedly clean verdict. Each fixture must produce a well-formed
verifier header and tally consistent with its exit status: 0 for clean or 1
for verification problems. Signals, arena exhaustion, other error exits and
missing or malformed output fail even for an expected-dirty fixture. Focused
subtest filters
check every selected verdict; the aggregate call floor applies to full runs.

## Final native measurement

Host: Mac15,6, Apple M3 Pro, 12 CPUs, 36 GiB RAM, Go 1.26.0 darwin/arm64.
Both variants used the same native ARM64 Darwin verifier built from
`asm_modload_run.fern` at `394d0edd6`, whose compiler sources match the
source-staging merge `056b68bc8`. Driver construction was excluded.
The old serial test body and the new helper were invoked by temporary Go
tests using that prebuilt executable. No QEMU or generated program ran.

Four fresh test processes ran in before/after/after/before order, with no
other heavy local work. `/usr/bin/time -l` captured CPU and memory statistics.
The parallel variant used `-test.parallel=4`; the macOS fallback memory budget
of 12,000 MiB admitted at most two simultaneous 5 GiB reservations.

| Trial | Wall seconds | CPU seconds | Reported maximum RSS bytes |
| --- | ---: | ---: | ---: |
| Before 1 | 55.664 | 53.06 | 1894989824 |
| After 1 | 28.330 | 55.47 | 2146877440 |
| After 2 | 28.380 | 55.57 | 2134343680 |
| Before 2 | 54.568 | 53.22 | 3122724864 |

Mean wall time falls from 55.116 to 28.355 seconds, a 1.94x speedup for this
verifier sweep, with 4.5% more CPU time. Every trial passed all 581 fixtures
and counted 325,900 resolved calls. This is not a whole-CI speedup. The RSS
statistic does not replace measuring total concurrent memory on CI runners.

These measurements include the final per-fixture verdict checks and the
compiler/library sources at `056b68bc8`. Every fixture and resolved-call count was
verified from each trial log.

The initial native race pilot covered `multi_file`, `pub_use_reexport` and
the deliberately invalid `diag_e065`. The complete 581-fixture run then
passed with race detection and `-parallel 4`. Source lint also passed.

The same focused and full runs also passed with the actual Linux x86-64
verifier executed under QEMU in the Linux ARM64 devbox, again checking all
581 fixtures and 325,900 calls with race detection. The full Go test process
took 258.012 seconds. That emulated run is correctness evidence only.
Package vet, formatting, test selectors and pinned actionlint also passed.

After updating the branch to source-staging head `e70f0698a` and its current
compiler/library sources, the same focused Linux pilot passed with the
stricter verdict validation. Regression cases cover expected and unexpected
diagnostics, clean results, exit 125, exit 137, signals, missing or truncated
tallies, overflowing counts, and disagreement between header and status.
The complete Linux x86-under-QEMU run at that source revision passed all 581 fixtures
and all verdict regressions with race detection, resolving 325,900 direct
calls. Package time was 453.837 seconds while another QEMU bootstrap test
overlapped part of the run. This establishes correctness only. All other
heavy local work finished before the native timing trials above.

Main later advanced to `5a1247235`, with additional compiler changes. The
measurements and full-run evidence above describe their recorded revisions;
integration validation against that newer compiler is tracked separately.
The Go CLI and native ARM64 Darwin verifier were rebuilt from the integrated
sources. A three-fixture race pilot passed, followed by the complete corpus:
581 fixtures and 325,900 direct calls passed with race detection (31.158s
package time). Verdict regression tests, full source lint, package vet,
formatting and test selectors also pass. This integration run is not a new
controlled speed comparison; full current-head CI remains required.

## Reproduction on native Linux x86-64

Run the focused case selection first:

```sh
go test ./internal/e2eselfhost \
  -run '^TestSelfHostIRVerifyProvidedCorpusClean$/cases/^(multi_file|pub_use_reexport|diag_e065)$' \
  -race -parallel 4 -count=1 -v
```

Then remove the case filter to validate the complete corpus:

```sh
go test ./internal/e2eselfhost \
  -run '^TestSelfHostIRVerifyProvidedCorpusClean$' \
  -race -parallel 4 -count=1 -v -timeout 10m
```

For a scheduling comparison on the actual CI runner, compile the test binary
once, warm the same driver cache, and repeat the full test with `-parallel 1`
and `-parallel 4` in alternating order. Record CPU, aggregate memory and the
top-level elapsed time together with fixture and call counts. Rebalance shard
weights from native CI evidence; parallel child durations overlap and must
not be summed as wall time.
