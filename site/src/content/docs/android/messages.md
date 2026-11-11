---
title: State and messages
description: Every message that update can get in a std/android/ui app — taps, text, timers, animation frames, the back key and Android events.
sidebar:
  order: 2
---

`update` is the one place where the state of the app changes. This page
lists the messages that it gets, and the settings of a program that
send more of them.

## The messages

| Message | When it comes |
|---|---|
| `ui.Tap(name, n)` | A tap on an element with `.on_tap(name)` or `.on_tap_with(name, n)` |
| `ui.LongPress(name, n)` | A long press on an element with `.on_long(name, n)` |
| `ui.Edit(field, text)` | The text of a field changed |
| `ui.Submit(field)` | The action key of the keyboard, on a field |
| `ui.Back` | The back key, when the program handles it |
| `ui.Tick` | The time of `with_wait` went by |
| `ui.Frame(ns)` | A frame of the display, while `with_animating` is true |
| `ui.System(event)` | Each other event of Android, as an `android.Event` |

`update` is a `match` on the message. End it with a `_` case that
returns the state as it is, so a message that you do not use changes
nothing.

## Timers

`with_wait` gives the milliseconds to the next `ui.Tick`, from the
state. Return -1 for no tick. A stopwatch that draws ten times a second
while it runs:

```fern
ui.program(view, update).with_wait((m: Model): i32 => {
  if (m.running) {
    return 100;
  }
  return -1;
})
```

A phone that waits uses no power. Return -1 when nothing on the screen
changes.

## Animation

While `with_animating` returns true, `update` gets `ui.Frame(ns)` for
each frame of the display. `ns` is the time of the frame in
nanoseconds. `std/android/motion` has the curves of Android animations:

```fern
ui.Frame(ns) => {
  let part = ((ns - m.started) as f64 / 300.0e6).min(1.0);
  return Model { ...m, shown: motion.ease(part), moving: part < 1.0 };
},
```

```fern
ui.program(view, update).with_animating((m: Model): boolean => m.moving)
```

## The back key

The back key closes the app. When the app has more than one screen, it
must go back one screen:

```fern
ui.program(view, update).with_back((m: Model): boolean => m.screen != HOME)
```

While `with_back` returns true, the back key comes to `update` as
`ui.Back`. While it returns false, the back key closes the app.

## More than one screen

Keep the screen in the state, and choose the tree in `view`:

```fern
function view(m: Model, s: ui.Screen): ui.El {
  if (m.screen == DETAIL) {
    return detail(m);
  }
  return home(m);
}
```

## Android events

`ui.System(event)` has the events that the module does not use itself:
`android.Start`, `android.Resume`, `android.Pause`, `android.Stop`,
`android.Destroy`, and `android.Posted` for the messages of other
modules. Save the state to a file on `android.Pause`.

Other modules of `std/android` reach more of the phone: `audio`,
`player`, `video`, `service`, `image`, `fonts` and `p2p`. The standard
library reference lists them.
