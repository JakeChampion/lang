---
title: Android apps in Fern
description: Write an Android app in Fern only — a state, a view and an update — and build a signed APK with one command and no Android SDK.
sidebar:
  order: 0
  label: Your first app
---

An Android app in Fern has no Java code and no XML. You write the state of
the app, a function that makes the screen from the state, and a function
that changes the state. `fern -apk` writes the signed APK. It needs no
Android SDK, no Gradle and no JDK.

`std/android/ui` does the work between your functions and the phone:

- It lays out the screen, in dp and sp.
- It draws on the GPU, with the font of the system.
- It finds what a touch is on, scrolls lists, and shows the keyboard.
- It follows the dark theme of the system.
- It gives the screen to TalkBack.

## A counter

This is a whole app. Save it as `app.fern`.

```fern
import "std/android";
import "std/android/ui";
import "std/android/ui/app";

struct Model { count: i32 }

function view(m: Model, s: ui.Screen): ui.El {
  return ui.column(
    [
      ui.text(m.count.to_string()).sized(96.0).weighted(700),
      ui.row([ui.outlined_button("Reset", "reset"), ui.button("Add one", "add")]).spaced(12.0),
    ],
  ).spaced(16.0).centered();
}

function update(own m: Model, msg: ui.Msg): Model {
  match (msg) {
    ui.Tap("add", _) => {
      return Model { count: m.count + 1 };
    },
    ui.Tap("reset", _) => {
      return Model { count: 0 };
    },
    _ => {
      return m;
    }
  }
}

pub function ANativeActivity_onCreate(activity: usize, saved: usize, saved_size: usize): void {
  android.start(activity, app_main_entry());
}

@callback("app_main")
function app_main_entry(): usize;

function app_main(ctx: usize): usize {
  let program = ui.program(view, update);
  let last = app.run(ctx, Model { count: 0 }, program);
  return 0 as usize;
}
```

The app has three parts:

- **`Model`** is the state. Here it is one number.
- **`view`** makes the screen from the state. It returns a tree of
  elements: a column with a text and a row of two buttons. It does not
  draw, and it has no positions in pixels.
- **`update`** takes the state and a message, and returns the next state.
  A tap on the button with the action `"add"` comes as `ui.Tap("add", _)`.

After each message, the module calls `view` again and draws the new
screen. Your code never changes the screen directly.

The last three declarations start the app. Android calls
`ANativeActivity_onCreate`, and `app.run` runs the program until Android
closes the activity. They are the same in each app.

## Build it and run it

```bash
fern -apk counter.apk app.fern
adb install counter.apk
```

`fern -apk` builds the app for arm64 phones and for x86-64 emulators,
and signs the APK. The first build makes a signing key next to the APK,
in `counter.key.pem`. Keep that file: Android installs an update only
when the same key signs it.

While you work on the app, one command builds it, installs it, starts
it, and shows its log:

```bash
fern -apk counter.apk -apk-run app.fern
```

## Settings of the app

A `fern.toml` next to `app.fern` gives the name of the app and what it
needs from Android:

```toml
[package]
name = "counter"
lib = "app.fern"

[android]
package = "dev.example.counter"
label = "Counter"
icon = "icon.png"
min_sdk = 26
permissions = ["INTERNET"]
```

## Where to go next

| Page | What it covers |
|---|---|
| [Screens](./screens/) | Elements, sizes, colors, buttons, lists that scroll, text fields, pictures, and your own drawing |
| [State and messages](./messages/) | Every message, timers, animation, the back key, and the events of Android |
| [Testing](./testing/) | Tests that tap buttons and type text on your computer, with no phone |

Complete apps are in
[`examples/android`](https://github.com/JakeChampion/lang/tree/main/examples/android):
`counter` is this page, and `todo` has a text field and a list.
