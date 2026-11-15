# Android icons

This module draws the icons of a media app or of a list, for a canvas of `std/android/ui`. Each function gives the paint of a canvas: `ui.canvas(key, icons.play(color))`. An app needs no image files for its buttons, and the icons take each color of a theme.

## Model

An icon fills the middle of the box of its canvas, at the size of the shorter side of the box. So a canvas of 48 by 48 dp shows an icon of about 24 dp, with room around it for a finger.

The icons are drawn with the discs, the rings, the rounded rectangles and the filled rows of `std/android/draw`. A line is a row of discs, so its ends are round. A triangle is filled row by row.

- `play`, `pause` and `skip` are the buttons of the sound. `skip` points back, to the track before, or forward, to the next one.
- `shuffle` is two lines that cross, with the heads of arrows. `repeat` is a loop of two arrows, with a 1 in it for the repeat of one track.
- `heart` is filled for a favorite, or is an edge on the background `bg`.
- `dots` is three dots in a row, or in a column for the menu of a row.
- `timer`, `lyrics`, `queue`, `gear` and `search` are the tools of a player. `queue` and `gear` take the background `bg` for their holes.
- `chevron` is the head of an arrow, down or to the left.

A button of icons has no text, so give each canvas a text that an accessibility service reads: `.described("Play")`. The simulator of `std/android/ui/sim` finds the button by that text.

## Contracts

The paint draws only in its box. A color is a color of `std/android/draw`, not a role of a theme.

## Module assembly

```fern
<<*>>=
// This module draws the icons of a media app.
import "std/android/draw";
import "std/i32";

// A line from x0, y0 to x1, y1, of the width `t`, with round ends.
function stroke(f: draw.Frame, x0: i32, y0: i32, x1: i32, y1: i32, t: i32, color: i32): void {
  let dx = x1 - x0;
  let dy = y1 - y0;
  let n = dx.abs().max(dy.abs()).max(1);
  let r = (t / 2).max(1);
  for k in 0..n + 1 {
    f.disc(x0 + dx * k / n, y0 + dy * k / n, r, color);
  }
}

// A triangle in the box at x, y of w by h, with its point to the right, or
// to the left when `left` is set.
function triangle(f: draw.Frame, x: i32, y: i32, w: i32, h: i32, left: boolean, color: i32): void {
  let half = (h / 2).max(1);
  for row in 0..h {
    let len = w * (half - (row - half).abs()) / half;
    f.fill(if (left) { x + w - len } else { x }, y + row, len, 1, color);
  }
}

// The side of the icon in a box of w by h, and its left and top edges.
function square(x: i32, y: i32, w: i32, h: i32, part: i32): (i32, i32, i32) {
  let s = w.min(h) * part / 100;
  return (s, x + (w - s) / 2, y + (h - s) / 2);
}

// The width of the lines of an icon of side `s`.
function line_width(s: i32): i32 {
  return (s / 11).max(2);
}

pub function play(color: i32): (draw.Frame, i32, i32, i32, i32) => void {
  return (f: draw.Frame, x: i32, y: i32, w: i32, h: i32): void => {
    let (s, l, t) = square(x, y, w, h, 50);
    triangle(f, l + s / 8, t, s * 7 / 8, s, false, color);
  };
}

pub function pause(color: i32): (draw.Frame, i32, i32, i32, i32) => void {
  return (f: draw.Frame, x: i32, y: i32, w: i32, h: i32): void => {
    let (s, l, t) = square(x, y, w, h, 46);
    let bar = s * 3 / 10;
    f.round_rect(l + s / 10, t, bar, s, bar / 3, color);
    f.round_rect(l + s - s / 10 - bar, t, bar, s, bar / 3, color);
  };
}

// The button of the track before: a bar and a triangle to the left. The
// button of the next track is the same to the right.
pub function skip(color: i32, back: boolean): (draw.Frame, i32, i32, i32, i32) => void {
  return (f: draw.Frame, x: i32, y: i32, w: i32, h: i32): void => {
    let (s, l, t) = square(x, y, w, h, 40);
    let bar = (s / 6).max(2);
    if (back) {
      f.fill(l, t, bar, s, color);
      triangle(f, l + bar, t, s - bar, s, true, color);
    } else {
      triangle(f, l, t, s - bar, s, false, color);
      f.fill(l + s - bar, t, bar, s, color);
    }
  };
}

// Two lines that cross, each with the head of an arrow at its right end.
pub function shuffle(color: i32): (draw.Frame, i32, i32, i32, i32) => void {
  return (f: draw.Frame, x: i32, y: i32, w: i32, h: i32): void => {
    let (s, l, t) = square(x, y, w, h, 50);
    let lw = line_width(s);
    let a = s / 4;
    let r = l + s;
    let b = t + s * 4 / 5;
    let top = t + s / 5;
    stroke(f, l, b, r, top, lw, color);
    stroke(f, l, top, r, b, lw, color);
    stroke(f, r, top, r - a, top, lw, color);
    stroke(f, r, top, r, top + a, lw, color);
    stroke(f, r, b, r - a, b, lw, color);
    stroke(f, r, b, r, b - a, lw, color);
  };
}

// A loop of two arrows. The repeat of one track has a 1 in the middle.
pub function repeat(color: i32, one: boolean): (draw.Frame, i32, i32, i32, i32) => void {
  return (f: draw.Frame, x: i32, y: i32, w: i32, h: i32): void => {
    let (s, l, t) = square(x, y, w, h, 50);
    let lw = line_width(s);
    let a = s / 5;
    let r = l + s;
    let top = t + s / 5;
    let b = t + s * 4 / 5;
    let mid = t + s / 2;
    stroke(f, l, mid, l, top, lw, color);
    stroke(f, l, top, r, top, lw, color);
    stroke(f, r, top, r - a, top - a, lw, color);
    stroke(f, r, top, r - a, top + a, lw, color);
    stroke(f, r, mid, r, b, lw, color);
    stroke(f, r, b, l, b, lw, color);
    stroke(f, l, b, l + a, b - a, lw, color);
    stroke(f, l, b, l + a, b + a, lw, color);
    if (one) {
      let h = s / 4;
      stroke(f, l + s / 2, mid - h / 2, l + s / 2, mid + h / 2, lw, color);
      stroke(f, l + s / 2, mid - h / 2, l + s / 2 - h / 3, mid - h / 4, lw, color);
    }
  };
}

// A heart in the box at l, t of side s.
function heart_shape(f: draw.Frame, l: i32, t: i32, s: i32, color: i32): void {
  let r = s / 4;
  f.disc(l + r, t + r, r, color);
  f.disc(l + 3 * r, t + r, r, color);
  let top = t + r * 6 / 5;
  let rows = t + s - top;
  for row in 0..rows {
    let half = s / 2 * (rows - row) / rows;
    f.fill(l + s / 2 - half, top + row, 2 * half, 1, color);
  }
}

// A filled heart for a favorite, or the edge of a heart on `bg`.
pub function heart(color: i32, filled: boolean, bg: i32): (draw.Frame, i32, i32, i32, i32) => void {
  return (f: draw.Frame, x: i32, y: i32, w: i32, h: i32): void => {
    let (s, l, t) = square(x, y, w, h, 50);
    heart_shape(f, l, t, s, color);
    if (!filled) {
      let edge = line_width(s) * 3 / 2;
      heart_shape(f, l + edge, t + edge, s - 2 * edge, bg);
    }
  };
}

// Three dots in a row, or in a column when `down` is set.
pub function dots(color: i32, down: boolean): (draw.Frame, i32, i32, i32, i32) => void {
  return (f: draw.Frame, x: i32, y: i32, w: i32, h: i32): void => {
    let (s, l, t) = square(x, y, w, h, 46);
    let r = (s / 10).max(2);
    for k in 0..3 {
      let along = s / 6 + k * s / 3;
      if (down) {
        f.disc(l + s / 2, t + along, r, color);
      } else {
        f.disc(l + along, t + s / 2, r, color);
      }
    }
  };
}

// A stopwatch: a ring, a hand, and the knob on its top.
pub function timer(color: i32): (draw.Frame, i32, i32, i32, i32) => void {
  return (f: draw.Frame, x: i32, y: i32, w: i32, h: i32): void => {
    let (s, l, t) = square(x, y, w, h, 50);
    let lw = line_width(s);
    let r = s * 2 / 5;
    let cx = l + s / 2;
    let cy = t + s - r;
    f.ring(cx, cy, r, lw, color);
    stroke(f, cx, cy, cx, cy - r / 2, lw, color);
    f.fill(cx - s / 8, t, s / 4, lw, color);
  };
}

// Lines of text and a note.
pub function lyrics(color: i32): (draw.Frame, i32, i32, i32, i32) => void {
  return (f: draw.Frame, x: i32, y: i32, w: i32, h: i32): void => {
    let (s, l, t) = square(x, y, w, h, 50);
    let lw = line_width(s);
    for k in 0..3 {
      let row = t + s / 6 + k * s / 3;
      stroke(f, l, row, l + s * (6 - k) / 10, row, lw, color);
    }
    let nx = l + s * 4 / 5;
    f.disc(nx, t + s * 5 / 6, s / 8, color);
    stroke(f, nx + s / 8 - lw / 2, t + s * 5 / 6, nx + s / 8 - lw / 2, t, lw, color);
  };
}

// Two cards, one above the other.
pub function queue(color: i32, bg: i32): (draw.Frame, i32, i32, i32, i32) => void {
  return (f: draw.Frame, x: i32, y: i32, w: i32, h: i32): void => {
    let (s, l, t) = square(x, y, w, h, 50);
    let lw = line_width(s);
    let card = s * 9 / 20;
    for top in [t, t + s - card] {
      f.round_rect(l, top, s, card, card / 3, color);
      f.round_rect(l + lw, top + lw, s - 2 * lw, card - 2 * lw, card / 3 - lw, bg);
    }
  };
}

// A gear: a ring with eight teeth.
pub function gear(color: i32, bg: i32): (draw.Frame, i32, i32, i32, i32) => void {
  return (f: draw.Frame, x: i32, y: i32, w: i32, h: i32): void => {
    let (s, l, t) = square(x, y, w, h, 50);
    let cx = l + s / 2;
    let cy = t + s / 2;
    let r = s * 3 / 8;
    for k in 0..8 {
      let a: f64 = 6.283185307179586 * (k as f64) / 8.0;
      f.disc(cx + (r as f64 * a.cos()) as i32, cy + (r as f64 * a.sin()) as i32, s / 9, color);
    }
    f.disc(cx, cy, r, color);
    f.disc(cx, cy, s / 7, bg);
  };
}

// A lens and its handle.
pub function search(color: i32): (draw.Frame, i32, i32, i32, i32) => void {
  return (f: draw.Frame, x: i32, y: i32, w: i32, h: i32): void => {
    let (s, l, t) = square(x, y, w, h, 46);
    let lw = line_width(s);
    let r = s * 2 / 5;
    f.ring(l + r, t + r, r, lw, color);
    stroke(f, l + r * 17 / 10, t + r * 17 / 10, l + s, t + s, lw, color);
  };
}

// An arrow head down, or to the left when `left` is set.
pub function chevron(color: i32, left: boolean): (draw.Frame, i32, i32, i32, i32) => void {
  return (f: draw.Frame, x: i32, y: i32, w: i32, h: i32): void => {
    let (s, l, t) = square(x, y, w, h, 36);
    let lw = line_width(s) * 3 / 2;
    if (left) {
      stroke(f, l + s * 3 / 4, t, l + s / 4, t + s / 2, lw, color);
      stroke(f, l + s / 4, t + s / 2, l + s * 3 / 4, t + s, lw, color);
    } else {
      stroke(f, l, t + s / 4, l + s / 2, t + s * 3 / 4, lw, color);
      stroke(f, l + s / 2, t + s * 3 / 4, l + s, t + s / 4, lw, color);
    }
  };
}
```
