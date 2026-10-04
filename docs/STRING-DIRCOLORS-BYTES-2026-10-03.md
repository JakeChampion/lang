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

After integrating Stdio `d12624078`, including main `26b82ea7c` and the
map-alias repair `80d197eb8`, the full Linux suite and all lint gates pass
again. Go dircolors/matcher targets take 14.039 and 1.374 seconds; primary
targets take 32.156 and 24.992 seconds. The complete 114-case Linux raw-input
GNU comparison passes separately. Darwin Go targets, GNU corpora and primary
targets pass in 10.764, 13.180 and 0.315 seconds. All 5,934 Go/Fern files match
the tested snapshot. The reproduced compiler below passes the actual 48-case
native/core-WASM corpus in 1.149 seconds, with balanced allocations. Its
matcher runs free all 4,805 native and 4,736 core-WASM allocations, and the
interpreter passes the same fixture.

The October 4 integration of publication `74c69bdf8` passes the Linux target
tests, GNU comparisons, full unit suite and all lint gates. Darwin Go targets
pass in 11.143 seconds, GNU dircolors/ls/dir/vdir in 13.092 seconds, and
source-built primary dircolors/matcher tests in 37.798 seconds. The reproduced
compiler passes all 96 native/core-WASM artifact runs in 1.370 seconds with
balanced allocations. Its stages two and three match at 13,010,545 bytes.

## Measurements

Both versions use the reproduced primary compiler with SHA-256
`48efd5540bac3b85b564beb4b49e0c0d3b2b6aea510c1652c0328258909f66fe`
and matching standard library. The before dircolors source and complete
coreutils library directory come from `74c69bdf8`; the after version uses
this branch. This measures the consumer and shared-matcher changes together.
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
| Small ASCII entries | 59.523 ms | 53.923 ms | 2,516,990 | 839,207 |
| Small raw extensions | 48.608 ms | 46.800 ms | 1,048,983 | 344 |
| Long value | 20.441 ms | 20.719 ms | 791 | 474 |
| TERM pattern per entry | 65.360 ms | 64.167 ms | 3,532,450 | 1,324,862 |

All before/after timing ranges overlap; these samples do not establish a
speedup. The new implementation balances allocations and frees in every
workload. The old implementation leaks one large output body in each full
run: respectively 7,549,744, 9,437,184, 8,323,584 and 2,207,528 live bytes.
Those baseline failures are recorded, not treated as balanced results.

Both native files occupy 183,873 bytes. Code shrinks from 121,912 to 121,464
bytes, unwind data grows from 11,788 to 12,356 bytes, and data stays at 20,808
bytes. The combined parser and specialized matcher replace the old text
paths; their net code/unwind increase is 120 bytes. No binary-size baseline
changes.

## GNU and uutils comparison

GNU 9.12 and uutils 0.12.0 run on the same Darwin host with `LC_ALL=C`,
`TERM=linux` and an empty `COLORTERM`. An isolated uutils binary was built
with Rust 1.95.0 and its dircolors feature. The same pilot and full inputs
are checked byte-for-byte before timing. Two warmups precede seven samples
in rotating implementation order. Peak RSS is measured in a separate run.

| Workload | Fern median | GNU median | uutils median |
| --- | ---: | ---: | ---: |
| Small ASCII entries | 52.322 ms | 108.544 ms | 214.626 ms |
| Small raw extensions | 47.716 ms | 109.969 ms | Incorrect output |
| Long value | 20.691 ms | 29.048 ms | 18.259 ms |
| TERM pattern per entry | 64.403 ms | 109.483 ms | 325.119 ms |

Fern's timing ranges are below GNU's for ASCII entries, raw extensions and
long values; the TERM ranges overlap. Uutils is faster than Fern on long
values and slower on ASCII entries and TERM, with disjoint ranges.
For raw extensions, uutils exits successfully but emits 31 bytes rather
than the expected 9,437,215, so it is excluded from that timing comparison.

| Workload | Fern peak RSS | GNU peak RSS | uutils peak RSS |
| --- | ---: | ---: | ---: |
| Small ASCII entries | 49,266,688 B | 35,766,272 B | 9,502,720 B |
| Small raw extensions | 55,902,208 B | 53,608,448 B | Excluded |
| Long value | 34,717,696 B | 33,980,416 B | 10,829,824 B |
| TERM pattern per entry | 35,504,128 B | 15,958,016 B | 4,554,752 B |

Fern retains the whole input and output while parsing. Its lower runtime
on most measured cases does not imply lower peak memory use.

This removes dircolors' dependency on unchecked raw text reads and builder
extraction. Other consumers and the old text-boundary contracts still need
migration before #5714 is complete.
