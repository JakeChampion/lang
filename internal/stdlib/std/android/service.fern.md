# Android foreground services

This module runs a foreground service for a NativeActivity app of `std/android`, and keeps the CPU and the Wi-Fi radio awake for it.

## Model

A foreground service keeps the process of the app at a high priority while its window is not on the screen. Its notification stays in the status bar. The app declares a @jni class that extends `android.app.Service`, with a native `onBind` that returns null and a native `onStartCommand` that calls `command`. `fern -apk` then declares the service, with the types of the FOREGROUND_SERVICE_TYPE permissions of the app.

From API level 31, a notice can require an unlocked phone for the action of the app. Then a person who holds a locked phone cannot use the action.

A button of the notification sends an intent to the service. `command` then sends a message to the live loop of the app with `android.tell`. The loop gives it to the app as a `Posted` event. When no loop runs, no message goes. `event` gives `Stopped` or `Action` for that event.

`start_media` starts the service with the token of a media session of `std/android/session`. Its notification is then the media controls of the app in the notification panel, and a tap on it opens the app. A notice with no label for the stop action has no stop action.

The module needs API level 26, for the notification channel. From API level 29, `startForeground` takes the types of the manifest.

The module calls Java only through `std/android/service/bindings`, which `jnibind` writes from `bindings.spec`.

## Contracts

The activity is not valid after its Destroy event, so the app calls `stop` before that. Call each function except `command` on the loop thread.

`stop` does not call `stopService`. Android stops an app when a service that `startForegroundService` started stops before its `onStartCommand` calls `startForeground`. A short task can end in that time. `stop` thus sends a finish command with `startService`, and Android gives the service that command after the start command. When Android refuses `startService`, `stop` calls `stopService`.

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
import "std/android/service/bindings";
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

// An action in the media notification. The service handles its intent.
pub struct MediaButton { label: string, action: string, icon: i32 }

const STOP_ACTION: string = "dev.fern.android.STOP";

const APP_ACTION: string = "dev.fern.android.ACTION";

// The action of the command that `stop` sends.
const FINISH_ACTION: string = "dev.fern.android.FINISH";

// The extra of the start intent that holds the token of a media session.
const SESSION_EXTRA: string = "dev.fern.android.SESSION";

// The ID of the one notification of the service.
const NOTICE_ID: i32 = 1;

// Starts a local frame and makes the classes of one call. None when
// Android gives no frame or does not find a class.
function begin(e: usize): Option[bindings.Classes] {
  if (!jni.push_local_frame(e, 96)) {
    return None;
  }
  match (bindings.classes(e)) {
    Ok(c) => {
      return Some(c);
    },
    Err(_) => {
      let _ = jni.pop_local_frame(e, 0 as usize);
      return None;
    }
  }
}

// Releases the classes and ends the local frame of `begin`.
function end(e: usize, c: bindings.Classes): void {
  c.release(e);
  let _ = jni.pop_local_frame(e, 0 as usize);
}

// The class `name` of the app, which the class loader of `context` loads.
function load(e: usize, c: bindings.Classes, context: bindings.Context, name: string): Result[bindings.Class, jni.JavaError] {
  let loader = c.context.get_class_loader(e, context)?;
  return c.class_loader.load_class(e, loader, name);
}

// A Java string for a CharSequence parameter.
function text(e: usize, value: string): Result[jni.Object, jni.JavaError] {
  return Ok(jni.Object { ref: jni.checked_string(e, value)? });
}

// Starts the foreground service `class`, the Java name of the app's @jni
// subclass of Service. It is false when Android refuses, for example while
// the app is in the background. Call it on the loop thread.
pub function start(ctx: usize, class: string): boolean {
  return start_media(ctx, class, 0 as usize);
}

function started(e: usize, c: bindings.Classes, activity: bindings.Context, class: string, session: usize): Result[boolean, jni.JavaError] {
  let intent = c.intent.new(e, activity, load(e, c, activity, class)?)?;
  if (session != 0 as usize) {
    let with_session = c.intent.put_extra(e, intent, SESSION_EXTRA, jni.Object { ref: session })?;
  }
  let name = c.context.start_foreground_service(e, activity, intent)?;
  return Ok(!name.is_null());
}

// Starts the foreground service `class` with the notification of the media
// session whose token is `session`, from `std/android/session`. Android
// shows that notification as the media controls of the app.
pub function start_media(ctx: usize, class: string, session: usize): boolean {
  let e: usize = android.env(ctx);
  let Some(c) = begin(e) else {
    return false;
  };
  defer end(e, c);
  return match (started(e, c, bindings.Context { ref: android.activity_object(ctx) }, class, session)) {
    Ok(ok) => ok,
    Err(_) => false,
  };
}

function stopped(e: usize, c: bindings.Classes, activity: bindings.Context, class: string): Result[void, jni.JavaError] {
  let target = load(e, c, activity, class)?;
  let finish = c.intent.new(e, activity, target)?;
  let with_action = c.intent.set_action(e, finish, FINISH_ACTION)?;
  let sent = match (c.context.start_service(e, activity, finish)) {
    Ok(name) => !name.is_null(),
    Err(_) => false,
  };
  if (sent) {
    return Ok(());
  }
  let intent = c.intent.new(e, activity, target)?;
  let gone = c.context.stop_service(e, activity, intent)?;
  return Ok(());
}

// Stops the foreground service `class`, and removes its notification. Call
// it on the loop thread.
pub function stop(ctx: usize, class: string): void {
  let e: usize = android.env(ctx);
  let Some(c) = begin(e) else {
    return;
  };
  defer end(e, c);
  let _ = stopped(e, c, bindings.Context { ref: android.activity_object(ctx) }, class);
}

// Shows the notification `n` in place of the notification of the running
// foreground service `class`. Call it on the loop thread.
pub function update(ctx: usize, class: string, n: Notice): void {
  update_media(ctx, class, n, 0 as usize);
}

// Shows the notification `n` of the media session whose token is `session`
// in place of the notification of the running foreground service `class`.
pub function update_media(ctx: usize, class: string, n: Notice, session: usize): void {
  publish_media(ctx, class, n, session, []);
}

function notifications(e: usize, c: bindings.Classes, context: bindings.Context): Result[bindings.NotificationManager, jni.JavaError] {
  let manager = c.context.get_system_service(e, context, bindings.CONTEXT_NOTIFICATION_SERVICE)?;
  return Ok(bindings.NotificationManager { ref: manager.ref });
}

function published(e: usize, c: bindings.Classes, activity: bindings.Context, class: string, n: Notice, session: usize, buttons: MediaButton[]): Result[void, jni.JavaError] {
  let notification = notification_of(e, c, activity, load(e, c, activity, class)?, n, session, buttons)?;
  return c.notification_manager.notify(e, notifications(e, c, activity)?, NOTICE_ID, notification);
}

pub function publish_media(ctx: usize, class: string, n: Notice, session: usize, buttons: MediaButton[]): void {
  let e = android.env(ctx);
  let Some(c) = begin(e) else {
    return;
  };
  defer end(e, c);
  let _ = published(e, c, bindings.Context { ref: android.activity_object(ctx) }, class, n, session, buttons);
}

// The onStartCommand of the app's service: `env`, `service` and `intent`
// are its arguments. It shows the notification `n` and makes the service a
// foreground service. For the stop action of the notification, it stops
// the service and sends Stopped to the loop thread. For the action of
// the app, it sends Action. Return its value from onStartCommand.
pub function command(e: usize, service: usize, intent: usize, n: Notice): i32 {
  return command_media(e, service, intent, n, []);
}

pub function command_media(e: usize, service: usize, intent: usize, n: Notice, buttons: MediaButton[]): i32 {
  let Some(c) = begin(e) else {
    return bindings.SERVICE_START_NOT_STICKY;
  };
  defer end(e, c);
  let _ = commanded(e, c, bindings.Service { ref: service }, bindings.Intent { ref: intent }, n, buttons);
  return bindings.SERVICE_START_NOT_STICKY;
}

function commanded(e: usize, c: bindings.Classes, service: bindings.Service, intent: bindings.Intent, n: Notice, buttons: MediaButton[]): Result[void, jni.JavaError] {
  if (intent.is_null()) {
    return c.service.stop_self(e, service);
  }
  let action: string = android.intent_action(e, intent.ref);
  if (action == STOP_ACTION) {
    let gone = c.service.stop_self(e, service);
    let sent = android.tell(e, STOPPED, 0 as usize);
    return Ok(());
  }
  if (action == APP_ACTION) {
    let sent = android.tell(e, ACTION, 0 as usize);
    return Ok(());
  }
  if (action == FINISH_ACTION) {
    return c.service.stop_self(e, service);
  }
  let session = c.intent.get_parcelable_extra(e, intent, SESSION_EXTRA)?;
  let context = bindings.Context { ref: service.ref };
  let class = bindings.Class { ref: jni.get_object_class(e, service.ref) };
  let notification = notification_of(e, c, context, class, n, session.ref, buttons)?;
  // From API level 29, startForeground takes the types of the manifest.
  if (c.service.has_start_foreground_int_notification_int()) {
    return c.service.start_foreground_int_notification_int(e, service, NOTICE_ID, notification, bindings.SERVICE_INFO_FOREGROUND_SERVICE_TYPE_MANIFEST);
  }
  return c.service.start_foreground_int_notification(e, service, NOTICE_ID, notification);
}

// The global references of a partial wake lock and a Wi-Fi lock.
pub struct Awake { wake: usize, wifi: usize }

// A global reference to a partial wake lock that is held, or 0.
function wake_lock(e: usize, c: bindings.Classes, activity: bindings.Context, tag: string): Result[usize, jni.JavaError] {
  let power = c.context.get_system_service(e, activity, bindings.CONTEXT_POWER_SERVICE)?;
  if (power.is_null()) {
    return Ok(0 as usize);
  }
  let lock = c.power_manager.new_wake_lock(e, bindings.PowerManager { ref: power.ref }, bindings.POWER_MANAGER_PARTIAL_WAKE_LOCK, tag)?;
  if (lock.is_null()) {
    return Ok(0 as usize);
  }
  c.power_manager_wake_lock.acquire(e, lock)?;
  return Ok(jni.new_global_ref(e, lock.ref));
}

// A global reference to a Wi-Fi lock that is held, or 0.
function wifi_lock(e: usize, c: bindings.Classes, activity: bindings.Context, tag: string): Result[usize, jni.JavaError] {
  let app = c.context.get_application_context(e, activity)?;
  let manager = c.context.get_system_service(e, app, bindings.CONTEXT_WIFI_SERVICE)?;
  if (manager.is_null()) {
    return Ok(0 as usize);
  }
  let lock = c.wifi_manager.create_wifi_lock(e, bindings.WifiManager { ref: manager.ref }, bindings.WIFI_MANAGER_WIFI_MODE_FULL_HIGH_PERF, tag)?;
  if (lock.is_null()) {
    return Ok(0 as usize);
  }
  c.wifi_manager_wifi_lock.acquire(e, lock)?;
  return Ok(jni.new_global_ref(e, lock.ref));
}

// Keeps the CPU and the Wi-Fi radio awake while the screen is off, so a
// socket keeps its datagrams on time. It needs the WAKE_LOCK permission.
// Call it on the loop thread, and release_awake when the work stops.
pub function stay_awake(ctx: usize, tag: string): Awake {
  let e: usize = android.env(ctx);
  let Some(c) = begin(e) else {
    return Awake { wake: 0 as usize, wifi: 0 as usize };
  };
  defer end(e, c);
  let activity = bindings.Context { ref: android.activity_object(ctx) };
  let wake = match (wake_lock(e, c, activity, tag)) { Ok(lock) => lock, Err(_) => 0 as usize };
  let wifi = match (wifi_lock(e, c, activity, tag)) { Ok(lock) => lock, Err(_) => 0 as usize };
  return Awake { wake: wake, wifi: wifi };
}

pub function release_awake(ctx: usize, a: Awake): void {
  let e: usize = android.env(ctx);
  if (a.wake != 0 as usize) {
    if let Ok(locks) = bindings.power_manager_wake_lock_class(e) {
      let _ = locks.release_(e, bindings.PowerManager_WakeLock { ref: a.wake });
      locks.release(e);
    }
    jni.delete_global_ref(e, a.wake);
  }
  if (a.wifi != 0 as usize) {
    if let Ok(locks) = bindings.wifi_manager_wifi_lock_class(e) {
      let _ = locks.release_(e, bindings.WifiManager_WifiLock { ref: a.wifi });
      locks.release(e);
    }
    jni.delete_global_ref(e, a.wifi);
  }
}

// Makes the channel of `n` with the low importance: no sound and no pop-up.
function make_channel(e: usize, c: bindings.Classes, context: bindings.Context, n: Notice): Result[void, jni.JavaError] {
  let channel = c.notification_channel.new(e, n.channel, text(e, n.channel_name)?, bindings.NOTIFICATION_MANAGER_IMPORTANCE_LOW)?;
  return c.notification_manager.create_notification_channel(e, notifications(e, c, context)?, channel);
}

// The intent that a button of the notification sends to the service `class`
// with the action `action`.
function action_intent(e: usize, c: bindings.Classes, context: bindings.Context, class: bindings.Class, action: string): Result[bindings.PendingIntent, jni.JavaError] {
  let intent = c.intent.new(e, context, class)?;
  let same = c.intent.set_action(e, intent, action)?;
  return c.pending_intent.get_service(e, context, 0, intent, bindings.PENDING_INTENT_FLAG_IMMUTABLE | bindings.PENDING_INTENT_FLAG_UPDATE_CURRENT);
}

// A button of a notification that runs `pending` only on an unlocked phone.
// It needs API level 31.
function unlocked_action(e: usize, c: bindings.Classes, icon: i32, label: string, pending: bindings.PendingIntent): Result[jni.Object, jni.JavaError] {
  let builder = c.notification_action_builder.new(e, icon, text(e, label)?, pending)?;
  let same = c.notification_action_builder.set_authentication_required(e, builder, true)?;
  return c.notification_action_builder.build(e, builder);
}

// The intent that opens the app, for a tap on a notification.
function open_intent(e: usize, c: bindings.Classes, context: bindings.Context): Result[bindings.PendingIntent, jni.JavaError] {
  let packages = c.context.get_package_manager(e, context)?;
  let name = c.context.get_package_name(e, context)?;
  let launch = c.package_manager.get_launch_intent_for_package(e, packages, name)?;
  return c.pending_intent.get_activity(e, context, 0, launch, bindings.PENDING_INTENT_FLAG_IMMUTABLE | bindings.PENDING_INTENT_FLAG_UPDATE_CURRENT);
}

// Makes the notification of `builder` the media controls of the session
// whose token is `session`. A tap on it opens the app.
function media_style(e: usize, c: bindings.Classes, context: bindings.Context, builder: bindings.Notification_Builder, session: usize, compact: i32): Result[void, jni.JavaError] {
  let style = c.notification_media_style.new(e)?;
  let same = c.notification_media_style.set_media_session(e, style, jni.Object { ref: session })?;
  if (compact > 0) {
    let positions = jni.new_int_array(e, compact);
    jni.check(e)?;
    jni.set_int_array_region(e, positions, 0, [0, 1, 2].take(compact));
    let updated = c.notification_media_style.set_show_actions_in_compact_view(e, style, jni.Object { ref: positions })?;
  }
  let styled = c.notification_builder.set_style(e, builder, style.object())?;
  let icon = c.notification_builder.set_small_icon(e, builder, bindings.R_DRAWABLE_ic_media_play)?;
  let opens = c.notification_builder.set_content_intent(e, builder, open_intent(e, c, context)?)?;
  return Ok(());
}

// Gives `builder` one button with the icon `icon` and the label `label`,
// which sends `action` to the service `class`.
function add_button(e: usize, c: bindings.Classes, builder: bindings.Notification_Builder, context: bindings.Context, class: bindings.Class, icon: i32, label: string, action: string): Result[void, jni.JavaError] {
  let pending = action_intent(e, c, context, class, action)?;
  let same = c.notification_builder.add_action_int_char_sequence_pending_intent(e, builder, icon, text(e, label)?, pending)?;
  return Ok(());
}

// Gives `builder` the action of the app and the stop action of `n`.
function add_actions(e: usize, c: bindings.Classes, builder: bindings.Notification_Builder, context: bindings.Context, class: bindings.Class, n: Notice): Result[void, jni.JavaError] {
  let icon = bindings.R_DRAWABLE_ic_btn_speak_now;
  if (n.action.len() > 0 && n.unlock && c.notification_action_builder.has_set_authentication_required()) {
    let action = unlocked_action(e, c, icon, n.action, action_intent(e, c, context, class, APP_ACTION)?)?;
    let same = c.notification_builder.add_action_notification_action(e, builder, action)?;
  } else if (n.action.len() > 0) {
    add_button(e, c, builder, context, class, icon, n.action, APP_ACTION)?;
  }
  if (n.stop.len() > 0) {
    add_button(e, c, builder, context, class, bindings.R_DRAWABLE_ic_menu_close_clear_cancel, n.stop, STOP_ACTION)?;
  }
  return Ok(());
}

// The notification `n` of the service `class`, built in `context`: the
// service, or the activity. It is ongoing. It has its stop action when `n`
// has a label for it. With the token `session` of a media session, it is
// the media controls of that session.
function notification_of(e: usize, c: bindings.Classes, context: bindings.Context, class: bindings.Class, n: Notice, session: usize, buttons: MediaButton[]): Result[bindings.Notification, jni.JavaError] {
  make_channel(e, c, context, n)?;
  let builder = c.notification_builder.new(e, context, n.channel)?;
  let titled = c.notification_builder.set_content_title(e, builder, text(e, n.title)?)?;
  let worded = c.notification_builder.set_content_text(e, builder, text(e, n.text)?)?;
  let icon = c.notification_builder.set_small_icon(e, builder, bindings.R_DRAWABLE_ic_btn_speak_now)?;
  let ongoing = c.notification_builder.set_ongoing(e, builder, true)?;
  add_actions(e, c, builder, context, class, n)?;
  if (session != 0 as usize) {
    for button in buttons {
      add_button(e, c, builder, context, class, button.icon, button.label, button.action)?;
    }
    let offset = if (n.action.len() > 0 || n.stop.len() > 0) { 0 } else { buttons.len().min(3) };
    media_style(e, c, context, builder, session, offset)?;
  }
  return c.notification_builder.build(e, builder);
}
```
