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

## Existing techniques

| Article technique | Repository treatment |
| --- | --- |
| Slim token types | Compact Go tokens and suffix indices. Fern retains its existing token union. |
| Smaller integers | One-byte Go token tags and suffix indices. Fern already uses 32-bit source positions. |
| Small strings | Fern already stores short strings inline. Go reuses source text instead of a new string allocation. |
| SIMD scans | Fern string runs now use the existing scan kernel. Fern comments already use `__memchr`. |
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
The native row shows medians from seven samples.

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
