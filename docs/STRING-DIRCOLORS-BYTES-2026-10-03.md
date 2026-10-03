# Dircolors configuration stays in bytes

Dircolors now reads configuration through `read_chunk_bytes`, parses byte
offsets, and builds raw output with `buf_take_bytes`. Stdio writes that output
without a text conversion. Invalid UTF-8 in extensions, values and unknown
keyword diagnostics is preserved exactly as GNU emits it. The built-in
database and environment values retain their text contracts.

Line and keyword bounds remain scalar offsets, avoiding a temporary line
record and keyword slice per entry. The private keyword helpers borrow the
existing array and compare ASCII names over those bounds. TERM patterns use
`fnmatch_bytes_text([u8], string)`, a specialization of the existing matcher
for a raw pattern and text subject. It avoids wrapping the already-text TERM
value in another byte-input record; the matching algorithm is shared with
the text/text and bytes/bytes APIs.

The section filter, NUL handling, line numbers, shell escaping, diagnostics
and output-call boundaries retain the GNU contract. Raw arguments are never
passed through an unchecked string constructor.

GNU parity also exposed two existing Darwin differences. The shared matcher
now treats collating and equivalence syntax as ordinary bracket bytes on
Darwin, matching GNU's fallback implementation. Linux and WASM retain
collation support. Positive fallback cases and regular collation cases run
through all three matcher APIs. This follows the platform distinction in
[gnulib's matcher](https://github.com/coreutils/gnulib/blob/master/lib/fnmatch_loop.c).
The `ls` consumer check found that device numbers were decoded with Linux's
layout on Darwin. Its shared device helpers now use Darwin's 8-bit major
and 24-bit minor, verified against the SDK macros and `/dev/null` and
`/dev/zero`. No failing corpus cases were removed.

## Validation

The GNU differential corpus adds 114 cases across file/stdin input and all
three output modes. It covers malformed UTF-8, raw keywords, escapes, NUL,
unknown classes, missing values and buffer/read boundaries. A separate
48-case target corpus pins expected output, including every high-bit byte
and long records. The shared matcher fixture also checks the mixed-input API
against the existing text and byte APIs.

The reproduced primary compiler passes all 48 cases on Darwin and core WASM
with balanced allocation counts and zero live bytes. The expanded matcher
fixture passes Darwin, core WASM and the interpreter; native and WASM
allocation counts balance. The Darwin GNU dircolors corpus passes after
the matcher repair, and the complete `ls`, `dir` and `vdir` corpora pass
after the device-number repair.

The final snapshot containing both portability repairs passes the Linux
Go and primary target matrices, GNU dircolors/ls corpora, primary dircolors
parity, full unit suite and all lint gates. Final Darwin Go targets pass
in 8.663 seconds, GNU dircolors/ls/dir/vdir in 24.647 seconds, and the
source-built primary dircolors/matcher tests in 40.764 seconds. CI on the
eventual integrated publication head remains required before merge.

## Measurements

Both versions use the reproduced primary compiler with SHA-256
`fc15892a54e9f5d5017cd0b3ad748b31b6eb30b012237d136d6e6ac2b6e7ec1a`
and matching standard library. The before dircolors source is from `2bac47bb4`.
Both versions use the same current GNU Stdio and filename-matcher libraries,
so this comparison measures the consumer conversion, not those dependencies.
Task-owned heavy jobs were idle; the desktop was not isolated.

The benchmark reads a file and emits Bourne-shell output. A 131072-byte pilot
precedes an 8388608-byte request; only that scale parameter changes. Each
input repeats a complete record, rounded down to the requested size. The
records are `DIR 01;34` plus newline, a dot/0xff extension with value
`01;`/0x80, `DIR` with a 65536-byte ASCII value, and `TERM *linux*` followed
by `DIR 1`. Exact output and separate allocation counts are checked before
two warmups and seven alternating samples writing to `/dev/null`.

| Workload | Before median | After median | Before allocations | After allocations |
| --- | ---: | ---: | ---: | ---: |
| Small ASCII entries | 59.853 ms | 52.463 ms | 2,516,988 | 839,207 |
| Small raw extensions | 49.234 ms | 48.224 ms | 1,048,981 | 344 |
| Long value | 20.936 ms | 21.421 ms | 789 | 474 |
| TERM pattern per entry | 66.056 ms | 66.514 ms | 2,649,438 | 1,324,862 |

Only small-ASCII timing ranges are disjoint: 58.705-60.435 ms before and
50.930-54.201 ms after. Other ranges overlap; their medians do not establish
a speedup. The new implementation balances allocations and frees in every
workload. The old implementation leaks one large output body in each full
run: respectively 7,549,744, 9,437,184, 8,323,584 and 2,207,528 live bytes.
Those baseline failures are recorded, not treated as balanced results.

Both native files occupy 183,793 bytes. Code grows from 120,644 to 121,464
bytes, unwind data from 12,100 to 12,332 bytes, and data stays at 20,920
bytes. The added raw parsing, diagnostic and mixed-input matching paths
account for the change. No binary-size baseline changes.

## GNU and uutils comparison

GNU 9.12 and uutils 0.12.0 run on the same Darwin host with `LC_ALL=C`,
`TERM=linux` and an empty `COLORTERM`. An isolated uutils binary was built
with Rust 1.95.0 and its dircolors feature. The same pilot and full inputs
are checked byte-for-byte before timing. Two warmups precede seven samples
in rotating implementation order. Peak RSS is measured in a separate run.

| Workload | Fern median | GNU median | uutils median |
| --- | ---: | ---: | ---: |
| Small ASCII entries | 53.710 ms | 111.137 ms | 216.642 ms |
| Small raw extensions | 49.179 ms | 116.927 ms | Incorrect output |
| Long value | 22.115 ms | 30.867 ms | 19.459 ms |
| TERM pattern per entry | 67.779 ms | 116.956 ms | 338.785 ms |

Timing ranges are disjoint for every valid comparison in this table.
Fern beats GNU on these four workloads; uutils beats Fern on long values.
For raw extensions, uutils exits successfully but emits 31 bytes rather
than the expected 9,437,215, so it is excluded from that timing comparison.

| Workload | Fern peak RSS | GNU peak RSS | uutils peak RSS |
| --- | ---: | ---: | ---: |
| Small ASCII entries | 49,266,688 B | 52,117,504 B | 9,502,720 B |
| Small raw extensions | 55,869,440 B | 53,739,520 B | Excluded |
| Long value | 34,717,696 B | 33,996,800 B | 10,780,672 B |
| TERM pattern per entry | 35,504,128 B | 14,155,776 B | 4,341,760 B |

Fern retains the whole input and output while parsing. Its lower runtime
on most measured cases does not imply lower peak memory use.

This removes dircolors' dependency on unchecked raw text reads and builder
extraction. Other consumers and the old text-boundary contracts still need
migration before #5714 is complete.
