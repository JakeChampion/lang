# Android NativeActivity apps

This module runs an Android app with no Java code, through `android.app.NativeActivity`.

## Model

NativeActivity loads the app library and calls its `ANativeActivity_onCreate` export on the UI thread. That export calls `start`. `start` sets the activity callbacks and starts a loop thread. The callbacks run on the UI thread. Each callback sends an event to the loop thread through a pipe. Then it waits for the reply of the loop thread.

The loop thread runs `run`. `run` keeps the state of the app as an ordinary Fern value. It gives each event to the `step` function of the app. When `timeout` milliseconds go by with no event, it gives `Tick`.

When `timeout` gives 0, `run` gives `Tick` after the event that is ready, if one is. So an app that gives the time that is left to its next Tick gets that Tick while events come all the time.

The two threads take turns in the Fern code of the library: `__fern_busy` is a lock, and a C call gives it back.

The app draws the pixels itself: `lock` gives the frame of the window, `std/android/draw` draws rectangles and text on it, and `post` shows it. `request_frame` asks the display for its next frame, and the loop then gives `Vsync` to `step` at that vsync, so an app draws each frame of an animation at the time the display shows it. The window covers the whole screen, and `ContentRect` gives the part of it that the system bars and the keyboard leave free. `asset` reads a file that `fern -apk -apk-asset` put in the APK.

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

<<text fields>>

<<accessibility>>

<<windows>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module runs an Android app with no Java code, through NativeActivity.
import "std/android/draw";
import "std/android/trace";
import "std/ffi";
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

@import("libandroid.so", "ALooper_forThread")
function c_looper_for_thread(): usize;

@import("libandroid.so", "ALooper_removeFd")
function c_looper_remove_fd(looper: usize, fd: i32): i32;

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

@import("libandroid.so", "AKeyEvent_getMetaState")
function c_key_meta(event: usize): i32;

@import("libandroid.so", "AInputEvent_getDeviceId")
function c_event_device(event: usize): i32;

@import("libandroid.so", "ANativeWindow_toSurface")
function c_window_to_surface(env: usize, window: usize): usize;

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

@import("libandroid.so", "AAssetManager_open")
function c_asset_open(manager: usize, name: usize, mode: i32): usize;

@import("libandroid.so", "AAsset_getLength64")
function c_asset_length(asset: usize): i64;

@import("libandroid.so", "AAsset_read")
function c_asset_read(asset: usize, buf: usize, n: usize): i32;

@import("libandroid.so", "AAsset_close")
function c_asset_close(asset: usize): void;

@import("libandroid.so", "AChoreographer_getInstance")
function c_choreographer(): usize;

@import("libandroid.so", "AChoreographer_postFrameCallback")
function c_post_frame(choreographer: usize, callback: usize, data: usize): void;

@import("libandroid.so", "AConfiguration_new")
function c_config_new(): usize;

@import("libandroid.so", "AConfiguration_fromAssetManager")
function c_config_from_assets(config: usize, manager: usize): void;

@import("libandroid.so", "AConfiguration_getDensity")
function c_config_density(config: usize): i32;

@import("libandroid.so", "AConfiguration_getUiModeNight")
function c_config_night(config: usize): i32;

@import("libandroid.so", "AConfiguration_delete")
function c_config_delete(config: usize): void;

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

`Touch` has the action of the pointer: 0 down, 1 up, 2 move and 3 cancel. While TalkBack explores by touch, a finger on the screen comes as a hover pointer instead: 7 hover move, 9 hover enter and 10 hover exit. `Key` has the Android key code and the action: 0 down and 1 up. Key code 4 is the back key. The volume keys and the mute key (24, 25 and 164) come as `Key` events too, and then the system changes the volume.

`Char` follows the `Key` event of a key that goes down and types a character: the code point that the key map of the keyboard gives for the key and its shift state. A hardware keyboard types through these events.

`Access` has the id of an item of the tree of `std/android/access` and the action that an accessibility service, such as TalkBack, did on it: `ACCESS_CLICK` or `ACCESS_LONG_CLICK`.

`Text` has the whole text of the field of `std/android/keyboard` after the keyboard of the system changed it. `TextAction` has the code of the action key of that keyboard, such as `ACTION_GO`.

`Vsync` comes at the vsync after a call to `request_frame`, with the time of the frame on the monotonic clock in nanoseconds. Draw the frame of an animation for that time. The step of each `Vsync` is a section named `frame` in the system trace, through `std/android/trace`.

`ContentRect` has the left, top, right and bottom edges, in pixels of the window, of the part of the window that the status bar, the navigation bar and the keyboard leave free. It comes after the window shows and each time that part changes, for example when the keyboard opens. The keyboard counts only for an activity with `android:windowSoftInputMode` set to adjust its size, as `fern -apk` sets it.

`Posted` has the kind and the argument of a message that another module sent with `send`. The kinds from 1 to 12 and from 24 to 27 belong to this module. The kinds from 13 to 23 belong to `std/android/service`, `std/android/p2p`, `std/android/focus` and `std/android/player`. The kinds from 28 to 33 belong to `std/android/session`. The kinds from 100 are free for the app.

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
  ContentRect(i32, i32, i32, i32),
  Vsync(i64),
  Touch(i32, f32, f32),
  Key(i32, i32),
  Char(i32),
  Text(string),
  TextAction(i32),
  Access(i32, i32),
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

const CONTENT_RECT: i32 = 24;

const TEXT: i32 = 25;

const TEXT_ACTION: i32 = 26;

const ACCESS: i32 = 27;

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

// The event of a message from the UI thread. For CONTENT_RECT, `arg` is the
// ARect of the UI thread, which waits for the reply.
function message_event(kind: i32, arg: usize): Event {
  if (kind == FOCUS) {
    return Focus(arg != 0 as usize);
  }
  if (kind == CONTENT_RECT) {
    unsafe {
      let r = Rect.c_load(arg);
      return ContentRect(r.left, r.top, r.right, r.bottom);
    }
  }
  if (kind >= WINDOW_CREATED && kind <= WINDOW_DESTROYED) {
    return window_event(kind, Window { ptr: arg });
  }
  if (kind == TEXT) {
    return Text(taken_text(arg));
  }
  if (kind == TEXT_ACTION) {
    return TextAction(arg as i32);
  }
  if (kind == ACCESS) {
    return Access((arg >> 8) as i32, (arg & 255 as usize) as i32);
  }
  if (kind > INPUT_DESTROYED || kind < START) {
    return Posted(kind, arg);
  }
  return lifecycle_event(kind);
}
```

### `the UI thread`

The context block that `start` hangs off `ANativeActivity.instance` is a `Context`. It holds the two pipes, the activity, the thread handle, the JNIEnv of the loop thread, the window flags, the time of the last vsync, the frame flags, and the Choreographer of the loop thread. Each thread reads and writes one field at a time, so a write of one thread does not overwrite a field of the other.

Only the UI thread can change the views of the activity. `immersive` sets bit 0 for hidden bars and bit 1 for an explicit request. The focus callback applies the request through the JNIEnv of the UI thread, which the activity holds. The window gets the focus after it shows, and again each time the user returns to it.

`NativeActivity` and `Callbacks` have the fields of `ANativeActivity` and `ANativeActivityCallbacks` in `android/native_activity.h`, in their order. `@derive(ffi.CLayout)` gives each field its C offset.

```fern
<<the UI thread>>=
// The context block of an activity, which both threads share.
@derive(ffi.CLayout)
struct Context { message_read: i32, message_write: i32, reply_read: i32, reply_write: i32, activity: usize, thread: usize, env: usize, flags: i32, vsync_ns: i64, frame: i32, choreographer: usize, ui_read: i32, ui_write: i32, field: usize, watcher: usize, quiet: i32, focused: i32, host: usize, provider: usize, delegate_class: usize, tree_class: usize, tree: usize, hovered: i32, sent: i64 }

// ANativeActivity.
@derive(ffi.CLayout)
struct NativeActivity { callbacks: usize, vm: usize, env: usize, clazz: usize, internal_data_path: usize, external_data_path: usize, sdk_version: i32, instance: usize, asset_manager: usize, obb_path: usize }

// ANativeActivityCallbacks.
@derive(ffi.CLayout)
struct Callbacks { on_start: usize, on_resume: usize, on_save_instance_state: usize, on_pause: usize, on_stop: usize, on_destroy: usize, on_window_focus_changed: usize, on_native_window_created: usize, on_native_window_resized: usize, on_native_window_redraw_needed: usize, on_native_window_destroyed: usize, on_input_queue_created: usize, on_input_queue_destroyed: usize, on_content_rect_changed: usize, on_configuration_changed: usize, on_low_memory: usize }

// ARect of android/rect.h.
@derive(ffi.CLayout)
struct Rect { left: i32, top: i32, right: i32, bottom: i32 }

// A message from the UI thread to the loop thread.
@derive(ffi.CLayout)
struct Message { kind: i32, arg: usize }

// The frame flags: a frame is requested, and a vsync came.
const FRAME_REQUESTED: i32 = 1;

const FRAME_ARRIVED: i32 = 2;

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

@callback("on_content_rect")
function on_content_rect_entry(): usize;

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
  let v = 0 as usize;
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
    return NativeActivity.c_load_env(activity);
  }
}

// A new context for `activity`, with its three pipes, or 0 when a pipe
// cannot open.
function new_context(activity: usize): usize {
  unsafe {
    let ctx: usize = __alloc(Context.c_size() as i32);
    Context { message_read: 0, message_write: 0, reply_read: 0, reply_write: 0, activity: activity, thread: 0 as usize, env: 0 as usize, flags: 0, vsync_ns: 0i64, frame: 0, choreographer: 0 as usize, ui_read: 0, ui_write: 0, field: 0 as usize, watcher: 0 as usize, quiet: 0, focused: NO_ITEM, host: 0 as usize, provider: 0 as usize, delegate_class: 0 as usize, tree_class: 0 as usize, tree: 0 as usize, hovered: NO_ITEM, sent: 0i64 }.c_store(ctx);
    let ok: i32 = c_pipe2(ctx + Context.c_offset_message_read(), 524288) | c_pipe2(ctx + Context.c_offset_reply_read(), 524288) | c_pipe2(ctx + Context.c_offset_ui_read(), UI_PIPE_FLAGS);
    if (ok != 0) {
      __free(ctx, Context.c_size() as i32);
      return 0 as usize;
    }
    return ctx;
  }
}

// Sets the callbacks of `activity` and starts the loop thread at
// `thread_main`, a C function of one usize argument, such as an @callback
// entry. The loop thread gets the context, which it gives to `run`. Call it
// from ANativeActivity_onCreate.
pub function start(activity: usize, thread_main: usize): void {
  unsafe {
    let ctx: usize = new_context(activity);
    if (ctx == 0 as usize) {
      c_activity_finish(activity);
      return;
    }
    let watched: i32 = c_looper_add_fd(c_looper_for_thread(), Context.c_load_ui_read(ctx), LOOPER_CALLBACK, 1, on_ui_request_entry(), ctx);
    NativeActivity.c_store_instance(activity, ctx);
    let cb: usize = NativeActivity.c_load_callbacks(activity);
    Callbacks { ...Callbacks.c_load(cb), on_start: on_start_entry(), on_resume: on_resume_entry(), on_pause: on_pause_entry(), on_stop: on_stop_entry(), on_destroy: on_destroy_entry(), on_window_focus_changed: on_focus_entry(), on_native_window_created: on_window_created_entry(), on_native_window_resized: on_window_resized_entry(), on_native_window_redraw_needed: on_window_redraw_entry(), on_native_window_destroyed: on_window_destroyed_entry(), on_input_queue_created: on_input_created_entry(), on_input_queue_destroyed: on_input_destroyed_entry(), on_content_rect_changed: on_content_rect_entry() }.c_store(cb);
    if (c_pthread_create(ctx + Context.c_offset_thread(), 0 as usize, thread_main, ctx) == 0) {
      let ignored: i32 = c_pthread_detach(Context.c_load_thread(ctx));
    }
  }
  set_live(ui_env(activity), (activity as u64).to_string());
}

// Sends the message `kind` with `arg` to the loop thread of the
// ANativeActivity `activity`, and waits for its reply. Call it on a thread
// other than the loop thread.
pub function send(activity: usize, kind: i32, arg: usize): void {
  unsafe {
    let ctx: usize = NativeActivity.c_load_instance(activity);
    let size = Message.c_size();
    let msg: usize = __alloc(size as i32);
    Message { kind: kind, arg: arg }.c_store(msg);
    let sent: i64 = c_write(Context.c_load_message_write(ctx), msg, size);
    if (sent == size as i64) {
      let got: i64 = c_read(Context.c_load_reply_read(ctx), msg, 1 as usize);
    }
    __free(msg, size as i32);
    if (kind == DESTROY) {
      close_ui(activity, ctx);
      let a: i32 = c_close(Context.c_load_message_write(ctx));
      let b: i32 = c_close(Context.c_load_reply_read(ctx));
      NativeActivity.c_store_instance(activity, 0 as usize);
      __free(ctx, Context.c_size() as i32);
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
    let ctx: usize = NativeActivity.c_load_instance(activity);
    if (ctx == 0 as usize || (Context.c_load_flags(ctx) & FLAG_IMMERSIVE_SET) == 0) {
      return;
    }
    let ui = 0;
    let fullscreen = 0;
    if ((Context.c_load_flags(ctx) & FLAG_IMMERSIVE) != 0) {
      ui = IMMERSIVE_UI;
      fullscreen = 1024;
    }
    let e: usize = NativeActivity.c_load_env(activity);
    let obj: usize = NativeActivity.c_load_clazz(activity);
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

function on_content_rect(activity: usize, rect: usize): void {
  send(activity, CONTENT_RECT, rect);
}
```

### `the loop thread`

The looper of the loop thread has two sources: the message pipe, as ident 1, and the input queue, as ident 2. `ALooper_pollOnce` returns -3 when the timeout goes by. A negative timeout waits with no limit.

```fern
<<the loop thread>>=
const IDENT_MESSAGE: i32 = 1;

const IDENT_INPUT: i32 = 2;

const POLL_TIMEOUT: i32 = 0 - 3;

@callback("on_vsync")
function on_vsync_entry(): usize;

// The frame callback of the Choreographer, on the loop thread inside
// ALooper_pollOnce. `data` is the context.
function on_vsync(nanos: i64, data: usize): void {
  unsafe {
    Context.c_store_vsync_ns(data, nanos);
    Context.c_store_frame(data, FRAME_ARRIVED);
  }
}

// Asks for a Vsync event at the next vsync. A second call before that
// frame asks for no other. Call it on the loop thread.
pub function request_frame(ctx: usize): void {
  unsafe {
    if ((Context.c_load_frame(ctx) & FRAME_REQUESTED) != 0) {
      return;
    }
    let choreographer: usize = Context.c_load_choreographer(ctx);
    if (choreographer == 0 as usize) {
      choreographer = c_choreographer();
      Context.c_store_choreographer(ctx, choreographer);
    }
    if (choreographer == 0 as usize) {
      return;
    }
    Context.c_store_frame(ctx, FRAME_REQUESTED);
    c_post_frame(choreographer, on_vsync_entry(), ctx);
  }
}

// The Vsync event of a vsync that came, which clears it.
function frame_event(ctx: usize): Option[Event] {
  unsafe {
    if ((Context.c_load_frame(ctx) & FRAME_ARRIVED) == 0) {
      return None;
    }
    Context.c_store_frame(ctx, 0);
    return Some(Vsync(Context.c_load_vsync_ns(ctx)));
  }
}

struct Loop { ctx: usize, looper: usize, queue: usize, done: boolean }

// Runs the loop of the app on the loop thread, from the context that `start`
// gave to it. `step` takes the state with each event and gives the next
// state, so the state can hold values that `step` must consume. `timeout`
// gives the milliseconds to wait for the next event before a Tick. It
// returns the last state after the Destroy event.
pub function run[S](ctx: usize, own state: S, step: (own S, Event) => S, timeout: (S) => i32): S {
  let lp = Loop { ctx: ctx, looper: c_looper_prepare(1), queue: 0 as usize, done: false };
  let vm: usize = attach(ctx);
  unsafe {
    let added: i32 = c_looper_add_fd(lp.looper, Context.c_load_message_read(ctx), IDENT_MESSAGE, 1, 0 as usize, 0 as usize);
  }
  while (!lp.done) {
    let wait: i32 = timeout(state);
    let ident: i32 = c_looper_poll_once(wait, 0 as usize, 0 as usize, 0 as usize);
    if (ident == IDENT_MESSAGE) {
      let (next_lp, next_state) = message(lp, state, step);
      lp = next_lp;
      state = next_state;
    } else if (ident == IDENT_INPUT) {
      state = input(lp, state, step);
    }
    // A Tick that is due comes also when an event is ready at each poll.
    if (ident == POLL_TIMEOUT || wait == 0) {
      state = step(state, Tick);
    }
    // The frame callback can run in the same poll as another source.
    if let Some(e) = frame_event(ctx) {
      trace.begin("frame");
      state = step(state, e);
      trace.end();
    }
  }
  detach(vm);
  return state;
}

// Reads one message from the UI thread, handles it, and replies.
function message[S](lp: Loop, own state: S, step: (own S, Event) => S): (Loop, S) {
  unsafe {
    let size = Message.c_size();
    let msg: usize = __alloc(size as i32);
    let got: i64 = c_read(Context.c_load_message_read(lp.ctx), msg, size);
    let m = Message.c_load(msg);
    let kind: i32 = m.kind;
    let arg: usize = m.arg;
    if (got != size as i64) {
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
      let closed: i32 = c_close(Context.c_load_message_read(lp.ctx));
    }
    let reply_fd: i32 = Context.c_load_reply_write(lp.ctx);
    if (kind == DESTROY) {
      Context.c_store_reply_write(lp.ctx, 0 - 1);
    }
    let sent: i64 = c_write(reply_fd, msg, 1 as usize);
    if (kind == DESTROY) {
      let closed_reply: i32 = c_close(reply_fd);
    }
    __free(msg, size as i32);
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
        let handled = 0;
        if (kind == 2) {
          let action: i32 = c_motion_action(ev) & 255;
          let x: f32 = c_motion_x(ev, 0 as usize);
          let y: f32 = c_motion_y(ev, 0 as usize);
          hover(lp.ctx, action, x as i32, y as i32);
          state = step(state, Touch(action, x, y));
          handled = 1;
        } else if (kind == 1) {
          let code: i32 = c_key_code(ev);
          let action: i32 = c_key_action(ev);
          state = step(state, Key(code, action));
          if (action == 0 && !system_key(code)) {
            let c: i32 = key_char(lp.ctx, c_event_device(ev), code, c_key_meta(ev));
            if (c > 0) {
              state = step(state, Char(c));
            }
          }
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

// The character that key `code` types with the shift state `meta` on the
// keyboard `device`, or 0 when it types none. KeyCharacterMap.get sets the
// top bit for a dead key, which types nothing alone.
function key_char(ctx: usize, device: i32, code: i32, meta: i32): i32 {
  let e: usize = env(ctx);
  let class: usize = jni.find_class(e, "android/view/KeyCharacterMap");
  let args: usize = jni.jvalues([device as usize]);
  let map: usize = jni.call_static_object_method_a(e, class, jni.get_static_method_id(e, class, "load", "(I)Landroid/view/KeyCharacterMap;"), args);
  jni.free_buffer(args);
  if (jni.exception_occurred(e) != 0 as usize || map == 0 as usize) {
    let cleared: usize = jni.exception_clear(e);
    let gone: usize = jni.delete_local_ref(e, class);
    return 0;
  }
  let get_args: usize = jni.jvalues([code as usize, meta as usize]);
  let c: i32 = jni.call_int_method_a(e, map, jni.get_method_id(e, class, "get", "(II)I"), get_args);
  jni.free_buffer(get_args);
  if (jni.exception_occurred(e) != 0 as usize) {
    let cleared: usize = jni.exception_clear(e);
    c = 0;
  }
  let a: usize = jni.delete_local_ref(e, map);
  let b: usize = jni.delete_local_ref(e, class);
  return c;
}

// The volume keys and the mute key, which the system handles after the app.
function system_key(code: i32): boolean {
  return code == 24 || code == 25 || code == 164;
}

// JNIInvokeInterface of jni.h, the functions of a JavaVM.
@derive(ffi.CLayout)
struct InvokeInterface { reserved0: usize, reserved1: usize, reserved2: usize, destroy_java_vm: usize, attach_current_thread: usize, detach_current_thread: usize, get_env: usize, attach_current_thread_as_daemon: usize }

// Attaches the loop thread to the JVM of the activity, and keeps its
// JNIEnv in the context.
function attach(ctx: usize): usize {
  unsafe {
    let vm: usize = NativeActivity.c_load_vm(Context.c_load_activity(ctx));
    let attach_fn: usize = InvokeInterface.c_load_attach_current_thread(__load_ptr(vm));
    let ignored: usize = __c_call3(attach_fn, vm, ctx + Context.c_offset_env(), 0 as usize);
    return vm;
  }
}

function detach(vm: usize): void {
  unsafe {
    let detach_fn: usize = InvokeInterface.c_load_detach_current_thread(__load_ptr(vm));
    let ignored: usize = __c_call1(detach_fn, vm);
  }
}

// The JNIEnv of the loop thread. The first call before `run` attaches the
// thread to the JVM, so JNI calls work on the loop thread at any time.
pub function env(ctx: usize): usize {
  unsafe {
    if (Context.c_load_env(ctx) == 0 as usize) {
      let vm: usize = attach(ctx);
    }
    return Context.c_load_env(ctx);
  }
}

// The global reference of the NativeActivity object.
pub function activity_object(ctx: usize): usize {
  unsafe {
    return NativeActivity.c_load_clazz(Context.c_load_activity(ctx));
  }
}

// The action of the Java Intent `intent`.
pub function intent_action(e: usize, intent: usize): string {
  return jni.string_of(e, jni.call_object(e, intent, "getAction", "()Ljava/lang/String;", []));
}

// The string extra `name` of the Intent that started the activity, or ""
// when it has none. Call it on the loop thread.
pub function intent_extra(ctx: usize, name: string): string {
  let e: usize = env(ctx);
  let intent: usize = jni.call_object(e, activity_object(ctx), "getIntent", "()Landroid/content/Intent;", []);
  if (jni.exception_occurred(e) != 0 as usize || intent == 0 as usize) {
    let cleared: usize = jni.exception_clear(e);
    return "";
  }
  let value: usize = jni.call_object(e, intent, "getStringExtra", "(Ljava/lang/String;)Ljava/lang/String;", [jni.new_string(e, name)]);
  if (jni.exception_occurred(e) != 0 as usize) {
    let cleared: usize = jni.exception_clear(e);
    value = 0 as usize;
  }
  let out = "";
  if (value != 0 as usize) {
    out = jni.string_of(e, value);
    let a: usize = jni.delete_local_ref(e, value);
  }
  let b: usize = jni.delete_local_ref(e, intent);
  return out;
}

// The API level of the device.
pub function sdk_version(ctx: usize): i32 {
  unsafe {
    return NativeActivity.c_load_sdk_version(Context.c_load_activity(ctx));
  }
}

// The directory of the private files of the app, or "" when the activity
// gives none. Other apps cannot read the files in it. Android copies it to
// the backups of the user and to a new device: keep secrets in
// `no_backup_dir`.
pub function data_dir(ctx: usize): string {
  let bytes: u8[] = [];
  unsafe {
    let path: usize = NativeActivity.c_load_internal_data_path(Context.c_load_activity(ctx));
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
    c_activity_finish(Context.c_load_activity(ctx));
  }
}

// Keeps the screen on while the window shows, or stops that.
pub function keep_screen_on(ctx: usize, on: boolean): void {
  unsafe {
    let activity: usize = Context.c_load_activity(ctx);
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
    let flags: i32 = Context.c_load_flags(ctx) | FLAG_IMMERSIVE_SET;
    if (on) {
      Context.c_store_flags(ctx, flags | FLAG_IMMERSIVE);
    } else {
      Context.c_store_flags(ctx, flags & ~FLAG_IMMERSIVE);
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

// The file `name` under assets/ in the APK, or None when the APK has no
// such file.
pub function asset(ctx: usize, name: string): Option[u8[]] {
  unsafe {
    let manager: usize = NativeActivity.c_load_asset_manager(Context.c_load_activity(ctx));
    let path: usize = jni.cstr(name);
    // AASSET_MODE_BUFFER
    let a: usize = c_asset_open(manager, path, 3);
    jni.free_buffer(path);
    if (a == 0 as usize) {
      return None;
    }
    let n: i64 = c_asset_length(a);
    if (n < 0i64 || n > 268435456i64) {
      c_asset_close(a);
      return None;
    }
    let size = n as i32;
    let buf: usize = __alloc(size + 1);
    let got = 0;
    while (got < size) {
      let r: i32 = c_asset_read(a, buf + got as usize, (size - got) as usize);
      if (r <= 0) {
        break;
      }
      got += r;
    }
    c_asset_close(a);
    let out: u8[] = [];
    for i in 0..got {
      out = out.append(__load_u8(buf + i as usize) as u8);
    }
    __free(buf, size + 1);
    if (got < size) {
      return None;
    }
    return Some(out);
  }
}

// The density of the screen in dots per inch: 160 is one pixel for each
// density-independent pixel.
pub function density(ctx: usize): i32 {
  unsafe {
    let config: usize = c_config_new();
    c_config_from_assets(config, NativeActivity.c_load_asset_manager(Context.c_load_activity(ctx)));
    let dpi: i32 = c_config_density(config);
    c_config_delete(config);
    if (dpi <= 0 || dpi == 65534 || dpi == 65535) {
      return 160;
    }
    return dpi;
  }
}

// ACONFIGURATION_UI_MODE_NIGHT_YES.
const UI_MODE_NIGHT_YES: i32 = 2;

// The scale of the text of the system, which the person sets as the font
// size: 1 is the default, and 1.3 makes text 30% larger. Multiply a size
// in sp by the scale and by the pixels of one dp.
pub function font_scale(ctx: usize): f64 {
  let e: usize = env(ctx);
  let resources: usize = jni.call_object(e, activity_object(ctx), "getResources", "()Landroid/content/res/Resources;", []);
  let config: usize = jni.call_object(e, resources, "getConfiguration", "()Landroid/content/res/Configuration;", []);
  let class: usize = jni.get_object_class(e, config);
  let scale: f32 = jni.get_float_field(e, config, jni.get_field_id(e, class, "fontScale", "F"));
  let c: usize = jni.delete_local_ref(e, class);
  let b: usize = jni.delete_local_ref(e, config);
  let a: usize = jni.delete_local_ref(e, resources);
  if (scale <= 0.0 as f32) {
    return 1.0;
  }
  return scale as f64;
}

// The system uses the dark theme. Android starts an activity again when the
// theme changes, unless its manifest handles the change of uiMode itself.
pub function night_mode(ctx: usize): boolean {
  unsafe {
    let config: usize = c_config_new();
    c_config_from_assets(config, NativeActivity.c_load_asset_manager(Context.c_load_activity(ctx)));
    let night: i32 = c_config_night(config);
    c_config_delete(config);
    return night == UI_MODE_NIGHT_YES;
  }
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
```

### `text fields`

`std/android/keyboard` shows the keyboard of the system for a text field. This section holds the parts of that field that use the context of the activity. The keyboard types into a hidden `EditText` that this section adds to the window. Only the UI thread can change a view. So the loop thread writes each request to a pipe that the looper of the UI thread watches. The callback of that looper does the work on the UI thread.

The class `dev.fern.android.TextField` of `std/android/keyboard` watches the field. It calls `text_changed` after each change and `text_action` for the action key. Each one sends a message to the loop thread. A change that a request makes does not come back as a `Text` event.

```fern
<<text fields>>=
// O_CLOEXEC and O_NONBLOCK: the UI thread reads requests until none is left.
const UI_PIPE_FLAGS: i32 = 526336;

// ALOOPER_POLL_CALLBACK: the looper calls the callback of the fd.
const LOOPER_CALLBACK: i32 = -2;

const FIELD_SHOW: i32 = 1;

const FIELD_SET: i32 = 2;

const FIELD_HIDE: i32 = 3;

// InputType.TYPE_CLASS_TEXT: text with the suggestions of the keyboard.
pub const INPUT_TEXT: i32 = 1;

// InputType.TYPE_CLASS_TEXT | TYPE_TEXT_FLAG_MULTI_LINE: text of more than
// one line. The enter key of the keyboard starts a new line.
pub const INPUT_LINES: i32 = 131073;

// InputType.TYPE_CLASS_TEXT | TYPE_TEXT_FLAG_NO_SUGGESTIONS: text with no
// suggestions, such as a host name.
pub const INPUT_PLAIN: i32 = 524289;

// EditorInfo.IME_ACTION_GO: the action key goes to the result.
pub const ACTION_GO: i32 = 2;

// EditorInfo.IME_ACTION_SEARCH: the action key starts a search.
pub const ACTION_SEARCH: i32 = 3;

// EditorInfo.IME_ACTION_DONE: the action key ends the typing.
pub const ACTION_DONE: i32 = 6;

// EditorInfo.IME_FLAG_NO_EXTRACT_UI: the keyboard does not cover the app
// with a full-screen editor in landscape.
const NO_EXTRACT_UI: i32 = 268435456;

// A request from the loop thread to the UI thread: an op, two numbers, and a
// buffer from bytes_buffer or 0. The UI thread frees the buffer unless the op
// keeps it.
@derive(ffi.CLayout)
struct UiRequest { op: i32, a: i32, b: i32, data: usize }

// Asks the UI thread to show the field with `text`, the input type `input`
// and the action key `action`.
pub(package) function show_field(ctx: usize, text: string, input: i32, action: i32): void {
  ui_request(ctx, UiRequest { op: FIELD_SHOW, a: input, b: action, data: text_buffer(text) });
}

// Asks the UI thread to give the field the text `text`, with the cursor at
// its end.
pub(package) function set_field(ctx: usize, text: string): void {
  ui_request(ctx, UiRequest { op: FIELD_SET, a: 0, b: 0, data: text_buffer(text) });
}

// Asks the UI thread to give the field the text `text`, with the cursor
// before the UTF-16 code unit `cursor`.
pub(package) function set_field_at(ctx: usize, text: string, cursor: i32): void {
  ui_request(ctx, UiRequest { op: FIELD_SET, a: 0, b: cursor.max(0) + 1, data: text_buffer(text) });
}

// Asks the UI thread to hide the keyboard and take the focus from the field.
pub(package) function hide_field(ctx: usize): void {
  ui_request(ctx, UiRequest { op: FIELD_HIDE, a: 0, b: 0, data: 0 as usize });
}

function ui_request(ctx: usize, r: UiRequest): void {
  unsafe {
    let req: usize = __alloc(UiRequest.c_size() as i32);
    r.c_store(req);
    let slot: usize = __alloc(8);
    __store_ptr(slot, req);
    let sent: i64 = c_write(Context.c_load_ui_write(ctx), slot, 8 as usize);
    __free(slot, 8);
    if (sent != 8i64) {
      free_request(req);
    }
  }
}

function free_request(req: usize): void {
  unsafe {
    let data: usize = UiRequest.c_load_data(req);
    if (data != 0 as usize) {
      free_bytes(data);
    }
    __free(req, UiRequest.c_size() as i32);
  }
}

@callback("on_ui_request")
function on_ui_request_entry(): usize;

// The callback of the looper of the UI thread: it does each request in the
// pipe. `data` is the context.
function on_ui_request(fd: i32, events: i32, data: usize): i32 {
  unsafe {
    let slot: usize = __alloc(8);
    while (c_read(fd, slot, 8 as usize) == 8i64) {
      let req: usize = __load_ptr(slot);
      if (do_request(data, UiRequest.c_load(req))) {
        UiRequest.c_store_data(req, 0 as usize);
      }
      free_request(req);
    }
    __free(slot, 8);
  }
  return 1;
}

// Does the request `r` on the UI thread. It is true when the request keeps
// its buffer.
function do_request(ctx: usize, r: UiRequest): boolean {
  if (r.op > FIELD_HIDE) {
    return access_request(ctx, r);
  }
  field_request(ctx, r);
  return false;
}

function field_request(ctx: usize, r: UiRequest): void {
  unsafe {
    let activity: usize = Context.c_load_activity(ctx);
    let e: usize = NativeActivity.c_load_env(activity);
    let obj: usize = NativeActivity.c_load_clazz(activity);
    if (Context.c_load_field(ctx) == 0 as usize) {
      Context.c_store_field(ctx, new_field(ctx, e, obj));
    }
    let field: usize = Context.c_load_field(ctx);
    if (field == 0 as usize) {
      return;
    }
    if (r.op == FIELD_SHOW) {
      // TYPE_TEXT_FLAG_MULTI_LINE is 131072.
      let lines = 1 - (r.a >> 17 & 1);
      let single = jni.call_void(e, field, "setSingleLine", "(Z)V", [lines as usize]);
      let typed = jni.call_void(e, field, "setInputType", "(I)V", [r.a as usize]);
      let options = jni.call_void(e, field, "setImeOptions", "(I)V", [(r.b | NO_EXTRACT_UI) as usize]);
    }
    if (r.data != 0 as usize) {
      let cursor = -1;
      if (r.op == FIELD_SET) {
        cursor = r.b - 1;
      }
      field_text(ctx, e, field, text_of_buffer(r.data), cursor);
    }
    if (r.op == FIELD_SHOW) {
      let focused = call_bool(e, field, "requestFocus", "()Z", []);
      input_method(e, obj, "showSoftInput", "(Landroid/view/View;I)Z", field);
    } else if (r.op == FIELD_HIDE) {
      let token: usize = jni.call_object(e, field, "getWindowToken", "()Landroid/os/IBinder;", []);
      input_method(e, obj, "hideSoftInputFromWindow", "(Landroid/os/IBinder;I)Z", token);
      let cleared = jni.call_void(e, field, "clearFocus", "()V", []);
      let a: usize = jni.delete_local_ref(e, token);
    }
    let cleared: usize = jni.exception_clear(e);
  }
}

// Calls the method `name` of the InputMethodManager with `target` and the
// flags 0.
function input_method(e: usize, obj: usize, name: string, sig: string, target: usize): void {
  let service: usize = jni.new_string(e, "input_method");
  let imm: usize = jni.call_object(e, obj, "getSystemService", "(Ljava/lang/String;)Ljava/lang/Object;", [service]);
  if (imm != 0 as usize) {
    let done = call_bool(e, imm, name, sig, [target, 0 as usize]);
  }
  let a: usize = jni.delete_local_ref(e, imm);
  let b: usize = jni.delete_local_ref(e, service);
}

// The hidden EditText of the activity, as a global reference, or 0. It is one
// pixel, and NativeActivity draws no view, so it shows nothing.
function new_field(ctx: usize, e: usize, obj: usize): usize {
  let class: usize = jni.find_class(e, "android/widget/EditText");
  let args: usize = jni.jvalues([obj]);
  let edit: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", "(Landroid/content/Context;)V"), args);
  jni.free_buffer(args);
  let a: usize = jni.delete_local_ref(e, class);
  if (edit == 0 as usize) {
    let cleared: usize = jni.exception_clear(e);
    return 0 as usize;
  }
  let lp_class: usize = jni.find_class(e, "android/view/ViewGroup$LayoutParams");
  let lp_args: usize = jni.jvalues([1 as usize, 1 as usize]);
  let lp: usize = jni.new_object_a(e, lp_class, jni.get_method_id(e, lp_class, "<init>", "(II)V"), lp_args);
  jni.free_buffer(lp_args);
  let single = jni.call_void(e, edit, "setSingleLine", "(Z)V", [1 as usize]);
  let added = jni.call_void(e, obj, "addContentView", "(Landroid/view/View;Landroid/view/ViewGroup$LayoutParams;)V", [edit, lp]);
  let watcher_class: usize = jni.load_class(e, obj, "dev.fern.android.TextField");
  let out = 0 as usize;
  if (added && watcher_class != 0 as usize) {
    let none: usize = jni.jvalue_alloc(0);
    let watcher: usize = jni.new_object_a(e, watcher_class, jni.get_method_id(e, watcher_class, "<init>", "()V"), none);
    jni.free_buffer(none);
    let set: usize = jni.set_static_long_field(e, watcher_class, jni.get_static_field_id(e, watcher_class, "fern", "J"), ctx);
    let watching = jni.call_void(e, edit, "addTextChangedListener", "(Landroid/text/TextWatcher;)V", [watcher]);
    let listening = jni.call_void(e, edit, "setOnEditorActionListener", "(Landroid/widget/TextView$OnEditorActionListener;)V", [watcher]);
    out = jni.new_global_ref(e, edit);
    unsafe {
      Context.c_store_watcher(ctx, jni.new_global_ref(e, watcher_class));
    }
    let w: usize = jni.delete_local_ref(e, watcher);
  }
  let cleared: usize = jni.exception_clear(e);
  for r in [watcher_class, lp, lp_class, edit] {
    let d: usize = jni.delete_local_ref(e, r);
  }
  return out;
}

// Gives the field the text `text` with the cursor before the UTF-16 code
// unit `cursor`, or at the end of the text for a cursor below 0. The field
// does not report a change that it makes.
function field_text(ctx: usize, e: usize, field: usize, text: string, cursor: i32): void {
  unsafe {
    Context.c_store_quiet(ctx, 1);
    let s: usize = jni.new_string(e, text);
    let set = jni.call_void(e, field, "setText", "(Ljava/lang/CharSequence;)V", [s]);
    let class: usize = jni.get_object_class(e, field);
    let none: usize = jni.jvalue_alloc(0);
    let n: i32 = jni.call_int_method_a(e, field, jni.get_method_id(e, class, "length", "()I"), none);
    jni.free_buffer(none);
    let at = n;
    if (cursor >= 0 && cursor < n) {
      at = cursor;
    }
    let moved = jni.call_void(e, field, "setSelection", "(I)V", [at as usize]);
    let a: usize = jni.delete_local_ref(e, class);
    let b: usize = jni.delete_local_ref(e, s);
    Context.c_store_quiet(ctx, 0);
  }
}

function call_bool(e: usize, obj: usize, name: string, sig: string, values: usize[]): boolean {
  let class: usize = jni.get_object_class(e, obj);
  let args: usize = jni.jvalues(values);
  let got: i32 = jni.call_boolean_method_a(e, obj, jni.get_method_id(e, class, name, sig), args);
  jni.free_buffer(args);
  let a: usize = jni.delete_local_ref(e, class);
  return got != 0;
}

// Ends the requests on Destroy, on the UI thread: it stops the looper watching
// the pipe, frees the requests that wait in it, closes it, and releases the
// views of the requests.
function close_ui(activity: usize, ctx: usize): void {
  unsafe {
    let read: i32 = Context.c_load_ui_read(ctx);
    let removed: i32 = c_looper_remove_fd(c_looper_for_thread(), read);
    let slot: usize = __alloc(8);
    while (c_read(read, slot, 8 as usize) == 8i64) {
      free_request(__load_ptr(slot));
    }
    __free(slot, 8);
    let a: i32 = c_close(read);
    let b: i32 = c_close(Context.c_load_ui_write(ctx));
    let e: usize = NativeActivity.c_load_env(activity);
    let field: usize = Context.c_load_field(ctx);
    if (field != 0 as usize) {
      let d: usize = jni.delete_global_ref(e, field);
    }
    release_class(e, Context.c_load_watcher(ctx), ctx);
    close_access(e, ctx);
  }
}

// Releases the global reference `class` of a class with a static `fern`
// field, and clears that field when it still holds `ctx`. A new activity can
// start before this one ends, and own the class.
function release_class(e: usize, class: usize, ctx: usize): void {
  if (class == 0 as usize) {
    return;
  }
  let id: usize = jni.get_static_field_id(e, class, "fern", "J");
  if (jni.get_static_long_field(e, class, id) == ctx) {
    let f: usize = jni.set_static_long_field(e, class, id, 0 as usize);
  }
  let g: usize = jni.delete_global_ref(e, class);
}

// The context of the activity, which the class of `thiz` keeps in its static
// field `fern`.
function static_context(env: usize, thiz: usize): usize {
  let class: usize = jni.get_object_class(env, thiz);
  let ctx = jni.get_static_long_field(env, class, jni.get_static_field_id(env, class, "fern", "J"));
  let a: usize = jni.delete_local_ref(env, class);
  return ctx;
}

// After a change of the text of the field, on the UI thread: the loop thread
// gets the whole text.
pub(package) function text_changed(env: usize, thiz: usize, editable: usize): void {
  let ctx = static_context(env, thiz);
  if (ctx == 0 as usize) {
    return;
  }
  unsafe {
    if (Context.c_load_quiet(ctx) != 0) {
      return;
    }
    let s: usize = jni.call_object(env, editable, "toString", "()Ljava/lang/String;", []);
    let text = jni.string_of(env, s);
    let a: usize = jni.delete_local_ref(env, s);
    send(Context.c_load_activity(ctx), TEXT, text_buffer(text));
  }
}

// The action key of the keyboard, on the UI thread. It is true when the loop
// thread gets the action.
pub(package) function text_action(env: usize, thiz: usize, action: i32): boolean {
  let ctx = static_context(env, thiz);
  if (ctx == 0 as usize) {
    return false;
  }
  unsafe {
    send(Context.c_load_activity(ctx), TEXT_ACTION, action as usize);
  }
  return true;
}

// A heap copy of `text` for another thread: its length, then its bytes.
pub(package) function text_buffer(text: string): usize {
  return bytes_buffer(text.as_bytes());
}

// A heap copy of `b` for another thread: its length, then its bytes.
function bytes_buffer(b: [u8]): usize {
  unsafe {
    let buf: usize = __alloc(4 + b.len());
    __store_i32(buf, b.len());
    __memcpy(buf + 4 as usize, __bytes_data(b), b.len());
    return buf;
  }
}

function free_bytes(buf: usize): void {
  unsafe {
    __free(buf, 4 + __load_i32(buf));
  }
}

// The text of a buffer from text_buffer, which it frees.
pub(package) function taken_text(buf: usize): string {
  let text = text_of_buffer(buf);
  free_bytes(buf);
  return text;
}

function text_of_buffer(buf: usize): string {
  let b: u8[] = [];
  unsafe {
    let n: i32 = __load_i32(buf);
    for k in 0..n {
      b = b.append(__load_u8(buf + (4 + k) as usize) as u8);
    }
  }
  return unsafe { string_from_bytes_unchecked(b) };
}
```

### `accessibility`

An accessibility service, such as TalkBack, reads the views of a window. A NativeActivity draws its own pixels, so the service finds no view to read. `std/android/access` gives the service a tree of items. The app gives each item an id, a rectangle in the pixels of the window, a text, and the actions that the item takes.

This section keeps the parts of the tree that use the context of the activity. The first tree adds a plain `View` that covers the window. That view has a delegate, the class `dev.fern.android.AccessDelegate`, which gives the service a provider, `dev.fern.android.AccessTree`. The provider makes a node for each item from the last tree.

The loop thread sends each tree as a request to the UI thread, through the pipe of the text fields. It sends a tree only when the bytes of the tree change. A hover pointer, which TalkBack makes when it explores by touch, also goes to the UI thread. The item under the pointer gets the hover event that moves the focus of the service. A click or a long click of the service comes to the loop thread as an `Access` event.

```fern
<<accessibility>>=
const ACCESS_TREE: i32 = 4;

const ACCESS_HOVER: i32 = 5;

const ACCESS_EXIT: i32 = 6;

// AccessibilityNodeProvider.HOST_VIEW_ID: the node of the view itself.
const HOST_ID: i32 = -1;

// No item. The id of an item is 0 or more.
const NO_ITEM: i32 = -2;

// AccessibilityNodeInfo.ACTION_CLICK: the item does something on a tap.
pub const ACCESS_CLICK: i32 = 16;

// AccessibilityNodeInfo.ACTION_LONG_CLICK: the item does something on a
// long press.
pub const ACCESS_LONG_CLICK: i32 = 32;

const ACTION_ACCESSIBILITY_FOCUS: i32 = 64;

const ACTION_CLEAR_ACCESSIBILITY_FOCUS: i32 = 128;

// AccessibilityNodeProvider.findFocus: FOCUS_ACCESSIBILITY.
const FOCUS_ACCESSIBILITY: i32 = 2;

// The AccessibilityEvent types.
const EVENT_CLICKED: i32 = 1;

const EVENT_LONG_CLICKED: i32 = 2;

const EVENT_HOVER_ENTER: i32 = 128;

const EVENT_HOVER_EXIT: i32 = 256;

const EVENT_CONTENT_CHANGED: i32 = 2048;

const EVENT_FOCUSED: i32 = 32768;

const EVENT_FOCUS_CLEARED: i32 = 65536;

// An item of the tree: its id, its rectangle in the pixels of the window,
// the text that the service reads, and its actions, a sum of ACCESS_CLICK
// and ACCESS_LONG_CLICK.
pub struct AccessItem { id: i32, x: i32, y: i32, w: i32, h: i32, text: string, actions: i32 }

// The bytes of each item: its id, x, y, w, h and actions, and the length of
// its text, then its text.
const ITEM_HEAD: i32 = 28;

// Sends the tree `items` to the UI thread when it differs from the last
// tree. Call it on the loop thread.
pub(package) function publish_tree(ctx: usize, items: AccessItem[]): void {
  let b: u8[] = [];
  b = put_i32(b, items.len());
  for it in items {
    for v in [it.id, it.x, it.y, it.w, it.h, it.actions, it.text.len()] {
      b = put_i32(b, v);
    }
    b = b.concat(it.text.as_bytes());
  }
  let hash = fnv(b);
  unsafe {
    if (Context.c_load_sent(ctx) == hash) {
      return;
    }
    Context.c_store_sent(ctx, hash);
  }
  ui_request(ctx, UiRequest { op: ACCESS_TREE, a: 0, b: 0, data: bytes_buffer(b) });
}

function put_i32(b: u8[], v: i32): u8[] {
  let out = b;
  for k in 0..4 {
    out = out.append((v >> 8 * k & 255) as u8);
  }
  return out;
}

// The FNV-1a hash of `b`. It is never 0, the hash of no tree.
function fnv(b: u8[]): i64 {
  let h = -3750763034362895579i64;
  for c in b {
    h = (h ^ c as i64) * 1099511628211i64;
  }
  if (h == 0i64) {
    return 1i64;
  }
  return h;
}

// Sends a hover pointer of the loop thread to the UI thread, once a tree is
// sent.
function hover(ctx: usize, action: i32, x: i32, y: i32): void {
  unsafe {
    if (Context.c_load_sent(ctx) == 0i64) {
      return;
    }
  }
  if (action == 7 || action == 9) {
    ui_request(ctx, UiRequest { op: ACCESS_HOVER, a: x, b: y, data: 0 as usize });
  } else if (action == 10) {
    ui_request(ctx, UiRequest { op: ACCESS_EXIT, a: 0, b: 0, data: 0 as usize });
  }
}

struct Item { id: i32, x: i32, y: i32, w: i32, h: i32, actions: i32, text: string }

function tree_count(tree: usize): i32 {
  unsafe {
    return __load_i32(tree + 4 as usize);
  }
}

// The offset of each item in the buffer of a tree.
function item_offsets(tree: usize): i32[] {
  let out: i32[] = [];
  if (tree == 0 as usize) {
    return out;
  }
  let off = 8;
  unsafe {
    for k in 0..tree_count(tree) {
      out = out.append(off);
      off += ITEM_HEAD + __load_i32(tree + (off + 24) as usize);
    }
  }
  return out;
}

function item_at(tree: usize, off: i32): Item {
  unsafe {
    let p: usize = tree + off as usize;
    let n: i32 = __load_i32(p + 24 as usize);
    let b: u8[] = [];
    for k in 0..n {
      b = b.append(__load_u8(p + (ITEM_HEAD + k) as usize) as u8);
    }
    return Item { id: __load_i32(p), x: __load_i32(p + 4 as usize), y: __load_i32(p + 8 as usize), w: __load_i32(p + 12 as usize), h: __load_i32(p + 16 as usize), actions: __load_i32(p + 20 as usize), text: string_from_bytes_unchecked(b) };
  }
}

// The offset of the item `id` in the tree, or -1.
function item_offset(tree: usize, id: i32): i32 {
  for off in item_offsets(tree) {
    unsafe {
      if (__load_i32(tree + off as usize) == id) {
        return off;
      }
    }
  }
  return -1;
}

// The id of the last item whose rectangle holds (x, y), or NO_ITEM.
function item_under(tree: usize, x: i32, y: i32): i32 {
  let found = NO_ITEM;
  for off in item_offsets(tree) {
    let it = item_at(tree, off);
    if (x >= it.x && x < it.x + it.w && y >= it.y && y < it.y + it.h) {
      found = it.id;
    }
  }
  return found;
}

// Does a tree or hover request on the UI thread. It is true when it keeps
// the buffer of the request.
function access_request(ctx: usize, r: UiRequest): boolean {
  unsafe {
    let activity: usize = Context.c_load_activity(ctx);
    let e: usize = NativeActivity.c_load_env(activity);
    if (r.op == ACCESS_TREE && Context.c_load_host(ctx) == 0 as usize) {
      new_access_host(ctx, e, NativeActivity.c_load_clazz(activity));
    }
    let host: usize = Context.c_load_host(ctx);
    if (host == 0 as usize) {
      return false;
    }
    if (r.op == ACCESS_TREE) {
      store_tree(ctx, e, host, r.data);
      return true;
    }
    let id = NO_ITEM;
    if (r.op == ACCESS_HOVER) {
      id = item_under(Context.c_load_tree(ctx), r.a, r.b);
    }
    hover_item(ctx, e, id);
    return false;
  }
}

// Keeps the tree `tree` in place of the last tree. The focus and the hover
// end on an item that the new tree does not hold.
function store_tree(ctx: usize, e: usize, host: usize, tree: usize): void {
  unsafe {
    let old: usize = Context.c_load_tree(ctx);
    if (old != 0 as usize) {
      free_bytes(old);
    }
    Context.c_store_tree(ctx, tree);
    if (item_offset(tree, Context.c_load_focused(ctx)) < 0) {
      Context.c_store_focused(ctx, NO_ITEM);
    }
    if (item_offset(tree, Context.c_load_hovered(ctx)) < 0) {
      Context.c_store_hovered(ctx, NO_ITEM);
    }
  }
  let changed = jni.call_void(e, host, "sendAccessibilityEvent", "(I)V", [EVENT_CONTENT_CHANGED as usize]);
}

// Moves the hover to the item `id`, or NO_ITEM, with the exit and the enter
// events.
function hover_item(ctx: usize, e: usize, id: i32): void {
  unsafe {
    let was: i32 = Context.c_load_hovered(ctx);
    if (id == was) {
      return;
    }
    if (was != NO_ITEM) {
      send_item_event(ctx, e, was, EVENT_HOVER_EXIT);
    }
    if (id != NO_ITEM) {
      send_item_event(ctx, e, id, EVENT_HOVER_ENTER);
    }
    Context.c_store_hovered(ctx, id);
  }
}

// Adds the view that holds the tree, with its delegate and its provider.
function new_access_host(ctx: usize, e: usize, obj: usize): void {
  let view_class: usize = jni.find_class(e, "android/view/View");
  let args: usize = jni.jvalues([obj]);
  let view: usize = jni.new_object_a(e, view_class, jni.get_method_id(e, view_class, "<init>", "(Landroid/content/Context;)V"), args);
  jni.free_buffer(args);
  let a: usize = jni.delete_local_ref(e, view_class);
  if (view == 0 as usize) {
    let cleared: usize = jni.exception_clear(e);
    return;
  }
  // ViewGroup.LayoutParams.MATCH_PARENT in both directions.
  let lp_class: usize = jni.find_class(e, "android/view/ViewGroup$LayoutParams");
  let lp_args: usize = jni.jvalues([4294967295 as usize, 4294967295 as usize]);
  let lp: usize = jni.new_object_a(e, lp_class, jni.get_method_id(e, lp_class, "<init>", "(II)V"), lp_args);
  jni.free_buffer(lp_args);
  let important = jni.call_void(e, view, "setImportantForAccessibility", "(I)V", [1 as usize]);
  let added = jni.call_void(e, obj, "addContentView", "(Landroid/view/View;Landroid/view/ViewGroup$LayoutParams;)V", [view, lp]);
  let delegate_class: usize = jni.load_class(e, obj, "dev.fern.android.AccessDelegate");
  let tree_class: usize = jni.load_class(e, obj, "dev.fern.android.AccessTree");
  if (added && delegate_class != 0 as usize && tree_class != 0 as usize) {
    let delegate: usize = new_plain(e, delegate_class);
    let provider: usize = new_plain(e, tree_class);
    for class in [delegate_class, tree_class] {
      let set: usize = jni.set_static_long_field(e, class, jni.get_static_field_id(e, class, "fern", "J"), ctx);
    }
    let delegated = jni.call_void(e, view, "setAccessibilityDelegate", "(Landroid/view/View$AccessibilityDelegate;)V", [delegate]);
    unsafe {
      Context.c_store_host(ctx, jni.new_global_ref(e, view));
      Context.c_store_provider(ctx, jni.new_global_ref(e, provider));
      Context.c_store_delegate_class(ctx, jni.new_global_ref(e, delegate_class));
      Context.c_store_tree_class(ctx, jni.new_global_ref(e, tree_class));
    }
    for r in [delegate, provider] {
      let d: usize = jni.delete_local_ref(e, r);
    }
  }
  let cleared: usize = jni.exception_clear(e);
  for r in [tree_class, delegate_class, lp, lp_class, view] {
    let d: usize = jni.delete_local_ref(e, r);
  }
}

// A new object of `class` from its constructor with no arguments.
function new_plain(e: usize, class: usize): usize {
  let none: usize = jni.jvalue_alloc(0);
  let obj: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", "()V"), none);
  jni.free_buffer(none);
  return obj;
}

function call_static(e: usize, class: usize, name: string, sig: string, values: usize[]): usize {
  let args: usize = jni.jvalues(values);
  let got: usize = jni.call_static_object_method_a(e, class, jni.get_static_method_id(e, class, name, sig), args);
  jni.free_buffer(args);
  return got;
}

// The class name that tells the service the role of an item.
function item_class(it: Item): string {
  if ((it.actions & ACCESS_CLICK) != 0) {
    return "android.widget.Button";
  }
  return "android.widget.TextView";
}

// Sends the accessibility event `kind` for the item `id` from the view that
// holds the tree.
function send_item_event(ctx: usize, e: usize, id: i32, kind: i32): void {
  unsafe {
    let host: usize = Context.c_load_host(ctx);
    let obj: usize = NativeActivity.c_load_clazz(Context.c_load_activity(ctx));
    let class: usize = jni.find_class(e, "android/view/accessibility/AccessibilityEvent");
    let ev: usize = call_static(e, class, "obtain", "(I)Landroid/view/accessibility/AccessibilityEvent;", [kind as usize]);
    let pkg: usize = jni.call_object(e, obj, "getPackageName", "()Ljava/lang/String;", []);
    let named = jni.call_void(e, ev, "setPackageName", "(Ljava/lang/CharSequence;)V", [pkg]);
    let off = item_offset(Context.c_load_tree(ctx), id);
    if (off >= 0) {
      let it = item_at(Context.c_load_tree(ctx), off);
      let cls: usize = jni.new_string(e, item_class(it));
      let classed = jni.call_void(e, ev, "setClassName", "(Ljava/lang/CharSequence;)V", [cls]);
      let list: usize = jni.call_object(e, ev, "getText", "()Ljava/util/List;", []);
      let text: usize = jni.new_string(e, it.text);
      let added = call_bool(e, list, "add", "(Ljava/lang/Object;)Z", [text]);
      for r in [text, list, cls] {
        let d: usize = jni.delete_local_ref(e, r);
      }
    }
    let enabled = jni.call_void(e, ev, "setEnabled", "(Z)V", [1 as usize]);
    let sourced = jni.call_void(e, ev, "setSource", "(Landroid/view/View;I)V", [host, id as usize]);
    let parent: usize = jni.call_object(e, host, "getParent", "()Landroid/view/ViewParent;", []);
    if (parent != 0 as usize) {
      let sent = call_bool(e, parent, "requestSendAccessibilityEvent", "(Landroid/view/View;Landroid/view/accessibility/AccessibilityEvent;)Z", [host, ev]);
    }
    let cleared: usize = jni.exception_clear(e);
    for r in [parent, pkg, ev, class] {
      let d: usize = jni.delete_local_ref(e, r);
    }
  }
}

// The provider of the view that holds the tree, for the delegate.
pub(package) function access_provider(env: usize, thiz: usize): usize {
  let ctx = static_context(env, thiz);
  if (ctx == 0 as usize) {
    return 0 as usize;
  }
  unsafe {
    return jni.new_local_ref(env, Context.c_load_provider(ctx));
  }
}

// The node of the item `id`, or of the view that holds the tree for
// HOST_ID, as a local reference, or 0.
pub(package) function access_node(env: usize, thiz: usize, id: i32): usize {
  let ctx = static_context(env, thiz);
  if (ctx == 0 as usize) {
    return 0 as usize;
  }
  return item_node(ctx, env, id);
}

// The node that has the focus of the service, or 0.
pub(package) function access_focus(env: usize, thiz: usize, kind: i32): usize {
  let ctx = static_context(env, thiz);
  if (ctx == 0 as usize || kind != FOCUS_ACCESSIBILITY) {
    return 0 as usize;
  }
  unsafe {
    let id: i32 = Context.c_load_focused(ctx);
    if (id == NO_ITEM) {
      return 0 as usize;
    }
    return item_node(ctx, env, id);
  }
}

function item_node(ctx: usize, e: usize, id: i32): usize {
  unsafe {
    let host: usize = Context.c_load_host(ctx);
    let tree: usize = Context.c_load_tree(ctx);
    let class: usize = jni.find_class(e, "android/view/accessibility/AccessibilityNodeInfo");
    if (id == HOST_ID) {
      let info: usize = call_static(e, class, "obtain", "(Landroid/view/View;)Landroid/view/accessibility/AccessibilityNodeInfo;", [host]);
      let init = jni.call_void(e, host, "onInitializeAccessibilityNodeInfo", "(Landroid/view/accessibility/AccessibilityNodeInfo;)V", [info]);
      for off in item_offsets(tree) {
        let child = jni.call_void(e, info, "addChild", "(Landroid/view/View;I)V", [host, __load_i32(tree + off as usize) as usize]);
      }
      let a: usize = jni.delete_local_ref(e, class);
      return info;
    }
    let off = item_offset(tree, id);
    if (off < 0) {
      let b: usize = jni.delete_local_ref(e, class);
      return 0 as usize;
    }
    let it = item_at(tree, off);
    let info: usize = call_static(e, class, "obtain", "(Landroid/view/View;I)Landroid/view/accessibility/AccessibilityNodeInfo;", [host, id as usize]);
    let obj: usize = NativeActivity.c_load_clazz(Context.c_load_activity(ctx));
    let pkg: usize = jni.call_object(e, obj, "getPackageName", "()Ljava/lang/String;", []);
    let cls: usize = jni.new_string(e, item_class(it));
    let text: usize = jni.new_string(e, it.text);
    let rect_class: usize = jni.find_class(e, "android/graphics/Rect");
    let rect_args: usize = jni.jvalues([it.x as usize, it.y as usize, (it.x + it.w) as usize, (it.y + it.h) as usize]);
    let rect: usize = jni.new_object_a(e, rect_class, jni.get_method_id(e, rect_class, "<init>", "(IIII)V"), rect_args);
    jni.free_buffer(rect_args);
    let calls = [
      ("setParent", "(Landroid/view/View;)V", [host]),
      ("setPackageName", "(Ljava/lang/CharSequence;)V", [pkg]),
      ("setClassName", "(Ljava/lang/CharSequence;)V", [cls]),
      ("setText", "(Ljava/lang/CharSequence;)V", [text]),
      ("setBoundsInParent", "(Landroid/graphics/Rect;)V", [rect]),
      ("setVisibleToUser", "(Z)V", [1 as usize]),
      ("setEnabled", "(Z)V", [1 as usize]),
    ];
    for (name, sig, values) in calls {
      let done = jni.call_void(e, info, name, sig, values);
    }
    let place: usize = jni.new_int_array(e, 2);
    let located = jni.call_void(e, host, "getLocationOnScreen", "([I)V", [place]);
    let at = jni.int_array_region(e, place, 0, 2);
    let moved = jni.call_void(e, rect, "offset", "(II)V", [at[0] as usize, at[1] as usize]);
    let screen = jni.call_void(e, info, "setBoundsInScreen", "(Landroid/graphics/Rect;)V", [rect]);
    if ((it.actions & ACCESS_CLICK) != 0) {
      let c = jni.call_void(e, info, "addAction", "(I)V", [ACCESS_CLICK as usize]);
      let d = jni.call_void(e, info, "setClickable", "(Z)V", [1 as usize]);
    }
    if ((it.actions & ACCESS_LONG_CLICK) != 0) {
      let c = jni.call_void(e, info, "addAction", "(I)V", [ACCESS_LONG_CLICK as usize]);
      let d = jni.call_void(e, info, "setLongClickable", "(Z)V", [1 as usize]);
    }
    if (Context.c_load_focused(ctx) == id) {
      let c = jni.call_void(e, info, "setAccessibilityFocused", "(Z)V", [1 as usize]);
      let d = jni.call_void(e, info, "addAction", "(I)V", [ACTION_CLEAR_ACCESSIBILITY_FOCUS as usize]);
    } else {
      let c = jni.call_void(e, info, "addAction", "(I)V", [ACTION_ACCESSIBILITY_FOCUS as usize]);
    }
    for r in [place, rect, rect_class, text, cls, pkg, class] {
      let f: usize = jni.delete_local_ref(e, r);
    }
    return info;
  }
}

// The action `action` of the service on the item `id`. It is true when the
// item takes the action.
pub(package) function access_action(env: usize, thiz: usize, id: i32, action: i32, args: usize): boolean {
  let ctx = static_context(env, thiz);
  if (ctx == 0 as usize) {
    return false;
  }
  unsafe {
    if (id == HOST_ID) {
      return call_bool(env, Context.c_load_host(ctx), "performAccessibilityAction", "(ILandroid/os/Bundle;)Z", [action as usize, args]);
    }
    let tree: usize = Context.c_load_tree(ctx);
    let off = item_offset(tree, id);
    if (off < 0) {
      return false;
    }
    if (action == ACTION_ACCESSIBILITY_FOCUS || action == ACTION_CLEAR_ACCESSIBILITY_FOCUS) {
      return focus_action(ctx, env, id, action == ACTION_ACCESSIBILITY_FOCUS);
    }
    if ((action == ACCESS_CLICK || action == ACCESS_LONG_CLICK) && (item_at(tree, off).actions & action) != 0) {
      click_action(ctx, env, id, action);
      return true;
    }
    return false;
  }
}

// Gives the accessibility focus to the item `id`, or takes it away. It is
// false when the focus does not change.
function focus_action(ctx: usize, env: usize, id: i32, take: boolean): boolean {
  unsafe {
    let focused: i32 = Context.c_load_focused(ctx);
    if (focused == id == take) {
      return false;
    }
    if (!take) {
      Context.c_store_focused(ctx, NO_ITEM);
      send_item_event(ctx, env, id, EVENT_FOCUS_CLEARED);
      return true;
    }
    Context.c_store_focused(ctx, id);
    if (focused != NO_ITEM) {
      send_item_event(ctx, env, focused, EVENT_FOCUS_CLEARED);
    }
    send_item_event(ctx, env, id, EVENT_FOCUSED);
    return true;
  }
}

// Gives the click or the long click `action` on the item `id` to the loop
// thread, and tells the service.
function click_action(ctx: usize, env: usize, id: i32, action: i32): void {
  unsafe {
    send(Context.c_load_activity(ctx), ACCESS, id as usize << 8 | action as usize);
  }
  let kind = EVENT_CLICKED;
  if (action == ACCESS_LONG_CLICK) {
    kind = EVENT_LONG_CLICKED;
  }
  send_item_event(ctx, env, id, kind);
}

// Releases the view that holds the tree, and the tree, on Destroy.
function close_access(e: usize, ctx: usize): void {
  unsafe {
    for r in [Context.c_load_host(ctx), Context.c_load_provider(ctx)] {
      if (r != 0 as usize) {
        let d: usize = jni.delete_global_ref(e, r);
      }
    }
    release_class(e, Context.c_load_delegate_class(ctx), ctx);
    release_class(e, Context.c_load_tree_class(ctx), ctx);
    let tree: usize = Context.c_load_tree(ctx);
    if (tree != 0 as usize) {
      free_bytes(tree);
    }
  }
}
```

### `windows`

`ANativeWindow_Buffer` has the width at 0, the height at 4, the stride in pixels at 8, and the pixels at 16. It is 48 bytes. `std/android/draw` draws on the frame of a locked window, and this module re-exports its `Frame` and `rgb`.

The first `lock` connects the app to the window as its producer of pixels, and the window keeps one producer at a time. So a media player, such as `std/android/player`, can show its frames on the window only after `release_cpu`. The app can lock the window again only after the player gives the window up.

The NDK has no function for `release_cpu`: it calls the `perform` entry of `struct ANativeWindow`, at 152 on LP64, with `NATIVE_WINDOW_API_DISCONNECT` (14) and `NATIVE_WINDOW_API_CPU` (2). The entry is part of the ABI between the platform and its graphics drivers, and the magic number at 0 is checked first. `surface` gives the Java `Surface` of the window, which a player takes.

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
    let buffer: usize = __alloc(WindowBuffer.c_size() as i32);
    if (c_window_lock(w.ptr, buffer, 0 as usize) != 0) {
      __free(buffer, WindowBuffer.c_size() as i32);
      return draw.Frame { window: w.ptr, buffer: 0 as usize, bits: 0 as usize, width: 0, height: 0, stride: 0 };
    }
    let b = WindowBuffer.c_load(buffer);
    return draw.Frame { window: w.ptr, buffer: buffer, bits: b.bits, width: b.width, height: b.height, stride: b.stride };
  }
}

// ANativeWindow_Buffer of android/native_window.h.
@derive(ffi.CLayout)
struct WindowBuffer { width: i32, height: i32, stride: i32, format: i32, bits: usize, reserved0: u32, reserved1: u32, reserved2: u32, reserved3: u32, reserved4: u32, reserved5: u32 }

// The start of struct ANativeWindow of the platform's system/window.h, to its
// perform function.
@derive(ffi.CLayout)
struct NativeWindow { magic: i32, version: i32, reserved0: usize, reserved1: usize, reserved2: usize, reserved3: usize, inc_ref: usize, dec_ref: usize, flags: u32, min_swap_interval: i32, max_swap_interval: i32, xdpi: f32, ydpi: f32, oem0: usize, oem1: usize, oem2: usize, oem3: usize, set_swap_interval: usize, dequeue_buffer: usize, lock_buffer: usize, queue_buffer: usize, query: usize, perform: usize }

// The magic number of struct ANativeWindow: '_wnd'.
const WINDOW_MAGIC: i32 = 1601662564;

const API_DISCONNECT: i32 = 14;

const API_CPU: i32 = 2;

// Gives the window up as the target of `lock`, so that another producer can
// connect to it. The next `lock` connects again. It is false when the
// window refuses, for example when no lock connected it.
pub function (w: Window) release_cpu(): boolean {
  unsafe {
    if (NativeWindow.c_load_magic(w.ptr) != WINDOW_MAGIC) {
      return false;
    }
    let perform: usize = NativeWindow.c_load_perform(w.ptr);
    return __c_call3(perform, w.ptr, API_DISCONNECT as usize, API_CPU as usize) as i32 == 0;
  }
}

// A local reference to the Java Surface of the window, for the JNIEnv `e`
// of the calling thread. It needs API level 26.
pub function (w: Window) surface(e: usize): usize {
  return c_window_to_surface(e, w.ptr);
}

// Shows the pixels of the frame and unlocks its window.
pub function post(f: draw.Frame): void {
  if (f.buffer == 0 as usize) {
    return;
  }
  let posted: i32 = c_window_post(f.window);
  unsafe {
    __free(f.buffer, WindowBuffer.c_size() as i32);
  }
}
```
