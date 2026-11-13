# Android app widgets

This module shows the texts and sets the buttons of the app widget of an app, on the home screen of the phone.

## Model

`fern -apk` puts a widget in the app when the app has two things. The first is the list `widget` of the `[android]` table of `fern.toml`, which names the items of the row of the widget: `text:title:Song` is a text, and `button:next:Next` is a button. The second is a @jni class that extends `android.appwidget.AppWidgetProvider`. Android sends that class a broadcast when the person adds the widget, and when the person taps a button of it.

The items have numbers from 0, in the order of the list. `show` gives each widget of the app its texts and the actions of its buttons. A tap on an item with an action sends the provider class a broadcast with that action. A tap on an item that `opens` starts the app. A tap on an item of `starts` starts the app with the action of the item. The app reads that action from the Intent that started it. An app uses `starts` for the buttons of a widget while the app does not run, because then no player can take a broadcast.

Android keeps what `show` gave it while the app does not run. So the widget shows the last state of the app, and its buttons start the process of the app when the process is not there.

The native `onReceive` of the provider class calls `action` to read the action of the broadcast. For the action `UPDATE`, it calls `show`. For the action of a button, it does the work of the button.

The module needs API level 23.

## Contracts

`show` takes the JNIEnv of the thread that calls it and a Context: the activity, or the Context of `onReceive`. `provider` is the full name of the provider class. An item number is from 0 to the number of the items less 1: Android ignores a different one.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<widget>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module shows the texts and sets the buttons of an app widget.
import "std/jni";
```

### `widget`

The numbers of the resources are those that `fern -apk` gives the layout of the widget and the first of its items.

```fern
<<widget>>=
// What a widget shows: the text of each item of `texts`, the action that
// each item of `actions` sends to the provider class, the items of
// `opens`, which start the app, and the items of `starts`, which start the
// app with an action.
pub struct Look { texts: (i32, string)[], actions: (i32, string)[], opens: i32[], starts: (i32, string)[] }

// The action of the broadcast that Android sends when the person adds a
// widget, and when the phone starts.
pub const UPDATE: string = "android.appwidget.action.APPWIDGET_UPDATE";

const LAYOUT: i32 = 2130837504;

const FIRST_ITEM: i32 = 2130968576;

// FLAG_IMMUTABLE and FLAG_UPDATE_CURRENT of PendingIntent.
const PENDING_FLAGS: i32 = 201326592;

const VIEWS: string = "android/widget/RemoteViews";

const PENDING: string = "android/app/PendingIntent";

function new_object(e: usize, class: usize, sig: string, args: usize[]): usize {
  let values: usize = jni.jvalues(args);
  let obj: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", sig), values);
  jni.free_buffer(values);
  return obj;
}

function call_static(e: usize, class: usize, name: string, sig: string, args: usize[]): usize {
  let values: usize = jni.jvalues(args);
  let out: usize = jni.call_static_object_method_a(e, class, jni.get_static_method_id(e, class, name, sig), values);
  jni.free_buffer(values);
  return out;
}

// True when the last JNI call threw. It clears the exception.
function threw(e: usize): boolean {
  if (jni.exception_occurred(e) == 0 as usize) {
    return false;
  }
  jni.exception_clear(e);
  return true;
}

// The action of the Intent `intent` of a broadcast, or "".
pub function action(e: usize, intent: usize): string {
  if (intent == 0 as usize) {
    return "";
  }
  let name: usize = jni.call_object(e, intent, "getAction", "()Ljava/lang/String;", []);
  if (threw(e) || name == 0 as usize) {
    return "";
  }
  let text: string = jni.string_of(e, name);
  let dropped: usize = jni.delete_local_ref(e, name);
  return text;
}

// Gives the views the PendingIntent `pending` for a tap on the item `item`.
function on_tap(e: usize, views: usize, item: i32, pending: usize): void {
  if (!threw(e) && pending != 0 as usize) {
    let set: boolean = jni.call_void(e, views, "setOnClickPendingIntent", "(IL" + PENDING + ";)V", [(FIRST_ITEM + item) as usize, pending]);
    let dropped: usize = jni.delete_local_ref(e, pending);
  }
}

// A copy of the Intent `launch` that starts the app with the action
// `name`.
function started_with(e: usize, launch: usize, name: string): usize {
  if (launch == 0 as usize) {
    return 0 as usize;
  }
  let intent: usize = new_object(e, jni.find_class(e, "android/content/Intent"), "(Landroid/content/Intent;)V", [launch]);
  let label: usize = jni.new_string(e, name);
  let aimed: usize = jni.call_object(e, intent, "setAction", "(Ljava/lang/String;)Landroid/content/Intent;", [label]);
  for local in [aimed, label] {
    let dropped: usize = jni.delete_local_ref(e, local);
  }
  return intent;
}

// The RemoteViews of `look` for the package `package` and the provider
// class `provider`.
function views_of(e: usize, context: usize, package: usize, provider: string, look: Look): usize {
  let views: usize = new_object(e, jni.find_class(e, VIEWS), "(Ljava/lang/String;I)V", [package, LAYOUT as usize]);
  for pair in look.texts {
    let (item, text) = pair;
    let label: usize = jni.new_string(e, text);
    let set: boolean = jni.call_void(e, views, "setTextViewText", "(ILjava/lang/CharSequence;)V", [(FIRST_ITEM + item) as usize, label]);
    let dropped: usize = jni.delete_local_ref(e, label);
  }
  let pending_class: usize = jni.find_class(e, PENDING);
  for pair in look.actions {
    let (item, name) = pair;
    let intent: usize = new_object(e, jni.find_class(e, "android/content/Intent"), "(Ljava/lang/String;)V", [jni.new_string(e, name)]);
    let aimed: usize = jni.call_object(e, intent, "setClassName", "(Ljava/lang/String;Ljava/lang/String;)Landroid/content/Intent;", [package, jni.new_string(e, provider)]);
    on_tap(e, views, item, call_static(e, pending_class, "getBroadcast", "(Landroid/content/Context;ILandroid/content/Intent;I)L" + PENDING + ";", [context, item as usize, intent, PENDING_FLAGS as usize]));
    for local in [intent, aimed] {
      let dropped: usize = jni.delete_local_ref(e, local);
    }
  }
  if (look.opens.len() > 0 || look.starts.len() > 0) {
    let manager: usize = jni.call_object(e, context, "getPackageManager", "()Landroid/content/pm/PackageManager;", []);
    let launch: usize = jni.call_object(e, manager, "getLaunchIntentForPackage", "(Ljava/lang/String;)Landroid/content/Intent;", [package]);
    for item in look.opens {
      on_tap(e, views, item, call_static(e, pending_class, "getActivity", "(Landroid/content/Context;ILandroid/content/Intent;I)L" + PENDING + ";", [context, (64 + item) as usize, launch, PENDING_FLAGS as usize]));
    }
    for pair in look.starts {
      let (item, name) = pair;
      let intent: usize = started_with(e, launch, name);
      on_tap(e, views, item, call_static(e, pending_class, "getActivity", "(Landroid/content/Context;ILandroid/content/Intent;I)L" + PENDING + ";", [context, (128 + item) as usize, intent, PENDING_FLAGS as usize]));
      let dropped: usize = jni.delete_local_ref(e, intent);
    }
    for local in [launch, manager] {
      let dropped: usize = jni.delete_local_ref(e, local);
    }
  }
  return views;
}

// Shows `look` on each widget of the app whose provider class is
// `provider`. `context` is the activity, or the Context of `onReceive`. It
// is false when Android does not take the views.
pub function show(e: usize, context: usize, provider: string, look: Look): boolean {
  let package: usize = jni.call_object(e, context, "getPackageName", "()Ljava/lang/String;", []);
  if (threw(e) || package == 0 as usize) {
    return false;
  }
  let views: usize = views_of(e, context, package, provider, look);
  if (threw(e) || views == 0 as usize) {
    return false;
  }
  let manager: usize = call_static(e, jni.find_class(e, "android/appwidget/AppWidgetManager"), "getInstance", "(Landroid/content/Context;)Landroid/appwidget/AppWidgetManager;", [context]);
  let component: usize = new_object(e, jni.find_class(e, "android/content/ComponentName"), "(Ljava/lang/String;Ljava/lang/String;)V", [package, jni.new_string(e, provider)]);
  let shown: boolean = manager != 0 as usize && jni.call_void(e, manager, "updateAppWidget", "(Landroid/content/ComponentName;L" + VIEWS + ";)V", [component, views]);
  let failed: boolean = threw(e);
  for local in [component, manager, views, package] {
    let dropped: usize = jni.delete_local_ref(e, local);
  }
  return shown && !failed;
}
```
