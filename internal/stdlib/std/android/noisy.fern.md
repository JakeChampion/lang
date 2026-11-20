# Android audio that becomes noisy

This module tells a NativeActivity app of `std/android` when the sound is about to come from the speaker of the phone. For example, the person pulls out the headphones.

## Model

Android sends the broadcast `AUDIO_BECOMING_NOISY` when the sound goes from headphones or a Bluetooth device to the speaker of the phone. A music player stops then, so the sound does not come from the speaker. The app declares a @jni class that extends `android.content.BroadcastReceiver`, with a native `onReceive` that calls `received`.

`start` registers one receiver of that class on the application context of the app. The receiver stays registered after the Destroy of an activity, so an app whose loop lives on with no window gets the broadcast too. `received` sends the broadcast to the live loop of the app with `android.tell`. The loop gives it to the app as a `Posted` event. `event` tells if an event is that broadcast.

## Contracts

Call `start` and `stop` on the loop thread. Call `stop` before the loop ends.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<events>>

<<the receiver>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module tells when the sound of the phone becomes noisy.
import "std/android";
import "std/android/noisy/bindings";
import "std/jni";
```

### `events`

The receiver runs on the main thread of the app. It sends a `Posted` event of kind 34.

```fern
<<events>>=
const NOISY: i32 = 34;

// Whether the event `e` of the loop tells that the sound becomes noisy.
pub function event(e: android.Event): boolean {
  let android.Posted(kind, _) = e else {
    return false;
  };
  return kind == NOISY;
}
```

### `the receiver`

```fern
<<the receiver>>=
// The global references of the application context and the receiver.
pub struct Noisy { context: usize, receiver: usize }

const ACTION: string = "android.media.AUDIO_BECOMING_NOISY";

function push_frame(e: usize): boolean {
  if (jni.call1(e, 19, 32 as usize) as i32 != 0) {
    let cleared = jni.exception_clear(e);
    return false;
  }
  return true;
}

function pop_frame(e: usize): void {
  let popped = jni.call1(e, 20, 0 as usize);
}

// The app's receiver has a public no-argument constructor. Load it through
// the app's class loader because the loop runs on a native thread.
function new_receiver(e: usize, c: bindings.Classes, activity: bindings.Context, name: string): Result[bindings.BroadcastReceiver, jni.JavaError] {
  let loader = c.context.get_class_loader(e, activity)?;
  let class = c.class_loader.load_class(e, loader, name)?;
  let instance = c.class.new_instance(e, class)?;
  return Ok(bindings.BroadcastReceiver { ref: instance.ref });
}

// Registers one receiver of the app's @jni class. A successful registration
// may return a null sticky intent.
pub function start(ctx: usize, class: string): Option[Noisy] {
  let e = android.env(ctx);
  if (!push_frame(e)) {
    return None;
  }
  defer pop_frame(e);
  let Ok(c) = bindings.classes(e) else {
    return None;
  };
  defer c.release(e);
  let activity = bindings.Context { ref: android.activity_object(ctx) };
  let Ok(context) = c.context.get_application_context(e, activity) else {
    return None;
  };
  if (context.is_null()) {
    return None;
  }
  let Ok(receiver) = new_receiver(e, c, activity, class) else {
    return None;
  };
  let Ok(filter) = c.intent_filter.new(e, ACTION) else {
    return None;
  };
  let app = jni.new_global_ref(e, context.ref);
  if (app == 0 as usize) {
    let cleared = jni.exception_clear(e);
    return None;
  }
  let kept = jni.new_global_ref(e, receiver.ref);
  if (kept == 0 as usize) {
    let cleared = jni.exception_clear(e);
    jni.delete_global_ref(e, app);
    return None;
  }
  let Ok(_) = c.context.register_receiver(e, context, receiver, filter) else {
    jni.delete_global_ref(e, kept);
    jni.delete_global_ref(e, app);
    return None;
  };
  return Some(Noisy { context: app, receiver: kept });
}

// Unregisters the receiver and releases the references retained by start.
pub function stop(ctx: usize, n: Noisy): void {
  let e = android.env(ctx);
  defer jni.delete_global_ref(e, n.context);
  defer jni.delete_global_ref(e, n.receiver);
  if (!push_frame(e)) {
    return;
  }
  defer pop_frame(e);
  let Ok(c) = bindings.context_class(e) else {
    return;
  };
  defer c.release(e);
  let removed = c.unregister_receiver(e, bindings.Context { ref: n.context }, bindings.BroadcastReceiver { ref: n.receiver });
}

// The onReceive of the app's receiver: `env`, `receiver` and `intent` are
// its arguments. It runs on the main thread, and sends the broadcast to the
// loop thread.
pub function received(e: usize, receiver: usize, intent: usize): void {
  let sent = android.tell(e, NOISY, 0 as usize);
}
```
