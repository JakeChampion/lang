# Notifications

This module shows the notifications of an app in the status bar and in the notification panel.

## Model

A notification goes to a channel. The person sets the sound, the vibration and the importance of each channel in the settings of the app. `channel` makes a channel, or changes its name, with the importance that the app suggests. Make each channel before the first notification on it.

`post` shows the notification `n`, or changes the notification with the same ID. A tap on it opens the app, unless `open` is false. `progress` from 0 to 100 shows a progress bar, and -1 shows none. An `ongoing` notification stays until the app cancels it. `cancel` takes the notification with an ID away.

From Android 13 an app shows notifications only with the permission `POST_NOTIFICATIONS`. Declare it with `-apk-permission POST_NOTIFICATIONS` or in the `[android]` table. `allowed` tells whether the app can show notifications now, and `ask` shows the dialog of that permission.

## Contracts

Call each function on the loop thread, with the context that `launch.start` gave the loop. Channels need API level 26. Below it, `channel` does nothing, and `post` uses no channel.

`post` gives false and shows nothing when `allowed` is false. The person can turn the notifications of an app off at each time, so `allowed` can change while the app runs.

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
function manager(e: usize, obj: usize): usize {
  return jni.call_object(e, obj, "getSystemService", "(Ljava/lang/String;)Ljava/lang/Object;", [jni.new_string(e, "notification")]);
}

pub function channel(ctx: usize, c: Channel): void {
  if (android.sdk_version(ctx) < 26) {
    return;
  }
  let e: usize = android.env(ctx);
  let obj: usize = android.activity_object(ctx);
  let class: usize = jni.find_class(e, "android/app/NotificationChannel");
  let args: usize = jni.jvalues([jni.new_string(e, c.id), jni.new_string(e, c.name), c.importance as usize]);
  let made: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", "(Ljava/lang/String;Ljava/lang/CharSequence;I)V"), args);
  jni.free_buffer(args);
  let done: boolean = jni.call_void(e, manager(e, obj), "createNotificationChannel", "(Landroid/app/NotificationChannel;)V", [made]);
}

pub function allowed(ctx: usize): boolean {
  if (android.sdk_version(ctx) >= 33 && !android.has_permission(ctx, PERMISSION)) {
    return false;
  }
  let e: usize = android.env(ctx);
  let m: usize = manager(e, android.activity_object(ctx));
  let none: usize = jni.jvalue_alloc(0);
  let on: i32 = jni.call_boolean_method_a(e, m, jni.get_method_id(e, jni.get_object_class(e, m), "areNotificationsEnabled", "()Z"), none);
  jni.free_buffer(none);
  return on != 0;
}

// Shows the dialog of the permission to show notifications, from Android
// 13. The answer comes with the next Resume.
pub function ask(ctx: usize): void {
  if (android.sdk_version(ctx) >= 33 && !android.has_permission(ctx, PERMISSION)) {
    android.request_permissions(ctx, [PERMISSION]);
  }
}

// Calls the builder method `name` with the signature `sig` and the
// arguments `values`. Each one gives the builder back.
function build_step(e: usize, builder: usize, name: string, sig: string, values: usize[]): void {
  let same: usize = jni.call_object(e, builder, name, sig, values);
}

// The intent that opens the app, for a tap on a notification.
function open_intent(e: usize, obj: usize): usize {
  let packages: usize = jni.call_object(e, obj, "getPackageManager", "()Landroid/content/pm/PackageManager;", []);
  let name: usize = jni.call_object(e, obj, "getPackageName", "()Ljava/lang/String;", []);
  let launch: usize = jni.call_object(e, packages, "getLaunchIntentForPackage", "(Ljava/lang/String;)Landroid/content/Intent;", [name]);
  let class: usize = jni.find_class(e, "android/app/PendingIntent");
  let get: usize = jni.get_static_method_id(e, class, "getActivity", "(Landroid/content/Context;ILandroid/content/Intent;I)Landroid/app/PendingIntent;");
  // FLAG_IMMUTABLE | FLAG_UPDATE_CURRENT
  let args: usize = jni.jvalues([obj, 0 as usize, launch, 201326592 as usize]);
  let pending: usize = jni.call_static_object_method_a(e, class, get, args);
  jni.free_buffer(args);
  return pending;
}

// A builder of a notification on the channel `channel`, from API level 26.
function builder_of(ctx: usize, e: usize, obj: usize, class: usize, channel: string): usize {
  if (android.sdk_version(ctx) < 26) {
    let one: usize = jni.jvalues([obj]);
    let old: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", "(Landroid/content/Context;)V"), one);
    jni.free_buffer(one);
    return old;
  }
  let args: usize = jni.jvalues([obj, jni.new_string(e, channel)]);
  let made: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", "(Landroid/content/Context;Ljava/lang/String;)V"), args);
  jni.free_buffer(args);
  return made;
}

function built(ctx: usize, e: usize, obj: usize, n: Note): usize {
  let builder = builder_of(ctx, e, obj, jni.find_class(e, "android/app/Notification$Builder"), n.channel);
  let ret = ")Landroid/app/Notification$Builder;";
  let icon = if (n.icon != 0) { n.icon } else { INFO_ICON };
  build_step(e, builder, "setSmallIcon", "(I" + ret, [icon as usize]);
  build_step(e, builder, "setContentTitle", "(Ljava/lang/CharSequence;" + ret, [jni.new_string(e, n.title)]);
  build_step(e, builder, "setContentText", "(Ljava/lang/CharSequence;" + ret, [jni.new_string(e, n.text)]);
  build_step(e, builder, "setOngoing", "(Z" + ret, [if (n.ongoing) { 1 as usize } else { 0 as usize }]);
  build_step(e, builder, "setAutoCancel", "(Z" + ret, [if (n.ongoing) { 0 as usize } else { 1 as usize }]);
  if (n.progress >= 0) {
    build_step(e, builder, "setProgress", "(IIZ" + ret, [100 as usize, n.progress.min(100) as usize, 0 as usize]);
  }
  if (n.open) {
    build_step(e, builder, "setContentIntent", "(Landroid/app/PendingIntent;" + ret, [open_intent(e, obj)]);
  }
  return jni.call_object(e, builder, "build", "()Landroid/app/Notification;", []);
}

pub function post(ctx: usize, n: Note): boolean {
  if (!allowed(ctx)) {
    return false;
  }
  let e: usize = android.env(ctx);
  let obj: usize = android.activity_object(ctx);
  let shown: boolean = jni.call_void(e, manager(e, obj), "notify", "(ILandroid/app/Notification;)V", [n.id as usize, built(ctx, e, obj, n)]);
  return true;
}

pub function cancel(ctx: usize, id: i32): void {
  let e: usize = android.env(ctx);
  let done: boolean = jni.call_void(e, manager(e, android.activity_object(ctx)), "cancel", "(I)V", [id as usize]);
}
```
