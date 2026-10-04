# Validate text returned by the TZif parser

`std/tz.parse_tzif` reads a binary format but returns timezone abbreviations
and POSIX rule names as strings. It now validates each referenced designation
and the bounded footer with `utf8.from_bytes`. Malformed or truncated UTF-8
returns `None` through the parser's existing error channel.

Empty designations, missing footers and out-of-range designation offsets
retain their previous behavior. Unreferenced bytes after a terminated name
or footer remain binary data. Version selection, transition interpretation
and timezone calculations are unchanged. The parser separates header and
block selection from decoding a bounds-checked type and transition table.

The shared fixture covers both table widths, complete Unicode names,
overlong encodings, surrogates, out-of-range scalars, truncated sequences,
offsets inside a scalar and field boundaries. It also checks that returned
names remain valid after the input array is released. Primary native and
WASM runs check allocations as well as results.

This integration includes the previously validated Date/raw-consumer chain,
LineReader change dff8110c1 and Dircolors/main repair 44b7b68b7.

Validation:

- Complexity and interpreter pilots. The initial combined complexity gate
  failed at 824 against the unchanged 780 baseline and its 5% allowance.
  Separating table decoding made the gate pass without changing the limit.
- Fresh Linux bootstrap from stage0-20261004-ef49ae0. Stage 1 differs;
  stages 2 and 3 match at 13,063,360 bytes, SHA-256
  `06a46eee4b5be0c7c7539c1385113df747023a3b9235cfadcebd341c73502360`.
- Go target matrix for TZif, Date parsing, text readers and partial I/O
  errors: 201.775 seconds.
- Primary target/census matrix, including the guarded-match regression and
  printed IR comparison: 250.951 seconds. The TZif fixture also passes the
  primary interpreter. Existing timezone and buffered-I/O stdlib fixtures
  pass on both Linux architectures; GNU consumer comparisons pass.
- Linux primary consumers, the full unit suite and all lint gates.
- Fresh Darwin bootstrap from the same pin. Stage 1 differs; stages 2
  and 3 match at 13,261,505 bytes, SHA-256
  `80fac7f88f5e2bd2cbecc33e4076f8a417281cf4300da8045494f0105696dc25`.
- Darwin Go targets and reader/error cases pass. Primary targets, including
  native/interpreter TZif and allocation-checked reader cases, pass in
  29.822 seconds; primary Date/Touch/Pr/Uptime consumers pass in 31.211 seconds.
- Expanded Darwin GNU comparison still fails: exactly 25 Date, 85 Pr and
  33 Uptime cases match the previously recorded unchanged-source failures
  and diagnostics. Only random Pr/Uptime temporary-directory numbers are
  normalized. These are baseline comparisons, not passing GNU parity gates.

## Native measurements

Both versions use the reproduced Darwin stage 2. The baseline restores only
`std/tz` from b517dbe53; all other sources and compiler settings are identical.
The program repeatedly parses a synthetic TZif file and checks the aggregate
lengths of its returned fields. Each file has two types and two transitions.
The Unicode designation is `é日本𐐀`; version 2 adds a bounded POSIX footer.

A 16-iteration pilot passed, followed by 16,384 iterations. Those timing
batches lasted only 4-7 milliseconds, so the final run increased only the
iteration count to 262,144. Each workload has two warmups and seven samples
with alternating before/after order. No other task-owned compiler or Docker
job ran during measurement; the desktop was not isolated.

| Workload | Input bytes | Before median [range], ms | After median [range], ms |
| --- | ---: | ---: | ---: |
| ASCII v1 | 70 | 48.959083 [47.240833, 74.282625] | 54.815875 [52.342000, 96.279584] |
| ASCII v2 | 156 | 68.847583 [67.225125, 70.290208] | 76.679333 [75.619917, 89.206333] |
| Unicode v1 | 79 | 54.810250 [53.763500, 61.121459] | 66.564166 [65.337792, 76.397708] |
| Unicode v2 | 174 | 76.134500 [75.399209, 79.290958] | 93.151375 [91.118959, 98.754375] |

Validation has a measured cost: both v2 workloads and Unicode v1 are slower
with disjoint observed ranges. ASCII v1 ranges overlap, including the retained
74.282625/96.279584-millisecond samples. No speedup is claimed.

Both versions free every allocation and finish with zero live bytes. For
262,144 parses, v1 allocations increase from 4,194,315 to 4,718,603; v2 from
5,767,179 to 6,553,611. These increases equal two validated designation results
per parse, plus one footer result for v2. Measured peak RSS is 1,097,728 bytes
for every version and workload.

The native file stays at 83,057 bytes. Code grows from 45,448 to 47,852 bytes,
unwind data from 5,980 to 6,388, and data stays at 4,456. Assembly instruction,
literal and alignment counts reproduce the 2,404-byte code increase exactly:
592 for separating checked block decoding, 1,480 for UTF-8 validation and
its constructor, 268 for option cleanup, 44 in designation/footer handling,
and 20 in literal pools/alignment. No size or complexity baseline changed.
