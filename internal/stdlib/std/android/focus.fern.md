# Android audio focus

This module asks Android for the audio focus, for a NativeActivity app of `std/android`, and reports when the app loses the focus.

## Model

Android gives the audio focus to one client at a time. A call takes it, and so does an app that starts to play or record. The app declares a @jni class that implements `android.media.AudioManager$OnAudioFocusChangeListener`, with a native `onAudioFocusChange` that calls `changed`.

`new` makes one listener of that class, and a focus request for speech in a voice call. `changed` sends each change to the live loop of the app with `android.tell`. The loop gives it to the app as a `Posted` event. `event` decodes it.

`music` makes a focus request for music in place of speech. That request is not transient: a different app that plays stops, and it does not start again by itself.

`request` asks for the focus until `release`. A request of `new` is transient: the client that had the focus gets it back after the release. Each listener is a client of its own, also in one app.

A `Loss` comes when a call or another app takes the focus. A `Duck` comes when another app plays a short sound, such as a notification. A `Gain` comes when the focus comes back after a loss.

The module needs API level 26, for `AudioFocusRequest`.

## Contracts

Call each function except `changed` on the loop thread. Call `close` before Destroy.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<events>>

<<the focus>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module asks Android for the audio focus.
import "std/android";
import "std/jni";
import "std/android/focus/bindings";
```

### `events`

The listener runs on the main thread of the app. It sends the change in the argument of a `Posted` event of kind 18: 1 for a gain, 2 for a loss and 3 for a duck.

```fern
<<events>>=
pub enum Change { Gain, Loss, Duck }

const CHANGED: i32 = 18;

// The focus change in the event `e` of the loop, or None.
pub function event(e: android.Event): Option[Change] {
  let android.Posted(kind, arg) = e else {
    return None;
  };
  if (kind != CHANGED) {
    return None;
  }
  if (arg == 1 as usize) {
    return Some(Gain);
  }
  if (arg == 2 as usize) {
    return Some(Loss);
  }
  return Some(Duck);
}

// The argument of the event for an AUDIOFOCUS change: 1 is a gain, -1 and -2
// are losses, and -3 lets the app play on at a lower volume.
function code_of(change: i32): usize {
  if (change > 0) {
    return 1 as usize;
  }
  if (change == -3) {
    return 3 as usize;
  }
  return 2 as usize;
}
```

### `the focus`

`AudioManager` takes the request with `requestAudioFocus`, and gives 1 when it grants it. During a call it gives 0.

```fern
<<the focus>>=
// The global references of the audio manager, the request and the listener.
pub struct Focus { manager: usize, request: usize, listener: usize }

const GAIN: i32 = 1;

const GAIN_TRANSIENT: i32 = 2;

const USAGE_MEDIA: i32 = 1;

const CONTENT_MUSIC: i32 = 2;

const USAGE_VOICE_COMMUNICATION: i32 = 2;

const CONTENT_SPEECH: i32 = 1;

// Promote only the references that survive the local frame.
function keep(e: usize, obj: usize): Result[usize, jni.JavaError] {
  let ref = jni.new_global_ref(e, obj);
  if (ref == 0 as usize) {
    jni.check(e)?;
    return Err(jni.JavaError { text: "audio focus reference is null" });
  }
  return Ok(ref);
}

function make_focus(e: usize, activity: bindings.Context, name: string, gain: i32, purpose: i32, kind: i32): Result[Focus, jni.JavaError] {
  let c = bindings.classes(e)?;
  defer c.release(e);
  let manager = c.context.get_system_service(e, activity, "audio")?;
  if (manager.ref == 0 as usize) {
    return Err(jni.JavaError { text: "audio service unavailable" });
  }
  let loader = c.context.get_class_loader(e, activity)?;
  let class = c.class_loader.load_class(e, loader, name)?;
  let listener = c.class.new_instance(e, class)?;
  let attrs = c.audio_attributes_builder.new(e)?;
  let usage = c.audio_attributes_builder.set_usage(e, attrs, purpose)?;
  let content = c.audio_attributes_builder.set_content_type(e, usage, kind)?;
  let attributes = c.audio_attributes_builder.build(e, content)?;
  let builder = c.audio_focus_request_builder.new(e, gain)?;
  let configured = c.audio_focus_request_builder.set_audio_attributes(e, builder, attributes)?;
  let ready = c.audio_focus_request_builder.set_on_audio_focus_change_listener(e, configured, bindings.AudioManager_OnAudioFocusChangeListener { ref: listener.ref })?;
  let request = c.audio_focus_request_builder.build(e, ready)?;
  let m = keep(e, manager.ref)?;
  errdefer jni.delete_global_ref(e, m);
  let r = keep(e, request.ref)?;
  errdefer jni.delete_global_ref(e, r);
  let l = keep(e, listener.ref)?;
  return Ok(Focus { manager: m, request: r, listener: l });
}

// A focus request with one listener of `class`, the Java name of the app's
// @jni class that implements OnAudioFocusChangeListener. It is None when
// the phone has no audio manager.
pub function new(ctx: usize, class: string): Option[Focus] {
  return made(ctx, class, GAIN_TRANSIENT, USAGE_VOICE_COMMUNICATION, CONTENT_SPEECH);
}

// A focus request for music with one listener of `class`. The request is
// not transient: a different app that plays stops until the person starts
// it again.
pub function music(ctx: usize, class: string): Option[Focus] {
  return made(ctx, class, GAIN, USAGE_MEDIA, CONTENT_MUSIC);
}

function made(ctx: usize, class: string, gain: i32, purpose: i32, kind: i32): Option[Focus] {
  let e = android.env(ctx);
  if (!jni.push_local_frame(e, 64)) {
    return None;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  let Ok(f) = make_focus(e, bindings.Context { ref: android.activity_object(ctx) }, class, gain, purpose, kind) else {
    return None;
  };
  return Some(f);
}

// Asks for the focus. It is false when Android refuses, as during a call.
pub function request(ctx: usize, f: Focus): boolean {
  let e = android.env(ctx);
  if (!jni.push_local_frame(e, 64)) {
    return false;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  let Ok(c) = bindings.audio_manager_class(e) else {
    return false;
  };
  defer c.release(e);
  let Ok(result) = c.request_audio_focus(e, bindings.AudioManager { ref: f.manager }, bindings.AudioFocusRequest { ref: f.request }) else {
    return false;
  };
  return result == 1;
}

// Gives the focus back.
pub function release(ctx: usize, f: Focus): void {
  let e = android.env(ctx);
  if (!jni.push_local_frame(e, 64)) {
    return;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  let Ok(c) = bindings.audio_manager_class(e) else {
    return;
  };
  defer c.release(e);
  let result = c.abandon_audio_focus_request(e, bindings.AudioManager { ref: f.manager }, bindings.AudioFocusRequest { ref: f.request });
}

// Gives the focus back and frees the request. Call it before Destroy.
pub function close(ctx: usize, f: Focus): void {
  release(ctx, f);
  let e: usize = android.env(ctx);
  jni.delete_global_ref(e, f.listener);
  jni.delete_global_ref(e, f.request);
  jni.delete_global_ref(e, f.manager);
}

// The onAudioFocusChange of the app's listener: `env`, `listener` and
// `change` are its arguments. It runs on the UI thread, and sends the change
// to the loop thread.
pub function changed(e: usize, listener: usize, change: i32): void {
  let sent = android.tell(e, CHANGED, code_of(change));
}
```
