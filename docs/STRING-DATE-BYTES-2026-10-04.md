# Date batch input stays in bytes

`date -f` reads records with `ByteLineReader` and passes borrowed byte views
to the datetime lexer. Comments and rejected input can contain arbitrary
bytes. NUL truncation and raw failure diagnostics retain GNU behavior without
constructing strings from those fragments.

`datetime.parse_full_bytes` returns the ordinary text result plus an owned
raw diagnostic tail. The tail can begin inside a UTF-8 scalar; the CLI writes
it through the byte sink. The existing text parser keeps its signature and
decodes that tail with replacement characters. Token names contain only ASCII
letters and periods, as checked by the lexer before conversion.

A complete quoted `TZ` prefix is validated before it becomes a zone name.
Ill-formed names fail the parse with an explicit diagnostic. This is a text
boundary, so those cases pin Fern's rejection rather than GNU's unchecked
interpretation. Malformed quoting retains the existing lexer failure path.

The migration also releases the lexer and time-formatting builders after
taking their output. Earlier allocation checks found a retained builder in
each formatted result; the candidate must balance allocations in every
actual and benchmark run.

## Validation

The integration combines the validated Sort/Xattr branch `ff25d2999`, DU's
byte patterns and matcher repair `8bbcc3887`. The shared fixture covers file
and stdin input, debug output, NUL, invalid UTF-8, nested comments, quoted
zones and read boundaries. Parser-specific cases retain borrowed subranges
and diagnostic tails after releasing their input.

Linux raw and ordinary GNU comparisons pass, including Date, Touch, DU, Pr,
Uptime and listing consumers. Go interpreter/native/WASI and primary
native/WASM Date/parser matrices pass, as do shared matcher, Xattr, byte-scan
and WASM fetch regressions. The full Linux unit suite and all lint gates pass.
The first run found two stale Go SSA test entries; only those retired legs
were removed. Primary compiler SSA coverage remains.

Using the published `stage0-20261004-ef49ae0` pin, Linux stages 2 and 3 match
at 12,989,632 bytes, SHA-256
`cbf0341a1e1e7d8f35c9e59991e9f28e257da27532020be18aa02165519a2ad1`.
Stage 1 differs because the pin predates generator changes in this integration.

Darwin stages 2 and 3 match at 13,228,145 bytes, SHA-256
`522066344b683fdd5f31ee27f22bd1b10db7a2d4a7b7f90b527839e9f54616ad`.
Stage 1 again differs. The raw Date corpus and Touch checks pass. The expanded
GNU Date comparison fails 25 cases. Restoring the three changed Date/parser/
formatter files to `ff25d2999` in the same frozen source reproduces exactly
the same failed cases and diagnostics. Date already appears in the Darwin
failure catalogue; no exception is added and these are not passing results.

The expanded Pr and Uptime groups also reproduce their existing failures:
85 Pr cases and 33 Uptime cases. Their failed-case sets and diagnostics match
the same baseline after normalizing only randomized test-directory IDs.
The DU and listing groups pass. All three failing suites already appear in
the Darwin catalogue; the migration adds no exclusions.

The remaining Darwin target and primary-consumer checks pass. All 224
reproduced-compiler artifact cases pass on native Darwin and core WASM in
3.317 seconds, with balanced allocations and zero live bytes.

## Measurements

Both versions use the reproduced Darwin compiler above. The before version
restores only the three changed Date/parser/formatter files to `ff25d2999`;
the libraries and compiler otherwise match. The benchmark uses `LC_ALL=C`
and `TZ=UTC0`, checks output and diagnostics against GNU 9.12, then takes two
warmups and seven alternating samples. A 16-record pilot precedes 16,384
records, changing only the scale. Task-owned heavy jobs were idle; the
desktop was not isolated. Allocation counts and peak RSS use separate runs.

The records are `2024-01-02`, `Jan 2 2024 UTC`, a numeric date followed by a
comment containing bytes `ff ed a0 80`, and `FOO`/`ff`/`BAR` with `--debug`.
Each has a newline and runs through `-u -f dates +%s`. The last workload must
exit 1 and emit the exact GNU diagnostic bytes. Diagnostic normalization
removes only executable paths/names. The initial pilot's path normalization
was incomplete and was corrected before collecting these results.

| Workload | Before median | After median | Before range | After range |
| --- | ---: | ---: | ---: | ---: |
| Numeric dates | 14.399 ms | 15.015 ms | 14.100-15.935 ms | 14.526-54.331 ms |
| Word dates | 18.110 ms | 18.531 ms | 17.791-18.593 ms | 18.216-18.797 ms |
| Raw comments | 14.316 ms | 15.173 ms | 14.172-14.927 ms | 14.911-15.804 ms |
| Raw debug errors | 47.755 ms | 48.977 ms | 46.146-49.415 ms | 46.495-51.077 ms |

Every before/after range overlaps. This run establishes no migration speedup.
The numeric candidate includes a 54.331 ms sample, retained in the results.

| Workload | Before allocations | After allocations | Before live bytes |
| --- | ---: | ---: | ---: |
| Numeric dates | 803,064 | 835,832 | 1,572,864 |
| Word dates | 1,097,980 | 1,130,748 | 3,145,728 |
| Raw comments | 803,071 | 835,844 | 1,572,864 |
| Raw debug errors | 966,822 | 1,032,355 | 786,432 |

The candidate balances allocations and frees with zero live bytes in every
workload. It allocates more objects while releasing storage the old version
retains. Those baseline leaks remain failures in the record.

GNU 9.12 and uutils 0.12.0 use the same inputs on the same Darwin host.
Uutils was built with Rust 1.95.0 and its Date feature. Uutils rejects the raw
comments and emits different debug diagnostics, so those runs are excluded
from timing comparisons.

| Workload | Fern median | GNU median | uutils median |
| --- | ---: | ---: | ---: |
| Numeric dates | 15.015 ms | 26.446 ms | 15.746 ms |
| Word dates | 18.531 ms | 28.680 ms | 51.413 ms |
| Raw comments | 15.173 ms | 26.404 ms | Incorrect output |
| Raw debug errors | 48.977 ms | 73.785 ms | Incorrect diagnostics |

Numeric timing ranges overlap across implementations. Fern's ranges are
below GNU's for the other workloads and below uutils' for word dates.

| Workload | Before peak RSS | After peak RSS | GNU peak RSS | uutils peak RSS |
| --- | ---: | ---: | ---: | ---: |
| Numeric dates | 3,276,800 B | 1,720,320 B | 1,261,568 B | 2,490,368 B |
| Word dates | 4,882,432 B | 1,720,320 B | 1,212,416 B | 2,523,136 B |
| Raw comments | 3,260,416 B | 1,671,168 B | 1,212,416 B | Excluded |
| Raw debug errors | 2,392,064 B | 1,572,864 B | 1,261,568 B | Excluded |

The native file grows from 382,705 to 382,737 bytes. Code changes from
302,712 to 306,976 bytes, unwind data from 25,804 to 26,660 bytes, and data
from 26,544 to 26,800 bytes. No size baseline changes.

Counting the emitted ARM64 instructions and the assembler's literal pools
reproduces both code-section sizes exactly. The 4,264-byte increase comprises:

| Change | Code delta |
| --- | ---: |
| Raw reader and byte-record helpers, replacing the text reader | +912 B |
| Complete-zone UTF-8 validation | +1,480 B |
| Raw quoting, slicing and diagnostic tails | +972 B |
| Ownership helpers for byte results and optional byte arrays | +540 B |
| Parser, lexer, formatting and CLI changes | +348 B |
| Literal pools and their alignment | +12 B |

The old text reader and parser bodies are absent from the candidate. The
added code implements raw input, checked zone names and owned error tails;
there is no second parser retained alongside it. The function count with
unwind records rises from 416 to 429. Literal storage in code grows by 16
bytes while its alignment padding shrinks by 4 bytes.
