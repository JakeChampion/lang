# The start of an Android app

This module starts the loop thread of a NativeActivity app of `std/android`.

## Model

NativeActivity loads the app library and calls its `ANativeActivity_onCreate` export on the UI thread. That export calls `start`, which sets the callbacks of the activity and starts the loop thread at `thread_main`. When a loop of the app runs with no activity, the new activity takes it over, and no new loop starts.

From Android 13 the system gives the back key and the back gesture to the `OnBackInvokedCallback` of the activity, not to the input queue. From Android 15 the window goes under the system bars, the keyboard and the cutout of the display. So `start` registers two objects on the UI thread:

- A `dev.fern.android.Back` is the back callback. The loop gives each back to the app as a `Key` down and a `Key` up of key code 4.
- A `dev.fern.android.Insets` listens to the insets of the window. The loop gives the insets to the app as `Insets`.

The module declares these two Java classes, so the APK of an app that imports this module holds them. A JNI method needs a native target, so a program for the host cannot import this module. The tests of an app on the host use `std/android/ui/sim`.

## Contracts

Call `start` from `ANativeActivity_onCreate`, on the UI thread. The loop thread gets the context of the activity, which it gives to `android.run` or to `app.run` of `std/android/ui/app`.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<start>>

<<callbacks>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module starts the loop thread of a NativeActivity app.
import "std/android";
```

### `start`

`thread_main` is a C function of one usize argument, such as an `@callback` entry.

```fern
<<start>>=
// Sets the callbacks of `activity` and starts the loop thread at
// `thread_main`. Call it from ANativeActivity_onCreate.
pub function start(activity: usize, thread_main: usize): void {
  android.start(activity, thread_main);
}
```

### `callbacks`

The system calls these methods on the UI thread.

```fern
<<callbacks>>=
@jni("dev.fern.android.Back implements android.window.OnBackInvokedCallback", "void onBackInvoked()")
pub function back_invoked(env: usize, thiz: usize): void {
  android.back_invoked(env, thiz);
}

@jni("dev.fern.android.Insets implements android.view.View$OnApplyWindowInsetsListener", "android.view.WindowInsets onApplyWindowInsets(android.view.View, android.view.WindowInsets)")
pub function insets_applied(env: usize, thiz: usize, view: usize, insets: usize): usize {
  return android.insets_applied(env, thiz, view, insets);
}
```
