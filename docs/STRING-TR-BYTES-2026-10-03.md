# Raw byte operands and transformations for tr

`tr` reads raw chunks and expands operand sets as byte arrays. Translation
and deletion use the borrowed builder kernels; squeeze state carries across
reads without constructing text. Classes, ranges, escapes, repetition,
complement and truncation keep their C-locale behavior.

The reproduced builder compiler passes 34 GNU cases on both Darwin and
core WASM, with exact output and balanced allocations. Cases cover every
byte value, empty input, high and low mappings, classes, repeated sets,
equivalence, truncation, deletion, complement and squeeze combinations
across read boundaries. Compiler reproduction is documented in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md); this consumer
does not change compiler source.

The same compiler builds both consumer versions. Raw operations remove
1,456 bytes of code and 24 bytes of unwind data. Static data and the
133,121-byte file size are unchanged. No baseline changes.

Linux raw-byte and ordinary GNU parity checks, primary target checks, the
full serial unit suite and `make lint-all` pass. Source files match the
frozen validation snapshot.

Native arm64 Darwin measurements used the reproduced builder compiler,
GNU 9.12 and uutils 0.12.0 in the C locale. An 8,192-byte pilot passed
before the same workload scaled to 8,388,608 bytes. Each output was checked
before two warmups and seven alternating samples; RSS was sampled separately.
No other compiler, container or benchmark job ran during measurement.

Times below are median milliseconds with the observed minimum and maximum.
Every text/raw range overlaps, so these measurements establish no speedup.

| Workload | Text | Raw bytes | GNU | uutils |
|---|---|---|---|---|
| translate | 5.670 (5.359-6.163) | 5.833 (5.294-6.282) | 10.003 (9.546-10.659) | 5.402 (5.111-6.621) |
| delete | 7.127 (6.834-7.297) | 7.284 (7.110-7.984) | 11.286 (10.692-11.382) | 6.483 (6.332-6.838) |
| squeeze | 17.742 (17.230-18.772) | 17.586 (16.137-18.898) | 90.263 (89.873-93.630) | 7.778 (7.136-8.391) |
| translate squeeze | 14.444 (13.826-15.107) | 14.341 (13.510-14.576) | 11.607 (11.387-12.067) | 6.484 (6.262-6.623) |
| delete squeeze | 18.233 (17.995-20.168) | 18.026 (17.508-19.253) | 53.160 (52.667-56.478) | 8.611 (7.599-8.923) |

Separate peak RSS samples in bytes:

| Workload | Text | Raw bytes | GNU | uutils |
|---|---:|---:|---:|---:|
| translate | 1441792 | 1441792 | 1196032 | 1818624 |
| delete | 1392640 | 1392640 | 1196032 | 1802240 |
| squeeze | 1490944 | 1458176 | 1196032 | 1802240 |
| translate squeeze | 1441792 | 1359872 | 1146880 | 1785856 |
| delete squeeze | 1490944 | 1425408 | 1196032 | 1802240 |

GNU, uutils and both Fern versions produce identical benchmark outputs.
