# Android NativeActivity apps

This module runs an Android app with no Java code, through `android.app.NativeActivity`.

## Model

NativeActivity loads the app library and calls its `ANativeActivity_onCreate` export on the UI thread. That export calls `start`. `start` sets the activity callbacks and starts a loop thread. The callbacks run on the UI thread. Each callback sends an event to the loop thread through a pipe. Then it waits for the reply of the loop thread.

The loop thread runs `run`. `run` keeps the state of the app as an ordinary Fern value. It gives each event to the `step` function of the app. When `timeout` milliseconds go by with no event, it gives `Tick`. The two threads take turns in the Fern code of the library: `__fern_busy` is a lock, and a C call gives it back.

The app draws the pixels itself: `lock` gives the frame of the window, `std/android/draw` draws rectangles and text on it, and `post` shows it.

## Contracts

The context `ctx` that `start` gives to the loop thread is valid until `run` returns. A `Window` is valid from its `WindowCreated` event until `step` returns for its `WindowDestroyed` event. A `Frame` is valid until `post`.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<C functions>>

<<events>>

<<the UI thread>>

<<the loop thread>>

<<windows>>

<<the foreground service>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module runs an Android app with no Java code, through NativeActivity.
import "std/android/draw";
import "std/jni";
```

### `C functions`

The functions of `libandroid.so` and `libc.so` that the module calls.

```fern
<<C functions>>=
@import("libandroid.so", "ALooper_prepare")
function c_looper_prepare(opts: i32): usize;

@import("libandroid.so", "ALooper_addFd")
function c_looper_add_fd(looper: usize, fd: i32, ident: i32, events: i32, callback: usize, data: usize): i32;

@import("libandroid.so", "ALooper_pollOnce")
function c_looper_poll_once(timeout_ms: i32, out_fd: usize, out_events: usize, out_data: usize): i32;

@import("libandroid.so", "AInputQueue_attachLooper")
function c_queue_attach(queue: usize, looper: usize, ident: i32, callback: usize, data: usize): void;

@import("libandroid.so", "AInputQueue_detachLooper")
function c_queue_detach(queue: usize): void;

@import("libandroid.so", "AInputQueue_getEvent")
function c_queue_get_event(queue: usize, out_event: usize): i32;

@import("libandroid.so", "AInputQueue_preDispatchEvent")
function c_queue_pre_dispatch(queue: usize, event: usize): i32;

@import("libandroid.so", "AInputQueue_finishEvent")
function c_queue_finish_event(queue: usize, event: usize, handled: i32): void;

@import("libandroid.so", "AInputEvent_getType")
function c_event_type(event: usize): i32;

@import("libandroid.so", "AMotionEvent_getAction")
function c_motion_action(event: usize): i32;

@import("libandroid.so", "AMotionEvent_getX")
function c_motion_x(event: usize, pointer: usize): f32;

@import("libandroid.so", "AMotionEvent_getY")
function c_motion_y(event: usize, pointer: usize): f32;

@import("libandroid.so", "AKeyEvent_getKeyCode")
function c_key_code(event: usize): i32;

@import("libandroid.so", "AKeyEvent_getAction")
function c_key_action(event: usize): i32;

@import("libandroid.so", "ANativeWindow_getWidth")
function c_window_width(window: usize): i32;

@import("libandroid.so", "ANativeWindow_getHeight")
function c_window_height(window: usize): i32;

@import("libandroid.so", "ANativeWindow_setBuffersGeometry")
function c_window_set_geometry(window: usize, width: i32, height: i32, format: i32): i32;

@import("libandroid.so", "ANativeWindow_lock")
function c_window_lock(window: usize, buffer: usize, dirty: usize): i32;

@import("libandroid.so", "ANativeWindow_unlockAndPost")
function c_window_post(window: usize): i32;

@import("libandroid.so", "ANativeActivity_finish")
function c_activity_finish(activity: usize): void;

@import("libandroid.so", "ANativeActivity_setWindowFlags")
function c_activity_window_flags(activity: usize, add: i32, remove: i32): void;

@import("libc.so", "pipe2")
function c_pipe2(fds: usize, flags: i32): i32;

@import("libc.so", "read")
function c_read(fd: i32, buf: usize, n: usize): i64;

@import("libc.so", "write")
function c_write(fd: i32, buf: usize, n: usize): i64;

@import("libc.so", "close")
function c_close(fd: i32): i32;

@import("libc.so", "pthread_create")
function c_pthread_create(thread: usize, attr: usize, start: usize, arg: usize): i32;

@import("libc.so", "pthread_detach")
function c_pthread_detach(thread: usize): i32;
```

### `events`

`Touch` has the action of the pointer: 0 down, 1 up, 2 move and 3 cancel. `Key` has the Android key code and the action: 0 down and 1 up. Key code 4 is the back key. `ServiceStopped` comes when the user stops the foreground service from its notification.

```fern
<<events>>=
pub struct Window { ptr: usize }

pub enum Event {
  Start,
  Resume,
  Pause,
  Stop,
  Destroy,
  Focus(boolean),
  WindowCreated(Window),
  WindowResized(Window),
  WindowRedraw(Window),
  WindowDestroyed(Window),
  Touch(i32, f32, f32),
  Key(i32, i32),
  Tick,
  ServiceStopped,
}

const START: i32 = 1;

const RESUME: i32 = 2;

const PAUSE: i32 = 3;

const STOP: i32 = 4;

const DESTROY: i32 = 5;

const FOCUS: i32 = 6;

const WINDOW_CREATED: i32 = 7;

const WINDOW_RESIZED: i32 = 8;

const WINDOW_REDRAW: i32 = 9;

const WINDOW_DESTROYED: i32 = 10;

const INPUT_CREATED: i32 = 11;

const INPUT_DESTROYED: i32 = 12;

const SERVICE_STOPPED: i32 = 13;

function lifecycle_event(kind: i32): Event {
  if (kind == START) {
    return Start;
  }
  if (kind == RESUME) {
    return Resume;
  }
  if (kind == PAUSE) {
    return Pause;
  }
  if (kind == STOP) {
    return Stop;
  }
  return Destroy;
}

function window_event(kind: i32, w: Window): Event {
  if (kind == WINDOW_CREATED) {
    return WindowCreated(w);
  }
  if (kind == WINDOW_RESIZED) {
    return WindowResized(w);
  }
  if (kind == WINDOW_REDRAW) {
    return WindowRedraw(w);
  }
  return WindowDestroyed(w);
}

// The event of a message from the UI thread.
function message_event(kind: i32, arg: usize): Event {
  if (kind == FOCUS) {
    return Focus(arg != 0 as usize);
  }
  if (kind >= WINDOW_CREATED && kind <= WINDOW_DESTROYED) {
    return window_event(kind, Window { ptr: arg });
  }
  if (kind == SERVICE_STOPPED) {
    return ServiceStopped;
  }
  return lifecycle_event(kind);
}
```

### `the UI thread`

The context block that `start` hangs off `ANativeActivity.instance` holds the two pipes, the activity, the thread handle and the JNIEnv of the loop thread: the message pipe at 0 and 4, the reply pipe at 8 and 12, the activity at 16, the thread at 24, and the JNIEnv at 32.

The offsets of the activity are the LP64 layout of `ANativeActivity`: the callbacks at 0 and the instance at 56. The callbacks follow the order of `ANativeActivityCallbacks`.

```fern
<<the UI thread>>=
const CTX_SIZE: i32 = 40;

@callback("on_start")
function on_start_entry(): usize;

@callback("on_resume")
function on_resume_entry(): usize;

@callback("on_pause")
function on_pause_entry(): usize;

@callback("on_stop")
function on_stop_entry(): usize;

@callback("on_destroy")
function on_destroy_entry(): usize;

@callback("on_focus")
function on_focus_entry(): usize;

@callback("on_window_created")
function on_window_created_entry(): usize;

@callback("on_window_resized")
function on_window_resized_entry(): usize;

@callback("on_window_redraw")
function on_window_redraw_entry(): usize;

@callback("on_window_destroyed")
function on_window_destroyed_entry(): usize;

@callback("on_input_created")
function on_input_created_entry(): usize;

@callback("on_input_destroyed")
function on_input_destroyed_entry(): usize;

// Sets the callbacks of `activity` and starts the loop thread at
// `thread_main`, a C function of one usize argument, such as an @callback
// entry. The loop thread gets the context, which it gives to `run`. Call it
// from ANativeActivity_onCreate.
pub function start(activity: usize, thread_main: usize): void {
  unsafe {
    let ctx: usize = __alloc(CTX_SIZE);
    let ok: i32 = c_pipe2(ctx, 524288) | c_pipe2(ctx + 8 as usize, 524288);
    if (ok != 0) {
      c_activity_finish(activity);
      return;
    }
    __store_ptr(ctx + 16 as usize, activity);
    __store_ptr(activity + 56 as usize, ctx);
    let cb: usize = __load_ptr(activity);
    __store_ptr(cb, on_start_entry());
    __store_ptr(cb + 8 as usize, on_resume_entry());
    __store_ptr(cb + 24 as usize, on_pause_entry());
    __store_ptr(cb + 32 as usize, on_stop_entry());
    __store_ptr(cb + 40 as usize, on_destroy_entry());
    __store_ptr(cb + 48 as usize, on_focus_entry());
    __store_ptr(cb + 56 as usize, on_window_created_entry());
    __store_ptr(cb + 64 as usize, on_window_resized_entry());
    __store_ptr(cb + 72 as usize, on_window_redraw_entry());
    __store_ptr(cb + 80 as usize, on_window_destroyed_entry());
    __store_ptr(cb + 88 as usize, on_input_created_entry());
    __store_ptr(cb + 96 as usize, on_input_destroyed_entry());
    if (c_pthread_create(ctx + 24 as usize, 0 as usize, thread_main, ctx) == 0) {
      let ignored: i32 = c_pthread_detach(__load_ptr(ctx + 24 as usize));
    }
  }
}

// Sends one message to the loop thread and waits for its reply.
function send(activity: usize, kind: i32, arg: usize): void {
  unsafe {
    let ctx: usize = __load_ptr(activity + 56 as usize);
    let msg: usize = __alloc(16);
    __store_i32(msg, kind);
    __store_ptr(msg + 8 as usize, arg);
    let sent: i64 = c_write(__load_i32(ctx + 4 as usize), msg, 16 as usize);
    if (sent == 16i64) {
      let got: i64 = c_read(__load_i32(ctx + 8 as usize), msg, 1 as usize);
    }
    __free(msg, 16);
    if (kind == DESTROY) {
      let a: i32 = c_close(__load_i32(ctx + 4 as usize));
      let b: i32 = c_close(__load_i32(ctx + 8 as usize));
      __store_ptr(activity + 56 as usize, 0 as usize);
      __free(ctx, CTX_SIZE);
    }
  }
}

function on_start(activity: usize): void {
  send(activity, START, 0 as usize);
}

function on_resume(activity: usize): void {
  send(activity, RESUME, 0 as usize);
}

function on_pause(activity: usize): void {
  send(activity, PAUSE, 0 as usize);
}

function on_stop(activity: usize): void {
  send(activity, STOP, 0 as usize);
}

function on_destroy(activity: usize): void {
  send(activity, DESTROY, 0 as usize);
}

function on_focus(activity: usize, focused: i32): void {
  send(activity, FOCUS, focused as usize);
}

function on_window_created(activity: usize, window: usize): void {
  send(activity, WINDOW_CREATED, window);
}

function on_window_resized(activity: usize, window: usize): void {
  send(activity, WINDOW_RESIZED, window);
}

function on_window_redraw(activity: usize, window: usize): void {
  send(activity, WINDOW_REDRAW, window);
}

function on_window_destroyed(activity: usize, window: usize): void {
  send(activity, WINDOW_DESTROYED, window);
}

function on_input_created(activity: usize, queue: usize): void {
  send(activity, INPUT_CREATED, queue);
}

function on_input_destroyed(activity: usize, queue: usize): void {
  send(activity, INPUT_DESTROYED, queue);
}
```

### `the loop thread`

The looper of the loop thread has two sources: the message pipe, as ident 1, and the input queue, as ident 2. `ALooper_pollOnce` returns -3 when the timeout goes by. A negative timeout waits with no limit.

```fern
<<the loop thread>>=
const IDENT_MESSAGE: i32 = 1;

const IDENT_INPUT: i32 = 2;

const POLL_TIMEOUT: i32 = 0 - 3;

struct Loop { ctx: usize, looper: usize, queue: usize, done: boolean }

// Runs the loop of the app on the loop thread, from the context that `start`
// gave to it. `step` takes the state with each event and gives the next
// state, so the state can hold values that `step` must consume. `timeout`
// gives the milliseconds to wait for the next event before a Tick. It
// returns the last state after the Destroy event.
pub function run[S](ctx: usize, own state: S, step: (own S, Event) => S, timeout: (S) => i32): S {
  let lp: Loop = Loop { ctx: ctx, looper: c_looper_prepare(1), queue: 0 as usize, done: false };
  let vm: usize = attach(ctx);
  unsafe {
    let added: i32 = c_looper_add_fd(lp.looper, __load_i32(ctx), IDENT_MESSAGE, 1, 0 as usize, 0 as usize);
  }
  while (!lp.done) {
    let ident: i32 = c_looper_poll_once(timeout(state), 0 as usize, 0 as usize, 0 as usize);
    if (ident == IDENT_MESSAGE) {
      let (next_lp, next_state) = message(lp, state, step);
      lp = next_lp;
      state = next_state;
    } else if (ident == IDENT_INPUT) {
      state = input(lp, state, step);
    } else if (ident == POLL_TIMEOUT) {
      state = step(state, Tick);
    }
  }
  detach(vm);
  return state;
}

// Reads one message from the UI thread, handles it, and replies.
function message[S](lp: Loop, own state: S, step: (own S, Event) => S): (Loop, S) {
  unsafe {
    let msg: usize = __alloc(16);
    let got: i64 = c_read(__load_i32(lp.ctx), msg, 16 as usize);
    let kind: i32 = __load_i32(msg);
    let arg: usize = __load_ptr(msg + 8 as usize);
    if (got != 16i64) {
      kind = 0;
    }
    if (kind == INPUT_CREATED) {
      c_queue_attach(arg, lp.looper, IDENT_INPUT, 0 as usize, 0 as usize);
      lp = Loop { ...lp, queue: arg };
    } else if (kind == INPUT_DESTROYED) {
      c_queue_detach(arg);
      lp = Loop { ...lp, queue: 0 as usize };
    } else if (kind > 0) {
      state = step(state, message_event(kind, arg));
    }
    if (kind == DESTROY) {
      lp = Loop { ...lp, done: true };
      let closed: i32 = c_close(__load_i32(lp.ctx));
    }
    let reply_fd: i32 = __load_i32(lp.ctx + 12 as usize);
    if (kind == DESTROY) {
      __store_i32(lp.ctx + 12 as usize, 0 - 1);
    }
    let sent: i64 = c_write(reply_fd, msg, 1 as usize);
    if (kind == DESTROY) {
      let closed_reply: i32 = c_close(reply_fd);
    }
    __free(msg, 16);
    return (lp, state);
  }
}

// Gives each pending input event to `step`.
function input[S](lp: Loop, own state: S, step: (own S, Event) => S): S {
  if (lp.queue == 0 as usize) {
    return state;
  }
  unsafe {
    let slot: usize = __alloc(8);
    while (c_queue_get_event(lp.queue, slot) >= 0) {
      let ev: usize = __load_ptr(slot);
      if (c_queue_pre_dispatch(lp.queue, ev) == 0) {
        let kind: i32 = c_event_type(ev);
        let handled: i32 = 0;
        if (kind == 2) {
          state = step(state, Touch(c_motion_action(ev) & 255, c_motion_x(ev, 0 as usize), c_motion_y(ev, 0 as usize)));
          handled = 1;
        } else if (kind == 1) {
          state = step(state, Key(c_key_code(ev), c_key_action(ev)));
          handled = 1;
        }
        c_queue_finish_event(lp.queue, ev, handled);
      }
    }
    __free(slot, 8);
  }
  return state;
}

// Attaches the loop thread to the JVM of the activity, and keeps its
// JNIEnv in the context at 32. The JavaVM interface has
// AttachCurrentThread at 4 and DetachCurrentThread at 5.
function attach(ctx: usize): usize {
  unsafe {
    let vm: usize = __load_ptr(__load_ptr(ctx + 16 as usize) + 8 as usize);
    let attach_fn: usize = __load_ptr(__load_ptr(vm) + 32 as usize);
    let ignored: usize = __c_call3(attach_fn, vm, ctx + 32 as usize, 0 as usize);
    return vm;
  }
}

function detach(vm: usize): void {
  unsafe {
    let detach_fn: usize = __load_ptr(__load_ptr(vm) + 40 as usize);
    let ignored: usize = __c_call1(detach_fn, vm);
  }
}

// The JNIEnv of the loop thread. The first call before `run` attaches the
// thread to the JVM, so JNI calls work on the loop thread at any time.
pub function env(ctx: usize): usize {
  unsafe {
    if (__load_ptr(ctx + 32 as usize) == 0 as usize) {
      let vm: usize = attach(ctx);
    }
    return __load_ptr(ctx + 32 as usize);
  }
}

// The global reference of the NativeActivity object.
pub function activity_object(ctx: usize): usize {
  unsafe {
    return __load_ptr(__load_ptr(ctx + 16 as usize) + 24 as usize);
  }
}

// The API level of the device.
pub function sdk_version(ctx: usize): i32 {
  unsafe {
    return __load_i32(__load_ptr(ctx + 16 as usize) + 48 as usize);
  }
}

// The app has permission `name`, such as "android.permission.RECORD_AUDIO".
// Call it on the loop thread.
pub function has_permission(ctx: usize, name: string): boolean {
  let e: usize = env(ctx);
  let obj: usize = activity_object(ctx);
  let check: usize = jni.get_method_id(e, jni.get_object_class(e, obj), "checkSelfPermission", "(Ljava/lang/String;)I");
  let s: usize = jni.new_string(e, name);
  let args: usize = jni.jvalues([s]);
  let granted: i32 = jni.call_int_method_a(e, obj, check, args);
  jni.free_buffer(args);
  let deleted: usize = jni.delete_local_ref(e, s);
  return granted == 0;
}

// Asks the user for the permissions `names`. The activity pauses while the
// dialog shows, so check has_permission again at the next Resume. Call it on
// the loop thread.
pub function request_permissions(ctx: usize, names: string[]): void {
  let e: usize = env(ctx);
  let obj: usize = activity_object(ctx);
  let request: usize = jni.get_method_id(e, jni.get_object_class(e, obj), "requestPermissions", "([Ljava/lang/String;I)V");
  let array: usize = jni.string_array(e, names);
  let args: usize = jni.jvalues([array, 1 as usize]);
  let ignored: usize = jni.call_void_method_a(e, obj, request, args);
  jni.free_buffer(args);
  let deleted: usize = jni.delete_local_ref(e, array);
}

// Asks the activity to finish. The Destroy event follows.
pub function finish(ctx: usize): void {
  unsafe {
    c_activity_finish(__load_ptr(ctx + 16 as usize));
  }
}

// Keeps the screen on while the window shows, or stops that.
pub function keep_screen_on(ctx: usize, on: boolean): void {
  unsafe {
    let activity: usize = __load_ptr(ctx + 16 as usize);
    if (on) {
      c_activity_window_flags(activity, 128, 0);
    } else {
      c_activity_window_flags(activity, 0, 128);
    }
  }
}
```

### `windows`

`ANativeWindow_Buffer` has the width at 0, the height at 4, the stride in pixels at 8, and the pixels at 16. It is 48 bytes. `std/android/draw` draws on the frame of a locked window, and this module re-exports its `Frame` and `rgb`.

```fern
<<windows>>=
pub use "std/android/draw".{Frame, rgb};

pub function (w: Window) width(): i32 {
  return c_window_width(w.ptr);
}

pub function (w: Window) height(): i32 {
  return c_window_height(w.ptr);
}

// Locks the pixels of the window for drawing. The frame has width 0 when
// the window cannot give its pixels.
pub function (w: Window) lock(): draw.Frame {
  unsafe {
    let set: i32 = c_window_set_geometry(w.ptr, 0, 0, 1);
    let buffer: usize = __alloc(48);
    if (c_window_lock(w.ptr, buffer, 0 as usize) != 0) {
      __free(buffer, 48);
      return draw.Frame { window: w.ptr, buffer: 0 as usize, bits: 0 as usize, width: 0, height: 0, stride: 0 };
    }
    return draw.Frame { window: w.ptr, buffer: buffer, bits: __load_ptr(buffer + 16 as usize), width: __load_i32(buffer), height: __load_i32(buffer + 4 as usize), stride: __load_i32(buffer + 8 as usize) };
  }
}

// Shows the pixels of the frame and unlocks its window.
pub function post(f: draw.Frame): void {
  if (f.buffer == 0 as usize) {
    return;
  }
  let posted: i32 = c_window_post(f.window);
  unsafe {
    __free(f.buffer, 48);
  }
}
```

### `the foreground service`

A foreground service keeps the process of the app at a high priority while its window is not on the screen, and its notification stays in the status bar. The app declares a @jni class that extends `android.app.Service`, with a native `onBind` that returns null and a native `onStartCommand` that calls `service_command`. `fern -apk` then declares the service, with the types of the FOREGROUND_SERVICE_TYPE permissions of the app.

`start_service` gives the service the `ANativeActivity` in an extra of the intent. The stop action of the notification sends the intent back to the service with that extra, and `service_command` then sends `ServiceStopped` to the loop thread. The activity is not valid after its Destroy event, so the app calls `stop_service` before that.

The module needs API level 26, for the notification channel. From API level 29, `startForeground` takes the types of the manifest.

```fern
<<the foreground service>>=
// The notification of a foreground service: the channel ID and its name,
// the title and the text, and the label of the stop action.
pub struct Notice { channel: string, channel_name: string, title: string, text: string, stop: string }

const SERVICE_EXTRA: string = "dev.fern.android.activity";

const STOP_ACTION: string = "dev.fern.android.STOP";

// START_NOT_STICKY: the system does not start the service again after it
// stops the process.
const NOT_STICKY: i32 = 2;

// android.R.drawable.ic_btn_speak_now and ic_menu_close_clear_cancel.
const ICON: usize = 17301668;

const STOP_ICON: usize = 17301560;

// The class `name` of the app, loaded through the class loader of the
// activity.
function app_class(e: usize, obj: usize, name: string): usize {
  let none: usize = jni.jvalue_alloc(0);
  let loader: usize = jni.call_object_method_a(e, obj, jni.get_method_id(e, jni.get_object_class(e, obj), "getClassLoader", "()Ljava/lang/ClassLoader;"), none);
  let load: usize = jni.get_method_id(e, jni.get_object_class(e, loader), "loadClass", "(Ljava/lang/String;)Ljava/lang/Class;");
  let args: usize = jni.jvalues([jni.new_string(e, name)]);
  let class: usize = jni.call_object_method_a(e, loader, load, args);
  jni.free_buffer(none);
  jni.free_buffer(args);
  return class;
}

// A new intent from `context` to `class`, with the activity in its extra.
function service_intent(e: usize, context: usize, class: usize, activity: usize): usize {
  let intent_class: usize = jni.find_class(e, "android/content/Intent");
  let args: usize = jni.jvalues([context, class]);
  let intent: usize = jni.new_object_a(e, intent_class, jni.get_method_id(e, intent_class, "<init>", "(Landroid/content/Context;Ljava/lang/Class;)V"), args);
  jni.free_buffer(args);
  let put: usize = jni.get_method_id(e, intent_class, "putExtra", "(Ljava/lang/String;J)Landroid/content/Intent;");
  let extra: usize = jni.jvalues([jni.new_string(e, SERVICE_EXTRA), activity]);
  let same: usize = jni.call_object_method_a(e, intent, put, extra);
  jni.free_buffer(extra);
  return intent;
}

// Starts the foreground service `class`, the Java name of the app's @jni
// subclass of Service. It is false when Android refuses, for example while
// the app is in the background. Call it on the loop thread.
pub function start_service(ctx: usize, class: string): boolean {
  let e: usize = env(ctx);
  let obj: usize = activity_object(ctx);
  let activity: usize = 0 as usize;
  unsafe {
    activity = __load_ptr(ctx + 16 as usize);
  }
  let intent: usize = service_intent(e, obj, app_class(e, obj, class), activity);
  let start: usize = jni.get_method_id(e, jni.get_object_class(e, obj), "startForegroundService", "(Landroid/content/Intent;)Landroid/content/ComponentName;");
  let args: usize = jni.jvalues([intent]);
  let started: usize = jni.call_object_method_a(e, obj, start, args);
  jni.free_buffer(args);
  if (jni.exception_occurred(e) != 0 as usize) {
    jni.exception_clear(e);
    return false;
  }
  return started != 0 as usize;
}

// Stops the foreground service `class`, and removes its notification. Call
// it on the loop thread.
pub function stop_service(ctx: usize, class: string): void {
  let e: usize = env(ctx);
  let obj: usize = activity_object(ctx);
  let intent: usize = service_intent(e, obj, app_class(e, obj, class), 0 as usize);
  let stop: usize = jni.get_method_id(e, jni.get_object_class(e, obj), "stopService", "(Landroid/content/Intent;)Z");
  let args: usize = jni.jvalues([intent]);
  let stopped: i32 = jni.call_boolean_method_a(e, obj, stop, args);
  jni.free_buffer(args);
}

function intent_action(e: usize, intent: usize): string {
  let get: usize = jni.get_method_id(e, jni.get_object_class(e, intent), "getAction", "()Ljava/lang/String;");
  let none: usize = jni.jvalue_alloc(0);
  let action: string = jni.string_of(e, jni.call_object_method_a(e, intent, get, none));
  jni.free_buffer(none);
  return action;
}

function intent_activity(e: usize, intent: usize): usize {
  let get: usize = jni.get_method_id(e, jni.get_object_class(e, intent), "getLongExtra", "(Ljava/lang/String;J)J");
  let args: usize = jni.jvalues([jni.new_string(e, SERVICE_EXTRA), 0 as usize]);
  let activity: usize = jni.call_long_method_a(e, intent, get, args);
  jni.free_buffer(args);
  return activity;
}

// The onStartCommand of the app's service: `env`, `service` and `intent`
// are its arguments. It shows the notification `n` and makes the service a
// foreground service. For the stop action of the notification, it stops
// the service and sends ServiceStopped to the loop thread. Return its value
// from onStartCommand.
pub function service_command(e: usize, service: usize, intent: usize, n: Notice): i32 {
  if (intent == 0 as usize) {
    stop_self(e, service);
    return NOT_STICKY;
  }
  let activity: usize = intent_activity(e, intent);
  if (intent_action(e, intent) == STOP_ACTION) {
    stop_self(e, service);
    if (activity != 0 as usize) {
      send(activity, SERVICE_STOPPED, 0 as usize);
    }
    return NOT_STICKY;
  }
  let notification: usize = notification_of(e, service, activity, n);
  start_foreground(e, service, notification);
  return NOT_STICKY;
}

function stop_self(e: usize, service: usize): void {
  let none: usize = jni.jvalue_alloc(0);
  let stopped: usize = jni.call_void_method_a(e, service, jni.get_method_id(e, jni.get_object_class(e, service), "stopSelf", "()V"), none);
  jni.free_buffer(none);
}

function sdk_int(e: usize): i32 {
  let version: usize = jni.find_class(e, "android/os/Build$VERSION");
  return jni.get_static_int_field(e, version, jni.get_static_field_id(e, version, "SDK_INT", "I"));
}

function start_foreground(e: usize, service: usize, notification: usize): void {
  let class: usize = jni.get_object_class(e, service);
  if (sdk_int(e) >= 29) {
    let args: usize = jni.jvalues([1 as usize, notification, (0 - 1) as usize]);
    let started: usize = jni.call_void_method_a(e, service, jni.get_method_id(e, class, "startForeground", "(ILandroid/app/Notification;I)V"), args);
    jni.free_buffer(args);
    return;
  }
  let args: usize = jni.jvalues([1 as usize, notification]);
  let started: usize = jni.call_void_method_a(e, service, jni.get_method_id(e, class, "startForeground", "(ILandroid/app/Notification;)V"), args);
  jni.free_buffer(args);
}

// Makes the channel of `n` with the low importance: no sound and no pop-up.
function make_channel(e: usize, service: usize, n: Notice): void {
  let get: usize = jni.get_method_id(e, jni.get_object_class(e, service), "getSystemService", "(Ljava/lang/String;)Ljava/lang/Object;");
  let name: usize = jni.jvalues([jni.new_string(e, "notification")]);
  let manager: usize = jni.call_object_method_a(e, service, get, name);
  jni.free_buffer(name);
  let channel_class: usize = jni.find_class(e, "android/app/NotificationChannel");
  let args: usize = jni.jvalues([jni.new_string(e, n.channel), jni.new_string(e, n.channel_name), 2 as usize]);
  let channel: usize = jni.new_object_a(e, channel_class, jni.get_method_id(e, channel_class, "<init>", "(Ljava/lang/String;Ljava/lang/CharSequence;I)V"), args);
  jni.free_buffer(args);
  let make: usize = jni.get_method_id(e, jni.get_object_class(e, manager), "createNotificationChannel", "(Landroid/app/NotificationChannel;)V");
  let one: usize = jni.jvalues([channel]);
  let made: usize = jni.call_void_method_a(e, manager, make, one);
  jni.free_buffer(one);
}

// Calls the builder method `name` with the signature `sig` and the jvalues
// `values`. Each one gives the builder back.
function build_step(e: usize, builder: usize, name: string, sig: string, values: usize[]): void {
  let args: usize = jni.jvalues(values);
  let same: usize = jni.call_object_method_a(e, builder, jni.get_method_id(e, jni.get_object_class(e, builder), name, sig), args);
  jni.free_buffer(args);
}

// The intent that the stop action sends to the service.
function stop_intent(e: usize, service: usize, activity: usize): usize {
  let intent: usize = service_intent(e, service, jni.get_object_class(e, service), activity);
  let set: usize = jni.get_method_id(e, jni.get_object_class(e, intent), "setAction", "(Ljava/lang/String;)Landroid/content/Intent;");
  let args: usize = jni.jvalues([jni.new_string(e, STOP_ACTION)]);
  let same: usize = jni.call_object_method_a(e, intent, set, args);
  jni.free_buffer(args);
  let pending_class: usize = jni.find_class(e, "android/app/PendingIntent");
  let get: usize = jni.get_static_method_id(e, pending_class, "getService", "(Landroid/content/Context;ILandroid/content/Intent;I)Landroid/app/PendingIntent;");
  let pargs: usize = jni.jvalues([service, 0 as usize, intent, 201326592 as usize]);
  let pending: usize = jni.call_static_object_method_a(e, pending_class, get, pargs);
  jni.free_buffer(pargs);
  return pending;
}

// The ongoing notification of `n`, with its stop action. FLAG_IMMUTABLE |
// FLAG_UPDATE_CURRENT is 201326592.
function notification_of(e: usize, service: usize, activity: usize, n: Notice): usize {
  make_channel(e, service, n);
  let builder_class: usize = jni.find_class(e, "android/app/Notification$Builder");
  let args: usize = jni.jvalues([service, jni.new_string(e, n.channel)]);
  let builder: usize = jni.new_object_a(e, builder_class, jni.get_method_id(e, builder_class, "<init>", "(Landroid/content/Context;Ljava/lang/String;)V"), args);
  jni.free_buffer(args);
  let ret: string = ")Landroid/app/Notification$Builder;";
  build_step(e, builder, "setContentTitle", "(Ljava/lang/CharSequence;" + ret, [jni.new_string(e, n.title)]);
  build_step(e, builder, "setContentText", "(Ljava/lang/CharSequence;" + ret, [jni.new_string(e, n.text)]);
  build_step(e, builder, "setSmallIcon", "(I" + ret, [ICON]);
  build_step(e, builder, "setOngoing", "(Z" + ret, [1 as usize]);
  build_step(e, builder, "addAction", "(ILjava/lang/CharSequence;Landroid/app/PendingIntent;" + ret, [STOP_ICON, jni.new_string(e, n.stop), stop_intent(e, service, activity)]);
  let none: usize = jni.jvalue_alloc(0);
  let notification: usize = jni.call_object_method_a(e, builder, jni.get_method_id(e, builder_class, "build", "()Landroid/app/Notification;"), none);
  jni.free_buffer(none);
  return notification;
}
```

