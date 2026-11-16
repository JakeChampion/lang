# Camera

This module reads the frames of a camera of the phone, through the camera API of the NDK.

## Model

`start` opens the first camera that looks in the direction `facing`, and asks it for frames of `width` by `height` pixels. `sizes` gives the sizes that a camera can give. The camera sends its frames to an image reader. For each new frame the reader writes a byte to a pipe on the looper of the loop thread. Then the loop gives the event `android.Ready(android.IDENT_CAMERA)`, and `event` gives the newest frame. A frame that the app did not read before the next frame is lost, so a slow app sees fewer frames and not old ones.

A `Frame` has the luma `y` with one byte for each pixel, row by row. It has the chroma `u` and `v` with one byte for each block of 2 by 2 pixels. `ns` is the time of the frame on the monotonic clock in nanoseconds. `rgba` gives the frame as a `draw.Image`.

`stop` closes the camera. An open camera uses power, and other apps cannot use it. So stop the camera at each Pause, and start it again at the Resume.

The app needs the permission `CAMERA`. Declare it with `-apk-permission CAMERA` or in the `[android]` table. `allowed` tells whether the app has it, and `ask` shows its dialog.

## Contracts

Call each function on the loop thread, with the context that `launch.start` gave the loop. `start` gives None when the app does not have the permission, or when the phone has no camera that looks in the direction `facing`. A size that `sizes` does not give can give no frames.

The callbacks of the camera run on threads of the camera. While the loop thread runs Fern code, each callback waits for the lock of the library.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<C functions>>

<<frames>>

<<the camera>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads the frames of a camera of the phone.
import "std/android";
import "std/android/draw";
```

### `C functions`

```fern
<<C functions>>=
@import("libcamera2ndk.so", "ACameraManager_create")
function c_manager_create(): usize;

@import("libcamera2ndk.so", "ACameraManager_delete")
function c_manager_delete(manager: usize): void;

@import("libcamera2ndk.so", "ACameraManager_getCameraIdList")
function c_id_list(manager: usize, out: usize): i32;

@import("libcamera2ndk.so", "ACameraManager_deleteCameraIdList")
function c_id_list_delete(list: usize): void;

@import("libcamera2ndk.so", "ACameraManager_getCameraCharacteristics")
function c_characteristics(manager: usize, id: usize, out: usize): i32;

@import("libcamera2ndk.so", "ACameraMetadata_getConstEntry")
function c_entry(meta: usize, tag: u32, out: usize): i32;

@import("libcamera2ndk.so", "ACameraMetadata_free")
function c_metadata_free(meta: usize): void;

@import("libcamera2ndk.so", "ACameraManager_openCamera")
function c_open(manager: usize, id: usize, callbacks: usize, out: usize): i32;

@import("libcamera2ndk.so", "ACameraDevice_close")
function c_device_close(device: usize): i32;

@import("libcamera2ndk.so", "ACameraDevice_createCaptureRequest")
function c_request_create(device: usize, template: i32, out: usize): i32;

@import("libcamera2ndk.so", "ACaptureRequest_free")
function c_request_free(request: usize): void;

@import("libcamera2ndk.so", "ACameraOutputTarget_create")
function c_target_create(window: usize, out: usize): i32;

@import("libcamera2ndk.so", "ACameraOutputTarget_free")
function c_target_free(target: usize): void;

@import("libcamera2ndk.so", "ACaptureRequest_addTarget")
function c_request_add_target(request: usize, target: usize): i32;

@import("libcamera2ndk.so", "ACaptureSessionOutputContainer_create")
function c_container_create(out: usize): i32;

@import("libcamera2ndk.so", "ACaptureSessionOutputContainer_free")
function c_container_free(container: usize): void;

@import("libcamera2ndk.so", "ACaptureSessionOutput_create")
function c_output_create(window: usize, out: usize): i32;

@import("libcamera2ndk.so", "ACaptureSessionOutput_free")
function c_output_free(output: usize): void;

@import("libcamera2ndk.so", "ACaptureSessionOutputContainer_add")
function c_container_add(container: usize, output: usize): i32;

@import("libcamera2ndk.so", "ACameraDevice_createCaptureSession")
function c_session_create(device: usize, container: usize, callbacks: usize, out: usize): i32;

@import("libcamera2ndk.so", "ACameraCaptureSession_setRepeatingRequest")
function c_repeat(session: usize, callbacks: usize, count: i32, requests: usize, sequence: usize): i32;

@import("libcamera2ndk.so", "ACameraCaptureSession_stopRepeating")
function c_stop_repeating(session: usize): i32;

@import("libcamera2ndk.so", "ACameraCaptureSession_close")
function c_session_close(session: usize): void;

@import("libmediandk.so", "AImageReader_new")
function c_reader_new(width: i32, height: i32, format: i32, images: i32, out: usize): i32;

@import("libmediandk.so", "AImageReader_delete")
function c_reader_delete(reader: usize): void;

@import("libmediandk.so", "AImageReader_getWindow")
function c_reader_window(reader: usize, out: usize): i32;

@import("libmediandk.so", "AImageReader_setImageListener")
function c_reader_listen(reader: usize, listener: usize): i32;

@import("libmediandk.so", "AImageReader_acquireLatestImage")
function c_latest_image(reader: usize, out: usize): i32;

@import("libmediandk.so", "AImage_delete")
function c_image_delete(image: usize): void;

@import("libmediandk.so", "AImage_getTimestamp")
function c_image_time(image: usize, out: usize): i32;

@import("libmediandk.so", "AImage_getPlaneRowStride")
function c_row_stride(image: usize, plane: i32, out: usize): i32;

@import("libmediandk.so", "AImage_getPlanePixelStride")
function c_pixel_stride(image: usize, plane: i32, out: usize): i32;

@import("libmediandk.so", "AImage_getPlaneData")
function c_plane_data(image: usize, plane: i32, out: usize, length: usize): i32;

@import("libandroid.so", "ALooper_forThread")
function c_looper_for_thread(): usize;

@import("libandroid.so", "ALooper_addFd")
function c_looper_add_fd(looper: usize, fd: i32, ident: i32, events: i32, callback: usize, data: usize): i32;

@import("libandroid.so", "ALooper_removeFd")
function c_looper_remove_fd(looper: usize, fd: i32): i32;

@import("libc.so", "pipe2")
function c_pipe2(fds: usize, flags: i32): i32;

@import("libc.so", "read")
function c_read(fd: i32, buf: usize, n: usize): i64;

@import("libc.so", "close")
function c_close(fd: i32): i32;

@import("libc.so", "write")
function c_write(fd: i32, buf: usize, n: usize): i64;
```

### `frames`

The callbacks get the address of a block of 8 bytes: the write end of the pipe, then the byte to write.

```fern
<<frames>>=
pub const FRONT: i32 = 0;

pub const BACK: i32 = 1;

pub const EXTERNAL: i32 = 2;

pub struct Frame { width: i32, height: i32, ns: i64, y: u8[], u: u8[], v: u8[] }

pub struct Camera { manager: usize, device: usize, reader: usize, container: usize, output: usize, target: usize, request: usize, session: usize, pipe: i32, block: usize, width: i32, height: i32 }

const PERMISSION: string = "android.permission.CAMERA";

const LENS_FACING: u32 = 524293u32;

const STREAM_CONFIGURATIONS: u32 = 851978u32;

const YUV_420_888: i32 = 35;

const TEMPLATE_PREVIEW: i32 = 1;

// O_NONBLOCK | O_CLOEXEC
const PIPE_FLAGS: i32 = 526336;

const BLOCK: i32 = 8;

// The reader holds this many frames: one that the app reads, and one that
// the camera writes.
const IMAGES: i32 = 2;

@callback("frame_ready")
function frame_ready_entry(): usize;

@callback("camera_quiet")
function camera_quiet_entry(): usize;

@callback("camera_failed")
function camera_failed_entry(): usize;

// The onImageAvailable of the reader. It writes one byte to the pipe, so the
// loop wakes.
function frame_ready(block: usize, reader: usize): void {
  unsafe {
    let wrote: i64 = c_write(__load_i32(block), block + 4 as usize, 1 as usize);
  }
}

// onDisconnected of the device, and onClosed, onReady and onActive of the
// session.
function camera_quiet(block: usize, object: usize): void {}

// onError of the device. The camera then gives no more frames.
function camera_failed(block: usize, device: usize, error: i32): void {}

// The newest frame of `c`, when `e` is the Ready event of the camera.
pub function event(c: Camera, e: android.Event): Option[Frame] {
  if let android.Ready(ident) = e {
    if (ident == android.IDENT_CAMERA) {
      drain(c.pipe);
      return latest(c);
    }
  }
  return None;
}

function drain(fd: i32): void {
  unsafe {
    let buf: usize = __alloc(64);
    while (c_read(fd, buf, 64 as usize) > 0i64) {}
    __free(buf, 64);
  }
}

function latest(c: Camera): Option[Frame] {
  unsafe {
    let slot: usize = __alloc(8);
    __store_ptr(slot, 0 as usize);
    let got: i32 = c_latest_image(c.reader, slot);
    let image: usize = __load_ptr(slot);
    if (got != 0 || image == 0 as usize) {
      __free(slot, 8);
      return None;
    }
    let timed: i32 = c_image_time(image, slot);
    let ns: i64 = __load_i64(slot);
    __free(slot, 8);
    let f = Frame {
      width: c.width,
      height: c.height,
      ns: ns,
      y: plane(image, 0, c.width, c.height),
      u: plane(image, 1, c.width / 2, c.height / 2),
      v: plane(image, 2, c.width / 2, c.height / 2),
    };
    c_image_delete(image);
    return Some(f);
  }
}

// The bytes of plane `index` of `image`, `w` by `h` of them, with no gaps.
function plane(image: usize, index: i32, w: i32, h: i32): u8[] {
  let out: u8[] = [];
  unsafe {
    let slots: usize = __alloc(16);
    let rows: i32 = c_row_stride(image, index, slots);
    let row_stride: i32 = __load_i32(slots);
    let pixels: i32 = c_pixel_stride(image, index, slots);
    let pixel_stride: i32 = __load_i32(slots);
    let got: i32 = c_plane_data(image, index, slots, slots + 8 as usize);
    let data: usize = __load_ptr(slots);
    let length: i32 = __load_i32(slots + 8 as usize);
    __free(slots, 16);
    if (rows != 0 || pixels != 0 || got != 0 || data == 0 as usize) {
      return out;
    }
    for row in 0..h {
      for col in 0..w {
        let at = row * row_stride + col * pixel_stride;
        if (at < length) {
          out = out.append(__load_u8(data + at as usize) as u8);
        }
      }
    }
  }
  return out;
}

function clamped(v: i32): u8 {
  return v.max(0).min(255) as u8;
}

// The frame as red, green, blue and alpha, with the colours of BT.601.
pub function (f: Frame) rgba(): draw.Image {
  let cw = f.width / 2;
  if (f.y.len() < f.width * f.height || f.u.len() < cw * (f.height / 2) || f.v.len() < cw * (f.height / 2)) {
    return draw.Image { width: 0, height: 0, pixels: [] };
  }
  let px: u8[] = [];
  for row in 0..f.height {
    for col in 0..f.width {
      let c = (f.y[row * f.width + col] as i32 - 16) * 298;
      let at = row / 2 * cw + col / 2;
      let d = f.u[at] as i32 - 128;
      let e = f.v[at] as i32 - 128;
      px = px.append(clamped(c + 409 * e + 128 >> 8));
      px = px.append(clamped(c - 100 * d - 208 * e + 128 >> 8));
      px = px.append(clamped(c + 516 * d + 128 >> 8));
      px = px.append(255u8);
    }
  }
  return draw.Image { width: f.width, height: f.height, pixels: px };
}
```

### `the camera`

```fern
<<the camera>>=
pub function allowed(ctx: usize): boolean {
  return android.has_permission(ctx, PERMISSION);
}

// Shows the dialog of the permission. The answer comes with the next Resume.
pub function ask(ctx: usize): void {
  if (!allowed(ctx)) {
    android.request_permissions(ctx, [PERMISSION]);
  }
}

// An empty slot for a pointer that a C function writes.
function slot(): usize {
  unsafe {
    let s: usize = __alloc(8);
    __store_ptr(s, 0 as usize);
    return s;
  }
}

// The pointer in `s`, which it frees.
function taken(s: usize): usize {
  unsafe {
    let p: usize = __load_ptr(s);
    __free(s, 8);
    return p;
  }
}

// The direction of the camera `id`, or -1.
function facing_of(manager: usize, id: usize): i32 {
  unsafe {
    let s = slot();
    if (c_characteristics(manager, id, s) != 0) {
      let none = taken(s);
      return -1;
    }
    let meta = taken(s);
    let entry: usize = __alloc(24);
    let facing = -1;
    if (c_entry(meta, LENS_FACING, entry) == 0 && __load_i32(entry + 8 as usize) > 0) {
      facing = __load_u8(__load_ptr(entry + 16 as usize)) as i32;
    }
    __free(entry, 24);
    c_metadata_free(meta);
    return facing;
  }
}

// The sizes of the YUV frames of the camera `id`, as pairs of width and
// height.
function sizes_of(manager: usize, id: usize): i32[] {
  let out: i32[] = [];
  unsafe {
    let s = slot();
    if (c_characteristics(manager, id, s) != 0) {
      let none = taken(s);
      return out;
    }
    let meta = taken(s);
    let entry: usize = __alloc(24);
    if (c_entry(meta, STREAM_CONFIGURATIONS, entry) == 0) {
      let n: i32 = __load_i32(entry + 8 as usize);
      let data: usize = __load_ptr(entry + 16 as usize);
      let i = 0;
      while (i + 3 < n) {
        let at = data + (i * 4) as usize;
        if (__load_i32(at) == YUV_420_888 && __load_i32(at + 12 as usize) == 0) {
          out = out.append(__load_i32(at + 4 as usize)).append(__load_i32(at + 8 as usize));
        }
        i += 4;
      }
    }
    __free(entry, 24);
    c_metadata_free(meta);
  }
  return out;
}

// The ID of the first camera of `manager` that looks in the direction
// `facing`, in the list `list`, or 0.
function find(manager: usize, list: usize, facing: i32): usize {
  unsafe {
    let n: i32 = __load_i32(list);
    let ids: usize = __load_ptr(list + 8 as usize);
    for i in 0..n {
      let id: usize = __load_ptr(ids + (i * 8) as usize);
      if (facing_of(manager, id) == facing) {
        return id;
      }
    }
  }
  return 0 as usize;
}

// The sizes of the YUV frames of the first camera that looks in the
// direction `facing`, as pairs of width and height. It is empty when the
// phone has no such camera.
pub function sizes(facing: i32): (i32, i32)[] {
  let out: (i32, i32)[] = [];
  unsafe {
    let manager: usize = c_manager_create();
    let s = slot();
    if (c_id_list(manager, s) == 0) {
      let list = taken(s);
      let id = find(manager, list, facing);
      if (id != 0 as usize) {
        let flat = sizes_of(manager, id);
        let i = 0;
        while (i + 1 < flat.len()) {
          out = out.append((flat[i], flat[i + 1]));
          i += 2;
        }
      }
      c_id_list_delete(list);
    } else {
      let none = taken(s);
    }
    c_manager_delete(manager);
  }
  return out;
}

pub function has(facing: i32): boolean {
  return sizes(facing).len() > 0;
}

// The block for the callbacks, and the callback structs of the reader, the
// device and the session after it: 8 + 16 + 24 + 32 bytes.
const CALLBACKS: i32 = 80;

function callbacks(block: usize): void {
  unsafe {
    let reader = block + BLOCK as usize;
    __store_ptr(reader, block);
    __store_ptr(reader + 8 as usize, frame_ready_entry());
    let device = reader + 16 as usize;
    __store_ptr(device, block);
    __store_ptr(device + 8 as usize, camera_quiet_entry());
    __store_ptr(device + 16 as usize, camera_failed_entry());
    let session = device + 24 as usize;
    __store_ptr(session, block);
    __store_ptr(session + 8 as usize, camera_quiet_entry());
    __store_ptr(session + 16 as usize, camera_quiet_entry());
    __store_ptr(session + 24 as usize, camera_quiet_entry());
  }
}

pub function start(ctx: usize, facing: i32, width: i32, height: i32): Option[Camera] {
  if (!allowed(ctx)) {
    return None;
  }
  unsafe {
    let manager: usize = c_manager_create();
    let s = slot();
    if (c_id_list(manager, s) != 0) {
      let none = taken(s);
      c_manager_delete(manager);
      return None;
    }
    let list = taken(s);
    let id = find(manager, list, facing);
    if (id == 0 as usize) {
      c_id_list_delete(list);
      c_manager_delete(manager);
      return None;
    }
    let block: usize = __alloc(CALLBACKS);
    let fds: usize = __alloc(8);
    if (c_pipe2(fds, PIPE_FLAGS) != 0) {
      __free(fds, 8);
      __free(block, CALLBACKS);
      c_id_list_delete(list);
      c_manager_delete(manager);
      return None;
    }
    let pipe: i32 = __load_i32(fds);
    __store_i32(block, __load_i32(fds + 4 as usize));
    __store_u8(block + 4 as usize, 1);
    __free(fds, 8);
    callbacks(block);
    let device_slot = slot();
    let opened: i32 = c_open(manager, id, block + (BLOCK + 16) as usize, device_slot);
    c_id_list_delete(list);
    let device = taken(device_slot);
    let c = Camera { manager: manager, device: device, reader: 0 as usize, container: 0 as usize, output: 0 as usize, target: 0 as usize, request: 0 as usize, session: 0 as usize, pipe: pipe, block: block, width: width, height: height };
    if (opened != 0 || device == 0 as usize) {
      stop(c);
      return None;
    }
    return streamed(c);
  }
}

// `c` with its reader, its session and its repeating request, or None when
// the camera refuses one of them.
function streamed(c: Camera): Option[Camera] {
  unsafe {
    let s = slot();
    let made: i32 = c_reader_new(c.width, c.height, YUV_420_888, IMAGES, s);
    let r = Camera { ...c, reader: taken(s) };
    if (made != 0 || r.reader == 0 as usize) {
      stop(r);
      return None;
    }
    let listened: i32 = c_reader_listen(r.reader, r.block + BLOCK as usize);
    let ws = slot();
    let windowed: i32 = c_reader_window(r.reader, ws);
    let window = taken(ws);
    let cs = slot();
    let contained: i32 = c_container_create(cs);
    let os = slot();
    let output_made: i32 = c_output_create(window, os);
    let ts = slot();
    let target_made: i32 = c_target_create(window, ts);
    let qs = slot();
    let request_made: i32 = c_request_create(r.device, TEMPLATE_PREVIEW, qs);
    let o = Camera { ...r, container: taken(cs), output: taken(os), target: taken(ts), request: taken(qs) };
    if (listened != 0 || windowed != 0 || contained != 0 || output_made != 0 || target_made != 0 || request_made != 0) {
      stop(o);
      return None;
    }
    let added: i32 = c_container_add(o.container, o.output);
    let targeted: i32 = c_request_add_target(o.request, o.target);
    let ss = slot();
    let sessioned: i32 = c_session_create(o.device, o.container, o.block + (BLOCK + 40) as usize, ss);
    let out = Camera { ...o, session: taken(ss) };
    if (added != 0 || targeted != 0 || sessioned != 0) {
      stop(out);
      return None;
    }
    let requests: usize = __alloc(8);
    __store_ptr(requests, out.request);
    let repeating: i32 = c_repeat(out.session, 0 as usize, 1, requests, 0 as usize);
    __free(requests, 8);
    let added_fd: i32 = c_looper_add_fd(c_looper_for_thread(), out.pipe, android.IDENT_CAMERA, 1, 0 as usize, 0 as usize);
    if (repeating != 0 || added_fd != 1) {
      stop(out);
      return None;
    }
    return Some(out);
  }
}

// Closes the camera of `c` and frees each part of it.
pub function stop(c: Camera): void {
  unsafe {
    if (c.session != 0 as usize) {
      let stopped: i32 = c_stop_repeating(c.session);
      c_session_close(c.session);
    }
    if (c.request != 0 as usize) {
      c_request_free(c.request);
    }
    if (c.target != 0 as usize) {
      c_target_free(c.target);
    }
    if (c.output != 0 as usize) {
      c_output_free(c.output);
    }
    if (c.container != 0 as usize) {
      c_container_free(c.container);
    }
    if (c.device != 0 as usize) {
      let closed: i32 = c_device_close(c.device);
    }
    if (c.reader != 0 as usize) {
      c_reader_delete(c.reader);
    }
    c_manager_delete(c.manager);
    let removed: i32 = c_looper_remove_fd(c_looper_for_thread(), c.pipe);
    let a: i32 = c_close(c.pipe);
    let b: i32 = c_close(__load_i32(c.block));
    __free(c.block, CALLBACKS);
  }
}
```
