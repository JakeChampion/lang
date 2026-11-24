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
import "std/result";
import "std/android/widget/bindings";
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

// The action of the Intent that Android sent, or "".
pub function action(e: usize, intent: usize): string {
  if (intent == 0 as usize || !jni.push_local_frame(e, 32)) {
    return "";
  }
  defer jni.pop_local_frame(e, 0 as usize);
  let Ok(c) = bindings.intent_class(e) else {
    return "";
  };
  defer c.release(e);
  return c.get_action(e, bindings.Intent { ref: intent }).unwrap_or("");
}

function set_text(e: usize, c: bindings.Classes, views: bindings.RemoteViews, item: i32, text: string): Result[void, jni.JavaError] {
  let label = jni.checked_string(e, text)?;
  defer jni.delete_local_ref(e, label);
  return c.remote_views.set_text_view_text(e, views, FIRST_ITEM + item, jni.Object { ref: label });
}

function broadcast_button(e: usize, c: bindings.Classes, views: bindings.RemoteViews, context: bindings.Context, package: string, provider: string, item: i32, name: string): Result[void, jni.JavaError] {
  let intent = c.intent.new_string(e, name)?;
  defer jni.delete_local_ref(e, intent.ref);
  let aimed = c.intent.set_class_name(e, intent, package, provider)?;
  defer jni.delete_local_ref(e, aimed.ref);
  let pending = c.pending_intent.get_broadcast(e, context, item, intent, PENDING_FLAGS)?;
  if (pending.is_null()) {
    return Ok(());
  }
  defer jni.delete_local_ref(e, pending.ref);
  return c.remote_views.set_on_click_pending_intent(e, views, FIRST_ITEM + item, pending);
}

function launch_button(e: usize, c: bindings.Classes, views: bindings.RemoteViews, context: bindings.Context, item: i32, request: i32, intent: bindings.Intent): Result[void, jni.JavaError] {
  let pending = c.pending_intent.get_activity(e, context, request, intent, PENDING_FLAGS)?;
  if (pending.is_null()) {
    return Ok(());
  }
  defer jni.delete_local_ref(e, pending.ref);
  return c.remote_views.set_on_click_pending_intent(e, views, FIRST_ITEM + item, pending);
}

function action_button(e: usize, c: bindings.Classes, views: bindings.RemoteViews, context: bindings.Context, item: i32, launch: bindings.Intent, name: string): Result[void, jni.JavaError] {
  let intent = c.intent.new_intent(e, launch)?;
  defer jni.delete_local_ref(e, intent.ref);
  let aimed = c.intent.set_action(e, intent, name)?;
  defer jni.delete_local_ref(e, aimed.ref);
  return launch_button(e, c, views, context, item, 128 + item, intent);
}

function populate(e: usize, c: bindings.Classes, views: bindings.RemoteViews, context: bindings.Context, package: string, provider: string, look: Look): Result[boolean, jni.JavaError] {
  for pair in look.texts {
    let (item, text) = pair;
    set_text(e, c, views, item, text)?;
  }
  for pair in look.actions {
    let (item, name) = pair;
    broadcast_button(e, c, views, context, package, provider, item, name)?;
  }
  if (look.opens.len() > 0 || look.starts.len() > 0) {
    let manager = c.context.get_package_manager(e, context)?;
    if (manager.is_null()) {
      return Ok(false);
    }
    let launch = c.package_manager.get_launch_intent_for_package(e, manager, package)?;
    if (!launch.is_null()) {
      for item in look.opens {
        launch_button(e, c, views, context, item, 64 + item, launch)?;
      }
      for pair in look.starts {
        let (item, name) = pair;
        action_button(e, c, views, context, item, launch, name)?;
      }
    }
  }
  return Ok(true);
}

function update(e: usize, c: bindings.Classes, context: bindings.Context, provider: string, look: Look): Result[boolean, jni.JavaError] {
  let package = c.context.get_package_name(e, context)?;
  let views = c.remote_views.new(e, package, LAYOUT)?;
  let ready = populate(e, c, views, context, package, provider, look)?;
  if (!ready) {
    return Ok(false);
  }
  let manager = c.app_widget_manager.get_instance(e, context)?;
  if (manager.is_null()) {
    return Ok(false);
  }
  let component = c.component_name.new(e, package, provider)?;
  c.app_widget_manager.update_app_widget(e, manager, component, views)?;
  return Ok(true);
}

// Shows the layout on every widget for the given provider.
pub function show(e: usize, context: usize, provider: string, look: Look): boolean {
  if (context == 0 as usize || !jni.push_local_frame(e, 32)) {
    return false;
  }
  defer jni.pop_local_frame(e, 0 as usize);
  let Ok(c) = bindings.classes(e) else {
    return false;
  };
  defer c.release(e);
  return update(e, c, bindings.Context { ref: context }, provider, look).unwrap_or(false);
}
```
