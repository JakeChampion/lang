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

A step runs 30 instructions on x86-64, and its operations need about 22.
The index of the step goes through four integer instructions.

### `fill`, `blend` and `mask` (2026-11-26)

A fill with a color that is not opaque blends 4 pixels in one step.
`blend` puts 4 premultiplied pixels of a `[u8]` over the frame in one step.
Each divides by 255 as `(x + (x >> 8) + 1) >> 8`, which is exact for each value up to 65,152.

`mask` reads 16 bytes of coverage and blends 16 pixels in one step.
A swizzle gives each byte the coverage of its pixel, so the weights are lanes of 16 bits.
`bench/draw_fill.fern`, `bench/draw_rgba.fern` and `bench/draw_mask.fern` each do 8,388,608 pixels.
The counts are retired instructions of the function that holds the loop, on x86-64-linux, from callgrind.
The scalar counts are from the compiler of main at 2903c63b, and the lane counts are from the compiler of #17373.

| Program | Scalar | Lanes | Ratio |
| --- | --: | --: | --: |
| `draw_fill` | 1,031,798,784 | 58,769,408 | 17.6 |
| `draw_rgba` | 1,053,627,264 | 114,950,144 | 9.2 |
| `draw_mask` | 469,422,533 | 108,783,360 | 4.3 |

For each pixel, the fill runs 7.0 instructions in place of 123.
`blend` runs 13.7 in place of 125.6, and `mask` runs 13.0 in place of 56.0.

### `image` (2026-11-27)

An `Image` and a `Frame` hold the 4 bytes of a pixel in the same order.
So `image` copies each row of its box with one `__memcpy`, in place of a pack and a store for each pixel.
`bench/draw_image.fern` copies 512 by 256 pixels 64 times.
`image` and the copies that it calls run 9,396,713 instructions in place of 260,073,897: 1.1 for each pixel in place of 31.0.

`scaled` does two channels in one step, as lanes of `f64x2`, with the operations of one channel in each lane.
A channel is from 0 to 255.5, so its floor is the cast to `u8`.
The step reads the byte from the bits of the lane, as the color conversion of `std/jpeg` does.
It makes the source columns and weights of each x one time, and writes to an array of the full size.
Each pixel is the same as before, on x86-64, arm64 and wasm, for 20 pairs of sizes.

`bench/draw_scaled.fern` scales 600 by 300 pixels to about 512 by 256, 8 times.
The code of `std/android/draw` runs 292,594,019 instructions in place of 497,435,183: 279 for each pixel in place of 474.
The conversions of each byte to `f64` are most of what stays.

## The JPEG decoder (2026-11-26)

The inverse transform of `std/jpeg` does two samples in one step, as lanes of `f64x2`.
Each lane does the multiplications and additions of its sample in the order that the scalar transform did.
So each pixel is the same, on x86-64, arm64 and wasm, for each image of the tests and for 24 encoded images.
The transform writes to the plane, and the pixels go to an array of the full size.
Before, each block made an array of 64 samples, and each pixel made an array of its samples and appended 4 bytes.

`bench/jpeg_decode.fern` encodes a picture of 512 by 512 pixels at 4:2:0 and decodes it 4 times.
The counts are retired instructions on x86-64-linux, from callgrind.
The stage counts are from a copy of the module where each stage is a function that is not inline.

| Stage | Before | After | Ratio |
| --- | --: | --: | --: |
| transform and planes | 523,504,316 | 152,316,936 | 3.4 |
| all of `pixels` | 941,459,962 | 318,883,247 | 3.0 |
| whole program | 1,081,512,038 | 447,785,889 | 2.4 |

The color conversion is now 159 instructions for each pixel, in place of 400.

### The color conversion on lanes (2026-11-27)

The color conversion does two pixels in one step, as lanes of `f64x2`, with the operations of one pixel in each lane.
After the floor and the limits of 0 and 255, a lane gets 2^52 added.
A whole number from 0 to 255 is then the low byte of the bits of the lane.
So the step reads each byte with no conversion to an integer.
Each pixel is the same as before, on x86-64, arm64 and wasm, for the same 30 images.

The color conversion runs 90,029,303 instructions in place of 166,566,311: 85.9 for each pixel in place of 158.8.
The whole program runs 369,118,297 instructions in place of 447,785,889.

## Lane operations out of loops (#17362)

The compiler merges two lane operations that are the same.
It moves a lane value that does not change out of a loop.
A splat of a constant is 6 instructions on x86-64, so a value of constants moves also.

A lane value does not move out of a loop that makes a call that returns.
An abort of a bounds check does not return, so it does not count.
No register keeps a lane value across a call, so such a value goes to a frame slot.

#17369 moved a lane value out of a loop with a call when the loop reads it one time.
On x86-64 that cut `base64_decode` by 38%, because a splat there is 6 instructions.
On arm64 a splat is 2 or 3 instructions, and the values that moved took most of the lane registers.
The values of each step then went through the frame: `aarch64/base64_decode.ir` went up 33%, and its `.dr` went from 8.7M to 136.7M.
So that rule is out, and #17376 has the cause in the register allocator.

The blend loop of `draw_blend` has no call.
Its step runs 30 instructions: 7.5 for each pixel.

## What the numbers do not show

- A buffer of less than 16 bytes takes the scalar path, with one more test.
- The base64 step makes its digits with two shuffles, four shifts and six
  masks of byte lanes. No operation reads the bits of a lane value as lanes
  of a different width. With one, a step on lanes of 32 bits needs one
  shuffle, four shifts and four masks.
- The counts are for x86-64. The arm64 rows of `.github/perf-baseline.txt`
  have the counts of that target.
