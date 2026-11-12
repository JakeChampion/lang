---
title: Screens
description: The elements of std/android/ui — columns, rows, text, buttons, lists, text fields, pictures and custom drawing — and how they take their size.
sidebar:
  order: 1
---

`view` returns one element, which holds other elements. This page lists
the elements and the methods that change them. Sizes are in dp, so a
screen has the same size on each phone. Text sizes are in sp: a text
grows with the font size that the person set for the system.

## Columns, rows and stacks

```fern
ui.column([a, b, c])   // one below the other
ui.row([a, b, c])      // side by side
ui.stack([a, b])       // one over the other: b is on top
```

Each method returns a new element, so you chain them:

```fern
ui.column([title, body]).spaced(12.0).padding(16.0).background(ui.SURFACE).rounded(12.0)
```

| Method | What it does |
|---|---|
| `.spaced(dp)` | Room between the elements of a column or a row |
| `.padding(dp)`, `.padding_xy(x, y)` | Room inside the edges |
| `.aligned(a)` | Where the elements go across: `ui.START`, `ui.CENTER`, `ui.END`, `ui.STRETCH` |
| `.justified(j)` | Where they go along: `ui.START`, `ui.CENTER`, `ui.END`, `ui.BETWEEN` |
| `.centered()` | The middle, along and across |
| `.background(color)`, `.rounded(dp)`, `.border(color, dp)` | The look of the box |

## Sizes

An element takes the size of what it holds. Three methods change that:

| Method | The element takes |
|---|---|
| `.width(dp)`, `.height(dp)` | An exact size |
| `.wide()`, `.tall()` | All the width, or all the height, that is left |
| `.grow(weight)` | A share of the room that is left, by its weight |

`ui.space()` is an empty element that grows. It pushes the elements
after it to the far edge:

```fern
ui.row([ui.text("Wi-Fi"), ui.space(), ui.toggle(m.wifi, "Wi-Fi", WifiChanged)]).aligned(ui.CENTER)
```

The element that `view` returns takes the whole screen, less the status
bar, the navigation bar and the keyboard.

## Text

```fern
ui.text("Hello").sized(24.0).weighted(700).colored(ui.MUTED).clamped(2)
```

A text wraps at the width of its box. `.clamped(n)` keeps `n` lines and
ends the last one with an ellipsis. The weights are 400, 500 and 700.

## Colors

A color is `draw.rgb(r, g, b)` from `std/android/draw`, or a role. A role
takes its color from the theme, which is light or dark as the system is:

`ui.PRIMARY`, `ui.ON_PRIMARY`, `ui.INK`, `ui.MUTED`, `ui.SURFACE`,
`ui.OUTLINE`, `ui.BACKGROUND`.

Use roles, and the app has a dark theme with no more code. For your own
colors, give the program a theme:

```fern
ui.program(view, update).with_theme((dark: boolean): ui.Theme => my_theme(dark))
```

When the person chooses the colors in the app, give the program a theme
that reads its state:

```fern
ui.program(view, update).with_theme_of((s: State, dark: boolean): ui.Theme => ui.theme(s.dark || dark))
```

## Buttons and other ready-made elements

| Element | What it is |
|---|---|
| `ui.button(label, msg)` | A filled button |
| `ui.outlined_button(label, msg)` | A button with a border |
| `ui.text_button(label, msg)` | A button with its text only |
| `ui.toggle(on, say, msg)` | A switch. TalkBack reads `say` and its state |
| `ui.card([...])` | A column on the surface color, with round corners |
| `ui.progress(part)` | A bar, from 0 to 1 |
| `ui.slider(part, slide)` | A bar with a thumb that a finger moves |
| `ui.divider()` | A line across a column |

## Taps

Each element can take a tap. `.on_tap(msg)` gives it a message of your
message type, and a tap gives that message to `update`.
[State and messages](../messages/) shows how to declare the type.

A list has many rows that do the same thing. Give the message the
number of the row:

```fern
ui.row([ui.text(item.title)]).on_tap(Open(i))
```

`.on_long(msg)` does the same for a long press. The element under a
finger shows that it is pressed.

## Sliders

`ui.slider` shows a part from 0 to 1, and takes a function that makes a
message from the part under the finger. A touch or a drag on the slider
gives that message, and a drag that starts on it scrolls no list:

```fern
ui.slider(m.volume, (part: f64): Msg => Volume(part)).described("Volume")
```

Your state keeps the part, as it keeps the text of a field. `.on_slide`
gives the same message to an element of your own.

## Lists that scroll

`.scroll("name")` makes a column scroll down, and a row scroll across.
The list flings as each Android list does, and it springs back when a
finger pulls it past its end. A row that scrolls across can be in a list
that scrolls down.

```fern
ui.column(rows).scroll("settings").grow(1.0)
```

For a long list, use `ui.list`. It makes only the rows that show, so a
list of 100,000 rows costs as much as a list of 20:

```fern
ui.list("inbox", m.mails.len(), 72.0, (i: i32): ui.El[Msg] => mail_row(m.mails[i], i))
```

The arguments are the name of the list, the number of rows, and the
height of a row in dp. The last argument is a function that makes one
row. Write the result type of that function, `ui.El[Msg]`, so the
compiler has the type of its messages.

## Text fields

```fern
ui.field("name", m.name, "Your name").on_edit((text: string): Msg => Named(text)).on_submit(Saved)
```

The arguments are the name of the field, its text, and the hint that it
shows while it is empty. A tap shows the keyboard of the system.

Your state keeps the text. `.on_edit` takes a function that makes a
message from the new text, and each change gives that message to
`update`:

```fern
Named(text) => {
  return Model { ...m, name: text };
},
Saved => {
  return saved(m);
},
```

The message of `.on_submit` comes when the person presses the action
key of the keyboard. To clear the field, set its text to `""` in your
state.

A tap on the text of a field puts the caret there. A text that is wider
than the field shows the part around the caret.

`ui.area` is a field of more than one line. Its text wraps, and the
enter key starts a new line:

```fern
ui.area("note", m.note, "Write a note", 4)
```

The last argument is the number of lines that the area shows.

## Pictures and your own drawing

`ui.picture(key, image)` shows a `draw.Image`, scaled to cover its box.
`std/android/image` decodes JPEG, PNG and WebP files.

`ui.canvas(key, paint)` shows what your function draws:

```fern
ui.canvas("dial|" + m.angle.to_string(), (f: draw.Frame, x: i32, y: i32, w: i32, h: i32): void => draw_dial(f, x, y, w, h, m.angle)).width(120.0).height(120.0)
```

The key names the pixels. The module keeps the picture of each key on
the GPU, and calls your function again only for a new key.

## Dialogs

A dialog is an element over the screen. `.modal()` keeps the touches off
the elements below it:

```fern
ui.stack([
  page(m),
  ui.stack([ui.card([ui.text("Delete this item?"), ui.button("Delete", Deleted)])]).centered().grow(1.0).background(draw.rgba(0, 0, 0, 128)).modal().on_tap(Dismissed),
])
```

## Accessibility

TalkBack reads each text and each element with a message. An element
with no text, such as an icon, needs a description:

```fern
ui.canvas("bin", draw_bin).width(24.0).height(24.0).on_tap(Deleted).described("Delete")
```
