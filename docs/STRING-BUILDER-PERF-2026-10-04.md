# Builder text validation: HTTP benchmark cost

PR #11493 added UTF-8 validation and maximal-subpart replacement to
`buf_take`. The HTTP serializer uses that text boundary. Its performance
gate failed after the merge because the required decoder increased static
instruction counts beyond the existing 1% tolerance.

The measurement compares the exact pre-PR main revision `bcae0ce84` with
merged revision `f67909bc5`. Both use the same HTTP implementation and
benchmark input. The Go bootstrap compiler emits the assembly measured by
`scripts/perf-bench`; these counts do not describe the primary compiler's
native execution time.

| Metric | Before | After |
|---|---:|---:|
| x86-64 static instructions | 31616 | 31942 |
| ARM64 static instructions | 40784 | 41213 |
| ARM64 retired instructions | 130864526 | 132536537 |

ARM64 retired instructions were measured with Callgrind on native ARM64
Linux. Both binaries return checksum 30. The after count exactly matches
the ARM CI measurement. No QEMU timing is used.

The [raw measurements](benchmarks/builder-gate-2026-10-04/measurement.txt)
and [function attribution](benchmarks/builder-gate-2026-10-04/attribution.json)
are retained. To reproduce, build `./cmd/fern` at each stated revision,
emit `bench/http_hello.fern` for `arm64-linux` and `x86-64-linux`,
and count assembly lines beginning with whitespace as `scripts/perf-bench`
does. The [attribution script](benchmarks/builder-gate-2026-10-04/attribute.py)
groups that same count by `.type`/`.size` function boundaries. Pass it the
directory containing `before-arm64-linux.s`, `after-arm64-linux.s`,
`before-x86-64-linux.s` and `after-x86-64-linux.s`. Run each
linked ARM64 binary under `valgrind --tool=callgrind` on native ARM64 Linux.

## Static attribution

Every changed instruction belongs to the checked builder extraction path.
All other emitted function counts are unchanged.

| Function | x86-64 added instructions | ARM64 added instructions |
|---|---:|---:|
| `__fern_buf_take` | 11 | 23 |
| `__fern_buf_text_part` | 101 | 120 |
| `__fern_buf_text_size` | 97 | 126 |
| `__fern_buf_text_copy` | 117 | 160 |
| Total | 326 | 429 |

The size pass validates the input, counts replacement expansion without
overflow, and selects a direct copy for valid text. It scans ASCII eight
bytes at a time. Invalid input takes a second pass that copies valid runs
and writes U+FFFD once per maximal invalid subpart. Extraction allocates
only the exact output storage and retains the builder's capacity.

The extra code implements the text contract, including malformed input;
removing it would restore the invalid-string bug. The attribution found no
growth outside these helpers. The proposed baseline update recorded that
required feature cost with the existing tolerance unchanged.

The previous ARM retired-instruction baseline was 160540548, predating
separate HTTP improvements. The measured value also records that earlier gain.
The relevant cost of this change is the before/after comparison above,
not a claimed speedup against the stale baseline. x86-64 retired
instructions remain within the existing tolerance and that baseline is
unchanged.

Native primary-compiler builder measurements are tracked separately from
this deterministic Go-compiler gate.

## Subsequent main integration

Main subsequently merged the header implementation in PR #11500. Its
baseline supersedes the proposed update above: x86-64 static instructions
28820 and retired instructions 105296421; ARM64 static instructions 37034
and retired instructions 123332537. Those values retain the required UTF-8
decoder and record the separate header optimization. The combined branch
keeps main's values. The integrated branch reproduced both static counts
and the ARM64 retired-instruction count exactly. The x86-64 retired count
remains subject to native x86-64 CI. The historical attribution above
remains specific to the two stated revisions.
