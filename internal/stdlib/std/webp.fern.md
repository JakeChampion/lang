# Animated WebP files

This module makes an animated WebP file from still WebP files, one for each frame.

## Model

A WebP file is a RIFF file: the tag `RIFF`, the size, the tag `WEBP`, and chunks. A still image is one `VP8 ` chunk (lossy) or one `VP8L` chunk (lossless). A lossy image with alpha has a `VP8X` chunk, then an `ALPH` chunk and a `VP8 ` chunk.

An animated file has a `VP8X` chunk with the animation flag, an `ANIM` chunk with the loop count, and an `ANMF` chunk for each frame. An `ANMF` chunk holds the place, the size and the duration of the frame, and the image chunks of a still file. `animate` takes these chunks from each still file, so any encoder of still WebP can make the frames.

## Contracts

Each still file is `width` by `height`. The frames cover the canvas, and each frame replaces the frame before: it does not blend. The animation loops for ever. A duration is in milliseconds, from 0 to 16777215.

No frames, or a count of durations that is not the count of frames, gives `NoFrames`. A file that is not a still WebP file gives `NotWebp`. A width or a height that is not from 1 to 16384 gives `InvalidDimensions`.

## Executable example

```fern test name=std-webp-example
import "std/webp";

function main(): i32 {
  match (webp.animate(1, 1, [[1u8, 2u8]], [100])) {
    Ok(_) => {
      return 1;
    },
    Err(e) => {
      if (e.message() != "webp: a frame is not a still WebP file") {
        return 2;
      }
    }
  }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
// This module makes animated WebP files from still WebP files.
import "std/i32";

<<errors>>

<<chunks>>

<<animation>>
```

### `errors`

```fern
<<errors>>=
pub enum AnimateError { NoFrames, NotWebp, InvalidDimensions }

pub function (e: AnimateError) message(): string {
  match (e) {
    NoFrames => {
      return "webp: an animation needs one frame or more, and one duration for each frame";
    },
    NotWebp => {
      return "webp: a frame is not a still WebP file";
    },
    InvalidDimensions => {
      return "webp: the width and the height must be from 1 to 16384";
    }
  }
}
```

### `chunks`

A chunk is a tag of four bytes, the size of its data as a 32-bit little-endian word, and the data. Data of an odd size has one byte of padding after it.

```fern
<<chunks>>=
function le24(own out: u8[], v: i32): u8[] {
  return out.append((v & 255) as u8).append((v >> 8 & 255) as u8).append((v >> 16 & 255) as u8);
}

function le32(own out: u8[], v: i32): u8[] {
  return le24(out, v).append((v >> 24 & 255) as u8);
}

function u32_at(b: [u8], at: i32): i32 {
  return b[at] as i32 | b[at + 1] as i32 << 8 | b[at + 2] as i32 << 16 | b[at + 3] as i32 << 24;
}

function tag_at(b: [u8], at: i32, tag: string): boolean {
  let t = tag.as_bytes();
  return b[at] == t[0] && b[at + 1] == t[1] && b[at + 2] == t[2] && b[at + 3] == t[3];
}

function chunk(own out: u8[], tag: string, data: [u8]): u8[] {
  for c in tag.as_bytes() {
    out = out.append(c);
  }
  out = le32(out, data.len());
  for b in data {
    out = out.append(b);
  }
  if (data.len() % 2 == 1) {
    out = out.append(0u8);
  }
  return out;
}

function is_webp(file: [u8]): boolean {
  return file.len() >= 20 && tag_at(file, 0, "RIFF") && tag_at(file, 8, "WEBP");
}

// The chunk at `at` is a chunk of the image: `ALPH`, `VP8 ` or `VP8L`.
function is_image(file: [u8], at: i32): boolean {
  return tag_at(file, at, "ALPH") || tag_at(file, at, "VP8 ") || tag_at(file, at, "VP8L");
}

// The image chunks of the still WebP file `file`, with their headers and
// their padding: `ALPH`, `VP8 ` and `VP8L`. It is None for a file that is
// not WebP, or that has no image chunk.
function image_chunks(file: [u8]): Option[u8[]] {
  if (!is_webp(file)) {
    return None;
  }
  let out: u8[] = [];
  let found = false;
  let at = 12;
  while (at + 8 <= file.len()) {
    let size = u32_at(file, at + 4);
    let end = at + 8 + size + size % 2;
    if (size < 0 || at + 8 + size > file.len()) {
      return None;
    }
    if (is_image(file, at)) {
      found = found || !tag_at(file, at, "ALPH");
      for k in at..at + 8 + size {
        out = out.append(file[k]);
      }
      if (size % 2 == 1) {
        out = out.append(0u8);
      }
    }
    at = end;
  }
  return if (found) { Some(out) } else { None };
}
```

### `animation`

The `VP8X` flags are the animation flag and the alpha flag, so a frame can have alpha. The `ANMF` flags ask for no blending and no disposal.

```fern
<<animation>>=
function checked(width: i32, height: i32, stills: [u8[]], durations_ms: [i32]): Option[AnimateError] {
  if (width < 1 || height < 1 || width > 16384 || height > 16384) {
    return Some(InvalidDimensions);
  }
  if (stills.len() == 0 || durations_ms.len() != stills.len()) {
    return Some(NoFrames);
  }
  return None;
}

// The animated WebP file of the still WebP files `stills`, each `width` by
// `height`, with the durations of the frames in milliseconds.
pub function animate(width: i32, height: i32, stills: [u8[]], durations_ms: [i32]): Result[u8[], AnimateError] {
  if let Some(e) = checked(width, height, stills, durations_ms) {
    return Err(e);
  }
  let header = le24(le24([18u8, 0u8, 0u8, 0u8], width - 1), height - 1);
  let body: u8[] = [87u8, 69u8, 66u8, 80u8];
  body = chunk(body, "VP8X", header);
  body = chunk(body, "ANIM", [0u8, 0u8, 0u8, 0u8, 0u8, 0u8]);
  for i in 0..stills.len() {
    let Some(images) = image_chunks(stills[i]) else {
      return Err(NotWebp);
    };
    let d = durations_ms[i].max(0).min(16777215);
    let frame = le24(le24(le24(le24(le24([], 0), 0), width - 1), height - 1), d).append(2u8);
    for b in images {
      frame = frame.append(b);
    }
    body = chunk(body, "ANMF", frame);
  }
  let out: u8[] = [82u8, 73u8, 70u8, 70u8];
  out = le32(out, body.len());
  for b in body {
    out = out.append(b);
  }
  return Ok(out);
}
```
