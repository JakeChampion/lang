# Text boundary validation and native measurements

This record covers source `0b10e24e2`, including the main integration at
`a4bcfcf9a`. It advances #5714; existing macOS GNU divergences are listed below.

## Contracts

- `Reader.read_chunk(n)` rejects malformed or incomplete UTF-8 through
  `InvalidUtf8("")`. The physical read is consumed, without retry, carry or
  reading beyond `n`. Negative sizes fail before allocation. Its byte
  sibling preserves raw input.
- `buf_take` replaces each maximal invalid subpart with U+FFFD, drains the
  builder and retains its capacity. `buf_take_bytes` preserves bytes.
- Both line-reading builtins return complete lines, including the newline
  when present, and the final unterminated line before EOF. The existing
  D10 OS-text assumption still applies to malformed input.
- PEG keeps byte-oriented matching and ordered choice. Publishing a
  nonempty capture that splits a scalar fails with empty captures and the
  furthest byte position. It does not retry a different alternative.
- Simulated fetch futures retain complete valid bodies across resumptions;
  transport journals expose owned byte snapshots. HTTP stream buffers and
  raw OS time formats use byte storage, described in the
  [consumer report](STRING-BYTE-CELLS-2026-10-04.md).

## Validation

The integrated full unit suite and `make lint-all` passed. Linux checks ran
in an ARM64 container limited to two CPUs. x86-64 execution used QEMU for
correctness only. Boundary, ownership, HTTP, formatter and UTF-8 property
tests passed on the reproduced primary compiler. All eleven tracked driver
sizes passed the unchanged 5% gate; [exact byte counts are retained](benchmarks/string-boundaries-2026-10-04/linux-driver-sizes.txt).

Linux bootstrap stage 2 equals stage 3: 13,014,656 bytes, SHA-256
`61a5b3b67b11ab15d6af2e88f3db3c12aa70fd3699f817ca992b1550c6e9fb8f`.
Native macOS bootstrap also reached a fixed point: 13,245,633 bytes, SHA-256
`4f4afadfdf472003daf842055deeda893538a0d5f1819393230252e8b343b4c7`.
The reproduced macOS compiler passed Reader, builder, complete-line, PEG,
byte-cell, Date byte, formatter and closing UTF-8 property tests.

Native/core WASM allocation censuses balance. Component tests verify
behavior; the component runner has no exit-time census. The final
simulation check passed the Go paths and all six primary x86-64/ARM64 cases
against the merged header implementation.

## Native measurements

Measurements ran on Apple M3 Pro, Darwin 24.6.0, with 12 logical CPUs and
38,654,705,664 bytes of RAM. The before compiler uses pre-boundary source
`bcae0ce84`; it was built by the reproduced after compiler. Both compiler
hashes are in [the environment record](benchmarks/string-boundaries-2026-10-04/system.json).
The tests check exact output and balanced allocation censuses before timing.
Each run has two warmups and seven samples, alternating before/after order.
Small pilots preceded larger runs with only the iteration count changed.
Times include process startup. They compare the stated compiler/stdlib
revisions, not an isolated instruction change.

### Builder extraction

Each workload performs 32,768 extractions after a 128-extraction pilot.
The scalar input repeats `¢€𐐀`; malformed input repeats `61 e2 82 62 ff`.
The old path returns raw invalid bytes; the new path repairs them. Its
ASCII word scan and scalar validation add CPU work. Malformed input needs
a sizing pass followed by repair.

| Input bytes per extraction | Before median ms | After median ms |
|---|---:|---:|
| ASCII, 32 | 1.901 | 2.417 |
| ASCII, 8192 | 21.052 | 40.537 |
| Valid scalars, 4608 | 13.491 | 272.333 |
| Malformed, 2560 | 9.024 | 483.394 |

Every workload drops from 65,545 to 32,777 allocations, saving one per
extraction. Peak RSS is unchanged for each pair. Native `__text` grows by
1,252-1,256 bytes; linked file sizes are unchanged because of segment
padding. [Samples](benchmarks/string-boundaries-2026-10-04/builder-full.json),
[build sizes](benchmarks/string-boundaries-2026-10-04/builder-builds.json),
[script](benchmarks/string-boundaries-2026-10-04/measure-builder.py).

### PEG captures

Each workload performs 32,768 matches after a 128-match pilot. A capture
of the first byte of `é` now fails as specified, while an uncaptured byte
match remains successful.

| Case | Before median ms | After median ms | Before / after allocations |
|---|---:|---:|---:|
| ASCII capture | 10.132 | 10.518 | 557064 / 557064 |
| Unicode capture | 10.046 | 10.514 | 557064 / 557064 |
| Split-scalar capture | 10.107 | 9.038 | 557063 / 524295 |
| Uncaptured byte | 13.900 | 8.494 | 393222 / 393222 |

[Samples](benchmarks/string-boundaries-2026-10-04/peg-full.json) and
[script](benchmarks/string-boundaries-2026-10-04/measure-peg.py).

### Complete lines

An eight-line pilot preceded 128-line runs. Long-line throughput remains
dominated by the primitive's byte reads, while its output and allocation
behavior change substantially.

| Line bytes | Before / after pieces | Before / after allocations | Before / after median ms |
|---|---:|---:|---:|
| ASCII, 64 | 128 / 128 | 393 / 393 | 3.681 / 3.899 |
| ASCII, 8193 | 4224 / 128 | 12681 / 1161 | 240.240 / 239.062 |
| Unicode, 8201 | 4224 / 128 | 12681 / 1161 | 242.984 / 243.063 |

The previous Reader implementation returned at most 256 bytes per piece.
The new implementation returns every long line intact.
[Samples](benchmarks/string-boundaries-2026-10-04/read-line-full.json) and
[script](benchmarks/string-boundaries-2026-10-04/measure-read-line.py).

### Compiled time formats

A retained format is rendered 32,768 times after a 128-render pilot. Both
versions emit identical expected bytes. The new byte renderer reduces
allocations; the small ASCII timing ranges overlap.

| Format | Before / after allocations | Before / after median ms |
|---|---:|---:|
| ASCII timestamp | 262189 / 229409 | 8.358 / 8.043 |
| Unicode and refused directives | 327714 / 229402 | 7.281 / 6.306 |

[Samples](benchmarks/string-boundaries-2026-10-04/timefmt-full.json) and
[script](benchmarks/string-boundaries-2026-10-04/measure-timefmt.py).

## Unresolved GNU parity

All four full `date`, `du`, `ls` and `pr` parity suites pass against GNU 9.12
on Linux. The new raw-format cases also pass against Homebrew GNU 9.12 on
macOS. The broad macOS run has 110 Date/Pr failures covering timezone rules,
clock resolution, year formatting and `%r`. Unchanged main reproduces all
110 plus the raw-format defect fixed here; there are no new failing cases.
The CI-configured GNU 9.12 build reproduces the same failures. Both test
functions are already listed in the repository's Darwin ratchet; its list
and tolerances are unchanged. These are existing platform divergences, not
new failures from this change. This record does not claim full macOS GNU
parity. Completion of #5714 still requires the final integration gates.
