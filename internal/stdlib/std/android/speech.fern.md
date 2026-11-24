# Android speech recognition

This module recognizes the speech of a person with the `SpeechRecognizer` of Android, for a NativeActivity app of `std/android`.

## Model

Android gives an app a `SpeechRecognizer`, which records from the microphone and gives the words that it hears. The recognition service of the device does the work. A device can have no such service.

A `SpeechRecognizer` takes each call on the main thread of the app, and it calls its listener on that thread. The loop thread does not call it: each function of the loop thread sends a command to the main thread through `std/android/handler`. The main thread runs the commands in their order.

The module declares the class `dev.fern.android.Speech`. It extends `java.util.concurrent.atomic.AtomicReference`, and it implements `android.os.Handler$Callback` and `android.speech.RecognitionListener`. So the APK of an app that imports this module holds that class. `new` makes one object of the class for each recognizer. The object holds its `SpeechRecognizer`, so the main thread finds it with no table.

The listener sends each change to the live loop of the app with `android.tell`. The loop gives it to the app as a `Posted` event. `event` decodes it:

- `Ready` comes when the recognizer listens, after `start`.
- `Partial` has the words that the recognizer hears while the person speaks. Not each recognition service sends it.
- `Words` has the words of the person, after the person stops or after `stop`. The recognizer does not listen after it.
- `Failed` has an `ERROR_` code of `SpeechRecognizer`. The recognizer does not listen after it.

`start` listens in the language of a BCP 47 tag, such as `en-US`. An empty tag is the language of the device.

`stop` tells the recognizer to listen no more, and the words that it heard come as `Words`. `cancel` ends the recognition with no event.

An app needs the permission `RECORD_AUDIO`: give `RECORD_AUDIO` to `-apk-permission`. `allowed` tells whether the person gave it, and `ask` shows the dialog. A `start` with no permission gives `Failed(ERROR_PERMISSIONS)`.

From Android 11 an app sees the recognition service only when its manifest has a query for it. `fern -apk` writes that query for an app that has a class that implements `android.speech.RecognitionListener`.

## Contracts

Call `available`, `allowed`, `ask`, `new`, `start`, `stop`, `cancel`, `close` and `event` on the loop thread. Call `close` one time for each recognizer before Destroy.

`start`, `stop` and `cancel` do not wait for the main thread. They are true when the command went to the main thread.

Give each `Posted` event to `event` one time. `event` frees the text of a `Partial` or a `Words` event.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<events>>

<<the recognizer>>

<<the main thread>>

<<the listener>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module recognizes speech with the SpeechRecognizer of Android.
import "std/android";
import "std/android/handler";
import "std/android/speech/bindings";
import "std/jni";
import "std/log";
import "std/result";
```

### `events`

The listener runs on the main thread of the app. It sends each change as a `Posted` event of a kind from 42 to 45. The argument of `PARTIAL` and of `WORDS` is a buffer of `android.text_buffer`. The argument of `FAILED` is the error code.

```fern
<<events>>=
pub enum Change { Ready, Partial(string), Words(string), Failed(i32) }

const READY: i32 = 42;

const PARTIAL: i32 = 43;

const WORDS: i32 = 44;

const FAILED: i32 = 45;

// The ERROR_ codes of android.speech.SpeechRecognizer.
pub const ERROR_NETWORK_TIMEOUT: i32 = 1;

pub const ERROR_NETWORK: i32 = 2;

pub const ERROR_AUDIO: i32 = 3;

pub const ERROR_SERVER: i32 = 4;

pub const ERROR_CLIENT: i32 = 5;

pub const ERROR_SPEECH_TIMEOUT: i32 = 6;

pub const ERROR_NO_MATCH: i32 = 7;

pub const ERROR_BUSY: i32 = 8;

pub const ERROR_PERMISSIONS: i32 = 9;

// The change of a Posted event of this module, or None for each other
// event.
pub function event(e: android.Event): Option[Change] {
  let android.Posted(kind, arg) = e else {
    return None;
  };
  if (kind == READY) {
    return Some(Ready);
  }
  if (kind == PARTIAL) {
    return Some(Partial(android.taken_text(arg)));
  }
  if (kind == WORDS) {
    return Some(Words(android.taken_text(arg)));
  }
  if (kind == FAILED) {
    return Some(Failed(arg as i32));
  }
  return None;
}
```

### `the recognizer`

The commands are the `what` of a message. `CREATE` has the activity as its object, and `START` has the language tag as a Java string.

```fern
<<the recognizer>>=
// The global reference of the listener object, and the Handler that gives
// it the commands.
pub struct Recognizer { listener: usize, handler: handler.Handler }

const CLASS: string = "dev.fern.android.Speech";

const RECORD: string = "android.permission.RECORD_AUDIO";

const CREATE: i32 = 1;

const START: i32 = 2;

const STOP: i32 = 3;

const CANCEL: i32 = 4;

const RELEASE: i32 = 5;

// Whether the person gave the app the permission to record.
pub function allowed(ctx: usize): boolean {
  return android.has_permission(ctx, RECORD);
}

// Shows the dialog of the permission. The answer comes with the next Resume.
pub function ask(ctx: usize): void {
  if (!allowed(ctx)) {
    android.request_permissions(ctx, [RECORD]);
  }
}

// Whether the device has a recognition service that the app sees.
pub function available(ctx: usize): boolean {
  let e = android.env(ctx);
  if (!jni.push_local_frame(e, 32)) {
    return false;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  let Ok(c) = bindings.speech_recognizer_class(e) else {
    return false;
  };
  defer c.release(e);
  let Ok(found) = c.is_recognition_available(e, bindings.Context { ref: android.activity_object(ctx) }) else {
    return false;
  };
  return found;
}

function made(e: usize, activity: bindings.Context): Result[Recognizer, jni.JavaError] {
  let c = bindings.classes(e)?;
  defer c.release(e);
  let loader = c.context.get_class_loader(e, activity)?;
  let class = c.class_loader.load_class(e, loader, CLASS)?;
  let listener = c.class.new_instance(e, class)?;
  let commands = handler.new(e, listener.ref)?;
  let kept = jni.new_global_ref(e, listener.ref);
  if (kept == 0 as usize) {
    commands.close(e);
    jni.check(e)?;
    return Err(jni.JavaError { text: "speech listener reference is null" });
  }
  return Ok(Recognizer { listener: kept, handler: commands });
}

function forget(e: usize, r: Recognizer): void {
  r.handler.close(e);
  jni.delete_global_ref(e, r.listener);
}

// A recognizer, or None when the device has no recognition service. The
// main thread makes its SpeechRecognizer with the activity as its context.
pub function new(ctx: usize): Option[Recognizer] {
  if (!available(ctx)) {
    return None;
  }
  let e = android.env(ctx);
  if (!jni.push_local_frame(e, 32)) {
    return None;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  let activity = android.activity_object(ctx);
  let Ok(r) = made(e, bindings.Context { ref: activity }) else {
    return None;
  };
  if (!r.handler.send(e, CREATE, 0, activity)) {
    forget(e, r);
    return None;
  }
  return Some(r);
}

function send(ctx: usize, r: Recognizer, what: i32): boolean {
  let e = android.env(ctx);
  if (!jni.push_local_frame(e, 32)) {
    return false;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  return r.handler.send(e, what, 0, 0 as usize);
}

// Starts to listen for speech in the language of the BCP 47 tag
// `language`, or in the language of the device for an empty tag.
pub function start(ctx: usize, r: Recognizer, language: string): boolean {
  let e = android.env(ctx);
  if (!jni.push_local_frame(e, 32)) {
    return false;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  let tag = jni.new_string(e, language);
  if (jni.check(e).is_err() || tag == 0 as usize) {
    return false;
  }
  return r.handler.send(e, START, 0, tag);
}

// Tells the recognizer to listen no more. The words that it heard come
// as Words.
pub function stop(ctx: usize, r: Recognizer): boolean {
  return send(ctx, r, STOP);
}

// Ends the recognition with no event.
pub function cancel(ctx: usize, r: Recognizer): boolean {
  return send(ctx, r, CANCEL);
}

// Destroys the SpeechRecognizer on the main thread, and deletes the
// references of the loop thread. Call it before Destroy.
pub function close(ctx: usize, r: Recognizer): void {
  let sent = send(ctx, r, RELEASE);
  forget(android.env(ctx), r);
}
```

### `the main thread`

`handle` runs on the main thread, in the native `handleMessage` of the listener. It finds the classes in a local frame, and lets them go at its end. A command before `CREATE` or after `RELEASE` does nothing.

The intent of `START` asks for free speech, and for the words while the person speaks.

```fern
<<the main thread>>=
const ACTION: string = "android.speech.action.RECOGNIZE_SPEECH";

const EXTRA_MODEL: string = "android.speech.extra.LANGUAGE_MODEL";

const FREE_FORM: string = "free_form";

const EXTRA_PARTIAL: string = "android.speech.extra.PARTIAL_RESULTS";

const EXTRA_LANGUAGE: string = "android.speech.extra.LANGUAGE";

// The key of the words in the Bundle of onResults and onPartialResults.
const RESULTS: string = "results_recognition";

// Runs one command. It gives 1, as the message needs no other handler.
function handle(e: usize, listener: usize, msg: usize): u8 {
  if (!jni.push_local_frame(e, 32)) {
    return 0u8;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  match (bindings.classes(e)) {
    Ok(c) => {
      if let Err(err) = command(e, c, listener, msg) {
        log.warn("speech: a command failed: " + err.message());
      }
      c.release(e);
    },
    Err(err) => {
      log.warn("speech: no classes: " + err.message());
    }
  }
  return 1u8;
}

function command(e: usize, c: bindings.Classes, listener: usize, msg: usize): Result[void, jni.JavaError] {
  let m = handler.command(e, msg)?;
  let box = bindings.AtomicReference { ref: listener };
  if (m.what == CREATE) {
    return create(e, c, box, bindings.Context { ref: m.obj });
  }
  let held = c.atomic_reference.get(e, box)?;
  if (held.ref == 0 as usize) {
    return Ok(());
  }
  let r = bindings.SpeechRecognizer { ref: held.ref };
  if (m.what == START) {
    return listen(e, c, r, jni.string_of(e, m.obj));
  }
  if (m.what == STOP) {
    return c.speech_recognizer.stop_listening(e, r);
  }
  if (m.what == CANCEL) {
    return c.speech_recognizer.cancel(e, r);
  }
  if (m.what == RELEASE) {
    c.atomic_reference.set(e, box, jni.Object { ref: 0 as usize })?;
    return c.speech_recognizer.destroy(e, r);
  }
  return Ok(());
}

// Makes the SpeechRecognizer of the listener `box`, which holds it.
function create(e: usize, c: bindings.Classes, box: bindings.AtomicReference, activity: bindings.Context): Result[void, jni.JavaError] {
  let r = c.speech_recognizer.create_speech_recognizer(e, activity)?;
  if (r.is_null()) {
    return Err(jni.JavaError { text: "no speech recognizer" });
  }
  if let Err(err) = c.speech_recognizer.set_recognition_listener(e, r, bindings.RecognitionListener { ref: box.ref }) {
    let gone = c.speech_recognizer.destroy(e, r);
    return Err(err);
  }
  return c.atomic_reference.set(e, box, r.object());
}

function listen(e: usize, c: bindings.Classes, r: bindings.SpeechRecognizer, language: string): Result[void, jni.JavaError] {
  let intent = c.intent.new(e, ACTION)?;
  let modelled = c.intent.put_extra_string_string(e, intent, EXTRA_MODEL, FREE_FORM)?;
  let partial = c.intent.put_extra_string_boolean(e, intent, EXTRA_PARTIAL, true)?;
  if (language.len() > 0) {
    let spoken = c.intent.put_extra_string_string(e, intent, EXTRA_LANGUAGE, language)?;
  }
  return c.speech_recognizer.start_listening(e, r, intent);
}

// The first words of the Bundle `results`, which are the most probable
// ones, or "" when it has none.
function first(e: usize, results: usize): Result[string, jni.JavaError] {
  let bundles = bindings.bundle_class(e)?;
  defer bundles.release(e);
  let list = bundles.get_string_array_list(e, bindings.Bundle { ref: results }, RESULTS)?;
  if (list.is_null()) {
    return Ok("");
  }
  let lists = bindings.array_list_class(e)?;
  defer lists.release(e);
  if (lists.size(e, list)? == 0) {
    return Ok("");
  }
  let words = lists.get(e, list, 0)?;
  return Ok(jni.string_of(e, words.ref));
}

// The first words of the Bundle `results`, in a local frame.
function heard(e: usize, results: usize): string {
  if (results == 0 as usize || !jni.push_local_frame(e, 32)) {
    return "";
  }
  defer jni.pop_local_frame(e, 0 as usize);
  return first(e, results).unwrap_or("");
}

// Sends `text` to the loop thread as the event `kind`.
function post(e: usize, kind: i32, text: string): void {
  let buf = android.text_buffer(text);
  if (!android.tell(e, kind, buf)) {
    let dropped = android.taken_text(buf);
  }
}
```

### `the listener`

The system calls these methods on the main thread. A result with no words is `Failed(ERROR_NO_MATCH)`.

```fern
<<the listener>>=
@jni("dev.fern.android.Speech extends java.util.concurrent.atomic.AtomicReference implements android.os.Handler$Callback, android.speech.RecognitionListener", "boolean handleMessage(android.os.Message)")
pub function handle_message(env: usize, thiz: usize, msg: usize): u8 {
  return handle(env, thiz, msg);
}

@jni("dev.fern.android.Speech", "void onReadyForSpeech(android.os.Bundle)")
pub function on_ready(env: usize, thiz: usize, params: usize): void {
  let sent = android.tell(env, READY, 0 as usize);
}

@jni("dev.fern.android.Speech", "void onBeginningOfSpeech()")
pub function on_beginning(env: usize, thiz: usize): void {}

@jni("dev.fern.android.Speech", "void onRmsChanged(float)")
pub function on_rms(env: usize, thiz: usize, level: f32): void {}

@jni("dev.fern.android.Speech", "void onBufferReceived(byte[])")
pub function on_buffer(env: usize, thiz: usize, buffer: usize): void {}

@jni("dev.fern.android.Speech", "void onEndOfSpeech()")
pub function on_end(env: usize, thiz: usize): void {}

@jni("dev.fern.android.Speech", "void onError(int)")
pub function on_error(env: usize, thiz: usize, code: i32): void {
  let sent = android.tell(env, FAILED, code as usize);
}

@jni("dev.fern.android.Speech", "void onResults(android.os.Bundle)")
pub function on_results(env: usize, thiz: usize, results: usize): void {
  let text = heard(env, results);
  if (text.len() == 0) {
    let sent = android.tell(env, FAILED, ERROR_NO_MATCH as usize);
    return;
  }
  post(env, WORDS, text);
}

@jni("dev.fern.android.Speech", "void onPartialResults(android.os.Bundle)")
pub function on_partial(env: usize, thiz: usize, results: usize): void {
  let text = heard(env, results);
  if (text.len() > 0) {
    post(env, PARTIAL, text);
  }
}

@jni("dev.fern.android.Speech", "void onEvent(int, android.os.Bundle)")
pub function on_event(env: usize, thiz: usize, kind: i32, params: usize): void {}
```
