# Text for Android apps

This module measures text and draws it with the fonts of the device, for a NativeActivity app of `std/android`.

## Model

A `Fonts` holds one font for each weight that the app uses. `system` finds the files of a family on the device with `std/android/fonts`, so the APK holds no font. `load` makes a `Fonts` from files that the caller found, for example the files of a test on the host.

Each function takes a face: the index of a weight in the `Fonts`. `face_for` gives the index of the weight that is nearest to a number from 100 to 900.

A face does not have each character. For a character that the face does not have, a `Fallback` gives a font that has it. `covering` finds the fallback font of each character of a text, and gives the `Fonts` with them. It takes a function that gives the font files to try, in order. `system_fallbacks` gives the function for the fallback order of the system. Call `covering` before a text is measured or drawn, and keep the `Fonts` that it gives.

`fallback` makes a fallback that tries a list of fonts first, and `with_fallback` gives it to a `Fonts`. Each font file loads one time, and the fallback keeps the font of each code point. A character that is not covered takes the face. The height of a line comes from the face alone.

`width` and `line` measure a text with no drawing. `fit` cuts a text to a width, and `wrap` breaks it into lines. `wrap_ranges` gives the lines as ranges of characters. A text field uses them to find its cursor, and `std/bidi` uses them to order each line.

A run of Arabic letters goes to `std/shape`. So does a run of the letters of Hebrew, Thai, Lao, Khmer, Myanmar, an Indic script, Sinhala, Tibetan, Mongolian, Balinese, Javanese or N'Ko.

The Indic scripts are Devanagari, Bengali, Gurmukhi, Gujarati, Oriya, Tamil, Telugu, Kannada and Malayalam. `std/shape` gives the joining forms of the Arabic letters, and the conjuncts and the reordered matras of the Indic scripts. It orders the marks of the other scripts, and gives the position of each glyph. The run takes the font of its first letter. An Arabic, a Hebrew and an N'Ko run go from right to left.

A mark sits on its base at the anchors of the font. As in Minikin, a mark takes the font of its base when that font has the mark, so that the two can attach. The run has no letter spacing, so that its letters stay joined. Outside such a run, a mark of the font has no advance.

A glyph with colors in the `COLR` table of its font draws in its colors and not in the color of the text. An emoji is such a glyph. Its `Placed` holds its premultiplied colors, and a sprite holds them as a second layer that `put` draws over the text.

`std/truetype` draws each glyph one time for each size, weight and quarter of a pixel. A `Cache` keeps the glyphs. It also keeps a `Sprite` for each text: the coverage of the whole text, which `put` draws on a frame in a color. `layout` gives the glyphs of a text with their positions, for a painter that draws glyphs from an atlas on the GPU.

## Contracts

A size `px` is the pixels of one em. A letter spacing `track` is in thousandths of the em, as `letterSpacing` of Android. A `Fonts` with no font gives a width of 0 and draws nothing.

A text to draw is in visual order: its first character is at the left. `std/bidi` gives each line of a text that mixes directions in that order.

Each function that makes a glyph or a sprite takes the `Cache` and gives it back with the new entry.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<fonts>>

<<measurement>>

<<lines>>

<<sprites>>

<<drawing>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module measures text and draws it with the fonts of the device.
import "core/map";
import "std/android/draw";
import "std/android/fonts";
import "std/option";
import "std/result";
import "std/shape";
import "std/truetype";
import "std/unicode";
import "std/utf8";
```

### `fonts`

```fern
<<fonts>>=
pub struct Fonts { faces: truetype.Font[], weights: i32[], more: Fallback }

// The fonts for the characters that the faces do not have: the fonts that
// are loaded and the name of each, the font for each code point and weight
// that was looked for or -1, and the number of those code points.
pub struct Fallback { names: string[], faces: truetype.Font[], by_char: Map[i64, i32], looked: i32 }

// The id of the first fallback font. The faces have the ids before it.
const FALLBACK: i32 = 64;

// Fonts with no fallback.
pub function from_faces(faces: truetype.Font[], weights: i32[]): Fonts {
  return Fonts { faces: faces, weights: weights, more: fallback([]) };
}

// A fallback that tries the fonts `given` first.
pub function fallback(given: truetype.Font[]): Fallback {
  let names: string[] = [];
  for i in 0..given.len() {
    names = names.append("given " + i.to_string());
  }
  return Fallback { names: names, faces: given, by_char: map_new(64), looked: 0 };
}

// `fs` with the fallback `more`.
pub function (fs: Fonts) with_fallback(more: Fallback): Fonts {
  return Fonts { ...fs, more: more };
}

// The number of code points that the fallback looked for. It grows when
// `covering` finds a character that it did not know.
pub function (fs: Fonts) looked(): i32 {
  return fs.more.looked;
}

function weight_of(fs: Fonts, w: i32): i32 {
  return fs.weights[w.max(0).min(fs.weights.len() - 1)];
}

function char_key(fs: Fonts, w: i32, cp: i32): i64 {
  return cp as i64 * 1024i64 + weight_of(fs, w) as i64;
}

// The id of the font for the code point `cp` in the face `w`: `w` when the
// face has it or no covered font has it, or else the id of a fallback font.
function face_of(fs: Fonts, w: i32, cp: i32): i32 {
  if (cp < 32 || font(fs, w).glyph(cp) != 0) {
    return w;
  }
  if let Some(at) = fs.more.by_char.get(char_key(fs, w, cp)) {
    if (at >= 0) {
      return FALLBACK + at;
    }
  }
  return w;
}

// `fs` with a fallback font for each character of `s` that the face `w`
// does not have, when a fallback font has it. `find` gives the files of the
// fonts to try for a weight and a code point, in order.
pub function (fs: Fonts) covering(w: i32, s: string, find: (i32, i32) => fonts.Found[]): Fonts {
  let out = fs;
  for c in s.chars() {
    out = covered(out, w, c as i32, find);
  }
  return out;
}

// The function for `covering` that gives the fallback order of the system
// for `family`. `ctx` is the context of the app.
pub function system_fallbacks(ctx: usize, family: string): (i32, i32) => fonts.Found[] {
  return (weight: i32, cp: i32): fonts.Found[] => fonts.fallbacks(ctx, family, weight, cp);
}

function covered(fs: Fonts, w: i32, cp: i32, find: (i32, i32) => fonts.Found[]): Fonts {
  if (!has(fs) || cp < 32 || font(fs, w).glyph(cp) != 0) {
    return fs;
  }
  let key = char_key(fs, w, cp);
  if (fs.more.by_char.get(key).is_some()) {
    return fs;
  }
  // A loaded font that has the character comes before the source.
  let more = fs.more;
  let at = first_with(more, cp);
  if (at < 0) {
    let (next, found) = from_files(more, find(weight_of(fs, w), cp), cp);
    more = next;
    at = found;
  }
  return Fonts { ...fs, more: Fallback { ...more, by_char: more.by_char.insert(key, at), looked: more.looked + 1 } };
}

// The index of the first loaded font that has the code point `cp`, or -1.
function first_with(more: Fallback, cp: i32): i32 {
  for i in 0..more.faces.len() {
    if (more.faces[i].glyph(cp) != 0) {
      return i;
    }
  }
  return -1;
}

// The fallback with the fonts of `found` loaded, and the index of the
// first of them that has `cp`, or -1.
function from_files(more: Fallback, found: fonts.Found[], cp: i32): (Fallback, i32) {
  let out = more;
  for f in found {
    let (next, at) = loaded_font(out, f);
    out = next;
    if (at >= 0 && out.faces[at].glyph(cp) != 0) {
      return (out, at);
    }
  }
  return (out, -1);
}

// The fallback with the font of `found` loaded, and its index. The file
// loads one time. The index is -1 when the file is not a font.
function loaded_font(more: Fallback, found: fonts.Found): (Fallback, i32) {
  let name = found.path + "#" + found.index.to_string() + "@" + found.weight.to_string();
  for i in 0..more.names.len() {
    if (more.names[i] == name) {
      return (more, i);
    }
  }
  if let Some(font) = truetype.parse_index(read_file_bytes(found.path).unwrap_or([]), found.index) {
    return (Fallback { ...more, names: more.names.append(name), faces: more.faces.append(fonts.styled(font, found, found.weight)) }, more.names.len());
  }
  return (more, -1);
}

// The fonts of the files in `found`, one for each weight of `ws`. `read`
// gives the bytes of a file. One file can hold more than one weight, so
// each font of a file is read and parsed one time. A weight with no font takes the
// first font that there is.
pub function load(found: Option[fonts.Found][], ws: i32[], read: (string) => u8[]): Fonts {
  let paths: string[] = [];
  let parsed: Option[truetype.Font][] = [];
  let got: Option[truetype.Font][] = [];
  for i in 0..found.len().min(ws.len()) {
    let face: Option[truetype.Font] = None;
    if let Some(f) = found[i] {
      let key = f.path + "#" + f.index.to_string();
      let at = place(paths, key);
      if (at < 0) {
        at = paths.len();
        paths = paths.append(key);
        parsed = parsed.append(truetype.parse_index(read(f.path), f.index));
      }
      if let Some(font) = parsed[at] {
        face = Some(fonts.styled(font, f, ws[i]));
      }
    }
    got = got.append(face);
  }
  return from_faces(filled(got), ws);
}

// The index of `path` in `paths`, or -1.
function place(paths: string[], path: string): i32 {
  for i in 0..paths.len() {
    if (paths[i] == path) {
      return i;
    }
  }
  return -1;
}

// Each font of `got`, with the first font that there is in each gap, or no
// fonts when there is none.
function filled(got: Option[truetype.Font][]): truetype.Font[] {
  let faces: truetype.Font[] = [];
  for g in got {
    if let Some(first) = g {
      for h in got {
        faces = faces.append(h.unwrap_or(first));
      }
      return faces;
    }
  }
  return faces;
}

function has(fs: Fonts): boolean {
  return fs.faces.len() > 0;
}

// The face `w`, or the fallback font of the id `w`.
function font(fs: Fonts, w: i32): truetype.Font {
  if (w >= FALLBACK) {
    return fs.more.faces[w - FALLBACK];
  }
  if (w >= 0 && w < fs.faces.len()) {
    return fs.faces[w];
  }
  return fs.faces[0];
}

// The ascent above the baseline and the descent below it, in pixels, for
// text of `px` pixels to the em.
pub function ascent(fs: Fonts, w: i32, px: i32): i32 {
  if (!has(fs)) {
    return px;
  }
  let f = font(fs, w);
  return (f.ascender * px + f.units_per_em - 1) / f.units_per_em;
}

pub function descent(fs: Fonts, w: i32, px: i32): i32 {
  if (!has(fs)) {
    return px / 4;
  }
  let f = font(fs, w);
  return (-f.descender * px + f.units_per_em - 1) / f.units_per_em;
}

// The height of a line, as Android gives a text view with no font padding.
pub function line(fs: Fonts, w: i32, px: i32): i32 {
  return ascent(fs, w, px) + descent(fs, w, px);
}

// The fonts of the family `family` on the device, one for each weight of
// `ws`.
pub function system(ctx: usize, family: string, ws: i32[]): Fonts {
  let found: Option[fonts.Found][] = [];
  for w in ws {
    found = found.append(fonts.find(ctx, family, w));
  }
  return load(found, ws, (path: string): u8[] => read_file_bytes(path).unwrap_or([]));
}

// The face of `fs` with the weight nearest to `weight`: 400 is regular and
// 700 is bold.
pub function face_for(fs: Fonts, weight: i32): i32 {
  let best = 0;
  let far = 1 << 20;
  for i in 0..fs.weights.len() {
    let d = (fs.weights[i] - weight).abs();
    if (d < far) {
      far = d;
      best = i;
    }
  }
  return best;
}
```

### `measurement`

```fern
<<measurement>>=
// The glyphs of `s` from the left: the font id and the glyph of each, and
// the pen position of each in pixels, then the end of the text. `pen` is
// the position of the next glyph. `ys` moves each glyph down from the
// baseline, in pixels. `track` adds space between the
// characters, in thousandths of the em, as Android's letterSpacing does.
struct Pens { ids: i32[], glyphs: i32[], at: f64[], ys: f64[], pen: f64 }

function pens(fs: Fonts, w: i32, px: i32, track: i32, s: string): Pens {
  let cs: char[] = [];
  for c in s.chars() {
    cs = cs.append(c);
  }
  let spacing = px as f64 * (track as f64) / 1000.0;
  let p = Pens { ids: [], glyphs: [], at: [], ys: [], pen: 0.0 };
  let prev = -1;
  let prev_id = -1;
  let i = 0;
  while (i < cs.len()) {
    let (end, run_id, script) = run_end(fs, w, cs, i);
    let id = if (end > i) { run_id } else { mark_face(fs, w, cs[i], prev_id) };
    let f = font(fs, id);
    if (end > i) {
      p = shaped_run(p, f, id, px, cs, i, end, script);
      prev = -1;
      i = end;
    } else {
      let g = f.glyph(cs[i] as i32);
      let kern = if (prev >= 0 && id == prev_id) { f.kerning(prev, g) } else { 0 };
      p = placed(p, f, id, px, g, kern);
      prev = g;
      i += 1;
    }
    p = Pens { ...p, pen: p.pen + spacing };
    prev_id = id;
  }
  return Pens { ...p, at: p.at.append(p.pen - spacing) };
}

// `p` with the glyph `g` of the font `id` at its pen, after `kern` font
// units. A mark of the font has no advance.
function placed(own p: Pens, f: truetype.Font, id: i32, px: i32, g: i32, kern: i32): Pens {
  let scale = px as f64 / (f.units_per_em as f64);
  let x = p.pen + kern as f64 * scale;
  let advance = if (f.glyph_class(g) == 3) { 0.0 } else { f.advance(g) as f64 * scale };
  return pen_glyph(p, id, g, x, 0.0, x + advance);
}

// `p` with the glyph `g` of the font `id` at `x`, `y` pixels below the
// baseline, and the pen at `next`.
function pen_glyph(own p: Pens, id: i32, g: i32, x: f64, y: f64, next: f64): Pens {
  return Pens { ids: p.ids.append(id), glyphs: p.glyphs.append(g), at: p.at.append(x), ys: p.ys.append(y), pen: next };
}

// The end of the run of letters to shape from `cs[i]`, the font that
// shapes it, and its script: "arab" or the tag of block_script. The font is that of the
// first letter. It is `i`, -1 and "" when no such run starts there. The run
// can hold marks and joiners, and it holds at least one letter.
function run_end(fs: Fonts, w: i32, cs: char[], i: i32): (i32, i32, string) {
  let (id, script) = run_font(fs, w, cs, i);
  let k = i;
  let letters = 0;
  while (id >= 0 && k < cs.len() && in_run(cs[k], script) && run_face(fs, w, cs[k], id, script)) {
    letters += if (letter_script(cs[k]) == script) { 1 } else { 0 };
    k += 1;
  }
  return if (letters > 0) { (k, id, script) } else { (i, -1, "") };
}

// The font and the script of the first letter of the characters from
// `cs[i]` that can be in a run, or -1 and "".
function run_font(fs: Fonts, w: i32, cs: char[], i: i32): (i32, string) {
  let j = i;
  while (j < cs.len() && (in_run(cs[j], "arab") || in_run(cs[j], "deva") || block_script(cs[j]) != "")) {
    let script = letter_script(cs[j]);
    if (script != "") {
      return (face_of(fs, w, cs[j] as i32), script);
    }
    j += 1;
  }
  return (-1, "");
}

// The script of `c` when it is a letter of a run: "arab" for an Arabic
// letter that joins, the tag of block_script for a character that is not a
// mark, or "".
function letter_script(c: char): string {
  if (arabic(c) && unicode.joining_type(c) != JT_T) {
    return "arab";
  }
  return if (c.is_mark()) { "" } else { block_script(c) };
}

// Whether the run of the font `id` holds `c`: a letter of that font, or a
// mark or a joiner that the font has. A mark takes the font of its letter,
// as Minikin gives it.
function run_face(fs: Fonts, w: i32, c: char, id: i32, script: string): boolean {
  if (face_of(fs, w, c as i32) == id) {
    return true;
  }
  return letter_script(c) != script && font(fs, id).glyph(c as i32) != 0;
}

// The font of `c`: the font `prev` of the character before it when `c` is
// a mark that the font has, or else the font that face_of gives.
function mark_face(fs: Fonts, w: i32, c: char, prev: i32): i32 {
  if (prev >= 0 && c.is_mark() && font(fs, prev).glyph(c as i32) != 0) {
    return prev;
  }
  return face_of(fs, w, c as i32);
}

// The joining types of std/unicode that a run can hold with no Arabic
// letter: join causing, such as the zero width joiner, and transparent.
const JT_C: i32 = 1;

const JT_T: i32 = 5;

// Whether `c` can be in a run of the script `script`. An Arabic run holds
// the Arabic letters that join, and the characters that join with no
// script, such as a mark. A run of another script holds the characters of
// the blocks of its script, the marks, the ZWNJ and the ZWJ.
function in_run(c: char, script: string): boolean {
  if (script != "arab") {
    let n = c as i32;
    return block_script(c) == script || c.is_mark() || n == 0x200C || n == 0x200D;
  }
  let t = unicode.joining_type(c);
  return t == JT_C || t == JT_T || t != 0 && arabic(c);
}

// The blocks of the scripts that std/shape shapes in a run of their own,
// other than the Indic blocks from U+0900 to U+0D7F: the first and the last
// code point of each one.
const SCRIPT_BLOCKS: i32[] = [0x590, 0x5FF, 0xFB1D, 0xFB4F, 0xA8E0, 0xA8FF, 0xE00, 0xE7F, 0xE80, 0xEFF, 0x1780, 0x17FF, 0x1000, 0x109F, 0xA9E0, 0xA9FF, 0xAA60, 0xAA7F, 0x116D0, 0x116FF, 0xD80, 0xDFF, 0xF00, 0xFFF, 0x1800, 0x18AF, 0x1B00, 0x1B7F, 0xA980, 0xA9DF, 0x7C0, 0x7FF];

// The OpenType tag of the script of each block of SCRIPT_BLOCKS.
const BLOCK_TAGS: string[] = ["hebr", "hebr", "deva", "thai", "lao ", "khmr", "mymr", "mymr", "mymr", "mymr", "sinh", "tibt", "mong", "bali", "java", "nko "];

// The OpenType tag of the script of the block of `c` that std/shape shapes
// in a run of its own, or "". The Indic blocks from U+0900 to U+0D7F hold
// 128 characters each, and Devanagari Extended is Devanagari.
function block_script(c: char): string {
  let n = c as i32;
  for k in 0..BLOCK_TAGS.len() {
    if (n >= SCRIPT_BLOCKS[2 * k] && n <= SCRIPT_BLOCKS[2 * k + 1]) {
      return BLOCK_TAGS[k];
    }
  }
  let tags = ["deva", "beng", "guru", "gujr", "orya", "taml", "telu", "knda", "mlym"];
  return if (n >= 0x900 && n <= 0xD7F) { tags[(n - 0x900) / 128] } else { "" };
}

// Whether `c` is in a block of the Arabic script.
function arabic(c: char): boolean {
  let n = c as i32;
  return n >= 0x600 && n <= 0x6FF || n >= 0x750 && n <= 0x77F || n >= 0x870 && n <= 0x8FF || n >= 0xFB50 && n <= 0xFDFF || n >= 0xFE70 && n <= 0xFEFF;
}

// `p` with the glyphs of the letters `cs[i..end]` of the script `script`,
// at the positions that std/shape gives. The characters are in visual
// order. An Arabic run goes from right to left, so it is shaped from the
// last character to the first, and its glyphs are placed from the last one
// to the first. Each run of a script from right to left goes so too.
function shaped_run(own p: Pens, f: truetype.Font, id: i32, px: i32, cs: char[], i: i32, end: i32, script: string): Pens {
  let rtl = shape.rtl(script);
  let logical = "";
  for k in 0..end - i {
    logical += cs[if (rtl) { end - 1 - k } else { i + k }].to_string();
  }
  let run = shape.shape(f, logical, script);
  let scale = px as f64 / (f.units_per_em as f64);
  let out = p;
  let n = run.glyphs.len();
  for k in 0..n {
    let j = if (rtl) { n - 1 - k } else { k };
    let x = out.pen + run.pos.dx[j] as f64 * scale;
    let next = out.pen + run.pos.advances[j] as f64 * scale;
    out = pen_glyph(out, id, run.glyphs[j], x, -(run.pos.dy[j] as f64) * scale, next);
  }
  return out;
}

pub function width(fs: Fonts, w: i32, px: i32, s: string): i32 {
  return spaced_width(fs, w, px, 0, s);
}

pub function spaced_width(fs: Fonts, w: i32, px: i32, track: i32, s: string): i32 {
  if (!has(fs) || s.len() == 0) {
    return 0;
  }
  let p = pens(fs, w, px, track, s).at;
  return (p[p.len() - 1] + 0.999) as i32;
}

// `s` cut to fit in `room` pixels, with an ellipsis when it is longer.
pub function fit(fs: Fonts, w: i32, px: i32, s: string, room: i32): string {
  if (width(fs, w, px, s) <= room) {
    return s;
  }
  let cs: string[] = [];
  for c in s.chars() {
    cs = cs.append(c.to_string());
  }
  let n = cs.len();
  while (n > 0) {
    n -= 1;
    let cut = cs.take(n).join("").trim() + "…";
    if (width(fs, w, px, cut) <= room) {
      return cut;
    }
  }
  return "";
}

// The end of `s` that fits in `room` pixels, with an ellipsis in front, as
// a text field shows the text it types.
pub function fit_end(fs: Fonts, w: i32, px: i32, s: string, room: i32): string {
  if (width(fs, w, px, s) <= room) {
    return s;
  }
  let cs: string[] = [];
  for c in s.chars() {
    cs = cs.append(c.to_string());
  }
  let k = 1;
  while (k < cs.len()) {
    let cut = "…" + cs.drop(k).join("");
    if (width(fs, w, px, cut) <= room) {
      return cut;
    }
    k += 1;
  }
  return "";
}
```

### `lines`

A line breaks at a space. A word that is wider than the room breaks between two characters.

```fern
<<lines>>=
// The lines of `s` in `room` pixels. A line feed in `s` starts a new line.
pub function wrap(fs: Fonts, w: i32, px: i32, s: string, room: i32): string[] {
  let cs = letters(s);
  let lines: string[] = [];
  for r in wrap_ranges(fs, w, px, s, room) {
    let (a, b) = r;
    lines = lines.append(span(cs, a, b));
  }
  return lines;
}

// Each character of `s` as a string.
pub function letters(s: string): string[] {
  let cs: string[] = [];
  for c in s.chars() {
    cs = cs.append(c.to_string());
  }
  return cs;
}

// The characters of `cs` from `a` up to `b`, as one string.
pub function span(cs: string[], a: i32, b: i32): string {
  return cs.take(b.max(a)).drop(a).join("");
}

// The lines of `s` in `room` pixels, each as the index of its first
// character and the index after its last one. A line breaks at a space or
// at a line feed, and that character is in no line. A word that is wider
// than the room breaks between two characters.
pub function wrap_ranges(fs: Fonts, w: i32, px: i32, s: string, room: i32): (i32, i32)[] {
  let cs = letters(s);
  let out: (i32, i32)[] = [];
  let start = 0;
  while (start <= cs.len()) {
    let end = start;
    while (end < cs.len() && cs[end] != "\n") {
      end += 1;
    }
    out = paragraph_ranges(fs, w, px, cs, start, end, room, out);
    start = end + 1;
  }
  return out;
}

// The lines of the paragraph from `start` up to `end`. A line breaks at the
// last space where the line fits.
function paragraph_ranges(fs: Fonts, w: i32, px: i32, cs: string[], start: i32, end: i32, room: i32, own out: (i32, i32)[]): (i32, i32)[] {
  if (width(fs, w, px, span(cs, start, end)) <= room) {
    return out.append((start, end));
  }
  let lines = out;
  let from = start;
  let to = start;
  let has_word = false;
  let ws = start;
  while (ws <= end) {
    let we = ws;
    while (we < end && cs[we] != " ") {
      we += 1;
    }
    if (!has_word) {
      from = ws;
    }
    if (width(fs, w, px, span(cs, from, we)) > room) {
      if (has_word && to > from) {
        lines = lines.append((from, to));
      }
      from = ws;
      while (width(fs, w, px, span(cs, from, we)) > room) {
        let head = head_end(fs, w, px, cs, from, we, room);
        lines = lines.append((from, head));
        from = head;
      }
    }
    to = we;
    has_word = true;
    ws = we + 1;
  }
  return lines.append((from, to));
}

// The end of the longest start of the word from `from` up to `to` that fits
// in `room` pixels: one character or more.
function head_end(fs: Fonts, w: i32, px: i32, cs: string[], from: i32, to: i32, room: i32): i32 {
  let k = from + 1;
  while (k < to && width(fs, w, px, span(cs, from, k + 1)) <= room) {
    k += 1;
  }
  return k;
}
```

### `sprites`

```fern
<<sprites>>=
// The coverage of a text, w by h. Its first pen is `left` pixels from the
// left edge, and its baseline is row `base`.
pub struct Sprite { w: i32, h: i32, left: i32, base: i32, cov: u8[], rgba: u8[] }

pub struct Cache { glyphs: Map[i64, truetype.Bitmap], colors: Map[i64, u8[]], sprites: Map[string, Sprite] }

pub function new_cache(): Cache {
  return Cache { glyphs: map_new(512), colors: map_new(64), sprites: map_new(256) };
}

function no_sprite(): Sprite {
  return Sprite { w: 0, h: 0, left: 0, base: 0, cov: [], rgba: [] };
}

// The bitmap of glyph `g` of the font `w` at `px`, with its pen `sub`
// quarters of a pixel right of a pixel edge.
// A color glyph has the box of its colors and no coverage, and its colors
// come third: premultiplied RGBA. The entry of the text color in its
// palette draws black. Each other glyph has no colors.
function glyph(own c: Cache, fs: Fonts, w: i32, px: i32, g: i32, sub: i32): (Cache, truetype.Bitmap, u8[]) {
  let key = glyph_key(w, px, g, sub);
  if let Some(bm) = c.glyphs.get(key) {
    let rgba = c.colors.get(key).unwrap_or([]);
    return (c, bm, rgba);
  }
  let f = font(fs, w);
  if (f.has_colors(g)) {
    let cb = f.render_color(g, px as f64, sub as f64 / 4.0, BLACK);
    let bm = truetype.Bitmap { width: cb.width, height: cb.height, left: cb.left, top: cb.top, coverage: [] };
    return (Cache { ...c, glyphs: c.glyphs.insert(key, bm), colors: c.colors.insert(key, cb.rgba) }, bm, cb.rgba);
  }
  let bm = f.render(g, px as f64, sub as f64 / 4.0);
  return (Cache { ...c, glyphs: c.glyphs.insert(key, bm) }, bm, []);
}

// Opaque black, as an ARGB color.
const BLACK: i32 = -16777216;

// A glyph of a text: `key` names its bitmap at its size and its part of a
// pixel, and its bitmap has its left column `x` pixels right of the first
// pen. Its top row is `y` rows below the baseline. A color glyph has its
// colors in `rgba`, premultiplied, and no coverage in `bm`.
pub struct Placed { key: i64, bm: truetype.Bitmap, x: i32, y: i32, rgba: u8[] }

// The glyphs of `s` in weight `w` at `px`, with the letter spacing `track`,
// each on a quarter of a pixel.
pub function layout(own c: Cache, fs: Fonts, w: i32, px: i32, track: i32, s: string): (Cache, Placed[]) {
  if (!has(fs) || s.len() == 0) {
    return (c, []);
  }
  let p = pens(fs, w, px, track, s);
  let cache = c;
  let out: Placed[] = [];
  for i in 0..p.glyphs.len() {
    let q = (p.at[i] * 4.0 + 0.5).floor() as i32;
    let (next, bm, rgba) = glyph(cache, fs, p.ids[i], px, p.glyphs[i], q & 3);
    cache = next;
    out = out.append(Placed { key: glyph_key(p.ids[i], px, p.glyphs[i], q & 3), bm: bm, x: (q >> 2) + bm.left, y: bm.top + (p.ys[i] + 0.5).floor() as i32, rgba: rgba });
  }
  return (cache, out);
}

// The coverage of the glyphs of `placed` that have no colors, in a box of
// `sw` by `sh` pixels from (x0, y0).
function coverage_layer(placed: Placed[], x0: i32, y0: i32, sw: i32, sh: i32): u8[] {
  let cov: u8[] = [];
  for i in 0..sw * sh {
    cov = cov.append(0u8);
  }
  for pl in placed {
    let bm = pl.bm;
    let from_rows = if (pl.rgba.len() > 0) { 0 } else { bm.height };
    for row in 0..from_rows {
      let to = (pl.y - y0 + row) * sw + pl.x - x0;
      let from = row * bm.width;
      for col in 0..bm.width {
        let a = bm.coverage[from + col] as i32;
        if (a > 0) {
          let b = cov[to + col] as i32;
          cov = cov.with(to + col, (a + b - (a * b + 127) / 255) as u8);
        }
      }
    }
  }
  return cov;
}

// The colors of the color glyphs of `placed` in a box of `sw` by `sh`
// pixels from (x0, y0), premultiplied, one over another. It is empty when
// no glyph has colors.
function color_layer(placed: Placed[], x0: i32, y0: i32, sw: i32, sh: i32): u8[] {
  let out: u8[] = [];
  for pl in placed {
    if (pl.rgba.len() == 0) {
      continue;
    }
    if (out.len() == 0) {
      for i in 0..4 * sw * sh {
        out = out.append(0u8);
      }
    }
    for row in 0..pl.bm.height {
      for col in 0..pl.bm.width {
        let from = 4 * (row * pl.bm.width + col);
        let to = 4 * ((pl.y - y0 + row) * sw + pl.x - x0 + col);
        let keep = 255 - pl.rgba[from + 3] as i32;
        for k in 0..4 {
          out = out.with(to + k, (pl.rgba[from + k] as i32 + (out[to + k] as i32 * keep + 127) / 255).min(255) as u8);
        }
      }
    }
  }
  return out;
}

function glyph_key(w: i32, px: i32, g: i32, sub: i32): i64 {
  return (w as i64 * 4096i64 + px as i64) * 4i64 + sub as i64 << 32 | g as i64;
}

// The sprite of `s` in weight `w` at `px`, with the letter spacing `track`.
// Where two glyphs share a pixel, the second one goes over the first, as
// Android draws text one glyph at a time.
pub function sprite(own c: Cache, fs: Fonts, w: i32, px: i32, track: i32, s: string): (Cache, Sprite) {
  let key = w.to_string() + "|" + px.to_string() + "|" + track.to_string() + "|" + s;
  if let Some(sp) = c.sprites.get(key) {
    return (c, sp);
  }
  let (cache, placed) = layout(c, fs, w, px, track, s);
  let (x0, x1, y0, y1) = (1 << 20, -(1 << 20), 1 << 20, -(1 << 20));
  for pl in placed {
    if (pl.bm.width > 0) {
      x0 = min(x0, pl.x);
      x1 = max(x1, pl.x + pl.bm.width);
      y0 = min(y0, pl.y);
      y1 = max(y1, pl.y + pl.bm.height);
    }
  }
  if (x1 <= x0) {
    return (cache, no_sprite());
  }
  let sw = x1 - x0;
  let sh = y1 - y0;
  let sp = Sprite { w: sw, h: sh, left: -x0, base: -y0, cov: coverage_layer(placed, x0, y0, sw, sh), rgba: color_layer(placed, x0, y0, sw, sh) };
  return (Cache { ...cache, sprites: cache.sprites.insert(key, sp) }, sp);
}

// The soft shadow of `s`: its sprite blurred three times by a box of radius
// `r` along the rows and then the columns, which is close to the Gaussian
// blur of Android's text shadows.
pub function shadow(own c: Cache, fs: Fonts, w: i32, px: i32, s: string, r: i32): (Cache, Sprite) {
  let key = "shadow|" + r.to_string() + "|" + w.to_string() + "|" + px.to_string() + "|" + s;
  if let Some(sp) = c.sprites.get(key) {
    return (c, sp);
  }
  let (cache, text) = sprite(c, fs, w, px, 0, s);
  if (text.w == 0) {
    return (cache, text);
  }
  let pad = 3 * r + 1;
  let sw = text.w + 2 * pad;
  let sh = text.h + 2 * pad;
  let buf: i32[] = [];
  for i in 0..sw * sh {
    buf = buf.append(0);
  }
  for row in 0..text.h {
    for col in 0..text.w {
      buf = buf.with((row + pad) * sw + col + pad, text.cov[row * text.w + col] as i32);
    }
  }
  for pass in 0..3 {
    buf = box_blur(buf, sw, sh, r, true);
    buf = box_blur(buf, sw, sh, r, false);
  }
  let cov: u8[] = [];
  for v in buf {
    cov = cov.append(min(v, 255) as u8);
  }
  let sp = Sprite { w: sw, h: sh, left: text.left + pad, base: text.base + pad, cov: cov, rgba: [] };
  return (Cache { ...cache, sprites: cache.sprites.insert(key, sp) }, sp);
}

// A box blur of radius r along the rows, or along the columns.
function box_blur(src: i32[], w: i32, h: i32, r: i32, rows: boolean): i32[] {
  let out: i32[] = [];
  for i in 0..w * h {
    out = out.append(0);
  }
  let n = 2 * r + 1;
  let (lines, len, step, stride) = (h, w, 1, w);
  if (!rows) {
    lines = w;
    len = h;
    step = w;
    stride = 1;
  }
  for l in 0..lines {
    let start = l * stride;
    let sum = 0;
    for k in 0..min(r + 1, len) {
      sum += src[start + k * step];
    }
    for i in 0..len {
      out = out.with(start + i * step, sum / n);
      if (i + r + 1 < len) {
        sum += src[start + (i + r + 1) * step];
      }
      if (i - r >= 0) {
        sum -= src[start + (i - r) * step];
      }
    }
  }
  return out;
}

function min(a: i32, b: i32): i32 {
  if (a < b) {
    return a;
  }
  return b;
}

function max(a: i32, b: i32): i32 {
  if (a > b) {
    return a;
  }
  return b;
}
```

### `drawing`

```fern
<<drawing>>=
// Draws `sp` in `color` with its first pen at x and its baseline at y.
pub function put(fr: draw.Frame, sp: Sprite, x: i32, y: i32, color: i32): void {
  if (sp.w > 0) {
    fr.mask(x - sp.left, y - sp.base, sp.w, sp.h, sp.cov, color);
  }
  if (sp.rgba.len() > 0) {
    fr.blend(x - sp.left, y - sp.base, sp.w, sp.h, sp.rgba);
  }
}

// Draws `s` with its first pen at x and its baseline at y.
pub function text(own c: Cache, fr: draw.Frame, fs: Fonts, w: i32, px: i32, track: i32, x: i32, y: i32, s: string, color: i32): Cache {
  let (next, sp) = sprite(c, fs, w, px, track, s);
  put(fr, sp, x, y, color);
  return next;
}
```
