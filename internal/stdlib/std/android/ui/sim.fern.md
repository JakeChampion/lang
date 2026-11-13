# A simulator for user interfaces

This module runs a program of `std/android/ui` on the host with no phone, for the tests of an app.

## Model

A `Sim` holds the shell of a program and a `Device` that draws nothing. `start` gives the program a window and draws its first frame.

Each function then does what a person does. `tap` and `hold` find an element by the text that it shows, or by what an accessibility service reads for it. `drag` moves a finger, `hold_drag` moves a finger after a long press, and `type_in` types a text into a field. `enter` presses the action key of the keyboard, `key` presses a key of a hardware keyboard, and `back` presses the back key. `after` lets time go by, and `send` gives the program a message with no touch. `place_of` gives the place of an element, for a gesture that starts or ends there.

After each event, the simulator draws a frame, as the display does at the next vsync. So the layout that the test reads is the layout that the person sees.

`themed` sets the dark theme, and `scaled` sets the font size of the system.

`texts` gives the texts that the screen shows, and `shows` tells whether it shows one text. `finished` tells whether the program closed the app, and `keyboard` gives the text that the keyboard of the system shows. `spoken` gives what an accessibility service reads. `picture` draws the screen on a frame, which `testkit.save_png` writes as a PNG file.

The simulator keeps the clock. A touch, a frame and a wait each take the time that they take on a phone: a frame is 16 ms.

## Contracts

The device of the simulator writes what the program asks of it into files in a temporary directory, so two simulators do not share a directory. The size of the window is in pixels, and `d` is the pixels of one dp.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<device>>

<<events>>

<<gestures>>

<<reading>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module runs a program of std/android/ui on the host with no phone.
import "std/android";
import "std/android/draw";
import "std/android/testkit";
import "std/android/text";
import "std/android/ui";
import "std/android/ui/painter";
import "std/android/ui/scene";
import "std/option";
import "std/result";
```

### `device`

```fern
<<device>>=
// The program, its shell, the directory of the device, the size of the
// window, and the time in ms.
pub struct Sim[S, M] { p: ui.Program[S, M], shell: ui.Shell[S, M], dir: string, w: i32, h: i32, d: f64, dark: boolean, font: f64, now: i64 }

const FRAME_MS: i64 = 16i64;

function note(dir: string, file: string, line: string): void {
  let ok = write_file(dir + "/" + file, testkit.read_text(dir + "/" + file) + line + "\n");
}

function said(items: android.AccessItem[]): string {
  let out: string[] = [];
  for it in items {
    out = out.append(it.text);
  }
  return out.join("\n");
}

// A device with a window of w by h pixels at the time `now` in ms.
function device(dir: string, w: i32, h: i32, d: f64, dark: boolean, font: f64, now: i64): ui.Device {
  return ui.Device {
    size: (window: usize): (i32, i32) => (w, h),
    density: (): f64 => d,
    dark: (): boolean => dark,
    font_scale: (): f64 => font,
    now: (): i64 => now * 1000000i64,
    request_frame: (): void => {},
    finish: (): void => note(dir, "calls", "finish"),
    attach: (p: painter.Painter, window: usize): painter.Painter => p,
    detach: (p: painter.Painter): painter.Painter => p,
    close: (p: painter.Painter): painter.Painter => p,
    present: (own p: painter.Painter, window: usize, nodes: scene.Node[], background: i32): painter.Painter => p,
    publish: (items: android.AccessItem[]): void => { let ok = write_file(dir + "/spoken", said(items)); },
    show_keyboard: (text: string, lines: boolean): void => note(dir, "calls", "keyboard " + text.replace("\n", "↵")),
    set_keyboard: (text: string, caret: i32): void => note(dir, "calls", "keyboard " + text.replace("\n", "↵")),
    hide_keyboard: (): void => note(dir, "calls", "no keyboard"),
  };
}

// A simulator of the program `p` from the state `state`, on a window of
// w by h pixels with `d` pixels to the dp, in the light theme. It draws
// the first frame.
pub function start[S, M](own state: S, p: ui.Program[S, M], fonts: text.Fonts, w: i32, h: i32, d: f64): Sim[S, M] {
  let dir = temp_dir("fern-ui-sim").unwrap_or(".");
  let s = Sim { p: p, shell: ui.shell(state, fonts, device(dir, w, h, d, false, 1.0, 0i64)), dir: dir, w: w, h: h, d: d, dark: false, font: 1.0, now: 0i64 };
  return event(event(s, android.Start), android.WindowCreated(android.Window { ptr: 1 as usize }));
}

// The simulator with the text of the system at the scale `font`: 1 is the
// default font size.
pub function scaled[S, M](own s: Sim[S, M], font: f64): Sim[S, M] {
  return event(Sim { p: s.p, shell: s.shell, dir: s.dir, w: s.w, h: s.h, d: s.d, dark: s.dark, font: font, now: s.now }, android.WindowResized(android.Window { ptr: 1 as usize }));
}

// The simulator in the dark theme, or in the light theme.
pub function themed[S, M](own s: Sim[S, M], dark: boolean): Sim[S, M] {
  return frame(Sim { p: s.p, shell: s.shell, dir: s.dir, w: s.w, h: s.h, d: s.d, dark: dark, font: s.font, now: s.now });
}
```

### `events`

`scaled` sends a new size of the window, because the shell reads the font size when the window changes.

```fern
<<events>>=
// The shell with the device of the time of the simulator.
function at_now[S, M](own s: Sim[S, M]): Sim[S, M] {
  let dev = device(s.dir, s.w, s.h, s.d, s.dark, s.font, s.now);
  return Sim { p: s.p, shell: ui.Shell { app: s.shell.app, host: ui.Host { ...s.shell.host, dev: dev }, said: s.shell.said }, dir: s.dir, w: s.w, h: s.h, d: s.d, dark: s.dark, font: s.font, now: s.now };
}

function stepped[S, M](own s: Sim[S, M], e: android.Event): Sim[S, M] {
  let t = at_now(s);
  return Sim { p: t.p, shell: ui.step(t.shell, t.p, e), dir: t.dir, w: t.w, h: t.h, d: t.d, dark: t.dark, font: t.font, now: t.now };
}

// Draws a frame at the time of the simulator.
pub function frame[S, M](own s: Sim[S, M]): Sim[S, M] {
  let ns = s.now * 1000000i64;
  return stepped(s, android.Vsync(ns));
}

function told[S, M](own s: Sim[S, M], m: M): Sim[S, M] {
  let t = at_now(s);
  return Sim { p: t.p, shell: ui.send(t.shell, t.p, m), dir: t.dir, w: t.w, h: t.h, d: t.d, dark: t.dark, font: t.font, now: t.now };
}

// Gives `update` the message `m`, and draws a frame.
pub function send[S, M](own s: Sim[S, M], m: M): Sim[S, M] {
  return frame(told(s, m));
}

// Gives the program the event `e`, and draws a frame.
pub function event[S, M](own s: Sim[S, M], e: android.Event): Sim[S, M] {
  return frame(stepped(s, e));
}

function later[S, M](own s: Sim[S, M], ms: i64): Sim[S, M] {
  return Sim { p: s.p, shell: s.shell, dir: s.dir, w: s.w, h: s.h, d: s.d, dark: s.dark, font: s.font, now: s.now + ms };
}

// Lets `ms` milliseconds go by. The program gets each tick that it waits
// for in that time, and a frame after each one.
pub function after[S, M](own s: Sim[S, M], ms: i64): Sim[S, M] {
  let t = s;
  let left = ms;
  let turns = 0;
  while (turns < 10000) {
    let wait = ui.wait(at_now_view(t), t.p) as i64;
    if (wait < 0i64 || wait > left) {
      break;
    }
    t = event(later(t, wait.max(1i64)), android.Tick);
    left -= wait.max(1i64);
    turns += 1;
  }
  return frame(later(t, left.max(0i64)));
}

// The shell of `s` with the device of its time, to read only.
function at_now_view[S, M](s: Sim[S, M]): ui.Shell[S, M] {
  return ui.Shell { app: s.shell.app, host: ui.Host { ...s.shell.host, dev: device(s.dir, s.w, s.h, s.d, s.dark, s.font, s.now) }, said: s.shell.said };
}

// Draws frames until no list flings, for at most 10 seconds.
pub function settle[S, M](own s: Sim[S, M]): Sim[S, M] {
  let t = s;
  let turns = 0;
  while (t.shell.host.input.flinging() && turns < 625) {
    t = frame(later(t, FRAME_MS));
    turns += 1;
  }
  return t;
}
```

### `gestures`

```fern
<<gestures>>=
// A finger goes down at x and y, and lifts 50 ms later.
pub function tap_at[S, M](own s: Sim[S, M], x: i32, y: i32): Sim[S, M] {
  let pressed = event(s, android.Touch(0, x as f32, y as f32));
  return event(later(pressed, 50i64), android.Touch(1, x as f32, y as f32));
}

// The node shows the text `label`, or an accessibility service reads
// `label` for it.
function labelled(n: scene.Node, label: string): boolean {
  if (n.say == label) {
    return true;
  }
  match (n.paint) {
    scene.Label(t, _, _, _, _) => {
      return t == label;
    },
    _ => {
      return false;
    }
  }
}

// The middle of the part that shows of the node `n`, or None when no part
// of it shows. A text that no font can draw has no size, and it shows
// while its place is in the rows of its list.
function middle(n: scene.Node): Option[(i32, i32)] {
  let y0 = n.y.max(n.clip0);
  let y1 = (n.y + n.h).min(n.clip1);
  let empty = n.h == 0 && n.y >= n.clip0 && n.y < n.clip1;
  if (y1 <= y0 && !empty) {
    return None;
  }
  return Some((n.x + n.w / 2, (y0 + y1) / 2));
}

// Where a person puts a finger for `label`: the middle of the element
// number `nth`, from 0, that shows or says `label`.
pub function place_of[S, M](s: Sim[S, M], label: string, nth: i32): Option[(i32, i32)] {
  let seen = 0;
  for n in s.shell.host.lay.nodes {
    if (labelled(n, label)) {
      if let Some(at) = middle(n) {
        if (seen == nth) {
          return Some(at);
        }
        seen += 1;
      }
    }
  }
  return None;
}

// A tap on the first element that shows or says `label`. Nothing happens
// when the screen shows no such element.
pub function tap[S, M](own s: Sim[S, M], label: string): Sim[S, M] {
  return tap_nth(s, label, 0);
}

// A tap on the element number `nth`, from 0, that shows or says `label`.
pub function tap_nth[S, M](own s: Sim[S, M], label: string, nth: i32): Sim[S, M] {
  let Some((x, y)) = place_of(s, label, nth) else {
    return s;
  };
  return tap_at(s, x, y);
}

// A finger stays for 600 ms on the first element that shows or says
// `label`, then lifts.
pub function hold[S, M](own s: Sim[S, M], label: string): Sim[S, M] {
  return hold_nth(s, label, 0);
}

// A finger stays for 600 ms on the element number `nth`, from 0, that
// shows or says `label`, then lifts.
pub function hold_nth[S, M](own s: Sim[S, M], label: string, nth: i32): Sim[S, M] {
  let Some((x, y)) = place_of(s, label, nth) else {
    return s;
  };
  let pressed = event(s, android.Touch(0, x as f32, y as f32));
  return event(after(pressed, 600i64), android.Touch(1, x as f32, y as f32));
}

// A finger goes down on the first slider that says `label`, at `part` of
// its width from 0 to 1, and lifts.
pub function slide[S, M](own s: Sim[S, M], label: string, part: f64): Sim[S, M] {
  let at: Option[(i32, i32)] = None;
  for n in s.shell.host.lay.nodes {
    if (at.is_none() && n.tap.name == ui.SLIDER_ACTION && n.say == label) {
      if let Some((_, y)) = middle(n) {
        at = Some((ui.slide_x(n, part, s.d), y));
      }
    }
  }
  let Some((x, y)) = at else {
    return s;
  };
  return tap_at(s, x, y);
}

// A finger goes down at x0 and y0, moves to x1 and y1 in `ms`
// milliseconds, and lifts there. A frame draws each 16 ms.
pub function drag[S, M](own s: Sim[S, M], x0: i32, y0: i32, x1: i32, y1: i32, ms: i64): Sim[S, M] {
  let t = moved_to(event(s, android.Touch(0, x0 as f32, y0 as f32)), (x0, y0), (x1, y1), ms);
  return event(t, android.Touch(1, x1 as f32, y1 as f32));
}

// A finger stays for 600 ms at x0 and y0, so a long press lifts the row of
// a list that moves its rows. Then the finger moves to x1 and y1 in `ms`
// milliseconds, stays there for `stay` milliseconds, and lifts. A frame
// draws each 16 ms.
pub function hold_drag[S, M](own s: Sim[S, M], x0: i32, y0: i32, x1: i32, y1: i32, ms: i64, stay: i64): Sim[S, M] {
  let held = after(event(s, android.Touch(0, x0 as f32, y0 as f32)), 600i64);
  let t = moved_to(held, (x0, y0), (x1, y1), ms);
  for i in 0..(stay / FRAME_MS) as i32 {
    t = frame(later(t, FRAME_MS));
  }
  return event(t, android.Touch(1, x1 as f32, y1 as f32));
}

// The finger that is down at `from` moves to `to` in `ms` milliseconds,
// with one move for each frame.
function moved_to[S, M](own s: Sim[S, M], from: (i32, i32), to: (i32, i32), ms: i64): Sim[S, M] {
  let (x0, y0) = from;
  let (x1, y1) = to;
  let t = s;
  let steps = (ms / FRAME_MS).max(1i64) as i32;
  for i in 1..steps + 1 {
    let x = x0 + (x1 - x0) * i / steps;
    let y = y0 + (y1 - y0) * i / steps;
    t = event(later(t, FRAME_MS), android.Touch(2, x as f32, y as f32));
  }
  return t;
}

// A tap on the field `name`, then the keyboard of the system gives it the
// text `text`.
pub function type_in[S, M](own s: Sim[S, M], name: string, text: string): Sim[S, M] {
  for n in s.shell.host.lay.nodes {
    if (n.tap.name == ui.FIELD_ACTION + name) {
      if let Some((x, y)) = middle(n) {
        return event(tap_at(s, x, y), android.Text(text));
      }
    }
  }
  return s;
}

// The action key of the keyboard goes down.
pub function enter[S, M](own s: Sim[S, M]): Sim[S, M] {
  return event(s, android.TextAction(android.ACTION_DONE));
}

// The key with the Android key code `code` goes down and up: 66 is the
// enter key, 67 is the delete key, and 21 and 22 are the left and the right
// arrow.
pub function key[S, M](own s: Sim[S, M], code: i32): Sim[S, M] {
  return event(event(s, android.Key(code, 0)), android.Key(code, 1));
}

// The back key goes down and up.
pub function back[S, M](own s: Sim[S, M]): Sim[S, M] {
  return event(event(s, android.Key(4, 0)), android.Key(4, 1));
}
```

### `reading`

```fern
<<reading>>=
// The texts that the screen shows, in the order that they draw. A text
// outside the rows of its list does not show.
pub function texts[S, M](s: Sim[S, M]): string[] {
  let out: string[] = [];
  for n in s.shell.host.lay.nodes {
    match (n.paint) {
      scene.Label(t, _, _, _, _) => {
        if (n.y + n.h > n.clip0 && n.y < n.clip1) {
          out = out.append(t);
        }
      },
      _ => {}
    }
  }
  return out;
}

// The screen shows the text `t`.
pub function shows[S, M](s: Sim[S, M], t: string): boolean {
  return texts(s).contains(t);
}

// The program closed the app.
pub function finished[S, M](s: Sim[S, M]): boolean {
  return testkit.read_text(s.dir + "/calls").contains("finish");
}

// The text that the keyboard of the system shows, or None while the
// keyboard does not show.
pub function keyboard[S, M](s: Sim[S, M]): Option[string] {
  let last = "no keyboard";
  for line in testkit.read_text(s.dir + "/calls").split("\n") {
    if (line == "no keyboard" || line.starts_with("keyboard ")) {
      last = line.to_owned();
    }
  }
  if (last == "no keyboard") {
    return None;
  }
  return Some(last.drop(9).to_owned());
}

// What an accessibility service reads on the screen, one item for each
// line.
pub function spoken[S, M](s: Sim[S, M]): string[] {
  let out: string[] = [];
  for line in testkit.read_text(s.dir + "/spoken").split("\n") {
    out = out.append(line.to_owned());
  }
  return out;
}

// The screen on a frame, as the CPU draws it. Release the frame with
// draw.release.
pub function picture[S, M](s: Sim[S, M]): draw.Frame {
  let host = s.shell.host;
  let fr = draw.offscreen(s.w, s.h);
  fr.fill(0, 0, s.w, s.h, host.screen.theme.background);
  let c = scene.render(fr, host.fonts, text.new_cache(), ui.shown(host.input, host.lay, host.screen.theme.pressed));
  return fr;
}
```
