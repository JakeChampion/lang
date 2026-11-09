# Frontend CPU profiles

Status: [record] diagnostic profiles from 2026-11-09.

These profiles identify candidates for later experiments.
They do not establish a speedup or an external rank.
This record changes no lexer, parser, compiler, or runtime implementation.
The native and Go workloads use different corpora and do different work.
Do not compare their raw rates as a language or compiler score.

## Native frontend

The library source is `3a296b887ccb1c879743cce8a2a67351c548481f`.
The fixed input source is `12be8f11864c00d71bc3ae9e0c3bc1ddabea75ef`.
The [collection manifest](native/collection-manifest.json) records exact file sizes, file hashes, flags, commands, and binary identity.
The five files are `lexer.fern`, `parser.fern`, `checker.fern`, `ssarc.fern`, and `fern.fern`.
The harness reads each file once and processes the corpus 100 times per mode.

The qualified producer source is `38f241ca26366e5ef39a898c780bbfbc3a4694f5`.
The builds enable the CPU profiler, omit `-g`, and retain ownership certification.
The production harness has no allocation-statistics intrinsics and disables leak instrumentation.
An [emitted-assembly audit](native/assembly-audit.json) confirms zero references to the allocation counter symbol.
That assembly was not executed for the profiles.

| Mode | Samples | Dropped samples | Checksum | Expected exit |
| --- | ---: | ---: | ---: | ---: |
| Tokenize | 492 | 0 | 67,181,100 | 64 |
| Tokenize and parse | 3,802 | 0 | 67,590,100 | 15 |

The nonzero exits encode each checksum modulo 97.
Both checksums match the earlier counter-enabled harness.
The [raw lexer profile](native/lex.log) and [raw parser profile](native/parse.log) retain all recorded stacks.
The CPU profiler samples at 100 Hz.
Some parser stacks reach its 64-frame limit, so inclusive attribution can omit ancestors.
Inlining and tail calls also affect visible parents.

| Mode | Function | Exclusive samples | Share |
| --- | --- | ---: | ---: |
| Tokenize | `lexer.tokenize` | 217 | 44.11% |
| Tokenize | `__fern_alloc` | 52 | 10.57% |
| Tokenize | token drop | 48 | 9.76% |
| Tokenize | `__fern_arr_box` | 35 | 7.11% |
| Parse | `__fern_arr_box` | 755 | 19.86% |
| Parse | `parser.Par.peek` | 179 | 4.71% |
| Parse | `parser.Par.peek_ident` | 173 | 4.55% |
| Parse | AST expression drop | 163 | 4.29% |

Boxed array reads and lookahead calls are candidates for further analysis.
These samples do not prove that all boxing comes from lookahead or that all boxing is avoidable.
A generic projection or inline improvement needs a separate correctness and performance experiment.

## Counter-enabled diagnostic

The [first harness](counter-enabled-diagnostic/frontend.fern.txt) uses `__heap_alloc_count` and `__heap_bump_bytes`.
These intrinsics enable allocation-statistics code and can disable allocator fast paths.
Its 710 lexer samples and 3,774 parser samples remain intact, with zero dropped samples.
They describe that counter-enabled harness, not the production harness.
Do not use its allocator attribution as production evidence.

The replacement harness removes only those counters and their output lines.
The library source, producer, corpus, loop count, and checksums stay fixed.
The two profile runs are not a controlled timing comparison.
No profile sample was discarded for its value.

## Go oracle

The test binaries use Go 1.27.3 and library source `3a296b887c`.
Both benchmarks read fixed source from `12be8f1186`.
The [Go manifest](go/manifest.json) records commands and binary hashes.
The CPU profiles include benchmark setup and calibration.
The raw rates include profiler overhead and shared-host variation.

The lexer benchmark processes 391 compiler and driver files, with 15,281,580 bytes and no excluded file.
The [corpus census](go/go-corpus.json) retains every file hash and acceptance result.
The parser benchmark processes only `compiler/lexer.fern` and includes tokenization.
Each benchmark uses `-test.benchtime=10s` and one profiled invocation.

The baseline Go lexer and parser regression suites pass.
A [correctness control](go/token-digests.json) records 3,706,487 tokens and 4,200 comments across all 391 files.
The digests include token kind, text, position, scalar, suffix, f-string parts, and comments.
These digests are baseline correctness data, not performance samples.

The lexer profile attributes 30.00% of cumulative CPU time to `strings.HasPrefix` through its internal helper.
Its [source annotation](go/go-lexer-next.txt) attributes 9.99 seconds to the operator-prefix test line.
That is about 37.74% of the profile's 26.47 CPU seconds.
The loop tries all 35 operator strings in order for each punctuation candidate.
A dispatch on the first byte is a bounded candidate for a later experiment.
It must preserve longest-match behavior, token text, source positions, and error handling.

The raw [lexer CPU profile](go/go-lexer.cpu.pprof) and [parser CPU profile](go/go-parser.cpu.pprof) remain available.
The text reports filter low-cost nodes for display.
Those display filters do not remove data from the raw profiles.
Inclusive percentages overlap and are not additive.

## Static token projection check

The [assembly audit](native/static-parser-box-audit.json) finds no direct `__fern_arr_box` call in the `Par.peek` functions.
`Par.peek` loads a token pointer and increments its reference count when necessary.
This diagnostic assembly was not the executed profile binary.
Static call sites do not establish dynamic costs.
The `peek` samples do not establish a cause for the separate array-box samples.

## Isolated operator matcher probe

A [matcher probe](go/operator-probe-results.json) compares the current linear table scan with first-byte buckets.
It preserves the order of the same 35 operator strings.
Both matchers agree on 28,673 prefix controls and 2,033,495 punctuation positions from all 391 corpus files.
The controls include every operator prefix and every next-byte value, plus empty input.

Five alternating pairs process each punctuation position 20 times per sample.
Median matcher time per lookup is 144.825 ns for the linear scan and 11.361 ns for the buckets.
The [raw results](go/operator-probe-results.json) retain every sample and checksum.
The [manifest](go/operator-probe-manifest.json) records commands and source and binary identities.

This probe changes no production lexer implementation.
Both matchers use an indirect call.
Corpus construction, bucket initialization, startup costs, and complete lexer work are outside the timed section.
These matcher observations establish no lexer, parser, or compiler speedup.
A production experiment still needs complete regression, malformed-input, digest, allocation, memory, and timing controls.

## Controls and reproduction

A separate Callgrind compiler comparison was active during profile collection.
These shared-host diagnostic runs do not constitute a timing comparison.
The [outer manifest](manifest.json) defines hashes of the completed archive files.
Collection manifests retain their original records, which can include progress-file hashes before final log output.
The outer manifest defines the final hashes for those files.

Create checkouts of the recorded library and input revisions.
Set local paths in the native collector and Go commands.
Use the producer and Go version identified in the manifests.
Copy the native harness text to the `frontend.fern` path in the collector.
Run each mode without a source change.
Retain profile headers, raw stacks, checksums, and instrumentation labels.
