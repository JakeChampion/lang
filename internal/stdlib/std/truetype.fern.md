# TrueType fonts

This module reads TrueType fonts and draws their glyphs as coverage maps.

## Model

`parse` reads the tables of a font: the character map, the horizontal metrics, the outlines and the kerning of the GPOS table. A variable font holds one set of outlines and the changes that each axis, such as the weight, makes to them. `at_axis` gives the font at a value on one axis. `glyph` gives the glyph of a code point, and `advance` and `kerning` give the space between glyphs in font units. `render` scales the outline of a glyph to a size in pixels and gives the part of each pixel that the outline covers.

The rasterizer adds the signed area that each line of the outline covers to the cells of a buffer. The running sum of the buffer is then the coverage of each pixel. Curves become lines first, quadratic curves of TrueType and cubic curves of CFF alike. The rasterizer uses the nonzero rule, with the coverage limited to one.

## Contracts

`parse` accepts a font with `glyf` outlines, a `CFF` table or a `CFF2` table, and a Unicode character map of format 4 or 12. It gives None for other fonts. `parse_index` takes one font of a collection file, `.ttc`, by its index.

A CFF table can use CIDs, with a font dict for each group of glyphs. The charstrings are of Type 2. They draw cubic curves, and their hints have no effect. In a variable CFF2 font, `blend` takes the place of the font on each axis that `at_axis` sets.

A code point that the font does not map gives glyph 0. The kerning comes from the pair adjustment lookups of the `kern` feature, both formats. Composite glyphs that join their parts at points, not at offsets, draw their parts with no offset. The hinting instructions of the font have no effect.

`at_axis` moves the outlines and the advances of a variable font, from the `gvar` and `HVAR` tables. It limits the value to the range of the axis. The kerning and the line metrics stay those of the default design. A font with no such axis stays as it is.

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

<<variations>>

<<outlines>>

<<cff outlines>>

<<the rasterizer>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads TrueType fonts and draws their glyphs as coverage maps.
import "std/array";
import "std/f64";

// A font in the bytes `data`, with the offsets of its tables. `cmap` is the
// offset of the character map subtable, of format `cmap_format`. `pairs`
// are the offsets of the GPOS pair adjustment subtables of the kern feature.
// `fvar`, `avar`, `gvar` and `hvar` are the offsets of the tables of a
// variable font, or -1. `coords` holds the place of the font on each axis,
// from -1 to 1, and is empty at the default design.
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
  fvar: i32,
  avar: i32,
  gvar: i32,
  hvar: i32,
  coords: f64[],
  cff: Cff,
}

// The parts of a CFF or a CFF2 table: the offsets of the INDEX of the
// charstrings and of the global subroutines, the offset of the local
// subroutines of each font dict, and the offset of FDSelect.
// `charstrings` is -1 for a font with `glyf` outlines, and `fdselect` is
// -1 for a font with one dict. The count of an INDEX has `count_bytes`
// bytes: 2 in CFF and 4 in CFF2. A CFF2 table can have the offset of an
// item variation store, `vstore`, and the first `vsindex` of each dict.
pub struct Cff { charstrings: i32, gsubrs: i32, subrs: i32[], fdselect: i32, count_bytes: i32, vstore: i32, vsindex: i32[] }

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

// The offset of the table `tag` of the font whose table directory is at
// `base`, or -1.
function table(b: [u8], base: i32, tag: string): i32 {
  let n = u16_at(b, base + 4);
  for i in 0..n {
    let r = base + 12 + 16 * i;
    if (b.len() >= r + 16 && b[r] == tag[0] && b[r + 1] == tag[1] && b[r + 2] == tag[2] && b[r + 3] == tag[3]) {
      return u32_at(b, r + 8);
    }
  }
  return -1;
}

// The font in `data`, or None when it is not a font with outlines. For a
// collection, it is the first font.
pub function parse(data: u8[]): Option[Font] {
  return parse_index(data, 0);
}

// The font number `index` of the collection in `data`, from 0. A file that
// is not a collection holds the font 0 alone. The font keeps `data`.
pub function parse_index(data: u8[], index: i32): Option[Font] {
  let b = data;
  let base = font_base(b, index);
  if (base < 0 || b.len() < base + 12 || !known_version(u32_at(b, base))) {
    return None;
  }
  let Some(t) = tables(b, base, ["head", "hhea", "maxp", "cmap", "hmtx"]) else {
    return None;
  };
  let (head, hhea, maxp) = (t[0], t[1], t[2]);
  let (sub, format) = cmap_subtable(b, t[3]);
  let upem = u16_at(b, head + 18);
  let glyphs = u16_at(b, maxp + 4);
  let cff = cff_of(b, table(b, base, "CFF "), glyphs);
  if (cff.charstrings < 0) {
    cff = cff2_of(b, table(b, base, "CFF2"), glyphs);
  }
  let loca = table(b, base, "loca");
  let glyf = table(b, base, "glyf");
  if (sub < 0 || upem <= 0 || cff.charstrings < 0 && (loca < 0 || glyf < 0)) {
    return None;
  }
  let pairs = kern_subtables(b, table(b, base, "GPOS"));
  return Some(Font { data: b, cmap: sub, cmap_format: format, loca: loca, glyf: glyf, hmtx: t[4], hmetrics: u16_at(b, hhea + 34), glyphs: glyphs, long_loca: i16_at(b, head + 50) != 0, units_per_em: upem, ascender: i16_at(b, hhea + 4), descender: i16_at(b, hhea + 6), line_gap: i16_at(b, hhea + 8), pairs: pairs, fvar: table(b, base, "fvar"), avar: table(b, base, "avar"), gvar: table(b, base, "gvar"), hvar: table(b, base, "HVAR"), coords: [], cff: cff });
}

// The offset of the table directory of the font `index` of a collection,
// or of a font file alone, or -1 when there is no such font.
function font_base(b: [u8], index: i32): i32 {
  if (u32_at(b, 0) != TTCF) {
    return if (index == 0) { 0 } else { -1 };
  }
  if (index < 0 || index >= u32_at(b, 8)) {
    return -1;
  }
  return u32_at(b, 12 + 4 * index);
}

// The versions of a TrueType font, of an Apple TrueType font, "true", and
// of an OpenType font with CFF outlines.
function known_version(v: i32): boolean {
  return v == 65536 || v == 1953658213 || v == OTTO;
}

// The first four bytes of a collection file, "ttcf", and of an OpenType
// font with CFF outlines, "OTTO".
const TTCF: i32 = 1953784678;

const OTTO: i32 = 1330926671;

// The offsets of the tables `tags`, or None when one is missing.
function tables(b: [u8], base: i32, tags: string[]): Option[i32[]] {
  let out: i32[] = [];
  for tag in tags {
    let at = table(b, base, tag);
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
  let base = u16_at(f.data, f.hmtx + 4 * i);
  if (f.coords.len() == 0) {
    return base;
  }
  return base + (advance_delta(f, g) + 0.5).floor() as i32;
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
    if (u8_at(b, r) != 107 || u8_at(b, r + 1) != 101 || u8_at(b, r + 2) != 114 || u8_at(b, r + 3) != 110) {
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

### `variations`

A variable font holds the outlines of its default design. For each glyph the `gvar` table holds lists of changes to the points, and each list applies in a region of the axes. The four points after the last point of a glyph are not on the outline. The first two hold the origin and the advance.

```fern
<<variations>>=
function fixed_at(b: [u8], at: i32): f64 {
  return i16_at(b, at) as f64 + u16_at(b, at + 2) as f64 / 65536.0;
}

// The index of the axis `tag`, or -1.
function axis_index(f: Font, tag: string): i32 {
  if (f.fvar < 0 || tag.len() != 4) {
    return -1;
  }
  let b = f.data;
  let first = f.fvar + u16_at(b, f.fvar + 4);
  let size = u16_at(b, f.fvar + 10);
  for i in 0..u16_at(b, f.fvar + 8) {
    let r = first + size * i;
    if (u8_at(b, r) == tag[0] as i32 && u8_at(b, r + 1) == tag[1] as i32 && u8_at(b, r + 2) == tag[2] as i32 && u8_at(b, r + 3) == tag[3] as i32) {
      return i;
    }
  }
  return -1;
}

// True when the font has the axis `tag`, such as "wght" for the weight.
pub function (f: Font) has_axis(tag: string): boolean {
  return axis_index(f, tag) >= 0;
}

// The place of `value` on axis `i`: -1 at the least value of the axis, 0 at
// the default and 1 at the greatest.
function normalized(f: Font, i: i32, value: f64): f64 {
  let r = f.fvar + u16_at(f.data, f.fvar + 4) + u16_at(f.data, f.fvar + 10) * i;
  let low = fixed_at(f.data, r + 4);
  let base = fixed_at(f.data, r + 8);
  let high = fixed_at(f.data, r + 12);
  let v = value;
  if (v < low) {
    v = low;
  }
  if (v > high) {
    v = high;
  }
  if (v < base && base > low) {
    return (v - base) / (base - low);
  }
  if (v > base && high > base) {
    return (v - base) / (high - base);
  }
  return 0.0;
}

// The place `v` on axis `i` after the segment map of the `avar` table.
function avar_map(b: [u8], avar: i32, i: i32, v: f64): f64 {
  if (avar < 0 || i >= u16_at(b, avar + 6)) {
    return v;
  }
  let p = avar + 8;
  for k in 0..i {
    p = p + 2 + 4 * u16_at(b, p);
  }
  for k in 1..u16_at(b, p) {
    let r = p + 2 + 4 * k;
    let from0 = f2dot14(b, r - 4);
    let from1 = f2dot14(b, r);
    if (v <= from1 && from1 > from0) {
      return f2dot14(b, r - 2) + (v - from0) * (f2dot14(b, r + 2) - f2dot14(b, r - 2)) / (from1 - from0);
    }
  }
  return v;
}

// The font at `value` on the axis `tag`, in the units of the axis: 400 to
// 700 for the weights from regular to bold on "wght". The other axes keep
// their places. A font with no such axis stays as it is.
pub function (f: Font) at_axis(tag: string, value: f64): Font {
  let at = axis_index(f, tag);
  if (at < 0) {
    return f;
  }
  let coords: f64[] = [];
  for i in 0..u16_at(f.data, f.fvar + 8) {
    let c = 0.0;
    if (i < f.coords.len()) {
      c = f.coords[i];
    }
    if (i == at) {
      c = avar_map(f.data, f.avar, i, normalized(f, i, value));
    }
    coords = coords.append(c);
  }
  return Font { ...f, coords: coords };
}

// The part of a region that applies at the place `v` on one axis. The
// region starts at `s`, has its peak at `p` and ends at `e`.
function axis_scalar(s: f64, p: f64, e: f64, v: f64): f64 {
  if (p == 0.0 || s > p || p > e || s < 0.0 && e > 0.0) {
    return 1.0;
  }
  if (v < s || v > e) {
    return 0.0;
  }
  if (v == p) {
    return 1.0;
  }
  if (v < p) {
    return (v - s) / (p - s);
  }
  return (e - v) / (e - p);
}

// The part of a tuple of `gvar` that applies to the font. `peak` is the
// offset of the peak of each axis. `mid` is the offset of the start and
// then the end of each axis, or -1 for a region from the default to the
// peak.
function tuple_scalar(f: Font, peak: i32, mid: i32): f64 {
  let n = f.coords.len();
  let out = 1.0;
  for a in 0..n {
    let p = f2dot14(f.data, peak + 2 * a);
    let s = p.min(0.0);
    let e = p.max(0.0);
    if (mid >= 0) {
      s = f2dot14(f.data, mid + 2 * a);
      e = f2dot14(f.data, mid + 2 * n + 2 * a);
    }
    out = out * axis_scalar(s, p, e, f.coords[a]);
  }
  return out;
}

// A list of point numbers from the font. `every` is true for the list that
// names each point of the glyph. `next` is the offset after the list.
struct PointList { points: i32[], every: boolean, next: i32 }

function packed_points(b: [u8], at: i32): PointList {
  let n = u8_at(b, at);
  let p = at + 1;
  if (n == 0) {
    return PointList { points: [], every: true, next: p };
  }
  if ((n & 128) != 0) {
    n = (n & 127) << 8 | u8_at(b, p);
    p += 1;
  }
  let out: i32[] = [];
  let v = 0;
  while (out.len() < n) {
    let control = u8_at(b, p);
    p += 1;
    for r in 0..(control & 127) + 1 {
      if ((control & 128) != 0) {
        v += u16_at(b, p);
        p += 2;
      } else {
        v += u8_at(b, p);
        p += 1;
      }
      out = out.append(v);
    }
  }
  return PointList { points: out, every: false, next: p };
}

// `count` packed deltas from `at`, or some more where the last run is
// longer.
function packed_deltas(b: [u8], at: i32, count: i32): i32[] {
  let out: i32[] = [];
  let p = at;
  while (out.len() < count) {
    let control = u8_at(b, p);
    p += 1;
    for r in 0..(control & 63) + 1 {
      let d = 0;
      if ((control & 128) != 0) {
        d = 0;
      } else if ((control & 64) != 0) {
        d = i16_at(b, p);
        p += 2;
      } else {
        d = u8_at(b, p) << 24 >> 24;
        p += 1;
      }
      out = out.append(d);
    }
  }
  return out;
}

function zeros(n: i32): f64[] {
  let out: f64[] = [];
  for i in 0..n {
    out = out.append(0.0);
  }
  return out;
}

// The delta of a point at the coordinate `c` that the tuple does not name,
// between the named points at `ca` and `cb` with the deltas `da` and `db`.
function between(c: i32, ca: i32, cb: i32, da: f64, db: f64): f64 {
  if (ca == cb) {
    if (da == db) {
      return da;
    }
    return 0.0;
  }
  let (lo, hi, dlo, dhi) = (ca, cb, da, db);
  if (cb < ca) {
    lo = cb;
    hi = ca;
    dlo = db;
    dhi = da;
  }
  if (c <= lo) {
    return dlo;
  }
  if (c >= hi) {
    return dhi;
  }
  return dlo + (c - lo) as f64 * (dhi - dlo) / ((hi - lo) as f64);
}

// Gives each point of the contour from `first` to `last` that the tuple
// does not name a delta from the named points before it and after it. `cs`
// holds the coordinates of the default design.
function fill_contour(own d: f64[], named: boolean[], cs: i32[], first: i32, last: i32): f64[] {
  let ts: i32[] = [];
  for i in first..last + 1 {
    if (named[i]) {
      ts = ts.append(i);
    }
  }
  let out = d;
  for k in 0..ts.len() {
    let a = ts[k];
    let b = ts[(k + 1) % ts.len()];
    let i = a;
    for step in first..last {
      i += 1;
      if (i > last) {
        i = first;
      }
      if (i == b) {
        break;
      }
      let da = out[a];
      let db = out[b];
      out = out.with(i, between(cs[i], cs[a], cs[b], da, db));
    }
  }
  return out;
}

function fill(own d: f64[], named: boolean[], cs: i32[], ends: i32[]): f64[] {
  let out = d;
  let first = 0;
  for e in ends {
    if (e >= first && e < cs.len()) {
      out = fill_contour(out, named, cs, first, e);
    }
    first = e + 1;
  }
  return out;
}

// The deltas of one coordinate of a tuple for each point of the glyph.
// `values` holds a delta for each of `points`, from `from`.
function tuple_deltas(l: PointList, values: i32[], from: i32, cs: i32[], ends: i32[]): f64[] {
  let total = cs.len() + 4;
  let out: f64[] = [];
  if (l.every) {
    for i in 0..total {
      out = out.append(values[from + i] as f64);
    }
    return out;
  }
  out = zeros(total);
  let named: boolean[] = [];
  for i in 0..total {
    named = named.append(false);
  }
  for k in 0..l.points.len() {
    let i = l.points[k];
    if (i < total) {
      out = out.with(i, values[from + k] as f64);
      named = named.with(i, true);
    }
  }
  return fill(out, named, cs, ends);
}

function add_scaled(own sum: f64[], d: f64[], s: f64): f64[] {
  let out = sum;
  for i in 0..d.len() {
    let old = out[i];
    out = out.with(i, old + s * d[i]);
  }
  return out;
}

// The offset of the variation data of glyph `g` in the font, or -1.
function gvar_data(f: Font, g: i32): i32 {
  let b = f.data;
  let gv = f.gvar;
  if (gv < 0 || g >= u16_at(b, gv + 12) || u16_at(b, gv + 4) != f.coords.len()) {
    return -1;
  }
  let start = 0;
  let end = 0;
  if ((u16_at(b, gv + 14) & 1) != 0) {
    start = u32_at(b, gv + 20 + 4 * g);
    end = u32_at(b, gv + 24 + 4 * g);
  } else {
    start = 2 * u16_at(b, gv + 20 + 2 * g);
    end = 2 * u16_at(b, gv + 22 + 2 * g);
  }
  if (end <= start) {
    return -1;
  }
  return gv + u32_at(b, gv + 16) + start;
}

// The change to the x and the y of each point of glyph `g`, and of its four
// points that are not on the outline, at the place of the font.
function glyph_deltas(f: Font, g: i32, o: Outline): (f64[], f64[]) {
  let total = o.xs.len() + 4;
  let dx = zeros(total);
  let dy = zeros(total);
  if (f.coords.len() == 0) {
    return (dx, dy);
  }
  let d = gvar_data(f, g);
  if (d < 0) {
    return (dx, dy);
  }
  let b = f.data;
  let axes = f.coords.len();
  let count = u16_at(b, d);
  let data = d + u16_at(b, d + 2);
  let shared = PointList { points: [], every: true, next: data };
  if ((count & 32768) != 0) {
    shared = packed_points(b, data);
    data = shared.next;
  }
  let h = d + 4;
  for t in 0..count & 4095 {
    let size = u16_at(b, h);
    let index = u16_at(b, h + 2);
    h += 4;
    let peak = f.gvar + u32_at(b, f.gvar + 8) + 2 * axes * (index & 4095);
    if ((index & 32768) != 0) {
      peak = h;
      h += 2 * axes;
    }
    let mid = -1;
    if ((index & 16384) != 0) {
      mid = h;
      h += 4 * axes;
    }
    let s = tuple_scalar(f, peak, mid);
    if (s != 0.0) {
      let l = PointList { points: shared.points, every: shared.every, next: data };
      if ((index & 8192) != 0) {
        l = packed_points(b, data);
      }
      let n = l.points.len();
      if (l.every) {
        n = total;
      }
      let values = packed_deltas(b, l.next, 2 * n);
      dx = add_scaled(dx, tuple_deltas(l, values, 0, o.xs, o.ends), s);
      dy = add_scaled(dy, tuple_deltas(l, values, n, o.ys, o.ends), s);
    }
    data += size;
  }
  return (dx, dy);
}

// The part of region `region` of the list at `regions` that applies to the
// font.
function region_scalar(f: Font, regions: i32, region: i32): f64 {
  let axes = u16_at(f.data, regions);
  if (axes != f.coords.len() || region >= u16_at(f.data, regions + 2)) {
    return 0.0;
  }
  let r = regions + 4 + 6 * axes * region;
  let out = 1.0;
  for a in 0..axes {
    out = out * axis_scalar(f2dot14(f.data, r + 6 * a), f2dot14(f.data, r + 6 * a + 2), f2dot14(f.data, r + 6 * a + 4), f.coords[a]);
  }
  return out;
}

// The delta of item `inner` of the data `outer` of the variation store at
// `store`.
function store_delta(f: Font, store: i32, outer: i32, inner: i32): f64 {
  let b = f.data;
  if (outer >= u16_at(b, store + 6)) {
    return 0.0;
  }
  let d = store + u32_at(b, store + 8 + 4 * outer);
  let regions = u16_at(b, d + 4);
  let words = u16_at(b, d + 2) & 32767;
  let wide = 1;
  if ((u16_at(b, d + 2) & 32768) != 0) {
    wide = 2;
  }
  if (inner >= u16_at(b, d) || words > regions) {
    return 0.0;
  }
  let p = d + 6 + 2 * regions + inner * wide * (words + regions);
  let sum = 0.0;
  for r in 0..regions {
    let size = wide;
    if (r < words) {
      size = 2 * wide;
    }
    let v = u8_at(b, p) << 24 >> 24;
    if (size == 2) {
      v = i16_at(b, p);
    } else if (size == 4) {
      v = u32_at(b, p);
    }
    p += size;
    sum += v as f64 * region_scalar(f, store + u32_at(b, store + 2), u16_at(b, d + 6 + 2 * r));
  }
  return sum;
}

// The data and the item of glyph `g` in the index map at `m`.
function delta_index(b: [u8], m: i32, g: i32): (i32, i32) {
  let format = u8_at(b, m + 1);
  let count = u16_at(b, m + 2);
  let data = m + 4;
  if (u8_at(b, m) == 1) {
    count = u32_at(b, m + 2);
    data = m + 6;
  }
  if (count <= 0) {
    return (0, g);
  }
  let size = ((format & 48) >> 4) + 1;
  let i = g;
  if (i >= count) {
    i = count - 1;
  }
  let at = data + size * i;
  let entry = 0;
  for k in 0..size {
    entry = entry << 8 | u8_at(b, at + k);
  }
  let bits = (format & 15) + 1;
  return (entry >> bits, entry & (1 << bits) - 1);
}

// The change to the advance of glyph `g` at the place of the font: from the
// `HVAR` table, or from the points of `gvar` in a font with no such table.
function advance_delta(f: Font, g: i32): f64 {
  let b = f.data;
  if (f.hvar >= 0) {
    let map = u32_at(b, f.hvar + 8);
    let (outer, inner) = (0, g);
    if (map != 0) {
      let (o, i) = delta_index(b, f.hvar + map, g);
      outer = o;
      inner = i;
    }
    return store_delta(f, f.hvar + u32_at(b, f.hvar + 4), outer, inner);
  }
  let at = glyph_at(f, g);
  let o = Outline { xs: [], ys: [], flags: [], ends: [] };
  if (at >= 0 && i16_at(b, at) < 0) {
    o = part_outline(parts(b, at + 10));
  } else if (at >= 0) {
    o = simple(b, at, i16_at(b, at));
  }
  let (dx, _) = glyph_deltas(f, g, o);
  return dx[o.xs.len() + 1] - dx[o.xs.len()];
}
```

### `outlines`

```fern
<<outlines>>=
// A point of an outline, in font units, on the curve or a control point.
// Two control points of a cubic curve of CFF come in a row, and each has
// `cubic`.
struct Point { x: f64, y: f64, on: boolean, cubic: boolean }

// The points of a simple glyph in font units, the flags of each, and the
// last point of each contour. For a composite glyph, the points are the
// offsets of the parts.
struct Outline { xs: i32[], ys: i32[], flags: i32[], ends: i32[] }

// The offset of the data of glyph `g` in the font, or -1 for a glyph with
// no outline.
function glyph_at(f: Font, g: i32): i32 {
  if (g < 0 || g >= f.glyphs) {
    return -1;
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
    return -1;
  }
  return f.glyf + start;
}

// The most glyphs that the outline of one glyph can read. A composite glyph
// of a hostile font can name the same part many times at each depth.
const MAX_GLYPH_READS: i32 = 4096;

// The contours of glyph `g`, each a list of points.
function contours(f: Font, g: i32): Point[][] {
  if (f.cff.charstrings >= 0) {
    return cff_contours(f, g);
  }
  let (cs, _) = contours_within(f, g, 0, MAX_GLYPH_READS);
  return cs;
}

// The contours of glyph `g` and the glyph reads that remain of `reads`.
// `depth` stops composite glyphs that refer to themselves.
function contours_within(f: Font, g: i32, depth: i32, reads: i32): (Point[][], i32) {
  let at = glyph_at(f, g);
  if (at < 0 || depth > 8 || reads <= 0) {
    return ([], reads);
  }
  let n = i16_at(f.data, at);
  if (n < 0) {
    return composite(f, g, at + 10, depth, reads - 1);
  }
  let o = simple(f.data, at, n);
  let (dx, dy) = glyph_deltas(f, g, o);
  let pts: Point[] = [];
  for i in 0..o.xs.len() {
    pts = pts.append(Point { x: o.xs[i] as f64 + dx[i], y: o.ys[i] as f64 + dy[i], on: (o.flags[i] & 1) != 0, cubic: false });
  }
  return (split_contours(pts, o.ends), reads - 1);
}

function simple(b: [u8], at: i32, n: i32): Outline {
  let ends: i32[] = [];
  for i in 0..n {
    ends = ends.append(u16_at(b, at + 10 + 2 * i));
  }
  if (n == 0 || !rising(ends)) {
    return Outline { xs: [], ys: [], flags: [], ends: [] };
  }
  let count = ends[n - 1] + 1;
  let p = at + 10 + 2 * n;
  p = p + 2 + u16_at(b, p);
  let (flags, p1) = read_flags(b, p, count);
  let (xs, p2) = read_coords(b, p1, flags, 2, 16);
  let (ys, _) = read_coords(b, p2, flags, 4, 32);
  return Outline { xs: xs, ys: ys, flags: flags, ends: ends };
}

// Whether each number of `xs` is more than the one before it. The last point
// of a contour comes after the last point of the contour before it.
function rising(xs: i32[]): boolean {
  for i in 1..xs.len() {
    if (xs[i] <= xs[i - 1]) {
      return false;
    }
  }
  return true;
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
        d = -d;
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

// A part of a composite glyph: the glyph, its offset, its matrix, and the
// offset of the next part in the font, or -1 after the last part.
struct Part { glyph: i32, dx: i32, dy: i32, a: f64, b: f64, c: f64, d: f64, next: i32 }

function read_part(b: [u8], at0: i32): Part {
  let flags = u16_at(b, at0);
  let at = at0 + 4;
  let dx = 0;
  let dy = 0;
  if ((flags & 1) != 0) {
    dx = i16_at(b, at);
    dy = i16_at(b, at + 2);
    at += 4;
  } else {
    dx = u8_at(b, at) << 24 >> 24;
    dy = u8_at(b, at + 1) << 24 >> 24;
    at += 2;
  }
  if ((flags & 2) == 0) {
    // The parts join at points, not at offsets: not supported.
    dx = 0;
    dy = 0;
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
  if ((flags & 32) == 0) {
    at = -1;
  }
  return Part { glyph: u16_at(b, at0 + 2), dx: dx, dy: dy, a: a, b: bb, c: c, d: d, next: at };
}

// The parts of the composite glyph at `at`. A font can hold at most 65535
// glyphs, so a longer list is not a font.
function parts(b: [u8], at: i32): Part[] {
  let out: Part[] = [];
  let next = at;
  while (next >= 0 && next < b.len() && out.len() < 65536) {
    let part = read_part(b, next);
    out = out.append(part);
    next = part.next;
  }
  return out;
}

// The offsets of the parts as an outline: each part is a point and a
// contour of its own.
function part_outline(ps: Part[]): Outline {
  let o = Outline { xs: [], ys: [], flags: [], ends: [] };
  for i in 0..ps.len() {
    o = Outline { xs: o.xs.append(ps[i].dx), ys: o.ys.append(ps[i].dy), flags: o.flags, ends: o.ends.append(i) };
  }
  return o;
}

// A composite glyph: the contours of its parts, each moved and scaled, and
// the glyph reads that remain of `reads`.
function composite(f: Font, g: i32, at: i32, depth: i32, reads: i32): (Point[][], i32) {
  let out: Point[][] = [];
  let ps = parts(f.data, at);
  let (ddx, ddy) = glyph_deltas(f, g, part_outline(ps));
  let left = reads;
  for i in 0..ps.len() {
    if (left <= 0) {
      break;
    }
    let q = ps[i];
    let dx = q.dx as f64 + ddx[i];
    let dy = q.dy as f64 + ddy[i];
    let (inner, after) = contours_within(f, q.glyph, depth + 1, left);
    left = after;
    for part in inner {
      let moved: Point[] = [];
      for p in part {
        moved = moved.append(Point { x: q.a * p.x + q.c * p.y + dx, y: q.b * p.x + q.d * p.y + dy, on: p.on, cubic: p.cubic });
      }
      out = out.append(moved);
    }
  }
  return (out, left);
}

function f2dot14(b: [u8], at: i32): f64 {
  return i16_at(b, at) as f64 / 16384.0;
}
```

### `cff outlines`

A CFF table holds INDEX structures. An INDEX has a count, the size of an offset, the offsets of its items from 1, and the data of its items. A DICT is a list of operands, each list before its operator. A Type 2 charstring is a program for a stack machine: each number goes on the stack, and each operator takes the numbers of the stack. A subroutine is a charstring that another charstring calls by its biased number.

The first operator that clears the stack can take one more number than it needs: the width of the glyph. The widths come from `hmtx`, so the interpreter drops that number.

```fern
<<cff outlines>>=
// The number of `size` bytes at `at`, high byte first.
function offset_at(b: [u8], at: i32, size: i32): i32 {
  let v = 0;
  for i in 0..size {
    v = v << 8 | u8_at(b, at + i);
  }
  return v;
}

// The count of the INDEX at `at`, whose count has `cb` bytes.
function index_count(b: [u8], at: i32, cb: i32): i32 {
  return offset_at(b, at, cb);
}

// The start and the end of item `i` of the INDEX at `at`.
function index_item(b: [u8], at: i32, cb: i32, i: i32): (i32, i32) {
  let n = index_count(b, at, cb);
  if (i < 0 || i >= n) {
    return (0, 0);
  }
  let size = u8_at(b, at + cb);
  let offsets = at + cb + 1;
  let data = offsets - 1 + (n + 1) * size;
  return (data + offset_at(b, offsets + i * size, size), data + offset_at(b, offsets + (i + 1) * size, size));
}

// The offset after the INDEX at `at`.
function index_end(b: [u8], at: i32, cb: i32): i32 {
  let n = index_count(b, at, cb);
  if (n == 0) {
    return at + cb;
  }
  let (_, end) = index_item(b, at, cb, n - 1);
  return end;
}

// The operand of a DICT at `i` and the offset after it. A real number
// counts as 0.
function dict_operand(b: [u8], i: i32): (i32, i32) {
  let b0 = u8_at(b, i);
  if (b0 == 28) {
    return (i16_at(b, i + 1), i + 3);
  }
  if (b0 == 29) {
    return (u32_at(b, i + 1), i + 5);
  }
  if (b0 == 30) {
    return (0, real_end(b, i + 1));
  }
  return short_number(b, i);
}

// A number of one or two bytes, the same in a DICT and in a charstring.
function short_number(b: [u8], i: i32): (i32, i32) {
  let b0 = u8_at(b, i);
  if (b0 <= 246) {
    return (b0 - 139, i + 1);
  }
  if (b0 <= 250) {
    return ((b0 - 247) * 256 + u8_at(b, i + 1) + 108, i + 2);
  }
  return (-(b0 - 251) * 256 - u8_at(b, i + 1) - 108, i + 2);
}

// The offset after the nibbles of a real number, which end with 15.
function real_end(b: [u8], at: i32): i32 {
  let i = at;
  while (i < b.len()) {
    let v = u8_at(b, i);
    i += 1;
    if ((v & 15) == 15 || v >> 4 == 15) {
      return i;
    }
  }
  return i;
}

// The operands of the operator `op` in the DICT from `start` up to `end`,
// or none. An operator is a byte up to 27, and the operator 12 x is
// 1200 + x.
function dict_operands(b: [u8], start: i32, end: i32, op: i32): i32[] {
  let args: i32[] = [];
  let i = start;
  while (i < end) {
    let b0 = u8_at(b, i);
    if (b0 > 27) {
      let (v, next) = dict_operand(b, i);
      args = args.append(v);
      i = next;
      continue;
    }
    let code = if (b0 == 12) { 1200 + u8_at(b, i + 1) } else { b0 };
    if (code == op) {
      return args;
    }
    args = [];
    i += if (b0 == 12) { 2 } else { 1 };
  }
  return [];
}

function no_cff(): Cff {
  return Cff { charstrings: -1, gsubrs: -1, subrs: [], fdselect: -1, count_bytes: 2, vstore: -1, vsindex: [] };
}

// The parts of the CFF table at `at`, for a font of `glyphs` glyphs.
function cff_of(b: [u8], at: i32, glyphs: i32): Cff {
  if (at < 0 || u8_at(b, at) != 1) {
    return no_cff();
  }
  let tops = index_end(b, at + u8_at(b, at + 2), 2);
  let gsubrs = index_end(b, index_end(b, tops, 2), 2);
  let (ts, te) = index_item(b, tops, 2, 0);
  let cs = dict_operands(b, ts, te, 17);
  if (cs.len() != 1 || index_count(b, at + cs[0], 2) < glyphs) {
    return no_cff();
  }
  let fdarray = dict_operands(b, ts, te, 1236);
  let fdselect = dict_operands(b, ts, te, 1237);
  let one = Cff { ...no_cff(), charstrings: at + cs[0], gsubrs: gsubrs, subrs: [private_subrs(b, at, ts, te)], vsindex: [0] };
  if (fdarray.len() != 1 || fdselect.len() != 1) {
    return one;
  }
  return with_dicts(b, at, one, at + fdarray[0], at + fdselect[0]);
}

// The CFF2 table at `at`. Its top DICT follows the header, and the global
// subroutines follow the top DICT. It always has an FDArray.
function cff2_of(b: [u8], at: i32, glyphs: i32): Cff {
  if (at < 0 || u8_at(b, at) != 2) {
    return no_cff();
  }
  let ts = at + u8_at(b, at + 2);
  let te = ts + u16_at(b, at + 3);
  let cs = dict_operands(b, ts, te, 17);
  let fdarray = dict_operands(b, ts, te, 1236);
  if (cs.len() != 1 || fdarray.len() != 1 || index_count(b, at + cs[0], 4) < glyphs) {
    return no_cff();
  }
  let fdselect = dict_operands(b, ts, te, 1237);
  let vstore = dict_operands(b, ts, te, 24);
  let base = Cff { ...no_cff(), charstrings: at + cs[0], gsubrs: te, count_bytes: 4, vstore: if (vstore.len() == 1) { at + vstore[0] + 2 } else { -1 } };
  return with_dicts(b, at, base, at + fdarray[0], if (fdselect.len() == 1) { at + fdselect[0] } else { -1 });
}

// `c` with the local subroutines and the first vsindex of each font dict
// of the FDArray at `fds`, and the FDSelect at `fdselect`.
function with_dicts(b: [u8], at: i32, c: Cff, fds: i32, fdselect: i32): Cff {
  let subrs: i32[] = [];
  let vsindex: i32[] = [];
  for i in 0..index_count(b, fds, c.count_bytes) {
    let (fs, fe) = index_item(b, fds, c.count_bytes, i);
    subrs = subrs.append(private_subrs(b, at, fs, fe));
    vsindex = vsindex.append(private_vsindex(b, at, fs, fe));
  }
  return Cff { ...c, subrs: subrs, vsindex: vsindex, fdselect: fdselect };
}

// The offset of the local subroutines of the Private DICT that the DICT
// from `start` up to `end` names, or -1.
function private_subrs(b: [u8], at: i32, start: i32, end: i32): i32 {
  let p = dict_operands(b, start, end, 18);
  if (p.len() != 2) {
    return -1;
  }
  let ps = at + p[1];
  let s = dict_operands(b, ps, ps + p[0], 19);
  return if (s.len() == 1) { ps + s[0] } else { -1 };
}

// The vsindex of the Private DICT that the DICT from `start` up to `end`
// names, or 0.
function private_vsindex(b: [u8], at: i32, start: i32, end: i32): i32 {
  let p = dict_operands(b, start, end, 18);
  if (p.len() != 2) {
    return 0;
  }
  let ps = at + p[1];
  let v = dict_operands(b, ps, ps + p[0], 22);
  return if (v.len() == 1) { v[0] } else { 0 };
}

// The font dict of glyph `g`, from FDSelect of format 0 or 3.
function fd_of(f: Font, g: i32): i32 {
  let sel = f.cff.fdselect;
  if (sel < 0) {
    return 0;
  }
  let b = f.data;
  if (u8_at(b, sel) == 0) {
    return u8_at(b, sel + 1 + g);
  }
  let n = u16_at(b, sel + 1);
  for i in 0..n {
    let r = sel + 3 + 3 * i;
    if (g >= u16_at(b, r) && g < u16_at(b, r + 3)) {
      return u8_at(b, r + 2);
    }
  }
  return 0;
}

// The bias of the numbers of a set of `n` subroutines.
function bias(n: i32): i32 {
  if (n < 1240) {
    return 107;
  }
  return if (n < 33900) { 1131 } else { 32768 };
}

// The state of the Type 2 interpreter: the stack, the current point, the
// contours that are done and the one that is open, the count of stems, and
// whether the width is settled. `ops` counts the operators that ran.
// `vsindex` selects the regions of a blend.
struct T2 { stack: f64[], x: f64, y: f64, cs: Point[][], cur: Point[], stems: i32, width: boolean, done: boolean, ops: i32, vsindex: i32 }

// The most operators of one glyph, and the depth of the calls of
// subroutines. A hostile font can call the same subroutine many times.
const MAX_T2_OPS: i32 = 65536;

const MAX_T2_DEPTH: i32 = 10;

// The contours of glyph `g` of a font with CFF outlines.
function cff_contours(f: Font, g: i32): Point[][] {
  let cb = f.cff.count_bytes;
  if (g < 0 || g >= index_count(f.data, f.cff.charstrings, cb)) {
    return [];
  }
  let (s, e) = index_item(f.data, f.cff.charstrings, cb, g);
  let fd = fd_of(f, g);
  let local = if (fd < f.cff.subrs.len()) { f.cff.subrs[fd] } else { -1 };
  let vsindex = if (fd < f.cff.vsindex.len()) { f.cff.vsindex[fd] } else { 0 };
  // A CFF2 charstring has no width.
  let t = run(f, local, T2 { stack: [], x: 0.0, y: 0.0, cs: [], cur: [], stems: 0, width: cb == 4, done: false, ops: 0, vsindex: vsindex }, s, e, 0);
  return closed(t).cs;
}

// Runs the charstring from `start` up to `end`. `local` is the offset of
// the local subroutines.
function run(f: Font, local: i32, own st: T2, start: i32, end: i32, depth: i32): T2 {
  let b = f.data;
  let t = st;
  let i = start;
  while (i < end && !t.done && t.ops < MAX_T2_OPS) {
    let b0 = u8_at(b, i);
    t = T2 { ...t, ops: t.ops + 1 };
    if (is_number(b0)) {
      let (v, next) = t2_number(b, i);
      t = T2 { ...t, stack: t.stack.append(v) };
      i = next;
    } else if (is_call(b0)) {
      t = called(f, local, t, if (b0 == 10) { local } else { f.cff.gsubrs }, depth);
      i += 1;
    } else if (b0 == 11) {
      return t;
    } else if (is_mask(b0)) {
      t = stemmed(t);
      i += 1 + (t.stems + 7) / 8;
    } else if (b0 == 15 || b0 == 16) {
      t = varied(f, t, b0);
      i += 1;
    } else {
      t = cleared(operated(t, b0, u8_at(b, i + 1)));
      i += if (b0 == 12) { 2 } else { 1 };
    }
  }
  return t;
}

// The first bytes of a number, of a call of a local or a global
// subroutine, and of a hint mask or a counter mask.
function is_number(b0: i32): boolean {
  return b0 >= 32 || b0 == 28;
}

function is_call(b0: i32): boolean {
  return b0 == 10 || b0 == 29;
}

function is_mask(b0: i32): boolean {
  return b0 == 19 || b0 == 20;
}

// vsindex, which selects the regions of the blends after it, and blend.
// blend takes n values, n times as many deltas as there are regions, and
// n, and leaves the n values with their deltas added at the place of the
// font.
function varied(f: Font, own t: T2, op: i32): T2 {
  let s = t.stack;
  let len = s.len();
  if (len == 0) {
    return t;
  }
  if (op == 15) {
    return T2 { ...t, vsindex: s[len - 1] as i32, stack: [] };
  }
  let n = s[len - 1] as i32;
  let scalars = region_scalars(f, t.vsindex);
  let k = scalars.len();
  let start = len - 1 - n * (k + 1);
  if (n < 0 || start < 0) {
    return T2 { ...t, stack: [] };
  }
  let out = s.take(start);
  for i in 0..n {
    let v = s[start + i];
    for j in 0..k {
      v += s[start + n + i * k + j] * scalars[j];
    }
    out = out.append(v);
  }
  return T2 { ...t, stack: out };
}

// The scalar of each region of the item variation data `vsindex` of the
// CFF2 table, at the place of the font. Each is 0 at the default design.
function region_scalars(f: Font, vsindex: i32): f64[] {
  let store = f.cff.vstore;
  let b = f.data;
  if (store < 0 || vsindex < 0 || vsindex >= u16_at(b, store + 6)) {
    return [];
  }
  let d = store + u32_at(b, store + 8 + 4 * vsindex);
  let out: f64[] = [];
  for r in 0..u16_at(b, d + 4) {
    out = out.append(region_scalar(f, store + u32_at(b, store + 2), u16_at(b, d + 6 + 2 * r)));
  }
  return out;
}

// The number of a charstring at `i`, and the offset after it.
function t2_number(b: [u8], i: i32): (f64, i32) {
  let b0 = u8_at(b, i);
  if (b0 == 28) {
    return (i16_at(b, i + 1) as f64, i + 3);
  }
  if (b0 == 255) {
    return (u32_at(b, i + 1) as f64 / 65536.0, i + 5);
  }
  let (v, next) = short_number(b, i);
  return (v as f64, next);
}

// A call of the subroutine whose biased number is on the top of the stack,
// in the set at `set`.
function called(f: Font, local: i32, own st: T2, set: i32, depth: i32): T2 {
  let n = st.stack.len();
  if (n == 0) {
    return st;
  }
  let k = st.stack[n - 1] as i32;
  let t = T2 { ...st, stack: st.stack.take(n - 1) };
  if (set < 0 || depth >= MAX_T2_DEPTH) {
    return t;
  }
  let cb = f.cff.count_bytes;
  let (s, e) = index_item(f.data, set, cb, k + bias(index_count(f.data, set, cb)));
  if (e <= s) {
    return t;
  }
  return run(f, local, t, s, e, depth + 1);
}

function cleared(own t: T2): T2 {
  return T2 { ...t, stack: [] };
}

// The state with the width settled: the first number of the stack is the
// width when `extra` is true.
function settled(own t: T2, extra: boolean): T2 {
  if (t.width || !extra) {
    return T2 { ...t, width: true };
  }
  return T2 { ...t, stack: t.stack.drop(1), width: true };
}

// The stems of a stem operator or of a hint mask, which counts the stems of
// the numbers before it as vertical stems.
function stemmed(own t: T2): T2 {
  let odd = t.stack.len() % 2 == 1;
  let s = settled(t, odd);
  return T2 { ...s, stems: s.stems + s.stack.len() / 2, stack: [] };
}

// The stem operators, the moves and the lines.
function is_stem(op: i32): boolean {
  return op == 1 || op == 3 || op == 18 || op == 23;
}

function is_move(op: i32): boolean {
  return op == 21 || op == 22 || op == 4;
}

function is_line(op: i32): boolean {
  return op == 5 || op == 6 || op == 7;
}

function operated(own t: T2, op: i32, next: i32): T2 {
  if (is_stem(op)) {
    return stemmed(t);
  }
  if (is_move(op)) {
    return moved(t, op);
  }
  if (is_line(op)) {
    return lined(t, op);
  }
  if (op == 14) {
    let odd = t.stack.len() % 2 == 1;
    let s = closed(settled(t, odd));
    return T2 { ...s, done: true };
  }
  if (op == 12) {
    return flexed(t, next);
  }
  return curved(t, op);
}

// The open contour added to the contours that are done.
function closed(own t: T2): T2 {
  if (t.cur.len() < 2) {
    return T2 { ...t, cur: [] };
  }
  return T2 { ...t, cs: t.cs.append(t.cur), cur: [] };
}

function moved(own st: T2, op: i32): T2 {
  let need = if (op == 21) { 2 } else { 1 };
  let extra = st.stack.len() > need;
  let t = closed(settled(st, extra));
  let s = t.stack;
  let (dx, dy) = (0.0, 0.0);
  if (op == 21 && s.len() >= 2) {
    dx = s[0];
    dy = s[1];
  } else if (s.len() >= 1) {
    dx = if (op == 22) { s[0] } else { 0.0 };
    dy = if (op == 4) { s[0] } else { 0.0 };
  }
  let x = t.x + dx;
  let y = t.y + dy;
  return T2 { ...t, x: x, y: y, cur: [Point { x: x, y: y, on: true, cubic: false }] };
}

function line_to(own t: T2, dx: f64, dy: f64): T2 {
  let x = t.x + dx;
  let y = t.y + dy;
  return T2 { ...t, x: x, y: y, cur: t.cur.append(Point { x: x, y: y, on: true, cubic: false }) };
}

function curve_to(own t: T2, dx1: f64, dy1: f64, dx2: f64, dy2: f64, dx3: f64, dy3: f64): T2 {
  let (x1, y1) = (t.x + dx1, t.y + dy1);
  let (x2, y2) = (x1 + dx2, y1 + dy2);
  let (x3, y3) = (x2 + dx3, y2 + dy3);
  let cur = t.cur.append(Point { x: x1, y: y1, on: false, cubic: true }).append(Point { x: x2, y: y2, on: false, cubic: true }).append(Point { x: x3, y: y3, on: true, cubic: false });
  return T2 { ...t, x: x3, y: y3, cur: cur };
}

// rlineto, hlineto and vlineto. hlineto and vlineto change between
// horizontal and vertical lines.
function lined(own st: T2, op: i32): T2 {
  let s = st.stack;
  let t = st;
  if (op == 5) {
    for k in 0..s.len() / 2 {
      t = line_to(t, s[2 * k], s[2 * k + 1]);
    }
    return t;
  }
  for k in 0..s.len() {
    t = if (k % 2 == 0 == (op == 6)) { line_to(t, s[k], 0.0) } else { line_to(t, 0.0, s[k]) };
  }
  return t;
}

// rrcurveto, rcurveline, rlinecurve, hhcurveto, vvcurveto, vhcurveto and
// hvcurveto. Each other operator does nothing.
function curved(own t: T2, op: i32): T2 {
  if (op == 8 || op == 24) {
    return curves_then_line(t, op == 24);
  }
  if (op == 25) {
    return line_curve(t);
  }
  if (op == 27 || op == 26) {
    return straight_curves(t, op == 27);
  }
  if (op == 30 || op == 31) {
    return turning_curves(t, op == 31);
  }
  return t;
}

// rrcurveto, or rcurveline with `line` true: curves of six numbers, then a
// line from the last two.
function curves_then_line(own t: T2, line: boolean): T2 {
  let s = t.stack;
  let n = s.len();
  let room = if (line) { n - 2 } else { n };
  let out = t;
  let k = 0;
  while (k + 6 <= room) {
    out = curve_to(out, s[k], s[k + 1], s[k + 2], s[k + 3], s[k + 4], s[k + 5]);
    k += 6;
  }
  return if (line && k + 2 == n) { line_to(out, s[k], s[k + 1]) } else { out };
}

// rlinecurve: lines, then one curve from the last six numbers.
function line_curve(own t: T2): T2 {
  let s = t.stack;
  let n = s.len();
  if (n < 6) {
    return t;
  }
  let out = t;
  let k = 0;
  while (k + 2 <= n - 6) {
    out = line_to(out, s[k], s[k + 1]);
    k += 2;
  }
  return curve_to(out, s[n - 6], s[n - 5], s[n - 4], s[n - 3], s[n - 2], s[n - 1]);
}

// hhcurveto, with `across` true, or vvcurveto: curves that start and end
// horizontal, or vertical. An odd first number moves the first point
// across.
function straight_curves(own t: T2, across: boolean): T2 {
  let s = t.stack;
  let n = s.len();
  let out = t;
  let k = n % 2;
  let first = if (k == 1) { s[0] } else { 0.0 };
  while (k + 4 <= n) {
    out = if (across) { curve_to(out, s[k], first, s[k + 1], s[k + 2], s[k + 3], 0.0) } else { curve_to(out, first, s[k], s[k + 1], s[k + 2], 0.0, s[k + 3]) };
    first = 0.0;
    k += 4;
  }
  return out;
}

// hvcurveto, with `across` true, or vhcurveto: curves whose tangents change
// between horizontal and vertical. A fifth number ends the last curve.
function turning_curves(own t: T2, across: boolean): T2 {
  let s = t.stack;
  let n = s.len();
  let out = t;
  let k = 0;
  let h = across;
  while (k + 4 <= n) {
    let last = if (k + 5 == n) { s[k + 4] } else { 0.0 };
    out = if (h) { curve_to(out, s[k], 0.0, s[k + 1], s[k + 2], last, s[k + 3]) } else { curve_to(out, 0.0, s[k], s[k + 1], s[k + 2], s[k + 3], last) };
    h = !h;
    k += 4;
  }
  return out;
}

// The two-byte operators 12 x: flex, hflex, hflex1 and flex1, each two
// curves. Each other operator of the set does nothing.
function flexed(own t: T2, x: i32): T2 {
  let s = t.stack;
  let n = s.len();
  if (x == 35 && n >= 13) {
    return curve_to(curve_to(t, s[0], s[1], s[2], s[3], s[4], s[5]), s[6], s[7], s[8], s[9], s[10], s[11]);
  }
  if (x == 34 && n >= 7) {
    return curve_to(curve_to(t, s[0], 0.0, s[1], s[2], s[3], 0.0), s[4], 0.0, s[5], 0.0 - s[2], s[6], 0.0);
  }
  if (x == 36 && n >= 9) {
    return curve_to(curve_to(t, s[0], s[1], s[2], s[3], s[4], 0.0), s[5], 0.0, s[6], s[7], s[8], 0.0 - (s[1] + s[3] + s[7]));
  }
  if (x == 37 && n >= 11) {
    return flex1(t);
  }
  return t;
}

// flex1: the last point moves along the axis on which the five points
// before it move most.
function flex1(own t: T2): T2 {
  let s = t.stack;
  let dx = s[0] + s[2] + s[4] + s[6] + s[8];
  let dy = s[1] + s[3] + s[5] + s[7] + s[9];
  let (ex, ey) = if (dx.abs() > dy.abs()) { (s[10], 0.0 - dy) } else { (0.0 - dx, s[10]) };
  return curve_to(curve_to(t, s[0], s[1], s[2], s[3], s[4], s[5]), s[6], s[7], s[8], s[9], ex, ey);
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
    x -= ay * dxdy;
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

// A cubic curve as lines, about one for each two pixels of its length.
function cubic(r: Raster, x0: f64, y0: f64, c1: Point, c2: Point, x1: f64, y1: f64): Raster {
  let ax = x0 - 2.0 * c1.x + c2.x;
  let ay = y0 - 2.0 * c1.y + c2.y;
  let bx = c1.x - 2.0 * c2.x + x1;
  let by = c1.y - 2.0 * c2.y + y1;
  let dev = (ax * ax + ay * ay).max(bx * bx + by * by).sqrt();
  let n = 1;
  if (dev > 0.333) {
    n = 1 + (6.0 * dev).sqrt() as i32;
  }
  let out = r;
  let (px, py) = (x0, y0);
  for i in 1..n + 1 {
    let t = i as f64 / (n as f64);
    let u = 1.0 - t;
    let qx = u * u * u * x0 + 3.0 * u * u * t * c1.x + 3.0 * u * t * t * c2.x + t * t * t * x1;
    let qy = u * u * u * y0 + 3.0 * u * u * t * c1.y + 3.0 * u * t * t * c2.y + t * t * t * y1;
    out = line(out, px, py, qx, qy);
    px = qx;
    py = qy;
  }
  return out;
}

// The most pixels of one bitmap. The coordinates of a glyph and the size are
// input, so their product does not set the allocation.
const MAX_BITMAP_PIXELS: f64 = 16777216.0;

// The coverage of glyph `g` at `px` pixels per em, with the pen `dx` of a
// pixel to the right of a pixel edge. A glyph whose box holds more than
// 16777216 pixels gives an empty bitmap.
pub function (f: Font) render(g: i32, px: f64, dx: f64): Bitmap {
  let cs = contours(f, g);
  let scale = px / (f.units_per_em as f64);
  let (minx, miny, maxx, maxy) = bounds(cs, scale, dx);
  let wide = maxx.ceil() - minx.floor() + 1.0;
  let high = maxy.ceil() - miny.floor() + 1.0;
  if (maxx <= minx || maxy <= miny || !(wide * high <= MAX_BITMAP_PIXELS)) {
    return Bitmap { width: 0, height: 0, left: 0, top: 0, coverage: [] };
  }
  let left = minx.floor() as i32;
  let top = miny.floor() as i32;
  let w = wide as i32;
  let h = high as i32;
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
    sum += r.acc[i];
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
    pts = pts.append(Point { ...p, x: p.x * scale + ox, y: oy - p.y * scale });
  }
  let (first, sx, sy) = start_point(pts);
  let out = r;
  let (cx, cy) = (sx, sy);
  let control = false;
  let (qx, qy) = (0.0, 0.0);
  let held: Point[] = [];
  for k in 1..n + 1 {
    let p = pts[(first + k) % n];
    if (p.cubic) {
      held = held.append(p);
      continue;
    }
    if (held.len() == 2) {
      out = cubic(out, cx, cy, held[0], held[1], p.x, p.y);
      held = [];
    } else if (p.on && control) {
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
