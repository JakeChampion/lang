---
title: State and messages
description: How a std/android/ui app declares its messages as its own type, and how taps, text, timers, animation frames, the back key and Android events give them to update.
sidebar:
  order: 2
---

`update` is the one place where the state of the app changes. It gets a
message, and it returns the next state. This page shows how to declare
the messages, and what gives them to `update`.

## Your own message type

The messages are an enum that you declare. Each variant is one thing
that can occur:

```fern
enum Msg {
  Add,
  Open(i32),
  Named(string),
  Saved,
}
```

A variant can hold values. `Open(i32)` holds the number of a row, and
`Named(string)` holds the text of a field.

`view` returns `ui.El[Msg]`: a tree whose elements give messages of the
type `Msg`. The compiler finds the type from the result type of `view`,
so the elements do not write it:

```fern
function view(m: Model, s: ui.Screen): ui.El[Msg] {
  return ui.column([ui.text(m.title), ui.button("Add", Add)]);
}
```

`update` is a `match` on the message:

```fern
function update(own m: Model, msg: Msg): Model {
  match (msg) {
    Add => {
      return Model { ...m, count: m.count + 1 };
    },
    Open(row) => {
      return Model { ...m, open: row };
    },
    Named(text) => {
      return Model { ...m, name: text };
    },
    Saved => {
      return saved(m);
    }
  }
}
```

The compiler checks that the `match` handles each variant. When you add
a message, the compiler shows you each `match` that does not handle it.

A function that makes a part of the screen has the same result type:

```fern
function row(item: Item, i: i32): ui.El[Msg] {
  return ui.row([ui.text(item.title)]).on_tap(Open(i));
}
```

When a variant has the name of a variant of another enum in scope,
write the enum before it: `Msg.Tick`.

## What gives a message

| What occurs | How you give it a message |
|---|---|
| A tap on an element | `.on_tap(msg)`, or the message of `ui.button` |
| A finger on a slider | The function of `ui.slider`, or `.on_slide(slide)` |
| A long press on an element | `.on_long(msg)` |
| The text of a field changed | `.on_edit((text: string): Msg => Named(text))` |
| The action key of the keyboard, on a field | `.on_submit(msg)` |
| A time went by | `.with_tick(wait, msg)` on the program |
| A frame of the display | `.with_frames(animating, frame)` on the program |
| The back key | `.with_back(back)` on the program |
| Each other event of Android | `.with_system(system)` on the program |

An element or a program with no message for something gives nothing to
`update` when it occurs.

## Effects

`update` does nothing to the phone: it returns the next state only. An app
that plays a sound, reads a file or sends a request puts what it wants in
the state. `with_effects` takes a function that does it. The module calls
that function after each message, with the state, and the function
returns the state:

```fern
enum Want { Load(string), Stop }

struct Model { wants: Want[], ... }

function effects(m: Model): Model {
  for want in m.wants {
    // Do it on the phone.
  }
  return Model { ...m, wants: [] };
}

ui.program(view, update).with_effects(effects)
```

A test does not give the program that function, or gives one that does
nothing on a phone. Then the test reads `wants` from the state, and the
whole of `update` runs on your computer.
[`examples/android/tune`](https://github.com/JakeChampion/lang/tree/main/examples/android/tune)
is a music player that works this way.

## Timers

`with_tick` takes a function that gives the milliseconds to the next
tick, from the state, and the message of the tick. Return -1 for no
tick. A stopwatch that draws ten times a second while it runs:

```fern
ui.program(view, update).with_tick((m: Model): i32 => {
  if (m.running) {
    return 100;
  }
  return -1;
}, Ticked)
```

A phone that waits uses no power. Return -1 when nothing on the screen
changes.

## Animation

`with_frames` takes a function that tells whether the app animates, and
a function that makes a message from the time of a frame in
nanoseconds. While the first one returns true, `update` gets a message
for each frame of the display:

```fern
ui.program(view, update).with_frames((m: Model): boolean => m.moving, (ns: i64): Msg => Frame(ns))
```

`std/android/motion` has the curves of Android animations:

```fern
Frame(ns) => {
  let part = ((ns - m.started) as f64 / 300.0e6).min(1.0);
  return Model { ...m, shown: motion.ease(part), moving: part < 1.0 };
},
```

## The back key

The back key closes the app. When the app has more than one screen, it
must go back one screen. `with_back` takes a function that gives the
message of the back key, or `None` to let the back key close the app:

```fern
ui.program(view, update).with_back((m: Model): Option[Msg] => {
  if (m.screen == HOME) {
    return None;
  }
  return Some(WentBack);
})
```

## More than one screen

Keep the screen in the state, and choose the tree in `view`:

```fern
function view(m: Model, s: ui.Screen): ui.El[Msg] {
  if (m.screen == DETAIL) {
    return detail(m);
  }
  return home(m);
}
```

## Android events

`with_system` takes a function that makes a message from an event that
the module does not use itself: `android.Start`, `android.Resume`,
`android.Pause`, `android.Stop`, `android.Destroy`, and
`android.Posted` for the messages of other modules. Return `None` for
an event that you do not use. To save the state to a file when the app
goes to the background:

```fern
ui.program(view, update).with_system((e: android.Event): Option[Msg] => {
  match (e) {
    android.Pause => {
      return Some(Paused);
    },
    _ => {
      return None;
    }
  }
})
```

Other modules of `std/android` reach more of the phone: `audio`,
`player`, `video`, `service`, `image`, `fonts` and `p2p`. The standard
library reference lists them.
