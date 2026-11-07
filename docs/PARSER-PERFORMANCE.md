# Parser performance

Status: [record] measurements from 2026-11-07.

This change applies techniques from [Making Parsers Go Brr](https://howenyap.com/writings/making-parsers-go-brr/).
The comparison base is `bb853ddd010dd12a8bc2aee8dcaf8383e0ae1705`.

## Changes

The Go oracle stores each token in 48 bytes on amd64, compared with 88 bytes before this change.
The token kind and numeric suffix index each use one byte.
Only f-string tokens allocate a separate slice header for their parts.
The parser uses `Suffix()` and `FParts()` to read these fields.
Character values remain inline.

The Go lexer reuses source text for single-byte punctuation and strings without escapes.
Strings with escapes still use a builder.
The lexer uses `strings.IndexByte` to find the end of a comment.
This function selects the byte scan for the host CPU.
Comments retain their text, position, and trailing status.

The Fern lexer uses `__scan_set` for string runs.
The stop set contains newline, double quote, and backslash.
The existing kernels use AVX2 on x86-64 and NEON on ARM64.
WebAssembly uses the existing scan implementation for that target.
The string decoder still handles each escape and error.

The Fern numeric token stores its suffix tag and float flag in one `u8` field.
The parser reads these values through `suffix()` and `is_float()`.
Numeric validation computes the suffix tag once.
Numbers without digit separators reuse their decoded text.
`__memchr` detects that case before the separator decoder copies the text.
Raw spellings for separators, radix literals, characters, and bytes remain available to the formatter.

## Existing techniques

| Article technique | Repository treatment |
| --- | --- |
| Slim token types | Compact Go tokens and suffix indices. Fern retains its token union with a smaller numeric payload. |
| Smaller integers | One-byte Go token tags and suffix indices. Fern uses a byte for numeric flags and retains 32-bit source positions. |
| Small strings | Fern already stores short strings inline. Go reuses source text instead of a new string allocation. |
| SIMD scans | Fern string runs use the scan kernel. Numeric separator detection uses `__memchr`. Fern comments already use `__memchr`. |
| Contiguous allocation | Go tokens already occupy one slice. Fern already uses arrays. Both retain their existing allocation model. |

The change does not narrow Go AST positions.
Those positions use `int` throughout the diagnostics and parser APIs.
The change does not add an arena with a second lifetime model.

## Measurements

The host uses Linux amd64 and an Intel Xeon Platinum 8573C CPU.
The Go toolchain is 1.27.3 from `mise.lock`.
Both native benchmark binaries use the same compiler from `bootstrap/stage0.lock`.
Each comparison alternates the base and candidate order.
Both Go binaries read the same source files from the base checkout.
Wall times describe this host and workload.

The Go rows show medians from five samples.
The native row shows medians from seven samples of the string-scan change.

| Workload | Base | Candidate |
| --- | ---: | ---: |
| Go token size, amd64 | 88 bytes | 48 bytes |
| Go compiler corpus, allocated bytes | 471,271,354 | 252,467,344 |
| Go compiler corpus, allocations | 2,120,141 | 32,589 |
| Go compiler corpus, wall time | 701.50 ms | 549.72 ms |
| Go parse of compiler lexer, allocated bytes | 3,004,125 | 2,160,156 |
| Go parse of compiler lexer, allocations | 21,338 | 12,448 |
| Go parse of compiler lexer, wall time | 6.130 ms | 5.436 ms |
| Native long-string benchmark, wall time | 440.61 ms | 25.03 ms |

The corpus uses 46.4% fewer allocated bytes and 98.5% fewer allocations.
The native long-string benchmark is 17.6 times faster on this host.
Go wall times varied between samples on the shared host.
Use the allocation counts and byte counts for the repeatable Go result.

The Fern benchmark uses long strings with one escape in each string.
It does not measure a complete compilation.
Both binaries return checksum 69 and print 393000 allocations and sum 8354000.
The native change reduces scan work without a change to the string allocation model.
With half the rounds, the candidate median decreases from 24.50 ms to 12.70 ms across five pairs.

F-string tokens need one extra slice-header allocation each in the Go lexer.
The compact token slice reduces the total bytes even for that workload.
The payload benchmark records this tradeoff separately.

| Go payload workload | Base bytes/op | Candidate bytes/op | Base allocations/op | Candidate allocations/op |
| --- | ---: | ---: | ---: | ---: |
| Strings | 268,289 | 145,408 | 1,281 | 513 |
| Comments | 1,934,759 | 1,058,082 | 38 | 6 |
| Numeric suffixes | 311,297 | 172,032 | 1 | 1 |
| F-strings | 163,841 | 137,217 | 1,281 | 1,537 |

## Self-hosted frontend

`bench/compiler/lexer_numbers.fern` exercises integer suffixes, floats, separators, radix literals, characters, and bytes.
The program tokenizes the same source 100 times.
Both binaries return checksum 34 and print sum 230700.

| Numeric workload | Base | Candidate |
| --- | ---: | ---: |
| Allocations | 1,383,700 | 1,178,900 |
| Fresh heap growth, bytes | 403,184 | 354,032 |

This workload uses 14.8% fewer allocations and 12.2% less fresh heap growth.
Fresh heap growth measures the allocator high-water mark, not the sum of all allocated bytes.

`bench/compiler/frontend.fern` measures real compiler source files.
The fixed corpus contains `lexer.fern`, `parser.fern`, `checker.fern`, `ssarc.fern`, and `fern.fern` from the comparison base.
These five files total 2,535,504 bytes.
Each sample processes the corpus five times.
The lexer checksum is 6, and the parser checksum is 23.

| Corpus workload | Base allocations | Candidate allocations | Base fresh heap growth | Candidate fresh heap growth |
| --- | ---: | ---: | ---: | ---: |
| Tokenize | 4,889,350 | 4,774,010 | 30,183,960 | 30,141,664 |
| Tokenize and parse | 10,815,960 | 10,700,620 | 50,067,160 | 50,041,120 |

The real corpus has fewer numeric tokens than the numeric benchmark.
Its allocation reduction is 2.4% for the lexer and 1.1% for the combined lexer and parser.
The total heap change is small because other token payloads and AST nodes retain their layouts.

Wall times for the real corpus varied during the comparison.
Instruction counts from Callgrind provide a separate check of execution work.
On the fixed five-file corpus, the lexer decreases from 2,019,615,638 to 2,001,389,723 instructions.
The combined lexer and parser decrease from 5,403,658,355 to 5,386,513,375 instructions.
These reductions are 0.9% and 0.3%, respectively.
These runs use Valgrind 3.24.0 and the same native benchmark binaries.

## Reproduction

Run the Go benchmarks with the toolchain from `scripts/toolchain-env`:

```sh
go test ./internal/syntax/lexer -run '^$' -bench 'BenchmarkTokenize' -benchmem -count=5
go test ./internal/syntax/parser -run '^$' -bench BenchmarkParseCompilerLexer -benchmem -count=5
```

For a base comparison, copy the new benchmark files into a separate checkout of the comparison base.
Build both Go test binaries with `go test -c`.
Run both binaries from the same base checkout directory to fix the source corpus.
Use `-test.benchtime=2s` for each Go sample.

Compile `bench/compiler/lexer_strings.fern` in each checkout with the same stage0 binary:

```sh
stage0=$(./bootstrap/bootstrap.sh stage0)
"$stage0" -target x86-64-linux -o build/lexer-strings bench/compiler/lexer_strings.fern internal/stdlib
./build/lexer-strings
```

The expected exit code is 69.
Copy the benchmark into the base checkout before that build.
Compare seven pairs after both builds finish.
Alternate which binary runs first.

Compile the numeric and frontend benchmarks with the same command and their respective entry files.
Run `frontend lex` or `frontend parse` with the five absolute source paths after the mode argument.
Use paths from the comparison base for both binaries.

## Validation

The Go lexer, parser, and grammar tests pass.
The selected printer tests cover comments, strings, and round trips.
The Fern lexer tests pass on x86-64, ARM64 under qemu, and WebAssembly under wasmtime.
The Fern parser tests pass on x86-64 and ARM64 under qemu.
The full compiler source passes `-check`.
The feature census, Go build, Go vet, and format checks pass.

The native test wrapper writes `/proc/self/coredump_filter`, which this container does not expose.
Local native tests use a Go overlay that removes only this write.
The overlay retains `ulimit -c 0` and does not change compiler or lexer code.
Direct execution of the native lexer driver also passes.
The full unsharded printer suite was stopped after five minutes.
The selected printer tests pass in 19 seconds.

## Playground visibility fix

The initial CI run exposed [issue #14453](https://github.com/JakeChampion/lang/issues/14453).
The playground called the flattening operation without package metadata for embedded modules.
This caused E043 diagnostics for package methods within the standard library.
[PR #14450](https://github.com/JakeChampion/lang/pull/14450) fixed the bundle operation on `main` during this comparison.
This branch retains that fix and updates its remaining driver call sites for the overlay argument.
Additional regression tests permit calls between standard library modules and reject calls from the entry program.
