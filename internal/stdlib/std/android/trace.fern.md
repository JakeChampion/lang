# Android tracing

This module puts named sections and counters into the system trace of Android, which Perfetto and systrace show. It also keeps the times of the last frames of an app. The app can draw them as bars, which show where a frame was late.

## Model

`begin` starts a section with a name, and `end` stops the last section that started on the same thread. Sections can nest. `counter` sets a named value, which the trace shows as a graph. Each function does nothing while no trace records the app, so the calls cost almost nothing in a normal run. `enabled` tells whether a trace records the app.

The loop of `std/android` puts a section named `frame` around the step of each `Vsync` event.

`Frames` holds the times of the last 120 frames. `record` adds the time of a frame. `bars` gives a bar for each time, with the height of the bar in proportion to the time. A bar is late when its frame took longer than the time between two vsyncs.

## Contracts

Call `end` once for each `begin`, on the same thread. `counter` needs Android 10 (API level 29), and does nothing on older versions.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<C functions>>

<<sections>>

<<frames>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module puts named sections and counters into the system trace of
// Android, and keeps the times of the last frames of an app.
import "std/i64";
import "std/jni";
```

### `C functions`

`ATrace_setCounter` came in API level 29, so its import is weak, and `counter` reads the API level before it calls it. `__system_property_get` writes the value of a property, at most 92 bytes with its NUL, and gives its length.

```fern
<<C functions>>=
@import("libandroid.so", "ATrace_isEnabled")
function c_enabled(): i32;

@import("libandroid.so", "ATrace_beginSection")
function c_begin(name: usize): void;

@import("libandroid.so", "ATrace_endSection")
function c_end(): void;

@import("libandroid.so", "ATrace_setCounter", weak)
function c_counter(name: usize, value: i64): void;

@import("libc.so", "__system_property_get")
function c_property(name: usize, value: usize): i32;
```

### `sections`

```fern
<<sections>>=
// A trace records the app.
pub function enabled(): boolean {
  return c_enabled() != 0;
}

// Starts the section `name`.
pub function begin(name: string): void {
  if (!enabled()) {
    return;
  }
  let s = jni.cstr(name);
  c_begin(s);
  jni.free_buffer(s);
}

// Stops the last section that started on this thread.
pub function end(): void {
  if (enabled()) {
    c_end();
  }
}

// Sets the counter `name` to `value`.
pub function counter(name: string, value: i64): void {
  if (!enabled() || sdk() < 29) {
    return;
  }
  let s = jni.cstr(name);
  c_counter(s, value);
  jni.free_buffer(s);
}

// The API level of the device.
function sdk(): i32 {
  let name = jni.cstr("ro.build.version.sdk");
  let level = 0;
  unsafe {
    let buf: usize = __alloc(96);
    let n = c_property(name, buf);
    for i in 0..n {
      let b = __load_u8(buf + i as usize);
      if (b >= 48 && b <= 57) {
        level = level * 10 + b - 48;
      }
    }
    __free(buf, 96);
  }
  jni.free_buffer(name);
  return level;
}
```

### `frames`

```fern
<<frames>>=
// The number of frames that `Frames` keeps.
pub const KEPT: i32 = 120;

// The times of the last frames, in ns, oldest first.
pub struct Frames { times: i64[] }

pub function frames(): Frames {
  return Frames { times: [] };
}

// The frames with the time `ns` of a new frame added.
pub function record(f: Frames, ns: i64): Frames {
  let times = f.times;
  if (times.len() >= KEPT) {
    times = times.drop(times.len() - KEPT + 1);
  }
  return Frames { times: times.append(ns) };
}

// A bar of the overlay, in pixels. `late` is true when its frame took
// longer than the time between two vsyncs.
pub struct Bar { x: i32, y: i32, w: i32, h: i32, late: boolean }

// A bar for each frame in a box of w by h pixels, from the left, with the
// bottom of each bar on the bottom of the box. The top of the box is two
// vsync periods of `period` ns, and a bar stops at the top.
pub function bars(f: Frames, w: i32, h: i32, period: i64): Bar[] {
  let out: Bar[] = [];
  if (period <= 0i64) {
    return out;
  }
  let bw = (w / KEPT).max(1);
  for i in 0..f.times.len() {
    let t = f.times[i];
    let bh = (t * (h as i64) / (2i64 * period)).min(h as i64) as i32;
    out = out.append(Bar { x: i * bw, y: h - bh, w: bw, h: bh, late: t > period });
  }
  return out;
}
```
