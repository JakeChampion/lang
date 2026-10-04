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
`0129e5d95`.
The GNU differential corpus adds 114 raw-input comparisons across file/stdin
input and three output modes. It covers malformed UTF-8, raw keywords,
escapes, NUL, unknown classes, missing values and buffer/read boundaries.
A separate 48-case target corpus pins expected output, including every
high-bit byte and long records.

Linux Go and primary target matrices pass in 17.901 and 59.836 seconds.
GNU dircolors/listing comparisons pass in 8.245 seconds, primary comparisons
in 33.954 seconds, followed by the full unit suite and all lint gates.
The separate raw-input GNU group passes in 1.412 seconds.

Darwin Go targets pass in 10.574 seconds, GNU raw-input/dircolors/listing
comparisons in 13.060 seconds, primary dircolors/matcher targets in 16.536
seconds, and primary consumer comparisons in 30.144 seconds. The reproduced
compiler passes all 96 native Darwin/core-WASM artifact runs in 1.179 seconds, with balanced
allocations and zero live bytes.

Compiler, standard-library and backend sources are identical to matcher
`b6891a327`, so these actual runs reuse its fresh Darwin fixed point:
13,144,785 bytes, SHA-256
`85a30d6664eb5f7ddb1848e9658f212b5b91e2cf0c01bfe8c9d2bc92e9d598b5`.
Stages 2 and 3 match using the published repository pin recorded in the
matcher report; stage 1 differs. CI on the eventual publication head remains required.

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
| Small ASCII entries | 50.761 ms | 44.239 ms | 49.637-52.656 ms | 43.902-45.471 ms |
| Small raw extensions | 40.243 ms | 38.545 ms | 39.977-47.976 ms | 38.293-39.542 ms |
| Long value | 20.962 ms | 20.870 ms | 20.747-22.028 ms | 20.549-21.309 ms |
| TERM pattern per entry | 58.545 ms | 55.946 ms | 56.373-60.914 ms | 54.831-59.626 ms |

The ASCII-entry and raw-extension ranges are disjoint. The other paired ranges overlap, so
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
| Small ASCII entries | 43.857 ms | 106.895 ms | 210.780 ms |
| Small raw extensions | 39.401 ms | 114.565 ms | Incorrect output |
| Long value | 22.667 ms | 32.300 ms | 22.512 ms |
| TERM pattern per entry | 56.276 ms | 110.048 ms | 326.172 ms |

Fern's timing ranges are below GNU's for ASCII entries, raw extensions and
TERM. The long-value ranges overlap for all three implementations. Uutils
is slower on ASCII entries and TERM with disjoint ranges. For raw extensions,
uutils exits successfully but emits 31 bytes
rather than the expected 9,437,215, so it is excluded from that timing comparison.

| Workload | Fern peak RSS | GNU peak RSS | uutils peak RSS |
| --- | ---: | ---: | ---: |
| Small ASCII entries | 41,713,664 B | 34,390,016 B | 9,551,872 B |
| Small raw extensions | 46,481,408 B | 69,304,320 B | Excluded |
| Long value | 34,717,696 B | 33,931,264 B | 10,780,672 B |
| TERM pattern per entry | 33,259,520 B | 11,272,192 B | 4,177,920 B |

Fern retains the whole input and output while parsing. Faster execution
does not imply lower peak memory use.

This removes dircolors' dependency on unchecked raw text reads and builder
extraction. Other consumers and the old text-boundary contracts still need
migration before #5714 is complete.
