# Android media sessions

This module tells Android what an app plays, for a NativeActivity app of `std/android`, and reports the media buttons that the person uses.

## Model

A media session holds the title of the track that plays, its picture, its length, and the state of the player. Android shows them with its media controls: in the notification panel, on the lock screen, and on a headset or a watch. A button of those controls sends a command to the session.

The app declares a @jni class that extends `android.media.session.MediaSession$Callback`. Each native method of that class calls the function of this module for its command: `play`, `pause`, `next`, `previous`, `stop` or `seek_to`. `new` makes one session with one callback of that class.

The callback runs on the main thread of the app. It sends the command to the live loop of the app with `android.tell`. A loop that `android.keep_alive` keeps after the Destroy of its activity gets the command too. The loop gives it to the app as a `Posted` event. `event` decodes it as a `Command`.

`set_track` gives the session the track, with the picture of its album when the app has one. Android shows the picture behind the media controls. `set_state` gives it the state of the player and the time of the track. Android counts the time on from there while the player plays. So call `set_state` only when the state changes or the player goes to a different time.

`token` gives the token of the session. A notification of `std/android/service` that has the token shows as the media controls of the app.

The module needs API level 26.

The module calls Java only through `std/android/session/bindings`, which `jnibind` writes from `bindings.spec`.

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
import "std/android/session/bindings";
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
  let sent = android.tell(e, kind, arg);
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

// The room of the local frame of one call of this module.
const FRAME: i32 = 32;

function end_frame(e: usize): void {
  let _ = jni.pop_local_frame(e, 0 as usize);
}

function session_of(s: Session): bindings.MediaSession {
  return bindings.MediaSession { ref: s.obj };
}

// Gives `session` one callback of the class `class`, on a handler of the
// main looper of the app, and makes the session active.
function attach(e: usize, c: bindings.Classes, activity: bindings.Context, session: bindings.MediaSession, class: string): Result[Session, jni.JavaError] {
  let loader = c.context.get_class_loader(e, activity)?;
  let loaded = c.class_loader.load_class(e, loader, class)?;
  let callback = c.class.new_instance(e, loaded)?;
  let looper = c.looper.get_main_looper(e)?;
  let handler = c.handler.new(e, looper)?;
  c.media_session.set_callback(e, session, bindings.MediaSession_Callback { ref: callback.ref }, handler)?;
  c.media_session.set_active(e, session, true)?;
  return Ok(Session { obj: jni.new_global_ref(e, session.ref), callback: jni.new_global_ref(e, callback.ref) });
}

// An active session with the name `tag` and one callback of `class`, the
// Java name of the app's @jni class that extends MediaSession.Callback. It
// is None when Android gives no session.
pub function new(ctx: usize, class: string, tag: string): Option[Session] {
  let e: usize = android.env(ctx);
  if (!jni.push_local_frame(e, FRAME)) {
    return None;
  }
  defer end_frame(e);
  let Ok(c) = bindings.classes(e) else {
    return None;
  };
  defer c.release(e);
  let activity = bindings.Context { ref: android.activity_object(ctx) };
  let Ok(session) = c.media_session.new(e, activity, tag) else {
    return None;
  };
  if (session.is_null()) {
    return None;
  }
  match (attach(e, c, activity, session, class)) {
    Ok(s) => {
      return Some(s);
    },
    Err(_) => {
      let _ = c.media_session.release_(e, session);
      return None;
    }
  }
}

// The longest side in pixels of the picture that the session gets.
const ART_SIDE: i32 = 320;

// Puts the picture `img` of the album in the track of `builder`, at a
// size of `ART_SIDE` pixels or less. A pixel of a Bitmap is alpha, red,
// green and blue in one int.
function put_art(e: usize, c: bindings.Classes, builder: bindings.MediaMetadata_Builder, img: draw.Image): Result[void, jni.JavaError] {
  let small = img;
  let longest = img.width.max(img.height);
  if (longest > ART_SIDE) {
    small = img.scaled((img.width * ART_SIDE / longest).max(1), (img.height * ART_SIDE / longest).max(1));
  }
  let n = small.width * small.height;
  if (n <= 0 || small.pixels.len() < n * 4) {
    return Ok(());
  }
  let colors: i32[] = [];
  for k in 0..n {
    let at = k * 4;
    colors = colors.append(small.pixels[at + 3] as i32 << 24 | small.pixels[at] as i32 << 16 | small.pixels[at + 1] as i32 << 8 | small.pixels[at + 2] as i32);
  }
  let array: usize = jni.new_int_array(e, n);
  jni.check(e)?;
  if (array == 0 as usize) {
    return Ok(());
  }
  jni.set_int_array_region(e, array, 0, colors);
  let config = c.bitmap_config.argb_8888(e)?;
  let picture = c.bitmap.create_bitmap(e, jni.Object { ref: array }, small.width, small.height, config)?;
  if (picture.is_null()) {
    return Ok(());
  }
  let same = c.media_metadata_builder.put_bitmap(e, builder, bindings.MEDIA_METADATA_METADATA_KEY_ALBUM_ART, picture)?;
  return Ok(());
}

function publish_track(e: usize, c: bindings.Classes, s: Session, title: string, artist: string, album: string, length_ms: i64, art: Option[draw.Image]): Result[void, jni.JavaError] {
  let builder = c.media_metadata_builder.new(e)?;
  if let Some(img) = art {
    // A track with no picture is better than no track.
    let _ = put_art(e, c, builder, img);
  }
  let titled = c.media_metadata_builder.put_string(e, builder, bindings.MEDIA_METADATA_METADATA_KEY_TITLE, title)?;
  let by = c.media_metadata_builder.put_string(e, builder, bindings.MEDIA_METADATA_METADATA_KEY_ARTIST, artist)?;
  let on = c.media_metadata_builder.put_string(e, builder, bindings.MEDIA_METADATA_METADATA_KEY_ALBUM, album)?;
  let timed = c.media_metadata_builder.put_long(e, builder, bindings.MEDIA_METADATA_METADATA_KEY_DURATION, length_ms.max(0i64))?;
  let track = c.media_metadata_builder.build(e, builder)?;
  return c.media_session.set_metadata(e, session_of(s), track);
}

// Gives the session the track that plays: its title, its artist, its
// album, its length in ms, and the picture of its album or None.
pub function set_track(ctx: usize, s: Session, title: string, artist: string, album: string, length_ms: i64, art: Option[draw.Image]): void {
  let e: usize = android.env(ctx);
  if (!jni.push_local_frame(e, FRAME)) {
    return;
  }
  defer end_frame(e);
  let Ok(c) = bindings.classes(e) else {
    return;
  };
  defer c.release(e);
  let _ = publish_track(e, c, s, title, artist, album, length_ms, art);
}

// The controls of `set_state`. A browser such as Android Auto plays an
// item of `std/android/browse` with ACTION_PLAY_FROM_MEDIA_ID.
function default_actions(): i64 {
  return bindings.PLAYBACK_STATE_ACTION_STOP | bindings.PLAYBACK_STATE_ACTION_PAUSE | bindings.PLAYBACK_STATE_ACTION_PLAY | bindings.PLAYBACK_STATE_ACTION_SKIP_TO_PREVIOUS | bindings.PLAYBACK_STATE_ACTION_SKIP_TO_NEXT | bindings.PLAYBACK_STATE_ACTION_SEEK_TO | bindings.PLAYBACK_STATE_ACTION_PLAY_PAUSE | bindings.PLAYBACK_STATE_ACTION_PLAY_FROM_MEDIA_ID;
}

// Gives the session the state of the player, with its default controls.
pub function set_state(ctx: usize, s: Session, playing: boolean, at_ms: i64): void {
  let state = if (playing) { bindings.PLAYBACK_STATE_STATE_PLAYING } else { bindings.PLAYBACK_STATE_STATE_PAUSED };
  let rate = if (playing) { 1.0f32 } else { 0.0f32 };
  set_transport(ctx, s, state, at_ms, rate, default_actions());
}

// Publishes the Android playback state, speed, and action flags.
pub function set_transport(ctx: usize, s: Session, state: i32, at_ms: i64, rate: f32, actions: i64): void {
  set_transport_actions(ctx, s, state, at_ms, rate, actions, []);
}

pub struct TransportAction { id: string, label: string, icon: i32 }

function publish_state(e: usize, sessions: bindings.MediaSessionClass, builders: bindings.PlaybackState_BuilderClass, s: Session, state: i32, at_ms: i64, rate: f32, actions: i64, custom: TransportAction[]): Result[void, jni.JavaError] {
  let builder = builders.new(e)?;
  let acted = builders.set_actions(e, builder, actions)?;
  let stated = builders.set_state(e, builder, state, at_ms.max(0i64), rate)?;
  for action in custom {
    let added = builders.add_custom_action(e, builder, action.id, action.label, action.icon)?;
    let _ = jni.delete_local_ref(e, added.ref);
  }
  let built = builders.build(e, builder)?;
  return sessions.set_playback_state(e, session_of(s), built);
}

// Adds custom media controls to the published Android playback state.
pub function set_transport_actions(ctx: usize, s: Session, state: i32, at_ms: i64, rate: f32, actions: i64, custom: TransportAction[]): void {
  let e: usize = android.env(ctx);
  if (!jni.push_local_frame(e, FRAME)) {
    return;
  }
  defer end_frame(e);
  let Ok(sessions) = bindings.media_session_class(e) else {
    return;
  };
  defer sessions.release(e);
  let Ok(builders) = bindings.playback_state_builder_class(e) else {
    return;
  };
  defer builders.release(e);
  let _ = publish_state(e, sessions, builders, s, state, at_ms, rate, actions, custom);
}

// A global reference of the token of the session, for the notification of
// a foreground service. The caller keeps it for the life of the session.
// It is 0 when Android gives no token.
pub function token(ctx: usize, s: Session): usize {
  let e: usize = android.env(ctx);
  let Ok(sessions) = bindings.media_session_class(e) else {
    return 0 as usize;
  };
  defer sessions.release(e);
  let Ok(local) = sessions.get_session_token(e, session_of(s)) else {
    return 0 as usize;
  };
  if (local.is_null()) {
    return 0 as usize;
  }
  let global: usize = jni.new_global_ref(e, local.ref);
  let _ = jni.delete_local_ref(e, local.ref);
  return global;
}

// Ends the session and frees it. Call it before Destroy.
pub function close(ctx: usize, s: Session): void {
  let e: usize = android.env(ctx);
  if let Ok(sessions) = bindings.media_session_class(e) {
    let _ = sessions.release_(e, session_of(s));
    sessions.release(e);
  }
  jni.delete_global_ref(e, s.callback);
  jni.delete_global_ref(e, s.obj);
}
```
