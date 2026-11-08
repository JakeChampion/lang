# TrueType fonts

This module reads TrueType fonts and draws their glyphs as coverage maps.

## Model

`parse` reads the tables of a font: the character map, the horizontal metrics, the outlines and the kerning of the GPOS table. `glyph` gives the glyph of a code point, and `advance` and `kerning` give the space between glyphs in font units. `render` scales the outline of a glyph to a size in pixels and gives the part of each pixel that the outline covers.

The rasterizer adds the signed area that each line of the outline covers to the cells of a buffer. The running sum of the buffer is then the coverage of each pixel. Curves become lines first. The rasterizer uses the nonzero rule, with the coverage limited to one.

## Contracts

`parse` accepts a font with `glyf` outlines and a Unicode character map of format 4 or 12. It gives None for other fonts. A code point that the font does not map gives glyph 0. The kerning comes from the pair adjustment lookups of the `kern` feature, both formats. Composite glyphs that join their parts at points, not at offsets, draw their parts with no offset. The hinting instructions of the font have no effect.

## Executable example

```fern test name=std-truetype-example
import "std/truetype";

function main(): i32 {
  if let Some(_) = truetype.parse([0 as u8, 1 as u8, 0 as u8, 0 as u8]) {
    return 1;
  }
  return 0;
}
```

## Module assembly

The root chunk keeps the order of the declarations.

```fern
<<*>>=
<<module introduction>>

<<declarations>>

<<kerning>>

<<outlines>>

<<the rasterizer>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads TrueType fonts and draws their glyphs as coverage maps.
import "std/f64";

// A font in the bytes `data`, with the offsets of its tables. `cmap` is the
// offset of the character map subtable, of format `cmap_format`. `pairs`
// are the offsets of the GPOS pair adjustment subtables of the kern feature.
```

### `declarations`

```fern
<<declarations>>=
pub struct Font {
  data: u8[],
  cmap: i32,
  cmap_format: i32,
  loca: i32,
  glyf: i32,
  hmtx: i32,
  hmetrics: i32,
  glyphs: i32,
  long_loca: boolean,
  units_per_em: i32,
  ascender: i32,
  descender: i32,
  line_gap: i32,
  pairs: i32[],
}

// The coverage of a glyph, row by row, from 0 for none to 255 for all of a
// pixel. `left` and `top` place the first pixel from the pen on the
// baseline, with y down: `top` is negative above the baseline.
pub struct Bitmap { width: i32, height: i32, left: i32, top: i32, coverage: u8[] }

function u8_at(b: [u8], at: i32): i32 {
  if (at < 0 || at >= b.len()) {
    return 0;
  }
  return b[at] as i32;
}

function u16_at(b: [u8], at: i32): i32 {
  return u8_at(b, at) << 8 | u8_at(b, at + 1);
}

function i16_at(b: [u8], at: i32): i32 {
  let v = u16_at(b, at);
  if (v >= 32768) {
    return v - 65536;
  }
  return v;
}

function u32_at(b: [u8], at: i32): i32 {
  return u16_at(b, at) << 16 | u16_at(b, at + 2);
}

// The offset of the table `tag`, or -1.
function table(b: [u8], tag: string): i32 {
  let n = u16_at(b, 4);
  for i in 0..n {
    let r = 12 + 16 * i;
    if (b.len() >= r + 16 && b[r] == tag[0] && b[r + 1] == tag[1] && b[r + 2] == tag[2] && b[r + 3] == tag[3]) {
      return u32_at(b, r + 8);
    }
  }
  return -1;
}

// The font in `data`, or None when it is not a TrueType font with outlines.
pub function parse(data: [u8]): Option[Font] {
  let b: u8[] = [];
  for x in data {
    b = b.append(x);
  }
  let version = u32_at(b, 0);
  if (b.len() < 12 || version != 65536 && version != 1953658213) {
    return None;
  }
  let Some(t) = tables(b, ["head", "hhea", "maxp", "cmap", "loca", "glyf", "hmtx"]) else {
    return None;
  };
  let (head, hhea, maxp) = (t[0], t[1], t[2]);
  let (sub, format) = cmap_subtable(b, t[3]);
  let upem = u16_at(b, head + 18);
  if (sub < 0 || upem <= 0) {
    return None;
  }
  let pairs = kern_subtables(b, table(b, "GPOS"));
  return Some(Font { data: b, cmap: sub, cmap_format: format, loca: t[4], glyf: t[5], hmtx: t[6], hmetrics: u16_at(b, hhea + 34), glyphs: u16_at(b, maxp + 4), long_loca: i16_at(b, head + 50) != 0, units_per_em: upem, ascender: i16_at(b, hhea + 4), descender: i16_at(b, hhea + 6), line_gap: i16_at(b, hhea + 8), pairs: pairs });
}

// The offsets of the tables `tags`, or None when one is missing.
function tables(b: [u8], tags: string[]): Option[i32[]] {
  let out: i32[] = [];
  for tag in tags {
    let at = table(b, tag);
    if (at < 0) {
      return None;
    }
    out = out.append(at);
  }
  return Some(out);
}

// The Unicode subtable of the character map: format 12 for all of Unicode,
// else format 4 for the Basic Multilingual Plane.
function cmap_subtable(b: [u8], cmap: i32): (i32, i32) {
  let n = u16_at(b, cmap + 2);
  let best = -1;
  let best_format = 0;
  for i in 0..n {
    let r = cmap + 4 + 8 * i;
    let platform = u16_at(b, r);
    let encoding = u16_at(b, r + 2);
    let at = cmap + u32_at(b, r + 4);
    let format = u16_at(b, at);
    let unicode = platform == 0 || platform == 3 && (encoding == 1 || encoding == 10);
    if (unicode && format == 12) {
      return (at, 12);
    }
    if (unicode && format == 4 && best < 0) {
      best = at;
      best_format = 4;
    }
  }
  return (best, best_format);
}

// The glyph of the code point `cp`, or 0, the glyph of a missing character.
pub function (f: Font) glyph(cp: i32): i32 {
  if (f.cmap_format == 12) {
    return groups_glyph(f.data, f.cmap, cp);
  }
  if (cp > 65535) {
    return 0;
  }
  return segments_glyph(f.data, f.cmap, cp);
}

// The glyph of `cp` in the groups of a format 12 cmap subtable at `t`.
function groups_glyph(b: u8[], t: i32, cp: i32): i32 {
  let lo = 0;
  let hi = u32_at(b, t + 12) - 1;
  while (lo <= hi) {
    let mid = (lo + hi) / 2;
    let g = t + 16 + 12 * mid;
    if (cp < u32_at(b, g)) {
      hi = mid - 1;
    } else if (cp > u32_at(b, g + 4)) {
      lo = mid + 1;
    } else {
      return u32_at(b, g + 8) + cp - u32_at(b, g);
    }
  }
  return 0;
}

// The glyph of `cp` in the segments of a format 4 cmap subtable at `t`.
function segments_glyph(b: u8[], t: i32, cp: i32): i32 {
  let segs = u16_at(b, t + 6) / 2;
  let ends = t + 14;
  let starts = ends + 2 * segs + 2;
  let deltas = starts + 2 * segs;
  let ranges = deltas + 2 * segs;
  let lo = 0;
  let hi = segs - 1;
  while (lo <= hi) {
    let mid = (lo + hi) / 2;
    if (u16_at(b, ends + 2 * mid) < cp) {
      lo = mid + 1;
    } else {
      hi = mid - 1;
    }
  }
  if (lo >= segs) {
    return 0;
  }
  let start = u16_at(b, starts + 2 * lo);
  if (cp < start) {
    return 0;
  }
  let delta = u16_at(b, deltas + 2 * lo);
  let range = u16_at(b, ranges + 2 * lo);
  if (range == 0) {
    return cp + delta & 65535;
  }
  let g = u16_at(b, ranges + 2 * lo + range + 2 * (cp - start));
  if (g == 0) {
    return 0;
  }
  return g + delta & 65535;
}

// The advance of glyph `g` in font units.
pub function (f: Font) advance(g: i32): i32 {
  let i = g;
  if (i >= f.hmetrics) {
    i = f.hmetrics - 1;
  }
  return u16_at(f.data, f.hmtx + 4 * i);
}
```

### `kerning`

```fern
<<kerning>>=
// The pair adjustment subtables of the lookups of the "kern" feature.
function kern_subtables(b: [u8], gpos: i32): i32[] {
  let out: i32[] = [];
  if (gpos < 0) {
    return out;
  }
  let lookups = gpos + u16_at(b, gpos + 8);
  for li in kern_lookups(b, gpos + u16_at(b, gpos + 6)) {
    for st in pair_subtables(b, lookups + u16_at(b, lookups + 2 + 2 * li)) {
      out = out.append(st);
    }
  }
  return out;
}

// The indices of the lookups of each "kern" feature, each once.
function kern_lookups(b: [u8], features: i32): i32[] {
  let out: i32[] = [];
  for i in 0..u16_at(b, features) {
    let r = features + 2 + 6 * i;
    if (b[r] != 107u8 || b[r + 1] != 101u8 || b[r + 2] != 114u8 || b[r + 3] != 110u8) {
      continue;
    }
    let ft = features + u16_at(b, r + 4);
    for k in 0..u16_at(b, ft + 2) {
      let li = u16_at(b, ft + 4 + 2 * k);
      if (!contains(out, li)) {
        out = out.append(li);
      }
    }
  }
  return out;
}

function contains(xs: i32[], v: i32): boolean {
  for x in xs {
    if (x == v) {
      return true;
    }
  }
  return false;
}

// The pair adjustment subtables of the lookup at `lk`: of type 2, or of
// type 9, an extension that holds a subtable of type 2.
function pair_subtables(b: [u8], lk: i32): i32[] {
  let out: i32[] = [];
  let kind = u16_at(b, lk);
  for s in 0..u16_at(b, lk + 4) {
    let st = lk + u16_at(b, lk + 6 + 2 * s);
    if (kind == 9 && u16_at(b, st + 2) == 2) {
      out = out.append(st + u32_at(b, st + 4));
    } else if (kind == 2) {
      out = out.append(st);
    }
  }
  return out;
}

// The index of glyph `g` in a coverage table, or -1.
function coverage(b: [u8], at: i32, g: i32): i32 {
  let format = u16_at(b, at);
  let n = u16_at(b, at + 2);
  let lo = 0;
  let hi = n - 1;
  while (lo <= hi) {
    let mid = (lo + hi) / 2;
    if (format == 1) {
      let v = u16_at(b, at + 4 + 2 * mid);
      if (v == g) {
        return mid;
      }
      if (v < g) {
        lo = mid + 1;
      } else {
        hi = mid - 1;
      }
    } else {
      let r = at + 4 + 6 * mid;
      if (g < u16_at(b, r)) {
        hi = mid - 1;
      } else if (g > u16_at(b, r + 2)) {
        lo = mid + 1;
      } else {
        return u16_at(b, r + 4) + g - u16_at(b, r);
      }
    }
  }
  return -1;
}

// The class of glyph `g` in a class definition table.
function class_of(b: [u8], at: i32, g: i32): i32 {
  let format = u16_at(b, at);
  if (format == 1) {
    let first = u16_at(b, at + 2);
    if (g >= first && g < first + u16_at(b, at + 4)) {
      return u16_at(b, at + 6 + 2 * (g - first));
    }
    return 0;
  }
  let lo = 0;
  let hi = u16_at(b, at + 2) - 1;
  while (lo <= hi) {
    let mid = (lo + hi) / 2;
    let r = at + 4 + 6 * mid;
    if (g < u16_at(b, r)) {
      hi = mid - 1;
    } else if (g > u16_at(b, r + 2)) {
      lo = mid + 1;
    } else {
      return u16_at(b, r + 4);
    }
  }
  return 0;
}

// The bytes of a value record of `format`: two for each bit.
function value_size(format: i32): i32 {
  let n = 0;
  for i in 0..8 {
    if ((format >> i & 1) != 0) {
      n += 2;
    }
  }
  return n;
}

// The x advance of a value record at `at`, or 0.
function x_advance(b: [u8], at: i32, format: i32): i32 {
  if ((format & 4) == 0) {
    return 0;
  }
  return i16_at(b, at + value_size(format & 3));
}

// The kerning between glyphs `a` and `b`, in font units.
pub function (f: Font) kerning(a: i32, g2: i32): i32 {
  let b = f.data;
  for st in f.pairs {
    let format = u16_at(b, st);
    let i = coverage(b, st + u16_at(b, st + 2), a);
    if (i < 0) {
      continue;
    }
    let vf1 = u16_at(b, st + 4);
    let vf2 = u16_at(b, st + 6);
    let s1 = value_size(vf1);
    let s2 = value_size(vf2);
    if (format == 1) {
      let set = st + u16_at(b, st + 10 + 2 * i);
      let lo = 0;
      let hi = u16_at(b, set) - 1;
      let rec = 2 + s1 + s2;
      while (lo <= hi) {
        let mid = (lo + hi) / 2;
        let r = set + 2 + rec * mid;
        let second = u16_at(b, r);
        if (second == g2) {
          return x_advance(b, r + 2, vf1);
        }
        if (second < g2) {
          lo = mid + 1;
        } else {
          hi = mid - 1;
        }
      }
    } else if (format == 2) {
      let c1 = class_of(b, st + u16_at(b, st + 8), a);
      let c2 = class_of(b, st + u16_at(b, st + 10), g2);
      let n2 = u16_at(b, st + 14);
      let r = st + 16 + (c1 * n2 + c2) * (s1 + s2);
      let k = x_advance(b, r, vf1);
      if (k != 0) {
        return k;
      }
    }
  }
  return 0;
}
```

### `outlines`

```fern
<<outlines>>=
// A point of an outline, in font units, on the curve or a control point.
struct Point { x: f64, y: f64, on: boolean }

// The contours of glyph `g`, each a list of points. `depth` stops composite
// glyphs that refer to themselves.
function contours(f: Font, g: i32, depth: i32): Point[][] {
  let out: Point[][] = [];
  if (g < 0 || g >= f.glyphs || depth > 8) {
    return out;
  }
  let b = f.data;
  let start = 0;
  let end = 0;
  if (f.long_loca) {
    start = u32_at(b, f.loca + 4 * g);
    end = u32_at(b, f.loca + 4 * g + 4);
  } else {
    start = 2 * u16_at(b, f.loca + 2 * g);
    end = 2 * u16_at(b, f.loca + 2 * g + 2);
  }
  if (end <= start) {
    return out;
  }
  let at = f.glyf + start;
  let n = i16_at(b, at);
  if (n >= 0) {
    return simple(b, at, n);
  }
  return composite(f, at + 10, depth);
}

function simple(b: [u8], at: i32, n: i32): Point[][] {
  let ends: i32[] = [];
  for i in 0..n {
    ends = ends.append(u16_at(b, at + 10 + 2 * i));
  }
  if (n == 0) {
    return [];
  }
  let count = ends[n - 1] + 1;
  let p = at + 10 + 2 * n;
  p = p + 2 + u16_at(b, p);
  let (flags, p1) = read_flags(b, p, count);
  let (xs, p2) = read_coords(b, p1, flags, 2, 16);
  let (ys, _) = read_coords(b, p2, flags, 4, 32);
  let pts: Point[] = [];
  for i in 0..count {
    pts = pts.append(Point { x: xs[i] as f64, y: ys[i] as f64, on: (flags[i] & 1) != 0 });
  }
  return split_contours(pts, ends);
}

// The flags of `count` points from `p`, with their repeats, and the offset
// after them.
function read_flags(b: [u8], at: i32, count: i32): (i32[], i32) {
  let flags: i32[] = [];
  let p = at;
  while (flags.len() < count) {
    let fl = u8_at(b, p);
    p += 1;
    flags = flags.append(fl);
    if ((fl & 8) != 0) {
      let rep = u8_at(b, p);
      p += 1;
      for r in 0..rep {
        flags = flags.append(fl);
      }
    }
  }
  return (flags, p);
}

// The x or the y coordinates of the points, and the offset after them. The
// flag bit `short` marks one byte, and `same` marks a positive byte, or no
// change for a coordinate of two bytes.
function read_coords(b: [u8], at: i32, flags: i32[], short: i32, same: i32): (i32[], i32) {
  let out: i32[] = [];
  let v = 0;
  let p = at;
  for i in 0..flags.len() {
    let fl = flags[i];
    if ((fl & short) != 0) {
      let d = u8_at(b, p);
      p += 1;
      if ((fl & same) == 0) {
        d = 0 - d;
      }
      v += d;
    } else if ((fl & same) == 0) {
      v += i16_at(b, p);
      p += 2;
    }
    out = out.append(v);
  }
  return (out, p);
}

// The points split into contours at the last point of each.
function split_contours(pts: Point[], ends: i32[]): Point[][] {
  let out: Point[][] = [];
  let first = 0;
  for e in ends {
    let c: Point[] = [];
    for i in first..e + 1 {
      if (i < pts.len()) {
        c = c.append(pts[i]);
      }
    }
    out = out.append(c);
    first = e + 1;
  }
  return out;
}

// A composite glyph: the contours of its parts, each moved and scaled.
function composite(f: Font, at0: i32, depth: i32): Point[][] {
  let b = f.data;
  let out: Point[][] = [];
  let at = at0;
  let more = true;
  while (more) {
    let flags = u16_at(b, at);
    let g = u16_at(b, at + 2);
    at += 4;
    let dx = 0.0;
    let dy = 0.0;
    if ((flags & 1) != 0) {
      dx = i16_at(b, at) as f64;
      dy = i16_at(b, at + 2) as f64;
      at += 4;
    } else {
      dx = (u8_at(b, at) << 24 >> 24) as f64;
      dy = (u8_at(b, at + 1) << 24 >> 24) as f64;
      at += 2;
    }
    if ((flags & 2) == 0) {
      // The parts join at points, not at offsets: not supported.
      dx = 0.0;
      dy = 0.0;
    }
    let (a, bb, c, d) = (1.0, 0.0, 0.0, 1.0);
    if ((flags & 8) != 0) {
      a = f2dot14(b, at);
      d = a;
      at += 2;
    } else if ((flags & 64) != 0) {
      a = f2dot14(b, at);
      d = f2dot14(b, at + 2);
      at += 4;
    } else if ((flags & 128) != 0) {
      a = f2dot14(b, at);
      bb = f2dot14(b, at + 2);
      c = f2dot14(b, at + 4);
      d = f2dot14(b, at + 6);
      at += 8;
    }
    for part in contours(f, g, depth + 1) {
      let moved: Point[] = [];
      for p in part {
        moved = moved.append(Point { x: a * p.x + c * p.y + dx, y: bb * p.x + d * p.y + dy, on: p.on });
      }
      out = out.append(moved);
    }
    more = (flags & 32) != 0;
  }
  return out;
}

function f2dot14(b: [u8], at: i32): f64 {
  return i16_at(b, at) as f64 / 16384.0;
}
```

### `the rasterizer`

```fern
<<the rasterizer>>=
// The accumulation buffer of the rasterizer: each line of an outline adds
// the signed area it covers to the cells it crosses, and the running sum of
// the buffer is then the coverage of each pixel.
struct Raster { w: i32, h: i32, acc: f64[] }

function line(r: Raster, x0: f64, y0: f64, x1: f64, y1: f64): Raster {
  if (y0 == y1) {
    return r;
  }
  let dir = 1.0;
  let (ax, ay, bx, by) = (x0, y0, x1, y1);
  if (y0 > y1) {
    dir = -1.0;
    ax = x1;
    ay = y1;
    bx = x0;
    by = y0;
  }
  let dxdy = (bx - ax) / (by - ay);
  let x = ax;
  if (ay < 0.0) {
    x = x - ay * dxdy;
  }
  let acc = r.acc;
  let ystart = clampi(ay.floor() as i32, 0, r.h);
  let yend = clampi(by.ceil() as i32, 0, r.h);
  for y in ystart..yend {
    let top = y as f64;
    if (ay > top) {
      top = ay;
    }
    let bottom = (y + 1) as f64;
    if (by < bottom) {
      bottom = by;
    }
    let dy = bottom - top;
    let xnext = x + dxdy * dy;
    acc = row_cells(acc, y * r.w, x, xnext, dy * dir);
    x = xnext;
  }
  return Raster { w: r.w, h: r.h, acc: acc };
}

function clampi(v: i32, lo: i32, hi: i32): i32 {
  if (v < lo) {
    return lo;
  }
  if (v > hi) {
    return hi;
  }
  return v;
}

// Adds the area of the part of a line in one row, from x to xnext and of
// height and direction d, to the cells of the row that starts at `row`.
function row_cells(own acc: f64[], row: i32, x: f64, xnext: f64, d: f64): f64[] {
  let (xa, xb) = (x, xnext);
  if (xnext < x) {
    xa = xnext;
    xb = x;
  }
  let xaf = xa.floor();
  let xai = xaf as i32;
  let xbi = xb.ceil() as i32;
  if (xai < 0) {
    xai = 0;
  }
  let out = acc;
  if (xbi <= xai + 1) {
    let xmf = 0.5 * (x + xnext) - xaf;
    out = add(out, row + xai, d - d * xmf);
    return add(out, row + xai + 1, d * xmf);
  }
  let s = 1.0 / (xb - xa);
  let x0f = xa - xaf;
  let a0 = 0.5 * s * (1.0 - x0f) * (1.0 - x0f);
  let x1f = xb - xb.ceil() + 1.0;
  let am = 0.5 * s * x1f * x1f;
  out = add(out, row + xai, d * a0);
  if (xbi == xai + 2) {
    out = add(out, row + xai + 1, d * (1.0 - a0 - am));
  } else {
    let a1 = s * (1.5 - x0f);
    out = add(out, row + xai + 1, d * (a1 - a0));
    for xi in xai + 2..xbi - 1 {
      out = add(out, row + xi, d * s);
    }
    let a2 = a1 + (xbi - xai - 3) as f64 * s;
    out = add(out, row + xbi - 1, d * (1.0 - a2 - am));
  }
  return add(out, row + xbi, d * am);
}

function add(own acc: f64[], i: i32, v: f64): f64[] {
  if (i < 0 || i >= acc.len()) {
    return acc;
  }
  let old = acc[i];
  return acc.with(i, old + v);
}

// A quadratic curve as lines, about one for each two pixels of its length.
function curve(r: Raster, x0: f64, y0: f64, cx: f64, cy: f64, x1: f64, y1: f64): Raster {
  let ddx = x0 - 2.0 * cx + x1;
  let ddy = y0 - 2.0 * cy + y1;
  let dev = (ddx * ddx + ddy * ddy).sqrt();
  let n = 1;
  if (dev > 0.333) {
    n = 1 + (3.0 * dev).sqrt() as i32;
  }
  let out = r;
  let (px, py) = (x0, y0);
  for i in 1..n + 1 {
    let t = i as f64 / (n as f64);
    let u = 1.0 - t;
    let qx = u * u * x0 + 2.0 * u * t * cx + t * t * x1;
    let qy = u * u * y0 + 2.0 * u * t * cy + t * t * y1;
    out = line(out, px, py, qx, qy);
    px = qx;
    py = qy;
  }
  return out;
}

// The coverage of glyph `g` at `px` pixels per em, with the pen `dx` of a
// pixel to the right of a pixel edge.
pub function (f: Font) render(g: i32, px: f64, dx: f64): Bitmap {
  let cs = contours(f, g, 0);
  let scale = px / (f.units_per_em as f64);
  let (minx, miny, maxx, maxy) = bounds(cs, scale, dx);
  if (maxx <= minx || maxy <= miny) {
    return Bitmap { width: 0, height: 0, left: 0, top: 0, coverage: [] };
  }
  let left = minx.floor() as i32;
  let top = miny.floor() as i32;
  let w = maxx.ceil() as i32 - left + 1;
  let h = maxy.ceil() as i32 - top + 1;
  let acc: f64[] = [];
  for i in 0..w * h + 4 {
    acc = acc.append(0.0);
  }
  let r = Raster { w: w, h: h, acc: acc };
  let ox = dx - left as f64;
  let oy = 0.0 - top as f64;
  for c in cs {
    r = outline(r, c, scale, ox, oy);
  }
  return Bitmap { width: w, height: h, left: left, top: top, coverage: shade(r) };
}

// The pixel edges of the contours `cs` at `scale`, moved `dx` to the
// right, with y down: the least x and y, then the greatest.
function bounds(cs: Point[][], scale: f64, dx: f64): (f64, f64, f64, f64) {
  let minx = 1.0e9;
  let miny = 1.0e9;
  let maxx = -1.0e9;
  let maxy = -1.0e9;
  for c in cs {
    for p in c {
      let x = p.x * scale + dx;
      let y = 0.0 - p.y * scale;
      minx = minx.min(x);
      maxx = maxx.max(x);
      miny = miny.min(y);
      maxy = maxy.max(y);
    }
  }
  return (minx, miny, maxx, maxy);
}

// The coverage of each pixel: the running sum of the accumulated area,
// with its size held to 1.
function shade(r: Raster): u8[] {
  let cov: u8[] = [];
  let sum = 0.0;
  for i in 0..r.w * r.h {
    sum = sum + r.acc[i];
    let a = sum.abs().min(1.0);
    cov = cov.append((a * 255.0 + 0.5) as u8);
  }
  return cov;
}

// Adds one contour: its lines and its curves, with the on-curve point
// between two control points that TrueType leaves out.
function outline(r: Raster, c: Point[], scale: f64, ox: f64, oy: f64): Raster {
  let n = c.len();
  if (n < 2) {
    return r;
  }
  let pts: Point[] = [];
  for p in c {
    pts = pts.append(Point { x: p.x * scale + ox, y: oy - p.y * scale, on: p.on });
  }
  let (first, sx, sy) = start_point(pts);
  let out = r;
  let (cx, cy) = (sx, sy);
  let control = false;
  let (qx, qy) = (0.0, 0.0);
  for k in 1..n + 1 {
    let p = pts[(first + k) % n];
    if (p.on && control) {
      out = curve(out, cx, cy, qx, qy, p.x, p.y);
    } else if (p.on) {
      out = line(out, cx, cy, p.x, p.y);
    } else if (control) {
      let mx = (qx + p.x) / 2.0;
      let my = (qy + p.y) / 2.0;
      out = curve(out, cx, cy, qx, qy, mx, my);
      cx = mx;
      cy = my;
    }
    if (p.on) {
      cx = p.x;
      cy = p.y;
    } else {
      qx = p.x;
      qy = p.y;
    }
    control = !p.on;
  }
  if (control) {
    return curve(out, cx, cy, qx, qy, sx, sy);
  }
  return line(out, cx, cy, sx, sy);
}

// The point where a contour starts: its first point on the curve, or the
// middle of its first two control points when it has none.
function start_point(pts: Point[]): (i32, f64, f64) {
  for i in 0..pts.len() {
    if (pts[i].on) {
      return (i, pts[i].x, pts[i].y);
    }
  }
  return (0, (pts[0].x + pts[1].x) / 2.0, (pts[0].y + pts[1].y) / 2.0);
}
```
