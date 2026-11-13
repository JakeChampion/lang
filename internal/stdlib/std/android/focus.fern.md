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

const ATTRS: string = "android/media/AudioAttributes$Builder";

const BUILDER: string = "android/media/AudioFocusRequest$Builder";

const LISTENER_SIG: string = "Landroid/media/AudioManager$OnAudioFocusChangeListener;";

function new_object(e: usize, class: usize, sig: string, args: usize[]): usize {
  let values: usize = jni.jvalues(args);
  let obj: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", sig), values);
  jni.free_buffer(values);
  return obj;
}

function attributes(e: usize, purpose: i32, kind: i32): usize {
  let b: usize = new_object(e, jni.find_class(e, ATTRS), "()V", []);
  let usage: usize = jni.call_object(e, b, "setUsage", "(I)L" + ATTRS + ";", [purpose as usize]);
  let content: usize = jni.call_object(e, b, "setContentType", "(I)L" + ATTRS + ";", [kind as usize]);
  return jni.call_object(e, b, "build", "()Landroid/media/AudioAttributes;", []);
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
  let e: usize = android.env(ctx);
  let obj: usize = android.activity_object(ctx);
  let manager: usize = jni.call_object(e, obj, "getSystemService", "(Ljava/lang/String;)Ljava/lang/Object;", [jni.new_string(e, "audio")]);
  if (manager == 0 as usize) {
    return None;
  }
  let listener: usize = new_object(e, jni.load_class(e, obj, class), "()V", []);
  let b: usize = new_object(e, jni.find_class(e, BUILDER), "(I)V", [gain as usize]);
  let with_attrs: usize = jni.call_object(e, b, "setAudioAttributes", "(Landroid/media/AudioAttributes;)L" + BUILDER + ";", [attributes(e, purpose, kind)]);
  let with_listener: usize = jni.call_object(e, b, "setOnAudioFocusChangeListener", "(" + LISTENER_SIG + ")L" + BUILDER + ";", [listener]);
  let request: usize = jni.call_object(e, b, "build", "()Landroid/media/AudioFocusRequest;", []);
  if (request == 0 as usize) {
    return None;
  }
  return Some(Focus { manager: jni.new_global_ref(e, manager), request: jni.new_global_ref(e, request), listener: jni.new_global_ref(e, listener) });
}

function call_int(e: usize, obj: usize, name: string, sig: string, arg: usize): i32 {
  let values: usize = jni.jvalues([arg]);
  let got: i32 = jni.call_int_method_a(e, obj, jni.get_method_id(e, jni.get_object_class(e, obj), name, sig), values);
  jni.free_buffer(values);
  return got;
}

// Asks for the focus. It is false when Android refuses, as during a call.
pub function request(ctx: usize, f: Focus): boolean {
  return call_int(android.env(ctx), f.manager, "requestAudioFocus", "(Landroid/media/AudioFocusRequest;)I", f.request) == 1;
}

// Gives the focus back.
pub function release(ctx: usize, f: Focus): void {
  let released: i32 = call_int(android.env(ctx), f.manager, "abandonAudioFocusRequest", "(Landroid/media/AudioFocusRequest;)I", f.request);
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
