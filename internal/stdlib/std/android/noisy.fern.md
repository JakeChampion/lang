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

const RECEIVER_SIG: string = "Landroid/content/BroadcastReceiver;";

function new_object(e: usize, class: usize, sig: string, args: usize[]): usize {
  let values: usize = jni.jvalues(args);
  let obj: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", sig), values);
  jni.free_buffer(values);
  return obj;
}

// Registers one receiver of `class`, the Java name of the app's @jni
// subclass of BroadcastReceiver. It is None when Android gives no
// application context or refuses the receiver.
pub function start(ctx: usize, class: string): Option[Noisy] {
  let e: usize = android.env(ctx);
  let obj: usize = android.activity_object(ctx);
  let context: usize = jni.call_object(e, obj, "getApplicationContext", "()Landroid/content/Context;", []);
  if (context == 0 as usize) {
    return None;
  }
  let receiver: usize = new_object(e, jni.load_class(e, obj, class), "()V", []);
  let filter: usize = new_object(e, jni.find_class(e, "android/content/IntentFilter"), "(Ljava/lang/String;)V", [jni.new_string(e, ACTION)]);
  if (receiver == 0 as usize || filter == 0 as usize) {
    return None;
  }
  let sticky: usize = jni.call_object(e, context, "registerReceiver", "(" + RECEIVER_SIG + "Landroid/content/IntentFilter;)Landroid/content/Intent;", [receiver, filter]);
  return Some(Noisy { context: jni.new_global_ref(e, context), receiver: jni.new_global_ref(e, receiver) });
}

// Unregisters the receiver and frees its references.
pub function stop(ctx: usize, n: Noisy): void {
  let e: usize = android.env(ctx);
  let unregistered: boolean = jni.call_void(e, n.context, "unregisterReceiver", "(" + RECEIVER_SIG + ")V", [n.receiver]);
  jni.delete_global_ref(e, n.receiver);
  jni.delete_global_ref(e, n.context);
}

// The onReceive of the app's receiver: `env`, `receiver` and `intent` are
// its arguments. It runs on the main thread, and sends the broadcast to the
// loop thread.
pub function received(e: usize, receiver: usize, intent: usize): void {
  let sent = android.tell(e, NOISY, 0 as usize);
}
```
