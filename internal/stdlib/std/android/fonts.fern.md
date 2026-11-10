# Android system fonts

This module finds a font file of the Android system by its family and its weight. An app can then draw its text with a font that the device already has, and its APK holds no font.

## Model

`find` gives the file of the font that is nearest to a family and a weight. A weight is a number from 100 to 900: 400 is regular and 700 is bold.

On Android 10 (API level 29) and later, `find` asks the font matcher of the system. The matcher knows the families of the system by name, such as `sans-serif`. For a family that it does not know, it gives the default font of the system.

On older versions, `find` reads the names of the files in `/system/fonts`. A file there has the name of its family, a hyphen and the name of its style, as in `Roboto-Medium.ttf`. `find_in` does the same for a directory that the caller names.

A `Found` holds the path of the file. `index` is the place of the font in a file that holds a collection of fonts. `weight` is the weight of the font that the file holds. Since Android 12 one file holds each weight of Roboto as a variable font. For such a file, `axes` holds the value that the system sets on each axis for the weight.

`styled` gives a font of `std/truetype` the values of a `Found`. Parse a file one time and call `styled` for each weight that the file holds.

## Contracts

`find` needs the context of the app. It gives None when the system has no file for the family. Some directories have no file of the family. For them, `find_in` gives a file of the first of Roboto, Noto Sans and Droid Sans in the directory. It does not give an italic file.

`styled` sets the weight on a variable font also when the system gave no value for it. A font with no axes stays as it is.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<C functions>>

<<file names>>

<<the matcher>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module finds a font file of the Android system by family and weight.
import "std/android";
import "std/i32";
import "std/jni";
import "std/string";
import "std/truetype";
import "std/utf8";

// A value on an axis of a variable font, such as 500 on "wght".
pub struct Axis { tag: string, value: f64 }

// A font file of the system. `index` is the place of the font in a
// collection file. `weight` is the weight of the font in the file. `axes`
// holds the values that the system sets on a variable font.
pub struct Found { path: string, index: i32, weight: i32, axes: Axis[] }
```

### `C functions`

The font matcher came in API level 29, so each import is weak, and `find` reads the API level before it calls one.

```fern
<<C functions>>=
@import("libandroid.so", "AFontMatcher_create", weak)
function c_matcher_create(): usize;

@import("libandroid.so", "AFontMatcher_destroy", weak)
function c_matcher_destroy(matcher: usize): void;

@import("libandroid.so", "AFontMatcher_setStyle", weak)
function c_matcher_set_style(matcher: usize, weight: i32, italic: i32): void;

@import("libandroid.so", "AFontMatcher_match", weak)
function c_matcher_match(matcher: usize, family: usize, text: usize, length: i32, run: usize): usize;

@import("libandroid.so", "AFont_close", weak)
function c_font_close(font: usize): void;

@import("libandroid.so", "AFont_getFontFilePath", weak)
function c_font_path(font: usize): usize;

@import("libandroid.so", "AFont_getWeight", weak)
function c_font_weight(font: usize): i32;

@import("libandroid.so", "AFont_getCollectionIndex", weak)
function c_font_index(font: usize): usize;

@import("libandroid.so", "AFont_getAxisCount", weak)
function c_font_axis_count(font: usize): usize;

@import("libandroid.so", "AFont_getAxisTag", weak)
function c_font_axis_tag(font: usize, axis: i32): u32;

@import("libandroid.so", "AFont_getAxisValue", weak)
function c_font_axis_value(font: usize, axis: i32): f32;
```

### `file names`

```fern
<<file names>>=
// The directory of the fonts of the system.
pub const SYSTEM_DIR: string = "/system/fonts";

// The weight of the style `style` of a file name, or -1 for a style that
// is not an upright weight.
pub function style_weight(style: string): i32 {
  return match (style) {
    "Thin" => 100,
    "ExtraLight" => 200,
    "Light" => 300,
    "Regular" => 400,
    "Medium" => 500,
    "SemiBold" => 600,
    "Bold" => 700,
    "ExtraBold" => 800,
    "Black" => 900,
    _ => -1,
  };
}

// The weight of the file `name` when it is a font of `family`, or -1.
function file_weight(name: string, family: string): i32 {
  let Some(rest) = name.strip_prefix(family + "-") else {
    return -1;
  };
  if let Some(style) = rest.strip_suffix(".ttf") {
    return style_weight(style);
  }
  if let Some(style) = rest.strip_suffix(".otf") {
    return style_weight(style);
  }
  return -1;
}

// The file of `names` that is a font of `family` with the weight nearest
// to `weight`, and its weight. Of two files at the same distance, the
// heavier is nearer to a weight above 500 and the lighter to the others.
pub function pick(names: string[], family: string, weight: i32): Option[(string, i32)] {
  let best = "";
  let best_weight = -1;
  for name in names {
    let w = file_weight(name, family);
    if (w >= 0 && (best_weight < 0 || nearer(w, best_weight, weight))) {
      best = name;
      best_weight = w;
    }
  }
  if (best_weight < 0) {
    return None;
  }
  return Some((best, best_weight));
}

// True when the weight `a` is nearer to `want` than the weight `b` is.
function nearer(a: i32, b: i32, want: i32): boolean {
  let da = (a - want).abs();
  let db = (b - want).abs();
  if (da != db) {
    return da < db;
  }
  return a > b == want > 500;
}

// The font of `family` in the directory `dir` with the weight nearest to
// `weight`. With no file of the family, the font of the first of Roboto,
// Noto Sans and Droid Sans that the directory has.
pub function find_in(dir: string, family: string, weight: i32): Option[Found] {
  let Ok(names) = read_dir(dir) else {
    return None;
  };
  for f in [family, "Roboto", "NotoSans", "DroidSans"] {
    if let Some((name, w)) = pick(names, f, weight) {
      return Some(Found { path: dir + "/" + name, index: 0, weight: w, axes: [] });
    }
  }
  return None;
}
```

### `the matcher`

```fern
<<the matcher>>=
// The font `font`, parsed from the file of `found`, at the weight `weight`:
// each axis takes the value that the system gave. A variable font for
// which the system gave no weight takes `weight`.
pub function styled(font: truetype.Font, found: Found, weight: i32): truetype.Font {
  let out = font;
  let weighted = false;
  for a in found.axes {
    out = out.at_axis(a.tag, a.value);
    weighted = weighted || a.tag == "wght";
  }
  if (!weighted) {
    out = out.at_axis("wght", weight as f64);
  }
  return out;
}

// The font of the system that is nearest to `family` and `weight`. `ctx`
// is the context of the app.
pub function find(ctx: usize, family: string, weight: i32): Option[Found] {
  if (android.sdk_version(ctx) < 29) {
    return find_in(SYSTEM_DIR, family, weight);
  }
  let name = jni.cstr(family);
  let matcher = c_matcher_create();
  c_matcher_set_style(matcher, weight, 0);
  // The matcher gives the font for a text. One letter "A" in UTF-16 is the
  // text.
  let text = jni.cstr("A");
  let font = c_matcher_match(matcher, name, text, 1, 0 as usize);
  jni.free_buffer(text);
  jni.free_buffer(name);
  c_matcher_destroy(matcher);
  if (font == 0 as usize) {
    return None;
  }
  let found = Found { path: text_at(c_font_path(font)), index: c_font_index(font) as i32, weight: c_font_weight(font) & 65535, axes: axes_of(font) };
  c_font_close(font);
  if (found.path.len() == 0) {
    return None;
  }
  return Some(found);
}

// The text at the address `p`, to its NUL byte.
function text_at(p: usize): string {
  let bytes: u8[] = [];
  if (p == 0 as usize) {
    return "";
  }
  unsafe {
    let i = 0;
    while (i < 4096 && __load_u8(p + i as usize) != 0) {
      bytes = bytes.append(__load_u8(p + i as usize) as u8);
      i += 1;
    }
  }
  return utf8.from_bytes_lossy(bytes);
}

// The four letters of the tag `tag` of an axis, first letter in the high
// byte.
pub function tag_text(tag: u32): string {
  let bytes: u8[] = [];
  for shift in [24, 16, 8, 0] {
    bytes = bytes.append((tag >> shift as u32 & 255u32) as u8);
  }
  return utf8.from_bytes_lossy(bytes);
}

function axes_of(font: usize): Axis[] {
  let out: Axis[] = [];
  for i in 0..c_font_axis_count(font) as i32 {
    out = out.append(Axis { tag: tag_text(c_font_axis_tag(font, i)), value: c_font_axis_value(font, i) as f64 });
  }
  return out;
}
```
