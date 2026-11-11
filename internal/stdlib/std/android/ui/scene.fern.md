# A scene of nodes

This module holds what an Android app shows as a list of nodes. It finds the node under a touch, gives the items that an accessibility service reads, and draws the nodes on a frame.

## Model

A `Node` is a rectangle of the window in pixels, with a `Paint` that tells what it shows. The nodes of a screen draw in their order, so a later node is over an earlier one. `std/android/ui` makes the nodes of a screen from a tree of elements. An app can also make its nodes itself.

A node can have an action for a tap and an action for a long press. An `Action` is a name and a number. `hit` gives the topmost node with an action under a point. A node that is a barrier stops the search, so a dialog keeps the touches off the screen behind it.

A node can be part of a group: a list that scrolls. The node draws only in the rows from `clip0` to `clip1`, which are the edges of its list. `group_at` gives the group under a point.

`access_items` gives one item for each node with an action, and one for each text in no such node. The text of an item is the description of the node, or the texts that the node holds.

`render` draws the nodes on a frame on the CPU. `raster` draws one node alone on a transparent frame. `std/android/ui/painter` keeps that image on the GPU under the `paint_key` of the node, and draws it again in each frame.

## Contracts

A color is a pixel word of `std/android/draw`. A `Label` has its text, its size in pixels to the em, its face in the `text.Fonts`, its letter spacing and its color. A `Picture` and a `Custom` paint have a key that names what they show: change the key when the pixels change. The function of a `Custom` paint draws in the box that it gets, as x, y, width and height, on a frame that can be larger than the box.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<nodes>>

<<touches>>

<<accessibility>>

<<keys>>

<<drawing>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module holds what an Android app shows as a list of nodes.
import "std/android";
import "std/android/draw";
import "std/android/text";
import "std/f64";
import "std/option";
```

### `nodes`

```fern
<<nodes>>=
// What a node shows. A Fill has a color and a corner radius. An Outline has
// a color, a corner radius and a line width. A Picture has a key, an image
// and a corner radius. A Custom has a key and a function that draws it.
pub enum Paint {
  Blank,
  Fill(i32, i32),
  Outline(i32, i32, i32),
  Label(string, i32, i32, i32, i32),
  Picture(string, draw.Image, i32),
  Custom(string, (draw.Frame, i32, i32, i32, i32) => void),
}

// What a tap or a long press does: a name, and a number that tells one
// such action from another. The name "" is no action.
pub struct Action { name: string, arg: i32 }

pub function no_action(): Action {
  return Action { name: "", arg: 0 };
}

pub const NO_CLIP: i32 = 1 << 30;

pub struct Node {
  x: i32,
  y: i32,
  w: i32,
  h: i32,
  paint: Paint,
  tap: Action,
  long: Action,
  // The list that the node scrolls with, or "".
  group: string,
  clip0: i32,
  clip1: i32,
  // Touches do not go to the nodes below a barrier.
  barrier: boolean,
  // What an accessibility service reads for the node, or "".
  say: string,
}

pub function node(x: i32, y: i32, w: i32, h: i32, paint: Paint): Node {
  return Node { x: x, y: y, w: w, h: h, paint: paint, tap: no_action(), long: no_action(), group: "", clip0: 0, clip1: NO_CLIP, barrier: false, say: "" };
}

// A node acts when it has an action for a tap or for a long press.
pub function (n: Node) acts(): boolean {
  return n.tap.name.len() > 0 || n.long.name.len() > 0;
}

// The part of the node that shows holds the point x and y.
pub function (n: Node) holds(x: i32, y: i32): boolean {
  return x >= n.x && x < n.x + n.w && y >= n.y && y < n.y + n.h && y >= n.clip0 && y < n.clip1;
}

// The nodes of one screen, moved down `dy` rows, with one alpha for all of
// them: a screen that fades draws as one image.
pub struct Pass { nodes: Node[], dy: i32, alpha: f64 }
```

### `touches`

```fern
<<touches>>=
// The index of the topmost node under x and y that acts, or -1. A barrier
// under the point hides the nodes below it.
pub function hit_index(ns: Node[], x: i32, y: i32): i32 {
  let k = ns.len() - 1;
  while (k >= 0) {
    let n = ns[k];
    if (n.holds(x, y)) {
      if (n.acts()) {
        return k;
      }
      if (n.barrier) {
        return -1;
      }
    }
    k -= 1;
  }
  return -1;
}

// The topmost node under x and y that acts.
pub function hit(ns: Node[], x: i32, y: i32): Option[Node] {
  let k = hit_index(ns, x, y);
  if (k < 0) {
    return None;
  }
  return Some(ns[k]);
}

// The list that scrolls under x and y, or "".
pub function group_at(ns: Node[], x: i32, y: i32): string {
  let k = ns.len() - 1;
  while (k >= 0) {
    let n = ns[k];
    if (n.holds(x, y)) {
      if (n.group.len() > 0) {
        return n.group;
      }
      if (n.barrier) {
        return "";
      }
    }
    k -= 1;
  }
  return "";
}
```

### `accessibility`

An item gets an id from the hash of its actions or of its text, so the id of a button stays the same from frame to frame. A service keeps its focus on an item by its id.

```fern
<<accessibility>>=
// The text that a node shows, or "".
function shown_text(n: Node): string {
  match (n.paint) {
    Label(s, _, _, _, _) => {
      return s;
    },
    _ => {
      return "";
    }
  }
}

// The part of node `n` that shows, between its clip rows, as x, y, w, h.
function visible(n: Node): (i32, i32, i32, i32) {
  let y0 = n.y.max(n.clip0);
  let y1 = (n.y + n.h).min(n.clip1);
  return (n.x, y0, n.w, (y1 - y0).max(0));
}

// The middle of node `n` is in the box `r`.
function within(r: (i32, i32, i32, i32), n: Node): boolean {
  let (x, y, w, h) = r;
  let cx = n.x + n.w / 2;
  let cy = n.y + n.h / 2;
  return cx >= x && cx < x + w && cy >= y && cy < y + h;
}

// The nodes that a service can reach: those from the last barrier on.
function reachable(ns: Node[]): Node[] {
  let start = 0;
  for k in 0..ns.len() {
    if (ns[k].barrier) {
      start = k;
    }
  }
  return ns.drop(start);
}

// An id for the key `key`, which no id in `used` has.
function access_id(key: string, used: i32[]): i32 {
  let id = (key.hash_fnv32() & 1073741823u32) as i32;
  while (used.contains(id)) {
    id = id + 1 & 1073741823;
  }
  return id;
}

// What a service reads for the node `n` that acts, with the box `r`: its
// description, or the texts in the box, or the name of its action. `named`
// tells which nodes of `reach` are in an item already.
function said(reach: Node[], n: Node, r: (i32, i32, i32, i32), own named: boolean[]): (string, boolean[]) {
  let parts: string[] = [];
  let marks = named;
  for k in 0..reach.len() {
    let t = shown_text(reach[k]);
    if (t.len() > 0 && within(r, reach[k])) {
      parts = parts.append(t);
      marks = marks.with(k, true);
    }
  }
  if (n.say.len() > 0) {
    return (n.say, marks);
  }
  if (parts.len() > 0) {
    return (parts.join(", "), marks);
  }
  if (n.tap.name.len() > 0) {
    return (n.tap.name, marks);
  }
  return (n.long.name, marks);
}

// The actions of the node `n`, as the bits of android.ACCESS_CLICK and
// android.ACCESS_LONG_CLICK.
function actions_of(n: Node): i32 {
  let actions = 0;
  if (n.tap.name.len() > 0) {
    actions |= android.ACCESS_CLICK;
  }
  if (n.long.name.len() > 0) {
    actions |= android.ACCESS_LONG_CLICK;
  }
  return actions;
}

// What a service reads for the node `n` that does not act: its
// description, or its text.
function lone_text(n: Node): string {
  if (n.say.len() > 0) {
    return n.say;
  }
  return shown_text(n);
}

function access_pairs(ns: Node[]): (android.AccessItem, Node)[] {
  let reach = reachable(ns);
  let out: (android.AccessItem, Node)[] = [];
  let used: i32[] = [];
  let named: boolean[] = [];
  for n in reach {
    named = named.append(false);
  }
  for n in reach {
    let r = visible(n);
    let (x, y, w, h) = r;
    if (!n.acts() || h == 0) {
      continue;
    }
    let (text, marks) = said(reach, n, r, named);
    named = marks;
    let id = access_id(n.tap.name + "|" + n.tap.arg.to_string() + "|" + n.long.name + "|" + n.long.arg.to_string(), used);
    used = used.append(id);
    out = out.append((android.AccessItem { id: id, x: x, y: y, w: w, h: h, text: text, actions: actions_of(n) }, n));
  }
  for k in 0..reach.len() {
    let n = reach[k];
    let t = lone_text(n);
    let (x, y, w, h) = visible(n);
    if (t.len() == 0 || named[k] || n.acts() || h == 0) {
      continue;
    }
    let id = access_id("text|" + t, used);
    used = used.append(id);
    out = out.append((android.AccessItem { id: id, x: x, y: y, w: w, h: h, text: t, actions: 0 }, n));
  }
  return out;
}

// The items of the nodes `ns` for an accessibility service.
pub function access_items(ns: Node[]): android.AccessItem[] {
  let out: android.AccessItem[] = [];
  for (item, _) in access_pairs(ns) {
    out = out.append(item);
  }
  return out;
}

// The node of the item `id` of the nodes `ns`.
pub function access_node(ns: Node[], id: i32): Option[Node] {
  for (item, n) in access_pairs(ns) {
    if (item.id == id) {
      return Some(n);
    }
  }
  return None;
}
```

### `keys`

```fern
<<keys>>=
// The key of what node `n` shows at its size, which names its image on the
// GPU, or "" for a node with no image: a blank node.
pub function paint_key(n: Node): string {
  let size = "|" + n.w.to_string() + "x" + n.h.to_string();
  match (n.paint) {
    Blank => {
      return "";
    },
    Fill(color, r) => {
      return "fill|" + color.to_string() + "|" + r.to_string() + size;
    },
    Outline(color, r, width) => {
      return "outline|" + color.to_string() + "|" + r.to_string() + "|" + width.to_string() + size;
    },
    Label(s, px, face, track, color) => {
      return "label|" + px.to_string() + "|" + face.to_string() + "|" + track.to_string() + "|" + color.to_string() + "|" + s + size;
    },
    Picture(key, _, r) => {
      return "picture|" + key + "|" + r.to_string() + size;
    },
    Custom(key, _) => {
      return "custom|" + key + size;
    }
  }
}

// The pixels around node `n` that its image keeps, for the parts of glyphs
// that go past the box of the node.
pub function margin(n: Node): i32 {
  match (n.paint) {
    Label(_, _, _, _, _) => {
      return 4;
    },
    _ => {
      return 0;
    }
  }
}
```

### `drawing`

The edge of a rounded shape covers a part of each pixel that it crosses. `inside` gives that part for a rounded rectangle, from the distance of the middle of the pixel to the edge.

```fern
<<drawing>>=
// How much of the pixel at px and py is inside the rounded rectangle of
// w by h with its corner at 0 and 0, from 0 for none to 255 for all of it.
function inside(px: i32, py: i32, w: i32, h: i32, r: i32): i32 {
  if (w <= 0 || h <= 0) {
    return 0;
  }
  let rad = r as f64;
  let qx = (px as f64 + 0.5 - w as f64 / 2.0).abs() - (w as f64 / 2.0 - rad);
  let qy = (py as f64 + 0.5 - h as f64 / 2.0).abs() - (h as f64 / 2.0 - rad);
  let far = qx.max(qy).min(0.0);
  let ox = qx.max(0.0);
  let oy = qy.max(0.0);
  let part = 0.5 - ((ox * ox + oy * oy).sqrt() + far - rad);
  return (part.max(0.0).min(1.0) * 255.0) as i32;
}

// The corner radius `r`, at most half of the shorter side of w by h.
function radius_in(r: i32, w: i32, h: i32): i32 {
  return r.min(w / 2).min(h / 2).max(0);
}

// A rounded line of `width` pixels along the edge of the box of `n`.
function outline(fr: draw.Frame, n: Node, color: i32, r0: i32, width: i32): void {
  let r = radius_in(r0, n.w, n.h);
  let band = width.max(r);
  for py in 0..n.h {
    let edge_row = py < band || py >= n.h - band;
    let px = 0;
    while (px < n.w) {
      if (!edge_row && px == width && n.w - width > width) {
        px = n.w - width;
      }
      let a = inside(px, py, n.w, n.h, r) - inside(px - width, py - width, n.w - 2 * width, n.h - 2 * width, (r - width).max(0));
      fr.cover(n.x + px, n.y + py, color, a);
      px += 1;
    }
  }
}

// Takes the pixels of the box of `n` outside its rounded corners away.
function round_off(fr: draw.Frame, n: Node, r0: i32): void {
  let r = radius_in(r0, n.w, n.h);
  if (r == 0) {
    return;
  }
  for py in 0..n.h {
    if (py >= r && py < n.h - r) {
      continue;
    }
    for px in 0..n.w {
      if (px >= r && px < n.w - r) {
        continue;
      }
      let a = inside(px, py, n.w, n.h, r);
      if (a < 255) {
        let p = fr.pixel(n.x + px, n.y + py);
        let c = draw.rgba((p & 255) * a / 255, (p >> 8 & 255) * a / 255, (p >> 16 & 255) * a / 255, (p >> 24 & 255) * a / 255);
        fr.store(n.x + px, n.y + py, 1, 1, c);
      }
    }
  }
}

// The image scaled to cover the box of `n`, with the middle of it in the
// box.
function picture(fr: draw.Frame, n: Node, img: draw.Image, r: i32): void {
  if (img.width <= 0 || img.height <= 0 || n.w <= 0 || n.h <= 0) {
    return;
  }
  let (w, h) = draw.cover_size(img.width, img.height, n.w, n.h);
  if (w == img.width && h == img.height) {
    fr.image(n.x, n.y, n.w, n.h, img);
  } else {
    fr.image(n.x, n.y, n.w, n.h, img.scaled(w, h));
  }
  round_off(fr, n, r);
}

function paint(fr: draw.Frame, f: text.Fonts, own c: text.Cache, n: Node): text.Cache {
  match (n.paint) {
    Blank => {
      return c;
    },
    Fill(color, r) => {
      if (r > 0) {
        fr.round_rect(n.x, n.y, n.w, n.h, radius_in(r, n.w, n.h), color);
      } else {
        fr.fill(n.x, n.y, n.w, n.h, color);
      }
      return c;
    },
    Outline(color, r, width) => {
      outline(fr, n, color, r, width);
      return c;
    },
    Label(s, px, face, track, color) => {
      return text.text(c, fr, f, face, px, track, n.x, n.y + text.ascent(f, face, px), s, color);
    },
    Picture(_, img, r) => {
      picture(fr, n, img, r);
      return c;
    },
    Custom(_, f) => {
      f(fr, n.x, n.y, n.w, n.h);
      return c;
    }
  }
}

// Draws node `n` alone on the transparent frame `fr`, with its top left
// corner `m` pixels in from the corner of the frame. The pixels of the
// frame are then premultiplied.
pub function raster(fr: draw.Frame, f: text.Fonts, own c: text.Cache, n: Node, m: i32): text.Cache {
  fr.store(0, 0, fr.width, fr.height, 0);
  return paint(fr, f, c, Node { ...n, x: m, y: m, clip0: 0, clip1: NO_CLIP });
}

// The rows of `fr` from y0 to y1, as a frame of its own.
function band(fr: draw.Frame, y0: i32, y1: i32): draw.Frame {
  let a = y0.max(0);
  let b = y1.min(fr.height);
  if (b <= a) {
    return draw.Frame { ...fr, height: 0 };
  }
  return draw.Frame { ...fr, bits: fr.bits + (a * fr.stride * 4) as usize, height: b - a };
}

// Draws the nodes on the frame, each one between its clip rows. It gives
// the cache with the sprites that it made.
pub function render(fr: draw.Frame, f: text.Fonts, own c: text.Cache, ns: Node[]): text.Cache {
  let cache = c;
  for n in ns {
    if (n.clip0 > 0 || n.clip1 < fr.height) {
      let top = n.clip0.max(0);
      let b = band(fr, top, n.clip1);
      if (b.height > 0) {
        cache = paint(b, f, cache, Node { ...n, y: n.y - top });
      }
    } else {
      cache = paint(fr, f, cache, n);
    }
  }
  return cache;
}
```
