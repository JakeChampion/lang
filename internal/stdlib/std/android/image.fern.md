# Android image decoder

This module decodes JPEG, PNG, WebP, GIF and HEIF images with the NDK image decoder, for a NativeActivity app of `std/android`.

## Model

`AImageDecoder` of `libjnigraphics.so` reads an image from bytes in memory. It decodes the image at a size that the caller chooses, with a filter that the platform picks. `decode` decodes an image at its own size.

`decode_cover` scales it to cover a box with its shape kept, which is the size that `draw.cover_size` gives. `Frame.image` then shows the middle of it in that box. The pixels come as `draw.Image`: red, green, blue and alpha for each pixel, with the alpha multiplied in.

`size` reads only the header of an image.

The module needs API level 30, for `AImageDecoder`. A bad image, a format that the platform does not decode, or an image of more than `MAX_PIXELS` pixels gives `None`.

## Contracts

The functions use no state, so they can run on any thread of the app. They call C functions, so a worker of `std/worker` must not call them.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<C functions>>

<<decoding>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module decodes images with the NDK image decoder.
import "std/android/draw";
```

### `C functions`

`AImageDecoder_createFromBuffer` keeps a pointer to the bytes until `AImageDecoder_delete`, so the bytes stay in one C buffer for the life of the decoder. A result of 0 is `ANDROID_IMAGE_DECODER_SUCCESS`. Format 1 is `ANDROID_BITMAP_FORMAT_RGBA_8888`.

```fern
<<C functions>>=
@import("libjnigraphics.so", "AImageDecoder_createFromBuffer")
function c_create(buffer: usize, length: usize, out: usize): i32;

@import("libjnigraphics.so", "AImageDecoder_delete")
function c_delete(decoder: usize): void;

@import("libjnigraphics.so", "AImageDecoder_getHeaderInfo")
function c_header(decoder: usize): usize;

@import("libjnigraphics.so", "AImageDecoderHeaderInfo_getWidth")
function c_width(info: usize): i32;

@import("libjnigraphics.so", "AImageDecoderHeaderInfo_getHeight")
function c_height(info: usize): i32;

@import("libjnigraphics.so", "AImageDecoder_setAndroidBitmapFormat")
function c_set_format(decoder: usize, format: i32): i32;

@import("libjnigraphics.so", "AImageDecoder_setTargetSize")
function c_set_size(decoder: usize, width: i32, height: i32): i32;

@import("libjnigraphics.so", "AImageDecoder_decodeImage")
function c_decode(decoder: usize, pixels: usize, stride: usize, size: usize): i32;

const SUCCESS: i32 = 0;

const RGBA_8888: i32 = 1;
```

### `decoding`

```fern
<<decoding>>=
// The most pixels that an image can have: 16 megapixels, 64 MB of pixels.
pub const MAX_PIXELS: i32 = 16777216;

// A decoder over a C copy of the bytes.
struct Decoder { handle: usize, bytes: usize, len: i32 }

function open(bytes: [u8]): Option[Decoder] {
  let n = bytes.len();
  if (n == 0) {
    return None;
  }
  unsafe {
    let buf: usize = __alloc(n);
    __memcpy(buf, __bytes_data(bytes), n);
    let out: usize = __alloc(8);
    __store_ptr(out, 0 as usize);
    let rc = c_create(buf, n as usize, out);
    let handle: usize = __load_ptr(out);
    __free(out, 8);
    if (rc != SUCCESS || handle == 0 as usize) {
      __free(buf, n);
      return None;
    }
    return Some(Decoder { handle: handle, bytes: buf, len: n });
  }
}

function close(d: Decoder): void {
  c_delete(d.handle);
  unsafe {
    __free(d.bytes, d.len);
  }
}

function header_size(d: Decoder): (i32, i32) {
  let info = c_header(d.handle);
  return (c_width(info), c_height(info));
}

// The width and height of the image in `bytes`, or None when the platform
// cannot read its header.
pub function size(bytes: [u8]): Option[(i32, i32)] {
  let Some(d) = open(bytes) else {
    return None;
  };
  let got = header_size(d);
  close(d);
  return Some(got);
}

// Decodes at w by h. The pixels go to a C buffer, then to the image.
function decode_at(d: Decoder, w: i32, h: i32): Option[draw.Image] {
  if (w <= 0 || h <= 0 || w as i64 * (h as i64) > MAX_PIXELS as i64) {
    return None;
  }
  if (c_set_format(d.handle, RGBA_8888) != SUCCESS || c_set_size(d.handle, w, h) != SUCCESS) {
    return None;
  }
  let n = w * h * 4;
  unsafe {
    let buf: usize = __alloc(n);
    if (c_decode(d.handle, buf, (w * 4) as usize, n as usize) != SUCCESS) {
      __free(buf, n);
      return None;
    }
    let px = __alloc_u8(n);
    for i in 0..n {
      px = px.with(i, __load_u8(buf + i as usize) as u8);
    }
    __free(buf, n);
    return Some(draw.Image { width: w, height: h, pixels: px });
  }
}

// The image in `bytes` at its own size.
pub function decode(bytes: [u8]): Option[draw.Image] {
  let Some(d) = open(bytes) else {
    return None;
  };
  let (w, h) = header_size(d);
  let img = decode_at(d, w, h);
  close(d);
  return img;
}

// The image in `bytes`, scaled to cover a box of w by h with its shape kept.
pub function decode_cover(bytes: [u8], w: i32, h: i32): Option[draw.Image] {
  let Some(d) = open(bytes) else {
    return None;
  };
  let (w0, h0) = header_size(d);
  let (tw, th) = draw.cover_size(w0, h0, w, h);
  let img = decode_at(d, tw, th);
  close(d);
  return img;
}
```
