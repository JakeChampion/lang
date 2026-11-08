# Android media player

This module plays video and audio from a file or an HTTP stream with `android.media.MediaPlayer`, for a NativeActivity app of `std/android`.

## Model

`MediaPlayer` reads the stream, decodes it, keeps the audio and the video in time, and shows the video on a `Surface`. It reads a progressive file, such as MP4, Matroska or WebM, and HLS. The app declares a @jni class that implements the five listener interfaces of the player. Its native methods call `prepared`, `completed`, `failed`, `info` and `seeked`. `new` makes one player and one listener of that class.

The listener runs on the main thread of the app. Each callback sends an event to the loop thread of the live activity, which the loop gives to the app as a `Posted` event. `event` decodes it as a `Change`.

`open` starts to prepare a stream, and `Prepared` comes when the player can start. `show` puts the video on the window of the activity: it calls `Window.release_cpu` first, because a window has one producer of pixels at a time. `hide` takes the video off the window, so the app can lock the window and draw on it again. Do not lock the window between `show` and `hide`.

`Buffering(true)` and `Buffering(false)` come when the player waits for the network and when it plays again. `Rendering` comes when the first video frame shows. `Failed` has the `what` and `extra` codes of `MediaPlayer.OnErrorListener`.

The module needs API level 26, for `ANativeWindow_toSurface` and `AudioAttributes`.

## Contracts

Call each function except the five callbacks on the loop thread. Call `close` before Destroy. The functions that read the state of the player give 0 or false before `Prepared`.

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

`MediaPlayer` keeps its listeners across `reset`, so `open` can play one stream after another. A call that throws gives false or 0, and the exception is cleared.

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
  if (jni.exception_occurred(e) != 0 as usize) {
    let c: usize = jni.exception_clear(e);
    return true;
  }
  return false;
}

function call_void(e: usize, obj: usize, name: string, sig: string, args: usize[]): boolean {
  let values: usize = jni.jvalues(args);
  let done: usize = jni.call_void_method_a(e, obj, jni.get_method_id(e, jni.get_object_class(e, obj), name, sig), values);
  jni.free_buffer(values);
  return !cleared(e);
}

function call_int(e: usize, obj: usize, name: string): i32 {
  let none: usize = jni.jvalue_alloc(0);
  let got: i32 = jni.call_int_method_a(e, obj, jni.get_method_id(e, jni.get_object_class(e, obj), name, "()I"), none);
  jni.free_buffer(none);
  if (cleared(e)) {
    return 0;
  }
  return got;
}

function new_object(e: usize, class: usize, sig: string, args: usize[]): usize {
  let values: usize = jni.jvalues(args);
  let obj: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", sig), values);
  jni.free_buffer(values);
  return obj;
}

function attributes(e: usize): usize {
  let b: usize = new_object(e, jni.find_class(e, ATTRS), "()V", []);
  let usage: usize = jni.call_object(e, b, "setUsage", "(I)L" + ATTRS + ";", [USAGE_MEDIA as usize]);
  let content: usize = jni.call_object(e, b, "setContentType", "(I)L" + ATTRS + ";", [CONTENT_MOVIE as usize]);
  return jni.call_object(e, b, "build", "()Landroid/media/AudioAttributes;", []);
}

function listen(e: usize, mp: usize, listener: usize, name: string, iface: string): boolean {
  return call_void(e, mp, name, "(Landroid/media/MediaPlayer$" + iface + ";)V", [listener]);
}

// A player whose listener is a new object of `class`, the Java name of the
// app's @jni class that implements the listeners. None when the class or
// the player cannot be made.
pub function new(ctx: usize, class: string): Option[Player] {
  let e: usize = android.env(ctx);
  let listener: usize = new_object(e, jni.load_class(e, android.activity_object(ctx), class), "()V", []);
  if (cleared(e) || listener == 0 as usize) {
    return None;
  }
  let mp: usize = new_object(e, jni.find_class(e, PLAYER), "()V", []);
  if (cleared(e) || mp == 0 as usize) {
    return None;
  }
  let ok = listen(e, mp, listener, "setOnPreparedListener", "OnPreparedListener") && listen(e, mp, listener, "setOnCompletionListener", "OnCompletionListener") && listen(e, mp, listener, "setOnErrorListener", "OnErrorListener") && listen(e, mp, listener, "setOnInfoListener", "OnInfoListener") && listen(e, mp, listener, "setOnSeekCompleteListener", "OnSeekCompleteListener");
  let attrs = call_void(e, mp, "setAudioAttributes", "(Landroid/media/AudioAttributes;)V", [attributes(e)]);
  if (!ok) {
    let released = call_void(e, mp, "release", "()V", []);
    return None;
  }
  return Some(Player { obj: jni.new_global_ref(e, mp), listener: jni.new_global_ref(e, listener) });
}

// Starts to prepare the stream at `uri`: a path, or an http or https
// address. Prepared or Failed follows. It is false when the player refuses
// the address at once.
pub function open(ctx: usize, p: Player, uri: string): boolean {
  let e: usize = android.env(ctx);
  let reset = call_void(e, p.obj, "reset", "()V", []);
  return call_void(e, p.obj, "setDataSource", "(Ljava/lang/String;)V", [jni.new_string(e, uri)]) && call_void(e, p.obj, "prepareAsync", "()V", []);
}

// Shows the video on window `w`. It gives the window up as the target of
// lock first.
pub function show(ctx: usize, p: Player, w: android.Window): boolean {
  let e: usize = android.env(ctx);
  let released = w.release_cpu();
  let surface: usize = w.surface(e);
  if (surface == 0 as usize) {
    return false;
  }
  let ok = call_void(e, p.obj, "setSurface", "(Landroid/view/Surface;)V", [surface]);
  let gone: usize = jni.delete_local_ref(e, surface);
  return ok;
}

// Takes the video off the window, so the app can lock it again.
pub function hide(ctx: usize, p: Player): void {
  let ok = call_void(android.env(ctx), p.obj, "setSurface", "(Landroid/view/Surface;)V", [0 as usize]);
}

pub function start(ctx: usize, p: Player): boolean {
  return call_void(android.env(ctx), p.obj, "start", "()V", []);
}

pub function pause(ctx: usize, p: Player): boolean {
  return call_void(android.env(ctx), p.obj, "pause", "()V", []);
}

// Moves to `ms` milliseconds from the start. Seeked follows.
pub function seek(ctx: usize, p: Player, ms: i64): boolean {
  return call_void(android.env(ctx), p.obj, "seekTo", "(I)V", [ms as usize]);
}

pub function position_ms(ctx: usize, p: Player): i64 {
  return call_int(android.env(ctx), p.obj, "getCurrentPosition") as i64;
}

// The length of the stream, or 0 when it has none, as a live stream.
pub function duration_ms(ctx: usize, p: Player): i64 {
  let d = call_int(android.env(ctx), p.obj, "getDuration");
  if (d < 0) {
    return 0i64;
  }
  return d as i64;
}

pub function playing(ctx: usize, p: Player): boolean {
  let e: usize = android.env(ctx);
  let none: usize = jni.jvalue_alloc(0);
  let got: i32 = jni.call_boolean_method_a(e, p.obj, jni.get_method_id(e, jni.get_object_class(e, p.obj), "isPlaying", "()Z"), none);
  jni.free_buffer(none);
  if (cleared(e)) {
    return false;
  }
  return got != 0;
}

// The width and the height of the video, or 0 and 0 for audio.
pub function video_size(ctx: usize, p: Player): (i32, i32) {
  let e: usize = android.env(ctx);
  return (call_int(e, p.obj, "getVideoWidth"), call_int(e, p.obj, "getVideoHeight"));
}

// Stops the stream and takes the video off the window. The player keeps its
// listeners, and `open` plays the next stream.
pub function stop(ctx: usize, p: Player): void {
  let e: usize = android.env(ctx);
  let surface = call_void(e, p.obj, "setSurface", "(Landroid/view/Surface;)V", [0 as usize]);
  let reset = call_void(e, p.obj, "reset", "()V", []);
}

// Stops the player and frees it and its listener.
pub function close(ctx: usize, p: Player): void {
  let e: usize = android.env(ctx);
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
