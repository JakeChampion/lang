# Android NativeActivity apps

This module runs an Android app with no Java code, through `android.app.NativeActivity`.

## Model

NativeActivity loads the app library and calls its `ANativeActivity_onCreate` export on the UI thread. That export calls `start`. `start` sets the activity callbacks and starts a loop thread. The callbacks run on the UI thread. Each callback sends an event to the loop thread through a pipe. Then it waits for the reply of the loop thread.

The loop thread runs `run`. `run` keeps the state of the app as an ordinary Fern value. It gives each event to the `step` function of the app. When `timeout` milliseconds go by with no event, it gives `Tick`. The two threads take turns in the Fern code of the library: `__fern_busy` is a lock, and a C call gives it back.

The app draws the pixels itself: `lock` gives the frame of the window, `std/android/draw` draws rectangles and text on it, and `post` shows it.

Other modules send their own events to the loop thread with `send`, and the loop gives them to `step` as `Posted` events. `std/android/service` runs a foreground service, `std/android/p2p` uses Wi-Fi Direct, and `std/android/focus` asks for the audio focus. Each one decodes its `Posted` events. An app that uses none of them does not carry their code.

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
```

### `module introduction`

```fern
<<module introduction>>=
// This module runs an Android app with no Java code, through NativeActivity.
import "std/android/draw";
import "std/jni";
import "std/utf8";
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

@import("libandroid.so", "ANativeActivity_showSoftInput")
function c_activity_show_input(activity: usize, flags: i32): void;

@import("libandroid.so", "ANativeActivity_hideSoftInput")
function c_activity_hide_input(activity: usize, flags: i32): void;

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

`Touch` has the action of the pointer: 0 down, 1 up, 2 move and 3 cancel. `Key` has the Android key code and the action: 0 down and 1 up. Key code 4 is the back key. The volume keys and the mute key (24, 25 and 164) come as `Key` events too, and then the system changes the volume.

`Posted` has the kind and the argument of a message that another module sent with `send`. The kinds from 1 to 12 belong to this module, and the kinds from 13 to 18 belong to `std/android/service`, `std/android/p2p` and `std/android/focus`. The kinds from 100 are free for the app.

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
  Posted(i32, usize),
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
  if (kind > INPUT_DESTROYED || kind < START) {
    return Posted(kind, arg);
  }
  return lifecycle_event(kind);
}
```

### `the UI thread`

The context block that `start` hangs off `ANativeActivity.instance` holds the two pipes, the activity, the thread handle, the JNIEnv of the loop thread and the window flags: the message pipe at 0 and 4, the reply pipe at 8 and 12, the activity at 16, the thread at 24, the JNIEnv at 32, and the flags at 40.

Only the UI thread can change the views of the activity. `immersive` sets bit 0 for hidden bars and bit 1 for an explicit request. The focus callback applies the request through the JNIEnv of the UI thread at 16 of the activity. The window gets the focus after it shows, and again each time the user returns to it.

The offsets of the activity are the LP64 layout of `ANativeActivity`: the callbacks at 0, the internal data path at 32, the API level at 48 and the instance at 56. The callbacks follow the order of `ANativeActivityCallbacks`.

```fern
<<the UI thread>>=
const CTX_SIZE: i32 = 48;

const FLAG_IMMERSIVE: i32 = 1;

const FLAG_IMMERSIVE_SET: i32 = 2;

// View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY, FULLSCREEN, HIDE_NAVIGATION,
// LAYOUT_STABLE, LAYOUT_FULLSCREEN and LAYOUT_HIDE_NAVIGATION.
const IMMERSIVE_UI: i32 = 5894;

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

// The system property that names the live ANativeActivity of the process.
// A component that can outlive the activity, such as a service, reads it,
// so it does not reach an activity that Android freed.
const LIVE: string = "dev.fern.android.activity";

// Sets the property LIVE to `value`, or clears it when `value` is "".
function set_live(e: usize, value: string): void {
  let class: usize = jni.find_class(e, "java/lang/System");
  let key: usize = jni.new_string(e, LIVE);
  if (value.len() == 0) {
    let args: usize = jni.jvalues([key]);
    let old: usize = jni.call_static_object_method_a(e, class, jni.get_static_method_id(e, class, "clearProperty", "(Ljava/lang/String;)Ljava/lang/String;"), args);
    jni.free_buffer(args);
  } else {
    let args: usize = jni.jvalues([key, jni.new_string(e, value)]);
    let old: usize = jni.call_static_object_method_a(e, class, jni.get_static_method_id(e, class, "setProperty", "(Ljava/lang/String;Ljava/lang/String;)Ljava/lang/String;"), args);
    jni.free_buffer(args);
  }
  let cleared: usize = jni.exception_clear(e);
}

// The ANativeActivity that runs now, or 0 when there is none: before its
// start, or after its Destroy. Android starts and destroys an activity on the
// UI thread, so call it there, as a service does in onStartCommand.
pub function live_activity(e: usize): usize {
  let class: usize = jni.find_class(e, "java/lang/System");
  let args: usize = jni.jvalues([jni.new_string(e, LIVE)]);
  let got: usize = jni.call_static_object_method_a(e, class, jni.get_static_method_id(e, class, "getProperty", "(Ljava/lang/String;)Ljava/lang/String;"), args);
  jni.free_buffer(args);
  let text: string = jni.string_of(e, got);
  let v: usize = 0 as usize;
  for c in text.as_bytes() {
    if (c < 48u8 || c > 57u8) {
      return 0 as usize;
    }
    v = v * (10 as usize) + (c - 48u8) as usize;
  }
  return v;
}

// The JNIEnv of the UI thread, which ANativeActivity holds.
function ui_env(activity: usize): usize {
  unsafe {
    return __load_ptr(activity + 16 as usize);
  }
}

// Sets the callbacks of `activity` and starts the loop thread at
// `thread_main`, a C function of one usize argument, such as an @callback
// entry. The loop thread gets the context, which it gives to `run`. Call it
// from ANativeActivity_onCreate.
pub function start(activity: usize, thread_main: usize): void {
  unsafe {
    let ctx: usize = __alloc(CTX_SIZE);
    __store_ptr(ctx + 32 as usize, 0 as usize);
    __store_i32(ctx + 40 as usize, 0);
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
  set_live(ui_env(activity), (activity as u64).to_string());
}

// Sends the message `kind` with `arg` to the loop thread of the
// ANativeActivity `activity`, and waits for its reply. Call it on a thread
// other than the loop thread.
pub function send(activity: usize, kind: i32, arg: usize): void {
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
  if (live_activity(ui_env(activity)) == activity) {
    set_live(ui_env(activity), "");
  }
  send(activity, DESTROY, 0 as usize);
}

function on_focus(activity: usize, focused: i32): void {
  if (focused != 0) {
    apply_system_bars(activity);
  }
  send(activity, FOCUS, focused as usize);
}

// Applies the requested bar visibility on the UI thread.
function apply_system_bars(activity: usize): void {
  unsafe {
    let ctx: usize = __load_ptr(activity + 56 as usize);
    if (ctx == 0 as usize || (__load_i32(ctx + 40 as usize) & FLAG_IMMERSIVE_SET) == 0) {
      return;
    }
    let ui: i32 = 0;
    let fullscreen: i32 = 0;
    if ((__load_i32(ctx + 40 as usize) & FLAG_IMMERSIVE) != 0) {
      ui = IMMERSIVE_UI;
      fullscreen = 1024;
    }
    let e: usize = __load_ptr(activity + 16 as usize);
    let obj: usize = __load_ptr(activity + 24 as usize);
    let activity_class: usize = jni.get_object_class(e, obj);
    let none: usize = jni.jvalue_alloc(0);
    let window: usize = jni.call_object_method_a(e, obj, jni.get_method_id(e, activity_class, "getWindow", "()Landroid/view/Window;"), none);
    let window_class: usize = jni.get_object_class(e, window);
    let decor: usize = jni.call_object_method_a(e, window, jni.get_method_id(e, window_class, "getDecorView", "()Landroid/view/View;"), none);
    jni.free_buffer(none);
    let window_args: usize = jni.jvalues([fullscreen as usize, 1024 as usize]);
    let changed: usize = jni.call_void_method_a(e, window, jni.get_method_id(e, window_class, "setFlags", "(II)V"), window_args);
    jni.free_buffer(window_args);
    let decor_class: usize = jni.get_object_class(e, decor);
    let args: usize = jni.jvalues([ui as usize]);
    let ignored: usize = jni.call_void_method_a(e, decor, jni.get_method_id(e, decor_class, "setSystemUiVisibility", "(I)V"), args);
    jni.free_buffer(args);
    if (jni.exception_occurred(e) != 0 as usize) {
      let cleared: usize = jni.exception_clear(e);
    }
    let a: usize = jni.delete_local_ref(e, decor);
    let b: usize = jni.delete_local_ref(e, window);
    let c: usize = jni.delete_local_ref(e, decor_class);
    let d: usize = jni.delete_local_ref(e, window_class);
    let f: usize = jni.delete_local_ref(e, activity_class);
  }
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
          let code: i32 = c_key_code(ev);
          state = step(state, Key(code, c_key_action(ev)));
          if (!system_key(code)) {
            handled = 1;
          }
        }
        c_queue_finish_event(lp.queue, ev, handled);
      }
    }
    __free(slot, 8);
  }
  return state;
}

// The volume keys and the mute key, which the system handles after the app.
function system_key(code: i32): boolean {
  return code == 24 || code == 25 || code == 164;
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

// The action of the Java Intent `intent`.
pub function intent_action(e: usize, intent: usize): string {
  return jni.string_of(e, jni.call_object(e, intent, "getAction", "()Ljava/lang/String;", []));
}

// The API level of the device.
pub function sdk_version(ctx: usize): i32 {
  unsafe {
    return __load_i32(__load_ptr(ctx + 16 as usize) + 48 as usize);
  }
}

// The directory of the private files of the app, or "" when the activity
// gives none. Other apps cannot read the files in it. Android copies it to
// the backups of the user and to a new device: keep secrets in
// `no_backup_dir`.
pub function data_dir(ctx: usize): string {
  let bytes: u8[] = [];
  unsafe {
    let path: usize = __load_ptr(__load_ptr(ctx + 16 as usize) + 32 as usize);
    if (path == 0 as usize) {
      return "";
    }
    let i = 0;
    while (i < 4096 && __load_u8(path + i as usize) != 0) {
      bytes = bytes.append(__load_u8(path + i as usize) as u8);
      i += 1;
    }
  }
  return utf8.from_bytes_lossy(bytes);
}

// The directory of the private files of the app that Android does not copy
// to a backup or to a new device, or "" when there is none. Keep keys here.
// Call it on the loop thread.
pub function no_backup_dir(ctx: usize): string {
  let e: usize = env(ctx);
  let dir: usize = jni.call_object(e, activity_object(ctx), "getNoBackupFilesDir", "()Ljava/io/File;", []);
  if (jni.exception_occurred(e) != 0 as usize || dir == 0 as usize) {
    let cleared: usize = jni.exception_clear(e);
    return "";
  }
  let path: usize = jni.call_object(e, dir, "getAbsolutePath", "()Ljava/lang/String;", []);
  let out: string = jni.string_of(e, path);
  let a: usize = jni.delete_local_ref(e, path);
  let b: usize = jni.delete_local_ref(e, dir);
  return out;
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

// Hides the status bar and the navigation bar while the window has the
// focus, or shows them again the next time the window gets the focus. A
// swipe from the edge shows the bars for a short time.
pub function immersive(ctx: usize, on: boolean): void {
  unsafe {
    let flags: i32 = __load_i32(ctx + 40 as usize) | FLAG_IMMERSIVE_SET;
    if (on) {
      __store_i32(ctx + 40 as usize, flags | FLAG_IMMERSIVE);
    } else {
      __store_i32(ctx + 40 as usize, flags & ~FLAG_IMMERSIVE);
    }
  }
}

// The audio stream that the volume keys change while the window shows, as
// an android.media.AudioManager stream: 3 music, 4 alarm and 5 notification.
pub function volume_stream(ctx: usize, stream: i32): void {
  let e: usize = env(ctx);
  let obj: usize = activity_object(ctx);
  let class: usize = jni.get_object_class(e, obj);
  let args: usize = jni.jvalues([stream as usize]);
  let ignored: usize = jni.call_void_method_a(e, obj, jni.get_method_id(e, class, "setVolumeControlStream", "(I)V"), args);
  jni.free_buffer(args);
  let deleted: usize = jni.delete_local_ref(e, class);
}

// The offset of the local time of the device from UTC, in milliseconds, at
// `unix_ms`. It comes from java.util.TimeZone, so it follows the time zone
// and the zone data of the device. Call it on the loop thread.
pub function utc_offset_ms(ctx: usize, unix_ms: i64): i32 {
  let e: usize = env(ctx);
  let class: usize = jni.find_class(e, "java/util/TimeZone");
  let none: usize = jni.jvalue_alloc(0);
  let zone: usize = jni.call_static_object_method_a(e, class, jni.get_static_method_id(e, class, "getDefault", "()Ljava/util/TimeZone;"), none);
  jni.free_buffer(none);
  let args: usize = jni.jvalues([unix_ms as usize]);
  let offset: i32 = jni.call_int_method_a(e, zone, jni.get_method_id(e, class, "getOffset", "(J)I"), args);
  jni.free_buffer(args);
  if (jni.exception_occurred(e) != 0 as usize) {
    let cleared: usize = jni.exception_clear(e);
    offset = 0;
  }
  let a: usize = jni.delete_local_ref(e, zone);
  let b: usize = jni.delete_local_ref(e, class);
  return offset;
}

// Moves the task of the activity behind the other apps, as the home key
// does. The activity and its foreground service keep running.
pub function to_background(ctx: usize): void {
  let e: usize = env(ctx);
  let obj: usize = activity_object(ctx);
  let args: usize = jni.jvalues([1 as usize]);
  let moved: i32 = jni.call_boolean_method_a(e, obj, jni.get_method_id(e, jni.get_object_class(e, obj), "moveTaskToBack", "(Z)Z"), args);
  jni.free_buffer(args);
}

// Shows the keyboard of the system, or hides it. Its keys come to the loop
// thread as Key events. The activity has no text field, so a keyboard that
// sends only text, not keys, types nothing.
pub function show_keyboard(ctx: usize, show: boolean): void {
  unsafe {
    let activity: usize = __load_ptr(ctx + 16 as usize);
    if (show) {
      // ANATIVEACTIVITY_SHOW_SOFT_INPUT_FORCED
      c_activity_show_input(activity, 2);
    } else {
      c_activity_hide_input(activity, 0);
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
