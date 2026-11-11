# Running a user interface on a device

This module runs a program of `std/android/ui` in a NativeActivity app.

## Model

`run` is the loop of the app. It takes the context that `android.start` gave the loop thread, the first state, and the program. It returns the last state after Android destroys the activity.

```fern
import "std/android";
import "std/android/ui";
import "std/android/ui/app";

pub function ANativeActivity_onCreate(activity: usize, saved: usize, saved_size: usize): void {
  android.start(activity, app_main_entry());
}

@callback("app_main")
function app_main_entry(): usize;

function app_main(ctx: usize): usize {
  let last = app.run(ctx, Model { count: 0 }, ui.program((m: Model, s: ui.Screen): ui.El => view(m, s), (own m: Model, msg: ui.Msg): Model => update(m, msg)));
  return 0 as usize;
}
```

`run` makes the `Device` of the phone for the shell of `std/android/ui`:

- The text draws in the sans-serif font of the system, in the weights 400, 500 and 700. The APK holds no font.
- The frames draw on the GPU with `std/android/ui/painter`. On a device with no OpenGL ES 2, they draw on the CPU.
- A frame draws at the vsync after a change, through `android.request_frame`.
- The items of each frame go to the accessibility service through `std/android/access`. So TalkBack reads the screen, and a double tap on an item is a tap on its element.
- The theme is dark when the system is in night mode.
- A text field types with the keyboard of the system, through `std/android/keyboard`.

## Contracts

Call `run` on the loop thread. This module imports `std/android/access` and `std/android/keyboard`, which declare Java classes. So `fern -apk` writes a `classes.dex` for the app, and a program for the host cannot import this module. The tests of an app on the host use `std/android/ui/sim`.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<device>>

<<loop>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module runs a program of std/android/ui in a NativeActivity app.
import "std/android";
import "std/android/access";
import "std/android/keyboard";
import "std/android/text";
import "std/android/ui";
import "std/android/ui/painter";
import "std/android/ui/scene";
```

### `device`

The font matcher of Android 10 and later knows the family `sans-serif`. Older versions have files with the name of the family, which is Roboto.

```fern
<<device>>=
function weights(): i32[] {
  return [400, 500, 700];
}

// The sans-serif fonts of the system.
function system_fonts(ctx: usize): text.Fonts {
  let fs = text.system(ctx, "sans-serif", weights());
  if (fs.faces.len() > 0) {
    return fs;
  }
  return text.system(ctx, "Roboto", weights());
}

function present(own p: painter.Painter, window: usize, nodes: scene.Node[], background: i32): painter.Painter {
  if (painter.attached(p)) {
    return painter.present(p, [scene.Pass { nodes: nodes, dy: 0, alpha: 1.0 }], background);
  }
  return painter.present_cpu(p, window, nodes, background);
}

// The device of the activity of the context `ctx`.
function device(ctx: usize): ui.Device {
  return ui.Device {
    size: (window: usize): (i32, i32) => {
      let w = android.Window { ptr: window };
      return (w.width(), w.height());
    },
    density: (): f64 => android.density(ctx) as f64 / 160.0,
    dark: (): boolean => android.night_mode(ctx),
    now: (): i64 => monotonic_ns(),
    request_frame: (): void => android.request_frame(ctx),
    finish: (): void => android.finish(ctx),
    attach: (p: painter.Painter, window: usize): painter.Painter => painter.attach(p, window),
    detach: (p: painter.Painter): painter.Painter => painter.detach(p),
    close: (p: painter.Painter): painter.Painter => painter.close(p),
    present: (own p: painter.Painter, window: usize, nodes: scene.Node[], background: i32): painter.Painter => present(p, window, nodes, background),
    publish: (items: android.AccessItem[]): void => access.publish(ctx, items),
    show_keyboard: (text: string): void => keyboard.show(ctx, text, android.INPUT_TEXT, android.ACTION_DONE),
    set_keyboard: (text: string): void => keyboard.set_text(ctx, text),
    hide_keyboard: (): void => keyboard.hide(ctx),
  };
}
```

### `loop`

```fern
<<loop>>=
// Runs the program `p` from the state `state` until Android destroys the
// activity, and gives the last state.
pub function run[S](ctx: usize, own state: S, p: ui.Program[S]): S {
  let first = ui.shell(state, system_fonts(ctx), device(ctx));
  let last = android.run(ctx, first, (own s: ui.Shell[S], e: android.Event): ui.Shell[S] => ui.step(s, p, e), (s: ui.Shell[S]): i32 => ui.wait(s, p));
  return last.app;
}
```
