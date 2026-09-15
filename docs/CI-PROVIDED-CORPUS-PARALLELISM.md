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
dirty or unexpectedly clean verdict. Abnormal subprocess termination now
fails explicitly even for an expected-dirty fixture. Focused subtest filters
check every selected verdict; the aggregate call floor applies to full runs.

## Native measurement

Host: Mac15,6, Apple M3 Pro, 12 CPUs, 36 GiB RAM, Go 1.26.0 darwin/arm64.
Both variants used the same native ARM64 Darwin verifier built from
`asm_modload_run.fern` at main `2d6ffc1a6`. Driver construction was excluded.
The old serial test body and the new helper were invoked by temporary Go
tests using that prebuilt executable. No QEMU or generated program ran.

Four fresh test processes ran in before/after/after/before order, with no
other heavy local work. `/usr/bin/time -l` captured CPU and memory statistics.
The parallel variant used `-test.parallel=4`; the macOS fallback memory budget
of 12,000 MiB admitted at most two simultaneous 5 GiB reservations.

| Trial | Wall seconds | CPU seconds | Reported maximum RSS bytes |
| --- | ---: | ---: | ---: |
| Before 1 | 55.558 | 53.42 | 2859450368 |
| After 1 | 28.889 | 55.67 | 3163406336 |
| After 2 | 28.668 | 55.94 | 3527245824 |
| Before 2 | 54.960 | 53.47 | 3523969024 |

Mean wall time falls from 55.259 to 28.778 seconds, a 1.92x speedup for this
verifier sweep, with 4.4% more CPU time. Every trial passed all 581 fixtures
and counted 325,900 resolved calls. This is not a whole-CI speedup. The RSS
statistic does not replace measuring total concurrent memory on CI runners.

The initial native race pilot covered `multi_file`, `pub_use_reexport` and
the deliberately invalid `diag_e065`. The complete 581-fixture run then
passed with race detection and `-parallel 4`. Source lint also passed.

The same focused and full runs also passed with the actual Linux x86-64
verifier executed under QEMU in the Linux ARM64 devbox, again checking all
581 fixtures and 325,900 calls with race detection. The full Go test process
took 258.012 seconds. That emulated run is correctness evidence only.
Package vet, formatting, test selectors and pinned actionlint also passed.

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
