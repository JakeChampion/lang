# Android launcher shortcuts

This module gives an app its shortcuts in the launcher, for a NativeActivity app of `std/android`.

## Model

A long press on the icon of an app shows its shortcuts. A shortcut has an id, a label, and an action. A tap on it starts the activity of the app with an intent that has that action.

`set` gives the launcher the shortcuts of the app. They stay until the next `set`, also while the app does not run.

`action` gives the action of the intent that started the activity. An activity that the icon of the app started has the action `android.intent.action.MAIN`. `action` takes the action, so the next call gives "" until a new intent comes. Thus an app can call it at each Resume event, and do the action of each intent one time.

`fern -apk` gives the activity the launch mode `singleTask`. If the activity already runs, Android sends the new intent to its `onNewIntent` method. `NativeActivity` does not keep that intent. An app that wants it declares a subclass of `NativeActivity` with `onNewIntent`, and calls `keep_intent` there. Android calls `onNewIntent` before the Resume event.

`set` and `action` need API level 25. `pin` needs API level 26. The launcher can ask the user to confirm the pin.

## Contracts

Call `keep_intent` on the UI thread. Call each other function on the loop thread, while the activity is valid. `action` gives "" while the loop has no activity. An action has the name of the package of the app before it, such as `dev.fern.tune.SHUFFLE`. A launcher shows at most four shortcuts of an app.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<shortcuts>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module gives an app its shortcuts in the launcher.
import "std/android";
import "std/android/shortcuts/bindings";
import "std/result";
import "std/jni";
```

### `shortcuts`

The intent of a shortcut names the activity of the app as its component. The module looks up the SDK methods through the generated bindings. Each shortcut gets a JNI local frame. The list keeps its objects after each local frame ends.

```fern
<<shortcuts>>=
// A shortcut: the id that the launcher knows it by, the label that the
// person sees, and the action of the intent that it starts the app with.
pub struct Shortcut { id: string, label: string, action: string }

function push_frame(e: usize): boolean {
  if (jni.call1(e, 19, 32 as usize) as i32 != 0) {
    let cleared = jni.exception_clear(e);
    return false;
  }
  return true;
}

function pop_frame(e: usize): void {
  let popped = jni.call1(e, 20, 0 as usize);
}

function build_item(e: usize, c: bindings.Classes, activity: bindings.Activity, item: Shortcut): Result[bindings.ShortcutInfo, jni.JavaError] {
  let intent = c.intent.new(e, item.action)?;
  let component = c.activity.get_component_name(e, activity)?;
  let aimed = c.intent.set_component(e, intent, component)?;
  let builder = c.shortcut_info_builder.new(e, activity.as_context(), item.id)?;
  let label = jni.new_string(e, item.label);
  jni.check(e)?;
  let labeled = c.shortcut_info_builder.set_short_label(e, builder, jni.Object { ref: label })?;
  let pointed = c.shortcut_info_builder.set_intent(e, labeled, aimed)?;
  return c.shortcut_info_builder.build(e, pointed);
}

function append_item(e: usize, c: bindings.Classes, activity: bindings.Activity, list: bindings.ArrayList, item: Shortcut): Result[boolean, jni.JavaError] {
  if (!push_frame(e)) {
    return Ok(false);
  }
  defer pop_frame(e);
  let shortcut = build_item(e, c, activity, item)?;
  return c.array_list.add(e, list, shortcut.object());
}

// Gives the launcher the shortcuts in place of those it has.
pub function set(ctx: usize, items: Shortcut[]): boolean {
  let e = android.env(ctx);
  if (!push_frame(e)) {
    return false;
  }
  defer pop_frame(e);
  let Ok(c) = bindings.classes(e) else {
    return false;
  };
  defer c.release(e);
  let activity = bindings.Activity { ref: android.activity_object(ctx) };
  let Ok(service) = c.context.get_system_service(e, activity.as_context(), "shortcut") else {
    return false;
  };
  if (service.ref == 0 as usize) {
    return false;
  }
  let Ok(list) = c.array_list.new(e) else {
    return false;
  };
  for item in items {
    if (!append_item(e, c, activity, list, item).unwrap_or(false)) {
      return false;
    }
  }
  return c.shortcut_manager.set_dynamic_shortcuts(e, bindings.ShortcutManager { ref: service.ref }, list.as_list()).unwrap_or(false);
}

// Asks the launcher to pin a shortcut to the home screen.
pub function pin(ctx: usize, item: Shortcut): boolean {
  if (android.detached(ctx)) {
    return false;
  }
  let e = android.env(ctx);
  if (!push_frame(e)) {
    return false;
  }
  defer pop_frame(e);
  let Ok(c) = bindings.classes(e) else {
    return false;
  };
  defer c.release(e);
  let activity = bindings.Activity { ref: android.activity_object(ctx) };
  let Ok(service) = c.context.get_system_service(e, activity.as_context(), "shortcut") else {
    return false;
  };
  if (service.ref == 0 as usize) {
    return false;
  }
  let Ok(info) = build_item(e, c, activity, item) else {
    return false;
  };
  return c.shortcut_manager.request_pin_shortcut(e, bindings.ShortcutManager { ref: service.ref }, info, jni.Object { ref: 0 as usize }).unwrap_or(false);
}

// Takes the action of the intent, so the next call gives "".
pub function action(ctx: usize): string {
  if (android.detached(ctx)) {
    return "";
  }
  let e = android.env(ctx);
  if (!push_frame(e)) {
    return "";
  }
  defer pop_frame(e);
  let Ok(activity) = bindings.activity_class(e) else {
    return "";
  };
  defer activity.release(e);
  let Ok(intent) = activity.get_intent(e, bindings.Activity { ref: android.activity_object(ctx) }) else {
    return "";
  };
  if (intent.is_null()) {
    return "";
  }
  let Ok(c) = bindings.intent_class(e) else {
    return "";
  };
  defer c.release(e);
  let Ok(name) = c.get_action(e, intent) else {
    return "";
  };
  let cleared = c.set_action(e, intent, None);
  return name;
}

// Keeps the intent from onNewIntent for the next Resume.
pub function keep_intent(env: usize, activity: usize, intent: usize): void {
  if (!push_frame(env)) {
    return;
  }
  defer pop_frame(env);
  let Ok(c) = bindings.activity_class(env) else {
    return;
  };
  defer c.release(env);
  let kept = c.set_intent(env, bindings.Activity { ref: activity }, bindings.Intent { ref: intent });
}
```
