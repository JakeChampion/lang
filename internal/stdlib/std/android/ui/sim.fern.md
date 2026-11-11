# A simulator for user interfaces

This module runs a program of `std/android/ui` on the host with no phone, for the tests of an app.

## Model

A `Sim` holds the shell of a program and a `Device` that draws nothing. `start` gives the program a window and draws its first frame. Each function then does what a person does: `tap` and `hold` find an element by the name of its action, `drag` moves a finger, `back` presses the back key, and `after` lets time go by.

After each event, the simulator draws a frame, as the display does at the next vsync. So the layout that the test reads is the layout that the person sees.

`texts` gives the texts that the screen shows, and `shows` tells whether it shows one text. `finished` tells whether the program closed the app. `spoken` gives what an accessibility service reads. `picture` draws the screen on a frame, which `testkit.save_png` writes as a PNG file.

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
pub struct Sim[S] { p: ui.Program[S], shell: ui.Shell[S], dir: string, w: i32, h: i32, d: f64, dark: boolean, now: i64 }

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
function device(dir: string, w: i32, h: i32, d: f64, dark: boolean, now: i64): ui.Device {
  return ui.Device {
    size: (window: usize): (i32, i32) => (w, h),
    density: (): f64 => d,
    dark: (): boolean => dark,
    now: (): i64 => now * 1000000i64,
    request_frame: (): void => {},
    finish: (): void => note(dir, "calls", "finish"),
    attach: (p: painter.Painter, window: usize): painter.Painter => p,
    detach: (p: painter.Painter): painter.Painter => p,
    close: (p: painter.Painter): painter.Painter => p,
    present: (own p: painter.Painter, window: usize, nodes: scene.Node[], background: i32): painter.Painter => p,
    publish: (items: android.AccessItem[]): void => { let ok = write_file(dir + "/spoken", said(items)); },
  };
}

// A simulator of the program `p` from the state `state`, on a window of
// w by h pixels with `d` pixels to the dp, in the light theme. It draws
// the first frame.
pub function start[S](own state: S, p: ui.Program[S], fonts: text.Fonts, w: i32, h: i32, d: f64): Sim[S] {
  let dir = temp_dir("fern-ui-sim").unwrap_or(".");
  let s = Sim { p: p, shell: ui.shell(state, fonts, device(dir, w, h, d, false, 0i64)), dir: dir, w: w, h: h, d: d, dark: false, now: 0i64 };
  return event(event(s, android.Start), android.WindowCreated(android.Window { ptr: 1 as usize }));
}

// The simulator in the dark theme, or in the light theme.
pub function themed[S](own s: Sim[S], dark: boolean): Sim[S] {
  return frame(Sim { p: s.p, shell: s.shell, dir: s.dir, w: s.w, h: s.h, d: s.d, dark: dark, now: s.now });
}
```

### `events`

```fern
<<events>>=
// The shell with the device of the time of the simulator.
function at_now[S](own s: Sim[S]): Sim[S] {
  let dev = device(s.dir, s.w, s.h, s.d, s.dark, s.now);
  return Sim { p: s.p, shell: ui.Shell { app: s.shell.app, host: ui.Host { ...s.shell.host, dev: dev } }, dir: s.dir, w: s.w, h: s.h, d: s.d, dark: s.dark, now: s.now };
}

function stepped[S](own s: Sim[S], e: android.Event): Sim[S] {
  let t = at_now(s);
  return Sim { p: t.p, shell: ui.step(t.shell, t.p, e), dir: t.dir, w: t.w, h: t.h, d: t.d, dark: t.dark, now: t.now };
}

// Draws a frame at the time of the simulator.
pub function frame[S](own s: Sim[S]): Sim[S] {
  let ns = s.now * 1000000i64;
  return stepped(s, android.Vsync(ns));
}

// Gives the program the event `e`, and draws a frame.
pub function event[S](own s: Sim[S], e: android.Event): Sim[S] {
  return frame(stepped(s, e));
}

function later[S](own s: Sim[S], ms: i64): Sim[S] {
  return Sim { p: s.p, shell: s.shell, dir: s.dir, w: s.w, h: s.h, d: s.d, dark: s.dark, now: s.now + ms };
}

// Lets `ms` milliseconds go by. The program gets each Tick that it waits
// for in that time, and a frame after each one.
pub function after[S](own s: Sim[S], ms: i64): Sim[S] {
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
function at_now_view[S](s: Sim[S]): ui.Shell[S] {
  return ui.Shell { app: s.shell.app, host: ui.Host { ...s.shell.host, dev: device(s.dir, s.w, s.h, s.d, s.dark, s.now) } };
}

// Draws frames until no list flings, for at most 10 seconds.
pub function settle[S](own s: Sim[S]): Sim[S] {
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
pub function tap_at[S](own s: Sim[S], x: i32, y: i32): Sim[S] {
  let pressed = event(s, android.Touch(0, x as f32, y as f32));
  return event(later(pressed, 50i64), android.Touch(1, x as f32, y as f32));
}

// The middle of the part that shows of the first node with the action
// `name` and the number `arg`, for a tap when `long` is false and for a
// long press when it is true.
function target[S](s: Sim[S], name: string, arg: i32, long: boolean): Option[(i32, i32)] {
  for n in s.shell.host.lay.nodes {
    let a = n.tap;
    if (long) {
      a = n.long;
    }
    let y0 = n.y.max(n.clip0);
    let y1 = (n.y + n.h).min(n.clip1);
    if (a.name == name && a.arg == arg && y1 > y0 && n.w > 0) {
      return Some((n.x + n.w / 2, (y0 + y1) / 2));
    }
  }
  return None;
}

// A tap on the element with the action `name` and the number 0. Nothing
// happens when the screen shows no such element.
pub function tap[S](own s: Sim[S], name: string): Sim[S] {
  return tap_with(s, name, 0);
}

// A tap on the element with the action `name` and the number `arg`.
pub function tap_with[S](own s: Sim[S], name: string, arg: i32): Sim[S] {
  let Some((x, y)) = target(s, name, arg, false) else {
    return s;
  };
  return tap_at(s, x, y);
}

// A finger stays for 600 ms on the element with the long-press action
// `name` and the number `arg`, then lifts.
pub function hold[S](own s: Sim[S], name: string, arg: i32): Sim[S] {
  let Some((x, y)) = target(s, name, arg, true) else {
    return s;
  };
  let pressed = event(s, android.Touch(0, x as f32, y as f32));
  return event(after(pressed, 600i64), android.Touch(1, x as f32, y as f32));
}

// A finger goes down at x0 and y0, moves to x1 and y1 in `ms`
// milliseconds, and lifts there. A frame draws each 16 ms.
pub function drag[S](own s: Sim[S], x0: i32, y0: i32, x1: i32, y1: i32, ms: i64): Sim[S] {
  let t = event(s, android.Touch(0, x0 as f32, y0 as f32));
  let steps = (ms / FRAME_MS).max(1i64) as i32;
  for i in 1..steps + 1 {
    let x = x0 + (x1 - x0) * i / steps;
    let y = y0 + (y1 - y0) * i / steps;
    t = event(later(t, FRAME_MS), android.Touch(2, x as f32, y as f32));
  }
  return event(t, android.Touch(1, x1 as f32, y1 as f32));
}

// The back key goes down and up.
pub function back[S](own s: Sim[S]): Sim[S] {
  return event(event(s, android.Key(4, 0)), android.Key(4, 1));
}
```

### `reading`

```fern
<<reading>>=
// The texts that the screen shows, in the order that they draw. A text
// outside the rows of its list does not show.
pub function texts[S](s: Sim[S]): string[] {
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
pub function shows[S](s: Sim[S], t: string): boolean {
  return texts(s).contains(t);
}

// The program closed the app.
pub function finished[S](s: Sim[S]): boolean {
  return testkit.read_text(s.dir + "/calls").contains("finish");
}

// What an accessibility service reads on the screen, one item for each
// line.
pub function spoken[S](s: Sim[S]): string[] {
  let out: string[] = [];
  for line in testkit.read_text(s.dir + "/spoken").split("\n") {
    out = out.append(line.to_owned());
  }
  return out;
}

// The screen on a frame, as the CPU draws it. Release the frame with
// draw.release.
pub function picture[S](s: Sim[S]): draw.Frame {
  let host = s.shell.host;
  let fr = draw.offscreen(s.w, s.h);
  fr.fill(0, 0, s.w, s.h, host.screen.theme.background);
  let c = scene.render(fr, host.fonts, text.new_cache(), ui.shown(host.input, host.lay, host.screen.theme.pressed));
  return fr;
}
```
