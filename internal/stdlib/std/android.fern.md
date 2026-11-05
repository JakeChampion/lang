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

<<Wi-Fi Direct>>
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

`Touch` has the action of the pointer: 0 down, 1 up, 2 move and 3 cancel. `Key` has the Android key code and the action: 0 down and 1 up. Key code 4 is the back key. The volume keys and the mute key (24, 25 and 164) come as `Key` events too, and then the system changes the volume. `ServiceStopped` comes when the user stops the foreground service from its notification, and `ServiceAction` when the user taps the action of the app in it. `P2pEnabled`, `P2pPeers` and `P2pGroup` come from Wi-Fi Direct.

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
  ServiceAction,
  P2pEnabled(boolean),
  P2pPeers(P2pPeer[]),
  P2pGroup(P2pGroup),
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

const P2P_STATE: i32 = 14;

const P2P_PEERS: i32 = 15;

const P2P_GROUP: i32 = 16;

const SERVICE_ACTION: i32 = 17;

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
  if (kind == SERVICE_ACTION) {
    return ServiceAction;
  }
  if (kind >= P2P_STATE) {
    return p2p_event(kind, arg);
  }
  return lifecycle_event(kind);
}
```

### `the UI thread`

The context block that `start` hangs off `ANativeActivity.instance` holds the two pipes, the activity, the thread handle, the JNIEnv of the loop thread and the window flags: the message pipe at 0 and 4, the reply pipe at 8 and 12, the activity at 16, the thread at 24, the JNIEnv at 32, and the flags at 40.

Only the UI thread can change the views of the activity. So `immersive` sets bit 0 of the flags, and the focus callback hides the system bars on the UI thread, through the JNIEnv of that thread at 16 of the activity. The window gets the focus after it shows, and again each time the user comes back to it.

The offsets of the activity are the LP64 layout of `ANativeActivity`: the callbacks at 0, the internal data path at 32, the API level at 48 and the instance at 56. The callbacks follow the order of `ANativeActivityCallbacks`.

```fern
<<the UI thread>>=
const CTX_SIZE: i32 = 48;

const FLAG_IMMERSIVE: i32 = 1;

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
  if (focused != 0) {
    hide_bars(activity);
  }
  send(activity, FOCUS, focused as usize);
}

// Hides the status bar and the navigation bar when the app asked for it. It
// runs on the UI thread.
function hide_bars(activity: usize): void {
  unsafe {
    let ctx: usize = __load_ptr(activity + 56 as usize);
    if (ctx == 0 as usize || (__load_i32(ctx + 40 as usize) & FLAG_IMMERSIVE) == 0) {
      return;
    }
    let e: usize = __load_ptr(activity + 16 as usize);
    let obj: usize = __load_ptr(activity + 24 as usize);
    let none: usize = jni.jvalue_alloc(0);
    let window: usize = jni.call_object_method_a(e, obj, jni.get_method_id(e, jni.get_object_class(e, obj), "getWindow", "()Landroid/view/Window;"), none);
    let decor: usize = jni.call_object_method_a(e, window, jni.get_method_id(e, jni.get_object_class(e, window), "getDecorView", "()Landroid/view/View;"), none);
    jni.free_buffer(none);
    let args: usize = jni.jvalues([IMMERSIVE_UI as usize]);
    let ignored: usize = jni.call_void_method_a(e, decor, jni.get_method_id(e, jni.get_object_class(e, decor), "setSystemUiVisibility", "(I)V"), args);
    jni.free_buffer(args);
    if (jni.exception_occurred(e) != 0 as usize) {
      let cleared: usize = jni.exception_clear(e);
    }
    let a: usize = jni.delete_local_ref(e, decor);
    let b: usize = jni.delete_local_ref(e, window);
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

// The API level of the device.
pub function sdk_version(ctx: usize): i32 {
  unsafe {
    return __load_i32(__load_ptr(ctx + 16 as usize) + 48 as usize);
  }
}

// The directory of the private files of the app, or "" when the activity
// gives none. Other apps cannot read the files in it.
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
    let flags: i32 = __load_i32(ctx + 40 as usize);
    if (on) {
      __store_i32(ctx + 40 as usize, flags | FLAG_IMMERSIVE);
    } else {
      __store_i32(ctx + 40 as usize, flags & 0 - 1 - FLAG_IMMERSIVE);
    }
  }
}

// The audio stream that the volume keys change while the window shows, as
// an android.media.AudioManager stream: 3 music, 4 alarm and 5 notification.
pub function volume_stream(ctx: usize, stream: i32): void {
  let e: usize = env(ctx);
  let obj: usize = activity_object(ctx);
  let args: usize = jni.jvalues([stream as usize]);
  let ignored: usize = jni.call_void_method_a(e, obj, jni.get_method_id(e, jni.get_object_class(e, obj), "setVolumeControlStream", "(I)V"), args);
  jni.free_buffer(args);
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

### `the foreground service`

A foreground service keeps the process of the app at a high priority while its window is not on the screen, and its notification stays in the status bar. The app declares a @jni class that extends `android.app.Service`, with a native `onBind` that returns null and a native `onStartCommand` that calls `service_command`. `fern -apk` then declares the service, with the types of the FOREGROUND_SERVICE_TYPE permissions of the app.

`start_service` gives the service the `ANativeActivity` in an extra of the intent. The stop action of the notification sends the intent back to the service with that extra, and `service_command` then sends `ServiceStopped` to the loop thread. The activity is not valid after its Destroy event, so the app calls `stop_service` before that.

The module needs API level 26, for the notification channel. From API level 29, `startForeground` takes the types of the manifest.

```fern
<<the foreground service>>=
// The notification of a foreground service: the channel ID and its name,
// the title and the text, the label of the action of the app or "" for no
// action, and the label of the stop action.
pub struct Notice { channel: string, channel_name: string, title: string, text: string, action: string, stop: string }

const SERVICE_EXTRA: string = "dev.fern.android.activity";

const STOP_ACTION: string = "dev.fern.android.STOP";

const APP_ACTION: string = "dev.fern.android.ACTION";

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

// Shows the notification `n` in place of the notification of the running
// foreground service `class`. Call it on the loop thread.
pub function update_notice(ctx: usize, class: string, n: Notice): void {
  let e: usize = env(ctx);
  let obj: usize = activity_object(ctx);
  let activity: usize = 0 as usize;
  unsafe {
    activity = __load_ptr(ctx + 16 as usize);
  }
  let notification: usize = notification_of(e, obj, app_class(e, obj, class), activity, n);
  let manager: usize = call_object(e, obj, "getSystemService", "(Ljava/lang/String;)Ljava/lang/Object;", [jni.new_string(e, "notification")]);
  let posted: boolean = call_void(e, manager, "notify", "(ILandroid/app/Notification;)V", [1 as usize, notification]);
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
// the service and sends ServiceStopped to the loop thread. For the action of
// the app, it sends ServiceAction. Return its value from onStartCommand.
pub function service_command(e: usize, service: usize, intent: usize, n: Notice): i32 {
  if (intent == 0 as usize) {
    stop_self(e, service);
    return NOT_STICKY;
  }
  let activity: usize = intent_activity(e, intent);
  let action: string = intent_action(e, intent);
  if (action == STOP_ACTION) {
    stop_self(e, service);
    if (activity != 0 as usize) {
      send(activity, SERVICE_STOPPED, 0 as usize);
    }
    return NOT_STICKY;
  }
  if (action == APP_ACTION) {
    if (activity != 0 as usize) {
      send(activity, SERVICE_ACTION, 0 as usize);
    }
    return NOT_STICKY;
  }
  let notification: usize = notification_of(e, service, jni.get_object_class(e, service), activity, n);
  start_foreground(e, service, notification);
  return NOT_STICKY;
}

// The global references of a partial wake lock and a Wi-Fi lock.
pub struct Awake { wake: usize, wifi: usize }

// Keeps the CPU and the Wi-Fi radio awake while the screen is off, so a
// socket keeps its datagrams on time. It needs the WAKE_LOCK permission.
// Call it on the loop thread, and release_awake when the work stops.
pub function stay_awake(ctx: usize, tag: string): Awake {
  let e: usize = env(ctx);
  let obj: usize = activity_object(ctx);
  let service: string = "(Ljava/lang/String;)Ljava/lang/Object;";
  let power: usize = call_object(e, obj, "getSystemService", service, [jni.new_string(e, "power")]);
  // PowerManager.PARTIAL_WAKE_LOCK
  let wake: usize = held(e, call_object(e, power, "newWakeLock", "(ILjava/lang/String;)Landroid/os/PowerManager$WakeLock;", [1 as usize, jni.new_string(e, tag)]));
  let app: usize = call_object(e, obj, "getApplicationContext", "()Landroid/content/Context;", []);
  let wifi_manager: usize = call_object(e, app, "getSystemService", service, [jni.new_string(e, "wifi")]);
  let wifi: usize = 0 as usize;
  if (wifi_manager != 0 as usize) {
    // WifiManager.WIFI_MODE_FULL_HIGH_PERF
    wifi = held(e, call_object(e, wifi_manager, "createWifiLock", "(ILjava/lang/String;)Landroid/net/wifi/WifiManager$WifiLock;", [3 as usize, jni.new_string(e, tag)]));
  }
  return Awake { wake: wake, wifi: wifi };
}

// Acquires the lock `lock`, and gives a global reference to it, or 0.
function held(e: usize, lock: usize): usize {
  if (lock == 0 as usize || !call_void(e, lock, "acquire", "()V", [])) {
    return 0 as usize;
  }
  return jni.new_global_ref(e, lock);
}

pub function release_awake(ctx: usize, a: Awake): void {
  let e: usize = env(ctx);
  for lock in [a.wake, a.wifi] {
    if (lock != 0 as usize) {
      let released: boolean = call_void(e, lock, "release", "()V", []);
      jni.delete_global_ref(e, lock);
    }
  }
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

// The intent that a button of the notification sends to the service `class`
// with the action `action`.
function action_intent(e: usize, context: usize, class: usize, activity: usize, action: string): usize {
  let intent: usize = service_intent(e, context, class, activity);
  let set: usize = jni.get_method_id(e, jni.get_object_class(e, intent), "setAction", "(Ljava/lang/String;)Landroid/content/Intent;");
  let args: usize = jni.jvalues([jni.new_string(e, action)]);
  let same: usize = jni.call_object_method_a(e, intent, set, args);
  jni.free_buffer(args);
  let pending_class: usize = jni.find_class(e, "android/app/PendingIntent");
  let get: usize = jni.get_static_method_id(e, pending_class, "getService", "(Landroid/content/Context;ILandroid/content/Intent;I)Landroid/app/PendingIntent;");
  // FLAG_IMMUTABLE | FLAG_UPDATE_CURRENT
  let pargs: usize = jni.jvalues([context, 0 as usize, intent, 201326592 as usize]);
  let pending: usize = jni.call_static_object_method_a(e, pending_class, get, pargs);
  jni.free_buffer(pargs);
  return pending;
}

// The ongoing notification of `n`, with its stop action. FLAG_IMMUTABLE |
// FLAG_UPDATE_CURRENT is 201326592.
// The notification `n` of the service `class`, built in `context`: the
// service, or the activity.
function notification_of(e: usize, context: usize, class: usize, activity: usize, n: Notice): usize {
  make_channel(e, context, n);
  let builder_class: usize = jni.find_class(e, "android/app/Notification$Builder");
  let args: usize = jni.jvalues([context, jni.new_string(e, n.channel)]);
  let builder: usize = jni.new_object_a(e, builder_class, jni.get_method_id(e, builder_class, "<init>", "(Landroid/content/Context;Ljava/lang/String;)V"), args);
  jni.free_buffer(args);
  let ret: string = ")Landroid/app/Notification$Builder;";
  build_step(e, builder, "setContentTitle", "(Ljava/lang/CharSequence;" + ret, [jni.new_string(e, n.title)]);
  build_step(e, builder, "setContentText", "(Ljava/lang/CharSequence;" + ret, [jni.new_string(e, n.text)]);
  build_step(e, builder, "setSmallIcon", "(I" + ret, [ICON]);
  build_step(e, builder, "setOngoing", "(Z" + ret, [1 as usize]);
  if (n.action.len() > 0) {
    build_step(e, builder, "addAction", "(ILjava/lang/CharSequence;Landroid/app/PendingIntent;" + ret, [ICON, jni.new_string(e, n.action), action_intent(e, context, class, activity, APP_ACTION)]);
  }
  build_step(e, builder, "addAction", "(ILjava/lang/CharSequence;Landroid/app/PendingIntent;" + ret, [STOP_ICON, jni.new_string(e, n.stop), action_intent(e, context, class, activity, STOP_ACTION)]);
  let none: usize = jni.jvalue_alloc(0);
  let notification: usize = jni.call_object_method_a(e, builder, jni.get_method_id(e, builder_class, "build", "()Landroid/app/Notification;"), none);
  jni.free_buffer(none);
  return notification;
}
```

### `Wi-Fi Direct`

Wi-Fi Direct connects two phones with no access point: one phone becomes the group owner, and the other connects to it. `WifiP2pManager` reports the state, the peers and the group through broadcasts. The app declares a @jni class that extends `android.content.BroadcastReceiver`, with a native `onReceive` that calls `p2p_received`. `p2p_start` registers it, and stores the `ANativeActivity` in the static field `fern` of the class, so that `p2p_received` can send the events to the loop thread.

Discovery needs `NEARBY_WIFI_DEVICES` from API level 33, and `ACCESS_FINE_LOCATION` before that. The commands report no result: the broadcasts that follow give the state. `connect` makes Android ask the user of the other phone to accept the invitation.

A `P2pPeer` has the status of `WifiP2pDevice`: 0 connected, 1 invited, 2 failed, 3 available and 4 unavailable.

```fern
<<Wi-Fi Direct>>=
pub struct P2pPeer { name: string, address: string, status: i32 }

// A group is formed when the two phones are connected. The owner address
// is the IP address of the group owner.
pub struct P2pGroup { formed: boolean, owner: boolean, owner_address: string }

// The global references of the manager, the channel and the receiver.
pub struct P2p { manager: usize, channel: usize, receiver: usize }

const P2P_ACTIONS: string[] = ["android.net.wifi.p2p.STATE_CHANGED", "android.net.wifi.p2p.PEERS_CHANGED", "android.net.wifi.p2p.CONNECTION_STATE_CHANGE"];

const CHANNEL_SIG: string = "Landroid/net/wifi/p2p/WifiP2pManager$Channel;";

const LISTENER_SIG: string = "Landroid/net/wifi/p2p/WifiP2pManager$ActionListener;";

function call_object(e: usize, obj: usize, name: string, sig: string, values: usize[]): usize {
  let args: usize = jni.jvalues(values);
  let got: usize = jni.call_object_method_a(e, obj, jni.get_method_id(e, jni.get_object_class(e, obj), name, sig), args);
  jni.free_buffer(args);
  return got;
}

function call_void(e: usize, obj: usize, name: string, sig: string, values: usize[]): boolean {
  let args: usize = jni.jvalues(values);
  let done: usize = jni.call_void_method_a(e, obj, jni.get_method_id(e, jni.get_object_class(e, obj), name, sig), args);
  jni.free_buffer(args);
  if (jni.exception_occurred(e) != 0 as usize) {
    jni.exception_clear(e);
    return false;
  }
  return true;
}

function p2p_filter(e: usize): usize {
  let class: usize = jni.find_class(e, "android/content/IntentFilter");
  let none: usize = jni.jvalue_alloc(0);
  let filter: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", "()V"), none);
  jni.free_buffer(none);
  for action in P2P_ACTIONS {
    let added: boolean = call_void(e, filter, "addAction", "(Ljava/lang/String;)V", [jni.new_string(e, action)]);
  }
  return filter;
}

// Starts Wi-Fi Direct and registers the receiver `class`, the Java name of
// the app's @jni subclass of BroadcastReceiver. It is None when the device
// has no Wi-Fi Direct. Call it on the loop thread.
pub function p2p_start(ctx: usize, class: string): Option[P2p] {
  let e: usize = env(ctx);
  let obj: usize = activity_object(ctx);
  let manager: usize = call_object(e, obj, "getSystemService", "(Ljava/lang/String;)Ljava/lang/Object;", [jni.new_string(e, "wifip2p")]);
  if (manager == 0 as usize) {
    return None;
  }
  let looper: usize = call_object(e, obj, "getMainLooper", "()Landroid/os/Looper;", []);
  let channel: usize = call_object(e, manager, "initialize", "(Landroid/content/Context;Landroid/os/Looper;Landroid/net/wifi/p2p/WifiP2pManager$ChannelListener;)" + CHANNEL_SIG, [obj, looper, 0 as usize]);
  if (channel == 0 as usize) {
    return None;
  }
  let receiver_class: usize = app_class(e, obj, class);
  let activity: usize = 0 as usize;
  unsafe {
    activity = __load_ptr(ctx + 16 as usize);
  }
  let stored: usize = jni.set_static_long_field(e, receiver_class, jni.get_static_field_id(e, receiver_class, "fern", "J"), activity);
  let none: usize = jni.jvalue_alloc(0);
  let receiver: usize = jni.new_object_a(e, receiver_class, jni.get_method_id(e, receiver_class, "<init>", "()V"), none);
  jni.free_buffer(none);
  let intent: usize = call_object(e, obj, "registerReceiver", "(Landroid/content/BroadcastReceiver;Landroid/content/IntentFilter;)Landroid/content/Intent;", [receiver, p2p_filter(e)]);
  return Some(P2p { manager: jni.new_global_ref(e, manager), channel: jni.new_global_ref(e, channel), receiver: jni.new_global_ref(e, receiver) });
}

// Looks for peers. P2pPeers events follow. It is false when Android
// refuses, for example without the permission.
pub function p2p_discover(ctx: usize, p: P2p): boolean {
  return call_void(env(ctx), p.manager, "discoverPeers", "(" + CHANNEL_SIG + LISTENER_SIG + ")V", [p.channel, 0 as usize]);
}

// Invites the peer with the MAC address `address`. A P2pGroup event
// follows when the other user accepts.
pub function p2p_connect(ctx: usize, p: P2p, address: string): boolean {
  let e: usize = env(ctx);
  let class: usize = jni.find_class(e, "android/net/wifi/p2p/WifiP2pConfig");
  let none: usize = jni.jvalue_alloc(0);
  let config: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", "()V"), none);
  jni.free_buffer(none);
  let set: usize = jni.set_object_field(e, config, jni.get_field_id(e, class, "deviceAddress", "Ljava/lang/String;"), jni.new_string(e, address));
  return call_void(e, p.manager, "connect", "(" + CHANNEL_SIG + "Landroid/net/wifi/p2p/WifiP2pConfig;" + LISTENER_SIG + ")V", [p.channel, config, 0 as usize]);
}

// Leaves the group and stops looking for peers.
pub function p2p_leave(ctx: usize, p: P2p): void {
  let e: usize = env(ctx);
  let sig: string = "(" + CHANNEL_SIG + LISTENER_SIG + ")V";
  let cancelled: boolean = call_void(e, p.manager, "cancelConnect", sig, [p.channel, 0 as usize]);
  let removed: boolean = call_void(e, p.manager, "removeGroup", sig, [p.channel, 0 as usize]);
  let stopped: boolean = call_void(e, p.manager, "stopPeerDiscovery", sig, [p.channel, 0 as usize]);
}

// Leaves the group and unregisters the receiver. Call it before Destroy.
pub function p2p_stop(ctx: usize, p: P2p): void {
  p2p_leave(ctx, p);
  let e: usize = env(ctx);
  let unregistered: boolean = call_void(e, activity_object(ctx), "unregisterReceiver", "(Landroid/content/BroadcastReceiver;)V", [p.receiver]);
  jni.delete_global_ref(e, p.receiver);
  jni.delete_global_ref(e, p.channel);
  jni.delete_global_ref(e, p.manager);
}

// The onReceive of the app's receiver: `env`, `receiver` and `intent` are
// its arguments. It sends the state, the peers or the group to the loop
// thread.
pub function p2p_received(e: usize, receiver: usize, intent: usize): void {
  let class: usize = jni.get_object_class(e, receiver);
  let activity: usize = jni.get_static_long_field(e, class, jni.get_static_field_id(e, class, "fern", "J"));
  if (activity == 0 as usize || intent == 0 as usize) {
    return;
  }
  let action: string = intent_action(e, intent);
  if (action == P2P_ACTIONS[0]) {
    let enabled: i32 = jni.call_int_method_a(e, intent, jni.get_method_id(e, jni.get_object_class(e, intent), "getIntExtra", "(Ljava/lang/String;I)I"), jni.jvalues([jni.new_string(e, "wifi_p2p_state"), 0 as usize]));
    let on: usize = 0 as usize;
    if (enabled == 2) {
      on = 1 as usize;
    }
    send(activity, P2P_STATE, on);
  } else if (action == P2P_ACTIONS[1]) {
    send(activity, P2P_PEERS, text_buffer(peers_text(e, intent_extra(e, intent, "wifiP2pDeviceList"))));
  } else if (action == P2P_ACTIONS[2]) {
    send(activity, P2P_GROUP, text_buffer(group_text(e, intent_extra(e, intent, "wifiP2pInfo"))));
  }
}

function intent_extra(e: usize, intent: usize, name: string): usize {
  return call_object(e, intent, "getParcelableExtra", "(Ljava/lang/String;)Landroid/os/Parcelable;", [jni.new_string(e, name)]);
}

function string_field(e: usize, obj: usize, name: string): string {
  return jni.string_of(e, jni.get_object_field(e, obj, jni.get_field_id(e, jni.get_object_class(e, obj), name, "Ljava/lang/String;")));
}

function int_field(e: usize, obj: usize, name: string): i32 {
  return jni.get_int_field(e, obj, jni.get_field_id(e, jni.get_object_class(e, obj), name, "I"));
}

function flag_field(e: usize, obj: usize, name: string): string {
  if ((jni.get_boolean_field(e, obj, jni.get_field_id(e, jni.get_object_class(e, obj), name, "Z")) & 255) != 0) {
    return "1";
  }
  return "0";
}

// One line for each device of the WifiP2pDeviceList `list`: the status,
// the address and the name, separated by tabs.
function peers_text(e: usize, list: usize): string {
  if (list == 0 as usize) {
    return "";
  }
  let devices: usize = call_object(e, call_object(e, list, "getDeviceList", "()Ljava/util/Collection;", []), "toArray", "()[Ljava/lang/Object;", []);
  let out: string = "";
  for k in 0..jni.get_array_length(e, devices) {
    let d: usize = jni.get_object_array_element(e, devices, k);
    out = out + int_field(e, d, "status").to_string() + "\t" + string_field(e, d, "deviceAddress") + "\t" + string_field(e, d, "deviceName") + "\n";
    let deleted: usize = jni.delete_local_ref(e, d);
  }
  return out;
}

// The WifiP2pInfo `info`: formed, owner and the owner address.
function group_text(e: usize, info: usize): string {
  if (info == 0 as usize) {
    return "0\t0\t";
  }
  let address: usize = jni.get_object_field(e, info, jni.get_field_id(e, jni.get_object_class(e, info), "groupOwnerAddress", "Ljava/net/InetAddress;"));
  let host: string = "";
  if (address != 0 as usize) {
    host = jni.string_of(e, call_object(e, address, "getHostAddress", "()Ljava/lang/String;", []));
  }
  return flag_field(e, info, "groupFormed") + "\t" + flag_field(e, info, "isGroupOwner") + "\t" + host;
}

// A heap copy of `text` for the loop thread: its length, then its bytes.
function text_buffer(text: string): usize {
  let b: u8[] = text.bytes();
  unsafe {
    let buf: usize = __alloc(4 + b.len());
    __store_i32(buf, b.len());
    for k in 0..b.len() {
      __store_u8(buf + (4 + k) as usize, b[k] as i32);
    }
    return buf;
  }
}

// The text of a buffer from text_buffer, which it frees.
function taken_text(buf: usize): string {
  let b: u8[] = [];
  unsafe {
    let n: i32 = __load_i32(buf);
    for k in 0..n {
      b = b.append(__load_u8(buf + (4 + k) as usize) as u8);
    }
    __free(buf, 4 + n);
  }
  return string_from_bytes_unchecked(b);
}

function digit_of(s: string): i32 {
  if (s.len() == 1 && s[0] >= b'0' && s[0] <= b'9') {
    return (s[0] - b'0') as i32;
  }
  return 4;
}

function peers_of(text: string): P2pPeer[] {
  let out: P2pPeer[] = [];
  for line in text.split("\n") {
    let f: string[] = line.split("\t");
    if (f.len() >= 3) {
      out = out.append(P2pPeer { status: digit_of(f[0]), address: f[1], name: f[2] });
    }
  }
  return out;
}

function group_of(text: string): P2pGroup {
  let f: string[] = text.split("\t");
  if (f.len() < 3) {
    return P2pGroup { formed: false, owner: false, owner_address: "" };
  }
  return P2pGroup { formed: f[0] == "1", owner: f[1] == "1", owner_address: f[2] };
}

function p2p_event(kind: i32, arg: usize): Event {
  if (kind == P2P_STATE) {
    return P2pEnabled(arg != 0 as usize);
  }
  let text: string = taken_text(arg);
  if (kind == P2P_PEERS) {
    return P2pPeers(peers_of(text));
  }
  return P2pGroup(group_of(text));
}
```

