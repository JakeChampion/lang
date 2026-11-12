# Android launcher shortcuts

This module gives an app its shortcuts in the launcher, for a NativeActivity app of `std/android`.

## Model

A long press on the icon of an app shows its shortcuts. A shortcut has an id, a label, and an action. A tap on it starts the activity of the app with an intent that has that action.

`set` gives the launcher the shortcuts of the app. They stay until the next `set`, also while the app does not run.

`action` gives the action of the intent that started the activity. An activity that the icon of the app started has the action `android.intent.action.MAIN`.

The module needs API level 25.

## Contracts

Call each function on the loop thread, while the activity is valid. An action has the name of the package of the app before it, such as `dev.fern.tune.SHUFFLE`. A launcher shows at most four shortcuts of an app.

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
import "std/jni";
```

### `shortcuts`

The intent of a shortcut names the activity of the app as its component, so only that activity can get it.

```fern
<<shortcuts>>=
// A shortcut: the id that the launcher knows it by, the label that the
// person sees, and the action of the intent that it starts the app with.
pub struct Shortcut { id: string, label: string, action: string }

const BUILDER: string = "android/content/pm/ShortcutInfo$Builder";

function new_object(e: usize, class: usize, sig: string, args: usize[]): usize {
  let values: usize = jni.jvalues(args);
  let obj: usize = jni.new_object_a(e, class, jni.get_method_id(e, class, "<init>", sig), values);
  jni.free_buffer(values);
  return obj;
}

// True when the last JNI call threw. It clears the exception.
function threw(e: usize): boolean {
  if (jni.exception_occurred(e) == 0 as usize) {
    return false;
  }
  jni.exception_clear(e);
  return true;
}

// The ShortcutInfo of `item` for the activity `activity`.
function info(e: usize, activity: usize, item: Shortcut): usize {
  let intent: usize = new_object(e, jni.find_class(e, "android/content/Intent"), "(Ljava/lang/String;)V", [jni.new_string(e, item.action)]);
  let component: usize = jni.call_object(e, activity, "getComponentName", "()Landroid/content/ComponentName;", []);
  let aimed: usize = jni.call_object(e, intent, "setComponent", "(Landroid/content/ComponentName;)Landroid/content/Intent;", [component]);
  let builder: usize = new_object(e, jni.find_class(e, BUILDER), "(Landroid/content/Context;Ljava/lang/String;)V", [activity, jni.new_string(e, item.id)]);
  let labeled: usize = jni.call_object(e, builder, "setShortLabel", "(Ljava/lang/CharSequence;)L" + BUILDER + ";", [jni.new_string(e, item.label)]);
  let pointed: usize = jni.call_object(e, builder, "setIntent", "(Landroid/content/Intent;)L" + BUILDER + ";", [intent]);
  let built: usize = jni.call_object(e, builder, "build", "()Landroid/content/pm/ShortcutInfo;", []);
  for local in [intent, component, aimed, builder, labeled, pointed] {
    let dropped: usize = jni.delete_local_ref(e, local);
  }
  return built;
}

// Gives the launcher the shortcuts `items` of the app, in the place of
// those that it has. It is false when Android does not take them.
pub function set(ctx: usize, items: Shortcut[]): boolean {
  let e: usize = android.env(ctx);
  let activity: usize = android.activity_object(ctx);
  let manager: usize = jni.call_object(e, activity, "getSystemService", "(Ljava/lang/String;)Ljava/lang/Object;", [jni.new_string(e, "shortcut")]);
  if (threw(e) || manager == 0 as usize) {
    return false;
  }
  let list: usize = new_object(e, jni.find_class(e, "java/util/ArrayList"), "()V", []);
  let add: usize = jni.get_method_id(e, jni.get_object_class(e, list), "add", "(Ljava/lang/Object;)Z");
  for item in items {
    let one: usize = info(e, activity, item);
    if (threw(e) || one == 0 as usize) {
      return false;
    }
    let values: usize = jni.jvalues([one]);
    let added: i32 = jni.call_boolean_method_a(e, list, add, values);
    jni.free_buffer(values);
    let dropped: usize = jni.delete_local_ref(e, one);
  }
  let method: usize = jni.get_method_id(e, jni.get_object_class(e, manager), "setDynamicShortcuts", "(Ljava/util/List;)Z");
  let values: usize = jni.jvalues([list]);
  let taken: i32 = jni.call_boolean_method_a(e, manager, method, values);
  jni.free_buffer(values);
  let failed: boolean = threw(e);
  for local in [list, manager] {
    let dropped: usize = jni.delete_local_ref(e, local);
  }
  return taken != 0 && !failed;
}

// The action of the intent that started the activity, or "".
pub function action(ctx: usize): string {
  let e: usize = android.env(ctx);
  let intent: usize = jni.call_object(e, android.activity_object(ctx), "getIntent", "()Landroid/content/Intent;", []);
  if (threw(e) || intent == 0 as usize) {
    return "";
  }
  let name: usize = jni.call_object(e, intent, "getAction", "()Ljava/lang/String;", []);
  if (threw(e) || name == 0 as usize) {
    return "";
  }
  let text: string = jni.string_of(e, name);
  for local in [name, intent] {
    let dropped: usize = jni.delete_local_ref(e, local);
  }
  return text;
}
```
