# Text for Android apps

This module measures text and draws it with the fonts of the device, for a NativeActivity app of `std/android`.

## Model

A `Fonts` holds one font for each weight that the app uses. `system` finds the files of a family on the device with `std/android/fonts`, so the APK holds no font. `load` makes a `Fonts` from files that the caller found, for example the files of a test on the host.

Each function takes a face: the index of a weight in the `Fonts`. `face_for` gives the index of the weight that is nearest to a number from 100 to 900.

`width` and `line` measure a text with no drawing. `fit` cuts a text to a width, and `wrap` breaks it into lines.

`std/truetype` draws each glyph one time for each size, weight and quarter of a pixel. A `Cache` keeps the glyphs. It also keeps a `Sprite` for each text: the coverage of the whole text, which `put` draws on a frame in a color. `layout` gives the glyphs of a text with their positions, for a painter that draws glyphs from an atlas on the GPU.

## Contracts

A size `px` is the pixels of one em. A letter spacing `track` is in thousandths of the em, as `letterSpacing` of Android. A `Fonts` with no font gives a width of 0 and draws nothing.

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
import "std/truetype";
import "std/utf8";
```

### `fonts`

```fern
<<fonts>>=
pub struct Fonts { faces: truetype.Font[], weights: i32[] }

// The fonts of the files in `found`, one for each weight of `ws`. `read`
// gives the bytes of a file. One file can hold more than one weight, so
// each file is read and parsed one time. A weight with no font takes the
// first font that there is.
pub function load(found: Option[fonts.Found][], ws: i32[], read: (string) => u8[]): Fonts {
  let paths: string[] = [];
  let parsed: Option[truetype.Font][] = [];
  let got: Option[truetype.Font][] = [];
  for i in 0..found.len().min(ws.len()) {
    let face: Option[truetype.Font] = None;
    if let Some(f) = found[i] {
      let at = place(paths, f.path);
      if (at < 0) {
        at = paths.len();
        paths = paths.append(f.path);
        parsed = parsed.append(truetype.parse(read(f.path)));
      }
      if let Some(font) = parsed[at] {
        face = Some(fonts.styled(font, f, ws[i]));
      }
    }
    got = got.append(face);
  }
  return Fonts { faces: filled(got), weights: ws };
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

function font(fs: Fonts, w: i32): truetype.Font {
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

// The glyphs of `s`, their pen positions in pixels, then the end of the

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
// text. `track` adds space between the characters, in thousandths of the
// em, as Android's letterSpacing does.
function pens(f: truetype.Font, px: i32, track: i32, s: string): (i32[], f64[]) {
  let glyphs: i32[] = [];
  let out: f64[] = [];
  let scale = px as f64 / (f.units_per_em as f64);
  let spacing = px as f64 * (track as f64) / 1000.0;
  let pen = 0.0;
  let prev = -1;
  for c in s.chars() {
    let g = f.glyph(c as i32);
    if (prev >= 0) {
      pen += f.kerning(prev, g) as f64 * scale;
    }
    glyphs = glyphs.append(g);
    out = out.append(pen);
    pen = pen + f.advance(g) as f64 * scale + spacing;
    prev = g;
  }
  return (glyphs, out.append(pen - spacing));
}

pub function width(fs: Fonts, w: i32, px: i32, s: string): i32 {
  return spaced_width(fs, w, px, 0, s);
}

pub function spaced_width(fs: Fonts, w: i32, px: i32, track: i32, s: string): i32 {
  if (!has(fs) || s.len() == 0) {
    return 0;
  }
  let (_, p) = pens(font(fs, w), px, track, s);
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
  let lines: string[] = [];
  for para in s.split("\n") {
    lines = wrap_paragraph(fs, w, px, para, room, lines);
  }
  return lines;
}

function wrap_paragraph(fs: Fonts, w: i32, px: i32, para: string, room: i32, own out: string[]): string[] {
  if (width(fs, w, px, para) <= room) {
    return out.append(para);
  }
  let lines = out;
  let line = "";
  for word in para.split(" ") {
    let joined = word;
    if (line.len() > 0) {
      joined = line + " " + word;
    }
    if (width(fs, w, px, joined) <= room) {
      line = joined;
      continue;
    }
    if (line.len() > 0) {
      lines = lines.append(line);
    }
    line = word;
    while (width(fs, w, px, line) > room) {
      let head = head_that_fits(fs, w, px, line, room);
      lines = lines.append(head);
      line = line.drop(head.len()).to_owned();
    }
  }
  return lines.append(line);
}

// The longest start of `word` that fits in `room` pixels: one character or
// more.
function head_that_fits(fs: Fonts, w: i32, px: i32, word: string, room: i32): string {
  let head = "";
  for c in word.chars() {
    let next = head + c.to_string();
    if (head.len() > 0 && width(fs, w, px, next) > room) {
      return head;
    }
    head = next;
  }
  return head;
}
```

### `sprites`

```fern
<<sprites>>=
// The coverage of a text, w by h. Its first pen is `left` pixels from the
// left edge, and its baseline is row `base`.
pub struct Sprite { w: i32, h: i32, left: i32, base: i32, cov: u8[] }

pub struct Cache { glyphs: Map[i64, truetype.Bitmap], sprites: Map[string, Sprite] }

pub function new_cache(): Cache {
  return Cache { glyphs: map_new(512), sprites: map_new(256) };
}

function no_sprite(): Sprite {
  return Sprite { w: 0, h: 0, left: 0, base: 0, cov: [] };
}

// The bitmap of glyph `g` of weight `w` at `px`, with its pen `sub`
// quarters of a pixel right of a pixel edge.
function glyph(own c: Cache, fs: Fonts, w: i32, px: i32, g: i32, sub: i32): (Cache, truetype.Bitmap) {
  let key = glyph_key(w, px, g, sub);
  if let Some(bm) = c.glyphs.get(key) {
    return (c, bm);
  }
  let bm = font(fs, w).render(g, px as f64, sub as f64 / 4.0);
  return (Cache { glyphs: c.glyphs.insert(key, bm), sprites: c.sprites }, bm);
}

// A glyph of a text: `key` names its bitmap at its size and its part of a
// pixel, and its bitmap has its left column `x` pixels right of the first
// pen. Its top row is `bm.top` rows below the baseline.
pub struct Placed { key: i64, bm: truetype.Bitmap, x: i32 }

// The glyphs of `s` in weight `w` at `px`, with the letter spacing `track`,
// each on a quarter of a pixel.
pub function layout(own c: Cache, fs: Fonts, w: i32, px: i32, track: i32, s: string): (Cache, Placed[]) {
  if (!has(fs) || s.len() == 0) {
    return (c, []);
  }
  let (glyphs, p) = pens(font(fs, w), px, track, s);
  let cache = c;
  let out: Placed[] = [];
  for i in 0..glyphs.len() {
    let q = (p[i] * 4.0 + 0.5).floor() as i32;
    let (next, bm) = glyph(cache, fs, w, px, glyphs[i], q & 3);
    cache = next;
    out = out.append(Placed { key: glyph_key(w, px, glyphs[i], q & 3), bm: bm, x: (q >> 2) + bm.left });
  }
  return (cache, out);
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
      y0 = min(y0, pl.bm.top);
      y1 = max(y1, pl.bm.top + pl.bm.height);
    }
  }
  if (x1 <= x0) {
    return (cache, no_sprite());
  }
  let sw = x1 - x0;
  let sh = y1 - y0;
  let cov: u8[] = [];
  for i in 0..sw * sh {
    cov = cov.append(0u8);
  }
  for pl in placed {
    let bm = pl.bm;
    for row in 0..bm.height {
      let to = (bm.top - y0 + row) * sw + pl.x - x0;
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
  let sp = Sprite { w: sw, h: sh, left: -x0, base: -y0, cov: cov };
  return (Cache { glyphs: cache.glyphs, sprites: cache.sprites.insert(key, sp) }, sp);
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
  let sp = Sprite { w: sw, h: sh, left: text.left + pad, base: text.base + pad, cov: cov };
  return (Cache { glyphs: cache.glyphs, sprites: cache.sprites.insert(key, sp) }, sp);
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
}

// Draws `s` with its first pen at x and its baseline at y.
pub function text(own c: Cache, fr: draw.Frame, fs: Fonts, w: i32, px: i32, track: i32, x: i32, y: i32, s: string, color: i32): Cache {
  let (next, sp) = sprite(c, fs, w, px, track, s);
  put(fr, sp, x, y, color);
  return next;
}
```
