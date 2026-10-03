# Raw tokens for factor

`factor` reads and scans byte arrays. A token spanning read boundaries
accumulates in a reusable byte builder. Invalid tokens pass through byte
quoting, which produces ASCII escapes without decoding the input. Only
the digit range proved valid by the scanner becomes text for big-integer
parsing. Factoring algorithms and output scheduling stay unchanged.

The shared GNU comparison covers empty input, all byte values, malformed
UTF-8, control-byte diagnostics, NUL termination, command-line operands,
tokens around the 65,536-byte read boundary, wide powers and exponent
output. The reproduced compiler passes 20 cases on Darwin and 19 on core
WebAssembly, with balanced allocations. Wasmtime rejects malformed argv
before the guest starts, so the raw-argument case is native-only; malformed
stdin remains covered on both targets. Compiler reproduction is recorded
in [the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

The same compiler reduces native code from 99,888 to 99,056 bytes. Unwind
data grows from 13,364 to 13,508 bytes, static data stays at 6,328 bytes,
and the executable remains 132,769 bytes. No size baseline changes.

The Darwin GNU corpus, raw-byte checks and primary parity pass. Linux
byte-input checks, primary target checks, the GNU corpus, primary parity,
the full unit suite and all lint gates also pass.

Native arm64 Darwin measurements compare GNU 9.12 and uutils 0.12.0. An
8,192-byte scale pilot precedes an 8,388,608-byte scale run, changing only
that parameter. Short inputs contain 8,388,609 bytes; long inputs contain
8,388,610. Both Fern versions and uutils match GNU output, status and
stderr on all four workloads at both scales. Two warmups precede seven
alternating samples. Peak RSS is measured separately, with other compiler
and container jobs idle.

The long invalid token improves with disjoint timing ranges. Both long
tokens use less sampled peak memory. Short-input and long-zero timing
ranges overlap. These workloads exercise input processing and small
integers, so they do not measure changes to factorization throughput for
large composite numbers.

Executable SHA-256 hashes are
`2a8d64807b971bde66965435cf4ad7c97b437edfc83b8021169ace87d894230a`
before and
`ae7480074f0791055323047116726c6a398c742209d7392cb2892c73404283d7`
after.

| Workload | Implementation | Median ms | Range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| short | text | 117.673 | 114.830-120.915 | 1,458,176 |
| short | bytes | 118.219 | 116.185-122.175 | 1,458,176 |
| short | gnu | 130.951 | 124.443-151.004 | 1,179,648 |
| short | uutils | 1573.168 | 1547.856-1625.845 | 1,949,696 |
| short_exponents | text | 116.802 | 114.639-120.977 | 1,458,176 |
| short_exponents | bytes | 117.904 | 116.313-118.972 | 1,458,176 |
| short_exponents | gnu | 127.211 | 123.614-128.523 | 1,179,648 |
| short_exponents | uutils | 1187.500 | 1179.738-1193.990 | 2,097,152 |
| long_zeros | text | 31.925 | 31.806-34.240 | 62,111,744 |
| long_zeros | bytes | 29.750 | 28.936-32.447 | 34,914,304 |
| long_zeros | gnu | 28.527 | 28.134-29.195 | 16,941,056 |
| long_zeros | uutils | 14.566 | 14.294-15.904 | 18,825,216 |
| long_invalid | text | 121.968 | 120.771-143.574 | 95,764,480 |
| long_invalid | bytes | 58.590 | 53.515-62.022 | 68,501,504 |
| long_invalid | gnu | 115.695 | 113.840-119.064 | 33,816,576 |
| long_invalid | uutils | 23.044 | 22.328-25.679 | 35,569,664 |
