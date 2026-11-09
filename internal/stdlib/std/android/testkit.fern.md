# Android test kit

This module holds the parts that the host tests of a NativeActivity app repeat. It steps the app at a time with the vsync after it, and it compares two frames. It writes a frame as a PNG file, and it reads the logs that a fake device writes.

## Model

An app of `std/android` is a state and a step function. A host test gives the step an event, then a `Vsync` at the same time. The display gives such a vsync after the app asks for a frame. `step_at` does both.

`differ` counts the pixels of two frames whose colours are far apart: more than `tolerance` in one channel or more. The CPU and the GPU round blends in different ways. So a test of a GPU frame against a CPU frame needs a tolerance of a few steps.

`save_png` writes a frame as a PNG file. The pixels of a frame are premultiplied, and the pixels of a PNG file are not, so `rgba` divides each channel by the alpha first.

A fake device often writes a log line for each call. `read_text` reads such a file, and gives "" when there is no file. `runs` puts each run of the same line into one line, so a test can compare the order of the calls and not their count.

## Contracts

The two frames of `differ` must have the same size. A pixel outside the smaller frame counts as different.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<steps>>

<<frames>>

<<logs>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module holds the parts that the host tests of a NativeActivity app
// repeat: steps, frame comparisons, PNG files of frames, and logs.
import "std/android";
import "std/android/draw";
import "std/png";
import "std/result";
```

### `steps`

```fern
<<steps>>=
// The state after `e` and the vsync after it, at the time `now_ms` on the
// monotonic clock.
pub function step_at[S](own s: S, step: (own S, android.Event) => S, e: android.Event, now_ms: i64): S {
  s = step(s, e);
  return step(s, android.Vsync(now_ms * 1000000i64));
}
```

### `frames`

```fern
<<frames>>=
// The pixels of `a` and `b` that differ by more than `tolerance` in one
// channel of red, green or blue.
pub function differ(a: draw.Frame, b: draw.Frame, tolerance: i32): i32 {
  let n = 0;
  for y in 0..a.height.max(b.height) {
    for x in 0..a.width.max(b.width) {
      if (x >= a.width || x >= b.width || y >= a.height || y >= b.height || !near(a.pixel(x, y), b.pixel(x, y), tolerance)) {
        n += 1;
      }
    }
  }
  return n;
}

// The pixels `p` and `q` differ by at most `tolerance` in each of red,
// green and blue.
pub function near(p: i32, q: i32, tolerance: i32): boolean {
  for shift in [0, 8, 16] {
    if (((p >> shift & 255) - (q >> shift & 255)).abs() > tolerance) {
      return false;
    }
  }
  return true;
}

// The pixels of `f`, row by row, as red, green, blue and alpha with each
// channel apart from the alpha.
pub function rgba(f: draw.Frame): u8[] {
  let out: u8[] = [];
  for y in 0..f.height {
    for x in 0..f.width {
      let c = f.pixel(x, y);
      let a = c >> 24 & 255;
      for shift in [0, 8, 16] {
        out = out.append(straight(c >> shift & 255, a) as u8);
      }
      out = out.append(a as u8);
    }
  }
  return out;
}

// Writes `f` to `path` as a PNG file with an alpha channel. It is false
// when the file cannot be written.
pub function save_png(f: draw.Frame, path: string): boolean {
  match (png.encode_rgba8(f.width, f.height, rgba(f))) {
    Ok(bytes) => {
      return write_file_bytes(path, bytes).is_ok();
    },
    Err(_) => {
      return false;
    }
  }
}

// The channel `v` of a premultiplied pixel with the alpha `a`, apart from
// the alpha.
function straight(v: i32, a: i32): i32 {
  if (a == 0) {
    return 0;
  }
  return (v * 255 + a / 2) / a;
}
```

### `logs`

```fern
<<logs>>=
// The text of the file at `path`, or "".
pub function read_text(path: string): string {
  if let Ok(b) = read_file_bytes(path) {
    return unsafe { string_from_bytes_unchecked(b) };
  }
  return "";
}

// The lines of `text` with each run of the same line as one line, joined
// by spaces.
pub function runs(text: string): string {
  let out: string[] = [];
  let last = "";
  for line in text.split("\n") {
    if (line != last) {
      out = out.append(line.to_owned());
      last = line.to_owned();
    }
  }
  return out.join(" ").trim().to_owned();
}
```
