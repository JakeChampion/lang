# Android layers

This module shows a layer of pixels above the window of a NativeActivity app of `std/android`. The display compositor moves and fades the layer, so an animation of it draws no pixels in the app.

## Model

A layer is a child SurfaceControl of the window with a hardware buffer. `create` makes the layer and keeps it hidden. `fill` copies the pixels of a frame into a new buffer and gives that buffer to the layer. `show` puts the layer at a position with an alpha, and `hide` takes it off the screen. Each call of `show` or `hide` is one transaction of the compositor, which applies at a vsync.

The app draws a layer once, then calls `show` at each frame of an animation, for example at each `Vsync` event. The window under the layer keeps its pixels.

The module needs API level 29, for the SurfaceControl functions of the NDK. `supported` tells whether the device has them.

## Contracts

A layer is valid until `release`, and only while its window is valid. Release each layer before the `WindowDestroyed` event of its window returns. Call each function on the loop thread.

## Module assembly

The root chunk keeps the order of the declarations.

```fern
<<*>>=
<<module introduction>>

<<C functions>>

<<layers>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module shows a layer of pixels above the window of a NativeActivity
// app, which the display compositor moves and fades.
import "std/android";
import "std/android/draw";
import "std/jni";
```

### `C functions`

The functions of `libandroid.so` that the module calls. The SurfaceControl functions came in Android 10. Each one is a weak import, so the library loads on an older device, and `supported` keeps the module from calling them there.

An `AHardwareBuffer_Desc` is 40 bytes. The width is at 0, the height at 4 and the layers at 8. The format is at 12, the usage at 16 and the stride at 24. An `ARect` has the left, top, right and bottom edges, 16 bytes.

```fern
<<C functions>>=
@import("libandroid.so", "ASurfaceControl_createFromWindow", weak)
function c_control_from_window(window: usize, name: usize): usize;

@import("libandroid.so", "ASurfaceControl_release", weak)
function c_control_release(control: usize): void;

@import("libandroid.so", "ASurfaceTransaction_create", weak)
function c_tx_create(): usize;

@import("libandroid.so", "ASurfaceTransaction_delete", weak)
function c_tx_delete(tx: usize): void;

@import("libandroid.so", "ASurfaceTransaction_apply", weak)
function c_tx_apply(tx: usize): void;

@import("libandroid.so", "ASurfaceTransaction_setBuffer", weak)
function c_tx_buffer(tx: usize, control: usize, buffer: usize, fence: i32): void;

@import("libandroid.so", "ASurfaceTransaction_setVisibility", weak)
function c_tx_visibility(tx: usize, control: usize, visible: i32): void;

@import("libandroid.so", "ASurfaceTransaction_setZOrder", weak)
function c_tx_z(tx: usize, control: usize, z: i32): void;

@import("libandroid.so", "ASurfaceTransaction_setBufferAlpha", weak)
function c_tx_alpha(tx: usize, control: usize, alpha: f32): void;

@import("libandroid.so", "ASurfaceTransaction_setBufferTransparency", weak)
function c_tx_transparency(tx: usize, control: usize, transparency: i32): void;

@import("libandroid.so", "ASurfaceTransaction_setGeometry", weak)
function c_tx_geometry(tx: usize, control: usize, source: usize, destination: usize, transform: i32): void;

@import("libandroid.so", "ASurfaceTransaction_reparent", weak)
function c_tx_reparent(tx: usize, control: usize, parent: usize): void;

@import("libandroid.so", "AHardwareBuffer_allocate")
function c_buffer_allocate(desc: usize, out: usize): i32;

@import("libandroid.so", "AHardwareBuffer_describe")
function c_buffer_describe(buffer: usize, desc: usize): void;

@import("libandroid.so", "AHardwareBuffer_lock")
function c_buffer_lock(buffer: usize, usage: u64, fence: i32, rect: usize, out: usize): i32;

@import("libandroid.so", "AHardwareBuffer_unlock")
function c_buffer_unlock(buffer: usize, fence: usize): i32;

@import("libandroid.so", "AHardwareBuffer_release")
function c_buffer_release(buffer: usize): void;
```

### `layers`

The buffer has the format `AHARDWAREBUFFER_FORMAT_R8G8B8A8_UNORM`, the pixel word of `std/android/draw`. Its usage lets the app write it often, the GPU sample it, and the compositor show it as an overlay. A layer is opaque, so the compositor need not blend its pixels with the window, only its alpha.

```fern
<<layers>>=
// A layer above a window: its SurfaceControl, its buffer, or 0 before the
// first `fill`, and its size.
pub struct Layer { control: usize, buffer: usize, width: i32, height: i32 }

// AHARDWAREBUFFER_USAGE_CPU_WRITE_OFTEN, GPU_SAMPLED_IMAGE and
// COMPOSER_OVERLAY.
const USAGE: u64 = 2352u64;

const WRITE_OFTEN: u64 = 48u64;

// ASURFACE_TRANSACTION_TRANSPARENCY_OPAQUE.
const OPAQUE: i32 = 2;

// The device has the SurfaceControl functions of the NDK.
pub function supported(ctx: usize): boolean {
  return android.sdk_version(ctx) >= 29;
}

// A hidden layer of `width` by `height` above window `w`, or None when the
// system makes none.
pub function create(w: android.Window, name: string, width: i32, height: i32): Option[Layer] {
  let n = jni.cstr(name);
  let control = c_control_from_window(w.ptr, n);
  jni.free_buffer(n);
  if (control == 0 as usize) {
    return None;
  }
  let tx = c_tx_create();
  c_tx_z(tx, control, 1);
  c_tx_visibility(tx, control, 0);
  c_tx_apply(tx);
  c_tx_delete(tx);
  return Some(Layer { control: control, buffer: 0 as usize, width: width, height: height });
}

function rect(x0: i32, y0: i32, x1: i32, y1: i32): usize {
  unsafe {
    let r = __alloc(16);
    __store_i32(r, x0);
    __store_i32(r + 4 as usize, y0);
    __store_i32(r + 8 as usize, x1);
    __store_i32(r + 12 as usize, y1);
    return r;
  }
}

// A new buffer of the size of the layer, or 0.
function allocate(l: Layer): usize {
  unsafe {
    let desc = __alloc(40);
    __memset(desc, 0, 40);
    __store_i32(desc, l.width);
    __store_i32(desc + 4 as usize, l.height);
    __store_i32(desc + 8 as usize, 1);
    __store_i32(desc + 12 as usize, 1);
    __store_i64(desc + 16 as usize, USAGE as i64);
    let out = __alloc(8);
    __store_ptr(out, 0 as usize);
    let failed = c_buffer_allocate(desc, out);
    let buffer = __load_ptr(out);
    __free(out, 8);
    __free(desc, 40);
    if (failed != 0) {
      return 0 as usize;
    }
    return buffer;
  }
}

// Copies the pixels of `src` into the buffer, row by row with its stride.
function copy_into(buffer: usize, src: draw.Frame, w: i32, h: i32): boolean {
  unsafe {
    let desc = __alloc(40);
    c_buffer_describe(buffer, desc);
    let stride = __load_i32(desc + 24 as usize);
    __free(desc, 40);
    let out = __alloc(8);
    __store_ptr(out, 0 as usize);
    let locked = c_buffer_lock(buffer, WRITE_OFTEN, 0 - 1, 0 as usize, out);
    let bits = __load_ptr(out);
    __free(out, 8);
    if (locked != 0 || bits == 0 as usize) {
      return false;
    }
    let cw = w;
    if (src.width < cw) {
      cw = src.width;
    }
    let rows = h;
    if (src.height < rows) {
      rows = src.height;
    }
    for row in 0..rows {
      __memcpy(bits + (row * stride * 4) as usize, src.bits + (row * src.stride * 4) as usize, cw * 4);
    }
    let unlocked = c_buffer_unlock(buffer, 0 as usize);
    return true;
  }
}

// The layer with the pixels of `src` in a new buffer, which the layer shows
// from its next `show`. The old buffer goes.
pub function (l: Layer) fill(src: draw.Frame): Layer {
  let buffer = allocate(l);
  if (buffer == 0 as usize) {
    return l;
  }
  if (!copy_into(buffer, src, l.width, l.height)) {
    c_buffer_release(buffer);
    return l;
  }
  let tx = c_tx_create();
  c_tx_buffer(tx, l.control, buffer, 0 - 1);
  c_tx_transparency(tx, l.control, OPAQUE);
  c_tx_apply(tx);
  c_tx_delete(tx);
  if (l.buffer != 0 as usize) {
    c_buffer_release(l.buffer);
  }
  return Layer { ...l, buffer: buffer };
}

// Shows the layer with its top left corner at x and y of the window, with
// the alpha `alpha` from 0 to 1.
pub function (l: Layer) show(x: i32, y: i32, alpha: f32): void {
  if (l.buffer == 0 as usize) {
    return;
  }
  let source = rect(0, 0, l.width, l.height);
  let destination = rect(x, y, x + l.width, y + l.height);
  let tx = c_tx_create();
  c_tx_geometry(tx, l.control, source, destination, 0);
  c_tx_alpha(tx, l.control, alpha);
  c_tx_visibility(tx, l.control, 1);
  c_tx_apply(tx);
  c_tx_delete(tx);
  unsafe {
    __free(source, 16);
    __free(destination, 16);
  }
}

pub function (l: Layer) hide(): void {
  let tx = c_tx_create();
  c_tx_visibility(tx, l.control, 0);
  c_tx_apply(tx);
  c_tx_delete(tx);
}

// Takes the layer off its window and frees it.
pub function release(l: Layer): void {
  let tx = c_tx_create();
  c_tx_reparent(tx, l.control, 0 as usize);
  c_tx_apply(tx);
  c_tx_delete(tx);
  c_control_release(l.control);
  if (l.buffer != 0 as usize) {
    c_buffer_release(l.buffer);
  }
}
```
