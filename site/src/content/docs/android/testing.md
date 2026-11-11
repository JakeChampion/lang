---
title: Testing
description: Run a std/android/ui app in a test on your computer — tap its buttons, type into its fields, read its screen, and save a screenshot — with no phone and no emulator.
sidebar:
  order: 3
---

An app is a `view` and an `update` with no Android code in them. So a
test can run the whole app on your computer. `std/android/ui/sim` is a
simulator: it gives the program a window, sends it touches and text,
and lets the test read the screen.

## A test

This program is a complete test of a counter. It runs with `fern -run`,
or as a native binary.

```fern
import "std/android/text";
import "std/android/ui";
import "std/android/ui/sim";
import "std/test";

struct Model { count: i32 }

function view(m: Model, s: ui.Screen): ui.El {
  return ui.column([ui.text(m.count.to_string()), ui.button("Add one", "add")]).centered();
}

function update(own m: Model, msg: ui.Msg): Model {
  match (msg) {
    ui.Tap("add", _) => {
      return Model { count: m.count + 1 };
    },
    _ => {
      return m;
    }
  }
}

function started(): sim.Sim[Model] {
  let program = ui.program(view, update);
  let no_fonts = text.Fonts { faces: [], weights: [] };
  return sim.start(Model { count: 0 }, program, no_fonts, 1080, 2400, 2.75);
}

function test_add(): test.TestOutcome {
  let s = sim.tap(sim.tap(started(), "add"), "add");
  return test.assert_eq(s.shell.app.count.to_string() + " " + sim.shows(s, "2").to_string(), "2 true");
}

function test_back_closes(): test.TestOutcome {
  return test.assert_eq(sim.finished(sim.back(started())), true);
}

function main(): i32 {
  let r = test.new("counter");
  r = r.it("two taps count to 2", test_add);
  r = r.it("the back key closes the app", test_back_closes);
  return r.finish();
}
```

`sim.start` takes the first state, the program, the fonts, the size of
the window in pixels, and the pixels of one dp. `sim.tap(s, "add")`
finds the element with the action `"add"` on the screen and taps the
middle of it. A test taps what a person can see: an element that is
off the screen, or under a dialog, takes no tap.

Each function takes the simulator and returns the next one.

## What a test can do

| Function | What it does |
|---|---|
| `sim.tap(s, name)`, `sim.tap_with(s, name, n)` | Taps the element with that action |
| `sim.tap_at(s, x, y)` | Taps a point |
| `sim.hold(s, name, n)` | A long press |
| `sim.drag(s, x0, y0, x1, y1, ms)` | Moves a finger, which scrolls a list |
| `sim.settle(s)` | Waits for a fling to end |
| `sim.type_in(s, field, text)` | Taps a field and types |
| `sim.enter(s)` | The action key of the keyboard |
| `sim.key(s, code)` | A key of a hardware keyboard, by its Android key code |
| `sim.back(s)` | The back key |
| `sim.after(s, ms)` | Lets time go by, with each `ui.Tick` on the way |
| `sim.themed(s, dark)` | The dark theme or the light theme |
| `sim.scaled(s, font)` | The font size of the system: 1 is the default |
| `sim.event(s, e)` | Any `android.Event` |

## What a test can read

| Function | What it gives |
|---|---|
| `s.shell.app` | The state of the app |
| `sim.texts(s)` | The texts on the screen, in order |
| `sim.shows(s, text)` | Whether the screen shows a text |
| `sim.spoken(s)` | What TalkBack reads |
| `sim.keyboard(s)` | The text of the keyboard, or `None` while it does not show |
| `sim.finished(s)` | Whether the app closed |

## Screenshots

With no fonts, a test runs fast and its texts have no width. Give the
simulator real fonts to draw the screen as a phone does.
`sim.picture` draws it on a frame, and `testkit.save_png` writes a PNG
file:

```fern
let fr = sim.picture(s);
let ok = testkit.save_png(fr, "counter.png");
draw.release(fr);
```

`examples/android/counter/counter_test.fern` loads Roboto from files and
writes a screenshot in the light theme and in the dark theme.

## On a phone

`fern -apk app.apk -apk-run app.fern` runs the app on the phone or the
emulator that `adb` sees, and shows its log. A line that the app writes
with `std/log` goes to logcat with the tag `fern`.
