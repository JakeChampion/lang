# Android image decoder and encoder

This module decodes and encodes images for a NativeActivity app of `std/android`. The NDK image decoder reads JPEG, PNG, WebP, GIF and HEIF images. The NDK bitmap encoder writes JPEG, PNG and WebP images.

## Model

`AImageDecoder` of `libjnigraphics.so` reads an image from bytes in memory. It decodes the image at a size that the caller chooses, with a filter that the platform picks. `decode` decodes an image at its own size.

`decode_cover` scales it to cover a box with its shape kept, which is the size that `draw.cover_size` gives. `Frame.image` then shows the middle of it in that box. The pixels come as `draw.Image`: red, green, blue and alpha for each pixel, with the alpha multiplied in.

`size` reads only the header of an image.

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

<<encoding>>
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
