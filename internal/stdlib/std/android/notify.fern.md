# Notifications

This module shows the notifications of an app in the status bar and in the notification panel.

## Model

A notification goes to a channel. The person sets the sound, the vibration and the importance of each channel in the settings of the app. `channel` makes a channel, or changes its name, with the importance that the app suggests. Make each channel before the first notification on it.

`post` shows the notification `n`, or changes the notification with the same ID. A tap on it opens the app, unless `open` is false. `progress` from 0 to 100 shows a progress bar, and -1 shows none. An `ongoing` notification stays until the app cancels it. `cancel` takes the notification with an ID away.

From Android 13 an app shows notifications only with the permission `POST_NOTIFICATIONS`. Declare it with `-apk-permission POST_NOTIFICATIONS` or in the `[android]` table. `allowed` tells whether the app can show notifications now, and `ask` shows the dialog of that permission.

## Contracts

Call each function on the loop thread, with the context that `launch.start` gave the loop. Channels need API level 26. Below it, `channel` does nothing, and `post` uses no channel.

`post` gives false and shows nothing when `allowed` is false or Android rejects the notification. The person can turn the notifications of an app off at each time, so `allowed` can change while the app runs.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<notes>>

<<calls>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module shows the notifications of an app.
import "std/android";
import "std/jni";
import "std/android/notify/bindings";
import "std/result";
```

### `notes`

```fern
<<notes>>=
// The importance of a channel: no sound, the default sound, and a pop-up
// with the sound.
pub const LOW: i32 = 2;

pub const DEFAULT: i32 = 3;

pub const HIGH: i32 = 4;

pub struct Channel { id: string, name: string, importance: i32 }

// A notification. `icon` is the resource ID of a drawable for the status
// bar, or 0 for the icon of information of the system.
pub struct Note { id: i32, channel: string, title: string, text: string, progress: i32, ongoing: boolean, open: boolean, icon: i32 }

// A notification on `channel` with no progress bar. A tap on it opens the
// app, and the person can take it away.
pub function note(id: i32, channel: string, title: string, text: string): Note {
  return Note { id: id, channel: channel, title: title, text: text, progress: -1, ongoing: false, open: true, icon: 0 };
}

// android.R.drawable.ic_dialog_info.
const INFO_ICON: i32 = 17301659;

const PERMISSION: string = "android.permission.POST_NOTIFICATIONS";
```

### `calls`

```fern
<<calls>>=
function push_frame(e: usize): boolean {
  if (jni.call1(e, 19, 48 as usize) as i32 != 0) {
    let cleared = jni.exception_clear(e);
    return false;
  }
  return true;
}

function pop_frame(e: usize): void {
  let popped = jni.call1(e, 20, 0 as usize);
}

function activity(ctx: usize): bindings.Context {
  return bindings.Context { ref: android.activity_object(ctx) };
}

function manager(e: usize, obj: bindings.Context): Result[bindings.NotificationManager, jni.JavaError] {
  let c = bindings.context_class(e)?;
  defer c.release(e);
  let service = c.get_system_service(e, obj, "notification")?;
  if (service.ref == 0 as usize) {
    return Err(jni.JavaError { text: "notification service unavailable" });
  }
  return Ok(bindings.NotificationManager { ref: service.ref });
}

function create_channel(e: usize, obj: bindings.Context, channel: Channel): Result[void, jni.JavaError] {
  let service = manager(e, obj)?;
  let c = bindings.notification_channel_class(e)?;
  defer c.release(e);
  let name = jni.checked_string(e, channel.name)?;
  let made = c.new(e, channel.id, jni.Object { ref: name }, channel.importance)?;
  let m = bindings.notification_manager_class(e)?;
  defer m.release(e);
  return m.create_notification_channel(e, service, made);
}

pub function channel(ctx: usize, c: Channel): void {
  if (android.sdk_version(ctx) < 26) {
    return;
  }
  let e = android.env(ctx);
  if (push_frame(e)) {
    defer pop_frame(e);
    let _ = create_channel(e, activity(ctx), c);
  }
}

function permission(ctx: usize): boolean {
  return android.sdk_version(ctx) < 33 || android.has_permission(ctx, PERMISSION);
}

function enabled(e: usize, obj: bindings.Context): Result[boolean, jni.JavaError] {
  let service = manager(e, obj)?;
  let m = bindings.notification_manager_class(e)?;
  defer m.release(e);
  return m.are_notifications_enabled(e, service);
}

pub function allowed(ctx: usize): boolean {
  let e = android.env(ctx);
  if (!permission(ctx) || !push_frame(e)) {
    return false;
  }
  defer pop_frame(e);
  return enabled(e, activity(ctx)).unwrap_or(false);
}

// Requests permission from Android 13 onwards. The answer comes with the next Resume.
pub function ask(ctx: usize): void {
  if (!permission(ctx)) {
    android.request_permissions(ctx, [PERMISSION]);
  }
}

function open_intent(e: usize, obj: bindings.Context): Result[bindings.PendingIntent, jni.JavaError] {
  let c = bindings.context_class(e)?;
  defer c.release(e);
  let packages = c.get_package_manager(e, obj)?;
  if (packages.is_null()) {
    return Err(jni.JavaError { text: "package manager unavailable" });
  }
  let name = c.get_package_name(e, obj)?;
  let pm = bindings.package_manager_class(e)?;
  defer pm.release(e);
  let launch = pm.get_launch_intent_for_package(e, packages, name)?;
  if (launch.is_null()) {
    return Ok(bindings.PendingIntent { ref: 0 as usize });
  }
  let pending = bindings.pending_intent_class(e)?;
  defer pending.release(e);
  // FLAG_IMMUTABLE | FLAG_UPDATE_CURRENT
  return pending.get_activity(e, obj, 0, launch, 201326592);
}

function set_content(e: usize, c: bindings.Notification_BuilderClass, builder: bindings.Notification_Builder, n: Note): Result[void, jni.JavaError] {
  let icon = if (n.icon != 0) { n.icon } else { INFO_ICON };
  c.set_small_icon(e, builder, icon)?;
  let title = jni.checked_string(e, n.title)?;
  c.set_content_title(e, builder, jni.Object { ref: title })?;
  let text = jni.checked_string(e, n.text)?;
  c.set_content_text(e, builder, jni.Object { ref: text })?;
  c.set_ongoing(e, builder, n.ongoing)?;
  c.set_auto_cancel(e, builder, !n.ongoing)?;
  return Ok(());
}

function built(ctx: usize, e: usize, obj: bindings.Context, n: Note): Result[bindings.Notification, jni.JavaError] {
  let c = bindings.notification_builder_class(e)?;
  defer c.release(e);
  let made = if (android.sdk_version(ctx) < 26) { c.new_context(e, obj) } else { c.new_context_string(e, obj, n.channel) };
  let builder = made?;
  set_content(e, c, builder, n)?;
  if (n.progress >= 0) {
    c.set_progress(e, builder, 100, n.progress.min(100), false)?;
  }
  if (n.open) {
    let pending = open_intent(e, obj)?;
    c.set_content_intent(e, builder, pending)?;
  }
  return c.build(e, builder);
}

function publish(ctx: usize, e: usize, n: Note): Result[boolean, jni.JavaError] {
  let obj = activity(ctx);
  let service = manager(e, obj)?;
  let m = bindings.notification_manager_class(e)?;
  defer m.release(e);
  let on = m.are_notifications_enabled(e, service)?;
  if (!on) {
    return Ok(false);
  }
  let note = built(ctx, e, obj, n)?;
  m.notify(e, service, n.id, note)?;
  return Ok(true);
}

pub function post(ctx: usize, n: Note): boolean {
  let e = android.env(ctx);
  if (!permission(ctx) || !push_frame(e)) {
    return false;
  }
  defer pop_frame(e);
  return publish(ctx, e, n).unwrap_or(false);
}

function remove(e: usize, obj: bindings.Context, id: i32): Result[void, jni.JavaError] {
  let service = manager(e, obj)?;
  let m = bindings.notification_manager_class(e)?;
  defer m.release(e);
  return m.cancel(e, service, id);
}

pub function cancel(ctx: usize, id: i32): void {
  let e = android.env(ctx);
  if (push_frame(e)) {
    defer pop_frame(e);
    let _ = remove(e, activity(ctx), id);
  }
}
```
