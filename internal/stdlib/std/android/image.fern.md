# Android image decoder and encoder

This module decodes and encodes images for a NativeActivity app of `std/android`. The NDK image decoder reads JPEG, PNG, WebP, GIF and HEIF images. The NDK bitmap encoder writes JPEG, PNG and WebP images.

## Model

`AImageDecoder` of `libjnigraphics.so` reads an image from bytes in memory. It decodes the image at a size that the caller chooses, with a filter that the platform picks. `decode` decodes an image at its own size.

`decode_cover` scales it to cover a box with its shape kept, which is the size that `draw.cover_size` gives. `Frame.image` then shows the middle of it in that box. The pixels come as `draw.Image`: red, green, blue and alpha for each pixel, with the alpha multiplied in.

`size` reads only the header of an image.

`decode_frames` decodes each frame of an animated GIF or WebP, from API level 31. A frame can cover a part of the image and blend with the frame before, so the decoder keeps one buffer: the frames decode into it in turn, and a frame that the file disposes to the background clears its rectangle first. The frames keep the size of the image, and the decoder stops at a budget of bytes.

`encode` writes a `draw.Image` as a file of a `Format`, with `AndroidBitmap_compress` of `libjnigraphics.so`. The quality is from 0 to 100. A lossless format ignores it.

The module needs API level 30, for `AImageDecoder` and `AndroidBitmap_compress`. A bad image, a format that the platform does not decode, or an image of more than `MAX_PIXELS` pixels gives `None`.

## Contracts

The functions use no state, so they can run on any thread of the app. They call C functions, so a worker of `std/worker` must not call them.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<C functions>>

<<decoding>>

<<animations>>

<<encoding>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module decodes and encodes images with the NDK.
import "std/android";
import "std/android/draw";
import "std/ffi";
import "std/i32";
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

@import("libjnigraphics.so", "AndroidBitmap_compress")
function c_compress(info: usize, dataspace: i32, pixels: usize, format: i32, quality: i32, context: usize, write: usize): i32;

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
    __memcpy(__bytes_data(px), buf, n);
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

### `animations`

The functions of frames came in API level 31, so their imports are weak: the library loads on API level 30 too, and `decode_frames` calls them only from API level 31. A dispose operation of 2 is `ANDROID_IMAGE_DECODER_DISPOSE_OP_BACKGROUND`.

```fern
<<animations>>=
@import("libjnigraphics.so", "AImageDecoder_isAnimated", weak)
function c_is_animated(decoder: usize): u8;

@import("libjnigraphics.so", "AImageDecoder_advanceFrame", weak)
function c_advance(decoder: usize): i32;

@import("libjnigraphics.so", "AImageDecoderFrameInfo_create", weak)
function c_frame_info(): usize;

@import("libjnigraphics.so", "AImageDecoderFrameInfo_delete", weak)
function c_frame_info_delete(info: usize): void;

@import("libjnigraphics.so", "AImageDecoder_getFrameInfo", weak)
function c_get_frame_info(decoder: usize, info: usize): i32;

@import("libjnigraphics.so", "AImageDecoderFrameInfo_getDuration", weak)
function c_duration(info: usize): i64;

@import("libjnigraphics.so", "AImageDecoderFrameInfo_getFrameRect", weak)
function c_frame_rect(info: usize): Rect;

@import("libjnigraphics.so", "AImageDecoderFrameInfo_getDisposeOp", weak)
function c_dispose_op(info: usize): i32;

// An ARect of the NDK.
@derive(ffi.CLayout)
struct Rect { left: i32, top: i32, right: i32, bottom: i32 }

const DISPOSE_BACKGROUND: i32 = 2;

// The frames of an animation, each the size of the image, and the time that
// each shows, in milliseconds.
pub struct Animation { frames: draw.Image[], delays_ms: i32[] }

// The frames of the animated image in `bytes`, at most `most_bytes` bytes of
// pixels of them. It is None for a still image, below API level 31, and
// when the platform cannot decode two frames.
pub function decode_frames(ctx: usize, bytes: [u8], most_bytes: i64): Option[Animation] {
  if (android.sdk_version(ctx) < 31) {
    return None;
  }
  let Some(d) = open(bytes) else {
    return None;
  };
  let (w, h) = header_size(d);
  let got = if (c_is_animated(d.handle) != 0u8 && c_set_format(d.handle, RGBA_8888) == SUCCESS) { frames_of(d, w, h, most_bytes) } else { None };
  close(d);
  return got;
}

function frames_of(d: Decoder, w: i32, h: i32, most_bytes: i64): Option[Animation] {
  let n = w * h * 4;
  if (w <= 0 || h <= 0 || w as i64 * (h as i64) > MAX_PIXELS as i64) {
    return None;
  }
  let frames: draw.Image[] = [];
  let delays: i32[] = [];
  unsafe {
    let buf: usize = __alloc(n);
    __memset(buf, 0, n);
    let info = c_frame_info();
    let more = true;
    while (more && (frames.len() + 1) as i64 * (n as i64) <= most_bytes) {
      if (c_decode(d.handle, buf, (w * 4) as usize, n as usize) != SUCCESS || c_get_frame_info(d.handle, info) != SUCCESS) {
        break;
      }
      let px = __alloc_u8(n);
      __memcpy(__bytes_data(px), buf, n);
      frames = frames.append(draw.Image { width: w, height: h, pixels: px });
      delays = delays.append((c_duration(info) / 1000000i64) as i32);
      if (c_dispose_op(info) == DISPOSE_BACKGROUND) {
        clear_rect(buf, w, h, c_frame_rect(info));
      }
      more = c_advance(d.handle) == SUCCESS;
    }
    c_frame_info_delete(info);
    __free(buf, n);
  }
  if (frames.len() < 2) {
    return None;
  }
  return Some(Animation { frames: frames, delays_ms: delays });
}

// Sets the pixels of the rectangle `r` of the buffer to transparent.
function clear_rect(buf: usize, w: i32, h: i32, r: Rect): void {
  let x0 = r.left.max(0);
  let x1 = r.right.min(w);
  if (x1 <= x0) {
    return;
  }
  unsafe {
    for y in r.top.max(0)..r.bottom.min(h) {
      __memset(buf + ((y * w + x0) * 4) as usize, 0, (x1 - x0) * 4);
    }
  }
}
```

### `encoding`

`AndroidBitmap_compress` gives the bytes of the file to a write function, in parts. `written` puts each part in a C buffer that grows: the buffer is a header of three words, the address, the length and the capacity of the bytes. The function runs on the thread of `encode`, before `AndroidBitmap_compress` returns.

The `AndroidBitmapInfo` is the width, the height and the stride as 32-bit words, then the format and the flags. A flag of 0 tells that the alpha is multiplied in. The data space is `ADATASPACE_SRGB`.

```fern
<<encoding>>=
// The formats of `encode`, with the numbers of AndroidBitmapCompressFormat.
pub enum Format { Jpeg, Png, Webp, WebpLossless }

function format_number(f: Format): i32 {
  match (f) {
    Jpeg => {
      return 0;
    },
    Png => {
      return 1;
    },
    Webp => {
      return 3;
    },
    WebpLossless => {
      return 4;
    }
  }
}

const SRGB: i32 = 142671872;

@callback("written")
function written_entry(): usize;

// Adds the `size` bytes at `data` to the buffer `out`.
function written(out: usize, data: usize, size: usize): u8 {
  unsafe {
    let at: usize = __load_ptr(out);
    let len: usize = __load_ptr(out + 8 as usize);
    let cap: usize = __load_ptr(out + 16 as usize);
    if (len + size > cap) {
      let more: usize = (len + size) * (2 as usize);
      let grown: usize = __alloc(more as i32);
      if (len > 0 as usize) {
        __memcpy(grown, at, len as i32);
        __free(at, cap as i32);
      }
      __store_ptr(out, grown);
      __store_ptr(out + 16 as usize, more);
      at = grown;
    }
    __memcpy(at + len, data, size as i32);
    __store_ptr(out + 8 as usize, len + size);
  }
  return 1u8;
}

// The file of `img` in the format `f` at `quality` from 0 to 100, or None
// when the platform does not encode it.
pub function encode(img: draw.Image, f: Format, quality: i32): Option[u8[]] {
  let n = img.width * img.height * 4;
  if (img.width <= 0 || img.height <= 0 || img.pixels.len() != n) {
    return None;
  }
  unsafe {
    let info: usize = __alloc(20);
    __store_i32(info, img.width);
    __store_i32(info + 4 as usize, img.height);
    __store_i32(info + 8 as usize, img.width * 4);
    __store_i32(info + 12 as usize, RGBA_8888);
    __store_i32(info + 16 as usize, 0);
    let pixels: usize = __alloc(n);
    __memcpy(pixels, __bytes_data(img.pixels), n);
    let out: usize = __alloc(24);
    __store_ptr(out, 0 as usize);
    __store_ptr(out + 8 as usize, 0 as usize);
    __store_ptr(out + 16 as usize, 0 as usize);
    let rc = c_compress(info, SRGB, pixels, format_number(f), quality, out, written_entry());
    __free(pixels, n);
    __free(info, 20);
    let at: usize = __load_ptr(out);
    let len = __load_ptr(out + 8 as usize) as i32;
    let cap = __load_ptr(out + 16 as usize) as i32;
    __free(out, 24);
    let bytes = __alloc_u8(len);
    if (len > 0) {
      __memcpy(__bytes_data(bytes), at, len);
      __free(at, cap);
    }
    if (rc != SUCCESS || len == 0) {
      return None;
    }
    return Some(bytes);
  }
}
```
