# Android media player

This module plays video and audio through `android.media.MediaPlayer` for a NativeActivity app.
Sources include paths, file URIs, HTTP streams, and granted content URIs.

## Model

`MediaPlayer` reads the stream, decodes it, keeps the audio and the video in time, and shows the video on a `Surface`. It reads a progressive file, such as MP4, Matroska or WebM, and HLS. The app declares a @jni class that implements the five listener interfaces of the player. Its native methods call `prepared`, `completed`, `failed`, `info` and `seeked`. `new` makes one player and one listener of that class.

The listener runs on the main thread of the app. Each callback sends an event to the live loop of the app with `android.tell`. The loop gives it to the app as a `Posted` event. `event` decodes it as a `Change`. Each player keeps its audio session as a sender of `std/android`, so its events hold its slot. Android gives each live player an audio session of its own. `sent` tells whether an event came from one player, for an app with more than one.

`set_next` gives a prepared player to a player that plays. When the first player completes, Android starts the next one with no gap, and `Completed` comes from the first player. Then the first player is free for the item after.

`open` starts to prepare a stream, and `Prepared` comes when the player can start. `show` puts the video on the window of the activity: it calls `Window.release_cpu` first, because a window has one producer of pixels at a time. `hide` takes the video off the window, so the app can lock the window and draw on it again. Do not lock the window between `show` and `hide`.

`Buffering(true)` and `Buffering(false)` come when the player waits for the network and when it plays again. `Rendering` comes when the first video frame shows. `AudioNotPlaying` and `VideoNotPlaying` come when one track stops while the other plays on, for example when no decoder takes the sound. `has_audio` tells whether the player selected a sound track at all: a track that the extractor cannot read is not selected.

`audio_tracks` gives the index of each sound track of the stream, in the order of the file, and `select_track` plays the track with an index. `Failed` has the `what` and `extra` codes of `MediaPlayer.OnErrorListener`.

`set_speed` sets the rate of the playback, with the pitch of the sound kept. A rate other than zero also starts a paused player, so set it while the player plays, and again after `start`.

`target` makes a `SurfaceTexture` of Android on an external texture of `std/android/gl`, and a `Surface` that feeds it. `show_target` puts the video on that surface in place of the window. The app then draws each frame with the GPU, for example with bars that keep the shape of the video.

`next_frame` takes the newest frame into the texture, and gives its transform matrix and its time. `held` is false until the texture holds a frame of the target. Call `target`, `next_frame` and `close_target` with the GL context of the texture current. `close_target` releases the two objects, after `hide` or `stop` takes the video off the surface.

The module needs API level 26, for `ANativeWindow_toSurface` and the long seek API.
Android selects the codecs and controls A/V synchronization.
This API does not select a named hardware or software codec.
Use `std/android/video` when the app needs explicit codec policies.

`timestamp` returns the platform media-time and monotonic-time anchor.
The media time uses microseconds. The monotonic time uses nanoseconds.
An unavailable or unknown anchor returns `None`.
The anchor does not include the clock rate or a physical output latency measurement.

Content URIs use the activity context and Android's content resolver.
This path accepts a URI from the system document picker or MediaStore.
The app must hold read permission for the URI when it opens the source.
A document picker can grant access to one item without broad storage permission.
The app must retain or persist that permission for later source opens.
A denied or unavailable source returns false or reports `Failed` through the platform.

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

The kinds from 19 to 23 belong to this module. The bits above the low 8 of the kind hold the slot of the player. A `Failed` event packs `what` in the high 32 bits of its argument and `extra` in the low 32 bits. An `Info` event has `what` as its argument.

`Cues` comes only from `std/android/exoplayer`, when the image subtitles change. Its `Info` code is 1000, which MediaPlayer does not use.

```fern
<<events>>=
pub enum Change { Prepared, Completed, Failed(i32, i32), Buffering(boolean), Rendering, Seeked, AudioNotPlaying, VideoNotPlaying, Cues }

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

// MediaPlayer.MEDIA_INFO_AUDIO_NOT_PLAYING and VIDEO_NOT_PLAYING.
const INFO_AUDIO_NOT_PLAYING: i32 = 804;

const INFO_VIDEO_NOT_PLAYING: i32 = 805;

const INFO_CUES: i32 = 1000;

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
  if (what == INFO_AUDIO_NOT_PLAYING) {
    return Some(AudioNotPlaying);
  }
  if (what == INFO_VIDEO_NOT_PLAYING) {
    return Some(VideoNotPlaying);
  }
  if (what == INFO_CUES) {
    return Some(Cues);
  }
  return None;
}

// The kind and the argument of the Posted event that carries the change
// `c` from the player in the slot `slot`. std/android/exoplayer sends its
// changes this way too, so `event` and `sent_by` read the events of both.
pub function posted(c: Change, slot: i32): (i32, usize) {
  let tag = slot_tag(slot);
  match (c) {
    Prepared => {
      return (PREPARED | tag, 0 as usize);
    },
    Completed => {
      return (COMPLETED | tag, 0 as usize);
    },
    Failed(what, extra) => {
      return (FAILED | tag, ((what as u64 & 4294967295u64) << 32u64 | extra as u64 & 4294967295u64) as usize);
    },
    Buffering(on) => {
      return (INFO | tag, buffering_info(on) as usize);
    },
    Rendering => {
      return (INFO | tag, INFO_RENDERING as usize);
    },
    Seeked => {
      return (SEEKED | tag, 0 as usize);
    },
    AudioNotPlaying => {
      return (INFO | tag, INFO_AUDIO_NOT_PLAYING as usize);
    },
    VideoNotPlaying => {
      return (INFO | tag, INFO_VIDEO_NOT_PLAYING as usize);
    },
    Cues => {
      return (INFO | tag, INFO_CUES as usize);
    }
  }
}

// The bits of the kind that hold the slot. A player with no slot sends
// with the slot 0.
function slot_tag(slot: i32): i32 {
  if (slot > 0) {
    return slot << 8;
  }
  return 0;
}

function buffering_info(on: boolean): i32 {
  if (on) {
    return INFO_BUFFERING_START;
  }
  return INFO_BUFFERING_END;
}

// Whether the Posted event `e` carries a change of the player in the slot
// `slot`.
pub function sent_by(e: android.Event, slot: i32): boolean {
  let android.Posted(tagged, arg) = e else {
    return false;
  };
  let kind = tagged & 255;
  return kind >= PREPARED && kind <= SEEKED && tagged >> 8 == slot;
}

// The change of the player that a Posted event carries, or None for an
// event of another module.
pub function event(e: android.Event): Option[Change] {
  let android.Posted(tagged, arg) = e else {
    return None;
  };
  let kind = tagged & 255;
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
`seek` uses the API 26 method with `SEEK_CLOSEST` or `SEEK_CLOSEST_SYNC`.
It rejects negative positions and positions above 2,147,483,647 milliseconds.
Android clamps larger values internally, despite the Java long parameter.

```fern
<<the player>>=
// The global references of the player and its listener, and the slot of
// its audio session in the senders of the loop, or -1.
pub struct Player { obj: usize, listener: usize, slot: i32 }

// Whether the player `p` sent the Posted event `e`.
pub function (p: Player) sent(e: android.Event): boolean {
  return sent_by(e, p.slot);
}

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
  return retain(ctx, e, mp, listener);
}

function set_attributes(e: usize, mp: usize): boolean {
  let attrs = attributes(e);
  return attrs != 0 as usize && call_void(e, mp, "setAudioAttributes", "(Landroid/media/AudioAttributes;)V", [attrs]);
}

function retain(ctx: usize, e: usize, mp: usize, listener: usize): Option[Player] {
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
  let slot = android.add_sender(ctx, session_of(e, mp));
  if (slot < 0) {
    let _ = jni.delete_global_ref(e, saved_listener);
    let _ = jni.delete_global_ref(e, obj);
    let _ = call_void(e, mp, "release", "()V", []);
    return None;
  }
  return Some(Player { obj: obj, listener: saved_listener, slot: slot });
}

function session_of(e: usize, mp: usize): i64 {
  let id = method(e, mp, "getAudioSessionId", "()I");
  if (id == 0 as usize) {
    return 0i64;
  }
  let session = jni.call_int_method_a(e, mp, id, 0 as usize);
  if (cleared(e)) {
    return 0i64;
  }
  return session as i64;
}

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

function parse_uri(e: usize, text: usize): usize {
  let class = find_class(e, "android/net/Uri");
  if (class == 0 as usize) {
    return 0 as usize;
  }
  let id = jni.get_static_method_id(e, class, "parse", "(Ljava/lang/String;)Landroid/net/Uri;");
  if (cleared(e) || id == 0 as usize) {
    return 0 as usize;
  }
  let values = jni.jvalues([text]);
  let uri = jni.call_static_object_method_a(e, class, id, values);
  jni.free_buffer(values);
  if (cleared(e)) {
    return 0 as usize;
  }
  return uri;
}

// Prepares a path, file URI, HTTP address, or granted content URI.
// Prepared or Failed follows. An immediate failure returns false.
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
  let parsed = parse_uri(e, text);
  if (parsed == 0 as usize) {
    return false;
  }
  return call_void(e, p.obj, "setDataSource", "(Landroid/content/Context;Landroid/net/Uri;)V", [android.activity_object(ctx), parsed]) && call_void(e, p.obj, "prepareAsync", "()V", []);
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

// A SurfaceTexture on an external GL texture and the Surface that feeds it,
// as global references.
pub struct Target { texture: usize, surface: usize }

// A target on the external texture with the id `texture_id`, or None.
pub function target(ctx: usize, texture_id: i32): Option[Target] {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return None;
  }
  defer end(e);
  let st = new_object(e, find_class(e, "android/graphics/SurfaceTexture"), "(I)V", [texture_id as usize]);
  if (st == 0 as usize) {
    return None;
  }
  let surface = new_object(e, find_class(e, "android/view/Surface"), "(Landroid/graphics/SurfaceTexture;)V", [st]);
  if (surface == 0 as usize) {
    let _ = call_void(e, st, "release", "()V", []);
    return None;
  }
  let saved_st = jni.new_global_ref(e, st);
  if (cleared(e) || saved_st == 0 as usize) {
    release_target(e, st, surface);
    return None;
  }
  let saved_surface = jni.new_global_ref(e, surface);
  if (cleared(e) || saved_surface == 0 as usize) {
    let _ = jni.delete_global_ref(e, saved_st);
    release_target(e, st, surface);
    return None;
  }
  return Some(Target { texture: saved_st, surface: saved_surface });
}

function release_target(e: usize, st: usize, surface: usize): void {
  let _ = call_void(e, surface, "release", "()V", []);
  let _ = call_void(e, st, "release", "()V", []);
}

// Shows the video on the target in place of the window.
pub function show_target(ctx: usize, p: Player, t: Target): boolean {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  return call_void(e, p.obj, "setSurface", "(Landroid/view/Surface;)V", [t.surface]);
}

// The newest frame of a target: the 16 numbers of its transform matrix,
// column by column, and its time in nanoseconds.
pub struct TargetFrame { matrix: f32[], time_ns: i64 }

// True when the texture holds a frame of the target. Until then the matrix
// is the identity, and the texture can still hold a frame of a target
// before: the transform of each real frame turns the rows over.
pub function (f: TargetFrame) held(): boolean {
  if (f.matrix.len() != 16) {
    return false;
  }
  for i in 0..16 {
    let want: f32 = 0.0;
    if (i % 5 == 0) {
      want = 1.0;
    }
    if (f.matrix[i] != want) {
      return true;
    }
  }
  return false;
}

// Takes the newest frame of the video into the texture of `t`.
pub function next_frame(ctx: usize, t: Target): Option[TargetFrame] {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return None;
  }
  defer end(e);
  if (!call_void(e, t.texture, "updateTexImage", "()V", [])) {
    return None;
  }
  let array = jni.new_float_array(e, 16);
  if (cleared(e) || array == 0 as usize) {
    return None;
  }
  if (!call_void(e, t.texture, "getTransformMatrix", "([F)V", [array])) {
    return None;
  }
  let matrix = jni.float_array_region(e, array, 0, 16);
  if (cleared(e)) {
    return None;
  }
  let Some(time) = call_long(e, t.texture, "getTimestamp") else {
    return None;
  };
  return Some(TargetFrame { matrix: matrix, time_ns: time });
}

// Releases the Surface and the SurfaceTexture of `t`.
pub function close_target(ctx: usize, t: Target): void {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return;
  }
  defer end(e);
  release_target(e, t.texture, t.surface);
  let _ = jni.delete_global_ref(e, t.surface);
  let _ = jni.delete_global_ref(e, t.texture);
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

// Where a seek lands. Exact decodes from the sync frame before the time up
// to the time. Sync lands on the sync frame nearest to the time, so it is
// fast on a long stream.
pub enum SeekMode { Exact, Sync }

// Moves to `ms` milliseconds from the start. Seeked follows.
pub function seek(ctx: usize, p: Player, ms: i64, mode: SeekMode): boolean {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  if (ms < 0i64 || ms > 2147483647i64) {
    return false;
  }
  // MediaPlayer.SEEK_CLOSEST is 3, and SEEK_CLOSEST_SYNC is 2.
  let code = match (mode) {
    Exact => 3,
    Sync => 2,
  };
  return call_void(e, p.obj, "seekTo", "(JI)V", [ms as usize, code as usize]);
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

// MediaPlayer.TrackInfo.MEDIA_TRACK_TYPE_AUDIO.
const TRACK_AUDIO: i32 = 2;

// True when the prepared player selected a sound track.
pub function has_audio(ctx: usize, p: Player): boolean {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  let id = method(e, p.obj, "getSelectedTrack", "(I)I");
  if (id == 0 as usize) {
    return false;
  }
  let values = jni.jvalues([TRACK_AUDIO as usize]);
  let track = jni.call_int_method_a(e, p.obj, id, values);
  jni.free_buffer(values);
  return !cleared(e) && track >= 0;
}

// The index of each sound track of the prepared player, in the order of
// the file.
pub function audio_tracks(ctx: usize, p: Player): i32[] {
  let e: usize = android.env(ctx);
  let out: i32[] = [];
  if (!begin(e)) {
    return out;
  }
  defer end(e);
  let infos = call_object(e, p.obj, "getTrackInfo", "()[Landroid/media/MediaPlayer$TrackInfo;", []);
  if (infos == 0 as usize) {
    return out;
  }
  let n = jni.get_array_length(e, infos);
  if (cleared(e)) {
    return out;
  }
  for i in 0..n {
    let info = jni.get_object_array_element(e, infos, i);
    if (cleared(e) || info == 0 as usize) {
      return out;
    }
    if (call_int(e, info, "getTrackType") == TRACK_AUDIO) {
      out = out.append(i);
    }
    let _ = jni.delete_local_ref(e, info);
  }
  return out;
}

// Plays the track with the index `index`.
pub function select_track(ctx: usize, p: Player, index: i32): boolean {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  return call_void(e, p.obj, "selectTrack", "(I)V", [index as usize]);
}

// Plays at `rate` times the normal speed, from 0.25 to 4.0, with
// PlaybackParams. False for a rate out of that range.
pub function set_speed(ctx: usize, p: Player, rate: f32): boolean {
  if (!(rate >= 0.25f32 && rate <= 4.0f32)) {
    return false;
  }
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  let params = new_object(e, find_class(e, "android/media/PlaybackParams"), "()V", []);
  let id = method(e, params, "setSpeed", "(F)Landroid/media/PlaybackParams;");
  if (id == 0 as usize) {
    return false;
  }
  let args = jni.jvalue_set_f32(jni.jvalue_alloc(1), 0, rate);
  let sped = jni.call_object_method_a(e, params, id, args);
  jni.free_buffer(args);
  if (cleared(e) || sped == 0 as usize) {
    return false;
  }
  return call_void(e, p.obj, "setPlaybackParams", "(Landroid/media/PlaybackParams;)V", [sped]);
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

// Gives the player `next`, which must be prepared, to the player `p`, so
// it starts with no gap when `p` completes. None takes it away.
pub function set_next(ctx: usize, p: Player, next: Option[Player]): boolean {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  let obj = 0 as usize;
  if let Some(n) = next {
    obj = n.obj;
  }
  return call_void(e, p.obj, "setNextMediaPlayer", "(Landroid/media/MediaPlayer;)V", [obj]);
}

// Stops the player and frees it and its listener.
pub function close(ctx: usize, p: Player): void {
  let e: usize = android.env(ctx);
  if (!begin(e)) {
    return;
  }
  defer end(e);
  let released = call_void(e, p.obj, "release", "()V", []);
  android.drop_sender(ctx, p.slot);
  jni.delete_global_ref(e, p.listener);
  jni.delete_global_ref(e, p.obj);
}
```

### `the listener`

Each native method of the app's listener calls one of these with its `JNIEnv` and its arguments. The listener of the error returns true, so no Completed follows a Failed. The listener of the info returns false, so the player does its own handling too.

```fern
<<the listener>>=
function post(e: usize, mp: usize, c: Change): void {
  if (!begin(e)) {
    return;
  }
  defer end(e);
  let (kind, arg) = posted(c, android.sender_slot(e, session_of(e, mp)));
  let sent = android.tell(e, kind, arg);
}

pub function prepared(e: usize, listener: usize, mp: usize): void {
  post(e, mp, Prepared);
}

pub function completed(e: usize, listener: usize, mp: usize): void {
  post(e, mp, Completed);
}

pub function failed(e: usize, listener: usize, mp: usize, what: i32, extra: i32): u8 {
  post(e, mp, Failed(what, extra));
  return 1u8;
}

pub function info(e: usize, listener: usize, mp: usize, what: i32, extra: i32): u8 {
  if let Some(c) = info_change(what) {
    post(e, mp, c);
  }
  return 0u8;
}

pub function seeked(e: usize, listener: usize, mp: usize): void {
  post(e, mp, Seeked);
}
```
