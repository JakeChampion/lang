# Inlining small scalar-array builders

The semantic inliner admitted scalar computations and projections but not
`append` or `with`. A small helper returning a scalar array stayed a call when
several sites used it. In particular, `std/json.__json_t_node` pushed two tape
words through a separate function for every JSON value.

The leaf rule now admits these two immutable operations on non-view arrays of
scalar elements, within the existing instruction, block and caller budgets.
The count planner runs after the splice and still decides whether storage can
be reused. An `own` argument can share storage; the splice does not establish
uniqueness. Reference-element arrays retain their existing admission rules,
and `@noinline`, `fip` and `fbip` retain their masks.

## Controls and retained data

The control is the self-built compiler at `8b08453d8`, SHA-256
`097d955d04442da04d2af425237fa38711157ded8cc7d0c5bf948c4329dc8234`.
That compiler built the candidate from the control sources plus the retained
[compiler patch](benchmarks/scalar-array-leaves-2026-10-17/compiler-source.patch)
(local measurement commit `de9bf396d`); its SHA-256 is
`0b40ff31921db1f58569e0b08a50d4fcf11316b360fb32bc977870e48bfb4d6b`.
Both compile exactly the same workload sources and stdlib. The separate
self-built candidate is recorded below.

[Metadata](benchmarks/scalar-array-leaves-2026-10-17/metadata.json) retains the
compiler and source revisions, hashes, tools, CPU and corpus origins.
The probes are retained as text beside it: [one parse](benchmarks/scalar-array-leaves-2026-10-17/json-probe.fern.txt),
[200 parses](benchmarks/scalar-array-leaves-2026-10-17/json-loop-probe.fern.txt)
and [4 MiB digests](benchmarks/scalar-array-leaves-2026-10-17/crypto-probe.fern.txt).

## JSON instructions and allocations

Each process reads one corpus, calls `json.parse_document`, checks every tape
word through a checksum and prints the tape size, checksum and allocation
count. Callgrind 3.24 counts the complete process, including reading and
validating the input. This measures the same Document API before and after;
it does not compare that API with simdjson's DOM or number conversion.

| Corpus | Control instructions | Candidate instructions | Change | Parse allocations, both |
| --- | ---: | ---: | ---: | ---: |
| twitter.json | 8,793,979 | 8,159,887 | -7.21% | 15 |
| citm_catalog.json | 20,531,429 | 19,050,610 | -7.21% | 16 |
| canada.json | 54,108,871 | 50,427,878 | -6.80% | 18 |

Tape sizes and checksums are identical. [Raw instruction results](benchmarks/scalar-array-leaves-2026-10-17/json-instructions.json)
include corpus hashes. A sanitized 200-parse twitter run on the refreshed
compiler exits successfully with `allocs=3012 frees=3012 live_bytes=0`.

## Native samples

With no other local heavy jobs running, each process parses the same document
200 times and checksums every tape word. Both variants are pinned to the same
CPU. After one warm-up per variant and corpus, five pairs alternate their run
order. The elapsed time includes process launch, the one file read and output.

| Corpus | Control median | Candidate median | Elapsed change |
| --- | ---: | ---: | ---: |
| twitter.json | 111.37 ms | 103.22 ms | -7.32% |
| citm_catalog.json | 258.50 ms | 228.63 ms | -11.55% |
| canada.json | 662.20 ms | 649.67 ms | -1.89% |

[All samples and outputs](benchmarks/scalar-array-leaves-2026-10-17/native-timings.json)
are retained. Samples vary by several percent; the Canada runtime result is
inconclusive. The instruction reduction is the more stable result for it.

## Other workloads and compiler cost

All 35 frozen standard benchmark binaries are byte-identical, with equal output
and exit status. Their [instruction results](benchmarks/scalar-array-leaves-2026-10-17/benchmark-instructions.json)
include executable hashes. `map_string` has different dynamic counts from the
same executable because its string hash seed is randomized per process.

SHA-256, SHA-512, SM3, SHA-1 and MD5 over 4 MiB of `a` show no instruction-count
regression. Every digest matches Python's reference implementation. The
[default-path measurements](benchmarks/scalar-array-leaves-2026-10-17/crypto-instructions.json)
and [forced software SHA measurements](benchmarks/scalar-array-leaves-2026-10-17/crypto-software-instructions.json)
retain every result. Software dispatch was forced only in an isolated stdlib
copy, using the existing routines.

The same-producer candidate adds 20,889 instructions to a 24,629,023,821-instruction
cold checker compile, about 0.000085%. The self-built candidate takes
24,619,161,265 instructions, a 0.040% reduction. Both produce the same checker
executable. Certification is enabled and `FERN_RC_CERTIFY_MEMO=0` for these
runs. The small difference is recorded as a regression check.

The self-built compiler grows from 12,278,800 to 12,393,520 bytes (+0.93%) as
its own helpers are inlined. Bootstrap stages 2 and 3 are identical, SHA-256
`343550c5a184aba0d920ce7bddf5f2f86c6f595baa6e664617aefe33e08fc0af`.
These measurements retain their original control revision; integration checks
on later main revisions are recorded in the PR.

## Reproducing an instruction comparison

Use a self-built control from `8b08453d8`. Prepare a second source checkout at
that revision and apply the retained compiler patch to reconstruct the measured
candidate independently of rebased commit IDs. Use the common stdlib from the
control revision. Set `CONTROL_COMPILER`,
`CANDIDATE_SOURCE`, `COMMON_STDLIB` and `CORPUS_PATH` to those local paths.
The retained corpus metadata identifies the downloads and their hashes.

```sh
MEASURE_DIR=/tmp/fern-scalar-array-leaves
mkdir -p "$MEASURE_DIR"
cp docs/benchmarks/scalar-array-leaves-2026-10-17/json-probe.fern.txt "$MEASURE_DIR/json.fern"
"$CONTROL_COMPILER" -target x86-64-linux -o "$MEASURE_DIR/candidate" "$CANDIDATE_SOURCE/compiler/fern.fern" "$COMMON_STDLIB"
"$CONTROL_COMPILER" -target x86-64-linux -o "$MEASURE_DIR/base-json" "$MEASURE_DIR/json.fern" "$COMMON_STDLIB"
"$MEASURE_DIR/candidate" -target x86-64-linux -o "$MEASURE_DIR/new-json" "$MEASURE_DIR/json.fern" "$COMMON_STDLIB"
valgrind --tool=callgrind --callgrind-out-file=/dev/null "$MEASURE_DIR/base-json" "$CORPUS_PATH"
valgrind --tool=callgrind --callgrind-out-file=/dev/null "$MEASURE_DIR/new-json" "$CORPUS_PATH"
```

## Correctness coverage

`TestSelfHostInlineScalarArrayLeaf` checks the emitted call sites and value
semantics on x86-64, ARM64, the x86 sanitizer and WASI, with balanced allocation
and release counts. It covers unique growth, retained heap aliases, arrays
inside borrowed tuples and variants, packed bytes, f32 rounding, booleans and
Unicode characters. Reference-element arrays and `@noinline` bodies keep
their calls. The bounds test requires exit 134 for negative and dynamic
out-of-range indices on all three targets.

The broader semantic-inlining, closure, JSON, certifier, allocation-contract
and bounds suites pass, as do JSON stdlib differentials against the interpreter,
source lint and the unchanged complexity gate. #12105's remaining parser and
representation work stays open.
