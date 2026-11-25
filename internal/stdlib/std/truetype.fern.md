# TrueType fonts

This module reads TrueType fonts and draws their glyphs as coverage maps.

## Model

`parse` reads the tables of a font: the character map, the horizontal metrics, the outlines, the kerning of the GPOS table and of the `kern` table, and the GSUB and GDEF tables. A variable font holds one set of outlines and the changes that each axis, such as the weight, makes to them. `at_axis` gives the font at a value on one axis. `glyph` gives the glyph of a code point, and `advance` and `kerning` give the space between glyphs in font units. `render` scales the outline of a glyph to a size in pixels and gives the part of each pixel that the outline covers.

`render_color` draws a color glyph of the `COLR` table, with the colors of the `CPAL` table, as a color for each pixel.

The rasterizer adds the signed area that each line of the outline covers to the cells of a buffer. The running sum of the buffer is then the coverage of each pixel. Curves become lines first, quadratic curves of TrueType and cubic curves of CFF alike. The rasterizer uses the nonzero rule, with the coverage limited to one.

## Contracts

`parse` accepts a font with `glyf` outlines, a `CFF` table or a `CFF2` table, and a Unicode character map of format 4 or 12. It gives None for other fonts. `parse_index` takes one font of a collection file, `.ttc`, by its index.

A CFF table can use CIDs, with a font dict for each group of glyphs. The charstrings are of Type 2. They draw cubic curves, and their hints have no effect. In a variable CFF2 font, `blend` takes the place of the font on each axis that `at_axis` sets.

A code point that the font does not map gives glyph 0. The kerning comes from the pair adjustment lookups of the `kern` feature, both formats. Composite glyphs that join their parts at points, not at offsets, draw their parts with no offset. The hinting instructions of the font have no effect.

`feature_lookups` gives the GSUB lookups of a feature for a script, from the default language system of the script. `substitute` applies one lookup to a `Run` of glyphs. It reads the lookups of the types 1 to 8 in each format. The flags of a lookup can skip glyphs by their GDEF class, their mark attachment class or a mark glyph set. A context applies the lookups of its rule, which can change the count of glyphs, as HarfBuzz does.

The matching of a lookup skips a default ignorable glyph, such as a ZWJ, when that glyph does not match. As in HarfBuzz, a ZWNJ in the input of a GSUB lookup stops the match. The flags of `substitute` can make a ZWJ stop it too. With `PER_SYLLABLE`, the input matches only in the syllable of its first glyph. An input does not join marks on different parts of an earlier ligature, and a ligature merges the clusters as HarfBuzz merges them. `would_substitute` tells whether the lookups of a feature apply to a sequence of glyphs, as the Indic shaper of HarfBuzz asks it.

A font whose GDEF table has no glyph classes gives each glyph of a run the class in its properties, from the bit `CLASS_SHIFT`. The shaper gives each glyph the class of its character, and a ligature or a multiple substitution changes it as in HarfBuzz. `class_at` gives the class of a glyph of a run in each font. A lookup whose mark filtering set is not in GDEF does not filter the marks.

`with_advances` gives each glyph of a run its advance. Then `position_lookups` gives the GPOS lookups of a feature, and `position` applies one lookup. It reads the lookups of the types 1 to 9: single and pair adjustments, cursive joins, marks on bases, on ligatures and on marks, contexts and extensions. The contour points and the device tables of anchors and of value records have no effect.

`with_kern_table` applies the `kern` table of a font, of OpenType or of Apple, as the section `kern table` tells. `extents` gives the box of the ink of a glyph, for the fallback positioning of the marks in `std/shape`.

The length of a run at `run_of` limits each lookup that applies to it, as HarfBuzz limits a buffer. The run grows to 64 glyphs for each glyph, with a floor of 16384 and a ceiling of 65536. The lookups have 1024 attempts for each glyph, with a floor of 16384: each glyph that a lookup tries takes one, and so does each lookup that a context applies. The feature variations of GSUB and GPOS have no effect.

`render_color` draws the glyphs of COLR versions 0 and 1, with each paint of version 1: layers, solid colors, the three gradients, glyph clips, the transforms and the composite modes. It uses the first palette. The colors of a gradient mix in premultiplied form. A paint of a variation format draws as its static format, and the variations of COLR have no effect. A glyph draws in its clip box, or else in its advance and the line metrics of the font.

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

<<substitution>>

<<positioning>>

<<kern table>>

<<variations>>

<<outlines>>

<<cff outlines>>

<<extents>>

<<the rasterizer>>

<<color glyphs>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads TrueType fonts and draws their glyphs as coverage maps.
import "std/array";
import "std/f64";
import "std/u64";

// A font in the bytes `data`, with the offsets of its tables. `cmap` is the
// offset of the character map subtable, of format `cmap_format`. `pairs`
// are the offsets of the GPOS pair adjustment subtables of the kern feature.
// `gsub`, `gpos`, `gdef`, `colr` and `cpal` are the offsets of the GSUB,
// GPOS, GDEF, COLR and CPAL tables, or -1.
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
  kern: i32[],
  gsub: i32,
  gpos: i32,
  gdef: i32,
  colr: i32,
  cpal: i32,
  fvar: i32,
  avar: i32,
  gvar: i32,
  hvar: i32,
  coords: f64[],
  cff: Cff,
  // The class of each glyph from `with_glyph_classes`, or empty.
  classes: u8[],
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
  if (at >= 0 && at < b.len() - 1) {
    return b[at] as i32 << 8 | b[at + 1] as i32;
  }
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
  let r = table_record(b, base, tag);
  return if (r < 0) { -1 } else { u32_at(b, r + 8) };
}

// The offset of the end of the table `tag`, or -1.
function table_end(b: [u8], base: i32, tag: string): i32 {
  let r = table_record(b, base, tag);
  return if (r < 0) { -1 } else { u32_at(b, r + 8) + u32_at(b, r + 12) };
}

// The offset of the record of the table `tag` in the table directory at
// `base`, or -1.
function table_record(b: [u8], base: i32, tag: string): i32 {
  let n = u16_at(b, base + 4);
  for i in 0..n {
    let r = base + 12 + 16 * i;
    if (b.len() >= r + 16 && b[r] == tag[0] && b[r + 1] == tag[1] && b[r + 2] == tag[2] && b[r + 3] == tag[3]) {
      return r;
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
  return Some(Font { data: b, cmap: sub, cmap_format: format, loca: loca, glyf: glyf, hmtx: t[4], hmetrics: u16_at(b, hhea + 34), glyphs: glyphs, long_loca: i16_at(b, head + 50) != 0, units_per_em: upem, ascender: i16_at(b, hhea + 4), descender: i16_at(b, hhea + 6), line_gap: i16_at(b, hhea + 8), pairs: pairs, kern: kern_table(b, table(b, base, "kern"), table_end(b, base, "kern")), gsub: table(b, base, "GSUB"), gpos: table(b, base, "GPOS"), gdef: table(b, base, "GDEF"), colr: table(b, base, "COLR"), cpal: table(b, base, "CPAL"), fvar: table(b, base, "fvar"), avar: table(b, base, "avar"), gvar: table(b, base, "gvar"), hvar: table(b, base, "HVAR"), coords: [], cff: cff, classes: [] });
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
    if (!tag_is(b, r, "kern")) {
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

// The field `bit` of the value record of `format` at `at`, or 0. The
// fields are, from the bit 1: the x and y placements, then the x and y
// advances.
function value_field(b: [u8], at: i32, format: i32, bit: i32): i32 {
  if ((format & bit) == 0) {
    return 0;
  }
  return i16_at(b, at + value_size(format & bit - 1));
}

// The offset of the first value record of the pair of `g1` and `g2` in the
// pair adjustment subtable at `st`, whose records have `size` bytes, or -1.
// `cov` is the coverage index of `g1`.
function pair_record(b: [u8], st: i32, cov: i32, g1: i32, g2: i32, size: i32): i32 {
  if (u16_at(b, st) == 2) {
    let c1 = class_of(b, st + u16_at(b, st + 8), g1);
    let c2 = class_of(b, st + u16_at(b, st + 10), g2);
    let n2 = u16_at(b, st + 14);
    let valid = c1 < u16_at(b, st + 12) && c2 < n2;
    return if (valid) { st + 16 + (c1 * n2 + c2) * size } else { -1 };
  }
  if (u16_at(b, st) != 1 || cov >= u16_at(b, st + 8)) {
    return -1;
  }
  let set = st + u16_at(b, st + 10 + 2 * cov);
  let lo = 0;
  let hi = u16_at(b, set) - 1;
  while (lo <= hi) {
    let mid = (lo + hi) / 2;
    let r = set + 2 + (2 + size) * mid;
    let second = u16_at(b, r);
    if (second == g2) {
      return r + 2;
    }
    lo = if (second < g2) { mid + 1 } else { lo };
    hi = if (second < g2) { hi } else { mid - 1 };
  }
  return -1;
}

// The kerning between glyphs `a` and `b`, in font units.
pub function (f: Font) kerning(a: i32, g2: i32): i32 {
  let b = f.data;
  for st in f.pairs {
    let cov = coverage(b, st + u16_at(b, st + 2), a);
    let vf1 = u16_at(b, st + 4);
    let at = if (cov < 0) { -1 } else { pair_record(b, st, cov, a, g2, value_size(vf1) + value_size(u16_at(b, st + 6))) };
    let k = if (at < 0) { 0 } else { value_field(b, at, vf1, 4) };
    if (at >= 0 && (k != 0 || u16_at(b, st) == 1)) {
      return k;
    }
  }
  return 0;
}
```

### `substitution`

The GSUB table changes the glyphs of a run. A lookup replaces one glyph with one other glyph, with a sequence or with a ligature. A context lookup applies other lookups where a sequence of glyphs matches. A lookup applies to a glyph when the mask of its feature and the mask of the glyph have a bit in common. Its flags can skip the glyph by its GDEF class. `std/shape` gives the order of the features.

```fern
<<substitution>>=
// Glyphs in logical order, with the cluster of each glyph: the index of
// its first character. A lookup applies to a glyph when the mask of the
// lookup and the mask of the glyph have a bit in common. `ligs` holds the
// ligature of each glyph as HarfBuzz keeps it: the ligature id from the
// bit 5, then 16 for a ligature glyph, and its count of parts or the part
// of a mark. `props` holds the properties of each glyph, from the bits
// below. `vars` holds a value of the shaper for each glyph, which the
// lookups copy as they copy the masks. `serial` gives the next ligature id.
// `pos` is empty until `with_advances` gives each glyph its advance.
// `max_len` is the most glyphs that the run can grow to, and `ops` the
// attempts that its lookups have left.
pub struct Run { glyphs: i32[], clusters: i32[], masks: i32[], ligs: i32[], props: i32[], vars: i32[], serial: i32, pos: Positions, max_len: i32, ops: i32 }

// The properties of a glyph. The shaper gives IGNORABLE, ZWNJ, ZWJ and
// HIDDEN from the character, and the lookups set the other three. The
// class of the glyph for a font with no glyph classes in GDEF is from the
// bit CLASS_SHIFT, as `class_at` reads it. The syllable of the glyph is
// from the bit SYLLABLE_SHIFT, and 0 is no syllable.
pub const IGNORABLE: i32 = 1;

pub const ZWNJ: i32 = 2;

pub const ZWJ: i32 = 4;

pub const SUBSTITUTED: i32 = 8;

pub const LIGATED: i32 = 16;

pub const MULTIPLIED: i32 = 32;

// A default ignorable glyph that a GSUB lookup does not skip, such as the
// combining grapheme joiner.
pub const HIDDEN: i32 = 64;

pub const CLASS_SHIFT: i32 = 7;

pub const SYLLABLE_SHIFT: i32 = 9;

// The flags of a lookup for `substitute` and `position`. With PER_SYLLABLE
// a GSUB lookup matches in one syllable. With MANUAL_ZWNJ the context of a
// GSUB lookup does not skip a ZWNJ, and with MANUAL_ZWJ the input does not
// skip a ZWJ.
pub const PER_SYLLABLE: i32 = 1;

pub const MANUAL_ZWNJ: i32 = 2;

pub const MANUAL_ZWJ: i32 = 4;

// The place of each glyph of a run in font units: its advance, and its
// offset from its pen. A glyph that GPOS attaches has the distance to its
// parent glyph in `chain`, and `attach` is 1 for a mark and 2 for a
// cursive join. `rtl` is the direction of the run.
pub struct Positions { advances: i32[], dx: i32[], dy: i32[], chain: i32[], attach: i32[], rtl: boolean }

// A GSUB or GPOS lookup: its type, its flags, its offset, its count of
// subtables, its mark filtering set or -1, and the offset of its table.
// `opts` holds the flags of `substitute`, and `base` the count of glyphs
// before the lookup. A prepared lookup has the facts of `subtable_facts`
// in `subs`, and another lookup has none.
struct Lookup { kind: i32, flag: i32, at: i32, count: i32, set: i32, table: i32, gpos: boolean, opts: i32, base: i32, subs: i32[] }

// How a search for the next glyph matches, as in HarfBuzz. A glyph matches
// only when its mask has a bit of `mask` and, when `syllable` is not 0, it
// is in that syllable. A default ignorable glyph that does not match is
// skipped. A ZWNJ is skipped only with `zwnj`, and a ZWJ only with `zwj`.
struct Matcher { mask: i32, syllable: i32, zwnj: boolean, zwj: boolean }

// A sequence of `n` values of 16 bits at `at`. A value of `kind` 0 is a
// glyph, of 1 a class of the class definition at `table`, and of 2 the
// offset of a coverage table from `table`.
struct Seq { kind: i32, table: i32, at: i32, n: i32 }

// The most glyphs that a run can grow to.
const MAX_GLYPHS: i32 = 65536;

// The limits of a run of n glyphs, as HarfBuzz sets them for a buffer: it can
// grow to 64 glyphs for each glyph, and its lookups can make 1024 attempts for
// each glyph, each with a floor of 16384.
const LEN_FACTOR: i32 = 64;

const OPS_FACTOR: i32 = 1024;

const MIN_LIMIT: i32 = 16384;

// An attempt at one glyph takes one more attempt from the budget for each
// this many subtables that it examines. A lookup can declare 65535 subtables,
// and without this each attempt could examine each of them for nothing.
const SUBTABLES_PER_ATTEMPT: i32 = 64;

function tag_is(b: [u8], at: i32, tag: string): boolean {
  return tag.len() == 4 && u8_at(b, at) == tag[0] as i32 && u8_at(b, at + 1) == tag[1] as i32 && u8_at(b, at + 2) == tag[2] as i32 && u8_at(b, at + 3) == tag[3] as i32;
}

// The default language system of the script `script` in the script list
// at `scripts`. A script that the font does not have takes "DFLT", then
// "dflt", then "latn". It is -1 when there is none.
function lang_sys(b: [u8], scripts: i32, script: string): i32 {
  for tag in [script, "DFLT", "dflt", "latn"] {
    for i in 0..u16_at(b, scripts) {
      let r = scripts + 2 + 6 * i;
      if (tag_is(b, r, tag)) {
        let s = scripts + u16_at(b, r + 4);
        let d = u16_at(b, s);
        return if (d == 0) { -1 } else { s + d };
      }
    }
  }
  return -1;
}

// A run of the glyphs `glyphs`, with the clusters `clusters` and the masks
// `masks`, before any lookup.
pub function run_of(glyphs: i32[], clusters: i32[], masks: i32[]): Run {
  let none: i32[] = [];
  let zeros = __alloc_i32(glyphs.len());
  let n = glyphs.len().min(MAX_GLYPHS);
  return Run { glyphs: glyphs, clusters: clusters, masks: masks, ligs: zeros, props: zeros, vars: zeros, serial: 0, pos: Positions { advances: none, dx: none, dy: none, chain: none, attach: none, rtl: false }, max_len: (n * LEN_FACTOR).max(MIN_LIMIT).min(MAX_GLYPHS), ops: (n * OPS_FACTOR).max(MIN_LIMIT) };
}

// The GSUB lookups of the feature `feature` for the script `script`, in
// the order of their indices, each one time.
pub function (f: Font) feature_lookups(script: string, feature: string): i32[] {
  return layout_lookups(f.data, f.gsub, script, feature);
}

// The GPOS lookups of the feature `feature` for the script `script`, as
// `feature_lookups` gives those of GSUB.
pub function (f: Font) position_lookups(script: string, feature: string): i32[] {
  return layout_lookups(f.data, f.gpos, script, feature);
}

// Whether the GSUB table, or the GPOS table with `gpos`, has the script
// `script`.
pub function (f: Font) has_script(script: string, gpos: boolean): boolean {
  let b = f.data;
  let t = if (gpos) { f.gpos } else { f.gsub };
  if (t < 0) {
    return false;
  }
  let scripts = t + u16_at(b, t + 4);
  for i in 0..u16_at(b, scripts) {
    if (tag_is(b, scripts + 2 + 6 * i, script)) {
      return true;
    }
  }
  return false;
}

// Whether the GSUB or the GPOS table has the feature `feature` for the
// script `script`, with lookups or without.
pub function (f: Font) has_feature(script: string, feature: string): boolean {
  return feature_tables(f.data, f.gsub, script, feature).len() > 0 || feature_tables(f.data, f.gpos, script, feature).len() > 0;
}

// Whether the GPOS table has the feature `feature` for the script
// `script`, with lookups or without.
pub function (f: Font) has_position_feature(script: string, feature: string): boolean {
  return feature_tables(f.data, f.gpos, script, feature).len() > 0;
}

// The lookups of the feature `feature` for the script `script` in the GSUB
// or GPOS table at `t`.
function layout_lookups(b: [u8], t: i32, script: string, feature: string): i32[] {
  let out: i32[] = [];
  for ft in feature_tables(b, t, script, feature) {
    for k in 0..u16_at(b, ft + 2) {
      out = inserted(out, u16_at(b, ft + 4 + 2 * k));
    }
  }
  return out;
}

// The offsets of the feature tables of `feature` for the script `script` in
// the GSUB or GPOS table at `t`.
function feature_tables(b: [u8], t: i32, script: string, feature: string): i32[] {
  let out: i32[] = [];
  let ls = if (t < 0) { -1 } else { lang_sys(b, t + u16_at(b, t + 4), script) };
  if (ls < 0) {
    return out;
  }
  let features = t + u16_at(b, t + 6);
  let count = u16_at(b, features);
  // The required feature precedes the count and the optional feature indices.
  for k in 0..1 + u16_at(b, ls + 4) {
    let at = if (k == 0) { ls + 2 } else { ls + 4 + 2 * k };
    let fi = u16_at(b, at);
    let r = features + 2 + 6 * fi;
    if (fi < count && tag_is(b, r, feature)) {
      out = out.append(features + u16_at(b, r + 4));
    }
  }
  return out;
}

// Whether a GSUB lookup of the feature `feature` for the script `script`
// applies to the glyphs `gs` as one input, with no glyphs skipped. With
// `zero_context` a chain context with glyphs before or after its input does
// not apply.
pub function (f: Font) would_substitute(script: string, feature: string, gs: i32[], zero_context: boolean): boolean {
  return f.would_substitute_lookups(f.feature_lookups(script, feature), gs, zero_context);
}

// Test GSUB lookup indices already selected for a font and script.
pub function (f: Font) would_substitute_lookups(lookups: i32[], gs: i32[], zero_context: boolean): boolean {
  if (gs.len() == 0) {
    return false;
  }
  for li in lookups {
    let l = lookup_of(f, f.gsub, false, li);
    for s in 0..l.count {
      let (st, kind) = subtable(f.data, l, s);
      if (would_apply(f.data, st, kind, gs, zero_context)) {
        return true;
      }
    }
  }
  return false;
}

// Whether the GSUB subtable `st` of type `kind` applies to the glyphs `gs`.
function would_apply(b: [u8], st: i32, kind: i32, gs: i32[], zero_context: boolean): boolean {
  let cov = first_coverage(b, st, kind, gs[0]);
  if (cov < 0) {
    return false;
  }
  if (kind == 5 || kind == 6) {
    return context_would_apply(b, st, kind, cov, gs, zero_context);
  }
  if (kind == 4) {
    return ligature_would_apply(b, st, cov, gs);
  }
  return gs.len() == 1 && kind >= 1 && kind <= 8;
}

// Whether a ligature of the set `cov` of the subtable `st` has the parts
// `gs`.
function ligature_would_apply(b: [u8], st: i32, cov: i32, gs: i32[]): boolean {
  if (cov >= u16_at(b, st + 4)) {
    return false;
  }
  let set = st + u16_at(b, st + 6 + 2 * cov);
  for k in 0..u16_at(b, set) {
    let lig = set + u16_at(b, set + 2 + 2 * k);
    if (u16_at(b, lig + 2) == gs.len() && seq_matches(b, Seq { kind: 0, table: 0, at: lig + 4, n: gs.len() - 1 }, gs, 1)) {
      return true;
    }
  }
  return false;
}

// Whether a rule of the context subtable `st` has the input `gs`.
function context_would_apply(b: [u8], st: i32, kind: i32, cov: i32, gs: i32[], zero_context: boolean): boolean {
  let format = u16_at(b, st);
  let set = rule_set(b, st, kind, cov, gs[0], format);
  let chain = kind - 5;
  for k in 0..rule_count(b, set, format) {
    let at = rule_at(b, st, set, format, k);
    let nb = chain * u16_at(b, at);
    let input_count = at + chain * (2 + 2 * nb);
    let n = u16_at(b, input_count) - 1;
    let first = input_count + first_input(chain, format);
    let bare = nb + chain * u16_at(b, first + 2 * n) == 0;
    if ((bare || !zero_context) && n + 1 == gs.len() && seq_matches(b, Seq { kind: (format - 1).min(2), table: rule_table(b, st, format, 4 + 2 * chain), at: first, n: n }, gs, 1)) {
      return true;
    }
  }
  return false;
}

// Whether the glyphs of `gs` from `from` are the values of `s`.
function seq_matches(b: [u8], s: Seq, gs: i32[], from: i32): boolean {
  for k in from..gs.len() {
    if (!seq_has(b, s, k - from, gs[k])) {
      return false;
    }
  }
  return true;
}

// The sorted `xs` with `v`, when it does not hold `v`.
function inserted(own xs: i32[], v: i32): i32[] {
  let n = xs.len();
  let at = n;
  while (at > 0 && xs[at - 1] > v) {
    at -= 1;
  }
  if (at > 0 && xs[at - 1] == v) {
    return xs;
  }
  if (at == n) {
    return xs.append(v);
  }
  xs = xs.append(v);
  let k = n;
  while (k > at) {
    xs = xs.with(k, xs[k - 1]);
    k -= 1;
  }
  return xs.with(at, v);
}

// The lookup `li` of the GSUB or GPOS table at `t`.
function lookup_of(f: Font, t: i32, gpos: boolean, li: i32): Lookup {
  let b = f.data;
  let list = t + u16_at(b, t + 8);
  if (t < 0 || li < 0 || li >= u16_at(b, list)) {
    return Lookup { kind: 0, flag: 0, at: 0, count: 0, set: -1, table: t, gpos: gpos, opts: 0, base: 0, subs: [] };
  }
  let lk = list + u16_at(b, list + 2 + 2 * li);
  let n = u16_at(b, lk + 4);
  let flag = u16_at(b, lk + 2);
  let set = if ((flag & 16) != 0) { u16_at(b, lk + 6 + 2 * n) } else { -1 };
  return Lookup { kind: u16_at(b, lk), flag: flag, at: lk, count: n, set: set, table: t, gpos: gpos, opts: 0, base: 0, subs: [] };
}

// The offset and the type of subtable `i` of `l`. An extension subtable,
// of type 7 in GSUB and 9 in GPOS, gives the subtable that it holds.
function subtable(b: [u8], l: Lookup, i: i32): (i32, i32) {
  let st = l.at + u16_at(b, l.at + 6 + 2 * i);
  if (l.kind == (if (l.gpos) { 9 } else { 7 })) {
    return (st + u32_at(b, st + 4), u16_at(b, st + 2));
  }
  return (st, l.kind);
}

// The GDEF class of glyph `g`: 1 base, 2 ligature, 3 mark, 4 component,
// or 0 when the font gives no class.
pub function (f: Font) glyph_class(g: i32): i32 {
  return gdef_class(f, 4, g);
}

// The class of `g` in the class definition at the offset `field` of GDEF.
function gdef_class(f: Font, field: i32, g: i32): i32 {
  let at = gdef_offset(f, field);
  return if (at == 0) { 0 } else { class_of(f.data, f.gdef + at, g) };
}

function gdef_offset(f: Font, field: i32): i32 {
  return if (f.gdef < 0) { 0 } else { u16_at(f.data, f.gdef + field) };
}

// The offset of the mark glyph sets of GDEF, or -1.
function mark_sets(f: Font): i32 {
  let b = f.data;
  let at = if (f.gdef < 0 || u32_at(b, f.gdef) < 65538) { 0 } else { u16_at(b, f.gdef + 12) };
  return if (at == 0) { -1 } else { f.gdef + at };
}

// Whether GDEF has the mark glyph set `set`. A lookup whose set is not
// there does not filter the marks, as in HarfBuzz.
function has_mark_set(f: Font, set: i32): boolean {
  let sets = mark_sets(f);
  return sets >= 0 && set < u16_at(f.data, sets + 2);
}

// Whether glyph `g` is in the mark glyph set `set` of GDEF, which
// `has_mark_set` holds.
function in_mark_set(f: Font, set: i32, g: i32): boolean {
  let sets = mark_sets(f);
  return coverage(f.data, sets + u32_at(f.data, sets + 4 + 4 * set), g) >= 0;
}

// Whether the GDEF table gives the glyph classes.
pub function (f: Font) has_glyph_classes(): boolean {
  return gdef_offset(f, 4) != 0;
}

// The class of the glyph at `i` of `r`, as `glyph_class` gives it. A font
// with no glyph classes takes the class in the properties of the glyph,
// which the shaper gives and the lookups change as HarfBuzz changes them.
pub function (f: Font) class_at(r: Run, i: i32): i32 {
  let g = r.glyphs[i];
  if (g >= 0 && g < f.classes.len()) {
    return f.classes[g] as i32;
  }
  let at = gdef_offset(f, 4);
  return if (at == 0) { r.props[i] >> CLASS_SHIFT & 3 } else { class_of(f.data, f.gdef + at, g) };
}

// `f` with the class of each glyph as `glyph_class` gives it in a table of
// one byte for each glyph, so that `class_at` does not search GDEF. A class
// above 255 becomes 255, which no lookup flag names. A font with no glyph
// classes is unchanged.
pub function (f: Font) with_glyph_classes(): Font {
  let at = gdef_offset(f, 4);
  if (at == 0) {
    return f;
  }
  let out = __alloc_u8(f.glyphs.max(0));
  for g in 0..out.len() {
    out = out.with(g, class_of(f.data, f.gdef + at, g).min(255) as u8);
  }
  return Font { ...f, classes: out };
}

// The properties `p` with the class `c`.
function with_class(p: i32, c: i32): i32 {
  return p & ~(3 << CLASS_SHIFT) | c << CLASS_SHIFT;
}

// The right-to-left flag alone does not filter any glyph class.
function filters_classes(l: Lookup): boolean {
  return (l.flag & ~1) != 0 || l.set >= 0;
}

// Whether the flags of `l` make it skip the glyph at `i` of `r`.
function skipped(f: Font, l: Lookup, r: Run, i: i32): boolean {
  return filters_classes(l) && skipped_class(f, l, r, i);
}

function skipped_class(f: Font, l: Lookup, r: Run, i: i32): boolean {
  let g = r.glyphs[i];
  let c = f.class_at(r, i);
  if (c == 1 && (l.flag & 2) != 0 || c == 2 && (l.flag & 4) != 0) {
    return true;
  }
  if (c != 3) {
    return false;
  }
  if ((l.flag & 8) != 0) {
    return true;
  }
  if (l.set >= 0 && has_mark_set(f, l.set)) {
    return !in_mark_set(f, l.set, g);
  }
  let kind = l.flag >> 8;
  return kind != 0 && gdef_class(f, 10, g) != kind;
}

// The matcher of the input of `l` from the glyph at `i`, with the mask
// `mask`.
function input_matcher(r: Run, l: Lookup, mask: i32, i: i32): Matcher {
  return Matcher { mask: mask, syllable: syllable_at(r, l, i), zwnj: l.gpos, zwj: (l.opts & MANUAL_ZWJ) == 0 };
}

// The matcher of the glyphs before or after the input of `l` at `i`. They
// stay in the syllable of `i` only with `same_syllable`.
function context_matcher(r: Run, l: Lookup, i: i32, same_syllable: boolean): Matcher {
  let syllable = if (same_syllable) { syllable_at(r, l, i) } else { 0 };
  return Matcher { mask: -1, syllable: syllable, zwnj: l.gpos || (l.opts & MANUAL_ZWNJ) == 0, zwj: true };
}

// The syllable that a GSUB lookup with PER_SYLLABLE keeps to at `i`, or 0.
function syllable_at(r: Run, l: Lookup, i: i32): i32 {
  return if (!l.gpos && (l.opts & PER_SYLLABLE) != 0) { r.props[i] >> SYLLABLE_SHIFT } else { 0 };
}

// The index of the glyph after `i` in the direction `step` that `m` takes,
// or -1. With `k` from 0, the glyph must be the value `k` of `s`. With `k`
// of -1, any glyph that is not skipped matches.
function next_match(f: Font, r: Run, l: Lookup, m: Matcher, i: i32, step: i32, s: Seq, k: i32): i32 {
  let j = i + step;
  while (j >= 0 && j < r.glyphs.len()) {
    let t = match_at(f, r, l, m, j, s, k);
    if (t != 0) {
      return if (t > 0) { j } else { -1 };
    }
    j += step;
  }
  return -1;
}

// What `m` makes of the glyph at `j`, as `next_match` asks it: 1 when the
// glyph matches, 0 when the search skips it, and -1 when it stops the
// search.
function match_at(f: Font, r: Run, l: Lookup, m: Matcher, j: i32, s: Seq, k: i32): i32 {
  if (skipped(f, l, r, j)) {
    return 0;
  }
  let p = r.props[j];
  let maybe = may_skip(p, m, l.gpos);
  let counts = (r.masks[j] & m.mask) != 0 && (m.syllable == 0 || p >> SYLLABLE_SHIFT == m.syllable);
  if (counts && (if (k < 0) { !maybe } else { seq_has(f.data, s, k, r.glyphs[j]) })) {
    return 1;
  }
  return if (maybe) { 0 } else { -1 };
}

// Whether a glyph with the properties `p` is default ignorable. As in
// HarfBuzz, a glyph that a GSUB lookup substituted is not.
pub function is_ignorable(p: i32): boolean {
  return (p & IGNORABLE) != 0 && (p & SUBSTITUTED) == 0;
}

// Whether a glyph with the properties `p` is a default ignorable glyph that
// the search can skip.
function may_skip(p: i32, m: Matcher, gpos: boolean): boolean {
  return is_ignorable(p) && (m.zwnj || (p & ZWNJ) == 0) && (m.zwj || (p & ZWJ) == 0) && (gpos || (p & HIDDEN) == 0);
}

// The index of the glyph after `i` in the direction `step` that `m` takes
// with any value, or -1.
function next_any(f: Font, r: Run, l: Lookup, m: Matcher, i: i32, step: i32): i32 {
  return next_match(f, r, l, m, i, step, Seq { kind: 0, table: 0, at: 0, n: 0 }, -1);
}

function seq_has(b: [u8], s: Seq, k: i32, g: i32): boolean {
  let v = u16_at(b, s.at + 2 * k);
  if (s.kind == 0) {
    return v == g;
  }
  if (s.kind == 1) {
    return class_of(b, s.table, g) == v;
  }
  return coverage(b, s.table + v, g) >= 0;
}

// The positions of the input glyphs that match `s` from the glyph at `i`,
// or an empty list. The glyph at `i` matched already, and `s`
// holds the glyphs after it. As in HarfBuzz, the input does not join marks
// on different parts of an earlier ligature.
function matched_input(f: Font, r: Run, l: Lookup, mask: i32, i: i32, s: Seq): i32[] {
  let none: i32[] = [];
  let m = input_matcher(r, l, mask, i);
  let out = [i];
  let j = i;
  let base = 0;
  for k in 0..s.n {
    j = next_match(f, r, l, m, j, 1, s, k);
    if (j < 0) {
      return none;
    }
    let (fits, b) = same_ligature(f, r, l, i, j, base);
    if (!fits) {
      return none;
    }
    base = b;
    out = out.append(j);
  }
  return out;
}

// Whether the glyph at `j` can be in one input with the glyph at `i`, as
// match_input of HarfBuzz tells it, and what is known of the ligature base:
// 0 when it is not checked, 1 when it cannot be skipped, and 2 when the
// flags of `l` skip it. When `i` is on a part of a ligature, `j` must be on
// the same part, unless the flags skip the base of the ligature. Else `j`
// must not be on a part of another ligature.
function same_ligature(f: Font, r: Run, l: Lookup, i: i32, j: i32, base0: i32): (boolean, i32) {
  let (first_id, first_part) = (lig_id(r.ligs[i]), lig_part(r.ligs[i]));
  let (this_id, this_part) = (lig_id(r.ligs[j]), lig_part(r.ligs[j]));
  if (first_id == 0 || first_part == 0) {
    return (this_id == 0 || this_part == 0 || this_id == first_id, base0);
  }
  if (first_id == this_id && first_part == this_part) {
    return (true, base0);
  }
  let base = if (base0 == 0) { ligature_base(f, r, l, i, first_id) } else { base0 };
  return (base == 2, base);
}

// 2 when a glyph before `i` that is the base of the ligature `id` is
// skipped by the flags of `l`, else 1.
function ligature_base(f: Font, r: Run, l: Lookup, i: i32, id: i32): i32 {
  let k = i;
  while (k > 0 && lig_id(r.ligs[k - 1]) == id) {
    k -= 1;
    if (lig_part(r.ligs[k]) == 0) {
      return if (skipped(f, l, r, k)) { 2 } else { 1 };
    }
  }
  return 1;
}

// Whether the glyphs from `i` in the direction `step` match `s`. With
// `same_syllable` they must be in the syllable of `i`.
function matched_context(f: Font, r: Run, l: Lookup, i: i32, s: Seq, step: i32, same_syllable: boolean): boolean {
  let m = context_matcher(r, l, i, same_syllable);
  let j = i;
  for k in 0..s.n {
    j = next_match(f, r, l, m, j, step, s, k);
    if (j < 0) {
      return false;
    }
  }
  return true;
}

// `l` applied to the glyph at `i` of `r`, and the index of the glyph to
// try next, or -1 when no subtable applies. Each lookup that a context
// applies takes one from `budget`, and so does each SUBTABLES_PER_ATTEMPT
// subtables that the attempt examines. The budget that is left comes last.
function applied(f: Font, own r: Run, l: Lookup, i: i32, mask: i32, budget: i32, digests: [u64]): (Run, i32, i32) {
  let b = f.data;
  for s in 0..l.count {
    if (s / SUBTABLES_PER_ATTEMPT > budget) {
      return (r, -1, 0);
    }
    if (!digest_has(digests, 3 + 3 * s, r.glyphs[i])) {
      continue;
    }
    let prepared = l.subs.len() > 0;
    let (st, kind) = if (prepared) { (l.subs[3 * s], l.subs[3 * s + 1]) } else { subtable(b, l, s) };
    let ctx = context_kind(l, kind);
    let cov = if (!prepared) { first_coverage(b, st, ctx, r.glyphs[i]) } else if (l.subs[3 * s + 2] < 0) { -1 } else { coverage(b, l.subs[3 * s + 2], r.glyphs[i]) };
    if (cov < 0) {
      continue;
    }
    if (ctx != 0) {
      let (next, at, left) = contextual(f, r, l, st, ctx, cov, i, mask, budget);
      if (at >= 0) {
        return (next, at, left - s / SUBTABLES_PER_ATTEMPT);
      }
      r = next;
      continue;
    }
    let (next, at) = if (l.gpos) { positioned(f, r, l, st, kind, cov, i, mask) } else { substituted(f, r, l, st, kind, cov, i, mask) };
    if (at >= 0) {
      return (next, at, budget - s / SUBTABLES_PER_ATTEMPT);
    }
    r = next;
  }
  return (r, -1, budget - l.count / SUBTABLES_PER_ATTEMPT);
}

// The type of a context subtable as GSUB numbers it, 5 or 6, or 0 for a
// subtable that is not a context. In GPOS a context has the type 7 or 8.
function context_kind(l: Lookup, kind: i32): i32 {
  let k = if (l.gpos) { kind - 2 } else { kind };
  return if (k == 5 || k == 6) { k } else { 0 };
}

// Format 3 stores one coverage per input glyph. In a chaining context the
// backtrack offsets precede the input count and its coverage offsets.
function first_coverage_table(b: [u8], st: i32, kind: i32): Option[i32] {
  if ((kind == 5 || kind == 6) && u16_at(b, st) == 3) {
    let count_at = if (kind == 5) { st + 2 } else { st + 4 + 2 * u16_at(b, st + 2) };
    if (u16_at(b, count_at) == 0) {
      return None;
    }
    let first = if (kind == 5) { st + 6 } else { count_at + 2 };
    return Some(st + u16_at(b, first));
  }
  return Some(st + u16_at(b, st + 2));
}

function first_coverage(b: [u8], st: i32, kind: i32, g: i32): i32 {
  let Some(table) = first_coverage_table(b, st, kind) else {
    return -1;
  };
  return coverage(b, table, g);
}

// `r` after the subtable `st` of type 1 to 4 at the glyph at `i`, and the
// index of the glyph to try next, or -1 when the subtable does not apply.
function substituted(f: Font, own r: Run, l: Lookup, st: i32, kind: i32, cov: i32, i: i32, mask: i32): (Run, i32) {
  let (to, used) = substitution(f, r, l, st, kind, cov, i, mask);
  if (used == 0) {
    return (r, -1);
  }
  if (kind == 4) {
    let at = to[to.len() - 1] + 2 - to.len();
    return (ligated(f, r, to, u16_at(f.data, used)), at);
  }
  if (to.len() == 0) {
    return (deleted(r, i), i);
  }
  if (r.glyphs.len() + to.len() > r.max_len) {
    return (r, -1);
  }
  // A ligature that splits into more glyphs gives bases, as in HarfBuzz.
  let split = to.len() > 1 && f.class_at(r, i) == 2;
  let from = if (split) { Run { ...r, props: r.props.with(i, with_class(r.props[i], 1)) } } else { r };
  return (replaced(from, i, to, to.len() > 1), i + to.len());
}

// What the subtable `st` of type 1 to 4 gives at the glyph at `i`, whose
// coverage index is `cov`. Types 1 to 3 give the new glyphs and 1. Type 4
// gives the positions of the parts of the ligature and the offset of the
// ligature. It gives 0 when the subtable does not apply.
function substitution(f: Font, r: Run, l: Lookup, st: i32, kind: i32, cov: i32, i: i32, mask: i32): (i32[], i32) {
  let b = f.data;
  let g = r.glyphs[i];
  let format = u16_at(b, st);
  let none: i32[] = [];
  if (kind == 1 && format == 1) {
    return ([g + i16_at(b, st + 4) & 65535], 1);
  }
  if (kind < 1 || kind > 4 || cov >= u16_at(b, st + 4)) {
    return (none, 0);
  }
  let at = st + u16_at(b, st + 6 + 2 * cov);
  if (kind == 1) {
    return ([u16_at(b, st + 6 + 2 * cov)], 1);
  }
  if (kind == 2) {
    // A sequence that the run has no room for does not apply. Its glyphs
    // are not read: a font can give 65535 of them for each glyph of the run.
    if (r.glyphs.len() + u16_at(b, at) > r.max_len) {
      return (none, 0);
    }
    return (values(b, at), 1);
  }
  if (kind == 4) {
    return ligature_parts(f, r, l, at, i, mask);
  }
  return if (u16_at(b, at) > 0) { ([u16_at(b, at + 2)], 1) } else { (none, 0) };
}

// The values of 16 bits after their count at `at`.
function values(b: [u8], at: i32): i32[] {
  let out: i32[] = [];
  for k in 0..u16_at(b, at) {
    out = out.append(u16_at(b, at + 2 + 2 * k));
  }
  return out;
}

// The positions of the parts of the first ligature of the set at `at`
// that matches from the glyph at `i`, and the offset of that ligature.
function ligature_parts(f: Font, r: Run, l: Lookup, at: i32, i: i32, mask: i32): (i32[], i32) {
  let b = f.data;
  for k in 0..u16_at(b, at) {
    let lig = at + u16_at(b, at + 2 + 2 * k);
    let ps = matched_input(f, r, l, mask, i, Seq { kind: 0, table: 0, at: lig + 4, n: u16_at(b, lig + 2) - 1 });
    if (ps.len() > 0) {
      return (ps, lig);
    }
  }
  let none: i32[] = [];
  return (none, 0);
}

// `r` with the glyph at `i` replaced with `to`. The new glyphs take the
// cluster, the mask, the values and the ligature of the glyph at `i`, and
// they are SUBSTITUTED. With `multiplied`, they are MULTIPLIED too, and
// each one is a part of the glyph when the glyph is not in a ligature.
function replaced(own r: Run, i: i32, to: i32[], multiplied: boolean): Run {
  let gs = r.glyphs.take(i);
  let cs = r.clusters.take(i);
  let ms = r.masks.take(i);
  let ls = r.ligs.take(i);
  let ps = r.props.take(i);
  let vs = r.vars.take(i);
  let parts = multiplied && lig_id(r.ligs[i]) == 0;
  let prop = r.props[i] | SUBSTITUTED | (if (multiplied) { MULTIPLIED } else { 0 });
  for k in 0..to.len() {
    gs = gs.append(to[k]);
    cs = cs.append(r.clusters[i]);
    ms = ms.append(r.masks[i]);
    ls = ls.append(if (parts) { k & 15 } else { r.ligs[i] });
    ps = ps.append(prop);
    vs = vs.append(r.vars[i]);
  }
  let rest = i + 1;
  return Run { ...r, glyphs: gs.concat(r.glyphs.drop(rest)), clusters: cs.concat(r.clusters.drop(rest)), masks: ms.concat(r.masks.drop(rest)), ligs: ls.concat(r.ligs.drop(rest)), props: ps.concat(r.props.drop(rest)), vars: vs.concat(r.vars.drop(rest)) };
}

// The ligature id, the part and the count of parts of the ligature value
// `p` of a glyph, as HarfBuzz reads them. A glyph that is not a ligature
// has one part.
pub function lig_id(p: i32): i32 {
  return p >> 5;
}

// The component of a ligature that a glyph with the ligature value `p` is
// on, from 1, or 0.
pub function lig_part(p: i32): i32 {
  return if ((p & 16) != 0) { 0 } else { p & 15 };
}

// The count of parts of the glyph at `q` of `r`.
pub function lig_parts(f: Font, r: Run, q: i32): i32 {
  let p = r.ligs[q];
  return if (f.class_at(r, q) == 2 && (p & 16) != 0) { p & 15 } else { 1 };
}

// The count of parts that the glyph at `q` of `r` adds to a ligature after
// its first glyph. As in HarfBuzz, a later part of a multiple substitution
// adds none: it is on the part of the first one.
function parts_in_ligature(f: Font, r: Run, q: i32): i32 {
  return if ((r.props[q] & MULTIPLIED) != 0 && lig_part(r.ligs[q]) != 0) { 0 } else { lig_parts(f, r, q) };
}

// `r` with no glyph at `i`. When no glyph next to it has its cluster, the
// cluster moves as HarfBuzz moves it: to the glyphs before it that have a
// later cluster, or at the start, to the glyphs after it.
function deleted(own r: Run, i: i32): Run {
  let cl = r.clusters[i];
  let out = replaced(r, i, [], false);
  if (out.glyphs.len() == 0) {
    return out;
  }
  let at = if (i > 0) { i - 1 } else { 0 };
  let old = out.clusters[at];
  let shared = old == cl || i > 0 && i < out.glyphs.len() && out.clusters[i] == cl;
  if (shared || i > 0 && cl >= old) {
    return out;
  }
  let step = if (i > 0) { -1 } else { 1 };
  return Run { ...out, clusters: merged(out.clusters, at, step, old, cl.min(old)) };
}

// `cs` with the cluster `cl` in place of `old`, from `at` in the
// direction `step` while the clusters are `old`.
function merged(cs: i32[], at: i32, step: i32, old: i32, cl: i32): i32[] {
  let out = cs;
  let j = at;
  while (j >= 0 && j < out.len() && out[j] == old) {
    out = out.with(j, cl);
    j += step;
  }
  return out;
}

// `r` with the glyphs at the positions `ps` made one ligature, from the
// ligature table at `lig`. The glyphs that the lookup skipped between them
// come after the ligature. The clusters merge as `merged_clusters` merges
// them. The ligature values change as HarfBuzz changes them.
function ligated(f: Font, own r: Run, ps: i32[], lig: i32): Run {
  let first = ps[0];
  let merged = merged_clusters(r.clusters, first, ps[ps.len() - 1] + 1);
  let (serial, props) = ligature_props(f, r, ps);
  let gs: i32[] = [];
  let cs: i32[] = [];
  let ms: i32[] = [];
  let ls: i32[] = [];
  let qs: i32[] = [];
  let vs: i32[] = [];
  let k = 1;
  for j in 0..r.glyphs.len() {
    if (k < ps.len() && ps[k] == j) {
      k += 1;
      continue;
    }
    gs = gs.append(if (j == first) { lig } else { r.glyphs[j] });
    cs = cs.append(merged[j]);
    ms = ms.append(r.masks[j]);
    ls = ls.append(props[j]);
    qs = qs.append(if (j == first) { lig_class((r.props[j] | SUBSTITUTED | LIGATED) & ~MULTIPLIED, serial != r.serial) } else { r.props[j] });
    vs = vs.append(r.vars[j]);
  }
  return Run { ...r, glyphs: gs, clusters: cs, masks: ms, ligs: ls, props: qs, vars: vs, serial: serial };
}

// `cs` with the clusters from `start` to `end` merged, as the
// merge_clusters of HarfBuzz merges them when a lookup ligates: each takes
// the least of them. The merge goes on over the glyphs after `end` that
// have the cluster of the last one, and over those before `start` that
// have the cluster of the first one, when that cluster changes.
pub function merged_clusters(cs0: i32[], start: i32, end: i32): i32[] {
  let cs = cs0;
  let cl = cs[start];
  for j in start + 1..end {
    cl = cl.min(cs[j]);
  }
  let (a, b) = (start, end);
  if (cl != cs[b - 1]) {
    while (b < cs.len() && cs[b - 1] == cs[b]) {
      b += 1;
    }
  }
  if (cl != cs[a]) {
    while (a > 0 && cs[a - 1] == cs[start]) {
      a -= 1;
    }
  }
  for j in a..b {
    cs = cs.with(j, cl);
  }
  return cs;
}

// The serial of `r` after the glyphs at `ps` become a ligature, and the
// ligature values. A base or a mark with marks after it is not a ligature
// to HarfBuzz, and takes no new id.
function ligature_props(f: Font, r: Run, ps: i32[]): (i32, i32[]) {
  let marks = true;
  for k in 1..ps.len() {
    marks = marks && f.class_at(r, ps[k]) == 3;
  }
  let c0 = f.class_at(r, ps[0]);
  let is_lig = !marks || c0 != 1 && c0 != 3;
  let serial = if (is_lig) { next_serial(r.serial) } else { r.serial };
  return (serial, lig_props(f, r, ps, if (is_lig) { serial & 7 } else { 0 }, marks && c0 == 3));
}

// The properties `p` of a new ligature glyph. A glyph that is not a base or
// a mark with marks after it takes the class of a ligature.
function lig_class(p: i32, is_lig: boolean): i32 {
  return if (is_lig) { with_class(p, 2) } else { p };
}

// The serial after `serial` that gives a ligature id from 1 to 7.
function next_serial(serial: i32): i32 {
  let s = serial + 1;
  return if ((s & 7) == 0) { s + 1 } else { s };
}

// The ligature values of `r` when the glyphs at `ps` become the ligature
// `id`, or 0 when they are a base and its marks. A skipped mark between two
// parts takes the part before it, and so do the marks after the last part.
function lig_props(f: Font, r: Run, ps: i32[], id: i32, mark_lig: boolean): i32[] {
  let ls = r.ligs;
  let total = lig_parts(f, r, ps[0]);
  for k in 1..ps.len() {
    total += parts_in_ligature(f, r, ps[k]);
  }
  let out = if (id != 0) { ls.with(ps[0], id << 5 | 16 | total & 15) } else { ls };
  let last_n = lig_parts(f, r, ps[0]);
  let so_far = last_n;
  for k in 1..ps.len() {
    for j in ps[k - 1] + 1..ps[k] {
      let c = lig_part(ls[j]);
      let cur = if (c == 0) { last_n } else { c };
      let part = so_far - last_n + cur.min(last_n);
      out = if (id != 0) { out.with(j, id << 5 | part & 15) } else { out };
    }
    last_n = parts_in_ligature(f, r, ps[k]);
    so_far += last_n;
  }
  let last_id = lig_id(ls[ps[ps.len() - 1]]);
  if (mark_lig || last_id == 0) {
    return out;
  }
  return following_parts(out, ls, ps[ps.len() - 1] + 1, last_id, id, so_far - last_n, last_n);
}

// `out` with the marks from `from` that belong to the ligature `last_id`
// moved to the ligature `id`, from the part after `base`.
function following_parts(out: i32[], ls: i32[], from: i32, last_id: i32, id: i32, base: i32, last_n: i32): i32[] {
  let res = out;
  let j = from;
  while (j < ls.len() && lig_id(ls[j]) == last_id && lig_part(ls[j]) != 0) {
    res = res.with(j, id << 5 | base + lig_part(ls[j]).min(last_n) & 15);
    j += 1;
  }
  return res;
}

// `r` after the first rule of the context subtable at `st` that matches at
// `i`, and the index of the glyph after its input, or -1. It reads each part
// of a rule only when the parts before it match. `chain` is 1 in a chaining
// context, of `kind` 6, and 0 in a context, which has its input only.
function contextual(f: Font, own r: Run, l: Lookup, st: i32, kind: i32, cov: i32, i: i32, mask: i32, budget: i32): (Run, i32, i32) {
  let b = f.data;
  let format = u16_at(b, st);
  let set = rule_set(b, st, kind, cov, r.glyphs[i], format);
  let chain = kind - 5;
  let values = (format - 1).min(2);
  let first = first_input(chain, format);
  // Each part of a rule needs one glyph for each of its values.
  let after = r.glyphs.len() - 1 - i;
  for k in 0..rule_count(b, set, format) {
    let at = rule_at(b, st, set, format, k);
    let nb = chain * u16_at(b, at);
    let input_count = at + chain * (2 + 2 * nb);
    let n = u16_at(b, input_count) - 1;
    // A chaining context has the count of the glyphs after the input here.
    // A context has its lookup records here.
    let end = input_count + first + 2 * n;
    let na = chain * u16_at(b, end);
    // HarfBuzz keeps the glyphs before the input in the syllable only while
    // the lookup has not changed the count of glyphs, and the glyphs after
    // it only after an input of one glyph.
    if (nb > i || n + na > after || nb > 0 && !matched_context(f, r, l, i, Seq { kind: values, table: rule_table(b, st, format, 4), at: at + 2, n: nb }, -1, r.glyphs.len() == l.base)) {
      continue;
    }
    let ahead = Seq { kind: values, table: rule_table(b, st, format, 8), at: end + 2, n: na };
    // An input of one glyph is the glyph at `i`, so the glyphs after it can
    // refuse the rule before the input makes its list of positions.
    if (n == 0 && na > 0 && !matched_context(f, r, l, i, ahead, 1, true)) {
      continue;
    }
    let ps = matched_input(f, r, l, mask, i, Seq { kind: values, table: rule_table(b, st, format, 4 + 2 * chain), at: input_count + first, n: n });
    if (ps.len() == 0 || n > 0 && na > 0 && !matched_context(f, r, l, ps[ps.len() - 1], ahead, 1, false)) {
      continue;
    }
    let records = end + chain * (4 + 2 * na);
    return records_applied(f, r, l, ps, records, u16_at(b, if (chain == 1) { records - 2 } else { at + 2 }), mask, budget);
  }
  return (r, -1, budget);
}

// The rule-set offset for glyph g in context formats 1 and 2, or zero
// when absent. Format 3 stores its single rule directly in the subtable.
function rule_set(b: [u8], st: i32, kind: i32, cov: i32, g: i32, format: i32): i32 {
  let sets = st + 6;
  let index = cov;
  if (format == 2) {
    let input = if (kind == 5) { st + 4 } else { st + 6 };
    index = class_of(b, st + u16_at(b, input), g);
    sets += if (kind == 5) { 2 } else { 6 };
  } else if (format != 1) {
    return 0;
  }
  if (index >= u16_at(b, sets - 2)) {
    return 0;
  }
  let offset = u16_at(b, sets + 2 * index);
  return if (offset == 0) { 0 } else { st + offset };
}

function rule_count(b: [u8], set: i32, format: i32): i32 {
  if (format == 3) {
    return 1;
  }
  return if (set == 0) { 0 } else { u16_at(b, set) };
}

// The offset of the first count of rule `k` of the set at `set`. A subtable
// of format 3 is its one rule.
function rule_at(b: [u8], st: i32, set: i32, format: i32, k: i32): i32 {
  return if (format == 3) { st + 2 } else { set + u16_at(b, set + 2 + 2 * k) };
}

// How far the first input value of a rule is after the count of its input.
// A context has the count of its lookup records between them. Format 3 has
// the coverage of the first glyph, which matched already.
function first_input(chain: i32, format: i32): i32 {
  return 4 - 2 * chain + (if (format == 3) { 2 } else { 0 });
}

// The table that the values of a rule of the subtable `st` refer to: the
// class definition whose offset is at `field` in format 2, and the subtable
// in format 3.
function rule_table(b: [u8], st: i32, format: i32, field: i32): i32 {
  return if (format == 2) { st + u16_at(b, st + field) } else if (format == 3) { st } else { 0 };
}

// `r` after the `count` lookup records at `records`, at the input positions
// `ps`.
// When a lookup changes the count of glyphs, the positions after it move,
// as HarfBuzz moves them. The index is that of the glyph after the input.
function records_applied(f: Font, own r: Run, l: Lookup, ps0: i32[], records: i32, count: i32, mask: i32, budget: i32): (Run, i32, i32) {
  let b = f.data;
  let ps = ps0;
  let end = ps[ps.len() - 1] + 1;
  let run = r;
  let left = budget;
  for k in 0..count {
    let idx = u16_at(b, records + 4 * k);
    if (left <= 0) {
      break;
    }
    if (idx >= ps.len()) {
      continue;
    }
    let before = run.glyphs.len();
    let nested = Lookup { ...lookup_of(f, l.table, l.gpos, u16_at(b, records + 4 * k + 2)), opts: l.opts, base: l.base };
    let (next, at, rest) = applied(f, run, nested, ps[idx], mask, left - 1, []);
    run = next;
    left = rest;
    let delta = run.glyphs.len() - before;
    if (at < 0 || delta == 0) {
      continue;
    }
    end += delta;
    if (end < ps[idx]) {
      delta += ps[idx] - end;
      end = ps[idx];
    }
    ps = moved_positions(ps, idx, delta);
  }
  return (run, end, left);
}

// The positions `ps` after the lookup at position `idx` adds `delta`
// glyphs, or removes them when `delta` is negative.
function moved_positions(ps: i32[], idx: i32, delta: i32): i32[] {
  let out: i32[] = [];
  for j in 0..idx + 1 {
    out = out.append(ps[j]);
  }
  for t in 1..delta + 1 {
    out = out.append(ps[idx] + t);
  }
  let from = if (delta > 0) { idx + 1 } else { (idx + 1 - delta).min(ps.len()) };
  for j in from..ps.len() {
    out = out.append(ps[j] + delta);
  }
  return out;
}

// Cache a lookup and its first-glyph coverage for use across text runs.
pub struct PreparedLookup { font: Font, lookup: Lookup, reverse: boolean, digests: u64[] }

pub function (f: Font) prepare_lookup(li: i32, gpos: boolean): PreparedLookup {
  let table = if (gpos) { f.gpos } else { f.gsub };
  let l = lookup_of(f, table, gpos, li);
  let reverse = !gpos && l.count > 0 && subtable(f.data, l, 0).1 == 8;
  let facts = Lookup { ...l, subs: subtable_facts(f.data, l) };
  return PreparedLookup { font: f, lookup: facts, reverse: reverse, digests: lookup_digests(f.data, facts) };
}

// Three values for each subtable of `l`: its offset, its type, and the
// offset of its first coverage table, or -1 when it has none.
function subtable_facts(b: [u8], l: Lookup): i32[] {
  let out: i32[] = [];
  for s in 0..l.count {
    let (st, kind) = subtable(b, l, s);
    let cov = match (first_coverage_table(b, st, context_kind(l, kind))) { Some(c) => c, None => -1 };
    out = out.append(st).append(kind).append(cov);
  }
  return out;
}

// A digest is three words. Each word has one bit for each value of six bits
// of a glyph: the bits from 0, from 4 and from 9, as in HarfBuzz. A glyph that
// a coverage holds has its bit in each word, so a clear bit in one word tells
// that the coverage does not hold the glyph. A set bit in each word allows an
// exact coverage check, and a collision only costs that check.
//
// The first digest is for the whole lookup. For a lookup with several
// subtables, one digest for each subtable scanned within the shared budget
// comes after it. If the budget runs out, the first digest holds each glyph
// and the remaining subtables use exact coverage checks. `l` has the facts
// of `subtable_facts`.
function lookup_digests(b: [u8], l: Lookup): u64[] {
  let full = 0xffffffffffffffffu64;
  let out: u64[] = [0u64, 0u64, 0u64];
  let left = 256;
  if (l.count > left) {
    return [full, full, full];
  }
  for sub in 0..l.count {
    let bits: u64[] = [0u64, 0u64, 0u64];
    let cov = l.subs[3 * sub + 2];
    if (cov >= 0) {
      let fmt = u16_at(b, cov);
      let n = u16_at(b, cov + 2);
      if (fmt != 1 && fmt != 2 || n > left) {
        return out.with(0, full).with(1, full).with(2, full);
      }
      left -= n;
      for k in 0..n {
        let lo = u16_at(b, cov + 4 + (if (fmt == 1) { 2 } else { 6 }) * k);
        let hi = if (fmt == 1) { lo } else { u16_at(b, cov + 6 + 6 * k) };
        if (hi < lo) {
          return out.with(0, full).with(1, full).with(2, full);
        }
        for w in 0..3 {
          bits = bits.with(w, bits[w] | digest_bits(lo, hi, DIGEST_SHIFTS[w]));
        }
      }
    }
    for w in 0..3 {
      out = out.with(w, out[w] | bits[w]);
    }
    if (l.count > 1) {
      out = out.concat(bits);
    }
  }
  return out;
}

const DIGEST_SHIFTS: i32[] = [0, 4, 9];

// The bits of one word of a digest for the glyphs from `lo` to `hi`.
function digest_bits(lo: i32, hi: i32, shift: i32): u64 {
  let a = lo >> shift;
  let n = (hi >> shift) - a + 1;
  if (n >= 64) {
    return 0xffffffffffffffffu64;
  }
  return ((1u64 << n as u64) - 1u64).rotate_left(a & 63);
}

// Whether the digest at `at` of `ds` can hold the glyph `g`. A digest that
// `ds` does not have holds each glyph.
function digest_has(ds: [u64], at: i32, g: i32): boolean {
  if (at + 2 >= ds.len()) {
    return true;
  }
  return (ds[at] & 1u64 << (g & 63) as u64) != 0u64 && (ds[at + 1] & 1u64 << (g >> 4 & 63) as u64) != 0u64 && (ds[at + 2] & 1u64 << (g >> 9 & 63) as u64) != 0u64;
}

pub function (p: PreparedLookup) apply(r: Run, mask: i32, opts: i32): Run {
  if (!p.reverse && p.digests.len() >= 3) {
    let tried = idle_attempts(p, r, mask, p.digests[0], p.digests[1], p.digests[2]);
    if (tried >= 0) {
      return with_attempts_taken(r, tried);
    }
  }
  return applied_prepared(p, r, mask, opts);
}

// `r` after each lookup of `ps` in turn, with the mask `masks[k]` and the
// flags `opts[k]`, as `apply` gives it. A digest of the glyphs of the run
// tells that a lookup holds none of them with no look at each glyph. Such a
// lookup still takes its attempts, and lookups in sequence with one mask and
// one class filter take the same count.
pub function apply_each(ps: PreparedLookup[], r: Run, masks: i32[], opts: i32[]): Run {
  let run = r;
  let known = false;
  let (r0, r4, r9) = (0u64, 0u64, 0u64);
  let (of_mask, of_flag, of_set, counted) = (0, 0, 0, -1);
  let owed = 0;
  for k in 0..ps.len() {
    let p = ps[k];
    let ds = p.digests;
    let tried = -1;
    if (ds.len() >= 3 && !p.reverse) {
      if (!known) {
        r0 = 0u64;
        r4 = 0u64;
        r9 = 0u64;
        for g in run.glyphs {
          r0 |= 1u64 << (g & 63) as u64;
          r4 |= 1u64 << (g >> 4 & 63) as u64;
          r9 |= 1u64 << (g >> 9 & 63) as u64;
        }
        known = true;
        counted = -1;
      }
      if ((r0 & ds[0]) != 0u64 && (r4 & ds[1]) != 0u64 && (r9 & ds[2]) != 0u64) {
        tried = idle_attempts(p, run, masks[k], ds[0], ds[1], ds[2]);
      } else {
        let flag = p.lookup.flag;
        let set = p.lookup.set;
        if (counted < 0 || of_mask != masks[k] || of_flag != flag || of_set != set) {
          of_mask = masks[k];
          of_flag = flag;
          of_set = set;
          counted = idle_attempts(p, run, of_mask, 0u64, 0u64, 0u64);
        }
        tried = counted;
      }
    }
    if (tried >= 0) {
      owed += tried;
      continue;
    }
    run = applied_prepared(p, with_attempts_taken(run, owed), masks[k], opts[k]);
    owed = 0;
    known = false;
  }
  return with_attempts_taken(run, owed);
}

// `r` after `p` at each glyph that it tries.
function applied_prepared(p: PreparedLookup, r: Run, mask: i32, opts: i32): Run {
  let l = Lookup { ...p.lookup, opts: opts, base: r.glyphs.len() };
  if (p.reverse) {
    return reverse_substituted(p.font, r, l, mask);
  }
  return applied_to_each(p.font, r, l, mask, p.digests);
}

// `r` after a lookup that applied at no glyph took `tried` attempts.
function with_attempts_taken(r: Run, tried: i32): Run {
  if (tried == 0 || r.ops <= 0) {
    return r;
  }
  return Run { ...r, ops: (r.ops - tried).max(0) };
}

// The attempts that `applied_to_each` takes from the run when the digest
// `d0`, `d4`, `d9` holds no glyph that the lookup of `p` tries, or -1 when it
// holds one. Each glyph that the lookup tries takes one attempt there.
function idle_attempts(p: PreparedLookup, r: Run, mask: i32, d0: u64, d4: u64, d9: u64): i32 {
  let filter = filters_classes(p.lookup);
  let tried = 0;
  for i in 0..r.glyphs.len() {
    if ((r.masks[i] & mask) == 0 || filter && skipped_class(p.font, p.lookup, r, i)) {
      continue;
    }
    let g = r.glyphs[i];
    if ((d0 >> (g & 63) as u64 & d4 >> (g >> 4 & 63) as u64 & d9 >> (g >> 9 & 63) as u64 & 1u64) != 0u64) {
      return -1;
    }
    tried += 1;
  }
  return tried;
}

// `r` after the GSUB lookup `li` at each glyph whose mask has a bit of
// `mask`, with the flags `opts`.
pub function (f: Font) substitute(r: Run, li: i32, mask: i32, opts: i32): Run {
  let l = Lookup { ...lookup_of(f, f.gsub, false, li), opts: opts, base: r.glyphs.len() };
  if (l.count > 0 && subtable(f.data, l, 0).1 == 8) {
    return reverse_substituted(f, r, l, mask);
  }
  return applied_to_each(f, r, l, mask, []);
}

// `r` after the GPOS lookup `li` at each glyph whose mask has a bit of
// `mask`, with the flags `opts`. `r` has its advances from `with_advances`.
pub function (f: Font) position(r: Run, li: i32, mask: i32, opts: i32): Run {
  return applied_to_each(f, r, Lookup { ...lookup_of(f, f.gpos, true, li), opts: opts, base: r.glyphs.len() }, mask, []);
}

// `r` after `l` at each glyph whose mask has a bit of `mask`, from the first
// glyph to the last. Each glyph that `l` tries takes one from the attempts of
// the run, and the lookup stops when none are left.
function applied_to_each(f: Font, r: Run, l: Lookup, mask: i32, digests: [u64]): Run {
  let run = r;
  let budget = r.ops;
  let filter = filters_classes(l);
  let i = 0;
  while (i < run.glyphs.len() && budget > 0) {
    if ((run.masks[i] & mask) == 0 || filter && skipped_class(f, l, run, i)) {
      i += 1;
      continue;
    }
    if (!digest_has(digests, 0, run.glyphs[i])) {
      budget -= 1;
      i += 1;
      continue;
    }
    let (next, at, left) = applied(f, run, l, i, mask, budget - 1, digests);
    run = next;
    budget = left;
    i = if (at < 0) { i + 1 } else { at };
  }
  return Run { ...run, ops: budget };
}

// `r` after a lookup of type 8. It goes from the last glyph to the first,
// and replaces one glyph at a time. Each glyph that the lookup tries takes
// one from the attempts of the run, and one more for each
// SUBTABLES_PER_ATTEMPT subtables that it examines. The lookup stops when
// none are left.
function reverse_substituted(f: Font, r: Run, l: Lookup, mask: i32): Run {
  let b = f.data;
  let run = r;
  let budget = r.ops;
  let i = run.glyphs.len() - 1;
  while (i >= 0 && budget > 0) {
    let g = run.glyphs[i];
    if ((run.masks[i] & mask) != 0 && !skipped(f, l, run, i)) {
      let examined = l.count;
      for s in 0..l.count {
        if (s / SUBTABLES_PER_ATTEMPT > budget) {
          examined = s;
          break;
        }
        let st = subtable(b, l, s).0;
        let cov = coverage(b, st + u16_at(b, st + 2), g);
        let nb = u16_at(b, st + 4);
        let na = st + 6 + 2 * nb;
        let subs = na + 2 + 2 * u16_at(b, na);
        let back = Seq { kind: 2, table: st, at: st + 6, n: nb };
        let ahead = Seq { kind: 2, table: st, at: na + 2, n: u16_at(b, na) };
        if (cov >= 0 && cov < u16_at(b, subs) && matched_context(f, run, l, i, back, -1, true) && matched_context(f, run, l, i, ahead, 1, true)) {
          run = replaced(run, i, [u16_at(b, subs + 2 + 2 * cov)], false);
          examined = s;
          break;
        }
      }
      budget = budget - 1 - examined / SUBTABLES_PER_ATTEMPT;
    }
    i -= 1;
  }
  return Run { ...run, ops: budget };
}
```

### `positioning`

The GPOS table moves the glyphs of a run. A lookup changes the advance of a glyph and its offset from its pen, alone or with the glyph after it. A cursive lookup joins the exit of a glyph to the entry of the glyph after it. A mark lookup puts the anchor of a mark on an anchor of the glyph before it. That glyph is a base, a ligature, or a mark. The context lookups apply other lookups, as in GSUB.

A lookup that attaches a glyph keeps the distance to the glyph that it attaches to. A mark takes the y offset of that glyph when it attaches. Then `std/shape` adds the x offset of the glyph that a mark attaches to, as HarfBuzz 15 does. It adds the y offset for a cursive join.

```fern
<<positioning>>=
// `r` with the advance of each glyph from the font, no offsets and no
// attachments. `rtl` is true for a run that goes from right to left.
pub function (f: Font) with_advances(r: Run, rtl: boolean): Run {
  let advances = __alloc_i32(r.glyphs.len());
  for i in 0..r.glyphs.len() {
    advances = advances.with(i, f.advance(r.glyphs[i]));
  }
  let none = __alloc_i32(r.glyphs.len());
  return Run { ...r, pos: Positions { advances: advances, dx: none, dy: none, chain: none, attach: none, rtl: rtl } };
}

// `r` after the GPOS subtable `st` of type 1 to 6 at the glyph at `i`, whose
// coverage index is `cov`, and the index of the glyph to try next, or -1
// when the subtable does not apply.
function positioned(f: Font, own r: Run, l: Lookup, st: i32, kind: i32, cov: i32, i: i32, mask: i32): (Run, i32) {
  if (kind == 1) {
    return single_placed(f, r, st, cov, i);
  }
  if (kind == 2) {
    return pair_placed(f, r, l, st, cov, i, mask);
  }
  if (kind == 3) {
    return cursive_placed(f, r, l, st, cov, i, mask);
  }
  if (kind >= 4 && kind <= 6) {
    return mark_placed(f, r, l, st, kind, cov, i, mask);
  }
  return (r, -1);
}

// `p` with the value record of `format` at `at` added to the glyph at `i`.
function valued(own p: Positions, b: [u8], at: i32, format: i32, i: i32): Positions {
  let dx = p.dx.with(i, p.dx[i] + value_field(b, at, format, 1));
  let dy = p.dy.with(i, p.dy[i] + value_field(b, at, format, 2));
  return Positions { ...p, advances: p.advances.with(i, p.advances[i] + value_field(b, at, format, 4)), dx: dx, dy: dy };
}

// A single adjustment: one value record for each glyph, or one for all.
function single_placed(f: Font, own r: Run, st: i32, cov: i32, i: i32): (Run, i32) {
  let b = f.data;
  let format = u16_at(b, st);
  let vf = u16_at(b, st + 4);
  if (format != 1 && (format != 2 || cov >= u16_at(b, st + 6))) {
    return (r, -1);
  }
  let at = if (format == 1) { st + 6 } else { st + 8 + cov * value_size(vf) };
  return (Run { ...r, pos: valued(r.pos, b, at, vf, i) }, i + 1);
}

// A pair adjustment of the glyph at `i` and the next glyph that `l` does
// not skip. The next glyph to try is the second one, or the glyph after
// it when the second one has a value record.
function pair_placed(f: Font, own r: Run, l: Lookup, st: i32, cov: i32, i: i32, mask: i32): (Run, i32) {
  let b = f.data;
  let j = next_any(f, r, l, input_matcher(r, l, mask, i), i, 1);
  if (j < 0) {
    return (r, -1);
  }
  let vf1 = u16_at(b, st + 4);
  let vf2 = u16_at(b, st + 6);
  let s1 = value_size(vf1);
  let at = pair_record(b, st, cov, r.glyphs[i], r.glyphs[j], s1 + value_size(vf2));
  if (at < 0) {
    return (r, -1);
  }
  let p = valued(valued(r.pos, b, at, vf1, i), b, at + s1, vf2, j);
  return (Run { ...r, pos: p }, if (vf2 != 0) { j + 1 } else { j });
}

// The x and y of the anchor table at `at`, of any format. The contour
// point of format 2 and the device tables of format 3 have no effect.
function anchor(b: [u8], at: i32): (i32, i32) {
  return (i16_at(b, at + 2), i16_at(b, at + 4));
}

// The offset of the anchor at `field` of the entry and exit record `k` of
// the cursive subtable at `st`, or -1. The entry is at 0 and the exit at 2.
function cursive_anchor(b: [u8], st: i32, k: i32, field: i32): i32 {
  if (k < 0 || k >= u16_at(b, st + 4)) {
    return -1;
  }
  let at = u16_at(b, st + 6 + 4 * k + field);
  return if (at == 0) { -1 } else { st + at };
}

// A cursive attachment: the entry of the glyph at `i` joins the exit of the
// glyph before it, as HarfBuzz joins them.
function cursive_placed(f: Font, own r: Run, l: Lookup, st: i32, cov: i32, i: i32, mask: i32): (Run, i32) {
  let b = f.data;
  let h = next_any(f, r, l, input_matcher(r, l, mask, i), i, -1);
  let entry = cursive_anchor(b, st, cov, 0);
  let exit = if (h < 0) { -1 } else { cursive_anchor(b, st, coverage(b, st + u16_at(b, st + 2), r.glyphs[h]), 2) };
  if (entry < 0 || exit < 0) {
    return (r, -1);
  }
  let (ex, ey) = anchor(b, exit);
  let (nx, ny) = anchor(b, entry);
  let p = r.pos;
  if (p.rtl) {
    let d = ex + p.dx[h];
    p = Positions { ...p, advances: p.advances.with(h, p.advances[h] - d).with(i, nx + p.dx[i]), dx: p.dx.with(h, p.dx[h] - d) };
  } else {
    let d = nx + p.dx[i];
    p = Positions { ...p, advances: p.advances.with(h, ex + p.dx[h]).with(i, p.advances[i] - d), dx: p.dx.with(i, p.dx[i] - d) };
  }
  let right_to_left = (l.flag & 1) != 0;
  let child = if (right_to_left) { h } else { i };
  let parent = if (right_to_left) { i } else { h };
  let y = if (right_to_left) { ny - ey } else { ey - ny };
  p = reversed_cursive(p, child, parent);
  p = Positions { ...p, attach: p.attach.with(child, 2), chain: p.chain.with(child, parent - child), dy: p.dy.with(child, y) };
  if (p.chain[parent] == -p.chain[child]) {
    p = Positions { ...p, chain: p.chain.with(parent, 0), dy: p.dy.with(parent, 0) };
  }
  return (Run { ...r, pos: p }, i + 1);
}

// `p` with the cursive chain from `i` turned the other way, up to the glyph
// `parent`, so that `i` can attach to `parent`.
function reversed_cursive(own p: Positions, i: i32, parent: i32): Positions {
  let chain = p.chain[i];
  if (chain == 0 || p.attach[i] != 2) {
    return p;
  }
  let out = Positions { ...p, chain: p.chain.with(i, 0) };
  let j = i + chain;
  if (j == parent || j < 0 || j >= p.chain.len()) {
    return out;
  }
  out = reversed_cursive(out, j, parent);
  return Positions { ...out, dy: out.dy.with(j, -out.dy[i]), chain: out.chain.with(j, -chain), attach: out.attach.with(j, 2) };
}

// A mark attachment of the mark at `i`: of type 4 to a base, of type 5 to a
// part of a ligature, and of type 6 to the mark before it.
function mark_placed(f: Font, own r: Run, l: Lookup, st: i32, kind: i32, cov: i32, i: i32, mask: i32): (Run, i32) {
  let b = f.data;
  let k = mark_target(f, r, l, st, kind, i, input_matcher(r, l, mask, i));
  let idx = if (k < 0) { -1 } else { coverage(b, st + u16_at(b, st + 4), r.glyphs[k]) };
  let classes = u16_at(b, st + 6);
  let marks = st + u16_at(b, st + 8);
  let cls = u16_at(b, marks + 2 + 4 * cov);
  let target = if (idx < 0 || cov >= u16_at(b, marks) || cls >= classes) { -1 } else { target_anchor(b, st + u16_at(b, st + 10), kind, idx, classes, cls, r.ligs[i], r.ligs[k]) };
  if (target < 0) {
    return (r, -1);
  }
  let (mx, my) = anchor(b, marks + u16_at(b, marks + 4 + 4 * cov));
  let (ax, ay) = anchor(b, target);
  let p = r.pos;
  let placed = Positions { ...p, dx: p.dx.with(i, ax - mx), dy: p.dy.with(i, ay - my + cross_offset(p, k)), attach: p.attach.with(i, 1), chain: p.chain.with(i, k - i) };
  return (Run { ...r, pos: placed }, i + 1);
}

// The y offset of the glyph at `k` now, with the y offsets of the glyphs
// that its cursive joins attach it to. A mark takes it when it attaches to
// `k`, as HarfBuzz 15 does, and does not take a later move of `k` up or
// down.
function cross_offset(p: Positions, k: i32): i32 {
  let off = p.dy[k];
  let j = k;
  let n = 0;
  while (p.attach[j] == 2 && p.chain[j] != 0 && n < p.chain.len()) {
    j += p.chain[j];
    if (j < 0 || j >= p.chain.len()) {
      break;
    }
    off += p.dy[j];
    n += 1;
  }
  return off;
}

// The glyph that the mark at `i` attaches to, or -1. A base or a ligature
// is the glyph before the mark that is not a mark and that `m` takes. A
// base must also be the first part of a multiple substitution, or be in the
// coverage of the subtable `st`. For a mark on a mark, it is the glyph
// before it that the flags of `l` do not skip, when that glyph is a mark of
// the same part of a ligature.
function mark_target(f: Font, r: Run, l: Lookup, st: i32, kind: i32, i: i32, m: Matcher): i32 {
  if (kind == 6) {
    let j = next_any(f, r, Lookup { ...l, flag: l.flag & 65521 }, m, i, -1);
    if (j < 0 || f.class_at(r, j) != 3 || !same_part(r.ligs[i], r.ligs[j])) {
      return -1;
    }
    return j;
  }
  // HarfBuzz goes on past a glyph that stops other searches.
  let marks = Lookup { ...l, flag: 8, set: -1 };
  let none = Seq { kind: 0, table: 0, at: 0, n: 0 };
  let j = i - 1;
  while (j >= 0) {
    let base = kind == 5 || first_part(f, r, j) || coverage(f.data, st + u16_at(f.data, st + 4), r.glyphs[j]) >= 0;
    if (match_at(f, r, marks, m, j, none, -1) > 0 && base) {
      return j;
    }
    j -= 1;
  }
  return -1;
}

// Whether the glyph at `j` is not a later part of a multiple substitution:
// HarfBuzz attaches a mark only to the first part, but stops at a mark in
// the sequence.
function first_part(f: Font, r: Run, j: i32): boolean {
  let part = lig_part(r.ligs[j]);
  if ((r.props[j] & MULTIPLIED) == 0 || part == 0 || j == 0) {
    return true;
  }
  let h = j - 1;
  return f.class_at(r, h) == 3 || (r.props[h] & MULTIPLIED) == 0 || lig_id(r.ligs[j]) != lig_id(r.ligs[h]) || part != lig_part(r.ligs[h]) + 1;
}

// Whether two marks with the ligature values `a` and `b` are on the same
// part of a ligature, or one of them is a ligature of marks.
function same_part(a: i32, b: i32): boolean {
  let (id1, id2) = (lig_id(a), lig_id(b));
  let (c1, c2) = (lig_part(a), lig_part(b));
  if (id1 == id2) {
    return id1 == 0 || c1 == c2;
  }
  return id1 > 0 && c1 == 0 || id2 > 0 && c2 == 0;
}

// The offset of the anchor of the class `cls` on the glyph of coverage index
// `idx` in the array at `at`, or -1. The array of a ligature has a row for
// each part, and the mark takes the part that `mark` and `lig` give.
function target_anchor(b: [u8], at: i32, kind: i32, idx: i32, classes: i32, cls: i32, mark: i32, lig: i32): i32 {
  if (idx >= u16_at(b, at)) {
    return -1;
  }
  let table = at;
  let row = idx;
  if (kind == 5) {
    table = at + u16_at(b, at + 2 + 2 * idx);
    let parts = u16_at(b, table);
    let same = lig_id(lig) != 0 && lig_id(lig) == lig_id(mark) && lig_part(mark) > 0;
    row = if (same) { lig_part(mark).min(parts) - 1 } else { parts - 1 };
  }
  let off = if (row < 0) { 0 } else { u16_at(b, table + 2 + 2 * (row * classes + cls)) };
  return if (off == 0) { -1 } else { table + off };
}
```

### `kern table`

A font with no GPOS kerning can have a `kern` table, in the version of OpenType or in that of Apple. `std/shape` applies it as HarfBuzz applies it. Each subtable for horizontal text applies in turn, to each pair of glyphs that are not marks. A right-to-left run takes the pairs in visual order.

Half of the kerning of a pair goes to the advance of the first glyph. The other half goes to the second glyph, which also moves by that half. A cross-stream subtable moves the second glyph up or down. Then each glyph takes the y offset of the glyph before it, as in a cursive join.

The subtables of format 0, 2 and 3 apply. Format 0 holds pairs of glyphs, format 2 classes of glyphs and a table of offsets, and format 3 classes and a table of indices. HarfBuzz rejects a table whose subtables go past its end, and then `std/truetype` reads no subtables. The state machines of format 1 do not apply.

```fern
<<kern table>>=
// The flags of a subtable of the kern table, as `kern_table` gives them.
const KERN_HORIZONTAL: i32 = 1;

const KERN_CROSS: i32 = 2;

const KERN_VARIATION: i32 = 4;

// The count of values for each subtable that `kern_table` gives.
const KERN_FIELDS: i32 = 5;

// The subtables of the kern table at `t`, which ends at `end0`, as HarfBuzz
// reads them. For each one, its offset, the end of its data, its format,
// its flags and the size of its header. A table that HarfBuzz rejects
// gives none. As in HarfBuzz, the data of the last subtable can go past its
// length.
function kern_table(b: [u8], t: i32, end0: i32): i32[] {
  let version = u16_at(b, t);
  let end = end0.min(b.len());
  if (t < 0 || t + 8 > end || version > 1) {
    return [];
  }
  return if (version == 1) { kern_subtables_at(b, t + 8, u32_at(b, t + 4), end, true) } else { kern_subtables_at(b, t + 4, u16_at(b, t + 2), end, false) };
}

// The `n` subtables from `at0` of a kern table that ends at `end`, as
// `kern_table` gives them, of the version of Apple with `apple`.
function kern_subtables_at(b: [u8], at0: i32, n: i32, end: i32, apple: boolean): i32[] {
  let none: i32[] = [];
  let head = if (apple) { 8 } else { 6 };
  let (at, out, i) = (at0, none, 0);
  while (i < n) {
    let (length, format, coverage) = if (apple) { (u32_at(b, at), u8_at(b, at + 5), u8_at(b, at + 4)) } else { (u16_at(b, at + 2), u8_at(b, at + 4), u8_at(b, at + 5)) };
    let limit = if (i + 1 < n) { at + length } else { end };
    if (at + head > end || length < head || at + length > end || !kern_subtable_ok(b, at, at + head, format, limit)) {
      return none;
    }
    out = out.concat([at, limit, format, kern_flags(coverage, apple), head]);
    at += length;
    i += 1;
  }
  return out;
}

// The flags of a subtable with the coverage byte `c`.
function kern_flags(c: i32, apple: boolean): i32 {
  if (apple) {
    return (if ((c & 0x80) == 0) { KERN_HORIZONTAL } else { 0 }) | (if ((c & 0x40) != 0) { KERN_CROSS } else { 0 }) | (if ((c & 0x20) != 0) { KERN_VARIATION } else { 0 });
  }
  return (if ((c & 1) != 0) { KERN_HORIZONTAL } else { 0 }) | (if ((c & 4) != 0) { KERN_CROSS } else { 0 });
}

// Whether the subtable at `at` of the format `format`, with its data at
// `d`, stays before `limit`, as HarfBuzz checks it.
function kern_subtable_ok(b: [u8], at: i32, d: i32, format: i32, limit: i32): boolean {
  if (format == 0) {
    return d + 8 <= limit && d + 8 + 6 * u16_at(b, d) <= limit;
  }
  if (format == 2) {
    return d + 8 <= limit && class_table_ok(b, at + u16_at(b, d + 2), limit) && class_table_ok(b, at + u16_at(b, d + 4), limit) && at + u16_at(b, d + 6) <= limit;
  }
  if (format == 3) {
    return d + 6 <= limit && d + 6 + 2 * u8_at(b, d + 2) + 2 * u16_at(b, d) + u8_at(b, d + 3) * u8_at(b, d + 4) <= limit;
  }
  return true;
}

// Whether the class table of format 2 at `at` stays before `limit`.
function class_table_ok(b: [u8], at: i32, limit: i32): boolean {
  return at + 4 <= limit && at + 4 + 2 * u16_at(b, at + 2) <= limit;
}

// Whether the font has a kern table with a subtable.
pub function (f: Font) has_kern_table(): boolean {
  return f.kern.len() > 0;
}

// Whether a subtable of the kern table has the format `format`, or with
// `format` of -1, is cross-stream.
function kern_has(f: Font, format: i32): boolean {
  let k = 0;
  while (k < f.kern.len()) {
    if (format < 0 && (f.kern[k + 3] & KERN_CROSS) != 0 || f.kern[k + 2] == format) {
      return true;
    }
    k += KERN_FIELDS;
  }
  return false;
}

// Whether a subtable of the kern table is a state machine, of format 1.
pub function (f: Font) has_kern_machine(): boolean {
  return kern_has(f, 1);
}

// Whether a subtable of the kern table moves the glyphs up or down.
pub function (f: Font) has_cross_kern(): boolean {
  return kern_has(f, -1);
}

// `r`, whose glyphs have their advances, after the subtables of the kern
// table for horizontal text, as HarfBuzz applies them.
pub function (f: Font) with_kern_table(r: Run): Run {
  let n = r.glyphs.len();
  let order: i32[] = [];
  for k in 0..n {
    order = order.append(if (r.pos.rtl) { n - 1 - k } else { k });
  }
  let p = r.pos;
  let chained = false;
  let k = 0;
  while (k < f.kern.len()) {
    let sub = f.kern.drop(k).take(KERN_FIELDS);
    k += KERN_FIELDS;
    if ((sub[3] & KERN_HORIZONTAL) == 0 || (sub[3] & KERN_VARIATION) != 0 || !kern_touches(f, r, sub)) {
      continue;
    }
    let cross = (sub[3] & KERN_CROSS) != 0;
    if (cross && !chained) {
      chained = true;
      p = cross_chained(p);
    }
    p = kerned_by(f, Run { ...r, pos: p }, order, sub, cross);
  }
  return Run { ...r, pos: p };
}

// `p` with each glyph attached to the glyph before it in visual order, as
// a cursive join, as HarfBuzz attaches them for a cross-stream subtable.
function cross_chained(own p: Positions): Positions {
  let n = p.chain.len();
  let chain: i32[] = [];
  let attach: i32[] = [];
  for _ in 0..n {
    chain = chain.append(if (p.rtl) { 1 } else { -1 });
    attach = attach.append(2);
  }
  return Positions { ...p, chain: chain, attach: attach };
}

// Whether a glyph of `r` is a first glyph of the subtable `sub`, which
// HarfBuzz asks before it applies the subtable.
function kern_touches(f: Font, r: Run, sub: i32[]): boolean {
  let b = f.data;
  let (at, d, format) = (sub[0], sub[0] + sub[4], sub[2]);
  if (format == 3) {
    return u16_at(b, d) > 0;
  }
  if (format != 0 && format != 2) {
    return false;
  }
  for g in r.glyphs {
    if (format == 0 && first_of_pair(b, d, g) || format == 2 && kern_class(b, at + u16_at(b, d + 2), g) >= 0) {
      return true;
    }
  }
  return false;
}

// Whether glyph `g` is the first glyph of a pair of the subtable of format 0
// with its data at `d`.
function first_of_pair(b: [u8], d: i32, g: i32): boolean {
  let lo = 0;
  let hi = u16_at(b, d);
  while (lo < hi) {
    let mid = (lo + hi) / 2;
    if (u16_at(b, d + 8 + 6 * mid) < g) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  return lo < u16_at(b, d) && u16_at(b, d + 8 + 6 * lo) == g;
}

// The class of glyph `g` in the class table of format 2 at `at`, or -1 when
// the table does not hold it.
function kern_class(b: [u8], at: i32, g: i32): i32 {
  let i = g - u16_at(b, at);
  if (i < 0 || i >= u16_at(b, at + 2)) {
    return -1;
  }
  let c = u16_at(b, at + 4 + 2 * i);
  return if (c == 1) { -1 } else { c };
}

// The positions of `r` after the subtable `sub`, for the glyphs in the
// visual order `order`. A pair is a glyph and the next glyph that is not a
// mark and not default ignorable.
function kerned_by(f: Font, r: Run, order: i32[], sub: i32[], cross: boolean): Positions {
  let p = r.pos;
  let marks = Lookup { kind: 0, flag: 8, at: 0, count: 0, set: -1, table: 0, gpos: true, opts: 0, base: 0, subs: [] };
  let idx = 0;
  while (idx < order.len()) {
    let j = idx + 1;
    while (j < order.len() && (skipped(f, marks, r, order[j]) || is_ignorable(r.props[order[j]]))) {
      j += 1;
    }
    if (j >= order.len()) {
      break;
    }
    let (a, c) = (order[idx], order[j]);
    let kern = kern_value(f.data, sub, r.glyphs[a], r.glyphs[c]);
    if (kern != 0 && cross) {
      p = Positions { ...p, dy: p.dy.with(c, kern) };
    } else if (kern != 0) {
      let half = kern >> 1;
      let rest = kern - half;
      p = Positions { ...p, advances: p.advances.with(a, p.advances[a] + half).with(c, p.advances[c] + rest), dx: p.dx.with(c, p.dx[c] + rest) };
    }
    idx = j;
  }
  return p;
}

// The kerning of the glyphs `g1` and `g2` in the subtable `sub`.
function kern_value(b: [u8], sub: i32[], g1: i32, g2: i32): i32 {
  let (at, limit, format, d) = (sub[0], sub[1], sub[2], sub[0] + sub[4]);
  if (format == 0) {
    return pair_kern(b, d, g1 << 16 | g2);
  }
  if (format == 2) {
    let l = kern_class(b, at + u16_at(b, d + 2), g1);
    let r = kern_class(b, at + u16_at(b, d + 4), g2);
    let array = u16_at(b, d + 6);
    if (l < 0 || r < 0 || l + r < array) {
      return 0;
    }
    let v = at + array + 2 * ((l + r - array) / 2);
    return if (v + 2 <= limit) { i16_at(b, v) } else { 0 };
  }
  return class_kern(b, d, g1, g2);
}

// The kerning of the pair `key`, the first glyph and the second, in the
// sorted pairs of the subtable of format 0 with its data at `d`.
function pair_kern(b: [u8], d: i32, key: i32): i32 {
  let lo = 0;
  let hi = u16_at(b, d) - 1;
  while (lo <= hi) {
    let mid = (lo + hi) / 2;
    let at = d + 8 + 6 * mid;
    let k = u32_at(b, at);
    if (key < k) {
      hi = mid - 1;
    } else if (key > k) {
      lo = mid + 1;
    } else {
      return i16_at(b, at + 4);
    }
  }
  return 0;
}

// The kerning of `g1` and `g2` in the subtable of format 3 with its data at
// `d`. A glyph past the classes has the class 0.
function class_kern(b: [u8], d: i32, g1: i32, g2: i32): i32 {
  let glyphs = u16_at(b, d);
  let (values, lefts, rights) = (u8_at(b, d + 2), u8_at(b, d + 3), u8_at(b, d + 4));
  let left = d + 6 + 2 * values;
  let right = left + glyphs;
  let lc = if (g1 < glyphs) { u8_at(b, left + g1) } else { 0 };
  let rc = if (g2 < glyphs) { u8_at(b, right + g2) } else { 0 };
  if (lc >= lefts || rc >= rights) {
    return 0;
  }
  let index = u8_at(b, right + glyphs + lc * rights + rc);
  return if (index < values) { i16_at(b, d + 6 + 2 * index) } else { 0 };
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
  let (at, end) = glyph_span(f, g);
  let o = Outline { xs: [], ys: [], flags: [], ends: [] };
  if (at >= 0 && i16_at(b, at) < 0) {
    o = part_outline(parts(b, at + 10));
  } else if (at >= 0) {
    o = simple(b, at, end, i16_at(b, at));
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

// The start and exclusive end of the glyph, or (-1, -1) for an absent outline.
function glyph_span(f: Font, g: i32): (i32, i32) {
  if (g < 0 || g >= f.glyphs) {
    return (-1, -1);
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
  if (f.glyf < 0 || start < 0 || end < 10 || start > end - 10 || end > b.len() - f.glyf) {
    return (-1, -1);
  }
  return (f.glyf + start, f.glyf + end);
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
  let (at, end) = glyph_span(f, g);
  if (at < 0 || depth > 8 || reads <= 0) {
    return ([], reads);
  }
  let n = i16_at(f.data, at);
  if (n < 0) {
    return composite(f, g, at + 10, depth, reads - 1);
  }
  let o = simple(f.data, at, end, n);
  let (dx, dy) = glyph_deltas(f, g, o);
  let pts: Point[] = [];
  for i in 0..o.xs.len() {
    pts = pts.append(Point { x: o.xs[i] as f64 + dx[i], y: o.ys[i] as f64 + dy[i], on: (o.flags[i] & 1) != 0, cubic: false });
  }
  return (split_contours(pts, o.ends), reads - 1);
}

function simple(b: [u8], at: i32, end: i32, n: i32): Outline {
  let empty = Outline { xs: [], ys: [], flags: [], ends: [] };
  if (n == 0 || end - at < 12 + 2 * n) {
    return empty;
  }
  let ends: i32[] = [];
  let last = -1;
  for i in 0..n {
    let point = u16_at(b, at + 10 + 2 * i);
    if (point <= last) {
      return empty;
    }
    ends = ends.append(point);
    last = point;
  }
  let count = last + 1;
  let p = at + 10 + 2 * n;
  let skip = u16_at(b, p);
  if (skip > end - p - 2) {
    return empty;
  }
  let (flags, p1) = read_flags(b, p + 2 + skip, end, count);
  if (flags.len() != count) {
    return empty;
  }
  let (xs, p2) = read_coords(b, p1, end, flags, 2, 16);
  if (xs.len() != count) {
    return empty;
  }
  let (ys, _) = read_coords(b, p2, end, flags, 4, 32);
  if (ys.len() != count) {
    return empty;
  }
  return Outline { xs: xs, ys: ys, flags: flags, ends: ends };
}

// The flags of `count` points from `p`, with their repeats, and the offset
// after them.
function read_flags(b: [u8], at: i32, end: i32, count: i32): (i32[], i32) {
  let flags: i32[] = [];
  let p = at;
  while (flags.len() < count && p < end) {
    let fl = u8_at(b, p);
    p += 1;
    flags = flags.append(fl);
    if ((fl & 8) != 0) {
      if (p == end) {
        return ([], p);
      }
      let rep = u8_at(b, p);
      if (rep > count - flags.len()) {
        return ([], p);
      }
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
function read_coords(b: [u8], at: i32, end: i32, flags: i32[], short: i32, same: i32): (i32[], i32) {
  let out: i32[] = [];
  let v = 0;
  let p = at;
  for i in 0..flags.len() {
    let fl = flags[i];
    if ((fl & short) != 0) {
      if (p >= end) {
        return ([], p);
      }
      let d = u8_at(b, p);
      p += 1;
      if ((fl & same) == 0) {
        d = -d;
      }
      v += d;
    } else if ((fl & same) == 0) {
      if (end - p < 2) {
        return ([], p);
      }
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

### `extents`

`extents` gives the box of the ink of a glyph, as HarfBuzz gives it. For a TrueType glyph of the default design, the box comes from the header of the glyph. Its left edge is the left side bearing of `hmtx`. For a CFF glyph, or a glyph of a variable font at an axis, the box holds each point of the outline. Each edge is rounded. An empty glyph has a box of zero size.

```fern
<<extents>>=
// The box of the ink of a glyph in font units: its left edge, its top, its
// width and its height. The height is 0 or negative, as in HarfBuzz.
pub struct Extents { x_bearing: i32, y_bearing: i32, width: i32, height: i32 }

// The box of glyph `g`, or None when the font does not have the glyph.
pub function (f: Font) extents(g: i32): Option[Extents] {
  if (g < 0 || g >= f.glyphs) {
    return None;
  }
  if (f.cff.charstrings >= 0 || f.coords.len() > 0) {
    return Some(points_extents(contours(f, g), f.cff.charstrings >= 0));
  }
  let (at, _) = glyph_span(f, g);
  let b = f.data;
  if (at < 0 || i16_at(b, at) == 0) {
    return Some(Extents { x_bearing: 0, y_bearing: 0, width: 0, height: 0 });
  }
  let (x0, y0, x1, y1) = (i16_at(b, at + 2), i16_at(b, at + 4), i16_at(b, at + 6), i16_at(b, at + 8));
  return Some(Extents { x_bearing: left_bearing(f, g, x0.min(x1)), y_bearing: y0.max(y1), width: x0.max(x1) - x0.min(x1), height: y0.min(y1) - y0.max(y1) });
}

// The left side bearing of glyph `g` in `hmtx`, or `none` when the table
// does not hold it.
function left_bearing(f: Font, g: i32, none: i32): i32 {
  let at = if (g < f.hmetrics) { f.hmtx + 4 * g + 2 } else { f.hmtx + 4 * f.hmetrics + 2 * (g - f.hmetrics) };
  return if (at + 2 <= f.data.len()) { i16_at(f.data, at) } else { none };
}

// The box of the points of `cs`, each edge rounded. With `per_axis`, as
// for CFF, a box with no width or no height has 0 in place of that edge.
// Else such a box has zero size.
function points_extents(cs: Point[][], per_axis: boolean): Extents {
  let (x0, y0, x1, y1) = (1.0e9, 1.0e9, -1.0e9, -1.0e9);
  for c in cs {
    for p in c {
      x0 = x0.min(p.x);
      x1 = x1.max(p.x);
      y0 = y0.min(p.y);
      y1 = y1.max(p.y);
    }
  }
  let (left, width) = rounded_span(x0, x1, x0 < x1 && (per_axis || y0 < y1));
  let (top, height) = rounded_span(y1, y0, y0 < y1 && (per_axis || x0 < x1));
  return Extents { x_bearing: left, y_bearing: top, width: width, height: height };
}

// The edge `from` rounded, and the distance from it to the edge `to`
// rounded, or zeros when not `ok`.
function rounded_span(from: f64, to: f64, ok: boolean): (i32, i32) {
  if (!ok) {
    return (0, 0);
  }
  let start = from.round() as i32;
  return (start, to.round() as i32 - start);
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
  let m = Affine { xx: scale, yx: 0.0, xy: 0.0, yy: 0.0 - scale, dx: dx - left as f64, dy: 0.0 - top as f64 };
  for c in cs {
    r = outline(r, c, m);
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
function outline(r: Raster, c: Point[], m: Affine): Raster {
  let n = c.len();
  if (n < 2) {
    return r;
  }
  let pts: Point[] = [];
  for p in c {
    pts = pts.append(Point { ...p, x: m.xx * p.x + m.xy * p.y + m.dx, y: m.yx * p.x + m.yy * p.y + m.dy });
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

### `color glyphs`

A color glyph has a paint in the `COLR` table, and its colors come from the first palette of the `CPAL` table. A glyph of version 0 is a list of layers: each layer is the outline of a glyph in one color. A glyph of version 1 is a graph of paints. A paint can fill its area with a color or a gradient. It can also clip its child to a glyph outline, move its child, put layers on top of each other, or composite two paints.

`render_color` draws a color glyph into a buffer of premultiplied colors, one for each pixel of the clip box of the glyph. A paint gets the transform from its units to the pixels, and the coverage that the glyphs above it in the graph leave. A gradient finds the place of each pixel in its own units with the inverse of the transform.

```fern
<<color glyphs>>=
// The colors of a glyph, row by row: four bytes for each pixel, red,
// green, blue and alpha, with the color multiplied by the alpha. The box is
// that of `Bitmap`.
pub struct ColorBitmap { width: i32, height: i32, left: i32, top: i32, rgba: u8[] }

// An affine map: x' = xx x + xy y + dx, and y' = yx x + yy y + dy.
struct Affine { xx: f64, yx: f64, xy: f64, yy: f64, dx: f64, dy: f64 }

// A buffer of premultiplied colors in the range 0 to 1, and the pixels of
// the paint that draws into it: four values for each pixel.
struct Layer { w: i32, h: i32, rgba: f64[] }

// The most paints that the graph of one glyph can draw, and the most
// nesting of its paints.
const MAX_PAINTS: i32 = 65536;

const MAX_PAINT_DEPTH: i32 = 64;

// A palette index that takes the color of the text.
const FOREGROUND: i32 = 65535;

function u24_at(b: [u8], at: i32): i32 {
  return u8_at(b, at) << 16 | u16_at(b, at + 1);
}

function fixed(b: [u8], at: i32): f64 {
  return u32_at(b, at) as f64 / 65536.0;
}

// m after n: n applies first.
function times(m: Affine, n: Affine): Affine {
  return Affine { xx: m.xx * n.xx + m.xy * n.yx, yx: m.yx * n.xx + m.yy * n.yx, xy: m.xx * n.xy + m.xy * n.yy, yy: m.yx * n.xy + m.yy * n.yy, dx: m.xx * n.dx + m.xy * n.dy + m.dx, dy: m.yx * n.dx + m.yy * n.dy + m.dy };
}

function shifted(dx: f64, dy: f64): Affine {
  return Affine { xx: 1.0, yx: 0.0, xy: 0.0, yy: 1.0, dx: dx, dy: dy };
}

// `t` about the center (cx, cy).
function about(t: Affine, cx: f64, cy: f64): Affine {
  return times(shifted(cx, cy), times(t, shifted(0.0 - cx, 0.0 - cy)));
}

// The inverse of `m`, or None when it has none.
function inverse(m: Affine): Option[Affine] {
  let det = m.xx * m.yy - m.xy * m.yx;
  if (det.abs() < 1.0e-12) {
    return None;
  }
  let (xx, xy, yx, yy) = (m.yy / det, 0.0 - m.xy / det, 0.0 - m.yx / det, m.xx / det);
  return Some(Affine { xx: xx, yx: yx, xy: xy, yy: yy, dx: 0.0 - xx * m.dx - xy * m.dy, dy: 0.0 - yx * m.dx - yy * m.dy });
}

// The offset of the paint of glyph `g` in the base glyph list of COLR
// version 1, or -1.
function base_paint(f: Font, g: i32): i32 {
  let b = f.data;
  let list = if (f.colr < 0 || u16_at(b, f.colr) < 1) { 0 } else { u32_at(b, f.colr + 14) };
  if (list == 0) {
    return -1;
  }
  let at = f.colr + list;
  let lo = 0;
  let hi = u32_at(b, at) - 1;
  while (lo <= hi) {
    let mid = (lo + hi) / 2;
    let r = at + 4 + 6 * mid;
    let v = u16_at(b, r);
    if (v == g) {
      return at + u32_at(b, r + 2);
    }
    lo = if (v < g) { mid + 1 } else { lo };
    hi = if (v < g) { hi } else { mid - 1 };
  }
  return -1;
}

// The record of glyph `g` in the base glyph records of COLR version 0, or
// -1.
function base_record(f: Font, g: i32): i32 {
  let b = f.data;
  if (f.colr < 0) {
    return -1;
  }
  let at = f.colr + u32_at(b, f.colr + 4);
  let lo = 0;
  let hi = u16_at(b, f.colr + 2) - 1;
  while (lo <= hi) {
    let mid = (lo + hi) / 2;
    let v = u16_at(b, at + 6 * mid);
    if (v == g) {
      return at + 6 * mid;
    }
    lo = if (v < g) { mid + 1 } else { lo };
    hi = if (v < g) { hi } else { mid - 1 };
  }
  return -1;
}

// Whether glyph `g` has colors in COLR.
pub function (f: Font) has_colors(g: i32): boolean {
  return f.cpal >= 0 && (base_paint(f, g) >= 0 || base_record(f, g) >= 0);
}

// The premultiplied color of palette entry `i` of the first palette, with
// its alpha times `alpha`. The entry 65535 takes `fg`, an ARGB color.
function palette_color(f: Font, i: i32, alpha: f64, fg: i32): (f64, f64, f64, f64) {
  let b = f.data;
  let at = f.cpal + u32_at(b, f.cpal + 8) + 4 * (u16_at(b, f.cpal + 12) + i);
  let known = i < u16_at(b, f.cpal + 2);
  let bgra = if (known) { u32_at(b, at) } else { 0 };
  let argb = if (i == FOREGROUND) { fg } else { bgra << 24 | (bgra >> 8 & 255) << 16 | (bgra >> 16 & 255) << 8 | bgra >> 24 & 255 };
  let (r, g, bl, a) = (argb >> 16 & 255, argb >> 8 & 255, argb & 255, argb >> 24 & 255);
  let al = (a as f64 / 255.0 * alpha).max(0.0).min(1.0);
  return (r as f64 / 255.0 * al, g as f64 / 255.0 * al, bl as f64 / 255.0 * al, al);
}

// The box of glyph `g` in font units: its clip box in COLR, or else its
// advance and the line metrics of the font.
function color_box(f: Font, g: i32): (f64, f64, f64, f64) {
  let b = f.data;
  let clips = if (f.colr < 0 || u16_at(b, f.colr) < 1) { 0 } else { u32_at(b, f.colr + 22) };
  if (clips != 0) {
    let at = f.colr + clips;
    for k in 0..u32_at(b, at + 1) {
      let r = at + 5 + 7 * k;
      if (g >= u16_at(b, r) && g <= u16_at(b, r + 2)) {
        let box = at + u24_at(b, r + 4);
        return (i16_at(b, box + 1) as f64, i16_at(b, box + 3) as f64, i16_at(b, box + 5) as f64, i16_at(b, box + 7) as f64);
      }
    }
  }
  return (0.0, f.descender as f64, f.advance(g) as f64, f.ascender as f64);
}

// The colors of glyph `g` at `px` pixels to the em, with its pen `dx`
// pixels right of a pixel edge. The palette entry 65535 takes `fg`, an
// ARGB color. A glyph with no colors gives an empty bitmap.
pub function (f: Font) render_color(g: i32, px: f64, dx: f64, fg: i32): ColorBitmap {
  let empty = ColorBitmap { width: 0, height: 0, left: 0, top: 0, rgba: [] };
  if (!f.has_colors(g)) {
    return empty;
  }
  let scale = px / (f.units_per_em as f64);
  let (x0, y0, x1, y1) = color_box(f, g);
  let left = (x0 * scale + dx).floor() as i32;
  let top = (0.0 - y1 * scale).floor() as i32;
  let w = (x1 * scale + dx).ceil() as i32 - left;
  let h = (0.0 - y0 * scale).ceil() as i32 - top;
  if (w <= 0 || h <= 0 || !((w * h) as f64 <= MAX_BITMAP_PIXELS)) {
    return empty;
  }
  let m = Affine { xx: scale, yx: 0.0, xy: 0.0, yy: 0.0 - scale, dx: dx - left as f64, dy: 0.0 - top as f64 };
  let clip = full_clip(w * h);
  let layer = Layer { w: w, h: h, rgba: zeros(4 * w * h) };
  let at = base_paint(f, g);
  let out = if (at >= 0) { painted(f, at, m, clip, layer, fg, 0, MAX_PAINTS).0 } else { layers_v0(f, g, m, clip, layer, fg) };
  let bytes: u8[] = [];
  for v in out.rgba {
    bytes = bytes.append((v.max(0.0).min(1.0) * 255.0 + 0.5) as u8);
  }
  return ColorBitmap { width: w, height: h, left: left, top: top, rgba: bytes };
}

function full_clip(n: i32): f64[] {
  let out: f64[] = [];
  for _ in 0..n {
    out = out.append(1.0);
  }
  return out;
}

// The layers of glyph `g` of COLR version 0, each a glyph in one color.
function layers_v0(f: Font, g: i32, m: Affine, clip: f64[], own dst: Layer, fg: i32): Layer {
  let b = f.data;
  let rec = base_record(f, g);
  let layers = f.colr + u32_at(b, f.colr + 8);
  let out = dst;
  for k in 0..u16_at(b, rec + 4) {
    let lr = layers + 4 * (u16_at(b, rec + 2) + k);
    let c = palette_color(f, u16_at(b, lr + 2), 1.0, fg);
    let cover = glyph_clip(f, u16_at(b, lr), m, clip, out.w, out.h);
    out = solid_over(out, cover, c);
  }
  return out;
}

// `clip` times the coverage of the outline of glyph `g` through `m`.
function glyph_clip(f: Font, g: i32, m: Affine, clip: f64[], w: i32, h: i32): f64[] {
  let r = Raster { w: w, h: h, acc: zeros(w * h + 4) };
  for c in contours(f, g) {
    r = outline(r, c, m);
  }
  let out = clip;
  let sum = 0.0;
  for i in 0..w * h {
    sum += r.acc[i];
    out = out.with(i, out[i] * sum.abs().min(1.0));
  }
  return out;
}

// `dst` with the color `c` over it, where `cover` is above 0.
function solid_over(own dst: Layer, cover: f64[], c: (f64, f64, f64, f64)): Layer {
  let px = dst.rgba;
  let (r, g, b, a) = c;
  for i in 0..dst.w * dst.h {
    let k = cover[i];
    if (k > 0.0) {
      px = blended(px, 4 * i, r * k, g * k, b * k, a * k);
    }
  }
  return Layer { ...dst, rgba: px };
}

// `px` with the premultiplied color (r, g, b, a) over the pixel at `at`.
function blended(own px: f64[], at: i32, r: f64, g: f64, b: f64, a: f64): f64[] {
  let keep = 1.0 - a;
  let out = px.with(at, r + px[at] * keep).with(at + 1, g + px[at + 1] * keep);
  return out.with(at + 2, b + out[at + 2] * keep).with(at + 3, a + out[at + 3] * keep);
}

// `dst` after the paint at `at`, through the transform `m` and in the
// coverage `clip`, and the budget of paints that is left.
function painted(f: Font, at: i32, m: Affine, clip: f64[], own dst: Layer, fg: i32, depth: i32, budget: i32): (Layer, i32) {
  let b = f.data;
  let format = u8_at(b, at);
  if (depth >= MAX_PAINT_DEPTH || budget <= 0) {
    return (dst, 0);
  }
  if (format == 1) {
    return colr_layers(f, at, m, clip, dst, fg, depth, budget - 1);
  }
  if (format >= 10 && format <= 31) {
    return child_painted(f, at, format, m, clip, dst, fg, depth, budget - 1);
  }
  if (format == 32) {
    return composited(f, at, m, clip, dst, fg, depth, budget - 1);
  }
  return (filled(f, at, format, m, clip, dst, fg), budget - 1);
}

// `dst` with a solid color or a gradient over it, or as it is for a
// format that is not one of those.
function filled(f: Font, at: i32, format: i32, m: Affine, clip: f64[], own dst: Layer, fg: i32): Layer {
  let b = f.data;
  if (format == 2 || format == 3) {
    return solid_over(dst, clip, palette_color(f, u16_at(b, at + 1), f2dot14(b, at + 3), fg));
  }
  return if (format >= 4 && format <= 9) { gradient_over(f, at, m, clip, dst, fg) } else { dst };
}

// `dst` after a paint of the formats 10 to 31, which paints a child: in
// the outline of a glyph, as another color glyph, or through a transform.
function child_painted(f: Font, at: i32, format: i32, m: Affine, clip: f64[], own dst: Layer, fg: i32, depth: i32, budget: i32): (Layer, i32) {
  let b = f.data;
  if (format == 10) {
    let cover = glyph_clip(f, u16_at(b, at + 4), m, clip, dst.w, dst.h);
    return painted(f, at + u24_at(b, at + 1), m, cover, dst, fg, depth + 1, budget);
  }
  if (format == 11) {
    let base = base_paint(f, u16_at(b, at + 1));
    return if (base < 0) { (dst, budget) } else { painted(f, base, m, clip, dst, fg, depth + 1, budget) };
  }
  return painted(f, at + u24_at(b, at + 1), times(m, paint_transform(b, at, format)), clip, dst, fg, depth + 1, budget);
}

// The layers of a PaintColrLayers at `at`, from the layer list of COLR.
function colr_layers(f: Font, at: i32, m: Affine, clip: f64[], own dst: Layer, fg: i32, depth: i32, budget: i32): (Layer, i32) {
  let b = f.data;
  let list = f.colr + u32_at(b, f.colr + 18);
  let out = dst;
  let left = budget;
  for k in 0..u8_at(b, at + 1) {
    let i = u32_at(b, at + 2) + k;
    if (i < 0 || i >= u32_at(b, list)) {
      break;
    }
    let (next, rest) = painted(f, list + u32_at(b, list + 4 + 4 * i), m, clip, out, fg, depth + 1, left);
    out = next;
    left = rest;
  }
  return (out, left);
}

// The transform of a paint of the formats 12 to 31. A format of a
// variation, an odd one, has the fields of the format before it.
function paint_transform(b: [u8], at: i32, format: i32): Affine {
  let kind = format - (format & 1);
  if (kind == 12) {
    let t = at + u24_at(b, at + 4);
    return Affine { xx: fixed(b, t), yx: fixed(b, t + 4), xy: fixed(b, t + 8), yy: fixed(b, t + 12), dx: fixed(b, t + 16), dy: fixed(b, t + 20) };
  }
  if (kind == 14) {
    return shifted(i16_at(b, at + 4) as f64, i16_at(b, at + 6) as f64);
  }
  if (kind == 16 || kind == 18) {
    let s = Affine { xx: f2dot14(b, at + 4), yx: 0.0, xy: 0.0, yy: f2dot14(b, at + 6), dx: 0.0, dy: 0.0 };
    return if (kind == 18) { about(s, i16_at(b, at + 8) as f64, i16_at(b, at + 10) as f64) } else { s };
  }
  if (kind == 20 || kind == 22) {
    let k = f2dot14(b, at + 4);
    let s = Affine { xx: k, yx: 0.0, xy: 0.0, yy: k, dx: 0.0, dy: 0.0 };
    return if (kind == 22) { about(s, i16_at(b, at + 6) as f64, i16_at(b, at + 8) as f64) } else { s };
  }
  return turned(b, at, kind);
}

// The rotation of a paint of the formats 24 to 27, or the skew of one of
// the formats 28 to 31.
function turned(b: [u8], at: i32, kind: i32): Affine {
  let deg = 3.14159265358979323846 / 180.0;
  if (kind == 24 || kind == 26) {
    let a = f2dot14(b, at + 4) * 180.0 * deg;
    let r = Affine { xx: a.cos(), yx: a.sin(), xy: 0.0 - a.sin(), yy: a.cos(), dx: 0.0, dy: 0.0 };
    return if (kind == 26) { about(r, i16_at(b, at + 6) as f64, i16_at(b, at + 8) as f64) } else { r };
  }
  let s = Affine { xx: 1.0, yx: (f2dot14(b, at + 6) * 180.0 * deg).tan(), xy: 0.0 - (f2dot14(b, at + 4) * 180.0 * deg).tan(), yy: 1.0, dx: 0.0, dy: 0.0 };
  return if (kind == 30) { about(s, i16_at(b, at + 8) as f64, i16_at(b, at + 10) as f64) } else { s };
}

// `dst` with the result of a PaintComposite over it: the source paint and
// the backdrop paint, each drawn alone, joined with the composite mode.
function composited(f: Font, at: i32, m: Affine, clip: f64[], own dst: Layer, fg: i32, depth: i32, budget: i32): (Layer, i32) {
  let b = f.data;
  let blank = Layer { w: dst.w, h: dst.h, rgba: zeros(4 * dst.w * dst.h) };
  let (src, left) = painted(f, at + u24_at(b, at + 1), m, clip, blank, fg, depth + 1, budget);
  let (back, rest) = painted(f, at + u24_at(b, at + 5), m, clip, Layer { ...src, rgba: zeros(4 * dst.w * dst.h) }, fg, depth + 1, left);
  let mode = u8_at(b, at + 4);
  let px = dst.rgba;
  for i in 0..dst.w * dst.h {
    let k = 4 * i;
    let (r, g, bl, a) = joined_colors(mode, src.rgba, back.rgba, k);
    px = blended(px, k, r, g, bl, a);
  }
  return (Layer { ...dst, rgba: px }, rest);
}
```

The colors of a gradient come from its color line: a list of stops, each a place and a color, and a way to extend the line past its first and last stops. The colors between two stops mix in premultiplied form.

```fern
<<color glyphs>>=
// `dst` with the gradient of the formats 4 to 9 at `at` over it.
function gradient_over(f: Font, at: i32, m: Affine, clip: f64[], own dst: Layer, fg: i32): Layer {
  let b = f.data;
  let Some(inv) = inverse(m) else {
    return dst;
  };
  let format = u8_at(b, at);
  let line = color_stops(f, at + u24_at(b, at + 1), (format & 1) != 0, fg);
  let px = dst.rgba;
  for y in 0..dst.h {
    for x in 0..dst.w {
      let i = y * dst.w + x;
      if (clip[i] <= 0.0) {
        continue;
      }
      let (qx, qy) = (x as f64 + 0.5, y as f64 + 0.5);
      if let Some(t) = gradient_place(b, at, format, inv.xx * qx + inv.xy * qy + inv.dx, inv.yx * qx + inv.yy * qy + inv.dy) {
        let (r, g, bl, a) = line_color(line, t);
        let k = clip[i];
        px = blended(px, 4 * i, r * k, g * k, bl * k, a * k);
      }
    }
  }
  return Layer { ...dst, rgba: px };
}

// A color line: its extend mode, then each stop as five values: its place
// and its premultiplied color, in the order of the places.
struct ColorLine { extend: i32, stops: f64[] }

function color_stops(f: Font, at: i32, var: boolean, fg: i32): ColorLine {
  let b = f.data;
  let size = if (var) { 10 } else { 6 };
  let stops: f64[] = [];
  for k in 0..u16_at(b, at + 1) {
    let s = at + 3 + size * k;
    let (r, g, bl, a) = palette_color(f, u16_at(b, s + 2), f2dot14(b, s + 4), fg);
    stops = sorted_stop(stops, [f2dot14(b, s), r, g, bl, a]);
  }
  return ColorLine { extend: u8_at(b, at), stops: stops };
}

// `stops` with `stop` after each stop of the same or an earlier place.
function sorted_stop(stops: f64[], stop: f64[]): f64[] {
  let at = stops.len();
  while (at > 0 && stops[at - 5] > stop[0]) {
    at -= 5;
  }
  return stops.take(at).concat(stop).concat(stops.drop(at));
}

// The color of `line` at the place `t`.
function line_color(line: ColorLine, t: f64): (f64, f64, f64, f64) {
  let s = line.stops;
  let n = s.len() / 5;
  if (n == 0) {
    return (0.0, 0.0, 0.0, 0.0);
  }
  let (lo, hi) = (s[0], s[5 * (n - 1)]);
  let span = hi - lo;
  let u = if (span <= 0.0 || line.extend == 0) {
    t.max(lo).min(hi)
  } else if (line.extend == 1) {
    lo + ((t - lo) / span - ((t - lo) / span).floor()) * span
  } else {
    let q = ((t - lo) / span).abs();
    let p = q - 2.0 * (q / 2.0).floor();
    lo + (1.0 - (1.0 - p).abs()) * span
  };
  let k = 0;
  while (k + 1 < n && s[5 * (k + 1)] < u) {
    k += 1;
  }
  if (k + 1 >= n || u <= s[5 * k]) {
    return (s[5 * k + 1], s[5 * k + 2], s[5 * k + 3], s[5 * k + 4]);
  }
  let (a, z) = (5 * k, 5 * (k + 1));
  let w = (u - s[a]) / (s[z] - s[a]);
  return (s[a + 1] + (s[z + 1] - s[a + 1]) * w, s[a + 2] + (s[z + 2] - s[a + 2]) * w, s[a + 3] + (s[z + 3] - s[a + 3]) * w, s[a + 4] + (s[z + 4] - s[a + 4]) * w);
}

// The place on the color line of the point (x, y) in the units of the
// gradient at `at`, or None where the gradient draws nothing.
function gradient_place(b: [u8], at: i32, format: i32, x: f64, y: f64): Option[f64] {
  let p: f64[] = [];
  for k in 0..6 {
    p = p.append(i16_at(b, at + 4 + 2 * k) as f64);
  }
  if (format <= 5) {
    // p0, p1 and p2: the line from p0 to p1, turned so that it is at a
    // right angle to the line from p0 to p2.
    let (dx, dy) = (p[4] - p[0], p[5] - p[1]);
    let (nx, ny) = (0.0 - dy, dx);
    let nn = nx * nx + ny * ny;
    let along = if (nn == 0.0) { 0.0 } else { ((p[2] - p[0]) * nx + (p[3] - p[1]) * ny) / nn };
    let (vx, vy) = (nx * along, ny * along);
    let vv = vx * vx + vy * vy;
    return if (vv == 0.0) { None } else { Some(((x - p[0]) * vx + (y - p[1]) * vy) / vv) };
  }
  if (format <= 7) {
    return radial_place(p, x, y);
  }
  let deg = 180.0 / 3.14159265358979323846;
  let start = (f2dot14(b, at + 8) + 1.0) * 180.0;
  let end = (f2dot14(b, at + 10) + 1.0) * 180.0;
  let angle = (y - p[1]).atan2(x - p[0]) * deg;
  let a = if (angle < 0.0) { angle + 360.0 } else { angle };
  return if (end == start) { None } else { Some((a - start) / (end - start)) };
}

// The largest place t of a two-circle gradient where the circle of t,
// with a radius of 0 or more, passes through (x, y). `p` holds x0, y0,
// r0, x1, y1 and r1.
function radial_place(p: f64[], x: f64, y: f64): Option[f64] {
  let (cdx, cdy, dr) = (p[3] - p[0], p[4] - p[1], p[5] - p[2]);
  let (pdx, pdy) = (x - p[0], y - p[1]);
  let a = cdx * cdx + cdy * cdy - dr * dr;
  let bb = pdx * cdx + pdy * cdy + p[2] * dr;
  let c = pdx * pdx + pdy * pdy - p[2] * p[2];
  if (a.abs() < 1.0e-9) {
    let t = if (bb == 0.0) { -1.0e9 } else { c / (2.0 * bb) };
    return if (bb != 0.0 && p[2] + t * dr >= 0.0) { Some(t) } else { None };
  }
  let disc = bb * bb - a * c;
  if (disc < 0.0) {
    return None;
  }
  let (t1, t2) = ((bb + disc.sqrt()) / a, (bb - disc.sqrt()) / a);
  let (hi, lo) = (t1.max(t2), t1.min(t2));
  if (p[2] + hi * dr >= 0.0) {
    return Some(hi);
  }
  return if (p[2] + lo * dr >= 0.0) { Some(lo) } else { None };
}
```

A composite mode joins a source color with a backdrop color. The modes 0 to 12 are those of Porter and Duff. The modes 13 to 23 blend each channel. The modes 24 to 27 blend the hue, the saturation and the luminosity. The W3C compositing rules give each of these modes.

```fern
<<color glyphs>>=
// The source of `src` and the backdrop of `back` at `k`, joined with the
// composite mode `mode`. The colors are premultiplied.
function joined_colors(mode: i32, src: f64[], back: f64[], k: i32): (f64, f64, f64, f64) {
  let (sa, da) = (src[k + 3], back[k + 3]);
  if (mode <= 12) {
    let (fs, fd) = porter_duff(mode, sa, da);
    let mix = (c: i32): f64 => (src[k + c] * fs + back[k + c] * fd).min(1.0);
    return (mix(0), mix(1), mix(2), mix(3));
  }
  let s = unpremultiplied(src, k);
  let d = unpremultiplied(back, k);
  let mixed = if (mode >= 24) { hsl_blend(mode, s, d) } else { [channel_blend(mode, s[0], d[0]), channel_blend(mode, s[1], d[1]), channel_blend(mode, s[2], d[2])] };
  let joined = (c: i32): f64 => src[k + c] * (1.0 - da) + back[k + c] * (1.0 - sa) + sa * da * mixed[c];
  return (joined(0), joined(1), joined(2), sa + da - sa * da);
}

// The factors of the source and of the backdrop for a Porter-Duff mode.
function porter_duff(mode: i32, sa: f64, da: f64): (f64, f64) {
  let pairs = [(0.0, 0.0), (1.0, 0.0), (0.0, 1.0), (1.0, 1.0 - sa), (1.0 - da, 1.0), (da, 0.0), (0.0, sa), (1.0 - da, 0.0), (0.0, 1.0 - sa), (da, 1.0 - sa), (1.0 - da, sa), (1.0 - da, 1.0 - sa), (1.0, 1.0)];
  return pairs[mode];
}

function unpremultiplied(px: f64[], k: i32): f64[] {
  let a = px[k + 3];
  if (a <= 0.0) {
    return [0.0, 0.0, 0.0];
  }
  return [(px[k] / a).min(1.0), (px[k + 1] / a).min(1.0), (px[k + 2] / a).min(1.0)];
}

// The blend of the source channel `s` and the backdrop channel `d`.
function channel_blend(mode: i32, s: f64, d: f64): f64 {
  if (mode == 13) {
    return s + d - s * d;
  }
  if (mode == 14) {
    return hard_light(d, s);
  }
  if (mode == 15 || mode == 16) {
    return if (mode == 15) { s.min(d) } else { s.max(d) };
  }
  if (mode == 17) {
    return if (d == 0.0) { 0.0 } else if (s >= 1.0) { 1.0 } else { (d / (1.0 - s)).min(1.0) };
  }
  return more_channel_blend(mode, s, d);
}

// The blend of a channel for the modes 18 to 23.
function more_channel_blend(mode: i32, s: f64, d: f64): f64 {
  if (mode == 18) {
    return if (d >= 1.0) { 1.0 } else if (s <= 0.0) { 0.0 } else { 1.0 - ((1.0 - d) / s).min(1.0) };
  }
  if (mode == 19) {
    return hard_light(s, d);
  }
  if (mode == 20) {
    return soft_light(s, d);
  }
  if (mode == 21 || mode == 22) {
    return if (mode == 21) { (s - d).abs() } else { s + d - 2.0 * s * d };
  }
  return s * d;
}

function hard_light(s: f64, d: f64): f64 {
  if (s <= 0.5) {
    return d * 2.0 * s;
  }
  let t = 2.0 * s - 1.0;
  return d + t - d * t;
}

function soft_light(s: f64, d: f64): f64 {
  if (s <= 0.5) {
    return d - (1.0 - 2.0 * s) * d * (1.0 - d);
  }
  let dd = if (d <= 0.25) { ((16.0 * d - 12.0) * d + 4.0) * d } else { d.sqrt() };
  return d + (2.0 * s - 1.0) * (dd - d);
}

// The blend of the colors `s` and `d` by hue, saturation, color or
// luminosity.
function hsl_blend(mode: i32, s: f64[], d: f64[]): f64[] {
  if (mode == 24) {
    return with_lum(with_sat(s, sat(d)), lum(d));
  }
  if (mode == 25) {
    return with_lum(with_sat(d, sat(s)), lum(d));
  }
  return if (mode == 26) { with_lum(s, lum(d)) } else { with_lum(d, lum(s)) };
}

function lum(c: f64[]): f64 {
  return 0.3 * c[0] + 0.59 * c[1] + 0.11 * c[2];
}

function sat(c: f64[]): f64 {
  return c[0].max(c[1]).max(c[2]) - c[0].min(c[1]).min(c[2]);
}

function with_lum(c: f64[], l: f64): f64[] {
  let d = l - lum(c);
  let m = c.map((v: f64): f64 => v + d);
  let lm = lum(m);
  let (lo, hi) = (m[0].min(m[1]).min(m[2]), m[0].max(m[1]).max(m[2]));
  return m.map((v: f64): f64 => if (lo < 0.0) { lm + (v - lm) * lm / (lm - lo) } else if (hi > 1.0) { lm + (v - lm) * (1.0 - lm) / (hi - lm) } else { v });
}

function with_sat(c: f64[], s: f64): f64[] {
  let (lo, hi) = (c[0].min(c[1]).min(c[2]), c[0].max(c[1]).max(c[2]));
  return c.map((v: f64): f64 => if (hi > lo) { (v - lo) * s / (hi - lo) } else { 0.0 });
}
```
