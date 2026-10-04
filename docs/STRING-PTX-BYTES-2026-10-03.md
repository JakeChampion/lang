# Raw byte records and escaped patterns for ptx

`ptx` carries records, keyword ranges, references and escaped patterns as
bytes. Sorting reads raw byte prefixes, matching uses byte BRE APIs, and
formatting writes byte ranges. GNU quoting renders diagnostic text safely.

Direct checks with the reproduced builder compiler pass on Darwin and
core WebAssembly, including arbitrary bytes, escaped patterns, references,
format escaping, input boundaries and output files. Successful exits have
balanced allocation censuses. Abrupt-error behavior is recorded below.
Compiler reproduction is recorded in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

The same compiler attributes size separately to the prerequisite and this
consumer. Text `ptx` gains 760 bytes of code and 152 bytes of unwind data
from the raw-pattern parser. Migrating the consumer adds another 232 and
248 bytes respectively. Static data and the 265,361-byte file size remain
unchanged. The consumer cost implements raw range output and byte-prefix
sorting. No size baseline changes.

Native measurements use 16 repeats for the pilot and 4,096 for
the full run, with only the repeat count changed. Both runs verify output
before timing. Two warmups precede seven alternating samples; peak RSS
comes from a separate run. No other compiler or container job was running.

GNU coreutils 9.12 is the output oracle; Rust uutils 0.12.0 is also
checked. Timings include only implementations with identical stdout,
stderr and exit status.

| Workload | Implementation | Median ms | Range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| columns | text | 7.428 | 7.131-8.480 | 9,076,736 |
| columns | bytes | 7.649 | 7.245-8.954 | 8,945,664 |
| columns | gnu | 10.174 | 9.876-10.714 | 2,228,224 |
| traditional | text | 6.151 | 5.857-6.981 | 9,060,352 |
| traditional | bytes | 6.198 | 6.015-6.962 | 8,945,664 |
| traditional | gnu | 8.618 | 8.416-8.982 | 2,228,224 |
| roff | text | 7.005 | 6.787-7.128 | 9,060,352 |
| roff | bytes | 7.212 | 7.044-7.472 | 8,929,280 |
| roff | gnu | 10.958 | 10.708-11.186 | 2,244,608 |
| tex | text | 7.586 | 7.493-7.981 | 9,060,352 |
| tex | bytes | 7.753 | 7.499-7.903 | 8,929,280 |
| tex | gnu | 11.224 | 10.980-11.346 | 2,244,608 |
| folded_ignore | text | 5.687 | 5.633-5.820 | 3,751,936 |
| folded_ignore | bytes | 4.581 | 4.548-4.762 | 3,784,704 |
| folded_ignore | gnu | 6.279 | 6.175-6.430 | 1,835,008 |

The folded ignore-list case improves from 5.687 to 4.581 ms with
non-overlapping ranges. Other before/after ranges overlap. The full input
is 118,784 bytes, with output sizes recorded below. This is not a claim
that every PTX workload becomes faster.

| Workload | Argument bytes | Output bytes |
| --- | ---: | ---: |
| columns | 6 | 942,080 |
| traditional | 9 | 708,608 |
| roff | 9 | 708,608 |
| tex | 9 | 692,224 |
| folded_ignore | 22 | 356,352 |

uutils differs at both scales and is excluded from timing. Offsets below
are zero-based; the folded ignore-list case rejects invalid UTF-8.

| Repeats | Workload | Status | Expected bytes | Actual bytes | First difference |
| ---: | --- | ---: | ---: | ---: | ---: |
| 16 | columns | 0 | 3,680 | 3,904 | 44 |
| 16 | traditional | 0 | 2,768 | 3,152 | 16 |
| 16 | roff | 0 | 2,768 | 3,152 | 16 |
| 16 | tex | 0 | 2,704 | 3,088 | 16 |
| 16 | folded_ignore | 1 | 1,392 | 0 | 0 |
| 4,096 | columns | 0 | 942,080 | 999,424 | 44 |
| 4,096 | traditional | 0 | 708,608 | 806,912 | 16 |
| 4,096 | roff | 0 | 708,608 | 806,912 | 16 |
| 4,096 | tex | 0 | 692,224 | 790,528 | 16 |
| 4,096 | folded_ignore | 1 | 356,352 | 0 | 0 |

Parent-source census checks expose 4,128 live bytes on Darwin and
4,136 on core WebAssembly for successful traditional output to a file; the
candidate balances those exits. Abrupt errors still leave allocations:
invalid patterns rise from 648 to 728 bytes on Darwin, and from 512/520
to 560/568 on core WebAssembly (pipe/file). Empty-sentence diagnostics
rise from 2,120 to 2,200 on Darwin and from 1,544/1,552 to 1,592/1,600
on core WebAssembly. This change does not claim cleanup on abrupt exit.

Linux passes the new byte tests, primary target checks, existing GNU and
primary parity corpora, the full unit suite and all lint checks. The BRE
regression gate includes expr, tac, nl and csplit. The local GNU printf
oracle was rebuilt against glibc 2.39 because its older glibc 2.36 build
rejected binary prefixes already required by the corpus. No test expectation
or production numeric behavior changed to accommodate that environment.

Darwin passes the new byte checks, the existing GNU PTX corpus and primary
compiler parity. `TestPtxParity` is removed from the Darwin exceptions list
so CI now requires it to keep passing.
