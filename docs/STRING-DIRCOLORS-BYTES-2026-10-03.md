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

The October 4 integration includes matcher `6fda03eb5` and main `dcc065645`.
The GNU differential corpus adds 114 raw-input comparisons across file/stdin
input and three output modes. It covers malformed UTF-8, raw keywords,
escapes, NUL, unknown classes, missing values and buffer/read boundaries.
A separate 48-case target corpus pins expected output, including every
high-bit byte and long records.

Linux Go and primary target matrices pass in 10.908 and 39.610 seconds.
GNU dircolors/listing comparisons pass in 3.621 seconds, primary comparisons
in 20.806 seconds, followed by the full unit suite and all lint gates.
The separate raw-input GNU group passes in 0.689 seconds.

Darwin Go targets pass in 9.897 seconds, GNU raw-input/dircolors/listing
comparisons in 12.950 seconds, primary dircolors/matcher targets in 38.747
seconds, and primary consumer comparisons in 29.573 seconds. All 5,523
Go/Fern files match the frozen source. The reproduced compiler passes all
96 native Darwin/core-WASM artifact runs in 1.403 seconds, with balanced
allocations and zero live bytes.

Compiler, standard-library and backend sources are identical to matcher
`6fda03eb5`, so these actual runs reuse its fresh Darwin fixed point:
13,128,225 bytes, SHA-256
`11a977b65e7f17b6791feb06d9f8108f8df9f59bcfa2f6b4a91416f67a803d32`.
Its three identical stages used the explicit local seed recorded in the
matcher report. CI on the eventual publication head remains required.

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
| Small ASCII entries | 52.546 ms | 47.123 ms | 51.217-54.940 ms | 45.526-48.172 ms |
| Small raw extensions | 42.798 ms | 41.971 ms | 40.683-44.642 ms | 41.336-45.446 ms |
| Long value | 20.788 ms | 20.829 ms | 20.388-22.408 ms | 20.115-21.971 ms |
| TERM pattern per entry | 59.053 ms | 57.541 ms | 56.721-61.724 ms | 56.751-59.230 ms |

The ASCII-entry ranges are disjoint. The other paired ranges overlap, so
these samples do not establish a speedup for those workloads.

| Workload | Before allocations | After allocations | Before live bytes |
| --- | ---: | ---: | ---: |
| Small ASCII entries | 2,516,988 | 839,205 | 7,549,744 |
| Small raw extensions | 1,048,981 | 342 | 9,437,184 |
| Long value | 789 | 472 | 8,323,584 |
| TERM pattern per entry | 3,532,448 | 1,324,860 | 2,207,528 |

The candidate balances allocations and frees in every workload. The old
implementation retains one large output body in each full run. Those
baseline leaks are recorded as failures to release storage.

Both native files occupy 183,873 bytes. Code shrinks from 120,464 to 119,824
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
| Small ASCII entries | 45.890 ms | 108.645 ms | 212.210 ms |
| Small raw extensions | 39.196 ms | 109.776 ms | Incorrect output |
| Long value | 21.905 ms | 31.082 ms | 19.450 ms |
| TERM pattern per entry | 58.114 ms | 109.925 ms | 326.364 ms |

Fern's timing ranges are below GNU's in all four workloads. Uutils is slower
on ASCII entries and TERM with disjoint ranges; its long-value range overlaps
Fern's. For raw extensions, uutils exits successfully but emits 31 bytes
rather than the expected 9,437,215, so it is excluded from that timing comparison.

| Workload | Fern peak RSS | GNU peak RSS | uutils peak RSS |
| --- | ---: | ---: | ---: |
| Small ASCII entries | 41,713,664 B | 34,914,304 B | 9,977,856 B |
| Small raw extensions | 46,432,256 B | 68,485,120 B | Excluded |
| Long value | 34,717,696 B | 45,219,840 B | 10,780,672 B |
| TERM pattern per entry | 33,259,520 B | 10,289,152 B | 4,128,768 B |

Fern retains the whole input and output while parsing. Faster execution
does not imply lower peak memory use.

This removes dircolors' dependency on unchecked raw text reads and builder
extraction. Other consumers and the old text-boundary contracts still need
migration before #5714 is complete.
