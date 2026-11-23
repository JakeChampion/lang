# Commands for the main thread

This module sends commands from the loop thread to the main thread of an Android app, for a NativeActivity app of `std/android`.

## Model

Some Java objects take each call on the thread of one `Looper`. A `SpeechRecognizer` takes each call on the main thread, and an ExoPlayer takes each call on the thread that made it. The loop thread of `std/android` is not the main thread, so it does not call such an object.

A `Handler` on the main `Looper` moves the calls. The loop thread sends a command to the `Handler` as an `android.os.Message`. The main thread runs the commands in their order.

The module that owns the Java object declares a @jni class that implements `android.os.Handler$Callback`. `new` makes a `Handler` that gives each message to one object of that class. The native `handleMessage` of the class runs on the main thread. It reads the command with `command`, and calls the Java object.

A command has a number `what`, one number `arg` and one object `obj`. The owner module decides what each `what` does.

The main thread sends each result to the loop thread with `android.tell`. The loop gives the result to the app as a `Posted` event.

## Contracts

Call `new`, `send` and `close` on the loop thread, with the JNIEnv of that thread. Call `command` on the main thread, in the native `handleMessage`, with the JNIEnv of that call.

`send` does not wait for the main thread. A command can fail on the main thread with no answer to the loop thread.

A message holds its object until the main thread ran its command. Thus the caller of `send` can delete its own reference to the object after the call.

`close` deletes the reference of the loop thread. The commands that wait on the main thread still run after it.

The object that `command` gives is a local reference of the main thread.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<the handler>>

<<the main thread>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module sends commands from the loop thread to the main thread.
import "std/android/handler/bindings";
import "std/jni";
import "std/result";
```

### `the handler`

```fern
<<the handler>>=
// A global reference to a Handler on the main Looper, or 0.
pub struct Handler { ref: usize }

pub function (h: Handler) live(): boolean {
  return h.ref != 0 as usize;
}

// A Handler on the main Looper that gives each message to `callback`, an
// object of a @jni class that implements android.os.Handler$Callback.
pub function new(e: usize, callback: usize): Result[Handler, jni.JavaError] {
  let loopers = bindings.looper_class(e)?;
  defer loopers.release(e);
  let main = loopers.get_main_looper(e)?;
  defer jni.delete_local_ref(e, main.ref);
  let handlers = bindings.handler_class(e)?;
  defer handlers.release(e);
  let made = handlers.new(e, main, jni.Object { ref: callback })?;
  defer jni.delete_local_ref(e, made.ref);
  let kept = jni.new_global_ref(e, made.ref);
  if (kept == 0 as usize) {
    jni.check(e)?;
    return Err(jni.JavaError { text: "handler reference is null" });
  }
  return Ok(Handler { ref: kept });
}

// Sends the command `what` with the number `arg` and the object `obj` to
// the main thread. It is false when the command did not go.
pub function (h: Handler) send(e: usize, what: i32, arg: i32, obj: usize): boolean {
  if (!h.live()) {
    return false;
  }
  let Ok(handlers) = bindings.handler_class(e) else {
    return false;
  };
  defer handlers.release(e);
  let Ok(messages) = bindings.message_class(e) else {
    return false;
  };
  defer messages.release(e);
  let Ok(msg) = handlers.obtain_message(e, bindings.Handler { ref: h.ref }, what, arg, 0, jni.Object { ref: obj }) else {
    return false;
  };
  defer jni.delete_local_ref(e, msg.ref);
  return messages.send_to_target(e, msg).is_ok();
}

// Deletes the reference of the loop thread. Do not use `h` after this call.
pub function (h: Handler) close(e: usize): void {
  if (h.live()) {
    jni.delete_global_ref(e, h.ref);
  }
}
```

### `the main thread`

```fern
<<the main thread>>=
// One command: its number, its argument and a local reference to its
// object, or 0.
pub struct Command { what: i32, arg: i32, obj: usize }

// The command of `msg`, the android.os.Message of a native handleMessage.
pub function command(e: usize, msg: usize): Result[Command, jni.JavaError] {
  let messages = bindings.message_class(e)?;
  defer messages.release(e);
  let m = bindings.Message { ref: msg };
  let what = messages.what(e, m)?;
  let arg = messages.arg1(e, m)?;
  let obj = messages.obj(e, m)?;
  return Ok(Command { what: what, arg: arg, obj: obj.ref });
}
```
