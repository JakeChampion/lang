# Android user interfaces

This module makes the screens of a NativeActivity app from a tree of elements. The app gives a state, a function that makes the tree from the state, and a function that changes the state for each message.

## Model

An app is a `Program`: a `view` and an `update`. `view` takes the state and the `Screen`, and gives an `El`: the tree of what the screen shows. `update` takes the state and a message, and gives the next state. The app does not draw, and it does not find what a touch is on.

The app declares the type of its messages, usually an enum with one variant for each thing that can occur. `El[M]` is a tree whose elements give messages of the type `M`. The compiler finds `M` from the result type of `view`, so the calls that make the elements do not write it. A `match` in `update` must handle each variant, so the compiler finds a message that `update` does not handle.

The executable example below is such an app.

`std/android/ui/app` runs a program on a device. `shell`, `step` and `wait` run it on a `Device`, which is a set of functions. A test on the host gives a `Device` of its own, so it runs the whole app with no phone.

### Elements

`column` puts its elements one below the other, `row` puts them side by side, and `stack` puts them one over the other. `text` shows a text, which wraps at the width of its box. `fitted` makes a text of one line whose letters get smaller to fit its box. `wrapped` makes a row put the elements that do not fit on the next line. `picture` shows an image, and `canvas` shows what a function of the app draws. `box` is an empty rectangle, and `space` takes the room that is left.

Each element has a width and a height. `Hug` takes the size of what the element holds, and `Grow` takes a share of the room that is left. `Exact` is a size in dp. `square` makes an element the largest square that fits in its room, so a picture that grows keeps its shape on a short screen.

The methods of `El` set them, and they set the padding, the gap, the background, the corners and the border. Sizes are in dp, and text sizes are in sp: a text grows with the font size that the person set for the system. `align` sets where a column or a row puts its elements across it, and `justify` sets where it puts them along it.

A text shows each line in the order of the Unicode Bidirectional Algorithm, from `std/bidi`. The first strong letter of a paragraph sets its direction. Hebrew and Arabic paragraphs are right to left. Each line of such a paragraph is at the right of its box when the text has the alignment START. The text of a field does the same, and its caret follows the direction of the character next to it.

When the language of the person is written right to left, `Screen.rtl` is true and the layout mirrors, as Android mirrors a layout. A row puts its first element at the right, and START and END change sides. A list that scrolls across starts at the right, and a slider grows to the left.

The shell lays out as for a layout that is left to right. Then `on_screen` mirrors each node, and the shell mirrors the x of each touch. What a node draws in its box does not mirror, so a text keeps the order of its letters. `mirrored` gives a canvas another paint for such a layout, as the back arrow of `app_bar` has.

`on_tap` gives an element a message. A tap on the element gives that message to `update`. A message can hold values: the row of a list, for example. `on_long` does the same for a long press. `slider` is a bar with a thumb, and `on_slide` gives each element the message of a finger that moves across it.

`field` is a text field of one line, and `area` is a text field of more than one line. A tap gives a field the focus and shows the keyboard of the system, and a tap on its text puts the caret there. The program keeps the text. `on_edit` gives the field a function that makes a message from the new text, and each change gives that message to `update`. `view` gives the field the text that it shows. `on_submit` gives the field the message of the action key of the keyboard. `secret` makes the field show a dot for each character, for a password.

`scroll` makes a column a list that scrolls down, and a row a list that scrolls across. Each list has the fling of an Android list and a spring at each end. `list` is a list that makes only the rows that show, so a list of many rows costs as much as a short one. Each list has a name, which keeps its position from frame to frame.

`on_move` lets a person move the rows of a `list`: a long press lifts a row, and the row goes with the finger. `on_swipe` lets a person drag a row across the list to take it out.

A color is a pixel word of `std/android/draw`, or a role: `PRIMARY`, `ON_PRIMARY`, `INK`, `MUTED`, `SURFACE`, `OUTLINE`, `BACKGROUND`, `TRACK` and `ERROR`. A role takes its color from the `Theme`, which is light or dark as the system is. `seeded` makes a theme from one color, for example the key color of an album cover. `button`, `outlined_button`, `card`, `toggle`, `checkbox`, `labelled_checkbox`, `radio`, `tabs`, `menu`, `app_bar`, `snackbar`, `progress` and `divider` are elements in the look of Material 3.

A canvas gives its paint a color of `std/android/draw`. `tinted` makes a canvas whose paint gets the color of a role, so an icon of `std/android/icons` follows the theme.

A `Nav` is the stack of the pages of an app. `push` opens a page over the top page, and `pop` goes back to the page under it. The first page stays, so `can_pop` is false there. Give the back key to `pop` with `with_back` while `can_pop` is true, and let it close the app on the first page.

### Messages

An element gives the messages of `on_tap`, `on_long`, `on_slide`, `on_edit` and `on_submit`, and a list gives the messages of `on_move` and `on_swipe`. A program gives the others. `with_tick` gives a message each time the time of its `wait` goes by. `with_frames` gives a message for each frame while its `animating` is true, with the time of the frame. `with_back` gives a message for the back key, or lets the back key close the app. `with_system` gives a message for an event of `std/android` that this module does not use itself.

`with_visible` can give a message after layout. Use the list boxes and offsets to find rows that come into view.

`with_close` takes the final state during the Destroy that ends the loop, and returns its closed state. The callback runs before Android frees the context or detaches the loop thread from the JVM. A Destroy that `android.keep_alive` keeps the loop through does not close the state: the app runs on with no window, and a later Destroy closes it.

`with_theme` gives the colors for the light system and for the dark system. `with_theme_of` gives them for the state of the program also.

`update` does nothing to the device. A program that plays a sound or reads a file puts that request in its state, and `with_effects` gives the function that does it.

### The cost of a frame

`with_meter` shows a line over the screen with the cost of the last frame in milliseconds: `view`, the layout, the painter, and the accessibility tree. `frame` is the longest frame and `step` is the longest step of the last 120 frames. A step is the work for one event, with `update` and the effects. Use the meter on a device to find the part that makes an app slow.

### What the module does

`layout` gives each element its box and makes the nodes of `std/android/ui/scene`. The shell draws the nodes with `std/android/ui/painter`, and gives the items of the nodes to the accessibility service. It keeps the content out from under the status bar and the navigation bar. A touch on an element with an action shows it pressed. A drag on a list scrolls it, and a fast drag flings it. The field in focus loses the focus when a finger goes down on something else, when the back key goes down, and after `Submit`.

## Contracts

`view` must be pure: it gives the same tree for the same state and screen. The shell calls it for each frame that it draws.

A list has a name that no other list of the same screen has, and so has a field.

`step` takes the events of `android.run` on the loop thread. Each function of a `Device` runs on that thread.

## Executable example

The example lays out a counter on a screen of 400 by 800 pixels, and gives its `update` the tap on the button.

```fern test name=std-android-ui-example
import "core/map";
import "std/android/text";
import "std/android/ui";

struct Model { count: i32 }

enum Msg { Add }

function view(m: Model, s: ui.Screen): ui.El[Msg] {
  return ui.column([ui.text(m.count.to_string()).sized(72.0), ui.button("Add one", Add)]).spaced(24.0).centered();
}

function update(own m: Model, msg: Msg): Model {
  match (msg) {
    Add => {
      return Model { count: m.count + 1 };
    }
  }
}

function main(): i32 {
  let screen = ui.Screen { w: 400, h: 800, d: 2.0, dark: false, theme: ui.theme(false), left: 0, top: 0, right: 0, bottom: 0, font: 1.0, rtl: false };
  let lay = ui.layout(view(Model { count: 0 }, screen), text.from_faces([], []), screen, map_new(4), ui.no_focus(), ui.no_grab());
  let next = update(Model { count: 0 }, lay.taps[0]);
  if (lay.nodes.len() == 3 && lay.taps.len() == 1 && next.count == 1) {
    return 0;
  }
  return 1;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<theme>>

<<elements>>

<<text fields>>

<<element methods>>

<<widgets>>

<<measurement>>

<<placement>>

<<gestures>>

<<programs>>

<<shell>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module makes the screens of a NativeActivity app from a tree of
// elements.
import "core/map";
import "std/android";
import "std/android/draw";
import "std/android/fonts";
import "std/android/icons";
import "std/android/motion";
import "std/android/text";
import "std/android/ui/painter";
import "std/android/ui/scene";
import "std/bidi";
import "std/f64";
import "std/kv";
import "std/option";
import "std/worker";
```

### `theme`

The colors are the baseline colors of Material 3. A role is a pixel word with no alpha, which no shape can show, so a role and a color go in the same field.

```fern
<<theme>>=
// `track` is behind the bar of `progress`, and `pressed` is over the
// element under a finger. `error` is the color of a text that tells of a
// fault.
pub struct Theme { background: i32, surface: i32, primary: i32, on_primary: i32, ink: i32, muted: i32, outline: i32, track: i32, pressed: i32, error: i32 }

// The colors of the light theme, or of the dark theme, from the color
// `seed`. The primary color has the hue of the seed, and the surfaces and
// the text have a hint of it, as the dynamic colors of Android do.
pub function seeded(seed: i32, dark: boolean): Theme {
  let s = draw.lch_of(seed);
  let c = s.c.max(0.04).min(0.15);
  let tone = (l: f64, chroma: f64): i32 => draw.color_of(draw.Lch { l: l, c: chroma, h: s.h });
  if (dark) {
    let ink = tone(0.93, 0.012);
    let outline = tone(0.66, 0.03);
    return Theme { background: tone(0.17, 0.012), surface: tone(0.23, 0.022), primary: tone(0.82, c * 0.8), on_primary: tone(0.3, c * 0.6), ink: ink, muted: tone(0.81, 0.025), outline: outline, track: draw.with_alpha(outline, 77), pressed: draw.with_alpha(ink, 31), error: draw.rgb(242, 184, 181) };
  }
  let ink = tone(0.21, 0.014);
  let outline = tone(0.58, 0.03);
  return Theme { background: tone(0.985, 0.01), surface: tone(0.95, 0.022), primary: tone(0.48, c), on_primary: draw.rgb(255, 255, 255), ink: ink, muted: tone(0.42, 0.025), outline: outline, track: draw.with_alpha(outline, 77), pressed: draw.with_alpha(ink, 31), error: draw.rgb(179, 38, 30) };
}

// The colors of the light theme, or of the dark theme.
pub function theme(dark: boolean): Theme {
  if (dark) {
    return Theme { background: draw.rgb(20, 18, 24), surface: draw.rgb(33, 31, 38), primary: draw.rgb(208, 188, 255), on_primary: draw.rgb(56, 30, 114), ink: draw.rgb(230, 224, 233), muted: draw.rgb(202, 196, 208), outline: draw.rgb(147, 143, 153), track: draw.rgba(147, 143, 153, 77), pressed: draw.rgba(230, 224, 233, 31), error: draw.rgb(242, 184, 181) };
  }
  return Theme { background: draw.rgb(254, 247, 255), surface: draw.rgb(243, 237, 247), primary: draw.rgb(103, 80, 164), on_primary: draw.rgb(255, 255, 255), ink: draw.rgb(29, 27, 32), muted: draw.rgb(73, 69, 79), outline: draw.rgb(121, 116, 126), track: draw.rgba(121, 116, 126, 77), pressed: draw.rgba(29, 27, 32, 31), error: draw.rgb(179, 38, 30) };
}

pub const PRIMARY: i32 = 1;

pub const ON_PRIMARY: i32 = 2;

pub const INK: i32 = 3;

pub const MUTED: i32 = 4;

pub const SURFACE: i32 = 5;

pub const OUTLINE: i32 = 6;

pub const BACKGROUND: i32 = 7;

pub const TRACK: i32 = 8;

pub const ERROR: i32 = 9;

// The color of `c` in the theme `t`, when `c` is a role.
pub function (t: Theme) color(c: i32): i32 {
  if (c == PRIMARY) {
    return t.primary;
  }
  if (c == ON_PRIMARY) {
    return t.on_primary;
  }
  if (c == INK) {
    return t.ink;
  }
  if (c == MUTED) {
    return t.muted;
  }
  if (c == SURFACE) {
    return t.surface;
  }
  if (c == OUTLINE) {
    return t.outline;
  }
  if (c == BACKGROUND) {
    return t.background;
  }
  if (c == TRACK) {
    return t.track;
  }
  if (c == ERROR) {
    return t.error;
  }
  return c;
}

// The window of the app: its size in pixels, the pixels of one dp, the
// theme, and the parts at its edges that the bars of the system cover.
// `font` is the scale of the text of the system: a text of 1 sp is `font`
// dp high.
pub struct Screen { w: i32, h: i32, d: f64, dark: boolean, theme: Theme, left: i32, top: i32, right: i32, bottom: i32, font: f64, rtl: boolean }

// The width and the height of the part of the screen that is free, in dp.
pub function (s: Screen) width(): f64 {
  return (s.w - s.left - s.right) as f64 / s.d;
}

pub function (s: Screen) height(): f64 {
  return (s.h - s.top - s.bottom) as f64 / s.d;
}
```

### `elements`

```fern
<<elements>>=
// The size of an element along one axis. Grow has the weight of its share
// of the room that is left. Exact is in dp.
pub enum Size { Hug, Grow(f64), Exact(f64) }

pub const COLUMN: i32 = 0;

pub const ROW: i32 = 1;

pub const STACK: i32 = 2;

pub const TEXT: i32 = 3;

pub const PICTURE: i32 = 4;

pub const CANVAS: i32 = 5;

pub const LIST: i32 = 6;

pub const FIELD: i32 = 7;

pub const SPACE: i32 = 8;

pub const START: i32 = 0;

pub const CENTER: i32 = 1;

pub const END: i32 = 2;

// Across a column or a row: each element takes the whole size.
pub const STRETCH: i32 = 3;

// Along a column or a row: the room that is left goes between the elements.
pub const BETWEEN: i32 = 4;

// Padding in dp.
pub struct Edges { left: f64, top: f64, right: f64, bottom: f64 }

pub struct El[M] {
  kind: i32,
  kids: El[M][],
  w: Size,
  h: Size,
  pad: Edges,
  gap: f64,
  align: i32,
  justify: i32,
  bg: i32,
  radius: f64,
  edge: i32,
  edge_width: f64,
  // The messages of a tap and of a long press.
  tap: Option[M],
  long: Option[M],
  barrier: boolean,
  say: string,
  // The name of the list that the element is, or "".
  list: string,
  // A text: its size in sp, its weight, its color and its most lines.
  content: string,
  size: f64,
  weight: i32,
  ink: i32,
  lines: i32,
  // A picture or a canvas: the key of its pixels.
  key: string,
  image: Option[draw.Image],
  paint: Option[(draw.Frame, i32, i32, i32, i32) => void],
  // A canvas of `tinted`: the paint for the color of the role `ink`.
  tint: Option[(i32) => (draw.Frame, i32, i32, i32, i32) => void],
  tint_rtl: Option[(i32) => (draw.Frame, i32, i32, i32, i32) => void],
  // A list of `count` rows of `row` dp, which `build` makes.
  count: i32,
  row: f64,
  build: Option[(i32) => El[M]],
  start_pinned: boolean,
  // A text field: its name and the text that it shows while it is empty.
  // `content` is its text.
  field: string,
  hint: string,
  // A text field that shows a dot for each character of its text.
  secret: boolean,
  // A text field: the message of a change of its text, and the message of
  // the action key of the keyboard.
  edit: Option[(string) => M],
  submit: Option[M],
  // The message of a finger that is down on the element or moves on it,
  // from the part of the width that the finger is at.
  slide: Option[(f64) => M],
  // A list: the message of a row that a finger moved to a new place, and
  // the message of a row that a finger swiped away.
  moves: Option[(i32, i32) => M],
  swipes: Option[(i32) => M],
  // The element is the largest square that fits in its room.
  square: boolean,
  // A row: the elements that do not fit go on the next line.
  wraps: boolean,
  // A text of one line: its letters get smaller to fit its room.
  fit: boolean,
}

function element[M](kind: i32, kids: El[M][]): El[M] {
  return El { kind: kind, kids: kids, w: Hug, h: Hug, pad: Edges { left: 0.0, top: 0.0, right: 0.0, bottom: 0.0 }, gap: 0.0, align: START, justify: START, bg: 0, radius: 0.0, edge: 0, edge_width: 0.0, tap: None, long: None, barrier: false, say: "", list: "", content: "", size: 14.0, weight: 400, ink: INK, lines: 0, key: "", image: None, paint: None, tint: None, tint_rtl: None, count: 0, row: 0.0, build: None, start_pinned: false, field: "", hint: "", secret: false, edit: None, submit: None, slide: None, moves: None, swipes: None, square: false, wraps: false, fit: false };
}

// The elements one below the other.
pub function column[M](kids: El[M][]): El[M] {
  return element(COLUMN, kids);
}

// The elements side by side.
pub function row[M](kids: El[M][]): El[M] {
  return element(ROW, kids);
}

// The elements one over the other: the last one is on top.
pub function stack[M](kids: El[M][]): El[M] {
  return element(STACK, kids);
}

// An empty rectangle.
pub function box[M](): El[M] {
  return element(STACK, []);
}

// An element that takes the room that is left in a column or a row.
pub function space[M](): El[M] {
  return El { ...element(SPACE, []), w: Grow(1.0), h: Grow(1.0) };
}

// The text `s`, at 14 sp in the ink of the theme.
pub function text[M](s: string): El[M] {
  return El { ...element(TEXT, []), content: s };
}

// The image `img`, scaled to cover the box of the element. `key` names the
// pixels of the image.
pub function picture[M](key: string, img: draw.Image): El[M] {
  return El { ...element(PICTURE, []), key: key, image: Some(img) };
}

// What `paint` draws in the box that it gets as x, y, width and height.
// `key` names what it draws: a new key draws it again.
pub function canvas[M](key: string, paint: (draw.Frame, i32, i32, i32, i32) => void): El[M] {
  return El { ...element(CANVAS, []), key: key, paint: Some(paint) };
}

// What `paint` draws in the color of the role `color`: a canvas that follows
// the theme. The canvas draws again when the color changes.
pub function tinted[M](key: string, color: i32, paint: (i32) => (draw.Frame, i32, i32, i32, i32) => void): El[M] {
  return El { ...element(CANVAS, []), key: key, ink: color, tint: Some(paint) };
}

// The canvas `e` with `paint` in place of its own in a layout that is right
// to left, such as an arrow that points the other way.
pub function (e: El[M]) mirrored(paint: (i32) => (draw.Frame, i32, i32, i32, i32) => void): El[M] {
  return El { ...e, tint_rtl: Some(paint) };
}

// A list with the name `name` that scrolls: `count` rows of `row` dp each.
// `build` makes the row of an index, and the list makes only the rows that
// show.
pub function list[M](name: string, count: i32, row: f64, build: (i32) => El[M]): El[M] {
  return El { ...element(LIST, []), list: name, count: count, row: row, build: Some(build), w: Grow(1.0), h: Grow(1.0) };
}

// The list with content before and after a header that stays at the top.
// A new list starts at the header when start_pinned is true.
pub function (e: El[M]) sticky_header(before: El[M], header: El[M], after: El[M], start_pinned: boolean): El[M] {
  return El { ...e, kids: [before, header, after], start_pinned: start_pinned };
}
```

### `text fields`

A field is 56 dp high, as a text field of Material 3. The line below it is in the primary color while the field has the focus, and a caret stands where the next character goes. A text that is wider than the field shows the part around the caret. Out of focus, it shows its end.

An `area` wraps its text. When the text has more lines than the area, the area shows the lines around the caret.

The node of a field has a tap action with the name of the field after `FIELD_ACTION`. The shell gives the focus to the field for that action.

```fern
<<text fields>>=
// The start of the name of the tap action of the node of a field.
pub const FIELD_ACTION: string = "std/android/ui.field:";

// A text field with the name `name`, which shows `value`, or `hint` while
// `value` is empty. A tap gives it the focus and shows the keyboard.
// `on_edit` and `on_submit` give it its messages.
pub function field[M](name: string, value: string, hint: string): El[M] {
  return El { ...element(FIELD, []), field: name, content: value, hint: hint, size: 16.0, w: Grow(1.0), h: Exact(56.0), pad: Edges { left: 16.0, top: 0.0, right: 16.0, bottom: 0.0 }, bg: SURFACE, radius: 4.0 };
}

// A text field of `rows` lines. Its text wraps, and the enter key of the
// keyboard starts a new line, so it gives no message of `on_submit`.
pub function area[M](name: string, value: string, hint: string, rows: i32): El[M] {
  return El { ...field(name, value, hint), lines: rows.max(2), h: Hug, pad: Edges { left: 16.0, top: 16.0, right: 16.0, bottom: 16.0 } };
}

// The field `name` in the tree `e`.
function field_el[M](e: El[M], name: string): Option[El[M]] {
  if (e.kind == FIELD && e.field == name) {
    return Some(e);
  }
  for k in e.kids {
    if let Some(f) = field_el(k, name) {
      return Some(f);
    }
  }
  return None;
}

// Each change of the text of the field gives the message that `edit` makes
// from the new text.
pub function (e: El[M]) on_edit(edit: (string) => M): El[M] {
  return El { ...e, edit: Some(edit) };
}

// The field shows a dot for each character of its text, as a field of a
// password does. An accessibility service reads the count of the characters.
pub function (e: El[M]) secret(): El[M] {
  return El { ...e, secret: true };
}

// The action key of the keyboard gives the message `m`.
pub function (e: El[M]) on_submit(m: M): El[M] {
  return El { ...e, submit: Some(m) };
}

// The field that has the focus, or "", and the character that its caret
// is before.
pub struct Focus { field: string, caret: i32 }

pub function no_focus(): Focus {
  return Focus { field: "", caret: 0 };
}
```

### `element methods`

```fern
<<element methods>>=
// The width and the height in dp.
pub function (e: El[M]) width(dp: f64): El[M] {
  return El { ...e, w: Exact(dp) };
}

pub function (e: El[M]) height(dp: f64): El[M] {
  return El { ...e, h: Exact(dp) };
}

// The element takes a share of the room that is left along the column or
// the row that holds it, with the weight `weight`.
pub function (e: El[M]) grow(weight: f64): El[M] {
  return El { ...e, w: Grow(weight), h: Grow(weight) };
}

// The element takes the whole width, or the whole height, that it can get.
pub function (e: El[M]) wide(): El[M] {
  return El { ...e, w: Grow(1.0) };
}

pub function (e: El[M]) tall(): El[M] {
  return El { ...e, h: Grow(1.0) };
}

// The element as the largest square that fits in its room, and no wider
// than its exact width. With `tall`, a picture in a column takes the
// height that the other elements leave, and keeps its shape.
pub function (e: El[M]) square(): El[M] {
  return El { ...e, square: true };
}

// Padding of `dp` on each side.
pub function (e: El[M]) padding(dp: f64): El[M] {
  return El { ...e, pad: Edges { left: dp, top: dp, right: dp, bottom: dp } };
}

// Padding of `x` dp at the left and the right, and `y` dp at the top and
// the bottom.
pub function (e: El[M]) padding_xy(x: f64, y: f64): El[M] {
  return El { ...e, pad: Edges { left: x, top: y, right: x, bottom: y } };
}

// The room between the elements of a column or a row, in dp.
pub function (e: El[M]) spaced(dp: f64): El[M] {
  return El { ...e, gap: dp };
}

// Where a column or a row puts its elements across it: START, CENTER, END
// or STRETCH. A stack puts its elements there from left to right.
pub function (e: El[M]) aligned(a: i32): El[M] {
  return El { ...e, align: a };
}

// Where a column or a row puts its elements along it: START, CENTER, END
// or BETWEEN. A stack puts its elements there from top to bottom.
pub function (e: El[M]) justified(j: i32): El[M] {
  return El { ...e, justify: j };
}

// The elements go in the middle, along and across.
pub function (e: El[M]) centered(): El[M] {
  return El { ...e, align: CENTER, justify: CENTER };
}

// The color behind the element.
pub function (e: El[M]) background(color: i32): El[M] {
  return El { ...e, bg: color };
}

// Round corners of `dp`.
pub function (e: El[M]) rounded(dp: f64): El[M] {
  return El { ...e, radius: dp };
}

// A line of `dp` in `color` along the edge.
pub function (e: El[M]) border(color: i32, dp: f64): El[M] {
  return El { ...e, edge: color, edge_width: dp };
}

// A tap gives the message `m`.
pub function (e: El[M]) on_tap(m: M): El[M] {
  return El { ...e, tap: Some(m) };
}

// A finger that goes down on the element, or moves on it, gives the message
// that `slide` makes from where the finger is across the element: 0 at the
// left and 1 at the right. A drag that starts on the element scrolls no
// list.
pub function (e: El[M]) on_slide(slide: (f64) => M): El[M] {
  return El { ...e, slide: Some(slide) };
}

// A long press gives the message `m`.
pub function (e: El[M]) on_long(m: M): El[M] {
  return El { ...e, long: Some(m) };
}

// A long press on a row of the list lifts the row, and a drag moves it.
// When the finger lifts, the list gives the message that `moved` makes
// from the row and its new place. Only a `list` moves its rows.
pub function (e: El[M]) on_move(moved: (i32, i32) => M): El[M] {
  return El { ...e, moves: Some(moved) };
}

// A drag across a row of the list moves the row across. When the finger
// lifts two fifths of the width of the list away, the list gives the
// message that `swiped` makes from the row. Only a `list` swipes its rows.
pub function (e: El[M]) on_swipe(swiped: (i32) => M): El[M] {
  return El { ...e, swipes: Some(swiped) };
}

// Touches do not go to the elements below this one.
pub function (e: El[M]) modal(): El[M] {
  return El { ...e, barrier: true };
}

// What an accessibility service reads for the element.
pub function (e: El[M]) described(say: string): El[M] {
  return El { ...e, say: say };
}

// A column that scrolls down, or a row that scrolls across, as the list
// `name`.
pub function (e: El[M]) scroll(name: string): El[M] {
  return El { ...e, list: name };
}

// The size of a text in sp, its weight from 100 to 900, and its color.
pub function (e: El[M]) sized(sp: f64): El[M] {
  return El { ...e, size: sp };
}

pub function (e: El[M]) weighted(weight: i32): El[M] {
  return El { ...e, weight: weight };
}

pub function (e: El[M]) colored(color: i32): El[M] {
  return El { ...e, ink: color };
}

// A text of at most `n` lines: the last line ends with an ellipsis when
// the text is longer.
pub function (e: El[M]) clamped(n: i32): El[M] {
  return El { ...e, lines: n };
}

// A text of one line whose letters get smaller, down to 0.7 of their
// size, when the text is wider than its room. A text that is still too
// wide ends with an ellipsis.
pub function (e: El[M]) fitted(): El[M] {
  return El { ...e, fit: true, lines: 1 };
}

// A row that puts the elements that do not fit on the next line. The gap
// of the row is also between its lines. `justify` sets where each line
// puts its elements. An element of such a row does not grow.
pub function (e: El[M]) wrapped(): El[M] {
  return El { ...e, wraps: true };
}
```

### `widgets`

A switch is 52 dp by 32 dp, as in Material 3. A button, a checkbox and a radio button are 48 dp high, which is the least size that Android recommends for a touch target. `labelled_checkbox` is a checkbox with its text.

```fern
<<widgets>>=
function button_row[M](label: string, color: i32): El[M] {
  return row([text(label).weighted(500).colored(color).clamped(1)]).centered().padding_xy(24.0, 0.0).height(48.0).rounded(24.0);
}

// A button in the primary color. A tap gives the message `m`.
pub function button[M](label: string, m: M): El[M] {
  return button_row(label, ON_PRIMARY).background(PRIMARY).on_tap(m);
}

// A button with a border and no fill.
pub function outlined_button[M](label: string, m: M): El[M] {
  return button_row(label, PRIMARY).border(OUTLINE, 1.0).on_tap(m);
}

// A button with its text only.
pub function text_button[M](label: string, m: M): El[M] {
  return button_row(label, PRIMARY).padding_xy(12.0, 0.0).on_tap(m);
}

// The elements in a column on the surface color, with round corners.
pub function card[M](kids: El[M][]): El[M] {
  return column(kids).padding(16.0).background(SURFACE).rounded(12.0);
}

// A line of 1 dp across a column.
pub function divider[M](): El[M] {
  return box().wide().height(1.0).background(OUTLINE);
}

// A switch that is on or off. A tap gives the message `m`. An accessibility
// service reads `say` and the state of the switch.
pub function toggle[M](on: boolean, say: string, m: M): El[M] {
  let track: El[M] = row([box().width(16.0).height(16.0).rounded(8.0).background(OUTLINE)]).padding(8.0).border(OUTLINE, 2.0);
  let state = "off";
  if (on) {
    track = row([box().width(24.0).height(24.0).rounded(12.0).background(ON_PRIMARY)]).padding(4.0).justified(END).background(PRIMARY);
    state = "on";
  }
  return track.aligned(CENTER).width(52.0).height(32.0).rounded(16.0).on_tap(m).described(say + ", " + state);
}

// The width and the height of the thumb of a slider, in dp.
const THUMB_DP: f64 = 20.0;

// A bar with a thumb that a finger moves. `part` is where the thumb is,
// from 0 to 1. A touch or a drag gives the message that `slide` makes from
// the part under the finger. The slider is 48 dp high, so a finger finds
// it.
pub function slider[M](part: f64, slide: (f64) => M): El[M] {
  let at = part.max(0.0).min(1.0);
  let filled: El[M] = El { ...box(), w: Grow(at + 0.0001), h: Grow(1.0) };
  let rest: El[M] = El { ...box(), w: Grow(1.0001 - at), h: Grow(1.0) };
  let track: El[M] = row([filled.background(PRIMARY).rounded(2.0), rest]).wide().height(4.0).background(TRACK).rounded(2.0);
  let before: El[M] = El { ...box(), w: Grow(at + 0.0001) };
  let after: El[M] = El { ...box(), w: Grow(1.0001 - at) };
  let thumb: El[M] = box().width(THUMB_DP).height(THUMB_DP).rounded(THUMB_DP / 2.0).background(PRIMARY);
  let bar: El[M] = stack([row([track]).aligned(CENTER).padding_xy(THUMB_DP / 2.0, 0.0).wide().tall(), row([before, thumb, after]).aligned(CENTER).wide().tall()]);
  return bar.wide().height(48.0).on_slide(slide);
}

// A bar that shows the part of a task that is done, from 0 to 1.
pub function progress[M](part: f64): El[M] {
  let done = part.max(0.0).min(1.0);
  let filled: El[M] = El { ...box(), w: Grow(done + 0.0001), h: Grow(1.0) };
  let rest: El[M] = El { ...box(), w: Grow(1.0001 - done), h: Grow(1.0) };
  return row([filled.background(PRIMARY).rounded(2.0), rest]).wide().height(4.0).background(TRACK).rounded(2.0);
}

// The box of a checkbox, and how an accessibility service reads its state.
function check_mark[M](on: boolean): (El[M], string) {
  if (on) {
    let checked: El[M] = tinted("check", ON_PRIMARY, icons.check);
    return (checked.width(18.0).height(18.0).rounded(2.0).background(PRIMARY), "checked");
  }
  let empty: El[M] = box().width(18.0).height(18.0).rounded(2.0).border(OUTLINE, 2.0);
  return (empty, "not checked");
}

// A box that is checked or not, with room of 48 dp for a finger. A tap
// gives the message `m`. An accessibility service reads `say` and the
// state of the box. The box shows no text: `labelled_checkbox` does.
pub function checkbox[M](on: boolean, say: string, m: M): El[M] {
  let (mark, state) = check_mark[M](on);
  return row([mark]).centered().width(48.0).height(48.0).on_tap(m).described(say + ", " + state);
}

// A checkbox with the text `label` after it. A tap on the box or on the
// text gives the message `m`.
pub function labelled_checkbox[M](on: boolean, label: string, m: M): El[M] {
  let (mark, state) = check_mark[M](on);
  let room: El[M] = row([mark]).centered().width(48.0).height(48.0);
  return row([room, text(label).wide()]).aligned(CENTER).wide().on_tap(m).described(label + ", " + state);
}

// A radio button: one of a group of choices, selected or not. A tap gives
// the message `m`. An accessibility service reads `say` and the state.
pub function radio[M](on: boolean, say: string, m: M): El[M] {
  let ring: El[M] = box().width(20.0).height(20.0).rounded(10.0).border(OUTLINE, 2.0);
  let state = "not selected";
  if (on) {
    ring = row([box().width(10.0).height(10.0).rounded(5.0).background(PRIMARY)]).centered().width(20.0).height(20.0).rounded(10.0).border(PRIMARY, 2.0);
    state = "selected";
  }
  return row([ring]).centered().width(48.0).height(48.0).on_tap(m).described(say + ", " + state);
}

// A row of tabs with the labels `labels`, 48 dp high. A tab with a longer
// label is wider, and a label that is still too wide gets smaller. The tab
// `at` is selected, with a line under it. A tap on a tab gives the message that
// `pick` makes from its index.
pub function tabs[M](labels: string[], at: i32, pick: (i32) => M): El[M] {
  let kids: El[M][] = [];
  for i in 0..labels.len() {
    let color = MUTED;
    let line: El[M] = box().wide().height(3.0);
    let state = "";
    if (i == at) {
      color = PRIMARY;
      line = line.background(PRIMARY);
      state = ", selected";
    }
    let label: El[M] = row([text(labels[i]).weighted(500).colored(color).fitted().wide().aligned(CENTER)]).centered().padding_xy(2.0, 0.0).wide().grow(1.0);
    let say = labels[i] + ", tab " + (i + 1).to_string() + " of " + labels.len().to_string() + state;
    kids = kids.append(column([label, line]).grow((text.letters(labels[i]).len() + 4) as f64).height(48.0).on_tap(pick(i)).described(say));
  }
  return row(kids).wide();
}

// The items of a menu, one below the other, 48 dp high each. A tap on an
// item gives the message that `pick` makes from its index. The menu takes
// the touches over it.
pub function menu[M](items: string[], pick: (i32) => M): El[M] {
  let kids: El[M][] = [];
  for i in 0..items.len() {
    kids = kids.append(row([text(items[i])]).aligned(CENTER).padding_xy(12.0, 0.0).height(48.0).on_tap(pick(i)));
  }
  return column(kids).aligned(STRETCH).padding_xy(0.0, 8.0).background(SURFACE).rounded(4.0).border(OUTLINE, 1.0).modal();
}

// The bar at the top of a screen, 64 dp high, with the title `title`. With
// a message `back`, an arrow at the start gives it. The elements `actions`
// go at the end.
pub function app_bar[M](title: string, back: Option[M], actions: El[M][]): El[M] {
  let kids: El[M][] = [];
  let start = 16.0;
  if let Some(m) = back {
    let arrow: El[M] = tinted("back", INK, icons.back).mirrored(icons.forward);
    kids = kids.append(arrow.width(48.0).height(48.0).on_tap(m).described("Back"));
    start = 4.0;
  }
  kids = kids.append(text(title).sized(22.0).fitted().wide());
  for a in actions {
    kids = kids.append(a);
  }
  let bar: El[M] = row(kids).aligned(CENTER).spaced(4.0).wide().height(64.0);
  return El { ...bar, pad: Edges { left: start, top: 0.0, right: 4.0, bottom: 0.0 } };
}

// A short note at the bottom of a screen, in the colors of the ink. A tap on
// `action` gives the message `m`.
pub function snackbar[M](note: string, action: string, m: M): El[M] {
  let act: El[M] = row([text(action).weighted(500).colored(BACKGROUND)]).centered().padding_xy(12.0, 0.0).height(48.0).rounded(24.0).on_tap(m);
  return row([text(note).colored(BACKGROUND).clamped(2).wide(), act]).aligned(CENTER).padding_xy(16.0, 4.0).wide().background(INK).rounded(4.0);
}

// The stack of the pages of an app, with the page that shows on top.
pub struct Nav[P] { pages: P[] }

// A stack with the first page `first`.
pub function nav[P](first: P): Nav[P] {
  return Nav { pages: [first] };
}

// The page that shows.
pub function (n: Nav[P]) top(): P {
  return n.pages[n.pages.len() - 1];
}

// The stack with the page `p` over its top page.
pub function (n: Nav[P]) push(p: P): Nav[P] {
  return Nav { pages: n.pages.append(p) };
}

// Whether a page is under the top page.
pub function (n: Nav[P]) can_pop(): boolean {
  return n.pages.len() > 1;
}

// The stack without its top page. The first page stays.
pub function (n: Nav[P]) pop(): Nav[P] {
  let kept: P[] = [];
  for i in 0..(n.pages.len() - 1).max(1) {
    kept = kept.append(n.pages[i]);
  }
  return Nav { pages: kept };
}

// The number of pages.
pub function (n: Nav[P]) depth(): i32 {
  return n.pages.len();
}
```

### `measurement`

`measure` gives the size that an element takes in the room that it can get. An element in a list that scrolls can get any height, which is the size `UNBOUNDED`.

`sizes` gives the size of each element of a column or a row. The elements with no share take their size first. The elements that grow then divide the room that is left by their weights.

```fern
<<measurement>>=
const UNBOUNDED: i32 = 1 << 28;

function bounded(v: i32): boolean {
  return v < UNBOUNDED / 2;
}

struct Env { fonts: text.Fonts, d: f64, font: f64, theme: Theme, offsets: Map[string, i32], focus: Focus, grab: Grab, rtl: boolean }

function px(env: Env, dp: f64): i32 {
  return (dp * env.d).round() as i32;
}

// The pixels of a text of `size` sp, at the scale of the text of the
// system.
function sp(env: Env, size: f64): i32 {
  return (size * env.d * env.font).round().max(1.0) as i32;
}

// The exact size of `s` in pixels, or -1.
function exact(env: Env, s: Size): i32 {
  match (s) {
    Exact(dp) => {
      return px(env, dp);
    },
    _ => {
      return -1;
    }
  }
}

// The weight of `s`, or 0 for a size that does not grow.
function weight_of(s: Size): f64 {
  match (s) {
    Grow(w) => {
      return w;
    },
    _ => {
      return 0.0;
    }
  }
}

// The lines of a text in the order to show them, whether each line is in a
// paragraph that is right to left, the pixels of its em, its face and the
// height of a line.
struct Lines { lines: string[], rtl: boolean[], px: i32, face: i32, line: i32 }

function text_lines[M](env: Env, e: El[M], room: i32): Lines {
  let face = text.face_for(env.fonts, e.weight);
  let size = fitted_px(env, e, face, room);
  let ranges = text.wrap_ranges(env.fonts, face, size, e.content, room);
  let r = bidi.resolve(e.content, bidi.AUTO);
  let n = ranges.len();
  if (e.lines > 0) {
    n = n.min(e.lines);
  }
  let lines: string[] = [];
  let rtl: boolean[] = [];
  for i in 0..n {
    let (a, b) = ranges[i];
    let level = r.paragraph_level_at(a);
    rtl = rtl.append(level == 1);
    if (i == n - 1 && n < ranges.len()) {
      let rest = text.fit(env.fonts, face, size, rest_of(e.content, ranges, i), room);
      lines = lines.append(bidi.display(rest, level));
    } else {
      lines = lines.append(r.line(a, b));
    }
  }
  return Lines { lines: lines, rtl: rtl, px: size, face: face, line: text.line(env.fonts, face, size) };
}

// The pixels of the em of the text `e` in a room of `room` pixels.
function fitted_px[M](env: Env, e: El[M], face: i32, room: i32): i32 {
  let size = sp(env, e.size);
  if (!e.fit || !bounded(room)) {
    return size;
  }
  let width = text.width(env.fonts, face, size, e.content);
  if (width <= room) {
    return size;
  }
  let least = (size as f64 * 0.7).ceil() as i32;
  return (size * room / width).max(least);
}

// The lines of `s` from the line `i`, joined by spaces.
function rest_of(s: string, ranges: (i32, i32)[], i: i32): string {
  let cs = text.letters(s);
  let parts: string[] = [];
  for k in i..ranges.len() {
    let (a, b) = ranges[k];
    parts = parts.append(text.span(cs, a, b));
  }
  return parts.join(" ");
}

// The weight of `k` along a row when `across` is true, or along a column.
function weight_along[M](k: El[M], across: boolean): f64 {
  if (across) {
    return weight_of(k.w);
  }
  return weight_of(k.h);
}

// The sizes `out` of the elements of `e`, with a share of `rest` pixels for
// each element that grows. `total` is the sum of their weights.
function shared[M](env: Env, e: El[M], own out: (i32, i32)[], rest: i32, total: f64, iw: i32, ih: i32): (i32, i32)[] {
  let across = e.kind == ROW;
  let sized = out;
  for i in 0..e.kids.len() {
    let (w0, _) = sized[i];
    if (w0 >= 0) {
      continue;
    }
    let k = e.kids[i];
    let share = (rest as f64 * weight_along(k, across) / total) as i32;
    let (w, h) = if (across) { measure(env, k, share, ih) } else { measure(env, k, iw, share) };
    if (k.square) {
      sized = sized.with(i, (w, h));
    } else if (across) {
      sized = sized.with(i, (share, h));
    } else {
      sized = sized.with(i, (w, share));
    }
  }
  return sized;
}

// The size of each element of the column or the row `e`, in the room iw
// by ih.
function sizes[M](env: Env, e: El[M], iw: i32, ih: i32): (i32, i32)[] {
  let across = e.kind == ROW;
  let room = ih;
  if (across) {
    room = iw;
  }
  let out: (i32, i32)[] = [];
  let used = px(env, e.gap) * (e.kids.len() - 1).max(0);
  let total = 0.0;
  for k in e.kids {
    let weight = weight_along(k, across);
    if (weight > 0.0 && bounded(room)) {
      total += weight;
      out = out.append((-1, -1));
      continue;
    }
    let left = room;
    if (bounded(room)) {
      left = (room - used).max(0);
    }
    if (across) {
      let (w, h) = measure(env, k, left, ih);
      used += w;
      out = out.append((w, h));
    } else {
      let (w, h) = measure(env, k, iw, left);
      used += h;
      out = out.append((w, h));
    }
  }
  if (total > 0.0) {
    return shared(env, e, out, (room - used).max(0), total, iw, ih);
  }
  return out;
}

// The size of the lines of the text `e` in a room of `iw` pixels.
function text_size[M](env: Env, e: El[M], iw: i32): (i32, i32) {
  let t = text_lines(env, e, iw);
  let w = 0;
  for l in t.lines {
    w = w.max(text.width(env.fonts, t.face, t.px, l));
  }
  return (w, t.lines.len() * t.line);
}

// The size of the largest element of the stack `e`.
function stack_size[M](env: Env, e: El[M], iw: i32, ih: i32): (i32, i32) {
  let (w, h) = (0, 0);
  for k in e.kids {
    let (kw, kh) = measure(env, k, iw, ih);
    w = w.max(kw);
    h = h.max(kh);
  }
  return (w, h);
}

// The size of each element of the row `e` that wraps, and the line that
// each element is on, in a room of `iw` pixels.
function wrapped_sizes[M](env: Env, e: El[M], iw: i32, ih: i32): ((i32, i32)[], i32[]) {
  let gap = px(env, e.gap);
  let ss: (i32, i32)[] = [];
  let lines: i32[] = [];
  let line = 0;
  let used = 0;
  for k in e.kids {
    let (w, h) = measure(env, k, iw, ih);
    if (used > 0 && used + gap + w > iw) {
      line += 1;
      used = 0;
    }
    if (used > 0) {
      used += gap;
    }
    used += w;
    ss = ss.append((w, h));
    lines = lines.append(line);
  }
  return (ss, lines);
}

// The width and the height of each line of a row that wraps.
function line_sizes(ss: (i32, i32)[], lines: i32[], gap: i32): (i32, i32)[] {
  let out: (i32, i32)[] = [];
  for i in 0..ss.len() {
    let (w, h) = ss[i];
    if (lines[i] == out.len()) {
      out = out.append((w, h));
    } else {
      let (lw, lh) = out[lines[i]];
      out = out.with(lines[i], (lw + gap + w, lh.max(h)));
    }
  }
  return out;
}

function wrapped_size[M](env: Env, e: El[M], iw: i32, ih: i32): (i32, i32) {
  let gap = px(env, e.gap);
  let (ss, lines) = wrapped_sizes(env, e, iw, ih);
  let (w, h) = (0, 0);
  for l in line_sizes(ss, lines, gap) {
    let (lw, lh) = l;
    w = w.max(lw);
    h += lh + (if (h > 0) { gap } else { 0 });
  }
  return (w, h);
}

// The size of the elements of the column or the row `e`, with their gaps.
function flow_size[M](env: Env, e: El[M], iw: i32, ih: i32): (i32, i32) {
  if (e.wraps && e.kind == ROW && e.list.len() == 0 && bounded(iw)) {
    return wrapped_size(env, e, iw, ih);
  }
  let (rw, rh) = (iw, ih);
  if (e.list.len() > 0) {
    if (e.kind == ROW) {
      rw = UNBOUNDED;
    } else {
      rh = UNBOUNDED;
    }
  }
  let gaps = px(env, e.gap) * (e.kids.len() - 1).max(0);
  let (w, h) = (0, 0);
  let ss = sizes(env, e, rw, rh);
  for i in 0..ss.len() {
    let (kw, kh) = ss[i];
    // A space takes room along the flow only, so it adds nothing to the
    // size of the flow across.
    let across = e.kids[i].kind != SPACE;
    if (e.kind == ROW) {
      w += kw;
      if (across) {
        h = h.max(kh);
      }
    } else {
      if (across) {
        w = w.max(kw);
      }
      h += kh;
    }
  }
  if (e.kind == ROW) {
    return (w + gaps, h);
  }
  return (w, h + gaps);
}

// The size of what `e` holds, in the room iw by ih.
function content[M](env: Env, e: El[M], iw: i32, ih: i32): (i32, i32) {
  if (e.kind == TEXT) {
    return text_size(env, e, iw);
  }
  if (e.kind == PICTURE) {
    if let Some(img) = e.image {
      return (img.width, img.height);
    }
    return (0, 0);
  }
  if (e.kind == LIST) {
    let (before, header, after) = header_heights(env, e, iw, ih);
    return (0, before + header + after + e.count * px(env, e.row));
  }
  if (e.kind == FIELD) {
    return (0, e.lines.max(1) * text.line(env.fonts, text.face_for(env.fonts, e.weight), sp(env, e.size)));
  }
  if (e.kind == COLUMN || e.kind == ROW) {
    return flow_size(env, e, iw, ih);
  }
  return stack_size(env, e, iw, ih);
}

// The side of the largest square in the room ow by oh. A side with no
// bound does not limit it.
function square_side(ow: i32, oh: i32): i32 {
  if (!bounded(ow)) {
    return oh.max(0);
  }
  if (!bounded(oh)) {
    return ow.max(0);
  }
  return ow.min(oh).max(0);
}

// The size of `e` in the room aw by ah.
function measure[M](env: Env, e: El[M], aw: i32, ah: i32): (i32, i32) {
  let ew = exact(env, e.w);
  let eh = exact(env, e.h);
  let ow = aw;
  if (ew >= 0) {
    ow = ew;
  }
  let oh = ah;
  if (eh >= 0) {
    oh = eh;
  }
  let pw = px(env, e.pad.left) + px(env, e.pad.right);
  let ph = px(env, e.pad.top) + px(env, e.pad.bottom);
  let (cw, ch) = content(env, e, (ow - pw).max(0), (oh - ph).max(0));
  let w = (cw + pw).min(aw);
  if (ew >= 0) {
    w = ew;
  } else if (weight_of(e.w) > 0.0 && bounded(aw)) {
    w = aw;
  }
  let h = (ch + ph).min(ah);
  if (eh >= 0) {
    h = eh;
  } else if (weight_of(e.h) > 0.0 && bounded(ah)) {
    h = ah;
  }
  if (e.square) {
    let side = square_side(ow, oh);
    return (side, side);
  }
  return (w, h);
}
```

### `placement`

`place` gives an element its box and adds its nodes. The first node of an element has its actions. An element with an action and nothing to show gets a blank node, which only takes touches.

The elements of a list get the name of the list as their group, and the rows of the list as their clip. A `Scroller` tells how far a list can scroll.

```fern
<<placement>>=
// A list that scrolls: its name, its box, and how far it can scroll. It
// scrolls across when `across` is true, and down when it is false.
// `parent` is the name of the list that it is in, or "". A `list` has
// `count` rows of `row` pixels, and other lists have a `row` of 0.
// `moves` and `swipes` are the indexes of the functions of `on_move` and
// `on_swipe` in `Messages`, or -1.
pub struct Scroller {
  name: string,
  x: i32,
  y: i32,
  w: i32,
  h: i32,
  limit: i32,
  across: boolean,
  parent: string,
  row: i32,
  count: i32,
  moves: i32,
  swipes: i32,
  rows_y: i32,
  rows_clip: (i32, i32),
  initial: i32,
  pinned: boolean,
}

// The first row that shows and the index after the last row that shows.
pub function (s: Scroller) visible_rows(): (i32, i32) {
  let (top, bottom) = s.rows_clip;
  if (s.row == 0 || bottom <= top) {
    return (0, 0);
  }
  let first = ((top - s.rows_y) / s.row).max(0).min(s.count);
  let last = ((bottom - s.rows_y + s.row - 1) / s.row).max(first).min(s.count);
  return (first, last);
}

// The nodes of a screen, and its lists.
pub struct Layout { nodes: scene.Node[], scrollers: Scroller[] }

pub function no_layout(): Layout {
  return Layout { nodes: [], scrollers: [] };
}

// The messages of a text field, by the name of the field.
pub struct FieldMessages[M] { name: string, edit: Option[(string) => M], submit: Option[M] }

// The messages of a screen. The tap action or the long press action of a
// node that gives a message has the name `MESSAGE_ACTION` and the index of
// the message in `taps`. The tap action of a node that a finger slides on
// has the name `SLIDER_ACTION` and the index of its function in `slides`.
pub struct Messages[M] { taps: M[], fields: FieldMessages[M][], slides: ((f64) => M)[], moves: ((i32, i32) => M)[], swipes: ((i32) => M)[] }

// The name of the tap action of a node that a finger slides on.
pub const SLIDER_ACTION: string = "slider";

pub function no_messages[M](): Messages[M] {
  return Messages { taps: [], fields: [], slides: [], moves: [], swipes: [] };
}

// The name of the action of a node that gives a message. An accessibility
// service reads the name for a node that has no text and no description.
pub const MESSAGE_ACTION: string = "button";

// A screen: its nodes, its lists and its messages.
pub struct Built[M] { nodes: scene.Node[], scrollers: Scroller[], taps: M[], fields: FieldMessages[M][], slides: ((f64) => M)[], moves: ((i32, i32) => M)[], swipes: ((i32) => M)[] }

// The nodes and the lists of the screen.
pub function (b: Built[M]) layout(): Layout {
  return Layout { nodes: b.nodes, scrollers: b.scrollers };
}

// The messages of the screen.
pub function (b: Built[M]) messages(): Messages[M] {
  return Messages { taps: b.taps, fields: b.fields, slides: b.slides, moves: b.moves, swipes: b.swipes };
}

// The screen with the message `m`, and the action that gives it. No
// message gives no action.
function with_message[M](own out: Built[M], m: Option[M]): (Built[M], scene.Action) {
  if let Some(message) = m {
    let at = out.taps.len();
    return (Built { nodes: out.nodes, scrollers: out.scrollers, taps: out.taps.append(message), fields: out.fields, slides: out.slides, moves: out.moves, swipes: out.swipes }, scene.Action { name: MESSAGE_ACTION, arg: at });
  }
  return (out, scene.no_action());
}

// The list that the nodes of an element are in, with its rows and its
// columns.
struct Clip { group: string, c0: i32, c1: i32, left: i32, right: i32 }

// The node `n` in the list of `clip`.
function clipped(n: scene.Node, clip: Clip): scene.Node {
  return scene.Node { ...n, group: clip.group, clip0: clip.c0, clip1: clip.c1, left: clip.left, right: clip.right };
}

// The alignment `a` of a line of text: START and STRETCH are at the right
// of a line that is right to left.
function along_line(a: i32, rtl: boolean): i32 {
  if (!rtl || a == CENTER) {
    return a;
  }
  if (a == END) {
    return START;
  }
  return END;
}

// Where `rest` pixels of free room go before an element, for an alignment.
function shift(a: i32, rest: i32): i32 {
  if (a == CENTER) {
    return rest.max(0) / 2;
  }
  if (a == END) {
    return rest.max(0);
  }
  return 0;
}

// What the element shows of its own in the box x, y, w, h: its background,
// its border, and its picture or its canvas.
function paints[M](env: Env, e: El[M], x: i32, y: i32, w: i32, h: i32): scene.Node[] {
  let r = px(env, e.radius);
  let ns: scene.Node[] = [];
  if (e.bg != 0) {
    ns = ns.append(scene.node(x, y, w, h, scene.Fill(env.theme.color(e.bg), r)));
  }
  let line = px(env, e.edge_width);
  if (e.edge != 0 && line > 0) {
    ns = ns.append(scene.node(x, y, w, h, scene.Outline(env.theme.color(e.edge), r, line)));
  }
  if let Some(img) = e.image {
    ns = ns.append(scene.node(x, y, w, h, scene.Picture(e.key, img, r)));
  }
  if let Some(f) = e.paint {
    ns = ns.append(scene.node(x, y, w, h, scene.Custom(e.key, 0, f)));
  }
  let tint = e.tint;
  let key = e.key;
  if (env.rtl && e.tint_rtl.is_some()) {
    tint = e.tint_rtl;
    key += "|rtl";
  }
  if let Some(t) = tint {
    let color = env.theme.color(e.ink);
    ns = ns.append(scene.node(x, y, w, h, scene.Custom(key + "#" + color.to_string(), 0, t(color))));
  }
  return ns;
}

// The element takes touches, or an accessibility service reads it.
function takes_part[M](e: El[M]): boolean {
  return e.tap.is_some() || e.long.is_some() || e.slide.is_some() || e.kind == FIELD || e.barrier || e.say.len() > 0 || e.list.len() > 0;
}

// What an accessibility service reads for the element: a field with a
// text has its hint and its text.
function spoken[M](e: El[M]): string {
  if (e.kind == FIELD && e.say.len() == 0 && e.content.len() > 0) {
    if (e.secret) {
      return e.hint + ", " + text.letters(e.content).len().to_string() + " characters";
    }
    return e.hint + ", " + e.content;
  }
  return e.say;
}

// The nodes of the element itself. The first one has the actions `tap` and
// `long`.
function own_nodes[M](env: Env, e: El[M], x: i32, y: i32, w: i32, h: i32, clip: Clip, tap: scene.Action, long: scene.Action): scene.Node[] {
  let ns = paints(env, e, x, y, w, h);
  if (ns.len() == 0 && takes_part(e)) {
    ns = ns.append(scene.node(x, y, w, h, scene.Blank));
  }
  let own = clip;
  if (e.list.len() > 0) {
    own = Clip { ...clip, group: e.list };
  }
  let out: scene.Node[] = [];
  for i in 0..ns.len() {
    let n = clipped(ns[i], own);
    if (i == 0) {
      n = scene.Node { ...n, tap: tap, long: long, barrier: e.barrier, say: spoken(e) };
    }
    out = out.append(n);
  }
  return out;
}

// How far the list `name` scrolls: from 0 to `limit`, or as far as `room`
// pixels past an end while a finger pulls it there.
function offset_of(env: Env, name: string, limit: i32, room: i32, initial: i32): i32 {
  return env.offsets.get(name).unwrap_or(initial).max(-room).min(limit + room);
}

function place_text[M](env: Env, e: El[M], x: i32, y: i32, w: i32, clip: Clip, own out: Built[M]): Built[M] {
  let t = text_lines(env, e, w);
  let nodes = out.nodes;
  let color = env.theme.color(e.ink);
  for i in 0..t.lines.len() {
    let l = t.lines[i];
    let lw = text.width(env.fonts, t.face, t.px, l);
    let n = scene.node(x + shift(along_line(e.align, t.rtl[i] != env.rtl), w - lw), y + i * t.line, lw, t.line, scene.Label(l, t.px, t.face, 0, color));
    nodes = nodes.append(clipped(n, clip));
  }
  return Built { nodes: nodes, scrollers: out.scrollers, taps: out.taps, fields: out.fields, slides: out.slides, moves: out.moves, swipes: out.swipes };
}

// What a field shows: the levels of its text, each line from its start up
// to its end in that text, the lines in the order to show them and the x
// of each, and the line and the x of the caret. The line of the caret is
// -1 for a field that does not have the focus.
struct FieldView { text: bidi.Resolved, starts: i32[], ends: i32[], lines: string[], xs: i32[], caret_line: i32, caret_x: i32 }

// The pixels of the em, the face and the height of a line of the field.
function field_type[M](env: Env, e: El[M]): (i32, i32, i32) {
  let size = sp(env, e.size);
  let face = text.face_for(env.fonts, e.weight);
  return (size, face, text.line(env.fonts, face, size));
}

// The view of the lines from `starts` up to `ends` of `r` in `w` pixels.
// A line of a paragraph that is right to left is at the right.
function field_lines[M](env: Env, e: El[M], w: i32, r: bidi.Resolved, starts: i32[], ends: i32[]): FieldView {
  let (size, face, _) = field_type(env, e);
  let lines: string[] = [];
  let xs: i32[] = [];
  for i in 0..starts.len() {
    let l = r.line(starts[i], ends[i]);
    lines = lines.append(l);
    xs = xs.append(if (r.paragraph_level_at(starts[i]) == 1 != env.rtl) { w - text.width(env.fonts, face, size, l) } else { 0 });
  }
  return FieldView { text: r, starts: starts, ends: ends, lines: lines, xs: xs, caret_line: -1, caret_x: 0 };
}

// The view `v` with the caret before the character `caret` of the line
// `row`. The caret is 2 dp wide, 1 dp past the edge of its character. In
// a layout that is right to left, the glyphs of a line do not mirror, so
// the caret is that far from the other end of the line.
function with_caret[M](env: Env, e: El[M], v: FieldView, row: i32, caret: i32): FieldView {
  let (size, face, _) = field_type(env, e);
  let order = v.text.order(v.starts[row], v.ends[row]);
  let cs = text.letters(v.lines[row]);
  let x = 0;
  if (order.len() > 0) {
    let p = after_caret(order, caret);
    // The caret is at the right edge of an RTL character that it is before,
    // or of an LTR character that it is after.
    let rtl = v.text.levels[order[p]] % 2 == 1;
    if (rtl != caret > order[p]) {
      p += 1;
    }
    x = text.width(env.fonts, face, size, text.span(cs, 0, p));
  }
  let at = v.xs[row] + x + px(env, 1.0);
  if (env.rtl) {
    at = v.xs[row] + text.width(env.fonts, face, size, v.lines[row]) - x - px(env, 1.0) - caret_width(env);
  }
  return FieldView { ...v, caret_line: row, caret_x: at };
}

function caret_width(env: Env): i32 {
  return px(env, 2.0).max(1);
}

// The place in `order` of the first character in logical order at or
// after `caret`, or else of the last character.
function after_caret(order: i32[], caret: i32): i32 {
  let best = -1;
  let last = 0;
  for p in 0..order.len() {
    if (order[p] >= caret && (best < 0 || order[p] < order[best])) {
      best = p;
    }
    if (order[p] > order[last]) {
      last = p;
    }
  }
  return if (best < 0) { last } else { best };
}

// A text of one line that a field shows as it is, such as its hint.
function shown_as_is[M](env: Env, e: El[M], w: i32, s: string, direction: i32): FieldView {
  let r = bidi.resolve(s, direction);
  return field_lines(env, e, w, r, [0], [r.cps.len()]);
}

// A field of one line in `w` pixels. In focus it shows the part of its
// text around the caret. Out of focus it shows the end of its text.
function line_view[M](env: Env, e: El[M], w: i32, focused: boolean): FieldView {
  let (size, face, _) = field_type(env, e);
  if (!focused) {
    return shown_as_is(env, e, w, text.fit_end(env.fonts, face, size, e.content, w), bidi.paragraph_level(e.content, bidi.AUTO));
  }
  let cs = text.letters(e.content);
  let caret = env.focus.caret.max(0).min(cs.len());
  let from = 0;
  while (from < caret && text.width(env.fonts, face, size, text.span(cs, from, caret)) > w) {
    from += 1;
  }
  let to = cs.len();
  while (to > caret && text.width(env.fonts, face, size, text.span(cs, from, to)) > w) {
    to -= 1;
  }
  return with_caret(env, e, field_lines(env, e, w, bidi.resolve(e.content, bidi.AUTO), [from], [to]), 0, caret);
}

// A field of more than one line in `w` pixels. In focus it shows the lines
// around the caret. Out of focus it shows its first lines.
function area_view[M](env: Env, e: El[M], w: i32, focused: boolean): FieldView {
  let (size, face, _) = field_type(env, e);
  let cs = text.letters(e.content);
  let ranges = text.wrap_ranges(env.fonts, face, size, e.content, w);
  let caret = env.focus.caret.max(0).min(cs.len());
  let at = 0;
  for i in 0..ranges.len() {
    let (a, _) = ranges[i];
    if (caret >= a) {
      at = i;
    }
  }
  let first = 0;
  if (focused) {
    first = (at - e.lines + 1).max(0);
  }
  let starts: i32[] = [];
  let ends: i32[] = [];
  for i in first..(first + e.lines).min(ranges.len()) {
    let (a, b) = ranges[i];
    starts = starts.append(a);
    ends = ends.append(b);
  }
  let v = field_lines(env, e, w, bidi.resolve(e.content, bidi.AUTO), starts, ends);
  if (!focused) {
    return v;
  }
  return with_caret(env, e, v, at - first, caret);
}

// What the field `e` shows in `w` pixels. A secret field shows a dot in
// the place of each character, so its caret keeps its index.
function field_view[M](env: Env, e: El[M], w: i32): FieldView {
  if (e.secret) {
    return clear_view(env, El { ...e, content: "•".repeat(text.letters(e.content).len()) }, w);
  }
  return clear_view(env, e, w);
}

// What the field `e` shows in `w` pixels. An empty field shows its hint.
function clear_view[M](env: Env, e: El[M], w: i32): FieldView {
  let focused = e.field == env.focus.field;
  if (e.content.len() == 0) {
    let (size, face, _) = field_type(env, e);
    let v = shown_as_is(env, e, w, text.fit(env.fonts, face, size, e.hint, w), bidi.AUTO);
    if (focused) {
      return with_caret(env, e, v, 0, 0);
    }
    return v;
  }
  if (e.lines > 1) {
    return area_view(env, e, w, focused);
  }
  return line_view(env, e, w, focused);
}

// The row of the first line of the field `e` in the box from y, of height
// h: a field of one line has its text in the middle.
function field_top[M](env: Env, e: El[M], y: i32, h: i32): i32 {
  if (e.lines > 1) {
    return y;
  }
  let (_, _, line) = field_type(env, e);
  return y + (h - line) / 2;
}

// The character of the field `e` that the caret goes before, for a tap at
// tx and ty. The text of the field is in the box x, y, w, h.
function caret_at[M](env: Env, e: El[M], x: i32, y: i32, w: i32, h: i32, tx: i32, ty: i32): i32 {
  if (e.content.len() == 0) {
    return 0;
  }
  let (size, face, line) = field_type(env, e);
  let v = field_view(env, e, w);
  let row = ((ty - field_top(env, e, y, h)) / line).max(0).min(v.lines.len() - 1);
  let cs = text.letters(v.lines[row]);
  let order = v.text.order(v.starts[row], v.ends[row]);
  if (order.len() == 0) {
    return v.starts[row];
  }
  // The x of the tap from the left end of the glyphs, which do not mirror
  // in a layout that is right to left.
  let at = tx - x - v.xs[row];
  if (env.rtl) {
    at = text.width(env.fonts, face, size, v.lines[row]) - at;
  }
  let p = 0;
  while (p < cs.len() && text.width(env.fonts, face, size, text.span(cs, 0, p)) + text.width(env.fonts, face, size, cs[p]) / 2 < at) {
    p += 1;
  }
  // The caret goes at the left edge of the glyph `p`, or at the right edge
  // of the last glyph.
  let k = order[p.min(order.len() - 1)];
  let rtl = v.text.levels[k] % 2 == 1;
  if (rtl == p < order.len()) {
    return k + 1;
  }
  return k;
}

// The text of a field or its hint, the line below it, and the caret of the
// field that has the focus.
function place_field[M](env: Env, e: El[M], x: i32, y: i32, w: i32, h: i32, clip: Clip, own out: Built[M]): Built[M] {
  let focused = e.field == env.focus.field;
  let (size, face, line) = field_type(env, e);
  let v = field_view(env, e, w);
  let color = env.theme.ink;
  if (e.content.len() == 0) {
    color = env.theme.muted;
  }
  let top = field_top(env, e, y, h);
  let ns: scene.Node[] = [];
  for i in 0..v.lines.len() {
    ns = ns.append(scene.node(x + v.xs[i], top + i * line, text.width(env.fonts, face, size, v.lines[i]), line, scene.Label(v.lines[i], size, face, 0, color)));
  }
  let rule = px(env, 1.0).max(1);
  let rule_color = env.theme.outline;
  if (focused) {
    rule = px(env, 2.0).max(1);
    rule_color = env.theme.primary;
    ns = ns.append(scene.node(x + v.caret_x, top + v.caret_line * line, caret_width(env), line, scene.Fill(env.theme.primary, 0)));
  }
  let below = y + h + px(env, e.pad.bottom);
  ns = ns.append(scene.node(x - px(env, e.pad.left), below - rule, w + px(env, e.pad.left) + px(env, e.pad.right), rule, scene.Fill(rule_color, 0)));
  let nodes = out.nodes;
  for n in ns {
    nodes = nodes.append(clipped(n, clip));
  }
  return Built { nodes: nodes, scrollers: out.scrollers, taps: out.taps, fields: out.fields, slides: out.slides, moves: out.moves, swipes: out.swipes };
}

function header_heights[M](env: Env, e: El[M], w: i32, h: i32): (i32, i32, i32) {
  if (e.kids.len() != 3) {
    return (0, 0, 0);
  }
  let (_, before) = measure(env, e.kids[0], w, UNBOUNDED);
  let (_, header) = measure(env, e.kids[1], w, h);
  let (_, after) = measure(env, e.kids[2], w, UNBOUNDED);
  return (before, header, after);
}

function place_list_item[M](env: Env, e: El[M], x: i32, y: i32, w: i32, h: i32, clip: Clip, own out: Built[M]): Built[M] {
  if (h > 0 && y + h > clip.c0 && y < clip.c1) {
    return place(env, e, x, y, w, h, clip, out);
  }
  return out;
}

// The rows of a list that show, each in its box.
function place_rows[M](env: Env, e: El[M], x: i32, y: i32, w: i32, h: i32, clip: Clip, own out: Built[M]): Built[M] {
  let row = px(env, e.row).max(1);
  let (before, header, after) = header_heights(env, e, w, h);
  let limit = (before + header + after + e.count * row - h).max(before);
  let initial = if (e.start_pinned) { before } else { 0 };
  let at = offset_of(env, e.list, limit, h, initial);
  let inner = Clip { ...clip, group: e.list, c0: clip.c0.max(y), c1: clip.c1.min(y + h) };
  let header_y = (y + before - at).max(y);
  let body = if (header > 0) { Clip { ...inner, c0: inner.c0.max(header_y + header) } } else { inner };
  let rows_y = y + before + header + after - at;
  let moves = index_of_some(out.moves.len(), e.moves.is_some());
  let swipes = index_of_some(out.swipes.len(), e.swipes.is_some());
  let list = Scroller { name: e.list, x: x, y: y, w: w, h: h, limit: limit, across: false, parent: clip.group, row: row, count: e.count, moves: moves, swipes: swipes, rows_y: rows_y, rows_clip: (body.c0, body.c1), initial: initial, pinned: header > 0 && at >= before };
  let o = Built { nodes: out.nodes, scrollers: out.scrollers.append(list), taps: out.taps, fields: out.fields, slides: out.slides, moves: with_some(out.moves, e.moves), swipes: with_some(out.swipes, e.swipes) };
  if (e.kids.len() == 3) {
    let zone = clipped(scene.node(x, y, w, h, scene.Blank), inner);
    o = Built { ...o, nodes: o.nodes.append(zone) };
    o = place_list_item(env, e.kids[0], x, y - at, w, before, inner, o);
    o = place_list_item(env, e.kids[1], x, header_y, w, header, inner, o);
    o = place_list_item(env, e.kids[2], x, rows_y - after, w, after, body, o);
  }
  return place_virtual_rows(env, e, list, body, o);
}

function place_virtual_rows[M](env: Env, e: El[M], list: Scroller, clip: Clip, own out: Built[M]): Built[M] {
  let Some(build) = e.build else {
    return out;
  };
  let (first, last) = list.visible_rows();
  let g = env.grab;
  if (g.list == e.list && g.row >= 0 && g.row < e.count) {
    return held_rows(env, build, g, e.count, list.x, list.rows_y, list.w, list.row, (first, last), clip, out);
  }
  let o = out;
  for i in first..last {
    o = place(env, build(i), list.x, list.rows_y + i * list.row, list.w, list.row, clip, o);
  }
  return o;
}

// The index that a function gets at the end of a list of `len`, or -1
// when there is no function.
function index_of_some(len: i32, some: boolean): i32 {
  if (some) {
    return len;
  }
  return -1;
}

function with_some[T](own xs: T[], x: Option[T]): T[] {
  if let Some(v) = x {
    return xs.append(v);
  }
  return xs;
}

// The place of the row `i` while the held row `from` is at the place `to`:
// the rows between the two make room for it.
function slot_of(i: i32, from: i32, to: i32): i32 {
  if (from < i && i <= to) {
    return i - 1;
  }
  if (to <= i && i < from) {
    return i + 1;
  }
  return i;
}

// The rows of a list of `count` rows from `top`, while a finger holds the
// row of `g`. `shown` has the first place that shows and the place after
// the last one. The held row goes with the finger over a surface, and the
// other rows make room for it at its new place. A held row that has not
// moved keeps its place in the order of the nodes, so its node does not
// change.
function held_rows[M](env: Env, build: (i32) => El[M], g: Grab, count: i32, x: i32, top: i32, w: i32, row: i32, shown: (i32, i32), clip: Clip, own out: Built[M]): Built[M] {
  let (first, last) = shown;
  let to = target(g, row, count);
  let still = g.shift == 0;
  let o = out;
  for i in (first - 1).max(0)..(last + 1).min(count) {
    let slot = held_slot(i, g, to);
    if (i == g.row && still) {
      o = with_lift(env, o, clip, x, top + slot * row, w, row);
    }
    if ((i != g.row || still) && slot >= first && slot < last) {
      o = place(env, build(i), x, top + slot * row, w, row, clip, o);
    }
  }
  if (still) {
    return o;
  }
  let (lx, ly) = lifted_at(g, x, top + g.row * row);
  return place(env, build(g.row), lx, ly, w, row, clip, with_lift(env, o, clip, lx, ly, w, row));
}

// The place of the row `i` while the row of `g` is held at the place `to`.
// A row that swipes moves no other row.
function held_slot(i: i32, g: Grab, to: i32): i32 {
  if (g.across) {
    return i;
  }
  return slot_of(i, g.row, to);
}

// Where the held row of `g` is, when its place is at x and y.
function lifted_at(g: Grab, x: i32, y: i32): (i32, i32) {
  if (g.across) {
    return (x + g.shift, y);
  }
  return (x, y + g.shift);
}

// The screen with the surface of a held row in the box x, y, w, h.
function with_lift[M](env: Env, own out: Built[M], clip: Clip, x: i32, y: i32, w: i32, h: i32): Built[M] {
  let r = px(env, 12.0);
  let base = clipped(scene.node(x, y, w, h, scene.Fill(env.theme.surface, r)), clip);
  let tint = clipped(scene.node(x, y, w, h, scene.Fill(env.theme.pressed, r)), clip);
  return Built { nodes: out.nodes.append(base).append(tint), scrollers: out.scrollers, taps: out.taps, fields: out.fields, slides: out.slides, moves: out.moves, swipes: out.swipes };
}

// The elements of a row, each in its box, from the column `left` on. An
// element outside the columns of `clip` gets no nodes.
function place_along[M](env: Env, e: El[M], ss: (i32, i32)[], left: i32, y: i32, h: i32, gap: i32, clip: Clip, own out: Built[M]): Built[M] {
  let o = out;
  let at = left;
  for i in 0..e.kids.len() {
    let (kw, kh) = ss[i];
    if (e.align == STRETCH) {
      kh = h;
    }
    if (at + kw > clip.left && at < clip.right) {
      o = place(env, e.kids[i], at, y + shift(e.align, h - kh), kw, kh, clip, o);
    }
    at += kw + gap;
  }
  return o;
}

// The elements of a row that wraps, each in its box on its line.
function place_wrapped[M](env: Env, e: El[M], x: i32, y: i32, w: i32, h: i32, clip: Clip, own out: Built[M]): Built[M] {
  let gap = px(env, e.gap);
  let (ss, lines) = wrapped_sizes(env, e, w, h);
  let rows = line_sizes(ss, lines, gap);
  let o = out;
  let top = y;
  let at = x;
  for i in 0..e.kids.len() {
    let (kw, kh) = ss[i];
    let (lw, lh) = rows[lines[i]];
    if (i == 0 || lines[i] != lines[i - 1]) {
      if (i > 0) {
        let (_, above) = rows[lines[i - 1]];
        top += above + gap;
      }
      at = x + shift(e.justify, w - lw);
    }
    if (e.align == STRETCH) {
      kh = lh;
    }
    if (top + lh > clip.c0 && top < clip.c1) {
      o = place(env, e.kids[i], at, top + shift(e.align, lh - kh), kw, kh, clip, o);
    }
    at += kw + gap;
  }
  return o;
}

// The elements of a row, each in its box. A row that is a list scrolls
// them across.
function place_row[M](env: Env, e: El[M], x: i32, y: i32, w: i32, h: i32, clip: Clip, own out: Built[M]): Built[M] {
  let scrolls = e.list.len() > 0;
  if (e.wraps && !scrolls) {
    return place_wrapped(env, e, x, y, w, h, clip, out);
  }
  let room = w;
  if (scrolls) {
    room = UNBOUNDED;
  }
  let ss = sizes(env, e, room, h);
  let n = e.kids.len();
  let gap = px(env, e.gap);
  let total = gap * (n - 1).max(0);
  for s in ss {
    let (kw, _) = s;
    total += kw;
  }
  if (scrolls) {
    let limit = (total - w).max(0);
    let inner = Clip { ...clip, group: e.list, left: clip.left.max(x), right: clip.right.min(x + w) };
    return place_along(env, e, ss, x - offset_of(env, e.list, limit, w, 0), y, h, gap, inner, Built { ...out, scrollers: out.scrollers.append(Scroller { name: e.list, x: x, y: y, w: w, h: h, limit: limit, across: true, parent: clip.group, row: 0, count: 0, moves: -1, swipes: -1, rows_y: 0, rows_clip: (0, 0), initial: 0, pinned: false }) });
  }
  if (e.justify == BETWEEN && n > 1) {
    return place_along(env, e, ss, x, y, h, gap + (w - total).max(0) / (n - 1), clip, out);
  }
  return place_along(env, e, ss, x + shift(e.justify, w - total), y, h, gap, clip, out);
}

// The elements of a column, each in its box, from the row `top` down. An
// element outside the rows of `clip` gets no nodes.
function place_down[M](env: Env, e: El[M], ss: (i32, i32)[], x: i32, top: i32, w: i32, gap: i32, clip: Clip, own out: Built[M]): Built[M] {
  let o = out;
  let at = top;
  for i in 0..e.kids.len() {
    let (kw, kh) = ss[i];
    if (e.align == STRETCH) {
      kw = w;
    }
    if (at + kh > clip.c0 && at < clip.c1) {
      o = place(env, e.kids[i], x + shift(e.align, w - kw), at, kw, kh, clip, o);
    }
    at += kh + gap;
  }
  return o;
}

// The elements of a column, each in its box. A column that is a list
// scrolls them.
function place_column[M](env: Env, e: El[M], x: i32, y: i32, w: i32, h: i32, clip: Clip, own out: Built[M]): Built[M] {
  let scrolls = e.list.len() > 0;
  let room = h;
  if (scrolls) {
    room = UNBOUNDED;
  }
  let ss = sizes(env, e, w, room);
  let n = e.kids.len();
  let gap = px(env, e.gap);
  let total = gap * (n - 1).max(0);
  for s in ss {
    let (_, kh) = s;
    total += kh;
  }
  if (scrolls) {
    let limit = (total - h).max(0);
    let inner = Clip { ...clip, group: e.list, c0: clip.c0.max(y), c1: clip.c1.min(y + h) };
    return place_down(env, e, ss, x, y - offset_of(env, e.list, limit, h, 0), w, gap, inner, Built { ...out, scrollers: out.scrollers.append(Scroller { name: e.list, x: x, y: y, w: w, h: h, limit: limit, across: false, parent: clip.group, row: 0, count: 0, moves: -1, swipes: -1, rows_y: 0, rows_clip: (0, 0), initial: 0, pinned: false }) });
  }
  if (e.justify == BETWEEN && n > 1) {
    return place_down(env, e, ss, x, y, w, gap + (h - total).max(0) / (n - 1), clip, out);
  }
  return place_down(env, e, ss, x, y + shift(e.justify, h - total), w, gap, clip, out);
}

// Gives `e` the box x, y, w, h, and adds its nodes to `out`.
function place[M](env: Env, e: El[M], x: i32, y: i32, w: i32, h: i32, clip: Clip, own out: Built[M]): Built[M] {
  let (told, tap) = with_message(out, e.tap);
  let (both, long) = with_message(told, e.long);
  let fields = both.fields;
  if (e.kind == FIELD) {
    tap = scene.Action { name: FIELD_ACTION + e.field, arg: 0 };
    fields = fields.append(FieldMessages { name: e.field, edit: e.edit, submit: e.submit });
  }
  let slides = both.slides;
  if let Some(slide) = e.slide {
    tap = scene.Action { name: SLIDER_ACTION, arg: slides.len() };
    slides = slides.append(slide);
  }
  let nodes = both.nodes;
  for n in own_nodes(env, e, x, y, w, h, clip, tap, long) {
    nodes = nodes.append(n);
  }
  let o = Built { nodes: nodes, scrollers: both.scrollers, taps: both.taps, fields: fields, slides: slides, moves: both.moves, swipes: both.swipes };
  let ix = x + px(env, e.pad.left);
  let iy = y + px(env, e.pad.top);
  let iw = (w - px(env, e.pad.left) - px(env, e.pad.right)).max(0);
  let ih = (h - px(env, e.pad.top) - px(env, e.pad.bottom)).max(0);
  if (e.kind == TEXT) {
    return place_text(env, e, ix, iy, iw, clip, o);
  }
  if (e.kind == LIST) {
    return place_rows(env, e, ix, iy, iw, ih, clip, o);
  }
  if (e.kind == FIELD) {
    return place_field(env, e, ix, iy, iw, ih, clip, o);
  }
  if (e.kind == COLUMN) {
    return place_column(env, e, ix, iy, iw, ih, clip, o);
  }
  if (e.kind == ROW) {
    return place_row(env, e, ix, iy, iw, ih, clip, o);
  }
  for k in e.kids {
    let (kw, kh) = measure(env, k, iw, ih);
    o = place(env, k, ix + shift(e.align, iw - kw), iy + shift(e.justify, ih - kh), kw, kh, clip, o);
  }
  return o;
}

// The nodes, the lists and the messages of the tree `root` on the screen `s`. The tree
// takes the part of the screen that is free, or its exact size. `offsets`
// has how far each list scrolls, by its name. `focus` has the field that
// has the focus, and its caret. `grab` has the row that a finger holds.
pub function layout[M](root: El[M], fonts: text.Fonts, s: Screen, offsets: Map[string, i32], focus: Focus, grab: Grab): Built[M] {
  let env = Env { fonts: fonts, d: s.d, font: s.font, theme: s.theme, offsets: offsets, focus: focus, grab: grab, rtl: s.rtl };
  let w = (s.w - s.left - s.right).max(0);
  let h = (s.h - s.top - s.bottom).max(0);
  if (exact(env, root.w) >= 0) {
    w = exact(env, root.w);
  }
  if (exact(env, root.h) >= 0) {
    h = exact(env, root.h);
  }
  return place(env, root, s.left, s.top, w, h, Clip { group: "", c0: 0, c1: scene.NO_CLIP, left: 0, right: scene.NO_CLIP }, Built { nodes: [], scrollers: [], taps: [], fields: [], slides: [], moves: [], swipes: [] });
}
```

### `gestures`

The times and the distances are those of the `ViewConfiguration` of Android: a finger that moves more than 8 dp drags, and a finger that stays for 400 ms presses long.

A touch that starts on a list and drags scrolls the list. A drag across scrolls the nearest list that scrolls across, and a drag down scrolls the nearest list that scrolls down. So a row that scrolls across can be in a list that scrolls down. When the finger lifts, the list flings at the speed of the finger, as `std/android/motion` works it out.

A finger can pull a list past its end, and the list goes half as far as the finger. When the finger lifts, the list springs back. A fling that reaches the end of its list goes past it by its speed and springs back too.

A long press on a row of a `list` with `on_move` lifts the row. The row goes with the finger, and the other rows make room for it. When the finger is in the top row or the bottom row of the list, the list scrolls under the held row. It scrolls as far as 12 rows in a second. When the finger lifts, the list gives the message of `on_move` with the row and its new place. A finger that lifts before it moves gives the long press message of the node under it.

A drag across a row of a `list` with `on_swipe` moves the row across, and scrolls no list. When the finger lifts two fifths of the width of the list away, the list gives the message of `on_swipe` with the row. Nearer, the row goes back to its place.

```fern
<<gestures>>=
const SLOP_DP: f64 = 8.0;

const LONG_PRESS_NS: i64 = 400000000i64;

// A finger on the screen: where it went down and where it is, the time
// that it went down, and the node that it went down on. `inner` is the
// list under the finger. After the finger passes the slop, `group` is the
// list that it drags, across or down, and `scroll0` is how far that list
// was pulled when the drag started. `row` is the row of `inner` under the
// finger when `inner` moves or swipes its rows, and -1 when it does not.
// `lifts` tells that a long press lifts that row.
pub struct Press { active: boolean, x0: i32, y0: i32, x: i32, y: i32, at: i64, moved: boolean, fired: boolean, target: Option[scene.Node], inner: string, group: string, across: boolean, scroll0: i32, samples: motion.Sample[], row: i32, lifts: boolean }

// The row `row` that a finger holds in the list `list`. The row goes with
// the finger across the list when `across` is true, and down the list when
// it is false. `shift` is how far the row is from its place, in pixels.
// `from` is where the finger was along the list when the row started to
// move, and `scroll0` is how far the list scrolled then. `pull` is from -1
// to 1: how fast the list scrolls under the row, up or down. `at` is the
// time of the last scroll.
pub struct Grab { list: string, row: i32, across: boolean, shift: i32, from: i32, scroll0: i32, pull: f64, at: i64 }

// The finger, the list that flings, the list that springs back to its end,
// how far each list scrolls, and the row that the finger holds.
pub struct Input { press: Press, flung: string, fling: motion.Fling, sprung: string, spring: motion.Spring, offsets: Map[string, i32], grab: Grab }

function no_press(): Press {
  return Press { active: false, x0: 0, y0: 0, x: 0, y: 0, at: 0i64, moved: false, fired: false, target: None, inner: "", group: "", across: false, scroll0: 0, samples: [], row: -1, lifts: false };
}

// No row that a finger holds.
pub function no_grab(): Grab {
  return Grab { list: "", row: -1, across: false, shift: 0, from: 0, scroll0: 0, pull: 0.0, at: 0i64 };
}

// The place that the row of `g` goes to in a list of `count` rows of `row`
// pixels.
function target(g: Grab, row: i32, count: i32): i32 {
  if (g.across) {
    return g.row;
  }
  let by = (g.shift as f64 / (row.max(1) as f64)).round() as i32;
  return (g.row + by).max(0).min(count - 1);
}

function no_spring(): motion.Spring {
  return motion.spring(0.0, 0.0, 0.0, 0i64);
}

pub function no_input(): Input {
  return Input { press: no_press(), flung: "", fling: motion.no_fling(), sprung: "", spring: no_spring(), offsets: map_new(8), grab: no_grab() };
}

// The list `name` of `lay`.
function scroller_of(lay: Layout, name: string): Option[Scroller] {
  for s in lay.scrollers {
    if (s.name == name) {
      return Some(s);
    }
  }
  return None;
}

// How far the list `name` of `lay` can scroll.
function limit_of(lay: Layout, name: string): i32 {
  if let Some(s) = scroller_of(lay, name) {
    return s.limit;
  }
  return 0;
}

// The list that a drag scrolls, across or down: the list `name`, or the
// nearest list around it that scrolls that way, or "".
function list_for(lay: Layout, name: string, across: boolean): string {
  let at = name;
  for turn in 0..32 {
    let Some(s) = scroller_of(lay, at) else {
      return "";
    };
    if (s.across == across) {
      return at;
    }
    at = s.parent;
  }
  return "";
}

// How far the list `name` scrolls.
pub function (inp: Input) offset(name: string): i32 {
  return inp.offsets.get(name).unwrap_or(0);
}

function scrolled(inp: Input, name: string, to: i32): Input {
  return Input { ...inp, offsets: inp.offsets.insert(name, to) };
}

// Where a list is when a finger pulls it to `raw`: past an end, the list
// goes half as far as the finger.
function resisted(raw: i32, limit: i32): i32 {
  if (raw < 0) {
    return raw / 2;
  }
  if (raw > limit) {
    return limit + (raw - limit) / 2;
  }
  return raw;
}

// Where a finger pulled a list that is at `at`: the opposite of resisted.
function pulled(at: i32, limit: i32): i32 {
  if (at < 0) {
    return at * 2;
  }
  if (at > limit) {
    return limit + (at - limit) * 2;
  }
  return at;
}

// The row at `y` of the list `name` of `lay` when the list moves or swipes
// its rows, or -1.
function row_at(lay: Layout, name: string, y: i32): i32 {
  let Some(s) = scroller_of(lay, name) else {
    return -1;
  };
  let at = y - s.rows_y;
  let (top, bottom) = s.rows_clip;
  if (s.row <= 0 || s.moves < 0 && s.swipes < 0 || at < 0 || y < top || y >= bottom) {
    return -1;
  }
  let row = at / s.row;
  if (row >= s.count) {
    return -1;
  }
  return row;
}

// The list `name` of `lay` moves its rows.
function moves_rows(lay: Layout, name: string): boolean {
  if let Some(s) = scroller_of(lay, name) {
    return s.moves >= 0;
  }
  return false;
}

// The list `name` of `lay` swipes its rows.
function swipes_rows(lay: Layout, name: string): boolean {
  if let Some(s) = scroller_of(lay, name) {
    return s.swipes >= 0;
  }
  return false;
}

function down(inp: Input, lay: Layout, x: i32, y: i32, now: i64): Input {
  let inner = scene.group_at(lay.nodes, x, y);
  let row = row_at(lay, inner, y);
  let press = Press { ...no_press(), active: true, x0: x, y0: y, x: x, y: y, at: now, target: scene.hit(lay.nodes, x, y), inner: inner, row: row, lifts: row >= 0 && moves_rows(lay, inner) };
  return Input { ...inp, press: press, flung: "", fling: motion.no_fling(), sprung: "", spring: no_spring(), grab: no_grab() };
}

// The finger of `p` went down on a node that it slides on.
function on_slider(p: Press): boolean {
  if let Some(n) = p.target {
    return n.tap.name == SLIDER_ACTION;
  }
  return false;
}

// The press when the finger passes the slop at x and y: the drag starts
// there, so the list does not jump. The finger drags across when it moved
// farther across than down.
function dragged(inp: Input, lay: Layout, x: i32, y: i32): Press {
  let p = inp.press;
  let across = (x - p.x0).abs() > (y - p.y0).abs();
  let group = list_for(lay, p.inner, across);
  if (on_slider(p)) {
    group = "";
  }
  return Press { ...p, x0: x, y0: y, moved: true, across: across, group: group, scroll0: pulled(inp.offset(group), limit_of(lay, group)), samples: [] };
}

// The place of the finger of `p` along its drag.
function along(p: Press, x: i32, y: i32): i32 {
  if (p.across) {
    return x;
  }
  return y;
}

// The input with the held row where the finger is, and with the speed of
// the list under it: a finger in the top row or the bottom row of the list
// scrolls it. The row stays at its place until the finger moves.
function held(inp: Input, lay: Layout, now: i64): Input {
  let g = inp.grab;
  let p = inp.press;
  if (!p.moved) {
    return inp;
  }
  if (g.across) {
    return Input { ...inp, grab: Grab { ...g, shift: p.x - g.from } };
  }
  let pull = 0.0;
  if let Some(s) = scroller_of(lay, g.list) {
    let edge = s.row.max(1);
    if (p.y < s.y + edge) {
      pull = 0.0 - ((s.y + edge - p.y) as f64 / (edge as f64)).min(1.0);
    }
    if (p.y > s.y + s.h - edge) {
      pull = ((p.y - (s.y + s.h - edge)) as f64 / (edge as f64)).min(1.0);
    }
  }
  let at = g.at;
  if (g.pull == 0.0) {
    at = now;
  }
  return Input { ...inp, grab: Grab { ...g, shift: p.y - g.from + inp.offset(g.list) - g.scroll0, pull: pull, at: at } };
}

// The press of `p` drags across a row of a list that swipes its rows.
function swipes(lay: Layout, p: Press): boolean {
  return p.across && p.row >= 0 && !on_slider(p) && swipes_rows(lay, p.inner);
}

function moved(inp: Input, lay: Layout, x: i32, y: i32, now: i64, d: f64): Input {
  let p = inp.press;
  let slop = (SLOP_DP * d) as i32;
  let within = !p.moved && (x - p.x0).abs() <= slop && (y - p.y0).abs() <= slop;
  if (inp.grab.list.len() > 0) {
    return held(Input { ...inp, press: Press { ...p, x: x, y: y, moved: p.moved || !within } }, lay, now);
  }
  if (within) {
    return Input { ...inp, press: Press { ...p, x: x, y: y } };
  }
  if (!p.moved) {
    p = dragged(inp, lay, x, y);
    if (swipes(lay, p)) {
      let g = Grab { ...no_grab(), list: p.inner, row: p.row, across: true, from: x, at: now };
      return held(Input { ...inp, press: Press { ...p, x: x, y: y, group: "" }, grab: g }, lay, now);
    }
  }
  let at = along(p, x, y);
  let next = Input { ...inp, press: Press { ...p, x: x, y: y, samples: motion.track(p.samples, motion.Sample { t: now, y: at as f64 }) } };
  if (p.group.len() == 0) {
    return next;
  }
  return scrolled(next, p.group, resisted(p.scroll0 + along(p, p.x0, p.y0) - at, limit_of(lay, p.group)));
}

// The input after the finger lifts from a list: a list past its end
// springs back, and a list that the finger moved fast flings.
function released(inp: Input, lay: Layout, p: Press, x: i32, y: i32, now: i64, d: f64): Input {
  let at = inp.offset(p.group);
  let limit = limit_of(lay, p.group);
  if (at < 0 || at > limit) {
    return Input { ...inp, sprung: p.group, spring: motion.spring(at as f64, at.max(0).min(limit) as f64, 0.0, now) };
  }
  let v = motion.velocity(motion.track(p.samples, motion.Sample { t: now, y: along(p, x, y) as f64 }));
  let f = motion.fling(at, 0.0 - v, d, now);
  if (motion.moving(f)) {
    return Input { ...inp, flung: p.group, fling: f };
  }
  return inp;
}

function up(inp: Input, lay: Layout, x: i32, y: i32, now: i64, d: f64): (Input, Option[scene.Node]) {
  let p = inp.press;
  let rest = Input { ...inp, press: no_press(), grab: no_grab() };
  if (inp.grab.list.len() > 0) {
    return (rest, None);
  }
  if (p.moved) {
    if (p.group.len() == 0) {
      return (rest, None);
    }
    return (released(rest, lay, p, x, y, now, d), None);
  }
  if (p.fired) {
    return (rest, None);
  }
  if let Some(n) = p.target {
    if (n.tap.name.len() > 0) {
      return (rest, Some(n));
    }
  }
  return (rest, None);
}

// The input after the touch `action` at x and y at the time `now`, and the
// node that a tap was on. `tap_message` gives the message of that node.
// The actions are those of the Touch event of std/android. `d` is the
// pixels of one dp.
pub function touch(inp: Input, lay: Layout, action: i32, x: i32, y: i32, now: i64, d: f64): (Input, Option[scene.Node]) {
  if (action == 0) {
    return (down(inp, lay, x, y, now), None);
  }
  if (!inp.press.active) {
    return (inp, None);
  }
  if (action == 2) {
    return (moved(inp, lay, x, y, now, d), None);
  }
  if (action == 1) {
    return up(inp, lay, x, y, now, d);
  }
  if (action == 3) {
    return (Input { ...inp, press: no_press(), grab: no_grab() }, None);
  }
  return (inp, None);
}

// The node under the finger that has a long press action, when the finger
// can still press long.
function long_target(inp: Input): Option[scene.Node] {
  let p = inp.press;
  if (!p.active || p.moved || p.fired) {
    return None;
  }
  if let Some(n) = p.target {
    if (n.long.name.len() > 0) {
      return Some(n);
    }
  }
  return None;
}

// A long press of the finger can lift the row under it.
function can_lift(inp: Input): boolean {
  let p = inp.press;
  return p.active && p.lifts && !p.moved && !p.fired && inp.grab.list.len() == 0 && !on_slider(p);
}

// The milliseconds from `now` to the long press of the finger, or -1 when
// no long press can come.
pub function long_wait(inp: Input, now: i64): i32 {
  if (long_target(inp).is_none() && !can_lift(inp)) {
    return -1;
  }
  let left = inp.press.at + LONG_PRESS_NS - now;
  if (left <= 0i64) {
    return 0;
  }
  return ((left + 999999i64) / 1000000i64) as i32;
}

// The input at the time `now`, and the node of a long press when its time
// came. `long_message` gives the message of that node. A long press on a
// row of a list that moves its rows lifts the row, and gives no node.
pub function long_press(inp: Input, now: i64): (Input, Option[scene.Node]) {
  let p = inp.press;
  if (now - p.at < LONG_PRESS_NS) {
    return (inp, None);
  }
  if (can_lift(inp)) {
    let g = Grab { ...no_grab(), list: p.inner, row: p.row, from: p.y, scroll0: inp.offset(p.inner), at: now };
    return (Input { ...inp, press: Press { ...p, fired: true }, grab: g }, None);
  }
  let Some(n) = long_target(inp) else {
    return (inp, None);
  };
  return (Input { ...inp, press: Press { ...p, fired: true } }, Some(n));
}

// The message of `on_swipe` for the row of `g` in the list `s`, when the
// row is two fifths of the width of the list away.
function swiped_message[M](said: Messages[M], s: Scroller, g: Grab): Option[M] {
  if (s.swipes < 0 || s.swipes >= said.swipes.len() || g.shift.abs() * 5 < s.w * 2) {
    return None;
  }
  let swiped = said.swipes[s.swipes];
  return Some(swiped(g.row));
}

// The row that the finger holds, when it lifts: its message of `on_move`
// with its new place, or its message of `on_swipe`. `inp` is the input
// before the finger lifts. A row that the finger did not move gives the
// long press message of the node under the finger.
pub function dropped[M](said: Messages[M], lay: Layout, inp: Input): Option[M] {
  let g = inp.grab;
  let Some(s) = scroller_of(lay, g.list) else {
    return None;
  };
  if (g.across) {
    return swiped_message(said, s, g);
  }
  if (!inp.press.moved) {
    let Some(n) = inp.press.target else {
      return None;
    };
    return long_message(said, lay, n);
  }
  let to = target(g, s.row, s.count);
  if (to == g.row || s.moves < 0 || s.moves >= said.moves.len()) {
    return None;
  }
  let moved = said.moves[s.moves];
  return Some(moved(g.row, to));
}

// A list flings, springs back to its end, or scrolls under a held row.
pub function (inp: Input) flinging(): boolean {
  return motion.moving(inp.fling) || inp.sprung.len() > 0 || inp.grab.pull != 0.0;
}

// The input with the list that flings at its place at the time `now`. A
// fling that passes an end of its list becomes a spring at that end, with
// the speed of the fling.
function flung(inp: Input, lay: Layout, now: i64): Input {
  if (!motion.moving(inp.fling)) {
    return inp;
  }
  let (pos, done) = motion.position(inp.fling, now);
  let limit = limit_of(lay, inp.flung);
  let to = pos.round() as i32;
  let stopped = Input { ...inp, flung: "", fling: motion.no_fling() };
  if (to < 0 || to > limit) {
    let (before, _) = motion.position(inp.fling, now - 1000000i64);
    let end = to.max(0).min(limit);
    return scrolled(Input { ...stopped, sprung: inp.flung, spring: motion.spring(end as f64, end as f64, (pos - before) * 1000.0, now) }, inp.flung, end);
  }
  if (done) {
    return scrolled(stopped, inp.flung, to);
  }
  return scrolled(inp, inp.flung, to);
}

// The input with the list that springs at its place at the time `now`.
function sprung(inp: Input, now: i64): Input {
  if (inp.sprung.len() == 0) {
    return inp;
  }
  let (pos, done) = motion.spring_position(inp.spring, now);
  if (done) {
    return scrolled(Input { ...inp, sprung: "", spring: no_spring() }, inp.sprung, inp.spring.to.round() as i32);
  }
  return scrolled(inp, inp.sprung, pos.round() as i32);
}

// The input with each list that nothing moves between its ends: a list
// that got shorter does not stay past its end.
function settled(inp: Input, lay: Layout): Input {
  let out = inp;
  for s in lay.scrollers {
    let at = out.offset(s.name);
    let held = out.press.active && out.press.group == s.name;
    if ((at < 0 || at > s.limit) && !held && out.sprung != s.name && out.flung != s.name) {
      out = scrolled(out, s.name, at.max(0).min(s.limit));
    }
  }
  return out;
}

// The input at the frame time `now`: the list that flings moves, and the
// list that is past its end springs back.
pub function advance(inp: Input, lay: Layout, now: i64): Input {
  return settled(carried(sprung(flung(inp, lay, now), now), lay, now), lay);
}

// The input with the list under a held row scrolled for the time since its
// last scroll. The list goes as far as 12 rows in a second.
function carried(inp: Input, lay: Layout, now: i64): Input {
  let g = inp.grab;
  if (g.pull == 0.0) {
    return inp;
  }
  let Some(s) = scroller_of(lay, g.list) else {
    return inp;
  };
  let dt = (now - g.at).max(0i64).min(100000000i64);
  let by = (g.pull * ((s.row * 12) as f64) * (dt as f64) / 1000000000.0).round() as i32;
  let to = (inp.offset(g.list) + by).max(0).min(s.limit);
  return held(scrolled(Input { ...inp, grab: Grab { ...g, at: now } }, g.list, to), lay, now);
}

// The corner radius of what `n` shows.
function corner(n: scene.Node): i32 {
  match (n.paint) {
    scene.Fill(_, r) => {
      return r;
    },
    scene.Outline(_, r, _) => {
      return r;
    },
    _ => {
      return 0;
    }
  }
}

// `n` has the box and the actions of `t`.
function same_node(n: scene.Node, t: scene.Node): boolean {
  let boxes = n.x == t.x && n.y == t.y && n.w == t.w && n.h == t.h;
  return boxes && n.tap.name == t.tap.name && n.tap.arg == t.tap.arg && n.long.name == t.long.name && n.long.arg == t.long.arg;
}

// The message of `said` for the action `a` of the node `t`. A node that
// `lay` does not have gives no message: the screen changed below the
// finger.
function message[M](said: Messages[M], lay: Layout, t: scene.Node, a: scene.Action): Option[M] {
  if (a.name != MESSAGE_ACTION || a.arg < 0 || a.arg >= said.taps.len()) {
    return None;
  }
  for n in lay.nodes {
    if (same_node(n, t)) {
      return Some(said.taps[a.arg]);
    }
  }
  return None;
}

// The x of a finger that puts the thumb of the slider node `n` at `part`,
// on a screen of `d` pixels to the dp.
pub function slide_x(n: scene.Node, part: f64, d: f64): i32 {
  let r = (THUMB_DP * d / 2.0) as i32;
  return n.x + r + (part.max(0.0).min(1.0) * ((n.w - 2 * r).max(1) as f64)) as i32;
}

// The message of a finger at `x` on the slider node `n`.
pub function slide_message[M](said: Messages[M], n: scene.Node, x: i32, d: f64): Option[M] {
  if (n.tap.name != SLIDER_ACTION || n.tap.arg < 0 || n.tap.arg >= said.slides.len()) {
    return None;
  }
  let r = (THUMB_DP * d / 2.0) as i32;
  let part = ((x - n.x - r) as f64 / ((n.w - 2 * r).max(1) as f64)).max(0.0).min(1.0);
  let slide = said.slides[n.tap.arg];
  return Some(slide(part));
}

// The message of a tap on the node `n` of `lay`.
pub function tap_message[M](said: Messages[M], lay: Layout, n: scene.Node): Option[M] {
  return message(said, lay, n, n.tap);
}

// The message of a long press on the node `n` of `lay`.
pub function long_message[M](said: Messages[M], lay: Layout, n: scene.Node): Option[M] {
  return message(said, lay, n, n.long);
}

// The x of the layout for the x `x` on the screen `s`, and the x on the
// screen for an x of the layout. The two are the same unless the layout
// is right to left.
pub function along_screen(s: Screen, x: i32): i32 {
  return if (s.rtl) { s.w - 1 - x } else { x };
}

// The nodes `ns` of a layout as the screen `s` shows them. A layout that is
// right to left mirrors each box and each clip of columns. What a node
// draws in its box does not mirror.
pub function on_screen(ns: scene.Node[], s: Screen): scene.Node[] {
  if (!s.rtl) {
    return ns;
  }
  let out: scene.Node[] = [];
  for n in ns {
    let m = scene.Node { ...n, x: s.w - n.x - n.w };
    if (n.left > 0 || n.right < scene.NO_CLIP) {
      m = scene.Node { ...m, left: s.w - n.right.min(s.w), right: s.w - n.left };
    }
    out = out.append(m);
  }
  return out;
}

// The nodes of `lay` with the node under the finger shown pressed, in the
// color `pressed`.
pub function shown(inp: Input, lay: Layout, pressed: i32): scene.Node[] {
  let p = inp.press;
  let Some(t) = p.target else {
    return lay.nodes;
  };
  if (!p.active || p.moved || on_slider(p) || inp.grab.list.len() > 0) {
    return lay.nodes;
  }
  let out: scene.Node[] = [];
  let done = false;
  for n in lay.nodes {
    out = out.append(n);
    if (!done && same_node(n, t)) {
      out = out.append(scene.Node { ...scene.node(n.x, n.y, n.w, n.h, scene.Fill(pressed, corner(n))), group: n.group, clip0: n.clip0, clip1: n.clip1 });
      done = true;
    }
  }
  return out;
}
```

### `programs`

```fern
<<programs>>=
// An app: `view` makes the tree of the screen from the state, and `update`
// gives the next state for a message. `wait` gives the milliseconds to the
// next `tick` message, or -1 for none. While `animating` is true, each
// frame gives the message that `frame` makes from its time in ns. `back`
// gives the message of the back key, or None when the back key closes the
// app. `system` gives the message of an event of std/android that the
// shell does not use itself. `theme` gives the colors of the light theme
// and of the dark theme.
pub struct Program[S, M] {
  view: (S, Screen) => El[M],
  update: (own S, M) => S,
  wait: (S) => i32,
  tick: Option[M],
  animating: (S) => boolean,
  frame: Option[(i64) => M],
  back: (S) => Option[M],
  system: (android.Event) => Option[M],
  visible: Option[(S, Screen, Layout, Input) => Option[M]],
  effects: (S) => S,
  close: (own S) => own S,
  save: (S, kv.Store) => kv.Store,
  restore: (own S, kv.Store) => own S,
  start: (S, M) => own worker.Worker[M][],
  poll: (own worker.Worker[M][]) => (worker.Worker[M][], M[]),
  theme: (S, boolean) => Theme,
  meter: boolean,
}

// A program with no messages of its own, which the back key closes, in the
// colors of `theme`.
pub function program[S, M](view: (S, Screen) => El[M], update: (own S, M) => S): Program[S, M] {
  return Program { view: view, update: update, wait: (s: S): i32 => -1, tick: None, animating: (s: S): boolean => false, frame: None, back: (s: S): Option[M] => None, system: (e: android.Event): Option[M] => None, visible: None, effects: (s: S): S => s, close: (own s: S): own S => s, save: (s: S, k: kv.Store): kv.Store => k, restore: (own s: S, k: kv.Store): own S => s, start: (s: S, m: M): own worker.Worker[M][] => [], poll: (own ws: worker.Worker[M][]): (worker.Worker[M][], M[]) => (ws, []), theme: (s: S, dark: boolean): Theme => theme(dark), meter: false };
}

// After each message, the shell gives the state to `effects`, which does
// what the state asks of the device and gives the state back. `update`
// stays a function with no effect: it puts what it wants done in the
// state, and a test reads that from the state.
pub function (p: Program[S, M]) with_effects(effects: (S) => S): Program[S, M] {
  return Program { ...p, effects: effects };
}

// Takes the final state during the Destroy that ends the loop, and returns
// its closed state before Android frees the context and detaches the loop
// thread from the JVM. A Destroy that the loop runs on after does not
// close the state.
pub function (p: Program[S, M]) with_close(close: (own S) => own S): Program[S, M] {
  return Program { ...p, close: close };
}

// Keeps a part of the state when Android ends the process of the app. At
// each Pause, the shell writes the store that `save` gives for the state
// to the state file of the device. When the app starts, `restored` gives
// the first state and that store to `restore`. The store is empty at the
// first start, and when the file is not valid.
// Runs work beside the loop. After `update` takes a message, the shell
// gives the new state and the message to `start`, which gives the workers
// that the message starts, for example with `worker.spawn`. When a worker
// ends, its result is a message to `update`. A worker gets a copy of what
// its closure captures, so the work cannot change the state.
pub function (p: Program[S, M: worker.Send]) with_jobs(start: (S, M) => own worker.Worker[M][]): Program[S, M] {
  return Program { ...p, start: start, poll: polled_workers[M]() };
}

// Polls each worker: the ones that still run, and the results of the ones
// that ended.
function polled_workers[M: worker.Send](): (own worker.Worker[M][]) => (worker.Worker[M][], M[]) {
  return (own ws: worker.Worker[M][]): (worker.Worker[M][], M[]) => {
    let running: worker.Worker[M][] = [];
    let done: M[] = [];
    for w in ws {
      match (w.poll()) {
        worker.Finished(m) => {
          done = done.append(m);
        },
        worker.Running(again) => {
          running = running.append(again);
        }
      }
    }
    return (running, done);
  };
}

pub function (p: Program[S, M]) with_state(save: (S, kv.Store) => kv.Store, restore: (own S, kv.Store) => own S): Program[S, M] {
  return Program { ...p, save: save, restore: restore };
}

// `update` gets the message `m` each time the milliseconds of `wait` go by.
pub function (p: Program[S, M]) with_tick(wait: (S) => i32, m: M): Program[S, M] {
  return Program { ...p, wait: wait, tick: Some(m) };
}

// While `animating` is true, `update` gets the message of `frame` for each
// frame.
pub function (p: Program[S, M]) with_frames(animating: (S) => boolean, frame: (i64) => M): Program[S, M] {
  return Program { ...p, animating: animating, frame: Some(frame) };
}

// Each frame shows the cost of the last frame over the screen: the
// milliseconds of `view`, of the layout, of the painter and of the
// accessibility tree, then the longest frame and the longest step of the
// last 120 frames.
pub function (p: Program[S, M]) with_meter(): Program[S, M] {
  return Program { ...p, meter: true };
}

pub function (p: Program[S, M]) with_back(back: (S) => Option[M]): Program[S, M] {
  return Program { ...p, back: back };
}

pub function (p: Program[S, M]) with_system(system: (android.Event) => Option[M]): Program[S, M] {
  return Program { ...p, system: system };
}

// After a frame, the program can give a message for a change in the
// rows that show. Return None if the state already has this range.
pub function (p: Program[S, M]) with_visible(visible: (S, Screen, Layout, Input) => Option[M]): Program[S, M] {
  return Program { ...p, visible: Some(visible) };
}

// The program in the colors that `theme` gives for the light system and
// for the dark system.
pub function (p: Program[S, M]) with_theme(theme: (boolean) => Theme): Program[S, M] {
  return Program { ...p, theme: (s: S, dark: boolean): Theme => theme(dark) };
}

// The program in the colors that `theme` gives for its state, so the
// person can choose them in the program.
pub function (p: Program[S, M]) with_theme_of(theme: (S, boolean) => Theme): Program[S, M] {
  return Program { ...p, theme: theme };
}
```

### `shell`

The shell holds the state of the app and what the module keeps for it: the window, the painter, the finger and the lists. `step` gives it one event of `android.run`.

A change asks for a frame, and the frame draws at the next vsync. So many changes between two frames draw one time.

```fern
<<shell>>=
// What the shell does on the device. `stays` tells, during a Destroy
// event, that the loop runs on after it. `size` gives the width and the
// height of a window. `density` gives the pixels of one dp. `font_scale` gives
// the scale of the text of the system. `now` gives the time
// on the monotonic clock in ns. `present` draws the nodes on the window
// over a background color. `show_keyboard` shows the keyboard of the system
// on a text, which has more than one line when the flag is true.
// `set_keyboard` gives it another text, with the cursor before a
// character. `hide_keyboard` takes it away. `fallbacks` gives the font
// files to try for a weight and a character that the fonts do not have.
// `rtl` tells that the layout is right to left.
pub struct Device {
  stays: () => boolean,
  size: (usize) => (i32, i32),
  density: () => f64,
  dark: () => boolean,
  font_scale: () => f64,
  now: () => i64,
  request_frame: () => void,
  finish: () => void,
  attach: (painter.Painter, usize) => painter.Painter,
  detach: (painter.Painter) => painter.Painter,
  close: (painter.Painter) => painter.Painter,
  present: (own painter.Painter, usize, scene.Node[], i32) => painter.Painter,
  publish: (android.AccessItem[]) => void,
  show_keyboard: (string, boolean) => void,
  set_keyboard: (string, i32) => void,
  hide_keyboard: () => void,
  state_file: () => string,
  fallbacks: (i32, i32) => fonts.Found[],
  rtl: () => boolean,
}

// What the shell keeps for the app. `lay` is the layout of the last frame.
// `focus` is the name of the field that has the focus, or "". `typed` is
// the text that the keyboard holds for it, `caret` is the character that
// its caret is before, and `area` tells that it has more than one line.
// `cost` is the cost of the last frame. `ticked` is the time of the last
// tick of the program, in ns.
pub struct Host { dev: Device, fonts: text.Fonts, paint: painter.Painter, window: usize, screen: Screen, input: Input, lay: Layout, frames: i32, focus: string, typed: string, caret: i32, area: boolean, cost: Cost, ticked: i64, backspace_at: i64 }

// The milliseconds of the parts of the last frame: `view`, the layout, the
// painter and the accessibility tree. `frame` is the longest frame and
// `step` the longest step since the frame `since`.
pub struct Cost { view: i32, layout: i32, paint: i32, access: i32, frame: i32, step: i32, since: i32 }

function no_cost(): Cost {
  return Cost { view: 0, layout: 0, paint: 0, access: 0, frame: 0, step: 0, since: 0 };
}

// The milliseconds from the time `a` to the time `b`, in ns.
function ms_between(a: i64, b: i64): i32 {
  return ((b - a) / 1000000i64) as i32;
}

// The nodes of the meter for the cost `c`: one line of text on a dark
// rectangle, below the top inset of the screen.
pub function meter(fonts: text.Fonts, sc: Screen, c: Cost): scene.Node[] {
  let line = "view " + c.view.to_string() + "  layout " + c.layout.to_string() + "  paint " + c.paint.to_string() + "  access " + c.access.to_string() + "  frame " + c.frame.to_string() + "  step " + c.step.to_string() + " ms";
  let size = (11.0 * sc.d) as i32;
  let h = size + size / 2;
  return [scene.node(0, sc.top, sc.w, h, scene.Fill(-872415232, 0)), scene.node(size / 2, sc.top + size / 4, text.width(fonts, 0, size, line), h, scene.Label(line, size, 0, 0, -1))];
}

// `said` has the messages of the layout of the last frame.
pub struct Shell[S, M] { app: S, host: Host, said: Messages[M], jobs: worker.Worker[M][] }

// A shell for the state `state`, which draws text with `fonts`.
pub function shell[S, M](own state: S, fonts: text.Fonts, dev: Device): Shell[S, M] {
  let screen = Screen { w: 0, h: 0, d: dev.density(), dark: dev.dark(), theme: theme(false), left: 0, top: 0, right: 0, bottom: 0, font: dev.font_scale(), rtl: dev.rtl() };
  return Shell { app: state, host: Host { dev: dev, fonts: fonts, paint: painter.new(fonts), window: 0 as usize, screen: screen, input: no_input(), lay: no_layout(), frames: 0, focus: "", typed: "", caret: 0, area: false, cost: no_cost(), ticked: dev.now(), backspace_at: 0i64 }, said: no_messages(), jobs: [] };
}

function changed(h: Host): Host {
  h.dev.request_frame();
  return h;
}

// The host on the window `window`.
function on_window(h: Host, window: usize): Host {
  let (w, hh) = h.dev.size(window);
  let p = h.paint;
  if (window != h.window || !painter.attached(p)) {
    p = h.dev.attach(p, window);
  }
  return changed(Host { ...h, paint: painter.with_size(p, w, hh), window: window, screen: Screen { ...h.screen, w: w, h: hh, d: h.dev.density(), dark: h.dev.dark(), font: h.dev.font_scale(), rtl: h.dev.rtl() } });
}

// The shell after `update` took the message `m`.
pub function send[S, M](own s: Shell[S, M], p: Program[S, M], m: M): Shell[S, M] {
  let next = p.update(s.app, m);
  let started = p.start(next, m);
  return Shell { app: p.effects(next), host: changed(s.host), said: s.said, jobs: added(s.jobs, started) };
}

function added[M](own a: worker.Worker[M][], own b: worker.Worker[M][]): worker.Worker[M][] {
  let out = a;
  for w in b {
    out = out.append(w);
  }
  return out;
}

// The shell after `update` took the result of each job that ended.
function finished_jobs[S, M](own s: Shell[S, M], p: Program[S, M]): Shell[S, M] {
  if (s.jobs.len() == 0) {
    return s;
  }
  let (running, done) = p.poll(s.jobs);
  let t = Shell { app: s.app, host: s.host, said: s.said, jobs: running };
  for m in done {
    t = send(t, p, m);
  }
  return t;
}

// The shell after it waited for each job: `update` gets the result of
// each one.
pub function joined_jobs[S, M: worker.Send](own s: Shell[S, M], p: Program[S, M]): Shell[S, M] {
  let results = joined(s.jobs);
  let t = Shell { app: s.app, host: s.host, said: s.said, jobs: [] };
  for m in results {
    t = send(t, p, m);
  }
  return t;
}

function joined[M: worker.Send](own ws: worker.Worker[M][]): M[] {
  let out: M[] = [];
  for w in ws {
    out = out.append(w.join());
  }
  return out;
}

function told_maybe[S, M](own s: Shell[S, M], p: Program[S, M], m: Option[M]): Shell[S, M] {
  if let Some(msg) = m {
    return send(s, p, msg);
  }
  return s;
}

// The shell after `update` took the frame message of the time `ns`, while
// the program animates.
function animated[S, M](own s: Shell[S, M], p: Program[S, M], ns: i64): Shell[S, M] {
  if (!p.animating(s.app)) {
    return s;
  }
  let Some(frame) = p.frame else {
    return s;
  };
  let m = frame(ns);
  let next = p.update(s.app, m);
  let started = p.start(next, m);
  return Shell { app: p.effects(next), host: s.host, said: s.said, jobs: added(s.jobs, started) };
}

// The host with no field in focus and no keyboard.
function blurred(h: Host): Host {
  if (h.focus.len() > 0) {
    h.dev.hide_keyboard();
    h.dev.request_frame();
  }
  return Host { ...h, focus: "", typed: "", caret: 0, area: false, backspace_at: 0i64 };
}

// The host with the keyboard on the text that the tree `root` has for the
// field in focus, with the caret at its end. A field that the tree no
// longer has loses the focus.
function synced[M](own h: Host, root: El[M]): Host {
  if (h.focus.len() == 0) {
    return h;
  }
  let Some(f) = field_el(root, h.focus) else {
    return blurred(h);
  };
  if (f.content == h.typed) {
    return h;
  }
  let end = text.letters(f.content).len();
  h.dev.set_keyboard(f.content, end);
  return Host { ...h, typed: f.content, caret: end, backspace_at: 0i64 };
}

// The host with its lists where they are at the time `ns`.
function advanced(h: Host, ns: i64): Host {
  return Host { ...h, input: advance(h.input, h.lay, ns) };
}

function initial_scrolls(input: Input, lay: Layout): Input {
  let out = input;
  for list in lay.scrollers {
    if (list.initial > 0 && !out.offsets.contains(list.name)) {
      out = scrolled(out, list.name, list.initial);
    }
  }
  return out;
}

// Draws the layout `lay` on the screen `screen`, with the nodes `over` on
// top of it. `cost` has the costs of the frame before the painter.
function painted(own h: Host, lay: Layout, over: scene.Node[], screen: Screen, animating: boolean, cost: Cost): Host {
  let t0 = h.dev.now();
  let nodes = on_screen(shown(h.input, lay, screen.theme.pressed), screen);
  for n in over {
    nodes = nodes.append(n);
  }
  let paint = h.dev.present(h.paint, h.window, nodes, screen.theme.background);
  let t1 = h.dev.now();
  h.dev.publish(scene.access_items(on_screen(lay.nodes, screen)));
  if (h.input.flinging() || animating) {
    h.dev.request_frame();
  }
  let done = Cost { ...cost, paint: ms_between(t0, t1), access: ms_between(t1, h.dev.now()) };
  let whole = done.view + done.layout + done.paint + done.access;
  return Host { ...h, paint: paint, screen: screen, lay: lay, frames: h.frames + 1, cost: Cost { ...done, frame: done.frame.max(whole) } };
}

// The cost with its longest frame and step from this frame on, when they
// are from 120 frames or more before.
function aged(c: Cost, frames: i32): Cost {
  if (frames - c.since < 120) {
    return c;
  }
  return Cost { ...c, frame: 0, step: 0, since: frames };
}

// `fonts` with the fallback font of each character of the labels of
// `nodes`.
function covered_labels(fonts: text.Fonts, nodes: scene.Node[], find: (i32, i32) => fonts.Found[]): text.Fonts {
  let out = fonts;
  for n in nodes {
    if let scene.Label(s, _, face, _, _) = n.paint {
      out = out.covering(face, s, find);
    }
  }
  return out;
}

// Draws the frame of the time `ns`. A frame that shows a character that
// the fonts did not cover yet loads its fallback font, and lays out again.
function drawn[S, M](own s: Shell[S, M], p: Program[S, M], ns: i64): Shell[S, M] {
  if (s.host.window == 0 as usize || s.host.screen.w == 0) {
    return s;
  }
  let screen = Screen { ...s.host.screen, theme: p.theme(s.app, s.host.screen.dark) };
  let t0 = s.host.dev.now();
  let root = p.view(s.app, screen);
  let t1 = s.host.dev.now();
  let animating = p.animating(s.app);
  let host = advanced(synced(s.host, root), ns);
  let built = layout(root, host.fonts, screen, host.input.offsets, Focus { field: host.focus, caret: host.caret }, host.input.grab);
  let fonts = covered_labels(host.fonts, built.nodes, host.dev.fallbacks);
  if (fonts.looked() != host.fonts.looked()) {
    host = Host { ...host, fonts: fonts, paint: painter.with_fonts(host.paint, fonts) };
    built = layout(root, host.fonts, screen, host.input.offsets, Focus { field: host.focus, caret: host.caret }, host.input.grab);
  }
  let cost = Cost { ...aged(host.cost, host.frames), view: ms_between(t0, t1), layout: ms_between(t1, host.dev.now()) };
  let over: scene.Node[] = [];
  if (p.meter) {
    over = meter(host.fonts, screen, host.cost);
  }
  let lay = built.layout();
  host = Host { ...host, input: initial_scrolls(host.input, lay) };
  let said = built.messages();
  // The keyboard can shrink the viewport until it clips the field that
  // holds the focus. Its edit and submit actions still belong to the form.
  if (host.focus.len() > 0 && field_messages(said, host.focus).is_none()) {
    if let Some(f) = field_el(root, host.focus) {
      said = Messages { ...said, fields: said.fields.append(FieldMessages { name: f.field, edit: f.edit, submit: f.submit }) };
    }
  }
  let msg: Option[M] = None;
  if let Some(visible) = p.visible {
    msg = visible(s.app, screen, lay, host.input);
  }
  return told_maybe(Shell { app: s.app, host: painted(host, lay, over, screen, animating, cost), said: said, jobs: s.jobs }, p, msg);
}

function frame[S, M](own s: Shell[S, M], p: Program[S, M], ns: i64): Shell[S, M] {
  return drawn(animated(s, p, ns), p, ns);
}

// The name of the field of the tap action `name`, or "".
function field_of(name: string): string {
  if (name.starts_with(FIELD_ACTION)) {
    return name.drop(FIELD_ACTION.len()).to_owned();
  }
  return "";
}

// The host with the focus on the field `f` and the caret before the
// character `caret`. A field that gets the focus shows the keyboard on its
// text.
function focused[M](h: Host, f: El[M], caret: i32): Host {
  if (h.focus != f.field) {
    h.dev.show_keyboard(f.content, f.lines > 1);
  }
  if (h.focus == f.field || caret < text.letters(f.content).len()) {
    h.dev.set_keyboard(f.content, caret);
  }
  return changed(Host { ...h, focus: f.field, typed: f.content, caret: caret, area: f.lines > 1, backspace_at: 0i64 });
}

// The host after a finger went down: a finger on something other than the
// field in focus takes the focus away.
function pressed(own h: Host): Host {
  let on = "";
  if let Some(n) = h.input.press.target {
    on = field_of(n.tap.name);
  }
  if (on == h.focus) {
    return h;
  }
  return blurred(h);
}

// The host after a tap at x and y on the node `n` of the field `f`: the
// field has the focus, with its caret where the tap was.
function field_tapped[M](h: Host, f: El[M], n: scene.Node, x: i32, y: i32): Host {
  let sc = h.screen;
  let env = Env { fonts: h.fonts, d: sc.d, font: sc.font, theme: sc.theme, offsets: h.input.offsets, focus: Focus { field: h.focus, caret: h.caret }, grab: h.input.grab, rtl: sc.rtl };
  let left = px(env, f.pad.left);
  let top = px(env, f.pad.top);
  let caret = caret_at(env, f, n.x + left, n.y + top, n.w - left - px(env, f.pad.right), n.h - top - px(env, f.pad.bottom), x, y);
  return focused(h, f, caret);
}

// The shell after a finger at `x` on the slider node `n`. `down` tells
// that the finger went down now.
function slid[S, M](own s: Shell[S, M], p: Program[S, M], input: Input, n: scene.Node, x: i32, down: boolean): Shell[S, M] {
  let m = slide_message(s.said, n, x, s.host.screen.d);
  let host = Host { ...s.host, input: input };
  if (down) {
    host = pressed(Host { ...s.host, input: input });
  }
  return told_maybe(Shell { app: s.app, host: changed(host), said: s.said, jobs: s.jobs }, p, m);
}

// The slider node that the finger is on, while it is down or moves.
// `target` is the node of the press before the touch `action`.
function slider_held(target: Option[scene.Node], input: Input, action: i32): Option[scene.Node] {
  let held = target;
  if (action == 0) {
    held = input.press.target;
  }
  if let Some(n) = held {
    if (n.tap.name == SLIDER_ACTION && action <= 2) {
      return Some(n);
    }
  }
  return None;
}

function touched[S, M](own s: Shell[S, M], p: Program[S, M], action: i32, x: i32, y: i32): Shell[S, M] {
  let h = s.host;
  let before = h.input;
  let target = h.input.press.target;
  let (input, tapped) = touch(h.input, h.lay, action, x, y, h.dev.now(), h.screen.d);
  if let Some(n) = slider_held(target, input, action) {
    return slid(s, p, input, n, x, action == 0);
  }
  let host = Host { ...h, input: input };
  if (action == 0) {
    host = pressed(Host { ...h, input: input });
  }
  if (action == 1 && before.grab.list.len() > 0) {
    let m = dropped(s.said, h.lay, before);
    return told_maybe(Shell { app: s.app, host: changed(host), said: s.said, jobs: s.jobs }, p, m);
  }
  let Some(on) = tapped else {
    return Shell { app: s.app, host: changed(host), said: s.said, jobs: s.jobs };
  };
  let name = field_of(on.tap.name);
  if (name.len() > 0) {
    let found = field_el(p.view(s.app, host.screen), name);
    if let (Some(f), Some(n)) = (found, target) {
      return Shell { app: s.app, host: field_tapped(host, f, n, x, y), said: s.said, jobs: s.jobs };
    }
    return Shell { app: s.app, host: changed(host), said: s.said, jobs: s.jobs };
  }
  let m = tap_message(s.said, h.lay, on);
  return told_maybe(Shell { app: s.app, host: changed(host), said: s.said, jobs: s.jobs }, p, m);
}

// The caret after the keyboard changed the text `old`, with the caret
// before the character `caret`, to `new`: the characters after the caret
// stay after it.
function caret_after(old: string, new: string, caret: i32): i32 {
  let a = text.letters(old);
  let b = text.letters(new);
  let same = 0;
  while (same < a.len() - caret && same < b.len() && a[a.len() - 1 - same] == b[b.len() - 1 - same]) {
    same += 1;
  }
  return b.len() - same;
}

// The messages of the field `name`.
function field_messages[M](said: Messages[M], name: string): Option[FieldMessages[M]] {
  for f in said.fields {
    if (f.name == name) {
      return Some(f);
    }
  }
  return None;
}

// The shell after `update` took the message of the field in focus for its
// new text `text`.
function edit_told[S, M](own s: Shell[S, M], p: Program[S, M], text: string): Shell[S, M] {
  let m: Option[M] = None;
  if let Some(f) = field_messages(s.said, s.host.focus) {
    if let Some(edit) = f.edit {
      m = Some(edit(text));
    }
  }
  return told_maybe(Shell { app: s.app, host: changed(s.host), said: s.said, jobs: s.jobs }, p, m);
}

// The shell after a key of a hardware keyboard changed the text of the
// field in focus to `text`, with the caret before the character `caret`.
// The keyboard of the system gets the same text.
function retyped[S, M](own s: Shell[S, M], p: Program[S, M], text: string, caret: i32): Shell[S, M] {
  let h = s.host;
  h.dev.set_keyboard(text, caret);
  return edit_told(Shell { app: s.app, host: Host { ...h, typed: text, caret: caret }, said: s.said, jobs: s.jobs }, p, text);
}

// The shell after a key of a hardware keyboard typed `typed` at the caret.
function inserted[S, M](own s: Shell[S, M], p: Program[S, M], typed: string): Shell[S, M] {
  let cs = text.letters(s.host.typed);
  let caret = s.host.caret.max(0).min(cs.len());
  let next = text.span(cs, 0, caret) + typed + text.span(cs, caret, cs.len());
  return retyped(s, p, next, caret + 1);
}

// The shell after the delete key took the character before the caret.
function deleted[S, M](own s: Shell[S, M], p: Program[S, M]): Shell[S, M] {
  let cs = text.letters(s.host.typed);
  let caret = s.host.caret.max(0).min(cs.len());
  if (caret == 0) {
    return s;
  }
  let next = text.span(cs, 0, caret - 1) + text.span(cs, caret, cs.len());
  return retyped(s, p, next, caret - 1);
}

// Native repeat events extend this deadline. The shell supplies repeats when
// an input device sends one down event and holds the key until its up event.
function backspace[S, M](own s: Shell[S, M], p: Program[S, M], delay: i64): Shell[S, M] {
  let next = deleted(s, p);
  let h = next.host;
  let at = if (h.caret > 0) { h.dev.now() + delay * 1000000i64 } else { 0i64 };
  return Shell { ...next, host: Host { ...h, backspace_at: at } };
}

function backspace_wait(h: Host, now: i64): i32 {
  if (h.backspace_at == 0i64 || h.focus.len() == 0) {
    return -1;
  }
  return (((h.backspace_at - now).max(0i64) + 999999i64) / 1000000i64) as i32;
}

// The shell with the caret `by` characters to the right.
function stepped[S, M](own s: Shell[S, M], by: i32): Shell[S, M] {
  let h = s.host;
  let caret = (h.caret + by).max(0).min(text.letters(h.typed).len());
  h.dev.set_keyboard(h.typed, caret);
  return Shell { app: s.app, host: changed(Host { ...h, caret: caret }), said: s.said, jobs: s.jobs };
}

// The event is one that changes the text of a field.
function types(e: android.Event): boolean {
  match (e) {
    android.Text(_) => {
      return true;
    },
    android.TextAction(_) => {
      return true;
    },
    android.Char(_) => {
      return true;
    },
    android.Key(_, _) => {
      return true;
    },
    _ => {
      return false;
    }
  }
}

// The shell after the field in focus lost the focus and `update` took its
// submit message.
function submitted[S, M](own s: Shell[S, M], p: Program[S, M]): Shell[S, M] {
  let m: Option[M] = None;
  if let Some(f) = field_messages(s.said, s.host.focus) {
    m = f.submit;
  }
  return told_maybe(Shell { app: s.app, host: blurred(s.host), said: s.said, jobs: s.jobs }, p, m);
}

// The shell after a key went down, while a field has the focus. Key code
// 66 is the enter key: it starts a new line in a field of more than one
// line, and it submits a field of one line as the action key of the
// keyboard does. Key code 67 is the delete key, and the key codes 21 and
// 22 are the left and the right arrow.
function key_edited[S, M](own s: Shell[S, M], p: Program[S, M], code: i32): Shell[S, M] {
  if (code == 66 && s.host.area) {
    return inserted(s, p, "\n");
  }
  if (code == 66) {
    return submitted(s, p);
  }
  if (code == 67) {
    return backspace(s, p, 550i64);
  }
  if (code == 21) {
    return stepped(s, -1);
  }
  if (code == 22) {
    return stepped(s, 1);
  }
  return s;
}

// The shell after the keyboard event `e`, while a field has the focus. The
// back key takes the focus away.
function edited[S, M](own s: Shell[S, M], p: Program[S, M], e: android.Event): Shell[S, M] {
  let h = s.host;
  match (e) {
    android.Text(t) => {
      return edit_told(Shell { app: s.app, host: Host { ...h, typed: t, caret: caret_after(h.typed, t, h.caret), backspace_at: 0i64 }, said: s.said, jobs: s.jobs }, p, t);
    },
    android.TextAction(_) => {
      return submitted(s, p);
    },
    android.Char(c) => {
      if (c < 32 || c == 127) {
        return s;
      }
      return inserted(s, p, (c as char).to_string());
    },
    android.Key(code, action) => {
      if (code == 4 && action == 1) {
        return Shell { app: s.app, host: blurred(h), said: s.said, jobs: s.jobs };
      }
      if (action == 0) {
        return key_edited(s, p, code);
      }
      if (code == 67 && action == 1) {
        return Shell { ...s, host: Host { ...h, backspace_at: 0i64 } };
      }
      return s;
    },
    _ => {
      return s;
    }
  }
}

function ticked[S, M](own s: Shell[S, M], p: Program[S, M]): Shell[S, M] {
  let h = s.host;
  let now = h.dev.now();
  if (backspace_wait(h, now) == 0) {
    return backspace(s, p, 55i64);
  }
  let long = long_wait(h.input, now) == 0;
  if (!long && tick_left(h, p.wait(s.app), now) != 0) {
    return s;
  }
  if (long) {
    let (input, pressed) = long_press(h.input, now);
    let m: Option[M] = None;
    if let Some(n) = pressed {
      m = long_message(s.said, h.lay, n);
    }
    return told_maybe(Shell { app: s.app, host: changed(Host { ...h, input: input }), said: s.said, jobs: s.jobs }, p, m);
  }
  return told_maybe(Shell { app: s.app, host: Host { ...h, ticked: now }, said: s.said, jobs: s.jobs }, p, p.tick);
}

function accessed[S, M](own s: Shell[S, M], p: Program[S, M], id: i32, action: i32): Shell[S, M] {
  let Some(n) = scene.access_node(s.host.lay.nodes, id) else {
    return s;
  };
  let m: Option[M] = None;
  if (action == android.ACCESS_LONG_CLICK) {
    m = long_message(s.said, s.host.lay, n);
  }
  if (action == android.ACCESS_CLICK) {
    m = tap_message(s.said, s.host.lay, n);
  }
  return told_maybe(s, p, m);
}

// The shell after a key: the back key closes the app, or tells the
// program when it handles the key. Each other key goes to the program.
function keyed[S, M](own s: Shell[S, M], p: Program[S, M], e: android.Event, code: i32, action: i32): Shell[S, M] {
  if (code != 4) {
    let m = p.system(e);
    return told_maybe(s, p, m);
  }
  if (action != 1) {
    return s;
  }
  if let Some(m) = p.back(s.app) {
    return send(s, p, m);
  }
  s.host.dev.finish();
  return s;
}

// The shell after an event of the window or of the activity.
function arranged[S, M](own s: Shell[S, M], p: Program[S, M], e: android.Event): Shell[S, M] {
  let h = s.host;
  match (e) {
    android.WindowCreated(w) => {
      return Shell { app: s.app, host: on_window(h, w.ptr), said: s.said, jobs: s.jobs };
    },
    android.WindowResized(w) => {
      return Shell { app: s.app, host: on_window(h, w.ptr), said: s.said, jobs: s.jobs };
    },
    android.WindowRedraw(w) => {
      return Shell { app: s.app, host: on_window(h, w.ptr), said: s.said, jobs: s.jobs };
    },
    android.WindowDestroyed(_) => {
      return Shell { app: s.app, host: Host { ...h, paint: h.dev.detach(h.paint), window: 0 as usize }, said: s.said, jobs: s.jobs };
    },
    android.Insets(left, top, right, bottom) => {
      return Shell { app: s.app, host: changed(Host { ...h, screen: Screen { ...h.screen, left: left, top: top, right: right, bottom: bottom } }), said: s.said, jobs: s.jobs };
    },
    android.Pause => {
      let released = Shell { ...s, host: Host { ...h, backspace_at: 0i64 } };
      let t = told_maybe(released, p, p.system(e));
      kept(t.app, p, t.host.dev);
      return t;
    },
    android.Focus(false) => {
      return told_maybe(Shell { ...s, host: Host { ...h, backspace_at: 0i64 } }, p, p.system(e));
    },
    android.Destroy => {
      let t = told_maybe(s, p, p.system(e));
      let paint = t.host.dev.close(t.host.paint);
      if (t.host.dev.stays()) {
        return Shell { app: t.app, host: Host { ...t.host, paint: paint, window: 0 as usize }, said: t.said, jobs: t.jobs };
      }
      return Shell { app: p.close(t.app), host: Host { ...t.host, paint: paint, window: 0 as usize }, said: t.said, jobs: t.jobs };
    },
    _ => {
      return told_maybe(s, p, p.system(e));
    }
  }
}

// `state` with what `p` restores from the state file of `dev`.
pub function restored[S, M](own state: S, p: Program[S, M], dev: Device): own S {
  let path = dev.state_file();
  if (path.len() == 0) {
    return state;
  }
  return match (kv.open(path)) {
    Ok(store) => p.restore(state, store),
    Err(_) => p.restore(state, kv.empty(path)),
  };
}

// Writes the store that `p` saves for `state` to the state file of `dev`,
// when it differs from the file.
function kept[S, M](state: S, p: Program[S, M], dev: Device): void {
  let path = dev.state_file();
  if (path.len() == 0) {
    return;
  }
  let store = p.save(state, kv.empty(path));
  let old = read_file(path).unwrap_or(kv.empty(path).encode());
  if (old != store.encode()) {
    let saved = store.save();
  }
}

// The shell after the event `e` of android.run.
pub function step[S, M](own s: Shell[S, M], p: Program[S, M], e: android.Event): Shell[S, M] {
  let t0 = s.host.dev.now();
  let out = handled(finished_jobs(s, p), p, e);
  let c = out.host.cost;
  let ms = ms_between(t0, out.host.dev.now());
  if (ms <= c.step) {
    return out;
  }
  return Shell { app: out.app, host: Host { ...out.host, cost: Cost { ...c, step: ms } }, said: out.said, jobs: out.jobs };
}

function handled[S, M](own s: Shell[S, M], p: Program[S, M], e: android.Event): Shell[S, M] {
  if (s.host.focus.len() > 0 && types(e)) {
    return edited(s, p, e);
  }
  match (e) {
    android.Vsync(ns) => {
      return frame(s, p, ns);
    },
    android.Touch(action, x, y) => {
      let tx = along_screen(s.host.screen, x as i32);
      return touched(s, p, action, tx, y as i32);
    },
    android.Key(code, action) => {
      return keyed(s, p, e, code, action);
    },
    android.Tick => {
      return ticked(s, p);
    },
    android.Access(id, action) => {
      return accessed(s, p, id, action);
    },
    _ => {
      return arranged(s, p, e);
    }
  }
}

// The milliseconds to the next tick of a program that has a tick each
// `wait` ms, at the time `now`. A wait below 0 is no tick. The time counts
// from the last tick, so events between two ticks do not move the next
// one.
function tick_left(h: Host, wait: i32, now: i64): i32 {
  if (wait < 0) {
    return wait;
  }
  let gone = (now - h.ticked) / 1000000i64;
  if (gone >= wait as i64) {
    return 0;
  }
  return wait - gone as i32;
}

// The milliseconds that android.run waits for the next event before a
// Tick, or -1 for no limit.
// The milliseconds between two polls of the jobs while one runs.
const JOB_POLL_MS: i32 = 16;

pub function wait[S, M](s: Shell[S, M], p: Program[S, M]): i32 {
  let now = s.host.dev.now();
  let long = long_wait(s.host.input, now);
  let own_wait = tick_left(s.host, p.wait(s.app), now);
  let repeat = backspace_wait(s.host, now);
  if (repeat >= 0 && (own_wait < 0 || repeat < own_wait)) {
    own_wait = repeat;
  }
  if (s.jobs.len() > 0 && (own_wait < 0 || own_wait > JOB_POLL_MS)) {
    own_wait = JOB_POLL_MS;
  }
  if (long < 0) {
    return own_wait;
  }
  if (own_wait < 0) {
    return long;
  }
  return long.min(own_wait);
}
```
