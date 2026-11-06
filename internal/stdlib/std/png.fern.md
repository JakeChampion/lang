# RGB PNG output

This module encodes RGB PNG images with eight bits per channel and no interlace.

## Model

The encoder accepts flat RGB pixels with eight bits per channel. It produces a noninterlaced PNG with image dimensions, compressed rows, and chunk checksums.

## Contracts

Dimensions and pixel-buffer length must agree. Filter and compression choices are explicit options. Invalid dimensions or buffer length return a concrete error.

## Executable example

```fern test name=std-png-example
import "std/png";

function main(): i32 {
  let pixels: u8[] = [255, 0, 0];
  let Ok(image) = png.encode_rgb8(1, 1, pixels[:]) else { return 1; };
  if (image.len() < 8 || image[0] != 137 as u8) { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum EncodeError>>

<<4 pub function (e: EncodeError) message(): string>>

<<5 impl error.Error for EncodeError>>

<<6 pub function options(): Options>>

<<7 function filter_id(filter: Filter): i32>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module encodes RGB PNG images with eight bits per channel and no interlace.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/binary";
import "std/deflate";
import "std/hash/crc32";
import "core/cmp";
import "core/error";
```

### `3 pub enum EncodeError`

```fern
<<3 pub enum EncodeError>>=
pub enum EncodeError { InvalidDimensions, InvalidBufferLength }
```

### `4 pub function (e: EncodeError) message(): string`

```fern
<<4 pub function (e: EncodeError) message(): string>>=
pub function (e: EncodeError) message(): string {
  match (e) {
    InvalidDimensions => {
      return "PNG dimensions are invalid or exceed the buffer limit";
    },
    InvalidBufferLength => {
      return "RGB buffer length does not match the PNG dimensions";
    }
  }
}

pub function (e: EncodeError) to_string(): string {
  return e.message();
}
```

### `5 impl error.Error for EncodeError`

```fern
<<5 impl error.Error for EncodeError>>=
impl error.Error for EncodeError {}

impl cmp.Display for EncodeError {}

pub enum Filter { Unfiltered, Sub, Up, Average, Paeth, Adaptive }

pub enum Compression { Balanced, Fast }

pub struct Options { filter: Filter, compression: Compression }
```

### `6 pub function options(): Options`

```fern
<<6 pub function options(): Options>>=
pub function options(): Options {
  return Options { filter: Filter.Unfiltered, compression: Compression.Balanced };
}

pub function encode_rgb8(width: i32, height: i32, pixels: [u8]): Result[u8[], EncodeError] {
  return encode_rgb8_with_options(width, height, pixels, options());
}

pub function encode_rgb8_with_options(width: i32, height: i32, pixels: [u8], opts: Options): Result[u8[], EncodeError] {
  if (width <= 0 || height <= 0 || width as i64 * 3 + 1 > 2147483647) {
    return Err(InvalidDimensions);
  }
  let stride = width as i64 * 3;
  let bytes = (stride + 1) * (height as i64);
  if (bytes > 2147483647) {
    return Err(InvalidDimensions);
  }
  if (pixels.len() as i64 != stride * (height as i64)) {
    return Err(InvalidBufferLength);
  }
  let rows = __alloc_u8(bytes as i32);
  let row = 0;
  while (row < height) {
    let filter = filter_id(opts.filter);
    if (filter == 5) {
      filter = best_filter(pixels, row * (stride as i32), stride as i32);
    }
    rows = rows.with(row * (stride as i32 + 1), filter as u8);
    rows = write_row(rows, row * (stride as i32 + 1) + 1, pixels, row * (stride as i32), stride as i32, filter);
    row += 1;
  }
  let output = [137 as u8, 80 as u8, 78 as u8, 71 as u8, 13 as u8, 10 as u8, 26 as u8, 10 as u8];
  let header = binary.append_u32_be([], width as u32);
  header = binary.append_u32_be(header, height as u32);
  header = header.append(8 as u8).append(2 as u8).append(0 as u8).append(0 as u8).append(0 as u8);
  output = chunk(output, "IHDR", header);
  let payload: u8[] = [];
  match (opts.compression) {
    Compression.Balanced => {
      payload = deflate.deflate(rows);
    },
    Compression.Fast => {
      payload = deflate.deflate_fast(rows);
    }
  }
  let compressed = binary.append_bytes([120 as u8, 1 as u8], payload);
  compressed = binary.append_u32_be(compressed, deflate.adler32(rows));
  output = chunk(output, "IDAT", compressed);
  return Ok(chunk(output, "IEND", []));
}
```

### `7 function filter_id(filter: Filter): i32`

```fern
<<7 function filter_id(filter: Filter): i32>>=
function filter_id(filter: Filter): i32 {
  match (filter) {
    Filter.Unfiltered => {
      return 0;
    },
    Filter.Sub => {
      return 1;
    },
    Filter.Up => {
      return 2;
    },
    Filter.Average => {
      return 3;
    },
    Filter.Paeth => {
      return 4;
    },
    Filter.Adaptive => {
      return 5;
    }
  }
}

function absolute(value: i32): i32 {
  if (value < 0) {
    return -value;
  }
  return value;
}

function predictor(pixels: [u8], start: i32, at: i32, stride: i32, filter: i32): i32 {
  let left = 0;
  let up = 0;
  let corner = 0;
  if (at >= 3) {
    left = pixels[start + at - 3] as i32;
  }
  if (start >= stride) {
    up = pixels[start + at - stride] as i32;
    if (at >= 3) {
      corner = pixels[start + at - stride - 3] as i32;
    }
  }
  if (filter == 0) {
    return 0;
  }
  if (filter == 1) {
    return left;
  }
  if (filter == 2) {
    return up;
  }
  if (filter == 3) {
    return (left + up) / 2;
  }
  let estimate = left + up - corner;
  let a = absolute(estimate - left);
  let b = absolute(estimate - up);
  let c = absolute(estimate - corner);
  if (a <= b && a <= c) {
    return left;
  }
  if (b <= c) {
    return up;
  }
  return corner;
}

function write_row(own output: u8[], dest: i32, pixels: [u8], start: i32, stride: i32, filter: i32): u8[] {
  if (filter == 0 || filter == 2 && start == 0) {
    return __bytes_into(output, dest, pixels, start, stride);
  }
  let at = 0;
  if (filter == 1) {
    output = __bytes_into(output, dest, pixels, start, 3);
    at = 3;
    while (at < stride) {
      output = output.with(dest + at, (pixels[start + at] as i32 - pixels[start + at - 3] as i32) as u8);
      at += 1;
    }
    return output;
  }
  if (filter == 2) {
    while (at < stride) {
      output = output.with(dest + at, (pixels[start + at] as i32 - pixels[start + at - stride] as i32) as u8);
      at += 1;
    }
    return output;
  }
  while (at < stride) {
    output = output.with(dest + at, (pixels[start + at] as i32 - predictor(pixels, start, at, stride, filter)) as u8);
    at += 1;
  }
  return output;
}

function best_filter(pixels: [u8], start: i32, stride: i32): i32 {
  let best = 0;
  let score: i64 = 9223372036854775807;
  let filter = 0;
  while (filter <= 4 && score > 0) {
    let sum: i64 = 0;
    let at = 0;
    while (at < stride && sum < score) {
      let difference: i32 = pixels[start + at] as i32 - predictor(pixels, start, at, stride, filter) & 255;
      if (difference >= 128) {
        difference -= 256;
      }
      sum = sum + absolute(difference) as i64;
      at += 1;
    }
    if (sum < score) {
      score = sum;
      best = filter;
    }
    filter += 1;
  }
  return best;
}

function chunk(own output: u8[], kind: string, data: u8[]): u8[] {
  let checksum = crc32.new().update(kind.as_bytes()).update(data).finish();
  output = binary.append_u32_be(output, data.len() as u32);
  output = binary.append_bytes(output, kind.as_bytes());
  output = binary.append_bytes(output, data);
  return binary.append_u32_be(output, checksum);
}
```
