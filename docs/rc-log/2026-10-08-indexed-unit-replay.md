# Indexed counted-unit replay

Refs #11908. Plan replay previously scanned a block's steps for each operation.
It now builds a flat index bounded by the graph's instruction and block counts,
then reads one slot per entry, operation, return or edge. The existing flow
maps sparse block IDs to graph positions. Duplicate slots retain a rejection
marker, and the final accounted-step count rejects extra steps. Plan order is
irrelevant. The physical lowerer's public lookup API is unchanged.

The new tests exercise both replay entry points with reversed plans, sparse
blocks, missing edges, duplicate steps of every kind (including three copies),
invalid targets, parameter steps and extreme invalid IDs. The latter cannot
force an index allocation sized by an untrusted plan ID.

## Measurements

Native ARM Linux, Callgrind 3.19, base
`b752aec949c84fa37b688dfb08a044203a399e33` versus this change. Both compiler
binaries were built from the official `stage0-20261006-0d42e82` pin.
The isolated probe constructs one scalar block, checks its `n + 2` plan steps,
then repeats `verify_planned`. Counts include startup and plan construction.
Every run produced the expected step and repetition counts.

| Instructions in block | Replays | Before instructions | After instructions |
|---:|---:|---:|---:|
| 32 | 10 | 557,344 | 392,786 |
| 128 | 10 | 4,167,017 | 1,421,979 |
| 128 | 100 | 37,516,836 | 10,059,508 |
| 512 | 100 | 483,240,258 | 39,219,730 |

These are isolated replay costs. Real executable compilation with
`FERN_IR_VERIFY=quiet` showed a smaller improvement:

| Source compiled to ARM Linux assembly | Before instructions | After instructions |
|---|---:|---:|
| `bench/perceus_cfold.fern` | 67,410,623 | 67,383,449 |
| `compiler/drivers/checker_run.fern` | 19,559,387,419 | 19,540,958,474 |

Both assembly outputs were byte-identical. The cfold compile was the
sub-minute pilot. An earlier probe compiled `ssalive.fern` without an entry
point and emitted only startup code; it is excluded from these results.
No wall-time or whole-compiler speed claim is made.

The official Darwin ARM bootstrap reached identical stage 2 and stage 3
binaries. File size remained 12,405,793 bytes; text grew by 832 bytes and unwind
data by 184 bytes for the indexing helpers. Data and segment sizes stayed
unchanged. No performance baselines or tolerances changed.

## Reproduction and validation

The exact probe and compiler hashes are in [the evidence directory](data/replay-index/).
Copy `probe.fern.txt` to `/tmp/fern-replay-cost.fern` and link
`/tmp/fern-replay-modules` to the checkout's `compiler` directory. Compile it
with the same base primary compiler against each source tree, then run its
binary under Callgrind with the two arguments from each table row.

For each before/after compiler, run this command on the same checkout and
native ARM Linux host, first with the cfold entry and then the checker driver:

```sh
FERN_IR_VERIFY=quiet valgrind --tool=callgrind \
  --callgrind-out-file=compile.callgrind "$compiler" \
  -target arm64-linux -emit asm compiler/drivers/checker_run.fern \
  internal/stdlib > checker.s
```

Validation passed: all unit-plan cases on ARM Linux, x86-64 Linux and WASI;
the indexed cases with strict IR, sanitizer and balanced allocation census on
all three targets; semantic source RC tests; the direct native
`irverify_run.fern` assertions; full lint and official bootstrap fixed point.
The x86-only Go wrapper for the reuse verifier skipped on these ARM hosts,
so its Fern driver was compiled and executed directly on Darwin ARM instead.

This is the replay-cost slice of #11908. Independent certification of emitted
RC, complete compiler/conformance coverage, the combined overhead budget and
default activation remain unfinished. Replay remains opt-in.
