# Android user interfaces

This module makes the screens of a NativeActivity app from a tree of elements. The app gives a state, a function that makes the tree from the state, and a function that changes the state for each message.

## Model

An app is a `Program`: a `view` and an `update`. `view` takes the state and the `Screen`, and gives an `El`: the tree of what the screen shows. `update` takes the state and a message, and gives the next state. The app does not draw, and it does not find what a touch is on.

The app declares the type of its messages, usually an enum with one variant for each thing that can occur. `El[M]` is a tree whose elements give messages of the type `M`. The compiler finds `M` from the result type of `view`, so the calls that make the elements do not write it. A `match` in `update` must handle each variant, so the compiler finds a message that `update` does not handle.

The executable example below is such an app.

`std/android/ui/app` runs a program on a device. `shell`, `step` and `wait` run it on a `Device`, which is a set of functions. A test on the host gives a `Device` of its own, so it runs the whole app with no phone.

### Elements

`column` puts its elements one below the other, `row` puts them side by side, and `stack` puts them one over the other. `text` shows a text, which wraps at the width of its box. `picture` shows an image, and `canvas` shows what a function of the app draws. `box` is an empty rectangle, and `space` takes the room that is left.

Each element has a width and a height: `Hug` takes the size of what the element holds, `Grow` takes a share of the room that is left, and `Exact` is a size in dp. The methods of `El` set them, and they set the padding, the gap, the background, the corners and the border. Sizes are in dp, and text sizes are in sp: a text grows with the font size that the person set for the system. `align` sets where a column or a row puts its elements across it, and `justify` sets where it puts them along it.

`on_tap` gives an element a message. A tap on the element gives that message to `update`. A message can hold values: the row of a list, for example. `on_long` does the same for a long press. `slider` is a bar with a thumb, and `on_slide` gives each element the message of a finger that moves across it.

`field` is a text field of one line, and `area` is a text field of more than one line. A tap gives a field the focus and shows the keyboard of the system, and a tap on its text puts the caret there. The program keeps the text. `on_edit` gives the field a function that makes a message from the new text, and each change gives that message to `update`. `view` gives the field the text that it shows. `on_submit` gives the field the message of the action key of the keyboard.

`scroll` makes a column a list that scrolls down, and a row a list that scrolls across, with the fling of an Android list and a spring at each end. `list` is a list that makes only the rows that show, so a list of many rows costs as much as a short one. Each list has a name, which keeps its position from frame to frame.

A color is a pixel word of `std/android/draw`, or a role: `PRIMARY`, `ON_PRIMARY`, `INK`, `MUTED`, `SURFACE`, `OUTLINE`, `BACKGROUND` and `TRACK`. A role takes its color from the `Theme`, which is light or dark as the system is. `button`, `outlined_button`, `card`, `toggle`, `progress` and `divider` are elements in the look of Material 3.

### Messages

An element gives the messages of `on_tap`, `on_long`, `on_edit` and `on_submit`. A program gives the others. `with_tick` gives a message each time the time of its `wait` goes by. `with_frames` gives a message for each frame while its `animating` is true, with the time of the frame. `with_back` gives a message for the back key, or lets the back key close the app. `with_system` gives a message for an event of `std/android` that this module does not use itself.

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
  let screen = ui.Screen { w: 400, h: 800, d: 2.0, dark: false, theme: ui.theme(false), left: 0, top: 0, right: 0, bottom: 0, font: 1.0 };
  let lay = ui.layout(view(Model { count: 0 }, screen), text.Fonts { faces: [], weights: [] }, screen, map_new(4), ui.no_focus());
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
import "std/android/motion";
import "std/android/text";
import "std/android/ui/painter";
import "std/android/ui/scene";
import "std/f64";
import "std/option";
```

### `theme`

The colors are the baseline colors of Material 3. A role is a pixel word with no alpha, which no shape can show, so a role and a color go in the same field.

```fern
<<theme>>=
// `track` is behind the bar of `progress`, and `pressed` is over the
// element under a finger.
pub struct Theme { background: i32, surface: i32, primary: i32, on_primary: i32, ink: i32, muted: i32, outline: i32, track: i32, pressed: i32 }

// The colors of the light theme, or of the dark theme.
pub function theme(dark: boolean): Theme {
  if (dark) {
    return Theme { background: draw.rgb(20, 18, 24), surface: draw.rgb(33, 31, 38), primary: draw.rgb(208, 188, 255), on_primary: draw.rgb(56, 30, 114), ink: draw.rgb(230, 224, 233), muted: draw.rgb(202, 196, 208), outline: draw.rgb(147, 143, 153), track: draw.rgba(147, 143, 153, 77), pressed: draw.rgba(230, 224, 233, 31) };
  }
  return Theme { background: draw.rgb(254, 247, 255), surface: draw.rgb(243, 237, 247), primary: draw.rgb(103, 80, 164), on_primary: draw.rgb(255, 255, 255), ink: draw.rgb(29, 27, 32), muted: draw.rgb(73, 69, 79), outline: draw.rgb(121, 116, 126), track: draw.rgba(121, 116, 126, 77), pressed: draw.rgba(29, 27, 32, 31) };
}

pub const PRIMARY: i32 = 1;

pub const ON_PRIMARY: i32 = 2;

pub const INK: i32 = 3;

pub const MUTED: i32 = 4;

pub const SURFACE: i32 = 5;

pub const OUTLINE: i32 = 6;

pub const BACKGROUND: i32 = 7;

pub const TRACK: i32 = 8;

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
  return c;
}

// The window of the app: its size in pixels, the pixels of one dp, the
// theme, and the parts at its edges that the bars of the system cover.
// `font` is the scale of the text of the system: a text of 1 sp is `font`
// dp high.
pub struct Screen { w: i32, h: i32, d: f64, dark: boolean, theme: Theme, left: i32, top: i32, right: i32, bottom: i32, font: f64 }

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
  // A list of `count` rows of `row` dp, which `build` makes.
  count: i32,
  row: f64,
  build: Option[(i32) => El[M]],
  // A text field: its name and the text that it shows while it is empty.
  // `content` is its text.
  field: string,
  hint: string,
  // A text field: the message of a change of its text, and the message of
  // the action key of the keyboard.
  edit: Option[(string) => M],
  submit: Option[M],
  // The message of a finger that is down on the element or moves on it,
  // from the part of the width that the finger is at.
  slide: Option[(f64) => M],
}

function element[M](kind: i32, kids: El[M][]): El[M] {
  return El { kind: kind, kids: kids, w: Hug, h: Hug, pad: Edges { left: 0.0, top: 0.0, right: 0.0, bottom: 0.0 }, gap: 0.0, align: START, justify: START, bg: 0, radius: 0.0, edge: 0, edge_width: 0.0, tap: None, long: None, barrier: false, say: "", list: "", content: "", size: 14.0, weight: 400, ink: INK, lines: 0, key: "", image: None, paint: None, count: 0, row: 0.0, build: None, field: "", hint: "", edit: None, submit: None, slide: None };
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

// A list with the name `name` that scrolls: `count` rows of `row` dp each.
// `build` makes the row of an index, and the list makes only the rows that
// show.
pub function list[M](name: string, count: i32, row: f64, build: (i32) => El[M]): El[M] {
  return El { ...element(LIST, []), list: name, count: count, row: row, build: Some(build), w: Grow(1.0), h: Grow(1.0) };
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
```

### `widgets`

The sizes are those of Material 3: a button is 40 dp high with corners of 20 dp, and a switch is 52 dp by 32 dp.

```fern
<<widgets>>=
function button_row[M](label: string, color: i32): El[M] {
  return row([text(label).weighted(500).colored(color)]).centered().padding_xy(24.0, 0.0).height(40.0).rounded(20.0);
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

struct Env { fonts: text.Fonts, d: f64, font: f64, theme: Theme, offsets: Map[string, i32], focus: Focus }

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

// The lines of a text, the pixels of its em, its face and the height of a
// line.
struct Lines { lines: string[], px: i32, face: i32, line: i32 }

function text_lines[M](env: Env, e: El[M], room: i32): Lines {
  let size = sp(env, e.size);
  let face = text.face_for(env.fonts, e.weight);
  let lines = text.wrap(env.fonts, face, size, e.content, room);
  if (e.lines > 0 && lines.len() > e.lines) {
    let rest = lines.drop(e.lines - 1).join(" ");
    lines = lines.take(e.lines - 1).append(text.fit(env.fonts, face, size, rest, room));
  }
  return Lines { lines: lines, px: size, face: face, line: text.line(env.fonts, face, size) };
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
    if (across) {
      let (_, h) = measure(env, k, share, ih);
      sized = sized.with(i, (share, h));
    } else {
      let (w, _) = measure(env, k, iw, share);
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

// The size of the elements of the column or the row `e`, with their gaps.
function flow_size[M](env: Env, e: El[M], iw: i32, ih: i32): (i32, i32) {
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
    return (0, e.count * px(env, e.row));
  }
  if (e.kind == FIELD) {
    return (0, e.lines.max(1) * text.line(env.fonts, text.face_for(env.fonts, e.weight), sp(env, e.size)));
  }
  if (e.kind == COLUMN || e.kind == ROW) {
    return flow_size(env, e, iw, ih);
  }
  return stack_size(env, e, iw, ih);
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
// `parent` is the name of the list that it is in, or "".
pub struct Scroller { name: string, x: i32, y: i32, w: i32, h: i32, limit: i32, across: boolean, parent: string }

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
pub struct Messages[M] { taps: M[], fields: FieldMessages[M][], slides: ((f64) => M)[] }

// The name of the tap action of a node that a finger slides on.
pub const SLIDER_ACTION: string = "slider";

pub function no_messages[M](): Messages[M] {
  return Messages { taps: [], fields: [], slides: [] };
}

// The name of the action of a node that gives a message. An accessibility
// service reads the name for a node that has no text and no description.
pub const MESSAGE_ACTION: string = "button";

// A screen: its nodes, its lists and its messages.
pub struct Built[M] { nodes: scene.Node[], scrollers: Scroller[], taps: M[], fields: FieldMessages[M][], slides: ((f64) => M)[] }

// The nodes and the lists of the screen.
pub function (b: Built[M]) layout(): Layout {
  return Layout { nodes: b.nodes, scrollers: b.scrollers };
}

// The messages of the screen.
pub function (b: Built[M]) messages(): Messages[M] {
  return Messages { taps: b.taps, fields: b.fields, slides: b.slides };
}

// The screen with the message `m`, and the action that gives it. No
// message gives no action.
function with_message[M](own out: Built[M], m: Option[M]): (Built[M], scene.Action) {
  if let Some(message) = m {
    let at = out.taps.len();
    return (Built { nodes: out.nodes, scrollers: out.scrollers, taps: out.taps.append(message), fields: out.fields, slides: out.slides }, scene.Action { name: MESSAGE_ACTION, arg: at });
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
function offset_of(env: Env, name: string, limit: i32, room: i32): i32 {
  return env.offsets.get(name).unwrap_or(0).max(-room).min(limit + room);
}

function place_text[M](env: Env, e: El[M], x: i32, y: i32, w: i32, clip: Clip, own out: Built[M]): Built[M] {
  let t = text_lines(env, e, w);
  let nodes = out.nodes;
  let color = env.theme.color(e.ink);
  for i in 0..t.lines.len() {
    let l = t.lines[i];
    let lw = text.width(env.fonts, t.face, t.px, l);
    let n = scene.node(x + shift(e.align, w - lw), y + i * t.line, lw, t.line, scene.Label(l, t.px, t.face, 0, color));
    nodes = nodes.append(clipped(n, clip));
  }
  return Built { nodes: nodes, scrollers: out.scrollers, taps: out.taps, fields: out.fields, slides: out.slides };
}

// What a field shows: its lines, the index of the first character of each
// line, and the line and the x of the caret. The line of the caret is -1
// for a field that does not have the focus.
struct FieldView { lines: string[], starts: i32[], caret_line: i32, caret_x: i32 }

// The pixels of the em, the face and the height of a line of the field.
function field_type[M](env: Env, e: El[M]): (i32, i32, i32) {
  let size = sp(env, e.size);
  let face = text.face_for(env.fonts, e.weight);
  return (size, face, text.line(env.fonts, face, size));
}

// A field of one line in `w` pixels. In focus it shows the part of its
// text around the caret. Out of focus it shows the end of its text.
function line_view[M](env: Env, e: El[M], w: i32, focused: boolean): FieldView {
  let (size, face, _) = field_type(env, e);
  if (!focused) {
    return FieldView { lines: [text.fit_end(env.fonts, face, size, e.content, w)], starts: [0], caret_line: -1, caret_x: 0 };
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
  return FieldView { lines: [text.span(cs, from, to)], starts: [from], caret_line: 0, caret_x: text.width(env.fonts, face, size, text.span(cs, from, caret)) };
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
  let lines: string[] = [];
  let starts: i32[] = [];
  for i in first..(first + e.lines).min(ranges.len()) {
    let (a, b) = ranges[i];
    lines = lines.append(text.span(cs, a, b));
    starts = starts.append(a);
  }
  if (!focused) {
    return FieldView { lines: lines, starts: starts, caret_line: -1, caret_x: 0 };
  }
  let (a, _) = ranges[at];
  return FieldView { lines: lines, starts: starts, caret_line: at - first, caret_x: text.width(env.fonts, face, size, text.span(cs, a, caret)) };
}

// What the field `e` shows in `w` pixels. An empty field shows its hint.
function field_view[M](env: Env, e: El[M], w: i32): FieldView {
  let focused = e.field == env.focus.field;
  if (e.content.len() == 0) {
    let (size, face, _) = field_type(env, e);
    let caret_line = -1;
    if (focused) {
      caret_line = 0;
    }
    return FieldView { lines: [text.fit(env.fonts, face, size, e.hint, w)], starts: [0], caret_line: caret_line, caret_x: 0 };
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
  let k = 0;
  while (k < cs.len() && text.width(env.fonts, face, size, text.span(cs, 0, k)) + text.width(env.fonts, face, size, cs[k]) / 2 < tx - x) {
    k += 1;
  }
  return v.starts[row] + k;
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
    ns = ns.append(scene.node(x, top + i * line, text.width(env.fonts, face, size, v.lines[i]), line, scene.Label(v.lines[i], size, face, 0, color)));
  }
  let rule = px(env, 1.0).max(1);
  let rule_color = env.theme.outline;
  if (focused) {
    rule = px(env, 2.0).max(1);
    rule_color = env.theme.primary;
    ns = ns.append(scene.node(x + v.caret_x + px(env, 1.0), top + v.caret_line * line, px(env, 2.0).max(1), line, scene.Fill(env.theme.primary, 0)));
  }
  let below = y + h + px(env, e.pad.bottom);
  ns = ns.append(scene.node(x - px(env, e.pad.left), below - rule, w + px(env, e.pad.left) + px(env, e.pad.right), rule, scene.Fill(rule_color, 0)));
  let nodes = out.nodes;
  for n in ns {
    nodes = nodes.append(clipped(n, clip));
  }
  return Built { nodes: nodes, scrollers: out.scrollers, taps: out.taps, fields: out.fields, slides: out.slides };
}

// The rows of a list that show, each in its box.
function place_rows[M](env: Env, e: El[M], x: i32, y: i32, w: i32, h: i32, clip: Clip, own out: Built[M]): Built[M] {
  let row = px(env, e.row).max(1);
  let limit = (e.count * row - h).max(0);
  let at = offset_of(env, e.list, limit, h);
  let inner = Clip { ...clip, group: e.list, c0: clip.c0.max(y), c1: clip.c1.min(y + h) };
  let o = Built { nodes: out.nodes, scrollers: out.scrollers.append(Scroller { name: e.list, x: x, y: y, w: w, h: h, limit: limit, across: false, parent: clip.group }), taps: out.taps, fields: out.fields, slides: out.slides };
  let Some(build) = e.build else {
    return o;
  };
  let first = (at / row).max(0).min(e.count);
  let last = ((at + h + row - 1) / row).max(first).min(e.count);
  for i in first..last {
    o = place(env, build(i), x, y + i * row - at, w, row, inner, o);
  }
  return o;
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

// The elements of a row, each in its box. A row that is a list scrolls
// them across.
function place_row[M](env: Env, e: El[M], x: i32, y: i32, w: i32, h: i32, clip: Clip, own out: Built[M]): Built[M] {
  let scrolls = e.list.len() > 0;
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
    let o = Built { nodes: out.nodes, scrollers: out.scrollers.append(Scroller { name: e.list, x: x, y: y, w: w, h: h, limit: limit, across: true, parent: clip.group }), taps: out.taps, fields: out.fields, slides: out.slides };
    let inner = Clip { ...clip, group: e.list, left: clip.left.max(x), right: clip.right.min(x + w) };
    return place_along(env, e, ss, x - offset_of(env, e.list, limit, w), y, h, gap, inner, o);
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
    let o = Built { nodes: out.nodes, scrollers: out.scrollers.append(Scroller { name: e.list, x: x, y: y, w: w, h: h, limit: limit, across: false, parent: clip.group }), taps: out.taps, fields: out.fields, slides: out.slides };
    let inner = Clip { ...clip, group: e.list, c0: clip.c0.max(y), c1: clip.c1.min(y + h) };
    return place_down(env, e, ss, x, y - offset_of(env, e.list, limit, h), w, gap, inner, o);
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
  let o = Built { nodes: nodes, scrollers: both.scrollers, taps: both.taps, fields: fields, slides: slides };
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
// has the focus, and its caret.
pub function layout[M](root: El[M], fonts: text.Fonts, s: Screen, offsets: Map[string, i32], focus: Focus): Built[M] {
  let env = Env { fonts: fonts, d: s.d, font: s.font, theme: s.theme, offsets: offsets, focus: focus };
  let w = (s.w - s.left - s.right).max(0);
  let h = (s.h - s.top - s.bottom).max(0);
  if (exact(env, root.w) >= 0) {
    w = exact(env, root.w);
  }
  if (exact(env, root.h) >= 0) {
    h = exact(env, root.h);
  }
  return place(env, root, s.left, s.top, w, h, Clip { group: "", c0: 0, c1: scene.NO_CLIP, left: 0, right: scene.NO_CLIP }, Built { nodes: [], scrollers: [], taps: [], fields: [], slides: [] });
}
```

### `gestures`

The times and the distances are those of the `ViewConfiguration` of Android: a finger that moves more than 8 dp drags, and a finger that stays for 400 ms presses long.

A touch that starts on a list and drags scrolls the list. A drag across scrolls the nearest list that scrolls across, and a drag down scrolls the nearest list that scrolls down. So a row that scrolls across can be in a list that scrolls down. When the finger lifts, the list flings at the speed of the finger, as `std/android/motion` works it out.

A finger can pull a list past its end, and the list goes half as far as the finger. When the finger lifts, the list springs back. A fling that reaches the end of its list goes past it by its speed and springs back too.

```fern
<<gestures>>=
const SLOP_DP: f64 = 8.0;

const LONG_PRESS_NS: i64 = 400000000i64;

// A finger on the screen: where it went down and where it is, the time
// that it went down, and the node that it went down on. `inner` is the
// list under the finger. After the finger passes the slop, `group` is the
// list that it drags, across or down, and `scroll0` is how far that list
// was pulled when the drag started.
pub struct Press { active: boolean, x0: i32, y0: i32, x: i32, y: i32, at: i64, moved: boolean, fired: boolean, target: Option[scene.Node], inner: string, group: string, across: boolean, scroll0: i32, samples: motion.Sample[] }

// The finger, the list that flings, the list that springs back to its end,
// and how far each list scrolls.
pub struct Input { press: Press, flung: string, fling: motion.Fling, sprung: string, spring: motion.Spring, offsets: Map[string, i32] }

function no_press(): Press {
  return Press { active: false, x0: 0, y0: 0, x: 0, y: 0, at: 0i64, moved: false, fired: false, target: None, inner: "", group: "", across: false, scroll0: 0, samples: [] };
}

function no_spring(): motion.Spring {
  return motion.spring(0.0, 0.0, 0.0, 0i64);
}

pub function no_input(): Input {
  return Input { press: no_press(), flung: "", fling: motion.no_fling(), sprung: "", spring: no_spring(), offsets: map_new(8) };
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

function down(inp: Input, lay: Layout, x: i32, y: i32, now: i64): Input {
  let press = Press { ...no_press(), active: true, x0: x, y0: y, x: x, y: y, at: now, target: scene.hit(lay.nodes, x, y), inner: scene.group_at(lay.nodes, x, y) };
  return Input { ...inp, press: press, flung: "", fling: motion.no_fling(), sprung: "", spring: no_spring() };
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

function moved(inp: Input, lay: Layout, x: i32, y: i32, now: i64, d: f64): Input {
  let p = inp.press;
  let slop = (SLOP_DP * d) as i32;
  if (!p.moved && (x - p.x0).abs() <= slop && (y - p.y0).abs() <= slop) {
    return Input { ...inp, press: Press { ...p, x: x, y: y } };
  }
  if (!p.moved) {
    p = dragged(inp, lay, x, y);
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
  let rest = Input { ...inp, press: no_press() };
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
    return (Input { ...inp, press: no_press() }, None);
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

// The milliseconds from `now` to the long press of the finger, or -1 when
// no long press can come.
pub function long_wait(inp: Input, now: i64): i32 {
  if (long_target(inp).is_none()) {
    return -1;
  }
  let left = inp.press.at + LONG_PRESS_NS - now;
  if (left <= 0i64) {
    return 0;
  }
  return ((left + 999999i64) / 1000000i64) as i32;
}

// The input at the time `now`, and the node of a long press when its time
// came. `long_message` gives the message of that node.
pub function long_press(inp: Input, now: i64): (Input, Option[scene.Node]) {
  let Some(n) = long_target(inp) else {
    return (inp, None);
  };
  if (now - inp.press.at < LONG_PRESS_NS) {
    return (inp, None);
  }
  return (Input { ...inp, press: Press { ...inp.press, fired: true } }, Some(n));
}

// A list flings, or springs back to its end.
pub function (inp: Input) flinging(): boolean {
  return motion.moving(inp.fling) || inp.sprung.len() > 0;
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
  return settled(sprung(flung(inp, lay, now), now), lay);
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

// The nodes of `lay` with the node under the finger shown pressed, in the
// color `pressed`.
pub function shown(inp: Input, lay: Layout, pressed: i32): scene.Node[] {
  let p = inp.press;
  let Some(t) = p.target else {
    return lay.nodes;
  };
  if (!p.active || p.moved || on_slider(p)) {
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
  effects: (S) => S,
  theme: (boolean) => Theme,
  meter: boolean,
}

// A program with no messages of its own, which the back key closes, in the
// colors of `theme`.
pub function program[S, M](view: (S, Screen) => El[M], update: (own S, M) => S): Program[S, M] {
  return Program { view: view, update: update, wait: (s: S): i32 => -1, tick: None, animating: (s: S): boolean => false, frame: None, back: (s: S): Option[M] => None, system: (e: android.Event): Option[M] => None, effects: (s: S): S => s, theme: (dark: boolean): Theme => theme(dark), meter: false };
}

// After each message, the shell gives the state to `effects`, which does
// what the state asks of the device and gives the state back. `update`
// stays a function with no effect: it puts what it wants done in the
// state, and a test reads that from the state.
pub function (p: Program[S, M]) with_effects(effects: (S) => S): Program[S, M] {
  return Program { ...p, effects: effects };
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

pub function (p: Program[S, M]) with_theme(theme: (boolean) => Theme): Program[S, M] {
  return Program { ...p, theme: theme };
}
```

### `shell`

The shell holds the state of the app and what the module keeps for it: the window, the painter, the finger and the lists. `step` gives it one event of `android.run`.

A change asks for a frame, and the frame draws at the next vsync. So many changes between two frames draw one time.

```fern
<<shell>>=
// What the shell does on the device. `size` gives the width and the height
// of a window. `density` gives the pixels of one dp. `font_scale` gives
// the scale of the text of the system. `now` gives the time
// on the monotonic clock in ns. `present` draws the nodes on the window
// over a background color. `show_keyboard` shows the keyboard of the system
// on a text, which has more than one line when the flag is true.
// `set_keyboard` gives it another text, with the cursor before a
// character. `hide_keyboard` takes it away.
pub struct Device {
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
}

// What the shell keeps for the app. `lay` is the layout of the last frame.
// `focus` is the name of the field that has the focus, or "". `typed` is
// the text that the keyboard holds for it, `caret` is the character that
// its caret is before, and `area` tells that it has more than one line.
// `cost` is the cost of the last frame.
pub struct Host { dev: Device, fonts: text.Fonts, paint: painter.Painter, window: usize, screen: Screen, input: Input, lay: Layout, frames: i32, focus: string, typed: string, caret: i32, area: boolean, cost: Cost }

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
pub struct Shell[S, M] { app: S, host: Host, said: Messages[M] }

// A shell for the state `state`, which draws text with `fonts`.
pub function shell[S, M](own state: S, fonts: text.Fonts, dev: Device): Shell[S, M] {
  let screen = Screen { w: 0, h: 0, d: dev.density(), dark: dev.dark(), theme: theme(false), left: 0, top: 0, right: 0, bottom: 0, font: dev.font_scale() };
  return Shell { app: state, host: Host { dev: dev, fonts: fonts, paint: painter.new(fonts), window: 0 as usize, screen: screen, input: no_input(), lay: no_layout(), frames: 0, focus: "", typed: "", caret: 0, area: false, cost: no_cost() }, said: no_messages() };
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
  return changed(Host { ...h, paint: painter.with_size(p, w, hh), window: window, screen: Screen { ...h.screen, w: w, h: hh, d: h.dev.density(), dark: h.dev.dark(), font: h.dev.font_scale() } });
}

// The shell after `update` took the message `m`.
pub function send[S, M](own s: Shell[S, M], p: Program[S, M], m: M): Shell[S, M] {
  let next = p.update(s.app, m);
  return Shell { app: p.effects(next), host: changed(s.host), said: s.said };
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
  let next = p.update(s.app, frame(ns));
  return Shell { app: p.effects(next), host: s.host, said: s.said };
}

// The host with no field in focus and no keyboard.
function blurred(h: Host): Host {
  if (h.focus.len() > 0) {
    h.dev.hide_keyboard();
    h.dev.request_frame();
  }
  return Host { ...h, focus: "", typed: "", caret: 0, area: false };
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
  return Host { ...h, typed: f.content, caret: end };
}

// The host with its lists where they are at the time `ns`.
function advanced(h: Host, ns: i64): Host {
  return Host { ...h, input: advance(h.input, h.lay, ns) };
}

// Draws the layout `lay` on the screen `screen`, with the nodes `over` on
// top of it. `cost` has the costs of the frame before the painter.
function painted(own h: Host, lay: Layout, over: scene.Node[], screen: Screen, animating: boolean, cost: Cost): Host {
  let t0 = h.dev.now();
  let nodes = shown(h.input, lay, screen.theme.pressed);
  for n in over {
    nodes = nodes.append(n);
  }
  let paint = h.dev.present(h.paint, h.window, nodes, screen.theme.background);
  let t1 = h.dev.now();
  h.dev.publish(scene.access_items(lay.nodes));
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

// Draws the frame of the time `ns`.
function drawn[S, M](own s: Shell[S, M], p: Program[S, M], ns: i64): Shell[S, M] {
  if (s.host.window == 0 as usize || s.host.screen.w == 0) {
    return s;
  }
  let screen = Screen { ...s.host.screen, theme: p.theme(s.host.screen.dark) };
  let t0 = s.host.dev.now();
  let root = p.view(s.app, screen);
  let t1 = s.host.dev.now();
  let animating = p.animating(s.app);
  let host = advanced(synced(s.host, root), ns);
  let built = layout(root, host.fonts, screen, host.input.offsets, Focus { field: host.focus, caret: host.caret });
  let cost = Cost { ...aged(host.cost, host.frames), view: ms_between(t0, t1), layout: ms_between(t1, host.dev.now()) };
  let over: scene.Node[] = [];
  if (p.meter) {
    over = meter(host.fonts, screen, host.cost);
  }
  return Shell { app: s.app, host: painted(host, built.layout(), over, screen, animating, cost), said: built.messages() };
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
  return changed(Host { ...h, focus: f.field, typed: f.content, caret: caret, area: f.lines > 1 });
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
  let env = Env { fonts: h.fonts, d: sc.d, font: sc.font, theme: sc.theme, offsets: h.input.offsets, focus: Focus { field: h.focus, caret: h.caret } };
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
  return told_maybe(Shell { app: s.app, host: changed(host), said: s.said }, p, m);
}

function touched[S, M](own s: Shell[S, M], p: Program[S, M], action: i32, x: i32, y: i32): Shell[S, M] {
  let h = s.host;
  let target = h.input.press.target;
  let (input, tapped) = touch(h.input, h.lay, action, x, y, h.dev.now(), h.screen.d);
  let held = target;
  if (action == 0) {
    held = input.press.target;
  }
  if let Some(n) = held {
    if (n.tap.name == SLIDER_ACTION && action <= 2) {
      return slid(s, p, input, n, x, action == 0);
    }
  }
  let host = Host { ...h, input: input };
  if (action == 0) {
    host = pressed(Host { ...h, input: input });
  }
  let Some(on) = tapped else {
    return Shell { app: s.app, host: changed(host), said: s.said };
  };
  let name = field_of(on.tap.name);
  if (name.len() > 0) {
    let found = field_el(p.view(s.app, host.screen), name);
    if let (Some(f), Some(n)) = (found, target) {
      return Shell { app: s.app, host: field_tapped(host, f, n, x, y), said: s.said };
    }
    return Shell { app: s.app, host: changed(host), said: s.said };
  }
  let m = tap_message(s.said, h.lay, on);
  return told_maybe(Shell { app: s.app, host: changed(host), said: s.said }, p, m);
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
  return told_maybe(Shell { app: s.app, host: changed(s.host), said: s.said }, p, m);
}

// The shell after a key of a hardware keyboard changed the text of the
// field in focus to `text`, with the caret before the character `caret`.
// The keyboard of the system gets the same text.
function retyped[S, M](own s: Shell[S, M], p: Program[S, M], text: string, caret: i32): Shell[S, M] {
  let h = s.host;
  h.dev.set_keyboard(text, caret);
  return edit_told(Shell { app: s.app, host: Host { ...h, typed: text, caret: caret }, said: s.said }, p, text);
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

// The shell with the caret `by` characters to the right.
function stepped[S, M](own s: Shell[S, M], by: i32): Shell[S, M] {
  let h = s.host;
  let caret = (h.caret + by).max(0).min(text.letters(h.typed).len());
  h.dev.set_keyboard(h.typed, caret);
  return Shell { app: s.app, host: changed(Host { ...h, caret: caret }), said: s.said };
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
  return told_maybe(Shell { app: s.app, host: blurred(s.host), said: s.said }, p, m);
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
    return deleted(s, p);
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
      return edit_told(Shell { app: s.app, host: Host { ...h, typed: t, caret: caret_after(h.typed, t, h.caret) }, said: s.said }, p, t);
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
        return Shell { app: s.app, host: blurred(h), said: s.said };
      }
      if (action == 0) {
        return key_edited(s, p, code);
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
  let waits = p.wait(s.app) >= 0;
  if (long_wait(h.input, now) != 0) {
    if (waits) {
      let tick = p.tick;
      return told_maybe(s, p, tick);
    }
    return s;
  }
  let (input, pressed) = long_press(h.input, now);
  let m: Option[M] = None;
  if let Some(n) = pressed {
    m = long_message(s.said, h.lay, n);
  }
  return told_maybe(Shell { app: s.app, host: Host { ...h, input: input }, said: s.said }, p, m);
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
      return Shell { app: s.app, host: on_window(h, w.ptr), said: s.said };
    },
    android.WindowResized(w) => {
      return Shell { app: s.app, host: on_window(h, w.ptr), said: s.said };
    },
    android.WindowRedraw(w) => {
      return Shell { app: s.app, host: on_window(h, w.ptr), said: s.said };
    },
    android.WindowDestroyed(_) => {
      return Shell { app: s.app, host: Host { ...h, paint: h.dev.detach(h.paint), window: 0 as usize }, said: s.said };
    },
    android.ContentRect(left, top, right, bottom) => {
      let sc = h.screen;
      return Shell { app: s.app, host: changed(Host { ...h, screen: Screen { ...sc, left: left.max(0), top: top.max(0), right: (sc.w - right).max(0), bottom: (sc.h - bottom).max(0) } }), said: s.said };
    },
    android.Destroy => {
      let t = told_maybe(s, p, p.system(e));
      return Shell { app: t.app, host: Host { ...t.host, paint: t.host.dev.close(t.host.paint), window: 0 as usize }, said: t.said };
    },
    _ => {
      return told_maybe(s, p, p.system(e));
    }
  }
}

// The shell after the event `e` of android.run.
pub function step[S, M](own s: Shell[S, M], p: Program[S, M], e: android.Event): Shell[S, M] {
  let t0 = s.host.dev.now();
  let out = handled(s, p, e);
  let c = out.host.cost;
  let ms = ms_between(t0, out.host.dev.now());
  if (ms <= c.step) {
    return out;
  }
  return Shell { app: out.app, host: Host { ...out.host, cost: Cost { ...c, step: ms } }, said: out.said };
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
      return touched(s, p, action, x as i32, y as i32);
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

// The milliseconds that android.run waits for the next event before a
// Tick, or -1 for no limit.
pub function wait[S, M](s: Shell[S, M], p: Program[S, M]): i32 {
  let long = long_wait(s.host.input, s.host.dev.now());
  let own_wait = p.wait(s.app);
  if (long < 0) {
    return own_wait;
  }
  if (own_wait < 0) {
    return long;
  }
  return long.min(own_wait);
}
```
