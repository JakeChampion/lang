# Android ExoPlayer

This module plays video and audio through Media3 ExoPlayer for a NativeActivity app. The APK must hold the classes of Media3: give the DEX files of `tools/media3/fetch` to `-apk-dex`.

## Model

ExoPlayer reads progressive files, such as MP4 and Matroska, and HLS. It plays each sound track of a file, and it changes the sound track during the playback with no new start. `std/android/player` uses `MediaPlayer`, which plays the sound track that the file marks as the default, and changes it only on some files.

An ExoPlayer takes each call on the thread of its `Looper`, and it calls its listener on that thread. This module gives it the main `Looper` of the app. The loop thread does not call the ExoPlayer: each function of the loop thread sends a command to the main thread through `std/android/handler`. The main thread runs the commands in their order.

The app declares a @jni class that extends `java.util.concurrent.atomic.AtomicReference` and implements `android.os.Handler$Callback` and `androidx.media3.common.Player$Listener`. Its native `handleMessage` calls `handle`, and its native `onEvents` calls `events`. `new` makes one object of that class for each player. The object holds the state of its player, so `handle` and `events` find it with no table.

The state is an `AtomicLongArray`, which both threads read and write. The main thread writes the position, the length, the size of the video and the tracks into it after each command and each event. The loop thread reads them from it, with no command. `position_ms` adds the time since the main thread wrote the position, at the speed of the playback, while the player plays.

The events of the player are `Change` values of `std/android/player`. `player.event` decodes them, and `sent` tells whether an event came from one player.

`follow` gives the changes of one event of ExoPlayer from the phase of the media item. `Prepared` comes when the item can first play. `Buffering(true)` and `Buffering(false)` come while it waits for data. `Completed` comes at its end, and `Failed` has the error code of ExoPlayer. `Seeked` comes after each `seek`, and `Rendering` comes when the first video frame shows. `VideoNotPlaying` follows `Prepared` when the stream has video and no decoder plays it.

`select_track` plays the sound track with an index among the sound tracks of the stream, in the order of the file. A selection before the tracks are known waits for them.

`select_text` shows the subtitle track with an index among the subtitle tracks of the file, in their order, or no track with -1. A new item shows no track. ExoPlayer decodes the track.

The native `onCues` of the listener calls `cues`, which keeps the bitmaps of the cues and sends `Cues`. Then `image_cues` gives them to the loop thread as RGBA images, each with its box on the video. Image subtitles such as PGS, VobSub and DVB give bitmaps. A text cue gives no image, because the app draws text subtitles itself.

`chain` gives a player the address of the item after its item. ExoPlayer plays the two with no gap. When the second one starts, `Completed` comes, and the player plays the second item.

`show_target` puts the video on a `Target` of `std/android/player`. `close_target` releases a target on the main thread, after the commands that came before it, so the player does not draw on a released surface.

`passthrough_types` gives the MIME types of the compressed sound that the sound output takes as it is. An example is Dolby Digital to an HDMI soundbar. ExoPlayer sends such a track to the output with no decoder, so an app can play it with no transcode. The list comes from `AudioCapabilities` of Media3, and it changes when the output changes.

`match_frame_rate` asks the display to show the window at the frame rate of the video. Then a TV can change to 24 Hz for a film. Android 12 and later take the request, and the settings of the device can refuse it. A rate of 0 takes the request away.

## Contracts

Call each function except `handle` and `events` on the loop thread. Call `close` one time for each player before Destroy.

`show` puts the video on the window. A window can go away while a command waits on the main thread. So a GPU app shows the video on a target of its own.

Each command can fail on the main thread with no answer to the loop thread. Then the player logs the error, and the state does not change.

`passthrough_types` and `match_frame_rate` can run on each thread. Call `cues` only from the native `onCues(androidx.media3.common.text.CueGroup)` of the listener.

`open` refuses an empty address, a NUL byte, and an address longer than 8192 bytes. `seek` refuses a negative position and a position above 2,147,483,647 milliseconds.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<constants>>

<<the rules of the events>>

<<the player>>

<<the commands>>

<<the output>>

<<the cues>>

<<the main thread>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module plays media with Media3 ExoPlayer on the main thread of the
// app.
import "std/android";
import "std/android/draw";
import "std/android/exoplayer/bindings";
import "std/android/handler";
import "std/android/player";
import "std/jni";
import "std/log";
import "std/option";
```

### `constants`

The commands are the `what` of a message. The state has one long for each `S_` index. The `STATE_`, `EVENT_` and `TRACK_` values are those of `androidx.media3.common.Player` and `C`.

```fern
<<constants>>=
const CREATE: i32 = 1;

const OPEN: i32 = 2;

const START: i32 = 3;

const PAUSE: i32 = 4;

const SEEK: i32 = 5;

const SPEED: i32 = 6;

const STOP: i32 = 7;

const SURFACE: i32 = 8;

const AUDIO: i32 = 9;

const CHAIN: i32 = 10;

const DROP_TARGET: i32 = 11;

const RELEASE: i32 = 12;

const TEXT: i32 = 13;

const S_POSITION: i32 = 0;

const S_AT: i32 = 1;

const S_DURATION: i32 = 2;

const S_PLAYING: i32 = 3;

const S_SPEED: i32 = 4;

const S_WIDTH: i32 = 5;

const S_HEIGHT: i32 = 6;

const S_AUDIO: i32 = 7;

const S_AUDIO_SELECTED: i32 = 8;

// One more than the index of the sound track to select, or 0.
const S_WANTED: i32 = 9;

const S_SLOT: i32 = 10;

const S_PHASE: i32 = 11;

const S_BUFFERING: i32 = 12;

// 1 while the player has the item that `chain` gave it after its item.
const S_CHAINED: i32 = 13;

// One more than the index of the subtitle track to show, or 0.
const S_TEXT: i32 = 14;

const LONGS: i32 = 15;

pub const IDLE: i32 = 0;

pub const OPENING: i32 = 1;

pub const READY: i32 = 2;

pub const ENDED: i32 = 3;

pub const FAILED: i32 = 4;

pub const STATE_BUFFERING: i32 = 2;

pub const STATE_READY: i32 = 3;

pub const STATE_ENDED: i32 = 4;

const EVENT_TRANSITION: i32 = 1;

const EVENT_TRACKS: i32 = 2;

const EVENT_ERROR: i32 = 10;

const EVENT_FIRST_FRAME: i32 = 26;

const TRACK_AUDIO: i32 = 1;

const TRACK_VIDEO: i32 = 2;

const TRACK_TEXT: i32 = 3;

// C.USAGE_MEDIA and C.AUDIO_CONTENT_TYPE_MOVIE.
const USAGE_MEDIA: i32 = 1;

const CONTENT_MOVIE: i32 = 3;

const MAX_URI: i32 = 8192;

const MAX_SEEK: i64 = 2147483647i64;
```

### `the rules of the events`

`follow` is a function of values only, so a test on the host checks each rule. The phase is one of `IDLE`, `OPENING`, `READY`, `ENDED` and `FAILED`. An item that ends before it was ready still gives `Prepared` first.

```fern
<<the rules of the events>>=
// The phase of the media item of a player. `buffering` is true while a
// READY item waits for data.
pub struct Phase { stage: i32, buffering: boolean }

// What the main thread reads of the player in one event. `state` is a
// STATE_ value. `failed` is true for a new error with the code `error`.
// `moved_on` is true when the player went on to the item that `chain` gave
// it. `video` is the number of video tracks, and `video_played` the number
// that a decoder plays.
pub struct Snapshot { state: i32, failed: boolean, error: i32, moved_on: boolean, first_frame: boolean, video: i32, video_played: i32 }

// The phase after the event `s`, and the changes that the app hears.
pub function follow(was: Phase, s: Snapshot): (Phase, player.Change[]) {
  if (s.failed) {
    return (Phase { stage: FAILED, buffering: false }, [player.Failed(s.error, 0)]);
  }
  if (s.moved_on) {
    return (Phase { stage: READY, buffering: false }, [player.Completed]);
  }
  let (opened, out) = opening(was, s);
  let (now, change) = ready(opened, s.state);
  if let Some(c) = change {
    out = out.append(c);
  }
  if (s.first_frame) {
    out = out.append(player.Rendering);
  }
  return (now, out);
}

// An opening item that can play, or that ended, is prepared.
function opening(was: Phase, s: Snapshot): (Phase, player.Change[]) {
  if (was.stage != OPENING || s.state != STATE_READY && s.state != STATE_ENDED) {
    return (was, []);
  }
  let now = Phase { stage: READY, buffering: false };
  if (s.video > 0 && s.video_played == 0) {
    return (now, [player.Prepared, player.VideoNotPlaying]);
  }
  return (now, [player.Prepared]);
}

// A ready item ends, waits for data, or plays again after a wait.
function ready(now: Phase, state: i32): (Phase, Option[player.Change]) {
  if (now.stage != READY) {
    return (now, None);
  }
  if (state == STATE_ENDED) {
    return (Phase { stage: ENDED, buffering: false }, Some(player.Completed));
  }
  if (state == STATE_BUFFERING && !now.buffering) {
    return (Phase { stage: READY, buffering: true }, Some(player.Buffering(true)));
  }
  if (state == STATE_READY && now.buffering) {
    return (Phase { stage: READY, buffering: false }, Some(player.Buffering(false)));
  }
  return (now, None);
}

// The position at the monotonic time `now_ms`, from the longs of a state:
// the position that the main thread wrote, moved on at the speed of the
// playback while the player plays, and no further than the end.
pub function position_at(s: i64[], now_ms: i64): i64 {
  if (s.len() < LONGS) {
    return 0i64;
  }
  let at = s[S_POSITION];
  if (s[S_PLAYING] != 0i64 && now_ms > s[S_AT]) {
    at += (now_ms - s[S_AT]) * s[S_SPEED] / 1000i64;
  }
  if (s[S_DURATION] > 0i64 && at > s[S_DURATION]) {
    at = s[S_DURATION];
  }
  if (at < 0i64) {
    return 0i64;
  }
  return at;
}
```

### `the player`

The struct holds three global references: the listener object, the `Handler` and the state. `slot` is the slot of the player in the senders of the loop. `none` is a player with no object, on which each function does nothing.

```fern
<<the player>>=
pub struct Player { listener: usize, handler: handler.Handler, state: usize, slot: i32 }

pub function none(): Player {
  return Player { listener: 0 as usize, handler: handler.Handler { ref: 0 as usize }, state: 0 as usize, slot: -1 };
}

pub function (p: Player) live(): boolean {
  return p.handler.live();
}

// Whether the player `p` sent the Posted event `e`.
pub function (p: Player) sent(e: android.Event): boolean {
  return p.live() && player.sent_by(e, p.slot);
}

function begin(e: usize): boolean {
  if (e == 0 as usize) {
    return false;
  }
  let pending = jni.exception_occurred(e);
  if (pending != 0 as usize) {
    jni.delete_local_ref(e, pending);
    return false;
  }
  if (jni.call1(e, 19, 64 as usize) as i32 != 0) {
    jni.exception_clear(e);
    return false;
  }
  return true;
}

function end(e: usize): void {
  jni.call1(e, 20, 0 as usize);
}

// A player for the app's @jni class `class`, or None. The main thread makes
// its ExoPlayer with the activity as its context.
pub function new(ctx: usize, class: string): Option[Player] {
  let e = android.env(ctx);
  if (!begin(e)) {
    return None;
  }
  defer end(e);
  match (made(ctx, e, class)) {
    Ok(p) => {
      return Some(p);
    },
    Err(err) => {
      log.warn("exoplayer: no player: " + err.message());
      return None;
    }
  }
}

function made(ctx: usize, e: usize, class: string): Result[Player, jni.JavaError] {
  let listener = new_listener(ctx, e, class)?;
  let state = new_state(e, listener)?;
  let commands = handler.new(e, listener)?;
  let p = Player { listener: jni.new_global_ref(e, listener), handler: commands, state: jni.new_global_ref(e, state), slot: -1 };
  let slot = android.add_sender(ctx, p.listener as i64);
  if (slot < 0) {
    forget(e, p);
    return Err(jni.JavaError { text: "the loop has no free slot for a player" });
  }
  let out = Player { ...p, slot: slot };
  if (!put_slot(e, state, slot) || !send(ctx, out, CREATE, 0, android.activity_object(ctx))) {
    android.drop_sender(ctx, slot);
    forget(e, out);
    return Err(jni.JavaError { text: "the main thread did not take the player" });
  }
  return Ok(out);
}

// An object of the app's listener class `class`.
function new_listener(ctx: usize, e: usize, class: string): Result[usize, jni.JavaError] {
  let kind = jni.load_class(e, android.activity_object(ctx), class);
  jni.check(e)?;
  let ctor = jni.get_method_id(e, kind, "<init>", "()V");
  jni.check(e)?;
  let none_args = jni.jvalue_alloc(0);
  let listener = jni.new_object_a(e, kind, ctor, none_args);
  jni.free_buffer(none_args);
  jni.check(e)?;
  return Ok(listener);
}

// The state of a new player, in the array that the listener holds.
function new_state(e: usize, listener: usize): Result[usize, jni.JavaError] {
  let longs = bindings.atomic_long_array_class(e)?;
  defer longs.release(e);
  let state = longs.new(e, LONGS)?;
  longs.set(e, state, S_SPEED, 1000i64)?;
  let object = jni.find_class(e, "java/lang/Object");
  jni.check(e)?;
  let held = jni.new_object_array(e, 3, object, 0 as usize);
  jni.check(e)?;
  jni.set_object_array_element(e, held, 0, state.ref);
  jni.check(e)?;
  let refs = bindings.atomic_reference_class(e)?;
  defer refs.release(e);
  refs.set(e, bindings.AtomicReference { ref: listener }, jni.Object { ref: held })?;
  return Ok(state.ref);
}

function put_slot(e: usize, state: usize, slot: i32): boolean {
  let Ok(longs) = bindings.atomic_long_array_class(e) else {
    return false;
  };
  defer longs.release(e);
  return longs.set(e, bindings.AtomicLongArray { ref: state }, S_SLOT, slot as i64).is_ok();
}

function forget(e: usize, p: Player): void {
  jni.delete_global_ref(e, p.state);
  p.handler.close(e);
  jni.delete_global_ref(e, p.listener);
}

// Releases the ExoPlayer on the main thread, and the references of the
// loop thread.
pub function close(ctx: usize, p: Player): void {
  if (!p.live()) {
    return;
  }
  let sent = send(ctx, p, RELEASE, 0, 0 as usize);
  android.drop_sender(ctx, p.slot);
  forget(android.env(ctx), p);
}

// The longs of the state of `p`, or None.
function longs(ctx: usize, p: Player): Option[i64[]] {
  if (!p.live()) {
    return None;
  }
  let e = android.env(ctx);
  if (!begin(e)) {
    return None;
  }
  defer end(e);
  let Ok(c) = bindings.atomic_long_array_class(e) else {
    return None;
  };
  defer c.release(e);
  let a = bindings.AtomicLongArray { ref: p.state };
  let out: i64[] = [];
  for k in 0..LONGS {
    let Ok(v) = c.get(e, a, k) else {
      return None;
    };
    out = out.append(v);
  }
  return Some(out);
}

pub function position_ms(ctx: usize, p: Player): i64 {
  let Some(s) = longs(ctx, p) else {
    return 0i64;
  };
  return position_at(s, monotonic_ns() / 1000000i64);
}

pub function duration_ms(ctx: usize, p: Player): i64 {
  let Some(s) = longs(ctx, p) else {
    return 0i64;
  };
  return s[S_DURATION];
}

pub function playing(ctx: usize, p: Player): boolean {
  let Some(s) = longs(ctx, p) else {
    return false;
  };
  return s[S_PLAYING] != 0i64;
}

// The width and the height of the video, or (0, 0).
pub function video_size(ctx: usize, p: Player): (i32, i32) {
  let Some(s) = longs(ctx, p) else {
    return (0, 0);
  };
  return (s[S_WIDTH] as i32, s[S_HEIGHT] as i32);
}

// Whether the player selected a sound track.
pub function has_audio(ctx: usize, p: Player): boolean {
  let Some(s) = longs(ctx, p) else {
    return false;
  };
  return s[S_AUDIO_SELECTED] != 0i64;
}

// The index of each sound track of the stream, in the order of the file.
pub function audio_tracks(ctx: usize, p: Player): i32[] {
  let out: i32[] = [];
  let Some(s) = longs(ctx, p) else {
    return out;
  };
  for k in 0..s[S_AUDIO] as i32 {
    out = out.append(k);
  }
  return out;
}
```

### `the commands`

`send` gives the command, one number and one object to the `Handler` of the player. The message holds the object, so the loop thread can delete its own reference after the call.

```fern
<<the commands>>=
function send(ctx: usize, p: Player, what: i32, arg: i32, obj: usize): boolean {
  if (!p.live()) {
    return false;
  }
  let e = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  return p.handler.send(e, what, arg, obj);
}

// Sends a command whose object is the Java string `text`.
function send_text(ctx: usize, p: Player, what: i32, text: string): boolean {
  let e = android.env(ctx);
  if (!begin(e)) {
    return false;
  }
  defer end(e);
  let s = jni.new_string(e, text);
  if (jni.check(e).is_err() || s == 0 as usize) {
    return false;
  }
  return send(ctx, p, what, 0, s);
}

function valid_uri(uri: string): boolean {
  if (uri.len() == 0 || uri.len() > MAX_URI) {
    return false;
  }
  for byte in uri.as_bytes() {
    if (byte == 0u8) {
      return false;
    }
  }
  return true;
}

// Prepares a path, file URI or HTTP address. Prepared or Failed follows.
pub function open(ctx: usize, p: Player, uri: string): boolean {
  return valid_uri(uri) && send_text(ctx, p, OPEN, uri);
}

pub function start(ctx: usize, p: Player): boolean {
  return send(ctx, p, START, 0, 0 as usize);
}

pub function pause(ctx: usize, p: Player): boolean {
  return send(ctx, p, PAUSE, 0, 0 as usize);
}

// Goes to the position `ms`. Seeked follows.
pub function seek(ctx: usize, p: Player, ms: i64): boolean {
  return ms >= 0i64 && ms <= MAX_SEEK && send(ctx, p, SEEK, ms as i32, 0 as usize);
}

// Sets the rate of the playback, with the pitch of the sound kept.
pub function set_speed(ctx: usize, p: Player, rate: f32): boolean {
  if (!(rate > 0.0f32)) {
    return false;
  }
  return send(ctx, p, SPEED, (rate * 1000.0f32) as i32, 0 as usize);
}

// Stops the playback, and takes the media item away.
pub function stop(ctx: usize, p: Player): void {
  let sent = send(ctx, p, STOP, 0, 0 as usize);
}

// Plays the sound track with the index `index` among the sound tracks.
pub function select_track(ctx: usize, p: Player, index: i32): boolean {
  let Some(s) = longs(ctx, p) else {
    return false;
  };
  return index >= 0 && index as i64 < s[S_AUDIO] && send(ctx, p, AUDIO, index, 0 as usize);
}

// Shows the subtitle track with the index `index` among the subtitle
// tracks, or no track with -1.
pub function select_text(ctx: usize, p: Player, index: i32): boolean {
  return index >= -1 && send(ctx, p, TEXT, index, 0 as usize);
}

// Gives the player the address `next` of the item after its item, or takes
// that item away when `next` is empty.
pub function chain(ctx: usize, p: Player, next: string): boolean {
  if (next.len() == 0) {
    return send(ctx, p, CHAIN, 0, 0 as usize);
  }
  return valid_uri(next) && send_text(ctx, p, CHAIN, next);
}

// Shows the video on window `w`. It gives the window up as the target of
// lock first.
pub function show(ctx: usize, p: Player, w: android.Window): boolean {
  let e = android.env(ctx);
  if (!p.live() || !begin(e)) {
    return false;
  }
  defer end(e);
  let released = w.release_cpu();
  let surface = w.surface(e);
  if (jni.check(e).is_err() || surface == 0 as usize) {
    return false;
  }
  return send(ctx, p, SURFACE, 0, surface);
}

// Shows the video on the target in place of the window.
pub function show_target(ctx: usize, p: Player, t: player.Target): boolean {
  return send(ctx, p, SURFACE, 0, t.surface);
}

// Takes the video off its surface.
pub function hide(ctx: usize, p: Player): void {
  let sent = send(ctx, p, SURFACE, 0, 0 as usize);
}

// Releases the Surface and the SurfaceTexture of `t` on the main thread,
// after the commands before it.
pub function close_target(ctx: usize, p: Player, t: player.Target): void {
  let e = android.env(ctx);
  if (!p.live() || !begin(e)) {
    player.close_target(ctx, t);
    return;
  }
  defer end(e);
  let object = jni.find_class(e, "java/lang/Object");
  let pair = jni.new_object_array(e, 2, object, 0 as usize);
  if (jni.check(e).is_ok() && pair != 0 as usize) {
    jni.set_object_array_element(e, pair, 0, t.surface);
    jni.set_object_array_element(e, pair, 1, t.texture);
    if (jni.check(e).is_ok() && send(ctx, p, DROP_TARGET, 0, pair)) {
      jni.delete_global_ref(e, t.surface);
      jni.delete_global_ref(e, t.texture);
      return;
    }
  }
  player.close_target(ctx, t);
}
```

### `the output`

The encodings are those of `android.media.AudioFormat`. The frame rate goes to the `Surface` of the window with `FRAME_RATE_COMPATIBILITY_FIXED_SOURCE` and `CHANGE_FRAME_RATE_ALWAYS`, because the video fills the window.

```fern
<<the output>>=
// Each encoding of android.media.AudioFormat that an output can take as it
// is, with its MIME type.
function passthrough_encodings(): (i32, string)[] {
  return [(5, "audio/ac3"), (6, "audio/eac3"), (18, "audio/eac3-joc"), (7, "audio/vnd.dts"), (8, "audio/vnd.dts.hd"), (14, "audio/true-hd")];
}

// The MIME types of the sound that the output of the device takes as it is.
pub function passthrough_types(ctx: usize): string[] {
  let out: string[] = [];
  let e = android.env(ctx);
  if (!begin(e)) {
    return out;
  }
  defer end(e);
  let Ok(c) = bindings.audio_capabilities_class(e) else {
    return out;
  };
  defer c.release(e);
  let Ok(caps) = c.get_capabilities(e, bindings.Context { ref: android.activity_object(ctx) }) else {
    return out;
  };
  for (code, mime) in passthrough_encodings() {
    if let Ok(yes) = c.supports_encoding(e, caps, code) {
      if (yes) {
        out = out.append(mime);
      }
    }
  }
  return out;
}

const FIXED_SOURCE: i32 = 1;

const CHANGE_ALWAYS: i32 = 1;

// Asks the display for the frame rate `fps` of the video on window `w`, or
// takes the request away with 0. False when the device did not take it.
pub function match_frame_rate(ctx: usize, w: android.Window, fps: f32): boolean {
  let e = android.env(ctx);
  if (!(fps >= 0.0f32) || !begin(e)) {
    return false;
  }
  defer end(e);
  let surface = w.surface(e);
  if (jni.check(e).is_err() || surface == 0 as usize) {
    return false;
  }
  let Ok(c) = bindings.surface_class(e) else {
    return false;
  };
  defer c.release(e);
  return c.has_set_frame_rate() && c.set_frame_rate(e, bindings.Surface { ref: surface }, fps, FIXED_SOURCE, CHANGE_ALWAYS).is_ok();
}
```

### `the cues`

`cues` runs on the main thread. It keeps at most `MAX_CUES` bitmaps, each with no more than `MAX_PIXELS` pixels, in the third place of the array of the listener: an int[] with six values for each bitmap, then the int[] of the pixels of each bitmap. The six values are the width and the height in pixels, then the position, the line, the size and the height of the bitmap. These four are fractions of the video in millionths, or -1 when the cue does not set them.

`image_cues` reads them on the loop thread. A cue with no box shows at the bottom of the video in the middle.

```fern
<<the cues>>=
const MAX_CUES: i32 = 4;

const MAX_PIXELS: i32 = 4194304;

// A subtitle image and its box on the video, as fractions of its width
// and height.
pub struct ImageCue { image: draw.Image, x: f32, y: f32, width: f32, height: f32 }

// A fraction of the video in millionths, or -1 for Cue.DIMEN_UNSET.
function millionths(v: f32): i32 {
  if (!(v >= 0.0f32 && v <= 1.0f32)) {
    return -1;
  }
  return (v * 1000000.0f32) as i32;
}

// The native onCues of the listener: keeps the bitmaps of the cues of
// `group` and sends Cues.
pub function cues(e: usize, listener: usize, group: usize): void {
  if (!begin(e)) {
    return;
  }
  defer end(e);
  match (bindings.classes(e)) {
    Ok(c) => {
      if let Err(err) = keep_cues(e, c, listener, bindings.CueGroup { ref: group }) {
        log.warn("exoplayer: the cues failed: " + err.message());
      }
      c.release(e);
    },
    Err(err) => {
      log.warn("exoplayer: the APK has no Media3 classes: " + err.message());
    }
  }
}

// The int[] of the pixels of the bitmap of `cue`, and its six values, or
// None for a cue with no bitmap or with a bitmap that is too large.
function picture_of(e: usize, c: bindings.Classes, cue: bindings.Cue): Result[Option[(usize, i32[])], jni.JavaError] {
  let bitmap = c.cue.bitmap(e, cue)?;
  if (bitmap.is_null()) {
    return Ok(None);
  }
  let w = c.bitmap.get_width(e, bitmap)?;
  let h = c.bitmap.get_height(e, bitmap)?;
  if (w <= 0 || h <= 0 || w > MAX_PIXELS / h) {
    return Ok(None);
  }
  let pixels = jni.new_int_array(e, w * h);
  jni.check(e)?;
  c.bitmap.get_pixels(e, bitmap, jni.Object { ref: pixels }, 0, w, 0, 0, w, h)?;
  return Ok(Some((pixels, [w, h].concat(place_of(e, c, cue)?))));
}

// The position, the line, the size and the height of the bitmap of `cue`,
// in millionths.
function place_of(e: usize, c: bindings.Classes, cue: bindings.Cue): Result[i32[], jni.JavaError] {
  return Ok([millionths(c.cue.position(e, cue)?), millionths(c.cue.line(e, cue)?), millionths(c.cue.size(e, cue)?), millionths(c.cue.bitmap_height(e, cue)?)]);
}

function keep_cues(e: usize, c: bindings.Classes, listener: usize, group: bindings.CueGroup): Result[void, jni.JavaError] {
  let list = bindings.List { ref: c.cue_group.cues(e, group)?.ref };
  let meta: i32[] = [];
  let pictures: usize[] = [];
  for i in 0..c.list.size(e, list)? {
    if (pictures.len() < MAX_CUES) {
      if let Some((pixels, values)) = picture_of(e, c, bindings.Cue { ref: c.list.get(e, list, i)?.ref })? {
        meta = meta.concat(values);
        pictures = pictures.append(pixels);
      }
    }
  }
  return hold_cues(e, c, listener, meta, pictures);
}

// Puts the values and the pixels of the cues in the array of the listener,
// and sends Cues.
function hold_cues(e: usize, c: bindings.Classes, listener: usize, meta: i32[], pictures: usize[]): Result[void, jni.JavaError] {
  let object = jni.find_class(e, "java/lang/Object");
  jni.check(e)?;
  let held = jni.new_object_array(e, pictures.len() + 1, object, 0 as usize);
  jni.check(e)?;
  let numbers = jni.new_int_array(e, meta.len());
  jni.check(e)?;
  jni.set_int_array_region(e, numbers, 0, meta);
  jni.set_object_array_element(e, held, 0, numbers);
  for k in 0..pictures.len() {
    jni.set_object_array_element(e, held, k + 1, pictures[k]);
  }
  let b = box_of(e, c, listener)?;
  jni.set_object_array_element(e, b.array, 2, held);
  jni.check(e)?;
  post(e, get(e, c, b.state, S_SLOT)? as i32, player.Cues);
  return Ok(());
}

// The box of a cue as fractions of the video: the values that it sets,
// and otherwise the middle of the bottom.
function box_of_cue(w: i32, h: i32, position: i32, line: i32, size: i32, tall: i32): (f32, f32, f32, f32) {
  let width = if (size >= 0) { size as f32 / 1000000.0f32 } else { 0.6f32 };
  let height = if (tall >= 0) { tall as f32 / 1000000.0f32 } else { width * (h as f32) / (w as f32) };
  let x = if (position >= 0) { position as f32 / 1000000.0f32 } else { (1.0f32 - width) / 2.0f32 };
  let y = if (line >= 0) { line as f32 / 1000000.0f32 } else { 0.95f32 - height };
  return (x, y, width, height);
}

// RGBA bytes of the ARGB pixels that Bitmap.getPixels gives.
function rgba(argb: i32[]): u8[] {
  let out: u8[] = [];
  for v in argb {
    out = out.append((v >> 16 & 255) as u8).append((v >> 8 & 255) as u8).append((v & 255) as u8).append((v >> 24 & 255) as u8);
  }
  return out;
}

// The image subtitles that show now, on the loop thread.
pub function image_cues(ctx: usize, p: Player): ImageCue[] {
  let out: ImageCue[] = [];
  let e = android.env(ctx);
  if (!p.live() || !begin(e)) {
    return out;
  }
  defer end(e);
  let Ok(refs) = bindings.atomic_reference_class(e) else {
    return out;
  };
  defer refs.release(e);
  let Ok(held) = refs.get(e, bindings.AtomicReference { ref: p.listener }) else {
    return out;
  };
  let kept = jni.get_object_array_element(e, held.ref, 2);
  if (jni.check(e).is_err() || kept == 0 as usize) {
    return out;
  }
  let numbers = jni.get_object_array_element(e, kept, 0);
  let meta = jni.int_array_region(e, numbers, 0, jni.get_array_length(e, numbers));
  if (jni.check(e).is_err()) {
    return out;
  }
  for k in 0..meta.len() / 6 {
    let at = k * 6;
    let w = meta[at];
    let h = meta[at + 1];
    let pixels = jni.int_array_region(e, jni.get_object_array_element(e, kept, k + 1), 0, w * h);
    if (jni.check(e).is_err()) {
      return out;
    }
    let (x, y, width, height) = box_of_cue(w, h, meta[at + 2], meta[at + 3], meta[at + 4], meta[at + 5]);
    out = out.append(ImageCue { image: draw.Image { width: w, height: h, pixels: rgba(pixels) }, x: x, y: y, width: width, height: height });
  }
  return out;
}
```

### `the main thread`

`handle` and `events` run on the main thread, in the native methods of the app's listener. Each one finds the classes in a local frame, and lets them go at its end. `refresh` writes the state after each command and each event. It writes it before the changes go to the loop, so the loop reads the state of each change.

A sound track to select waits in `S_WANTED` until the tracks are known. `choose_audio` then gives the track selector an override for the group of that track.

```fern
<<the main thread>>=
// The listener object holds this array: the state, the ExoPlayer once
// CREATE made it, and the cues that `cues` keeps.
struct Box { array: usize, state: bindings.AtomicLongArray, player: bindings.Player }

function box_of(e: usize, c: bindings.Classes, listener: usize): Result[Box, jni.JavaError] {
  let held = c.atomic_reference.get(e, bindings.AtomicReference { ref: listener })?;
  let state = jni.get_object_array_element(e, held.ref, 0);
  jni.check(e)?;
  let exo = jni.get_object_array_element(e, held.ref, 1);
  jni.check(e)?;
  return Ok(Box { array: held.ref, state: bindings.AtomicLongArray { ref: state }, player: bindings.Player { ref: exo } });
}

function put(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, k: i32, v: i64): Result[void, jni.JavaError] {
  return c.atomic_long_array.set(e, s, k, v);
}

// Writes each value of `vs` at the index of the same place in `ks`.
function put_all(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, ks: i32[], vs: i64[]): Result[void, jni.JavaError] {
  for i in 0..ks.len() {
    put(e, c, s, ks[i], vs[i])?;
  }
  return Ok(());
}

function get(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, k: i32): Result[i64, jni.JavaError] {
  return c.atomic_long_array.get(e, s, k);
}

function flag(on: boolean): i64 {
  if (on) {
    return 1i64;
  }
  return 0i64;
}

function post(e: usize, slot: i32, c: player.Change): void {
  let (kind, arg) = player.posted(c, slot);
  let sent = android.tell(e, kind, arg);
}

// The native handleMessage of the listener: runs one command. It gives 1,
// as the message needs no other handler.
pub function handle(e: usize, listener: usize, msg: usize): u8 {
  if (!begin(e)) {
    return 0u8;
  }
  defer end(e);
  match (bindings.classes(e)) {
    Ok(c) => {
      if let Err(err) = command(e, c, listener, msg) {
        log.warn("exoplayer: a command failed: " + err.message());
      }
      c.release(e);
    },
    Err(err) => {
      log.warn("exoplayer: the APK has no Media3 classes: " + err.message());
    }
  }
  return 1u8;
}

function command(e: usize, c: bindings.Classes, listener: usize, msg: usize): Result[void, jni.JavaError] {
  let m = handler.command(e, msg)?;
  let what = m.what;
  let arg = m.arg;
  let obj = jni.Object { ref: m.obj };
  if (what == DROP_TARGET) {
    return drop_target(e, c, obj);
  }
  let b = box_of(e, c, listener)?;
  if (what == CREATE) {
    return create(e, c, listener, b, obj);
  }
  if (b.player.is_null()) {
    return Ok(());
  }
  return on_player(e, c, b, what, arg, obj);
}

// Runs a command on the ExoPlayer of the box `b`, then writes the state.
function on_player(e: usize, c: bindings.Classes, b: Box, what: i32, arg: i32, obj: jni.Object): Result[void, jni.JavaError] {
  if (what == RELEASE) {
    c.player.release_(e, b.player)?;
    jni.set_object_array_element(e, b.array, 1, 0 as usize);
    return jni.check(e);
  }
  run(e, c, b.state, b.player, what, arg, obj)?;
  refresh(e, c, b.state, b.player)?;
  if (what == SEEK) {
    post(e, get(e, c, b.state, S_SLOT)? as i32, player.Seeked);
  }
  return Ok(());
}

// Runs a command of the playback on the ExoPlayer `x`.
function run(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, x: bindings.Player, what: i32, arg: i32, obj: jni.Object): Result[void, jni.JavaError] {
  if (what == OPEN) {
    no_text_now(e, c, s, x)?;
    return open_item(e, c, s, x, obj);
  }
  if (what == START) {
    return c.player.play(e, x);
  }
  if (what == PAUSE) {
    return c.player.pause(e, x);
  }
  if (what == SEEK) {
    return c.player.seek_to(e, x, arg as i64);
  }
  if (what == SPEED) {
    c.player.set_playback_speed(e, x, arg as f32 / 1000.0f32)?;
    return put(e, c, s, S_SPEED, arg as i64);
  }
  return run_more(e, c, s, x, what, arg, obj);
}

function run_more(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, x: bindings.Player, what: i32, arg: i32, obj: jni.Object): Result[void, jni.JavaError] {
  if (what == STOP) {
    c.player.stop(e, x)?;
    c.player.clear_media_items(e, x)?;
    return put_all(e, c, s, [S_PHASE, S_CHAINED], [IDLE as i64, 0i64]);
  }
  if (what == SURFACE && obj.is_null()) {
    return c.player.clear_video_surface(e, x);
  }
  if (what == SURFACE) {
    return c.player.set_video_surface(e, x, bindings.Surface { ref: obj.ref });
  }
  if (what == CHAIN) {
    return chain_item(e, c, s, x, obj);
  }
  return run_tracks(e, c, s, x, what, arg);
}

// Runs a command that chooses a track: the sound with AUDIO, and the
// subtitle with TEXT.
function run_tracks(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, x: bindings.Player, what: i32, arg: i32): Result[void, jni.JavaError] {
  if (what == AUDIO) {
    put(e, c, s, S_WANTED, arg as i64 + 1i64)?;
    return choose_audio(e, c, s, x);
  }
  if (what == TEXT) {
    put(e, c, s, S_TEXT, arg as i64 + 1i64)?;
    return choose_text(e, c, s, x);
  }
  return Ok(());
}

// A new item shows no subtitle track.
function no_text_now(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, x: bindings.Player): Result[void, jni.JavaError] {
  if (get(e, c, s, S_TEXT)? <= 0i64) {
    return Ok(());
  }
  put(e, c, s, S_TEXT, 0i64)?;
  return no_text(e, c, x);
}

// Gives the track selector the tracks that wait, after the tracks of the
// player changed.
function tracks_changed(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, x: bindings.Player): Result[void, jni.JavaError] {
  choose_audio(e, c, s, x)?;
  if (get(e, c, s, S_TEXT)? > 0i64) {
    return choose_text(e, c, s, x);
  }
  return Ok(());
}

// Prepares the item at the address `obj`, paused.
function open_item(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, x: bindings.Player, obj: jni.Object): Result[void, jni.JavaError] {
  let item = c.media_item.from_uri(e, jni.string_of(e, obj.ref))?;
  c.player.pause(e, x)?;
  c.player.set_media_item(e, x, item, 0i64)?;
  c.player.prepare(e, x)?;
  return put_all(e, c, s, [S_PHASE, S_BUFFERING, S_CHAINED, S_WANTED, S_AUDIO, S_AUDIO_SELECTED], [OPENING as i64, 0i64, 0i64, 0i64, 0i64, 0i64]);
}

// Takes away each item after the item that plays, then adds the item at
// the address `obj`, if there is one.
function chain_item(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, x: bindings.Player, obj: jni.Object): Result[void, jni.JavaError] {
  let count = c.player.get_media_item_count(e, x)?;
  let at = c.player.get_current_media_item_index(e, x)?;
  if (count > at + 1) {
    c.player.remove_media_items(e, x, at + 1, count)?;
  }
  if (obj.is_null()) {
    return put(e, c, s, S_CHAINED, 0i64);
  }
  c.player.add_media_item(e, x, c.media_item.from_uri(e, jni.string_of(e, obj.ref))?)?;
  return put(e, c, s, S_CHAINED, 1i64);
}

// Releases the Surface and the SurfaceTexture in the array `obj`.
function drop_target(e: usize, c: bindings.Classes, obj: jni.Object): Result[void, jni.JavaError] {
  let surface = jni.get_object_array_element(e, obj.ref, 0);
  jni.check(e)?;
  let texture = jni.get_object_array_element(e, obj.ref, 1);
  jni.check(e)?;
  c.surface.release_(e, bindings.Surface { ref: surface })?;
  return c.surface_texture.release_(e, bindings.SurfaceTexture { ref: texture });
}

// Makes the ExoPlayer of the listener, with the attributes of a film and
// no text track. The app keeps the audio focus itself.
function create(e: usize, c: bindings.Classes, listener: usize, b: Box, context: jni.Object): Result[void, jni.JavaError] {
  let builder = c.exo_player_builder.new(e, bindings.Context { ref: context.ref })?;
  let with_attrs = c.exo_player_builder.set_audio_attributes(e, builder, film(e, c)?, false)?;
  let exo = c.exo_player_builder.build(e, with_attrs)?;
  let x = exo.as_player();
  c.player.add_listener(e, x, bindings.Player_Listener { ref: listener })?;
  no_text(e, c, x)?;
  jni.set_object_array_element(e, b.array, 1, exo.ref);
  return jni.check(e);
}

// The audio attributes of a film.
function film(e: usize, c: bindings.Classes): Result[bindings.AudioAttributes, jni.JavaError] {
  let ab = c.audio_attributes_builder.new(e)?;
  let used = c.audio_attributes_builder.set_usage(e, ab, USAGE_MEDIA)?;
  let typed = c.audio_attributes_builder.set_content_type(e, used, CONTENT_MOVIE)?;
  return c.audio_attributes_builder.build(e, typed);
}

function no_text(e: usize, c: bindings.Classes, x: bindings.Player): Result[void, jni.JavaError] {
  let params = c.player.get_track_selection_parameters(e, x)?;
  let upon = c.track_selection_parameters.build_upon(e, params)?;
  let off = c.track_selection_parameters_builder.set_track_type_disabled(e, upon, TRACK_TEXT, true)?;
  return c.player.set_track_selection_parameters(e, x, c.track_selection_parameters_builder.build(e, off)?);
}

// The numbers of the sound and video track groups of the player: all, the
// selected sound groups, and the video groups that a decoder plays.
struct Groups { audio: i32, audio_selected: i32, video: i32, video_played: i32 }

// The track groups of the player that plays.
function groups_list(e: usize, c: bindings.Classes, x: bindings.Player): Result[bindings.List, jni.JavaError] {
  let tracks = c.player.get_current_tracks(e, x)?;
  return Ok(bindings.List { ref: c.tracks.get_groups(e, tracks)?.ref });
}

// The kind of the group `i` of `list`, and whether it is selected, for a
// sound group, or played, for a video group.
function group_at(e: usize, c: bindings.Classes, list: bindings.List, i: i32): Result[(i32, boolean), jni.JavaError] {
  let g = bindings.Tracks_Group { ref: c.list.get(e, list, i)?.ref };
  defer jni.delete_local_ref(e, g.ref);
  let kind = c.tracks_group.get_type(e, g)?;
  if (kind == TRACK_AUDIO) {
    return Ok((kind, c.tracks_group.is_selected(e, g)?));
  }
  if (kind == TRACK_VIDEO) {
    return Ok((kind, c.tracks_group.is_supported(e, g)?));
  }
  return Ok((kind, false));
}

function groups(e: usize, c: bindings.Classes, x: bindings.Player): Result[Groups, jni.JavaError] {
  let list = groups_list(e, c, x)?;
  let audio = 0;
  let selected = 0;
  let video = 0;
  let played = 0;
  for i in 0..c.list.size(e, list)? {
    let (kind, on) = group_at(e, c, list, i)?;
    if (kind == TRACK_AUDIO) {
      audio += 1;
      selected += flag(on) as i32;
    } else if (kind == TRACK_VIDEO) {
      video += 1;
      played += flag(on) as i32;
    }
  }
  return Ok(Groups { audio: audio, audio_selected: selected, video: video, video_played: played });
}

// The group of the track of kind `kind` with the place `wanted` among the
// tracks of that kind, counted from 1, or None.
function group_of(e: usize, c: bindings.Classes, x: bindings.Player, kind: i32, wanted: i64): Result[Option[bindings.TrackGroup], jni.JavaError] {
  let list = groups_list(e, c, x)?;
  let k = 0i64;
  for i in 0..c.list.size(e, list)? {
    let g = bindings.Tracks_Group { ref: c.list.get(e, list, i)?.ref };
    if (c.tracks_group.get_type(e, g)? == kind) {
      k += 1i64;
      if (k == wanted) {
        return Ok(Some(c.tracks_group.get_media_track_group(e, g)?));
      }
    }
  }
  return Ok(None);
}

// Gives the track selector the sound track that S_WANTED names, when the
// player has it.
function choose_audio(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, x: bindings.Player): Result[void, jni.JavaError] {
  let wanted = get(e, c, s, S_WANTED)?;
  if (wanted <= 0i64) {
    return Ok(());
  }
  let Some(group) = group_of(e, c, x, TRACK_AUDIO, wanted)? else {
    return Ok(());
  };
  select_group(e, c, x, TRACK_AUDIO, group)?;
  return put(e, c, s, S_WANTED, 0i64);
}

// Gives the track selector the subtitle track that S_TEXT names, or turns
// the text tracks off. A track that the player does not have yet waits for
// the next change of the tracks.
function choose_text(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, x: bindings.Player): Result[void, jni.JavaError] {
  let wanted = get(e, c, s, S_TEXT)?;
  if (wanted <= 0i64) {
    return no_text(e, c, x);
  }
  let Some(group) = group_of(e, c, x, TRACK_TEXT, wanted)? else {
    return Ok(());
  };
  return select_group(e, c, x, TRACK_TEXT, group);
}

// Gives the track selector an override for the first track of `group`, a
// group of the kind `kind`, and turns that kind on.
function select_group(e: usize, c: bindings.Classes, x: bindings.Player, kind: i32, group: bindings.TrackGroup): Result[void, jni.JavaError] {
  let pick = c.track_selection_override.new(e, group, 0)?;
  let params = c.player.get_track_selection_parameters(e, x)?;
  let upon = c.track_selection_parameters.build_upon(e, params)?;
  let on = c.track_selection_parameters_builder.set_track_type_disabled(e, upon, kind, false)?;
  let chosen = c.track_selection_parameters_builder.set_override_for_type(e, on, pick)?;
  return c.player.set_track_selection_parameters(e, x, c.track_selection_parameters_builder.build(e, chosen)?);
}

// Writes the place, the length, the size of the video and the sound
// tracks of the player to the state.
function refresh(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, x: bindings.Player): Result[Groups, jni.JavaError] {
  let place = c.player.get_current_position(e, x)?;
  let length = c.player.get_duration(e, x)?;
  if (length < 0i64) {
    length = 0i64;
  }
  let on = c.player.is_playing(e, x)?;
  let size = c.player.get_video_size(e, x)?;
  let w = c.video_size.width(e, size)?;
  let h = c.video_size.height(e, size)?;
  let g = groups(e, c, x)?;
  put_all(e, c, s, [S_POSITION, S_AT, S_DURATION, S_PLAYING, S_WIDTH, S_HEIGHT, S_AUDIO, S_AUDIO_SELECTED], [place, monotonic_ns() / 1000000i64, length, flag(on), w as i64, h as i64, g.audio as i64, g.audio_selected as i64])?;
  return Ok(g);
}

// The native onEvents of the listener: writes the state, and sends the
// changes that `follow` gives to the loop.
pub function events(e: usize, listener: usize, exo: usize, events: usize): void {
  if (!begin(e)) {
    return;
  }
  defer end(e);
  match (bindings.classes(e)) {
    Ok(c) => {
      if let Err(err) = changed(e, c, listener, bindings.Player { ref: exo }, bindings.Player_Events { ref: events }) {
        log.warn("exoplayer: an event failed: " + err.message());
      }
      c.release(e);
    },
    Err(err) => {
      log.warn("exoplayer: the APK has no Media3 classes: " + err.message());
    }
  }
}

// The code of a new error of the player, or None.
function error_of(e: usize, c: bindings.Classes, x: bindings.Player, ev: bindings.Player_Events): Result[Option[i32], jni.JavaError] {
  if (!c.player_events.contains(e, ev, EVENT_ERROR)?) {
    return Ok(None);
  }
  let err = c.player.get_player_error(e, x)?;
  if (err.is_null()) {
    return Ok(Some(0));
  }
  return Ok(Some(c.playback_exception.error_code(e, err)?));
}

// True when the player went on to the item that `chain` gave it. Then the
// item before goes away.
function moved_on(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, x: bindings.Player, ev: bindings.Player_Events): Result[boolean, jni.JavaError] {
  if (!c.player_events.contains(e, ev, EVENT_TRANSITION)? || get(e, c, s, S_CHAINED)? == 0i64) {
    return Ok(false);
  }
  let at = c.player.get_current_media_item_index(e, x)?;
  if (at <= 0) {
    return Ok(false);
  }
  c.player.remove_media_items(e, x, 0, at)?;
  put(e, c, s, S_CHAINED, 0i64)?;
  return Ok(true);
}

// What the event `ev` shows of the player, with its track groups `g`.
function snapshot(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, x: bindings.Player, ev: bindings.Player_Events, g: Groups): Result[Snapshot, jni.JavaError] {
  let error = error_of(e, c, x, ev)?;
  let state = c.player.get_playback_state(e, x)?;
  let moved = moved_on(e, c, s, x, ev)?;
  let frame = c.player_events.contains(e, ev, EVENT_FIRST_FRAME)?;
  return Ok(Snapshot { state: state, failed: error.is_some(), error: error.unwrap_or(0), moved_on: moved, first_frame: frame, video: g.video, video_played: g.video_played });
}

function changed(e: usize, c: bindings.Classes, listener: usize, x: bindings.Player, ev: bindings.Player_Events): Result[void, jni.JavaError] {
  let s = box_of(e, c, listener)?.state;
  if (c.player_events.contains(e, ev, EVENT_TRACKS)?) {
    tracks_changed(e, c, s, x)?;
  }
  let changes = step(e, c, s, snapshot(e, c, s, x, ev, refresh(e, c, s, x)?)?)?;
  let slot = get(e, c, s, S_SLOT)? as i32;
  for change in changes {
    post(e, slot, change);
  }
  return Ok(());
}

// Moves the phase in the state on after the event `snap`, and gives the
// changes of the move.
function step(e: usize, c: bindings.Classes, s: bindings.AtomicLongArray, snap: Snapshot): Result[player.Change[], jni.JavaError] {
  let was = Phase { stage: get(e, c, s, S_PHASE)? as i32, buffering: get(e, c, s, S_BUFFERING)? != 0i64 };
  let (now, changes) = follow(was, snap);
  put_all(e, c, s, [S_PHASE, S_BUFFERING], [now.stage as i64, flag(now.buffering)])?;
  return Ok(changes);
}
```
