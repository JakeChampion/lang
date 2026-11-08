# Android media player

This module plays video and audio from a file or an HTTP stream with `android.media.MediaPlayer`, for a NativeActivity app of `std/android`.

## Model

`MediaPlayer` reads the stream, decodes it, keeps the audio and the video in time, and shows the video on a `Surface`. It reads a progressive file, such as MP4, Matroska or WebM, and HLS. The app declares a @jni class that implements the five listener interfaces of the player. Its native methods call `prepared`, `completed`, `failed`, `info` and `seeked`. `new` makes one player and one listener of that class.

The listener runs on the main thread of the app. Each callback sends an event to the loop thread of the live activity, which the loop gives to the app as a `Posted` event. `event` decodes it as a `Change`.

`open` starts to prepare a stream, and `Prepared` comes when the player can start. `show` puts the video on the window of the activity: it calls `Window.release_cpu` first, because a window has one producer of pixels at a time. `hide` takes the video off the window, so the app can lock the window and draw on it again. Do not lock the window between `show` and `hide`.

`Buffering(true)` and `Buffering(false)` come when the player waits for the network and when it plays again. `Rendering` comes when the first video frame shows. `Failed` has the `what` and `extra` codes of `MediaPlayer.OnErrorListener`.

The module needs API level 26, for `ANativeWindow_toSurface` and the long seek API.
Android selects the codecs and controls A/V synchronization.
This API does not select a named hardware or software codec.
Use `std/android/video` when the app needs explicit codec policies.

`timestamp` returns the platform media-time and monotonic-time anchor.
The media time uses microseconds. The monotonic time uses nanoseconds.
An unavailable or unknown anchor returns `None`.
The anchor does not include the clock rate or a physical output latency measurement.

## Contracts

Call each function except the five callbacks on the loop thread.
Close each successful player once before Destroy.
`close` releases both global Java references, even if Java `release` throws.
State queries return zero, false, or `None` before `Prepared`.

Each operation bounds its local JNI references with a local frame.
Handled exceptions clear before the operation returns.
A preexisting caller exception remains pending and prevents the operation.
Invalid method or object results prevent further JNI calls on that result.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<events>>

<<the player>>

<<the listener>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module plays media with android.media.MediaPlayer.
import "std/android";
import "std/jni";
```

### `events`

The kinds from 19 to 23 belong to this module. A `Failed` event packs `what` in the high 32 bits of its argument and `extra` in the low 32 bits. An `Info` event has `what` as its argument.

```fern
<<events>>=
pub enum Change { Prepared, Completed, Failed(i32, i32), Buffering(boolean), Rendering, Seeked }

const PREPARED: i32 = 19;

const COMPLETED: i32 = 20;

const FAILED: i32 = 21;

const INFO: i32 = 22;

const SEEKED: i32 = 23;

// MediaPlayer.MEDIA_INFO_VIDEO_RENDERING_START, BUFFERING_START and
// BUFFERING_END.
const INFO_RENDERING: i32 = 3;

const INFO_BUFFERING_START: i32 = 701;

const INFO_BUFFERING_END: i32 = 702;

function info_change(what: i32): Option[Change] {
  if (what == INFO_RENDERING) {
    return Some(Rendering);
  }
  if (what == INFO_BUFFERING_START) {
    return Some(Buffering(true));
  }
  if (what == INFO_BUFFERING_END) {
    return Some(Buffering(false));
  }
  return None;
}

// The change of the player that a Posted event carries, or None for an
// event of another module.
pub function event(e: android.Event): Option[Change] {
  let android.Posted(kind, arg) = e else {
    return None;
  };
  if (kind == PREPARED) {
    return Some(Prepared);
  }
  if (kind == COMPLETED) {
    return Some(Completed);
  }
  if (kind == SEEKED) {
    return Some(Seeked);
  }
  if (kind == FAILED) {
    let v = arg as u64;
    return Some(Failed((v >> 32u64) as i32, (v & 4294967295u64) as i32));
  }
  if (kind == INFO) {
    return info_change(arg as i32);
  }
  return None;
}
```

### `the player`

`MediaPlayer` keeps its listeners across `reset`, so `open` can play one stream after another.
A failed reset prevents source and preparation calls.
`open` rejects empty addresses, embedded NUL bytes, and addresses longer than 8192 UTF-8 bytes.
`seek` uses the API 26 method with `SEEK_CLOSEST`.
It rejects negative positions and positions above 2,147,483,647 milliseconds.
Android clamps larger values internally, despite the Java long parameter.

```fern
<<the player>>=
// The global references of the player and its listener.
pub struct Player { obj: usize, listener: usize }

const PLAYER: string = "android/media/MediaPlayer";

const ATTRS: string = "android/media/AudioAttributes$Builder";

// AudioAttributes.USAGE_MEDIA and CONTENT_TYPE_MOVIE.
const USAGE_MEDIA: i32 = 1;

const CONTENT_MOVIE: i32 = 3;

function cleared(e: usize): boolean {
  let exception = jni.exception_occurred(e);
  if (exception == 0 as usize) {
    return false;
  }
  let _ = jni.exception_clear(e);
  let _ = jni.delete_local_ref(e, exception);
  return true;
}

function begin(e: usize): boolean {
  if (e == 0 as usize) {
    return false;
  }
  let pending = jni.exception_occurred(e);
  if (pending != 0 as usize) {
    let _ = jni.delete_local_ref(e, pending);
    return false;
  }
  if (jni.call1(e, 19, 64 as usize) as i32 != 0) {
    let _ = cleared(e);
    return false;
  }
  return true;
}

function end(e: usize): void {
  let _ = jni.call1(e, 20, 0 as usize);
}

function method(e: usize, obj: usize, name: string, sig: string): usize {
  if (obj == 0 as usize) {
    return 0 as usize;
  }
  let class = jni.get_object_class(e, obj);
  if (cleared(e) || class == 0 as usize) {
    return 0 as usize;
  }
  let id = jni.get_method_id(e, class, name, sig);
  if (cleared(e)) {
    return 0 as usize;
  }
  return id;
}

function call_void(e: usize, obj: usize, name: string, sig: string, args: usize[]): boolean {
  let id = method(e, obj, name, sig);
  if (id == 0 as usize) {
    return false;
  }
  let values = jni.jvalues(args);
  let _ = jni.call_void_method_a(e, obj, id, values);
  jni.free_buffer(values);
  return !cleared(e);
}

function call_object(e: usize, obj: usize, name: string, sig: string, args: usize[]): usize {
  let id = method(e, obj, name, sig);
  if (id == 0 as usize) {
    return 0 as usize;
  }
  let values = jni.jvalues(args);
  let value = jni.call_object_method_a(e, obj, id, values);
  jni.free_buffer(values);
  if (cleared(e)) {
    return 0 as usize;
  }
  return value;
}

function call_int(e: usize, obj: usize, name: string): i32 {
  let id = method(e, obj, name, "()I");
  if (id == 0 as usize) {
    return 0;
  }
  let value = jni.call_int_method_a(e, obj, id, 0 as usize);
  if (cleared(e)) {
    return 0;
  }
  return value;
}

function call_long(e: usize, obj: usize, name: string): Option[i64] {
  let id = method(e, obj, name, "()J");
  if (id == 0 as usize) {
    return None;
  }
  let value = jni.call_long_method_a(e, obj, id, 0 as usize) as i64;
  if (cleared(e)) {
    return None;
  }
  return Some(value);
}

function find_class(e: usize, name: string): usize {
  let class = jni.find_class(e, name);
  if (cleared(e)) {
    return 0 as usize;
  }
  return class;
}

function new_object(e: usize, class: usize, sig: string, args: usize[]): usize {
  if (class == 0 as usize) {
    return 0 as usize;
  }
  let id = jni.get_method_id(e, class, "<init>", sig);
  if (cleared(e) || id == 0 as usize) {
    return 0 as usize;
  }
  let values = jni.jvalues(args);
  let value = jni.new_object_a(e, class, id, values);
  jni.free_buffer(values);
  if (cleared(e)) {
    return 0 as usize;
  }
  return value;
}

function load_class(e: usize, activity: usize, name: string): usize {
  let loader = call_object(e, activity, "getClassLoader", "()Ljava/lang/ClassLoader;", []);
  let text = jni.new_string(e, name);
  if (cleared(e) || text == 0 as usize) {
    return 0 as usize;
  }
  return call_object(e, loader, "loadClass", "(Ljava/lang/String;)Ljava/lang/Class;", [text]);
}

function attributes(e: usize): usize {
  let b = new_object(e, find_class(e, ATTRS), "()V", []);
  let usage = call_object(e, b, "setUsage", "(I)L" + ATTRS + ";", [USAGE_MEDIA as usize]);
  if (usage == 0 as usize) {
    return 0 as usize;
  }
  let content = call_object(e, b, "setContentType", "(I)L" + ATTRS + ";", [CONTENT_MOVIE as usize]);
  if (content == 0 as usize) {
    return 0 as usize;
  }
  return call_object(e, b, "build", "()Landroid/media/AudioAttributes;", []);
}

function listen(e: usize, mp: usize, listener: usize, name: string, iface: string): boolean {
  return call_void(e, mp, name, "(Landroid/media/MediaPlayer$" + iface + ";)V", [listener]);
}

// A player whose listener is a new object of `class`, the Java name of the
// app's @jni class that implements the listeners. None when the class or
// the player cannot be made.
pub function new(ctx: usize, class: string): Option[Player] {
  let e = android.env(ctx);
  if (!begin(e)) {
    return None;
  }
  defer end(e);
  let listener = new_object(e, load_class(e, android.activity_object(ctx), class), "()V", []);
  if (listener == 0 as usize) {
    return None;
  }
  let mp = new_object(e, find_class(e, PLAYER), "()V", []);
  if (mp == 0 as usize) {
    return None;
  }
  let ok = listen(e, mp, listener, "setOnPreparedListener", "OnPreparedListener") && listen(e, mp, listener, "setOnCompletionListener", "OnCompletionListener") && listen(e, mp, listener, "setOnErrorListener", "OnErrorListener") && listen(e, mp, listener, "setOnInfoListener", "OnInfoListener") && listen(e, mp, listener, "setOnSeekCompleteListener", "OnSeekCompleteListener");
  if (!ok || !set_attributes(e, mp)) {
    let _ = call_void(e, mp, "release", "()V", []);
    return None;
  }
  return retain(e, mp, listener);
}

function set_attributes(e: usize, mp: usize): boolean {
  let attrs = attributes(e);
  return attrs != 0 as usize && call_void(e, mp, "setAudioAttributes", "(Landroid/media/AudioAttributes;)V", [attrs]);
}

function retain(e: usize, mp: usize, listener: usize): Option[Player] {
  let obj = jni.new_global_ref(e, mp);
  if (cleared(e) || obj == 0 as usize) {
    let _ = call_void(e, mp, "release", "()V", []);
    return None;
  }
  let saved_listener = jni.new_global_ref(e, listener);
  if (cleared(e) || saved_listener == 0 as usize) {
    let _ = jni.delete_global_ref(e, obj);
    let _ = call_void(e, mp, "release", "()V", []);
    return None;
  }
  return Some(Player { obj: obj, listener: saved_listener });
}

// Starts to prepare the stream at `uri`: a path, or an http or https
// address. Prepared or Failed follows. It is false when the player refuses
// the address at once.
function valid_uri(uri: string): boolean {
  if (uri.len() == 0 || uri.len() > 8192) {
    return false;
  }
  for byte in uri.as_bytes() {
    if (byte == 0u8) {
      return false;
    }
  }
  return true;
}

pub function open(ctx: usize, p: Player, uri: string): boolean {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  if (!valid_uri(uri) || !call_void(e, p.obj, "reset", "()V", [])) {
    return false;
  }
  let text = jni.new_string(e, uri);
  if (cleared(e) || text == 0 as usize) {
    return false;
  }
  return call_void(e, p.obj, "setDataSource", "(Ljava/lang/String;)V", [text]) && call_void(e, p.obj, "prepareAsync", "()V", []);
}

// Shows the video on window `w`. It gives the window up as the target of
// lock first.
pub function show(ctx: usize, p: Player, w: android.Window): boolean {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  let released = w.release_cpu();
  let surface: usize = w.surface(e);
  if (cleared(e) || surface == 0 as usize) {
    return false;
  }
  let ok = call_void(e, p.obj, "setSurface", "(Landroid/view/Surface;)V", [surface]);
  let gone: usize = jni.delete_local_ref(e, surface);
  return ok;
}

// Takes the video off the window, so the app can lock it again.
pub function hide(ctx: usize, p: Player): void {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return;
  }
  defer end(e);
  let ok = call_void(e, p.obj, "setSurface", "(Landroid/view/Surface;)V", [0 as usize]);
}

pub function start(ctx: usize, p: Player): boolean {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  return call_void(e, p.obj, "start", "()V", []);
}

pub function pause(ctx: usize, p: Player): boolean {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  return call_void(e, p.obj, "pause", "()V", []);
}

// Moves to `ms` milliseconds from the start. Seeked follows.
pub function seek(ctx: usize, p: Player, ms: i64): boolean {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  if (ms < 0i64 || ms > 2147483647i64) {
    return false;
  }
  return call_void(e, p.obj, "seekTo", "(JI)V", [ms as usize, 3 as usize]);
}

pub function position_ms(ctx: usize, p: Player): i64 {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return 0i64;
  }
  defer end(e);
  return call_int(e, p.obj, "getCurrentPosition") as i64;
}

// The length of the stream, or 0 when it has none, as a live stream.
pub function duration_ms(ctx: usize, p: Player): i64 {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return 0i64;
  }
  defer end(e);
  let d = call_int(e, p.obj, "getDuration");
  if (d < 0) {
    return 0i64;
  }
  return d as i64;
}

pub function playing(ctx: usize, p: Player): boolean {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  let id = method(e, p.obj, "isPlaying", "()Z");
  if (id == 0 as usize) {
    return false;
  }
  let got = jni.call_boolean_method_a(e, p.obj, id, 0 as usize);
  return !cleared(e) && got != 0;
}

pub struct Timestamp { media_us: i64, monotonic_ns: i64 }

// The platform's media and system time anchor, or None when it is unknown.
// An anchor does not establish the clock rate or physical output latency.
pub function timestamp(ctx: usize, p: Player): Option[Timestamp] {
  let e = android.env(ctx);
  if (!begin(e)) {
    return None;
  }
  defer end(e);
  let anchor = call_object(e, p.obj, "getTimestamp", "()Landroid/media/MediaTimestamp;", []);
  if (anchor == 0 as usize) {
    return None;
  }
  let Some(media) = call_long(e, anchor, "getAnchorMediaTimeUs") else {
    return None;
  };
  // This spelling is the API 23 method. The corrected name needs API 28.
  let Some(system) = call_long(e, anchor, "getAnchorSytemNanoTime") else {
    return None;
  };
  if (media < 0i64 || system < 0i64) {
    return None;
  }
  return Some(Timestamp { media_us: media, monotonic_ns: system });
}

// The width and the height of the video, or 0 and 0 for audio.
pub function video_size(ctx: usize, p: Player): (i32, i32) {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return (0, 0);
  }
  defer end(e);
  return (call_int(e, p.obj, "getVideoWidth"), call_int(e, p.obj, "getVideoHeight"));
}

// Stops the stream and takes the video off the window. The player keeps its
// listeners, and `open` plays the next stream.
pub function stop(ctx: usize, p: Player): void {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return;
  }
  defer end(e);
  let surface = call_void(e, p.obj, "setSurface", "(Landroid/view/Surface;)V", [0 as usize]);
  let reset = call_void(e, p.obj, "reset", "()V", []);
}

// Stops the player and frees it and its listener.
pub function close(ctx: usize, p: Player): void {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return;
  }
  defer end(e);
  let released = call_void(e, p.obj, "release", "()V", []);
  jni.delete_global_ref(e, p.listener);
  jni.delete_global_ref(e, p.obj);
}
```

### `the listener`

Each native method of the app's listener calls one of these with its `JNIEnv` and its arguments. The listener of the error returns true, so no Completed follows a Failed. The listener of the info returns false, so the player does its own handling too.

```fern
<<the listener>>=
function post(e: usize, kind: i32, arg: usize): void {
  if (!begin(e)) {
    return;
  }
  defer end(e);
  let activity: usize = android.live_activity(e);
  if (activity != 0 as usize) {
    android.send(activity, kind, arg);
  }
}

pub function prepared(e: usize, listener: usize, mp: usize): void {
  post(e, PREPARED, 0 as usize);
}

pub function completed(e: usize, listener: usize, mp: usize): void {
  post(e, COMPLETED, 0 as usize);
}

pub function failed(e: usize, listener: usize, mp: usize, what: i32, extra: i32): u8 {
  post(e, FAILED, ((what as u64 & 4294967295u64) << 32u64 | extra as u64 & 4294967295u64) as usize);
  return 1u8;
}

pub function info(e: usize, listener: usize, mp: usize, what: i32, extra: i32): u8 {
  if let Some(_) = info_change(what) {
    post(e, INFO, what as usize);
  }
  return 0u8;
}

pub function seeked(e: usize, listener: usize, mp: usize): void {
  post(e, SEEKED, 0 as usize);
}
```
