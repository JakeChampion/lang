# The hex and base64 encoders on lanes (2026-11-15)

`std/hex` and `std/base64` encode 16 bytes and 12 bytes in one step, as
lanes of `u8x16`. This record has the measurements of the two encoders
against the scalar encoders that they replace.

The programs are `bench/hex_encode.fern` and `bench/base64_encode.fern`.
Each encodes one buffer 256 times and returns a checksum of the text. The
counts are retired instructions on x86-64-linux, from callgrind, for the
whole program. The scalar counts are from the compiler of main at 3bdd72da.

| Program | Bytes encoded | Scalar | Lanes | Ratio |
| --- | --: | --: | --: | --: |
| `hex_encode` | 16 MiB | 492,114,730 | 70,631,215 | 7.0 |
| `base64_encode` | 12 MiB | 311,247,402 | 93,474,351 | 3.3 |

`bench/hex_decode.fern` decodes 128 KiB of digits 256 times. The decoder reads 32 digits in one step.
A step that holds a byte that is not a digit stops, and the scalar decoder goes on from that step.
The scalar count is from the compiler of main at 79104f89.

| Program | Digits decoded | Scalar | Lanes | Ratio |
| --- | --: | --: | --: | --: |
| `hex_decode` | 32 MiB | 1,077,375,161 | 263,820,985 | 4.1 |

For each byte, the hex encoder runs 4.2 instructions in place of 29.3.
The base64 encoder runs 7.4 in place of 24.7.

Time on a 4-core x86-64 container, for 220 MiB of input to the hex encoder:
0.33 s with the scalar encoder and 0.08 s with lanes.

## What the numbers do not show

- A buffer of less than 16 bytes takes the scalar path, with one more test.
- The base64 step makes its digits with two shuffles, four shifts and six
  masks of byte lanes. No operation reads the bits of a lane value as lanes
  of a different width. With one, a step on lanes of 32 bits needs one
  shuffle, four shifts and four masks.
- The counts are for x86-64. The arm64 rows of `.github/perf-baseline.txt`
  have the counts of that target.
