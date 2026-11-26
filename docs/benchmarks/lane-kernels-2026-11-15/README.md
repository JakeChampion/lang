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

`bench/base64_decode.fern` decodes the 128 KiB text of 96 KiB of bytes 256 times.
The decoder reads 16 characters in one step and writes 12 bytes.
A step that holds a pad or a byte outside the alphabet stops, and the scalar decoder goes on from that step.
The scalar count is from the compiler of main at 79104f89.

| Program | Characters decoded | Scalar | Lanes | Ratio |
| --- | --: | --: | --: | --: |
| `base64_decode` | 32 MiB | 1,700,509,514 | 551,372,106 | 3.1 |

`bench/utf8_validate.fern` validates 128 KiB of text 256 times with `utf8.from_bytes`.
The text has ASCII words and 2-, 3- and 4-byte sequences between them.
The validator checks 16 bytes in one step, by the lookup method of Keiser and Lemire.
A step that finds an error stops, and the scalar validator goes on from the start of the last sequence before that step.
The scalar count is from the compiler of main at 7feebc01.

| Program | Bytes validated | Scalar | Lanes | Ratio |
| --- | --: | --: | --: | --: |
| `utf8_validate` | 32 MiB | 428,769,185 | 216,573,090 | 2.0 |

The step of the validator runs about 100 instructions on x86-64 where its operations need about 30. #16323 has the two costs in the emitted loop.

For each byte, the hex encoder runs 4.2 instructions in place of 29.3.
The base64 encoder runs 7.4 in place of 24.7.

Time on a 4-core x86-64 container, for 220 MiB of input to the hex encoder:
0.33 s with the scalar encoder and 0.08 s with lanes.

## The frame blend of `std/android/draw` (2026-11-26)

`blend_frame` blends 4 pixels in one step, as lanes of `u16x8`.
A `Frame` holds its pixels in raw memory, so the step reads and writes them with `__u8x16_load_ptr` and `__u8x16_store`.
`bench/draw_blend.fern` blends a layer of 512 by 256 pixels over a frame 64 times, with a different alpha each time.
The counts are retired instructions of the function that holds the blend loop, on x86-64-linux, from callgrind.
The scalar count is from the compiler of main at 59584e37.

| Program | Pixels blended | Scalar | Lanes | Ratio |
| --- | --: | --: | --: | --: |
| `draw_blend` | 8,388,608 | 297,695,818 | 92,139,264 | 3.2 |

For each pixel, the blend runs 11.0 instructions in place of 35.5.
The whole program runs 116,782,626 instructions in place of 321,963,735, and it returns the same checksum.

A step runs 32 instructions on x86-64, and its operations need about 22.
Each widen makes a zero register again, and the index of the step goes through four integer instructions.

## Lane operations out of loops (#17362)

The compiler merges two lane operations that are the same.
It moves a lane value that does not change out of a loop.
A splat of a constant is 6 instructions on x86-64, so a value of constants moves also.

No register keeps a lane value across a call, so a value out of a loop that makes a call goes to a frame slot.
Each read then loads it again.
So out of such a loop, a lane value moves only when the loop reads it one time, in the loop of its definition.

The counts are for the whole program on x86-64-linux.
The counts before are from the compiler of #17363.

| Program | Before | After | Change |
| --- | --: | --: | --: |
| `draw_blend` | 116,782,626 | 92,106,786 | -21% |
| `hex_encode` | 55,930,166 | 55,914,613 | 0% |
| `hex_decode` | 246,979,175 | 202,938,498 | -18% |
| `base64_encode` | 65,107,254 | 65,098,080 | 0% |
| `base64_decode` | 509,200,760 | 307,902,904 | -40% |
| `utf8_validate` | 174,753,699 | 154,077,675 | -12% |

The blend loop of `draw_blend` runs 67,463,424 instructions in place of 92,139,264: 8.0 for each pixel.

## What the numbers do not show

- A buffer of less than 16 bytes takes the scalar path, with one more test.
- The base64 step makes its digits with two shuffles, four shifts and six
  masks of byte lanes. No operation reads the bits of a lane value as lanes
  of a different width. With one, a step on lanes of 32 bits needs one
  shuffle, four shifts and four masks.
- The counts are for x86-64. The arm64 rows of `.github/perf-baseline.txt`
  have the counts of that target.
