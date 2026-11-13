# Android foreground services

This module runs a foreground service for a NativeActivity app of `std/android`, and keeps the CPU and the Wi-Fi radio awake for it.

## Model

A foreground service keeps the process of the app at a high priority while its window is not on the screen. Its notification stays in the status bar. The app declares a @jni class that extends `android.app.Service`, with a native `onBind` that returns null and a native `onStartCommand` that calls `command`. `fern -apk` then declares the service, with the types of the FOREGROUND_SERVICE_TYPE permissions of the app.

From API level 31, a notice can require an unlocked phone for the action of the app. Then a person who holds a locked phone cannot use the action.

A button of the notification sends an intent to the service. `command` then sends a message to the live loop of the app with `android.tell`. The loop gives it to the app as a `Posted` event. When no loop runs, no message goes. `event` gives `Stopped` or `Action` for that event.

`start_media` starts the service with the token of a media session of `std/android/session`. Its notification is then the media controls of the app in the notification panel, and a tap on it opens the app. A notice with no label for the stop action has no stop action.

The module needs API level 26, for the notification channel. From API level 29, `startForeground` takes the types of the manifest.

## Contracts

The activity is not valid after its Destroy event, so the app calls `stop` before that. Call each function except `command` on the loop thread.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<events>>

<<the service>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module runs a foreground service for a NativeActivity app.
import "std/android";
import "std/jni";
```

### `events`

```fern
<<events>>=
// The user tapped the stop action of the notification, or the action of
// the app in it.
pub enum Event {
  Stopped,
  Action,
}

const STOPPED: i32 = 13;

const ACTION: i32 = 17;

// The event of the service in the event `e` of the loop, or None.
pub function event(e: android.Event): Option[Event] {
  match (e) {
    android.Posted(kind, _) => {
      if (kind == STOPPED) {
        return Some(Stopped);
      }
      if (kind == ACTION) {
        return Some(Action);
      }
      return None;
    },
    _ => {
      return None;
    }
  }
}
```

### `the service`

```fern
<<the service>>=
// The notification of a foreground service: the channel ID and its name,
// the title and the text, the label of the action of the app or "" for no
// action, and the label of the stop action or "" for no stop action. When `unlock` is set, the
// action of the app needs an unlocked phone, from API level 31.
pub struct Notice { channel: string, channel_name: string, title: string, text: string, action: string, stop: string, unlock: boolean }

const STOP_ACTION: string = "dev.fern.android.STOP";

const APP_ACTION: string = "dev.fern.android.ACTION";

// START_NOT_STICKY: the system does not start the service again after it
// stops the process.
const NOT_STICKY: i32 = 2;

// android.R.drawable.ic_btn_speak_now and ic_menu_close_clear_cancel.
const ICON: usize = 17301668;

const STOP_ICON: usize = 17301560;

// android.R.drawable.ic_media_play.
const MEDIA_ICON: usize = 17301540;

// The extra of the start intent that holds the token of a media session.
const SESSION_EXTRA: string = "dev.fern.android.SESSION";

// A new intent from `context` to `class`.
function service_intent(e: usize, context: usize, class: usize): usize {
  let intent_class: usize = jni.find_class(e, "android/content/Intent");
  let args: usize = jni.jvalues([context, class]);
  let intent: usize = jni.new_object_a(e, intent_class, jni.get_method_id(e, intent_class, "<init>", "(Landroid/content/Context;Ljava/lang/Class;)V"), args);
  jni.free_buffer(args);
  return intent;
}

// Starts the foreground service `class`, the Java name of the app's @jni
// subclass of Service. It is false when Android refuses, for example while
// the app is in the background. Call it on the loop thread.
pub function start(ctx: usize, class: string): boolean {
  return start_media(ctx, class, 0 as usize);
}

// Starts the foreground service `class` with the notification of the media
// session whose token is `session`, from `std/android/session`. Android
// shows that notification as the media controls of the app.
pub function start_media(ctx: usize, class: string, session: usize): boolean {
  let e: usize = android.env(ctx);
  let obj: usize = android.activity_object(ctx);
  let intent: usize = service_intent(e, obj, jni.load_class(e, obj, class));
  if (session != 0 as usize) {
    let with_session: usize = jni.call_object(e, intent, "putExtra", "(Ljava/lang/String;Landroid/os/Parcelable;)Landroid/content/Intent;", [jni.new_string(e, SESSION_EXTRA), session]);
  }
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
pub function stop(ctx: usize, class: string): void {
  let e: usize = android.env(ctx);
  let obj: usize = android.activity_object(ctx);
  let intent: usize = service_intent(e, obj, jni.load_class(e, obj, class));
  let stop: usize = jni.get_method_id(e, jni.get_object_class(e, obj), "stopService", "(Landroid/content/Intent;)Z");
  let args: usize = jni.jvalues([intent]);
  let stopped: i32 = jni.call_boolean_method_a(e, obj, stop, args);
  jni.free_buffer(args);
}

// Shows the notification `n` in place of the notification of the running
// foreground service `class`. Call it on the loop thread.
pub function update(ctx: usize, class: string, n: Notice): void {
  update_media(ctx, class, n, 0 as usize);
}

// Shows the notification `n` of the media session whose token is `session`
// in place of the notification of the running foreground service `class`.
pub function update_media(ctx: usize, class: string, n: Notice, session: usize): void {
  let e: usize = android.env(ctx);
  let obj: usize = android.activity_object(ctx);
  let notification: usize = notification_of(e, obj, jni.load_class(e, obj, class), n, session);
  let manager: usize = jni.call_object(e, obj, "getSystemService", "(Ljava/lang/String;)Ljava/lang/Object;", [jni.new_string(e, "notification")]);
  let posted: boolean = jni.call_void(e, manager, "notify", "(ILandroid/app/Notification;)V", [1 as usize, notification]);
}

// The onStartCommand of the app's service: `env`, `service` and `intent`
// are its arguments. It shows the notification `n` and makes the service a
// foreground service. For the stop action of the notification, it stops
// the service and sends Stopped to the loop thread. For the action of
// the app, it sends Action. Return its value from onStartCommand.
pub function command(e: usize, service: usize, intent: usize, n: Notice): i32 {
  if (intent == 0 as usize) {
    stop_self(e, service);
    return NOT_STICKY;
  }
  let action: string = android.intent_action(e, intent);
  if (action == STOP_ACTION) {
    stop_self(e, service);
    let sent = android.tell(e, STOPPED, 0 as usize);
    return NOT_STICKY;
  }
  if (action == APP_ACTION) {
    let sent = android.tell(e, ACTION, 0 as usize);
    return NOT_STICKY;
  }
  let session: usize = jni.call_object(e, intent, "getParcelableExtra", "(Ljava/lang/String;)Landroid/os/Parcelable;", [jni.new_string(e, SESSION_EXTRA)]);
  let notification: usize = notification_of(e, service, jni.get_object_class(e, service), n, session);
  start_foreground(e, service, notification);
  return NOT_STICKY;
}

// The global references of a partial wake lock and a Wi-Fi lock.
pub struct Awake { wake: usize, wifi: usize }

// Keeps the CPU and the Wi-Fi radio awake while the screen is off, so a
// socket keeps its datagrams on time. It needs the WAKE_LOCK permission.
// Call it on the loop thread, and release_awake when the work stops.
pub function stay_awake(ctx: usize, tag: string): Awake {
  let e: usize = android.env(ctx);
  let obj: usize = android.activity_object(ctx);
  let service: string = "(Ljava/lang/String;)Ljava/lang/Object;";
  let power: usize = jni.call_object(e, obj, "getSystemService", service, [jni.new_string(e, "power")]);
  // PowerManager.PARTIAL_WAKE_LOCK
  let wake: usize = held(e, jni.call_object(e, power, "newWakeLock", "(ILjava/lang/String;)Landroid/os/PowerManager$WakeLock;", [1 as usize, jni.new_string(e, tag)]));
  let app: usize = jni.call_object(e, obj, "getApplicationContext", "()Landroid/content/Context;", []);
  let wifi_manager: usize = jni.call_object(e, app, "getSystemService", service, [jni.new_string(e, "wifi")]);
  let wifi: usize = 0 as usize;
  if (wifi_manager != 0 as usize) {
    // WifiManager.WIFI_MODE_FULL_HIGH_PERF
    wifi = held(e, jni.call_object(e, wifi_manager, "createWifiLock", "(ILjava/lang/String;)Landroid/net/wifi/WifiManager$WifiLock;", [3 as usize, jni.new_string(e, tag)]));
  }
  return Awake { wake: wake, wifi: wifi };
}

// Acquires the lock `lock`, and gives a global reference to it, or 0.
function held(e: usize, lock: usize): usize {
  if (lock == 0 as usize || !jni.call_void(e, lock, "acquire", "()V", [])) {
    return 0 as usize;
  }
  return jni.new_global_ref(e, lock);
}

pub function release_awake(ctx: usize, a: Awake): void {
  let e: usize = android.env(ctx);
  for lock in [a.wake, a.wifi] {
    if (lock != 0 as usize) {
      let released: boolean = jni.call_void(e, lock, "release", "()V", []);
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
function action_intent(e: usize, context: usize, class: usize, action: string): usize {
  let intent: usize = service_intent(e, context, class);
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

// A button of a notification that runs `pending` only on an unlocked phone.
// It needs API level 31.
function unlocked_action(e: usize, icon: usize, label: string, pending: usize): usize {
  let class: usize = jni.find_class(e, "android/app/Notification$Action$Builder");
  let args: usize = jni.jvalues([icon, jni.new_string(e, label), pending]);
  let builder: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", "(ILjava/lang/CharSequence;Landroid/app/PendingIntent;)V"), args);
  jni.free_buffer(args);
  build_step(e, builder, "setAuthenticationRequired", "(Z)Landroid/app/Notification$Action$Builder;", [1 as usize]);
  let none: usize = jni.jvalue_alloc(0);
  let action: usize = jni.call_object_method_a(e, builder, jni.get_method_id(e, class, "build", "()Landroid/app/Notification$Action;"), none);
  jni.free_buffer(none);
  return action;
}

// The intent that opens the app, for a tap on a notification.
function open_intent(e: usize, context: usize): usize {
  let packages: usize = jni.call_object(e, context, "getPackageManager", "()Landroid/content/pm/PackageManager;", []);
  let name: usize = jni.call_object(e, context, "getPackageName", "()Ljava/lang/String;", []);
  let launch: usize = jni.call_object(e, packages, "getLaunchIntentForPackage", "(Ljava/lang/String;)Landroid/content/Intent;", [name]);
  let pending_class: usize = jni.find_class(e, "android/app/PendingIntent");
  let get: usize = jni.get_static_method_id(e, pending_class, "getActivity", "(Landroid/content/Context;ILandroid/content/Intent;I)Landroid/app/PendingIntent;");
  // FLAG_IMMUTABLE | FLAG_UPDATE_CURRENT
  let args: usize = jni.jvalues([context, 0 as usize, launch, 201326592 as usize]);
  let pending: usize = jni.call_static_object_method_a(e, pending_class, get, args);
  jni.free_buffer(args);
  return pending;
}

// Makes the notification of `builder` the media controls of the session
// whose token is `session`. A tap on it opens the app.
function media_style(e: usize, context: usize, builder: usize, session: usize): void {
  let ret = ")Landroid/app/Notification$Builder;";
  let style_class: usize = jni.find_class(e, "android/app/Notification$MediaStyle");
  let none: usize = jni.jvalue_alloc(0);
  let style: usize = jni.new_object_a(e, style_class, jni.get_method_id(e, style_class, "<init>", "()V"), none);
  jni.free_buffer(none);
  let same: usize = jni.call_object(e, style, "setMediaSession", "(Landroid/media/session/MediaSession$Token;)Landroid/app/Notification$MediaStyle;", [session]);
  build_step(e, builder, "setStyle", "(Landroid/app/Notification$Style;" + ret, [style]);
  build_step(e, builder, "setSmallIcon", "(I" + ret, [MEDIA_ICON]);
  build_step(e, builder, "setContentIntent", "(Landroid/app/PendingIntent;" + ret, [open_intent(e, context)]);
}

// The notification `n` of the service `class`, built in `context`: the
// service, or the activity. It is ongoing. It has its stop action when `n`
// has a label for it. With the token `session` of a media session, it is
// the media controls of that session.
function notification_of(e: usize, context: usize, class: usize, n: Notice, session: usize): usize {
  make_channel(e, context, n);
  let builder_class: usize = jni.find_class(e, "android/app/Notification$Builder");
  let args: usize = jni.jvalues([context, jni.new_string(e, n.channel)]);
  let builder: usize = jni.new_object_a(e, builder_class, jni.get_method_id(e, builder_class, "<init>", "(Landroid/content/Context;Ljava/lang/String;)V"), args);
  jni.free_buffer(args);
  let ret = ")Landroid/app/Notification$Builder;";
  build_step(e, builder, "setContentTitle", "(Ljava/lang/CharSequence;" + ret, [jni.new_string(e, n.title)]);
  build_step(e, builder, "setContentText", "(Ljava/lang/CharSequence;" + ret, [jni.new_string(e, n.text)]);
  build_step(e, builder, "setSmallIcon", "(I" + ret, [ICON]);
  build_step(e, builder, "setOngoing", "(Z" + ret, [1 as usize]);
  if (n.action.len() > 0 && n.unlock && sdk_int(e) >= 31) {
    build_step(e, builder, "addAction", "(Landroid/app/Notification$Action;" + ret, [unlocked_action(e, ICON, n.action, action_intent(e, context, class, APP_ACTION))]);
  } else if (n.action.len() > 0) {
    build_step(e, builder, "addAction", "(ILjava/lang/CharSequence;Landroid/app/PendingIntent;" + ret, [ICON, jni.new_string(e, n.action), action_intent(e, context, class, APP_ACTION)]);
  }
  if (n.stop.len() > 0) {
    build_step(e, builder, "addAction", "(ILjava/lang/CharSequence;Landroid/app/PendingIntent;" + ret, [STOP_ICON, jni.new_string(e, n.stop), action_intent(e, context, class, STOP_ACTION)]);
  }
  if (session != 0 as usize) {
    media_style(e, context, builder, session);
  }
  let none: usize = jni.jvalue_alloc(0);
  let notification: usize = jni.call_object_method_a(e, builder, jni.get_method_id(e, builder_class, "build", "()Landroid/app/Notification;"), none);
  jni.free_buffer(none);
  return notification;
}
```
