# Dircolors configuration stays in bytes

Dircolors reads configuration through `read_chunk_bytes`, parses byte
offsets, and builds raw output with `buf_take_bytes`. Stdio writes that output
without a text conversion. Invalid UTF-8 in extensions, values and unknown
keyword diagnostics is preserved exactly as GNU emits it. The built-in
database and environment values retain their text contracts.

Line and keyword bounds remain scalar offsets, avoiding a temporary line
record and keyword slice per entry. The private keyword helpers borrow the
existing array and compare ASCII names over those bounds. TERM patterns use
`fnmatch_bytes_text([u8], string)`, sharing the matcher with the text/text and
bytes/bytes APIs. The section filter, NUL handling, line numbers, shell
escaping, diagnostics and output-call boundaries retain the GNU contract.
Raw input never passes through an unchecked string constructor.

The matcher dependency supplies GNU's Darwin collating-bracket fallback and
the listing device/birth-time corrections. Their validation and measurements
are recorded in [the matcher report](STRING-FNMATCH-BYTES-2026-10-03.md).

## Validation

The October 4 integration includes matcher repairs `b6891a327` and main
`c0ca52a6b`.
The GNU differential corpus adds 114 raw-input comparisons across file/stdin
input and three output modes. It covers malformed UTF-8, raw keywords,
escapes, NUL, unknown classes, missing values and buffer/read boundaries.
A separate 48-case target corpus pins expected output, including every
high-bit byte and long records.

Linux Go targets pass in 11.120 seconds. Primary target tests and guarded-
match regressions pass in 44.407 seconds. GNU dircolors/listing comparisons
pass in 3.422 seconds, primary comparisons in 20.577 seconds, followed by the
full unit suite and all lint gates. The separate raw-input GNU group passes
in 0.681 seconds.

Darwin Go targets pass in 10.541 seconds, GNU raw-input/dircolors/listing
comparisons in 12.858 seconds, primary dircolors/matcher targets and guarded-
match regressions in 32.229 seconds, and primary consumer comparisons in
29.899 seconds. The reproduced compiler passes all 96 native Darwin/core-WASM
artifact runs in 1.195 seconds, with balanced allocations and zero live bytes.

Fresh bootstrap from the published `stage0-20261001-c891ebc` pin reaches
matching stages 2 and 3 on both hosts. Linux is 12,987,328 bytes, SHA-256
`aae764e78245c22fbb1b58393f850be6878085b3378e40c6b5c7cdb9f5e709cc`.
Darwin is 13,161,633 bytes, SHA-256
`49785c27d336389b2992d39b5bf568be753f56b30a57ca674e77dcfba6f055d6`.
Stage 1 differs on both hosts.

Main's guarded-match totality fix left the printed-IR snapshot expecting a
redundant final variant test. The reproduced output differs only in that
graph. The expectation now matches, and an additional execution case checks
that a `None` input reaches the closing statement arm. The comparison runs
with golden regeneration disabled. Publication-head CI and review remain
required.

## Measurements

Both versions use that reproduced compiler and matching standard library.
The before dircolors source and complete coreutils library directory come
from `dcc065645`; the after version uses this integration. This measures
the consumer and shared-matcher changes together. Task-owned heavy jobs
were idle; the desktop was not isolated.

The benchmark reads a file and emits Bourne-shell output. A 131072-byte pilot
precedes an 8388608-byte request; only that scale parameter changes. Each
input repeats a complete record, rounded down to the requested size. The
records are `DIR 01;34` plus newline, a dot/0xff extension with value
`01;`/0x80, `DIR` with a 65536-byte ASCII value, and `TERM *linux*` followed
by `DIR 1`. Exact output and separate allocation counts are checked before
two warmups and seven alternating samples writing to `/dev/null`.

| Workload | Before median | After median | Before range | After range |
| --- | ---: | ---: | ---: | ---: |
| Small ASCII entries | 50.043 ms | 43.236 ms | 49.574-52.177 ms | 42.563-48.053 ms |
| Small raw extensions | 43.122 ms | 39.975 ms | 39.676-58.986 ms | 38.268-41.994 ms |
| Long value | 21.126 ms | 20.914 ms | 20.665-22.000 ms | 20.467-22.155 ms |
| TERM pattern per entry | 58.769 ms | 55.894 ms | 57.477-64.723 ms | 54.996-58.494 ms |

The ASCII-entry ranges are disjoint. The other paired ranges overlap, so
these samples do not establish a speedup for those workloads. The raw-input
baseline's 58.986 ms sample is retained.

| Workload | Before allocations | After allocations | Before live bytes |
| --- | ---: | ---: | ---: |
| Small ASCII entries | 2,516,988 | 839,205 | 7,549,744 |
| Small raw extensions | 1,048,981 | 342 | 9,437,184 |
| Long value | 789 | 472 | 8,323,584 |
| TERM pattern per entry | 3,532,448 | 1,324,860 | 2,207,528 |

The candidate balances allocations and frees in every workload. The old
implementation retains one large output body in each full run. Those
baseline leaks are recorded as failures to release storage.

Both native files occupy 183,873 bytes. Code shrinks from 120,176 to 119,536
bytes, unwind data grows from 11,788 to 12,356 bytes, and data stays at 20,808
bytes. Combined code and unwind data shrink by 72 bytes. No size baseline
changes.

## GNU and uutils comparison

GNU 9.12 and uutils 0.12.0 run on the same Darwin host with `LC_ALL=C`,
`TERM=linux` and an empty `COLORTERM`. The isolated uutils binary was built
with Rust 1.95.0 and its dircolors feature. The same pilot and full inputs
are checked byte-for-byte before timing. Two warmups precede seven samples
in rotating implementation order. Peak RSS is measured in a separate run.

| Workload | Fern median | GNU median | uutils median |
| --- | ---: | ---: | ---: |
| Small ASCII entries | 44.120 ms | 107.728 ms | 212.452 ms |
| Small raw extensions | 38.675 ms | 109.222 ms | Incorrect output |
| Long value | 20.326 ms | 28.684 ms | 18.108 ms |
| TERM pattern per entry | 55.542 ms | 108.989 ms | 325.092 ms |

Fern's timing ranges are below GNU's on all four workloads. Uutils is slower
on ASCII entries and TERM, and faster on long values, with disjoint ranges.
For raw extensions,
uutils exits successfully but emits 31 bytes
rather than the expected 9,437,215, so it is excluded from that timing comparison.

| Workload | Fern peak RSS | GNU peak RSS | uutils peak RSS |
| --- | ---: | ---: | ---: |
| Small ASCII entries | 41,746,432 B | 39,469,056 B | 9,912,320 B |
| Small raw extensions | 46,432,256 B | 53,608,448 B | Excluded |
| Long value | 34,717,696 B | 33,931,264 B | 10,780,672 B |
| TERM pattern per entry | 33,259,520 B | 8,503,296 B | 4,128,768 B |

Fern retains the whole input and output while parsing. Faster execution
does not imply lower peak memory use.

This removes dircolors' dependency on unchecked raw text reads and builder
extraction. Other consumers and the old text-boundary contracts still need
migration before #5714 is complete.
