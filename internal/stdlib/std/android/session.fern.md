# Android media sessions

This module tells Android what an app plays, for a NativeActivity app of `std/android`, and reports the media buttons that the person uses.

## Model

A media session holds the title of the track that plays, its picture, its length, and the state of the player. Android shows them with its media controls: in the notification panel, on the lock screen, and on a headset or a watch. A button of those controls sends a command to the session.

The app declares a @jni class that extends `android.media.session.MediaSession$Callback`. Each native method of that class calls the function of this module for its command: `play`, `pause`, `next`, `previous`, `stop` or `seek_to`. `new` makes one session with one callback of that class.

The callback runs on the main thread of the app. It sends the command to the loop thread of the live activity, which `android.live_activity` names. The loop gives it to the app as a `Posted` event. `event` decodes it as a `Command`.

`set_track` gives the session the track, with the picture of its album when the app has one. Android shows the picture behind the media controls. `set_state` gives it the state of the player and the time of the track. Android counts the time on from there while the player plays. So call `set_state` only when the state changes or the player goes to a different time.

`token` gives the token of the session. A notification of `std/android/service` that has the token shows as the media controls of the app.

The module needs API level 26.

## Contracts

Call each function except the six command functions on the loop thread. Call `close` before Destroy.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<commands>>

<<the session>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module tells Android what an app plays, and reports media buttons.
import "std/android";
import "std/android/draw";
import "std/jni";
```

### `commands`

The callback sends each command as a `Posted` event of the kinds 28 to 33. The argument of the kind 33 is the time in milliseconds.

```fern
<<commands>>=
// A media button: `Hold` is the pause button, `End` is the stop button,
// and `SeekTo` has the time in ms that the person went to.
pub enum Command { Play, Hold, Next, Previous, End, SeekTo(i64) }

const PLAY: i32 = 28;

const PAUSE: i32 = 29;

const NEXT: i32 = 30;

const PREVIOUS: i32 = 31;

const STOP: i32 = 32;

const SEEK_TO: i32 = 33;

// The command of the session in the event `e` of the loop, or None.
pub function event(e: android.Event): Option[Command] {
  let android.Posted(kind, arg) = e else {
    return None;
  };
  if (kind == SEEK_TO) {
    return Some(SeekTo(arg as i64));
  }
  let plain = [Play, Hold, Next, Previous, End];
  if (kind < PLAY || kind > STOP) {
    return None;
  }
  return Some(plain[kind - PLAY]);
}

function command(e: usize, kind: i32, arg: usize): void {
  let activity: usize = android.live_activity(e);
  if (activity == 0 as usize) {
    return;
  }
  android.send(activity, kind, arg);
}

// The onPlay of the app's callback: `e` is its JNIEnv. Each of these six
// functions runs on the UI thread, and sends its command to the loop thread.
pub function play(e: usize): void {
  command(e, PLAY, 0 as usize);
}

// The onPause of the app's callback.
pub function pause(e: usize): void {
  command(e, PAUSE, 0 as usize);
}

// The onSkipToNext of the app's callback.
pub function next(e: usize): void {
  command(e, NEXT, 0 as usize);
}

// The onSkipToPrevious of the app's callback.
pub function previous(e: usize): void {
  command(e, PREVIOUS, 0 as usize);
}

// The onStop of the app's callback.
pub function stop(e: usize): void {
  command(e, STOP, 0 as usize);
}

// The onSeekTo of the app's callback, with its time in ms.
pub function seek_to(e: usize, ms: i64): void {
  command(e, SEEK_TO, ms.max(0i64) as usize);
}
```

### `the session`

A callback needs a thread with a Java looper. The loop thread has none, so the callback gets a handler of the main looper.

```fern
<<the session>>=
// Global references of the session and of its callback.
pub struct Session { obj: usize, callback: usize }

const SESSION: string = "android/media/session/MediaSession";

const STATE_BUILDER: string = "android/media/session/PlaybackState$Builder";

const TRACK_BUILDER: string = "android/media/MediaMetadata$Builder";

// PlaybackState.STATE_PAUSED and STATE_PLAYING.
const PAUSED: i32 = 2;

const PLAYING: i32 = 3;

// ACTION_STOP, PAUSE, PLAY, SKIP_TO_PREVIOUS, SKIP_TO_NEXT, SEEK_TO and
// PLAY_PAUSE of PlaybackState.
const ACTIONS: i32 = 823;

// The bits of the float 1.0, the speed of a player that plays.
const SPEED_ONE: i32 = 1065353216;

function new_object(e: usize, class: usize, sig: string, args: usize[]): usize {
  let values: usize = jni.jvalues(args);
  let obj: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", sig), values);
  jni.free_buffer(values);
  return obj;
}

// True when the last JNI call threw. It clears the exception.
function threw(e: usize): boolean {
  if (jni.exception_occurred(e) == 0 as usize) {
    return false;
  }
  jni.exception_clear(e);
  return true;
}

// A handler of the main looper of the app.
function main_handler(e: usize): usize {
  let looper_class: usize = jni.find_class(e, "android/os/Looper");
  let none: usize = jni.jvalue_alloc(0);
  let looper: usize = jni.call_static_object_method_a(e, looper_class, jni.get_static_method_id(e, looper_class, "getMainLooper", "()Landroid/os/Looper;"), none);
  jni.free_buffer(none);
  return new_object(e, jni.find_class(e, "android/os/Handler"), "(Landroid/os/Looper;)V", [looper]);
}

// An active session with the name `tag` and one callback of `class`, the
// Java name of the app's @jni class that extends MediaSession.Callback. It
// is None when Android gives no session.
pub function new(ctx: usize, class: string, tag: string): Option[Session] {
  let e: usize = android.env(ctx);
  let obj: usize = android.activity_object(ctx);
  let session: usize = new_object(e, jni.find_class(e, SESSION), "(Landroid/content/Context;Ljava/lang/String;)V", [obj, jni.new_string(e, tag)]);
  if (threw(e) || session == 0 as usize) {
    return None;
  }
  let callback: usize = new_object(e, jni.load_class(e, obj, class), "()V", []);
  if (threw(e) || callback == 0 as usize) {
    return None;
  }
  let set: boolean = jni.call_void(e, session, "setCallback", "(Landroid/media/session/MediaSession$Callback;Landroid/os/Handler;)V", [callback, main_handler(e)]);
  let active: boolean = jni.call_void(e, session, "setActive", "(Z)V", [1 as usize]);
  if (!set || !active) {
    return None;
  }
  return Some(Session { obj: jni.new_global_ref(e, session), callback: jni.new_global_ref(e, callback) });
}

// Puts the text `value` under the key `key` of the track of `builder`.
function put_text(e: usize, builder: usize, key: string, value: string): void {
  let k: usize = jni.new_string(e, key);
  let v: usize = jni.new_string(e, value);
  let same: usize = jni.call_object(e, builder, "putString", "(Ljava/lang/String;Ljava/lang/String;)L" + TRACK_BUILDER + ";", [k, v]);
  let a: usize = jni.delete_local_ref(e, k);
  let b: usize = jni.delete_local_ref(e, v);
  let c: usize = jni.delete_local_ref(e, same);
}

// The longest side in pixels of the picture that the session gets.
const ART_SIDE: i32 = 320;

// A Bitmap of `img`, or 0. A pixel of a Bitmap is alpha, red, green and
// blue in one int.
function bitmap(e: usize, img: draw.Image): usize {
  let n = img.width * img.height;
  if (n <= 0 || img.pixels.len() < n * 4) {
    return 0 as usize;
  }
  let colors: i32[] = [];
  for k in 0..n {
    let at = k * 4;
    colors = colors.append(img.pixels[at + 3] as i32 << 24 | img.pixels[at] as i32 << 16 | img.pixels[at + 1] as i32 << 8 | img.pixels[at + 2] as i32);
  }
  let array: usize = jni.new_int_array(e, n);
  jni.set_int_array_region(e, array, 0, colors);
  let config_class: usize = jni.find_class(e, "android/graphics/Bitmap$Config");
  let config: usize = jni.get_static_object_field(e, config_class, jni.get_static_field_id(e, config_class, "ARGB_8888", "Landroid/graphics/Bitmap$Config;"));
  let bitmap_class: usize = jni.find_class(e, "android/graphics/Bitmap");
  let values: usize = jni.jvalues([array, img.width as usize, img.height as usize, config]);
  let made: usize = jni.call_static_object_method_a(e, bitmap_class, jni.get_static_method_id(e, bitmap_class, "createBitmap", "([IIILandroid/graphics/Bitmap$Config;)Landroid/graphics/Bitmap;"), values);
  jni.free_buffer(values);
  for local in [array, config] {
    let dropped: usize = jni.delete_local_ref(e, local);
  }
  if (threw(e)) {
    return 0 as usize;
  }
  return made;
}

// Puts the picture `img` of the album in the track of `builder`, at a
// size of `ART_SIDE` pixels or less.
function put_art(e: usize, builder: usize, img: draw.Image): void {
  let small = img;
  let longest = img.width.max(img.height);
  if (longest > ART_SIDE) {
    small = img.scaled((img.width * ART_SIDE / longest).max(1), (img.height * ART_SIDE / longest).max(1));
  }
  let picture: usize = bitmap(e, small);
  if (picture == 0 as usize) {
    return;
  }
  let key: usize = jni.new_string(e, "android.media.metadata.ALBUM_ART");
  let same: usize = jni.call_object(e, builder, "putBitmap", "(Ljava/lang/String;Landroid/graphics/Bitmap;)L" + TRACK_BUILDER + ";", [key, picture]);
  for local in [key, same, picture] {
    let dropped: usize = jni.delete_local_ref(e, local);
  }
}

// Gives the session the track that plays: its title, its artist, its
// album, its length in ms, and the picture of its album or None.
pub function set_track(ctx: usize, s: Session, title: string, artist: string, album: string, length_ms: i64, art: Option[draw.Image]): void {
  let e: usize = android.env(ctx);
  let builder: usize = new_object(e, jni.find_class(e, TRACK_BUILDER), "()V", []);
  if let Some(img) = art {
    put_art(e, builder, img);
  }
  put_text(e, builder, "android.media.metadata.TITLE", title);
  put_text(e, builder, "android.media.metadata.ARTIST", artist);
  put_text(e, builder, "android.media.metadata.ALBUM", album);
  let key: usize = jni.new_string(e, "android.media.metadata.DURATION");
  let same: usize = jni.call_object(e, builder, "putLong", "(Ljava/lang/String;J)L" + TRACK_BUILDER + ";", [key, length_ms.max(0i64) as usize]);
  let track: usize = jni.call_object(e, builder, "build", "()Landroid/media/MediaMetadata;", []);
  let set: boolean = jni.call_void(e, s.obj, "setMetadata", "(Landroid/media/MediaMetadata;)V", [track]);
  for local in [key, same, track, builder] {
    let dropped: usize = jni.delete_local_ref(e, local);
  }
}

// Gives the session the state of the player: it plays or not, at the time
// `at_ms` of the track. The session takes each command.
pub function set_state(ctx: usize, s: Session, playing: boolean, at_ms: i64): void {
  let e: usize = android.env(ctx);
  let builder: usize = new_object(e, jni.find_class(e, STATE_BUILDER), "()V", []);
  let acts: usize = jni.call_object(e, builder, "setActions", "(J)L" + STATE_BUILDER + ";", [ACTIONS as usize]);
  let state = PAUSED;
  let speed = 0;
  if (playing) {
    state = PLAYING;
    speed = SPEED_ONE;
  }
  let stated: usize = jni.call_object(e, builder, "setState", "(IJF)L" + STATE_BUILDER + ";", [state as usize, at_ms.max(0i64) as usize, speed as usize]);
  let built: usize = jni.call_object(e, builder, "build", "()Landroid/media/session/PlaybackState;", []);
  let set: boolean = jni.call_void(e, s.obj, "setPlaybackState", "(Landroid/media/session/PlaybackState;)V", [built]);
  for local in [acts, stated, built, builder] {
    let dropped: usize = jni.delete_local_ref(e, local);
  }
}

// A global reference of the token of the session, for the notification of
// a foreground service. The caller keeps it for the life of the session.
pub function token(ctx: usize, s: Session): usize {
  let e: usize = android.env(ctx);
  let local: usize = jni.call_object(e, s.obj, "getSessionToken", "()Landroid/media/session/MediaSession$Token;", []);
  let global: usize = jni.new_global_ref(e, local);
  let dropped: usize = jni.delete_local_ref(e, local);
  return global;
}

// Ends the session and frees it. Call it before Destroy.
pub function close(ctx: usize, s: Session): void {
  let e: usize = android.env(ctx);
  let released: boolean = jni.call_void(e, s.obj, "release", "()V", []);
  jni.delete_global_ref(e, s.callback);
  jni.delete_global_ref(e, s.obj);
}
```
