# Text shaping

This module gives the glyphs of a font for a text, and their positions. It substitutes the glyphs for ligatures and for marks, for the joining forms of Arabic, and for the conjuncts of Indic scripts. Then it moves the glyphs: for the kerning, the cursive joins and the marks on their bases.

## Model

`shape` takes a text in logical order and the OpenType tag of its script, such as `"arab"`, `"deva"` or `"taml"`. It gives a `truetype.Run`: the glyphs in logical order, with the cluster of each glyph and its position. A cluster is the index of the first character that the glyph shows. A mark has the cluster of its base.

A position is the advance of the glyph and its offset from its pen, in font units. A glyph goes at its pen and its offset, and the pen then moves by its advance. In a right-to-left run, the glyph that is last in logical order goes first, at the left.

Shaping has these steps, in the order of HarfBuzz:

1. Each character starts a cluster. A mark, a ZWJ and the other characters that continue a grapheme take the cluster of the character before them.
2. A character with a canonical decomposition splits into its parts when the font has a glyph for each part. A character that has a glyph and no mark after it stays whole. In the Indic scripts, U+0958 and the other characters split.
3. The marks after each base go in the order of their combining class. Among the Arabic marks, the shadda goes first. In Arabic, the modifier combining marks, such as the hamza above, then go first among the marks of the same class.
4. A mark joins the starter before it when the two compose and the font has a glyph for the result. Thus U+064A and the hamza above become U+0626, and U+0928 and the nukta become U+0929.
5. Each character takes the glyph that the character map of the font gives. A space that the font does not have takes the glyph of the space, with the width that HarfBuzz gives it. U+2011 takes the glyph of U+2010.
6. In Arabic, each letter takes a form: isolated, initial, medial or final. It is the joining form of the letter between its neighbours, from its Unicode joining type. A mark is transparent: it does not stop the letters on its two sides from joining. The Indic scripts go through the Indic shaper, which the section "Indic scripts" tells.
7. The lookups of the GSUB features apply in stages. A lookup of a form applies only to the letters of that form.
8. Each glyph takes its advance from the font. Then the lookups of the GPOS features apply in one stage: `abvm`, `blwm`, `curs`, `dist`, `kern`, `mark` and `mkmk`.
9. Each mark, of the GDEF class 3, has no advance, but in the Indic scripts a mark keeps its advance. A default ignorable glyph, such as a ZWJ, has no advance and no offset. Each glyph that a lookup attached takes the offsets of the glyph that it attaches to.
10. Each default ignorable glyph becomes the glyph of the space. In a font with no space, the default ignorable glyphs go, and their clusters join those next to them.

A default ignorable glyph does not stop a lookup when the lookup can skip it. The flags of each feature tell which ZWNJ and ZWJ glyphs a lookup can skip, as in HarfBuzz.

The Arabic stages are `rvrn`, then `ccmp` and `locl`. Then `isol`, `fina`, `medi`, `init`, `rlig` and `calt` apply one at a time, and then `liga`, `clig` and `mset`. In a font with `rclt`, the stage of `calt` also holds `rclt`, `liga`, `clig` and `mset`. Other scripts have the stage `rvrn`, then one stage of `ccmp`, `locl`, `rlig`, `calt`, `clig`, `liga` and `rclt`.

## Indic scripts

`shape` follows the Indic shaper of HarfBuzz for nine scripts. They are Devanagari (`deva`), Bengali (`beng`), Gurmukhi (`guru`), Gujarati (`gujr`), Oriya (`orya`), Tamil (`taml`), Telugu (`telu`), Kannada (`knda`) and Malayalam (`mlym`). A font with the new tag of the script, such as `dev2` or `mlm2`, takes the rules of the new Indic specification. A font with the old tag only takes the rules of the old one. A font that has neither tag, but has `DFLT` or `latn`, takes the default shaper.

Each script has the configuration that HarfBuzz gives it: its virama, where its reph goes, and how a reph forms. In Telugu a reph forms only with a ZWJ, and in Malayalam it is the dot reph U+0D4E. In Telugu and Kannada the below-base forms apply only after the base. Kannada, Malayalam and Tamil have rules of their own in the reorderings.

1. Each character has an Indic category and an Indic position, from the table that `scripts/gen-indic-table.py` writes.
2. The stage `rvrn` applies. Then the text splits into syllables with the grammar of HarfBuzz: consonant syllables, vowel syllables, standalone clusters, symbol clusters and broken clusters. Each other character is a syllable alone. The features `locl` and `ccmp` apply in each syllable.
3. A consonant whose glyph takes a below-base or post-base form with the virama, by the lookups of the font, has that position. A broken cluster gets a dotted circle at its start, when the font has one.
4. The initial reordering finds the base consonant of each syllable, and a Ra and a virama at its start that become a reph. The characters then go in the order of their positions, so that a pre-base matra comes first. Each glyph gets the masks of the features that apply to it.
5. The features `nukt`, `akhn`, `rphf`, `rkrf`, `pref`, `blwf`, `abvf`, `half`, `pstf`, `vatu` and `cjct` apply one at a time, each in one syllable.
6. The final reordering moves a pre-base matra after the half forms, and the reph to its place near the end of the syllable.
7. The features `init`, `pres`, `abvs`, `blws`, `psts`, `haln`, `rlig`, `calt`, `clig` and `rclt` apply in one stage. The GPOS features follow.

The clusters join as HarfBuzz joins them when glyphs move. The tags of the Universal Shaping Engine, such as `dev3`, are not used. The table holds the blocks that HarfBuzz gives the Indic shaper: the nine Indic blocks, Vedic Extensions and Devanagari Extended, and the placeholders and joiners of Basic Latin, Latin-1 and General Punctuation.

## Contracts

The glyphs, the clusters and the positions are those of HarfBuzz. That is true for the GSUB lookups of the types 1 to 8 and the GPOS lookups of the types 1 to 9. The test `tests/stdlib/shape_test.fern` compares them for a set of Arabic and Devanagari words, and `tests/stdlib/shape_indic_test.fern` for the other Indic scripts. An Arabic run goes from right to left, and a run of another script from left to right.

A font with no GPOS table has no mark positioning: HarfBuzz then moves the marks by their combining classes, and `shape` does not. `shape` does not mirror the brackets of a right-to-left text. A character that the font does not have takes the glyph 0.

## Executable example

```fern test name=std-shape-example
import "std/shape";
import "std/truetype";

function main(): i32 {
  // A font with no GSUB table gives the glyph of each character.
  let Some(f) = truetype.parse([0 as u8, 1 as u8, 0 as u8, 0 as u8]) else {
    return 0;
  };
  return shape.shape(f, "\u{628}", "arab").glyphs.len() - 1;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<characters>>

<<marks>>

<<joining>>

<<stages>>

<<syllables>>

<<indic>>

<<positions>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module gives the glyphs of a font for a text.
import "std/shape/indic";
import "std/truetype";
import "std/unicode";

// The bits of the masks of a run: one for the features that apply to each
// glyph, one for each joining form, and one for each Indic feature that
// applies to some glyphs of a syllable.
const GLOBAL: i32 = 1;

const ISOL: i32 = 2;

const FINA: i32 = 4;

const MEDI: i32 = 8;

const INIT: i32 = 16;

const RPHF: i32 = 32;

const PREF: i32 = 64;

const BLWF: i32 = 128;

const ABVF: i32 = 256;

const HALF: i32 = 512;

const PSTF: i32 = 1024;
```

### `characters`

HarfBuzz gives each character its index as its cluster. Then a character that continues a grapheme takes the cluster of the character before it. Such a character is a mark, a ZWJ, an emoji modifier, a tag or a halfwidth katakana sound mark. A regional indicator after a regional indicator that starts a pair continues it too.

A default ignorable character gets the property `IGNORABLE` of `std/truetype`. A ZWNJ and a ZWJ get their own property too. The combining grapheme joiner, the Mongolian variation selectors and the tags are `HIDDEN`: a GSUB lookup does not skip them.

The Indic shaper of HarfBuzz first puts a dotted circle between an independent vowel and a matra that look like another vowel together. U+0905 and U+093E are such a pair. It also puts one between a Ra and a virama, and U+0907. The pairs come from the list of invalid clusters of Microsoft, as `hb-ot-shaper-vowel-constraints.cc` of HarfBuzz holds them. The dotted circle takes the cluster of the matra.

The normalization of HarfBuzz comes next. A character that has a glyph and no mark after it stays whole, unless it is a mark after the first character. Each other character splits into its canonical decomposition when the font has a glyph for each part. The Indic shaper splits each character that it can, but not U+0931. After the marks go in order, each mark composes with the starter before it, when a mark follows the first character of the text. The Indic shaper does not compose a mark with a mark.

```fern
<<characters>>=
// The characters of a text, with the cluster of each one.
struct Chars { cps: char[], clusters: i32[] }

// The characters besides the marks that continue a grapheme for HarfBuzz:
// the ZWJ, the halfwidth katakana sound marks, the emoji modifiers and the
// tags. Each range is its first and its last code point.
const GRAPHEME_EXTENDERS: i32[] = [0x200D, 0x200D, 0xFF9E, 0xFF9F, 0x1F3FB, 0x1F3FF, 0xE0020, 0xE007F];

// The default ignorable code points that HarfBuzz hides.
const DEFAULT_IGNORABLES: i32[] = [0xAD, 0xAD, 0x34F, 0x34F, 0x61C, 0x61C, 0x17B4, 0x17B5, 0x180B, 0x180F, 0x200B, 0x200F, 0x202A, 0x202E, 0x2060, 0x206F, 0xFE00, 0xFE0F, 0xFEFF, 0xFEFF, 0xFFF0, 0xFFF8, 0x1D173, 0x1D17A, 0xE0000, 0xE0FFF];

// The default ignorable code points that a GSUB lookup does not skip: the
// combining grapheme joiner, the Mongolian variation selectors and the tags.
const HIDDEN_CHARS: i32[] = [0x34F, 0x34F, 0x180B, 0x180D, 0x180F, 0x180F, 0xE0020, 0xE007F];

// The characters of `text` and their clusters.
function characters(text: string): Chars {
  let cps: char[] = [];
  let clusters: i32[] = [];
  let pair = false;
  for c in text.chars() {
    let i = cps.len();
    let n = c as i32;
    let ri = n >= 0x1F1E6 && n <= 0x1F1FF;
    let continues = i > 0 && (c.is_mark() || in_ranges(n, GRAPHEME_EXTENDERS) || ri && pair);
    pair = ri && !continues;
    cps = cps.append(c);
    clusters = clusters.append(if (continues) { clusters[i - 1] } else { i });
  }
  return Chars { cps: cps, clusters: clusters };
}

// Whether `n` is in one of the ranges of `ranges`, which holds the first
// and the last code point of each range.
function in_ranges(n: i32, ranges: i32[]): boolean {
  let k = 0;
  while (k + 1 < ranges.len()) {
    if (n >= ranges[k] && n <= ranges[k + 1]) {
      return true;
    }
    k += 2;
  }
  return false;
}

// The properties of `c` for std/truetype.
function char_props(c: char): i32 {
  let n = c as i32;
  if (!default_ignorable(n)) {
    return 0;
  }
  if (n == 0x200C) {
    return truetype.IGNORABLE | truetype.ZWNJ;
  }
  if (n == 0x200D) {
    return truetype.IGNORABLE | truetype.ZWJ;
  }
  return truetype.IGNORABLE | (if (in_ranges(n, HIDDEN_CHARS)) { truetype.HIDDEN } else { 0 });
}

// Whether HarfBuzz hides `n`: a default ignorable code point, but not a
// Hangul filler or a shorthand format control.
function default_ignorable(n: i32): boolean {
  return n >= 0xAD && in_ranges(n, DEFAULT_IGNORABLES);
}

function holds(xs: i32[], v: i32): boolean {
  for x in xs {
    if (x == v) {
      return true;
    }
  }
  return false;
}

// The pairs of an independent vowel and a matra that look like another
// vowel in the script `script`, from the list of invalid clusters of
// Microsoft. Each group is a vowel, its matras, and 0.
function invalid_pairs(script: string): i32[] {
  if (script == "deva") {
    return [0x905, 0x93A, 0x93B, 0x93E, 0x945, 0x946, 0x949, 0x94A, 0x94B, 0x94C, 0x94F, 0x956, 0x957, 0, 0x906, 0x93A, 0x945, 0x946, 0x947, 0x948, 0, 0x909, 0x941, 0, 0x90F, 0x945, 0x946, 0x947, 0];
  }
  if (script == "beng") {
    return [0x985, 0x9BE, 0, 0x98B, 0x9C3, 0, 0x98C, 0x9E2, 0];
  }
  if (script == "guru") {
    return [0xA05, 0xA3E, 0xA48, 0xA4C, 0, 0xA72, 0xA3F, 0xA40, 0xA47, 0, 0xA73, 0xA41, 0xA42, 0xA4B, 0];
  }
  if (script == "gujr") {
    return [0xA85, 0xABE, 0xAC5, 0xAC7, 0xAC8, 0xAC9, 0xACB, 0xACC, 0, 0xAC5, 0xABE, 0];
  }
  if (script == "orya") {
    return [0xB05, 0xB3E, 0, 0xB0F, 0xB57, 0, 0xB13, 0xB57, 0];
  }
  if (script == "taml") {
    return [0xB85, 0xBC2, 0];
  }
  if (script == "telu") {
    return [0xC12, 0xC4C, 0xC55, 0, 0xC3F, 0xC55, 0, 0xC46, 0xC55, 0, 0xC4A, 0xC55, 0];
  }
  if (script == "knda") {
    return [0xC89, 0xCBE, 0, 0xC8B, 0xCBE, 0, 0xC92, 0xCCC, 0];
  }
  return if (script == "mlym") { [0xD07, 0xD57, 0, 0xD09, 0xD57, 0, 0xD0E, 0xD46, 0, 0xD12, 0xD3E, 0xD57, 0] } else { [] };
}

// Whether the vowel `a` and the matra `b` are one of the pairs `pairs`.
function invalid_pair(pairs: i32[], a: i32, b: i32): boolean {
  let k = 0;
  while (k < pairs.len()) {
    let vowel = pairs[k];
    k += 1;
    while (k < pairs.len() && pairs[k] != 0) {
      if (vowel == a && pairs[k] == b) {
        return true;
      }
      k += 1;
    }
    k += 1;
  }
  return false;
}

// The index of the matra or the U+0907 after `i` that takes a dotted circle
// before it in the script `script`, or 0.
function circle_before(cps: char[], i: i32, script: string, pairs: i32[]): i32 {
  let n = cps.len();
  let c = cps[i] as i32;
  if (script == "deva" && c == 0x930 && i + 2 < n && cps[i + 1] as i32 == 0x94D && cps[i + 2] as i32 == 0x907) {
    return i + 2;
  }
  return if (i + 1 < n && invalid_pair(pairs, c, cps[i + 1] as i32)) { i + 1 } else { 0 };
}

// `cs` with a dotted circle before each matra of a pair of `invalid_pairs`
// for the script `script`, and in Devanagari before a U+0907 after a Ra and
// a virama.
function vowel_constrained(cs: Chars, script: string): Chars {
  let pairs = invalid_pairs(script);
  let cps: char[] = [];
  let clusters: i32[] = [];
  let i = 0;
  while (i < cs.cps.len()) {
    let at = circle_before(cs.cps, i, script, pairs);
    let to = if (at == 0) { i + 1 } else { at };
    for k in i..to {
      cps = cps.append(cs.cps[k]);
      clusters = clusters.append(cs.clusters[k]);
    }
    if (at != 0) {
      cps = cps.append(0x25CC as char).append(cs.cps[at]);
      clusters = clusters.append(cs.clusters[at]).append(cs.clusters[at]);
    }
    i = if (at == 0) { to } else { at + 1 };
  }
  return Chars { cps: cps, clusters: clusters };
}

// `cs` in the order of `mark_order`.
function ordered(cs: Chars, arabic: boolean): Chars {
  let cps: char[] = [];
  let clusters: i32[] = [];
  for k in mark_order(cs.cps, arabic) {
    cps = cps.append(cs.cps[k]);
    clusters = clusters.append(cs.clusters[k]);
  }
  return Chars { cps: cps, clusters: clusters };
}

// `cs` after the normalization of HarfBuzz, for the Indic shaper with
// `indic`. HarfBuzz composes only a text that holds a mark after its first
// character.
function normalized(f: truetype.Font, cs: Chars, arabic: boolean, indic: boolean): Chars {
  let parts = ordered(decomposed(f, cs, indic), arabic);
  for i in 1..cs.cps.len() {
    if (cs.cps[i].is_mark()) {
      return composed(f, parts, indic);
    }
  }
  return parts;
}

// `cs` with each character in its canonical decomposition, when the font
// `f` has a glyph for each part. The parts keep the cluster. Without
// `indic`, a character that has a glyph and no mark after it stays whole.
function decomposed(f: truetype.Font, cs: Chars, indic: boolean): Chars {
  let cps: char[] = [];
  let clusters: i32[] = [];
  for i in 0..cs.cps.len() {
    for part in parts_of(f, cs, i, indic) {
      cps = cps.append(part);
      clusters = clusters.append(cs.clusters[i]);
    }
  }
  return Chars { cps: cps, clusters: clusters };
}

// The characters that the character at `i` of `cs` becomes.
function parts_of(f: truetype.Font, cs: Chars, i: i32, indic: boolean): char[] {
  let c = cs.cps[i];
  if (stays_whole(f, cs, i, indic)) {
    return [c];
  }
  let parts = unicode.decomposition(c);
  for part in parts {
    if (f.glyph(part as i32) == 0) {
      return [c];
    }
  }
  return parts;
}

// Whether the character at `i` of `cs` keeps away from its decomposition:
// outside the Indic shaper, when the font has its glyph and no mark follows
// it, and in the Indic shaper, when it is one of KEPT_WHOLE.
function stays_whole(f: truetype.Font, cs: Chars, i: i32, indic: boolean): boolean {
  let c = cs.cps[i];
  if (indic) {
    return holds(KEPT_WHOLE, c as i32);
  }
  let next_mark = i + 1 < cs.cps.len() && cs.cps[i + 1].is_mark();
  return (i == 0 || !c.is_mark()) && !next_mark && f.glyph(c as i32) != 0;
}

// `cs` with each mark composed with the starter before it, when no mark
// between them has the same sort class or a higher one, and the font `f`
// has a glyph for the result. With `indic`, the starter must not be a mark.
// The characters from the starter to the mark take the first of their
// clusters.
function composed(f: truetype.Font, cs: Chars, indic: boolean): Chars {
  if (cs.cps.len() == 0) {
    return cs;
  }
  let cps = [cs.cps[0]];
  let clusters = [cs.clusters[0]];
  let starter = 0;
  for i in 1..cs.cps.len() {
    let c = cs.cps[i];
    let both = composite(f, cps, starter, c, indic);
    if (both != 0) {
      let cl = cs.clusters[i];
      for k in starter..cps.len() {
        cl = cl.min(clusters[k]);
      }
      cps = cps.with(starter, both as char);
      for k in starter..cps.len() {
        clusters = clusters.with(k, cl);
      }
      continue;
    }
    cps = cps.append(c);
    clusters = clusters.append(cs.clusters[i]);
    if (sort_class(c) == 0) {
      starter = cps.len() - 1;
    }
  }
  return Chars { cps: cps, clusters: clusters };
}

// The character that the mark `c` composes to with the starter at `starter`
// of `cps`, or 0.
function composite(f: truetype.Font, cps: char[], starter: i32, c: char, indic: boolean): i32 {
  let last = cps.len() - 1;
  let free = c.is_mark() && !(indic && cps[starter].is_mark()) && (starter == last || sort_class(cps[last]) < sort_class(c));
  let both = if (free) { composition_of(cps[starter], c, indic) } else { 0 };
  return if (both != 0 && f.glyph(both) != 0) { both } else { 0 };
}

// The composition of `a` and `b`. The Indic shaper also composes U+09AF
// and the nukta, which Unicode excludes.
function composition_of(a: char, b: char, indic: boolean): i32 {
  if (indic && a as i32 == 0x9AF && b as i32 == 0x9BC) {
    return 0x9DF;
  }
  return unicode.composition(a, b);
}

// A run of the glyphs of `cs` in the font `f`, with the masks `masks` and
// the properties of the characters. The shaper value of each glyph holds
// its character from the bit 9.
function run_of_chars(f: truetype.Font, cs: Chars, masks: i32[]): truetype.Run {
  let glyphs: i32[] = [];
  let props: i32[] = [];
  let vars: i32[] = [];
  for c in cs.cps {
    glyphs = glyphs.append(glyph_of(f, c as i32));
    props = props.append(char_props(c));
    vars = vars.append(c as i32 << 9);
  }
  return truetype.Run { ...truetype.run_of(glyphs, cs.clusters, masks), props: props, vars: vars };
}

// The glyph of `n` in `f`. A space that the font does not have takes the
// glyph of the space, and U+2011 takes that of U+2010, as in HarfBuzz.
function glyph_of(f: truetype.Font, n: i32): i32 {
  let g = f.glyph(n);
  if (g != 0) {
    return g;
  }
  if (space_width(n) != NO_SPACE) {
    return f.glyph(0x20);
  }
  return if (n == 0x2011) { f.glyph(0x2010) } else { 0 };
}

// The width that HarfBuzz gives a space with no glyph of its own: the em
// divided by the number, or the width of the space (0), of a digit
// (FIGURE_SPACE), of a full stop (PUNCTUATION_SPACE), of half a space
// (NARROW_SPACE), or of 4/18 of the em (MATH_SPACE).
const NO_SPACE: i32 = -1;

const FIGURE_SPACE: i32 = -2;

const PUNCTUATION_SPACE: i32 = -3;

const NARROW_SPACE: i32 = -4;

const MATH_SPACE: i32 = -5;

function space_width(n: i32): i32 {
  let spaces = [0x20, 0xA0, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200A, 0x202F, 0x205F, 0x3000];
  let widths = [0, 0, 2, 1, 2, 1, 3, 4, 6, FIGURE_SPACE, PUNCTUATION_SPACE, 5, 16, NARROW_SPACE, MATH_SPACE, 1];
  for k in 0..spaces.len() {
    if (spaces[k] == n) {
      return widths[k];
    }
  }
  return NO_SPACE;
}

// `r` with the advance of each space that took the glyph of the space, as
// HarfBuzz gives it before the GPOS lookups.
function with_space_widths(f: truetype.Font, r: truetype.Run): truetype.Run {
  let p = r.pos;
  for i in 0..r.glyphs.len() {
    let n = r.vars[i] >> 9;
    let w = space_width(n);
    if (w != NO_SPACE && w != 0 && f.glyph(n) == 0 && (r.props[i] & truetype.LIGATED) == 0) {
      p = truetype.Positions { ...p, advances: p.advances.with(i, fallback_advance(f, w, p.advances[i])) };
    }
  }
  return truetype.Run { ...r, pos: p };
}

// The advance of a space of the width `w` in `f`, from the advance
// `advance` of the space.
function fallback_advance(f: truetype.Font, w: i32, advance: i32): i32 {
  let em = f.units_per_em;
  if (w > 0) {
    return (em + w / 2) / w;
  }
  if (w == MATH_SPACE) {
    return em * 4 / 18;
  }
  if (w == NARROW_SPACE) {
    return advance / 2;
  }
  let chars = if (w == FIGURE_SPACE) { [0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39] } else { [0x2E, 0x2C] };
  for c in chars {
    if (f.glyph(c) != 0) {
      return f.advance(f.glyph(c));
    }
  }
  return advance;
}
```

### `marks`

HarfBuzz sorts a sequence of marks by a combining class that it changes for some scripts. For Arabic, the shadda takes the class 27 and the marks of the classes 27 to 32 move up by one. The Hebrew points take the order of their use. The Telugu length marks and the Thai vowels below go before the virama and the other marks. The sort is stable, and it does not change a sequence of more than 32 marks.

```fern
<<marks>>=
// The most marks in a sequence that the sort changes.
const MAX_MARKS: i32 = 32;

// The classes that HarfBuzz gives the combining classes 10 to 36: the
// Hebrew points, the Arabic marks and the superscript alaph.
const MODIFIED_CLASSES: i32[] = [22, 15, 16, 17, 23, 18, 19, 20, 21, 14, 24, 12, 25, 13, 10, 11, 26, 28, 29, 30, 31, 32, 33, 27, 34, 35, 36];

// The combining class of `c`, as HarfBuzz changes it to sort the marks.
function sort_class(c: char): i32 {
  let n = c as i32;
  if (n == 0x1A60 || n == 0xFC6) {
    return 254;
  }
  if (n == 0xF39) {
    return 127;
  }
  let k = unicode.combining_class(c);
  if (k >= 10 && k <= 36) {
    return MODIFIED_CLASSES[k - 10];
  }
  let from = [84, 91, 103, 130, 132];
  let to = [4, 5, 3, 132, 131];
  for i in 0..from.len() {
    if (k == from[i]) {
      return to[i];
    }
  }
  return k;
}

// The modifier combining marks of Unicode Technical Report 53.
function is_mcm(c: char): boolean {
  for m in [0x654, 0x655, 0x658, 0x6DC, 0x6E3, 0x6E7, 0x6E8, 0x8CA, 0x8CB, 0x8CD, 0x8CE, 0x8CF, 0x8D3, 0x8F3] {
    if (c as i32 == m) {
      return true;
    }
  }
  return false;
}

// The indices of `cps` in the order of their marks. With `arabic`, the
// modifier combining marks of each class from 220 to 230 go first.
function mark_order(cps: char[], arabic: boolean): i32[] {
  let order: i32[] = [];
  let i = 0;
  while (i < cps.len()) {
    let j = i;
    while (j < cps.len() && unicode.combining_class(cps[j]) != 0) {
      j += 1;
    }
    if (j == i) {
      order = order.append(i);
      i += 1;
      continue;
    }
    let run = sorted_marks(cps, i, j);
    if (arabic) {
      run = mcm_first(cps, run);
    }
    for k in run {
      order = order.append(k);
    }
    i = j;
  }
  return order;
}

// The indices from `i` to `j` in the order of their sort classes.
function sorted_marks(cps: char[], i: i32, j: i32): i32[] {
  let out: i32[] = [];
  for k in i..j {
    out = out.append(k);
  }
  if (j - i > MAX_MARKS) {
    return out;
  }
  for a in 1..out.len() {
    let b = a;
    while (b > 0 && sort_class(cps[out[b - 1]]) > sort_class(cps[out[b]])) {
      let t = out[b];
      out = out.with(b, out[b - 1]).with(b - 1, t);
      b -= 1;
    }
  }
  return out;
}

// The marks `run`, with the modifier combining marks of the class 220 at
// the start, then those of the class 230 after the marks of 220.
function mcm_first(cps: char[], run: i32[]): i32[] {
  let out = run;
  let start = 0;
  let i = 0;
  for cc in [220, 230] {
    while (i < out.len() && sort_class(cps[out[i]]) < cc) {
      i += 1;
    }
    if (i == out.len()) {
      break;
    }
    if (sort_class(cps[out[i]]) > cc) {
      continue;
    }
    let j = i;
    while (j < out.len() && sort_class(cps[out[j]]) == cc && is_mcm(cps[out[j]])) {
      j += 1;
    }
    if (j == i) {
      continue;
    }
    out = moved_to(out, start, i, j);
    start += j - i;
    i = j;
  }
  return out;
}

// `xs` with the items from `i` to `j` moved to the index `start`.
function moved_to(xs: i32[], start: i32, i: i32, j: i32): i32[] {
  let out: i32[] = [];
  for k in 0..start {
    out = out.append(xs[k]);
  }
  for k in i..j {
    out = out.append(xs[k]);
  }
  for k in start..i {
    out = out.append(xs[k]);
  }
  for k in j..xs.len() {
    out = out.append(xs[k]);
  }
  return out;
}
```

### `joining`

The joining type of each character comes from `std/unicode`. A letter joins the letter before it when that letter joins on its left side, as a dual-joining, left-joining or join-causing letter does. It joins the letter after it when that letter joins on its right side, as a dual-joining, right-joining or join-causing letter does. A transparent character, such as a mark, is not a neighbour. A character that does not join takes no form.

```fern
<<joining>>=
// The joining types of std/unicode.
const JT_U: i32 = 0;

const JT_C: i32 = 1;

const JT_D: i32 = 2;

const JT_R: i32 = 3;

const JT_L: i32 = 4;

const JT_T: i32 = 5;

// The form of each character of `cps`: ISOL, FINA, MEDI or INIT, or 0.
function forms(cps: char[]): i32[] {
  let out: i32[] = [];
  let prev = -1;
  let prev_type = JT_U;
  for i in 0..cps.len() {
    let t = unicode.joining_type(cps[i]);
    out = out.append(0);
    if (t == JT_T) {
      continue;
    }
    if (t == JT_U) {
      prev = -1;
      continue;
    }
    let joins_left = prev_type == JT_D || prev_type == JT_L || prev_type == JT_C;
    if (prev >= 0 && joins_left && t != JT_L) {
      out = out.with(i, FINA).with(prev, if (out[prev] == FINA) { MEDI } else { INIT });
    } else {
      out = out.with(i, ISOL);
    }
    prev = i;
    prev_type = t;
  }
  return out;
}
```

### `stages`

Each feature of a stage has a mask and the flags of its lookups. A lookup of two features of a stage applies with the masks of both and the flags of both.

```fern
<<stages>>=
// A feature of a stage: its tag, the mask of the glyphs that it applies to,
// and the flags of its lookups for `truetype.substitute`.
struct Feature { tag: string, mask: i32, opts: i32 }

function feature(tag: string, mask: i32, opts: i32): Feature {
  return Feature { tag: tag, mask: mask, opts: opts };
}

// The GPOS features, which apply in one stage. The lookups of `mark` and
// `mkmk` do not skip a ZWJ.
function position_features(): Feature[] {
  let manual = truetype.MANUAL_ZWNJ | truetype.MANUAL_ZWJ;
  return [feature("abvm", GLOBAL, 0), feature("blwm", GLOBAL, 0), feature("curs", GLOBAL, 0), feature("dist", GLOBAL, 0), feature("kern", GLOBAL, 0), feature("mark", GLOBAL, manual), feature("mkmk", GLOBAL, manual)];
}

// The features of each stage for the script `script` in the font `f`. In
// Arabic, a ZWJ stops a ligature, and `calt` has a stage of its own only
// when the font has no `rclt`.
function stages(f: truetype.Font, script: string): Feature[][] {
  let zwj = truetype.MANUAL_ZWJ;
  if (script == "arab") {
    let start = [[feature("rvrn", GLOBAL, 0)], [feature("ccmp", GLOBAL, zwj), feature("locl", GLOBAL, zwj)], [feature("isol", ISOL, zwj)], [feature("fina", FINA, zwj)], [feature("medi", MEDI, zwj)], [feature("init", INIT, zwj)], [feature("rlig", GLOBAL, zwj)]];
    let last = [feature("liga", GLOBAL, zwj), feature("clig", GLOBAL, zwj), feature("mset", GLOBAL, zwj)];
    if (f.has_feature(script, "rclt")) {
      return start.append([feature("calt", GLOBAL, zwj), feature("rclt", GLOBAL, 0)].concat(last));
    }
    return start.append([feature("calt", GLOBAL, zwj)]).append(last);
  }
  let all = ["ccmp", "locl", "rlig", "calt", "clig", "liga", "rclt"].map((tag: string): Feature => feature(tag, GLOBAL, 0));
  return [[feature("rvrn", GLOBAL, 0)], all];
}

// The glyphs of `text` in the font `f`, for the script `script`, an
// OpenType tag such as "arab", "deva" or "latn".
pub function shape(f: truetype.Font, text: string, script: string): truetype.Run {
  if let Some(c) = indic_config(script) {
    if let Some(p) = indic_plan(f, c) {
      return indic_shaped(f, text, p);
    }
  }
  let arabic = script == "arab";
  let cs = normalized(f, characters(text), arabic, false);
  let shapes = forms(cs.cps);
  let masks: i32[] = [];
  for i in 0..cs.cps.len() {
    masks = masks.append(GLOBAL | (if (arabic) { shapes[i] } else { 0 }));
  }
  let r = run_of_chars(f, cs, masks);
  for stage in stages(f, script) {
    r = applied_stage(f, r, script, stage, false);
  }
  r = with_space_widths(f, f.with_advances(r, arabic));
  r = applied_stage(f, r, script, position_features(), true);
  return finished(f, r, true);
}

// `r` after the lookups of the features `features`, in the order of their
// indices: those of GPOS with `gpos`, else those of GSUB.
function applied_stage(f: truetype.Font, r: truetype.Run, script: string, features: Feature[], gpos: boolean): truetype.Run {
  let lookups: i32[] = [];
  let masks: i32[] = [];
  let opts: i32[] = [];
  for ft in features {
    for li in if (gpos) { f.position_lookups(script, ft.tag) } else { f.feature_lookups(script, ft.tag) } {
      let at = 0;
      while (at < lookups.len() && lookups[at] < li) {
        at += 1;
      }
      if (at < lookups.len() && lookups[at] == li) {
        masks = masks.with(at, masks[at] | ft.mask);
        opts = opts.with(at, opts[at] | ft.opts);
        continue;
      }
      lookups = inserted_at(lookups, at, li);
      masks = inserted_at(masks, at, ft.mask);
      opts = inserted_at(opts, at, ft.opts);
    }
  }
  let run = r;
  for k in 0..lookups.len() {
    run = if (gpos) { f.position(run, lookups[k], masks[k], opts[k]) } else { f.substitute(run, lookups[k], masks[k], opts[k]) };
  }
  return run;
}

function inserted_at(xs: i32[], at: i32, v: i32): i32[] {
  let out: i32[] = [];
  for k in 0..xs.len() {
    if (k == at) {
      out = out.append(v);
    }
    out = out.append(xs[k]);
  }
  return if (at >= xs.len()) { out.append(v) } else { out };
}
```

### `syllables`

The grammar of the syllables is that of the Indic shaper of HarfBuzz. In it, `c` is a consonant or a Ra, and `z` is a ZWJ or a ZWNJ. A `reph` is a Ra and a virama, or a repha:

```text
n = ((ZWNJ? RS)? (N N?)?)
cn = c ZWJ? n?
matra_group = z* (M | SM? MPst) N? H?
syllable_tail = (z? SM SM? ZWNJ?)? A*
halant_group = z? H (ZWJ N?)?
final_halant_group = halant_group | H ZWNJ
halant_or_matra_group = final_halant_group | matra_group*
complex_syllable_tail = (halant_group cn)* CM? halant_or_matra_group syllable_tail

consonant_syllable = (Repha | CS)? cn complex_syllable_tail
vowel_syllable = reph? V n? (ZWJ | complex_syllable_tail)
standalone_cluster = ((Repha | CS)? PLACEHOLDER | reph? DOTTEDCIRCLE) n? complex_syllable_tail
symbol_cluster = Symbol N? syllable_tail
broken_cluster = reph? n? complex_syllable_tail
```

At each character the longest match is the syllable. Of two matches of the same length, the first in the list is the syllable. A character that no rule matches is a syllable alone. Each rule here takes a list of the positions where a match can start, in order, and gives the positions where a match can end.

```fern
<<syllables>>=
// The Indic categories of HarfBuzz.
const CAT_C: i32 = 1;

const CAT_V: i32 = 2;

const CAT_N: i32 = 3;

const CAT_H: i32 = 4;

const CAT_ZWNJ: i32 = 5;

const CAT_ZWJ: i32 = 6;

const CAT_M: i32 = 7;

const CAT_SM: i32 = 8;

const CAT_A: i32 = 10;

const CAT_PLACEHOLDER: i32 = 11;

const CAT_DOTTEDCIRCLE: i32 = 12;

const CAT_RS: i32 = 13;

const CAT_MPST: i32 = 14;

const CAT_REPHA: i32 = 15;

const CAT_RA: i32 = 16;

const CAT_CM: i32 = 17;

const CAT_SYMBOL: i32 = 18;

const CAT_CS: i32 = 19;

const CAT_SMPST: i32 = 20;

// The types of syllable that the shaper tests, by the place of their rule
// in the list. A consonant syllable is 0.
const SYL_SYMBOL: i32 = 3;

const SYL_BROKEN: i32 = 4;

const SYL_OTHER: i32 = 5;

// The bit of the category `c`, for a set of categories.
function cat_bit(c: i32): i32 {
  return 1 << c;
}

// `r` with the syllable of each glyph in its properties: the number of the
// syllable from 1, then its type in the four low bits.
function with_syllables(r: truetype.Run): truetype.Run {
  let cats: i32[] = [];
  for v in r.vars {
    cats = cats.append(v & 31);
  }
  let props = r.props;
  let serial = 1;
  let i = 0;
  while (i < cats.len()) {
    let (end, kind) = syllable_from(cats, i);
    for k in i..end {
      props = props.with(k, props[k] & 255 | (serial << 4 | kind) << truetype.SYLLABLE_SHIFT);
    }
    serial += 1;
    i = end;
  }
  return truetype.Run { ...r, props: props };
}

// The end and the type of the syllable that starts at `i`. A lone SMPst is
// a syllable of no type, before the broken clusters.
function syllable_from(cats: i32[], i: i32): (i32, i32) {
  let at = [i];
  let ends = [consonant_syllable(cats, at), vowel_syllable(cats, at), standalone_cluster(cats, at), symbol_cluster(cats, at), broken_cluster(cats, at)];
  let best = i;
  let kind = SYL_OTHER;
  for k in 0..ends.len() {
    let e = ends[k];
    if (k == SYL_BROKEN && cats[i] == CAT_SMPST && best == i) {
      best = i + 1;
    }
    if (e.len() > 0 && e[e.len() - 1] > best) {
      best = e[e.len() - 1];
      kind = k;
    }
  }
  return if (best == i) { (i + 1, SYL_OTHER) } else { (best, kind) };
}

// The positions after a character of a category of `set` at each position
// of `xs`.
function step(cats: i32[], xs: i32[], set: i32): i32[] {
  let out: i32[] = [];
  for x in xs {
    if (x < cats.len() && (set >> cats[x] & 1) != 0) {
      out = out.append(x + 1);
    }
  }
  return out;
}

// The positions of `xs` and `ys`, in order, each one time.
function either(xs: i32[], ys: i32[]): i32[] {
  let out: i32[] = [];
  let (i, j) = (0, 0);
  while (i < xs.len() || j < ys.len()) {
    let take_x = j >= ys.len() || i < xs.len() && xs[i] <= ys[j];
    let v = if (take_x) { xs[i] } else { ys[j] };
    if (take_x) {
      i += 1;
    } else {
      j += 1;
    }
    if (out.len() == 0 || out[out.len() - 1] != v) {
      out = out.append(v);
    }
  }
  return out;
}

// The positions of `xs`, and those after one character of `set`.
function optional(cats: i32[], xs: i32[], set: i32): i32[] {
  return either(xs, step(cats, xs, set));
}

// The positions after any count of characters of `set`.
function any_of(cats: i32[], xs: i32[], set: i32): i32[] {
  let all = xs;
  let next = xs;
  while (next.len() > 0) {
    next = step(cats, next, set);
    all = either(all, next);
  }
  return all;
}

function joiner_bits(): i32 {
  return cat_bit(CAT_ZWJ) | cat_bit(CAT_ZWNJ);
}

// n = ((ZWNJ? RS)? (N N?)?)
function modifiers(cats: i32[], xs: i32[]): i32[] {
  let a = either(xs, step(cats, optional(cats, xs, cat_bit(CAT_ZWNJ)), cat_bit(CAT_RS)));
  return either(a, optional(cats, step(cats, a, cat_bit(CAT_N)), cat_bit(CAT_N)));
}

// cn = c ZWJ? n?
function consonant_group(cats: i32[], xs: i32[]): i32[] {
  return modifiers(cats, optional(cats, step(cats, xs, cat_bit(CAT_C) | cat_bit(CAT_RA)), cat_bit(CAT_ZWJ)));
}

// reph?
function optional_reph(cats: i32[], xs: i32[]): i32[] {
  let ra = step(cats, step(cats, xs, cat_bit(CAT_RA)), cat_bit(CAT_H));
  return either(xs, either(ra, step(cats, xs, cat_bit(CAT_REPHA))));
}

// matra_group = z* (M | sm? MPst) N? H?
function matra_group(cats: i32[], xs: i32[]): i32[] {
  let a = any_of(cats, xs, joiner_bits());
  let m = either(step(cats, a, cat_bit(CAT_M)), step(cats, optional(cats, a, sm_bits()), cat_bit(CAT_MPST)));
  return optional(cats, optional(cats, m, cat_bit(CAT_N)), cat_bit(CAT_H));
}

// sm = SM | SMPst
function sm_bits(): i32 {
  return cat_bit(CAT_SM) | cat_bit(CAT_SMPST);
}

// syllable_tail = (z? sm sm? ZWNJ?)? A*
function syllable_tail(cats: i32[], xs: i32[]): i32[] {
  let a = step(cats, optional(cats, xs, joiner_bits()), sm_bits());
  let b = optional(cats, optional(cats, a, sm_bits()), cat_bit(CAT_ZWNJ));
  return any_of(cats, either(xs, b), cat_bit(CAT_A));
}

// halant_group = z? H (ZWJ N?)?
function halant_group(cats: i32[], xs: i32[]): i32[] {
  let a = step(cats, optional(cats, xs, joiner_bits()), cat_bit(CAT_H));
  return either(a, optional(cats, step(cats, a, cat_bit(CAT_ZWJ)), cat_bit(CAT_N)));
}

// halant_or_matra_group = (halant_group | H ZWNJ) | matra_group*
function halant_or_matra_group(cats: i32[], xs: i32[]): i32[] {
  let final_halant = either(halant_group(cats, xs), step(cats, step(cats, xs, cat_bit(CAT_H)), cat_bit(CAT_ZWNJ)));
  let all = xs;
  let next = xs;
  while (next.len() > 0) {
    next = matra_group(cats, next);
    all = either(all, next);
  }
  return either(final_halant, all);
}

// complex_syllable_tail = (halant_group cn)* CM? halant_or_matra_group syllable_tail
function complex_tail(cats: i32[], xs: i32[]): i32[] {
  let all = xs;
  let next = xs;
  while (next.len() > 0) {
    next = consonant_group(cats, halant_group(cats, next));
    all = either(all, next);
  }
  return syllable_tail(cats, halant_or_matra_group(cats, optional(cats, all, cat_bit(CAT_CM))));
}

// consonant_syllable = (Repha | CS)? cn complex_syllable_tail
function consonant_syllable(cats: i32[], xs: i32[]): i32[] {
  return complex_tail(cats, consonant_group(cats, optional(cats, xs, cat_bit(CAT_REPHA) | cat_bit(CAT_CS))));
}

// vowel_syllable = reph? V n? (ZWJ | complex_syllable_tail)
function vowel_syllable(cats: i32[], xs: i32[]): i32[] {
  let a = modifiers(cats, step(cats, optional_reph(cats, xs), cat_bit(CAT_V)));
  return either(step(cats, a, cat_bit(CAT_ZWJ)), complex_tail(cats, a));
}

// standalone_cluster = ((Repha | CS)? PLACEHOLDER | reph? DOTTEDCIRCLE) n? complex_syllable_tail
function standalone_cluster(cats: i32[], xs: i32[]): i32[] {
  let held = step(cats, optional(cats, xs, cat_bit(CAT_REPHA) | cat_bit(CAT_CS)), cat_bit(CAT_PLACEHOLDER));
  let circle = step(cats, optional_reph(cats, xs), cat_bit(CAT_DOTTEDCIRCLE));
  return complex_tail(cats, modifiers(cats, either(held, circle)));
}

// symbol_cluster = Symbol N? syllable_tail
function symbol_cluster(cats: i32[], xs: i32[]): i32[] {
  return syllable_tail(cats, optional(cats, step(cats, xs, cat_bit(CAT_SYMBOL)), cat_bit(CAT_N)));
}

// broken_cluster = reph? n? complex_syllable_tail
function broken_cluster(cats: i32[], xs: i32[]): i32[] {
  return complex_tail(cats, modifiers(cats, optional_reph(cats, xs)));
}
```

### `indic`

The reorderings work on a list of glyph records. The shaper value of each glyph holds three fields. The bits 0 to 4 hold the Indic category, and the bits 5 to 8 the Indic position. The character is from the bit 9. An inserted dotted circle has the character 0.

The steps follow `hb-ot-shaper-indic.cc` of HarfBuzz, for the configuration of Devanagari. The base is the last consonant that has no below-base or post-base form. The reph goes to the end of the syllable, before the syllable modifiers. A pre-base matra goes after the last halant that has no ZWJ after it.

```fern
<<indic>>=
// The Indic positions of HarfBuzz.
const POS_RA_TO_BECOME_REPH: i32 = 1;

const POS_PRE_M: i32 = 2;

const POS_PRE_C: i32 = 3;

const POS_BASE_C: i32 = 4;

const POS_AFTER_MAIN: i32 = 5;

const POS_BELOW_C: i32 = 8;

const POS_POST_C: i32 = 11;

const POS_AFTER_POST: i32 = 12;

const POS_SMVD: i32 = 13;

const POS_END: i32 = 14;

// The reph positions and the reph modes of HarfBuzz.
const REPH_BEFORE_POST: i32 = 0;

const REPH_AFTER_MAIN: i32 = 1;

const REPH_BEFORE_SUB: i32 = 2;

const REPH_AFTER_SUB: i32 = 3;

const REPH_AFTER_POST: i32 = 4;

const MODE_IMPLICIT: i32 = 0;

const MODE_EXPLICIT: i32 = 1;

const MODE_LOG_REPHA: i32 = 2;

// The configuration of an Indic script: its old tag and its new tag, its
// virama, where its reph goes, how a reph forms, and whether the
// below-base forms apply only after the base.
struct Config { script: string, tag: string, virama: i32, reph_pos: i32, reph_mode: i32, post_only: boolean }

// The characters that the Indic shaper does not decompose.
const KEPT_WHOLE: i32[] = [0x931, 0x9DC, 0x9DD, 0xB94];

// What the Indic shaper keeps for a font: the configuration of the script,
// the script tags of its GSUB and GPOS tables, whether the rules of the old
// specification apply, whether a context with glyphs around its input is
// left out when the shaper asks which lookups apply, the glyph of the
// virama, and whether the font has the features rphf and pref.
struct Plan { config: Config, gsub: string, gpos: string, old_spec: boolean, zero: boolean, virama: i32, rphf: boolean, pref: boolean }

// A glyph of a run, with its fields of the run. `key` holds the place of
// the glyph before a sort.
struct Glyph { id: i32, cluster: i32, mask: i32, lig: i32, prop: i32, var: i32, key: i32 }

// The configuration of the Indic script `script`, an old OpenType tag such
// as "deva", as indic_configs of HarfBuzz gives it, or None.
function indic_config(script: string): Option[Config] {
  let scripts = ["deva", "beng", "guru", "gujr", "orya", "taml", "telu", "knda", "mlym"];
  let tags = ["dev2", "bng2", "gur2", "gjr2", "ory2", "tml2", "tel2", "knd2", "mlm2"];
  let viramas = [0x94D, 0x9CD, 0xA4D, 0xACD, 0xB4D, 0xBCD, 0xC4D, 0xCCD, 0xD4D];
  let rephs = [REPH_BEFORE_POST, REPH_AFTER_SUB, REPH_BEFORE_SUB, REPH_BEFORE_POST, REPH_AFTER_MAIN, REPH_AFTER_POST, REPH_AFTER_POST, REPH_AFTER_POST, REPH_AFTER_MAIN];
  let modes = [MODE_IMPLICIT, MODE_IMPLICIT, MODE_IMPLICIT, MODE_IMPLICIT, MODE_IMPLICIT, MODE_IMPLICIT, MODE_EXPLICIT, MODE_IMPLICIT, MODE_LOG_REPHA];
  for k in 0..scripts.len() {
    if (scripts[k] == script) {
      let post_only = script == "telu" || script == "knda";
      return Some(Config { script: script, tag: tags[k], virama: viramas[k], reph_pos: rephs[k], reph_mode: modes[k], post_only: post_only });
    }
  }
  return None;
}

// The plan of the Indic shaper for the script of `c` in `f`, or None when
// the default shaper applies.
function indic_plan(f: truetype.Font, c: Config): Option[Plan] {
  let gsub = if (f.has_script(c.tag, false)) { c.tag } else { c.script };
  let neither = gsub == c.script && !f.has_script(c.script, false);
  if (neither && (f.has_script("DFLT", false) || !f.has_script("dflt", false) && f.has_script("latn", false))) {
    return None;
  }
  let gpos = if (f.has_script(c.tag, true)) { c.tag } else { c.script };
  let old_spec = gsub != c.tag;
  return Some(Plan { config: c, gsub: gsub, gpos: gpos, old_spec: old_spec, zero: !old_spec && c.script != "mlym", virama: f.glyph(c.virama), rphf: f.has_feature(gsub, "rphf"), pref: f.has_feature(gsub, "pref") });
}

// The features of the Indic shaper between the two reorderings, one stage
// each.
function basic_features(): Feature[] {
  let flags = truetype.PER_SYLLABLE | truetype.MANUAL_ZWNJ | truetype.MANUAL_ZWJ;
  let tags = ["nukt", "akhn", "rphf", "rkrf", "pref", "blwf", "abvf", "half", "pstf", "vatu", "cjct"];
  let masks = [GLOBAL, GLOBAL, RPHF, GLOBAL, PREF, BLWF, ABVF, HALF, PSTF, GLOBAL, GLOBAL];
  let out: Feature[] = [];
  for k in 0..tags.len() {
    out = out.append(feature(tags[k], masks[k], flags));
  }
  return out;
}

// The features of the stage after the final reordering.
function final_features(): Feature[] {
  let flags = truetype.PER_SYLLABLE | truetype.MANUAL_ZWNJ | truetype.MANUAL_ZWJ;
  let out = [feature("init", INIT, flags)];
  for tag in ["pres", "abvs", "blws", "psts", "haln"] {
    out = out.append(feature(tag, GLOBAL, flags));
  }
  for tag in ["rlig", "calt", "clig", "rclt"] {
    out = out.append(feature(tag, GLOBAL, 0));
  }
  return out;
}

// The glyphs of the text `text` of an Indic script in the font `f`.
function indic_shaped(f: truetype.Font, text: string, p: Plan): truetype.Run {
  let cs = normalized(f, vowel_constrained(characters(text), p.config.script), false, true);
  let vars: i32[] = [];
  let masks: i32[] = [];
  for c in cs.cps {
    vars = vars.append(indic.properties(c as i32) | c as i32 << 9);
    masks = masks.append(GLOBAL);
  }
  let r = truetype.Run { ...run_of_chars(f, cs, masks), vars: vars };
  r = applied_stage(f, r, p.gsub, [feature("rvrn", GLOBAL, 0)], false);
  r = with_syllables(r);
  let per = truetype.PER_SYLLABLE;
  r = applied_stage(f, r, p.gsub, [feature("locl", GLOBAL, per), feature("ccmp", GLOBAL, per)], false);
  r = run_of_glyphs(r, initially_reordered(f, p, glyphs_of(r)));
  for ft in basic_features() {
    r = applied_stage(f, r, p.gsub, [ft], false);
  }
  r = run_of_glyphs(r, finally_reordered(p, glyphs_of(r)));
  r = applied_stage(f, r, p.gsub, final_features(), false);
  r = with_space_widths(f, f.with_advances(r, false));
  r = applied_stage(f, r, p.gpos, position_features(), true);
  return finished(f, r, false);
}

function glyphs_of(r: truetype.Run): Glyph[] {
  let out: Glyph[] = [];
  for i in 0..r.glyphs.len() {
    out = out.append(Glyph { id: r.glyphs[i], cluster: r.clusters[i], mask: r.masks[i], lig: r.ligs[i], prop: r.props[i], var: r.vars[i], key: 0 });
  }
  return out;
}

// `r` with the glyphs `gs`. `r` has no positions yet.
function run_of_glyphs(r: truetype.Run, gs: Glyph[]): truetype.Run {
  let ids: i32[] = [];
  let cs: i32[] = [];
  let ms: i32[] = [];
  let ls: i32[] = [];
  let ps: i32[] = [];
  let vs: i32[] = [];
  for g in gs {
    ids = ids.append(g.id);
    cs = cs.append(g.cluster);
    ms = ms.append(g.mask);
    ls = ls.append(g.lig);
    ps = ps.append(g.prop);
    vs = vs.append(g.var);
  }
  return truetype.Run { ...r, glyphs: ids, clusters: cs, masks: ms, ligs: ls, props: ps, vars: vs };
}

function cat_of(g: Glyph): i32 {
  return g.var & 31;
}

function pos_of(g: Glyph): i32 {
  return g.var >> 5 & 15;
}

function with_pos(g: Glyph, pos: i32): Glyph {
  return Glyph { ...g, var: g.var & ~480 | pos << 5 };
}

function syllable_of(g: Glyph): i32 {
  return g.prop >> truetype.SYLLABLE_SHIFT;
}

function has_prop(g: Glyph, prop: i32): boolean {
  return (g.prop & prop) != 0;
}

// Whether `g` is of a category of `set`. A glyph that ligated is of no
// category.
function is_one_of(g: Glyph, set: i32): boolean {
  return !has_prop(g, truetype.LIGATED) && (set >> cat_of(g) & 1) != 0;
}

// Whether the category of `g` is in `set`, when it ligated too.
function in_set(g: Glyph, set: i32): boolean {
  return (set >> cat_of(g) & 1) != 0;
}

function consonant_bits(): i32 {
  return cat_bit(CAT_C) | cat_bit(CAT_CS) | cat_bit(CAT_RA) | cat_bit(CAT_CM) | cat_bit(CAT_V) | cat_bit(CAT_PLACEHOLDER) | cat_bit(CAT_DOTTEDCIRCLE);
}

// Whether `g` is a ligature and not the part of a multiple substitution.
function ligated_alone(g: Glyph): boolean {
  return has_prop(g, truetype.LIGATED) && !has_prop(g, truetype.MULTIPLIED);
}

// The end of the syllable that starts at `start`.
function syllable_end(gs: Glyph[], start: i32): i32 {
  let end = start + 1;
  while (end < gs.len() && syllable_of(gs[end]) == syllable_of(gs[start])) {
    end += 1;
  }
  return end;
}

// `gs` with the clusters from `start` to `end` joined, as the
// merge_clusters of HarfBuzz joins them: the range grows over the glyphs
// next to it that have the cluster of its ends.
function merged(gs: Glyph[], start: i32, end: i32): Glyph[] {
  if (end - start < 2) {
    return gs;
  }
  let cl = gs[start].cluster;
  for i in start + 1..end {
    cl = cl.min(gs[i].cluster);
  }
  let (a, b) = (start, end);
  if (cl != gs[b - 1].cluster) {
    while (b < gs.len() && gs[b - 1].cluster == gs[b].cluster) {
      b += 1;
    }
  }
  if (cl != gs[a].cluster) {
    while (a > 0 && gs[a - 1].cluster == gs[a].cluster) {
      a -= 1;
    }
  }
  let out = gs;
  for i in a..b {
    out = out.with(i, Glyph { ...out[i], cluster: cl });
  }
  return out;
}

// `gs` with the glyph at `from` moved to `to`, and the glyphs between them
// moved by one.
function moved(gs: Glyph[], from: i32, to: i32): Glyph[] {
  let g = gs[from];
  let out = gs;
  if (from < to) {
    for i in from..to {
      out = out.with(i, out[i + 1]);
    }
  } else {
    let i = from;
    while (i > to) {
      out = out.with(i, out[i - 1]);
      i -= 1;
    }
  }
  return out.with(to, g);
}

// `gs` with the glyphs from `start` to `end` in the other order.
function reversed(gs: Glyph[], start: i32, end: i32): Glyph[] {
  let out = gs;
  for k in 0..end - start {
    out = out.with(start + k, gs[end - 1 - k]);
  }
  return out;
}

// The glyphs `gs` after the initial reordering of each syllable.
function initially_reordered(f: truetype.Font, p: Plan, gs0: Glyph[]): Glyph[] {
  let gs = with_dotted_circles(f, with_consonant_positions(f, p, gs0));
  let start = 0;
  while (start < gs.len()) {
    let end = syllable_end(gs, start);
    let kind = syllable_of(gs[start]) & 15;
    if (kind <= SYL_BROKEN && kind != SYL_SYMBOL) {
      gs = reordered_syllable(f, p, gs, start, end);
    }
    start = end;
  }
  return gs;
}

// `gs` with the position of each consonant from the font: below the base
// when the lookups of blwf or vatu join it with the virama, and after the
// base when those of pstf or pref do.
function with_consonant_positions(f: truetype.Font, p: Plan, gs0: Glyph[]): Glyph[] {
  if (p.virama == 0) {
    return gs0;
  }
  let gs = gs0;
  let known: i32[] = [];
  let found: i32[] = [];
  for i in 0..gs.len() {
    if (pos_of(gs[i]) != POS_BASE_C) {
      continue;
    }
    let k = 0;
    while (k < known.len() && known[k] != gs[i].id) {
      k += 1;
    }
    if (k == known.len()) {
      known = known.append(gs[i].id);
      found = found.append(consonant_position(f, p, gs[i].id));
    }
    gs = gs.with(i, with_pos(gs[i], found[k]));
  }
  return gs;
}

function consonant_position(f: truetype.Font, p: Plan, g: i32): i32 {
  if (with_virama(f, p, "blwf", g) || with_virama(f, p, "vatu", g)) {
    return POS_BELOW_C;
  }
  if (with_virama(f, p, "pstf", g) || with_virama(f, p, "pref", g)) {
    return POS_POST_C;
  }
  return POS_BASE_C;
}

// Whether a lookup of `tag` joins the virama and the glyph `g`, in either
// order: some fonts have the order of the old specification in new tables.
function with_virama(f: truetype.Font, p: Plan, tag: string, g: i32): boolean {
  return f.would_substitute(p.gsub, tag, [p.virama, g], p.zero) || f.would_substitute(p.gsub, tag, [g, p.virama], p.zero);
}

// `gs` with a dotted circle at the start of each broken cluster, after a
// repha, when the font has the glyph.
function with_dotted_circles(f: truetype.Font, gs: Glyph[]): Glyph[] {
  let circle = f.glyph(0x25CC);
  if (circle == 0) {
    return gs;
  }
  let out: Glyph[] = [];
  let last = 0;
  let i = 0;
  while (i < gs.len()) {
    let s = syllable_of(gs[i]);
    if (s == last || (s & 15) != SYL_BROKEN) {
      out = out.append(gs[i]);
      i += 1;
      continue;
    }
    last = s;
    let g = Glyph { id: circle, cluster: gs[i].cluster, mask: gs[i].mask, lig: 0, prop: s << truetype.SYLLABLE_SHIFT, var: CAT_DOTTEDCIRCLE | POS_END << 5, key: 0 };
    while (i < gs.len() && syllable_of(gs[i]) == s && cat_of(gs[i]) == CAT_REPHA) {
      out = out.append(gs[i]);
      i += 1;
    }
    out = out.append(g);
  }
  return out;
}

// The index of the base of the syllable from `start` to `end`, the last
// consonant that has no below-base or post-base form, and whether a reph
// forms at the start.
function base_of(f: truetype.Font, p: Plan, gs: Glyph[], start: i32, end: i32): (i32, boolean) {
  let limit = reph_limit(f, p, gs, start, end);
  let reph = limit > start;
  let base = base_search(gs, start, end, limit, if (reph) { start } else { end });
  // A Ra and a virama with no other consonant do not form a reph.
  return (base, reph && !(base == start && limit - base <= 2));
}

// The index after the reph at `start` and the joiners after it, or `start`
// when no reph forms there. In Malayalam the reph is a repha character.
function reph_limit(f: truetype.Font, p: Plan, gs: Glyph[], start: i32, end: i32): i32 {
  let logical = p.config.reph_mode == MODE_LOG_REPHA;
  if (logical && cat_of(gs[start]) != CAT_REPHA || !logical && (!p.rphf || start + 3 > end || !reph_formed(f, p, gs, start))) {
    return start;
  }
  let limit = start + (if (logical) { 1 } else { 2 });
  while (limit < end && is_one_of(gs[limit], joiner_bits())) {
    limit += 1;
  }
  return limit;
}

// Whether a Ra and a virama at `start` form a reph by rphf. In Telugu a ZWJ
// must follow them.
function reph_formed(f: truetype.Font, p: Plan, gs: Glyph[], start: i32): boolean {
  let two = [gs[start].id, gs[start + 1].id];
  if (p.config.reph_mode == MODE_IMPLICIT) {
    return !is_one_of(gs[start + 2], joiner_bits()) && f.would_substitute(p.gsub, "rphf", two, p.zero);
  }
  return cat_of(gs[start + 2]) == CAT_ZWJ && (f.would_substitute(p.gsub, "rphf", two, p.zero) || f.would_substitute(p.gsub, "rphf", two.append(gs[start + 2].id), p.zero));
}

// The base from the end of the syllable back to `limit`: the last consonant
// with no below-base form, and with no post-base form after a below-base
// one, or else the first consonant. It is `base` when there is none.
function base_search(gs: Glyph[], start: i32, end: i32, limit: i32, base0: i32): i32 {
  let base = base0;
  let i = end;
  let below = false;
  while (true) {
    i -= 1;
    if (is_one_of(gs[i], consonant_bits())) {
      base = i;
      if (full_form(pos_of(gs[i]), below)) {
        break;
      }
      below = below || pos_of(gs[i]) == POS_BELOW_C;
    } else if (half_requested(gs, start, i)) {
      break;
    }
    if (i <= limit) {
      break;
    }
  }
  return base;
}

// Whether a consonant at the position `pos` can be the base.
function full_form(pos: i32, below: boolean): boolean {
  return pos != POS_BELOW_C && (pos != POS_POST_C || below);
}

// Whether the glyph at `i` is a ZWJ after a halant, which asks for a half
// form and stops the search for the base.
function half_requested(gs: Glyph[], start: i32, i: i32): boolean {
  return start < i && cat_of(gs[i]) == CAT_ZWJ && cat_of(gs[i - 1]) == CAT_H;
}

// `gs` after the initial reordering of the syllable from `start` to `end`.
function reordered_syllable(f: truetype.Font, p: Plan, gs0: Glyph[], start: i32, end: i32): Glyph[] {
  let gs = kannada_swapped(p, gs0, start, end);
  let (base, reph) = base_of(f, p, gs, start, end);
  for i in start..base {
    gs = gs.with(i, with_pos(gs[i], pos_of(gs[i]).min(POS_PRE_C)));
  }
  if (base < end) {
    gs = gs.with(base, with_pos(gs[base], POS_BASE_C));
  }
  if (reph) {
    gs = gs.with(start, with_pos(gs[start], POS_RA_TO_BECOME_REPH));
  }
  if (p.old_spec) {
    gs = halant_moved(gs, base, end, p.config.script == "knda");
  }
  gs = marks_attached(gs, start, end, base);
  gs = sorted_syllable(p, gs, start, end);
  let new_base = end;
  for i in start..end {
    if (pos_of(gs[i]) == POS_BASE_C) {
      new_base = i;
      break;
    }
  }
  return with_initial_masks(f, p, gs, start, end, new_base);
}

// In Kannada a Ra, a halant and a ZWJ at the start of a syllable act as a
// Ra, a ZWJ and a halant.
function kannada_swapped(p: Plan, gs: Glyph[], start: i32, end: i32): Glyph[] {
  if (p.config.script != "knda" || start + 3 > end || !is_one_of(gs[start], cat_bit(CAT_RA)) || !is_one_of(gs[start + 1], cat_bit(CAT_H)) || !is_one_of(gs[start + 2], cat_bit(CAT_ZWJ))) {
    return gs;
  }
  let out = merged(gs, start + 1, start + 3);
  let t = out[start + 1];
  return out.with(start + 1, out[start + 2]).with(start + 2, t);
}

// `gs` with the first halant after the base moved after the last consonant,
// as the old specification asks. In Kannada a second halant stops the move.
function halant_moved(gs: Glyph[], base: i32, end: i32, kannada: boolean): Glyph[] {
  for i in base + 1..end {
    if (cat_of(gs[i]) != CAT_H) {
      continue;
    }
    let j = end - 1;
    while (j > i && !is_one_of(gs[j], consonant_bits()) && !(kannada && cat_of(gs[j]) == CAT_H)) {
      j -= 1;
    }
    return if (cat_of(gs[j]) != CAT_H && j > i) { moved(gs, i, j) } else { gs };
  }
  return gs;
}

// `gs` with each joiner, nukta, register shifter, medial consonant and
// halant at the position of the character before it, and each glyph after
// the base before a post-base consonant at the position of that consonant.
function marks_attached(gs0: Glyph[], start: i32, end: i32, base: i32): Glyph[] {
  return owned_by_consonants(misc_attached(gs0, start, end), base, end);
}

function misc_attached(gs0: Glyph[], start: i32, end: i32): Glyph[] {
  let gs = gs0;
  let misc = joiner_bits() | cat_bit(CAT_N) | cat_bit(CAT_RS) | cat_bit(CAT_CM) | cat_bit(CAT_H);
  let last_pos = 0;
  for i in start..end {
    if (in_set(gs[i], misc)) {
      // A halant does not move with a pre-base matra.
      let halant = cat_of(gs[i]) == CAT_H && last_pos == POS_PRE_M;
      gs = gs.with(i, with_pos(gs[i], if (halant) { pos_before_matras(gs, start, i) } else { last_pos }));
    } else if (pos_of(gs[i]) != POS_SMVD) {
      if (cat_of(gs[i]) == CAT_MPST && i > start && cat_of(gs[i - 1]) == CAT_SM) {
        gs = gs.with(i - 1, with_pos(gs[i - 1], pos_of(gs[i])));
      }
      last_pos = pos_of(gs[i]);
    }
  }
  return gs;
}

// The position of the last glyph before `i` that is not a pre-base matra,
// or that of a pre-base matra when there is none.
function pos_before_matras(gs: Glyph[], start: i32, i: i32): i32 {
  let j = i;
  while (j > start) {
    if (pos_of(gs[j - 1]) != POS_PRE_M) {
      return pos_of(gs[j - 1]);
    }
    j -= 1;
  }
  return POS_PRE_M;
}

// `gs` with each glyph after the base and before a post-base consonant at
// the position of that consonant, from the last consonant or matra on.
function owned_by_consonants(gs0: Glyph[], base: i32, end: i32): Glyph[] {
  let gs = gs0;
  let last = base;
  for i in base + 1..end {
    if (is_one_of(gs[i], consonant_bits())) {
      for j in last + 1..i {
        if (pos_of(gs[j]) < POS_SMVD) {
          gs = gs.with(j, with_pos(gs[j], pos_of(gs[i])));
        }
      }
      last = i;
    } else if (in_set(gs[i], cat_bit(CAT_M) | cat_bit(CAT_MPST))) {
      last = i;
    }
  }
  return gs;
}

// `gs` with the syllable from `start` to `end` in the order of the
// positions, a run of pre-base matras in the order of the text, and the
// clusters after the base joined where glyphs swapped.
function sorted_syllable(p: Plan, gs0: Glyph[], start: i32, end: i32): Glyph[] {
  let gs = by_position(gs0, start, end);
  let base = end;
  for i in start..end {
    if (pos_of(gs[i]) == POS_BASE_C) {
      base = i;
      break;
    }
  }
  gs = left_matras_flipped(gs, start, base);
  if (p.old_spec || end - start > 127) {
    return merged(gs, base, end);
  }
  return cycles_merged(gs, start, end, base);
}

// `gs` with the glyphs from `start` to `end` in a stable sort by position.
// The key of each glyph is its place before the sort.
function by_position(gs0: Glyph[], start: i32, end: i32): Glyph[] {
  let gs = gs0;
  for i in start..end {
    gs = gs.with(i, Glyph { ...gs[i], key: i - start });
  }
  for a in start + 1..end {
    let b = a;
    while (b > start && pos_of(gs[b - 1]) > pos_of(gs[b])) {
      let t = gs[b];
      gs = gs.with(b, gs[b - 1]).with(b - 1, t);
      b -= 1;
    }
  }
  return gs;
}

// `gs` with the pre-base matras before `base` in the other order, but each
// group that ends with a matra in its own order.
function left_matras_flipped(gs: Glyph[], start: i32, base: i32): Glyph[] {
  let (first, last) = (-1, -1);
  for i in start..base {
    if (pos_of(gs[i]) == POS_PRE_M) {
      first = if (first < 0) { i } else { first };
      last = i;
    }
  }
  if (first < 0 || first == last) {
    return gs;
  }
  let out = reversed(gs, first, last + 1);
  let i = first;
  for j in first..last + 1 {
    if (in_set(out[j], cat_bit(CAT_M) | cat_bit(CAT_MPST))) {
      out = reversed(out, i, j + 1);
      i = j + 1;
    }
  }
  return out;
}

// `gs` with the clusters of each cycle of the sort joined, from the base on.
function cycles_merged(gs0: Glyph[], start: i32, end: i32, base: i32): Glyph[] {
  let gs = gs0;
  let done: boolean[] = [];
  for _ in start..end {
    done = done.append(false);
  }
  for i in base..end {
    if (done[i - start]) {
      continue;
    }
    let (lo, hi) = (i, i);
    let j = start + gs[i].key;
    while (j != i) {
      lo = lo.min(j);
      hi = hi.max(j);
      done = done.with(j - start, true);
      j = start + gs[j].key;
    }
    gs = merged(gs, base.max(lo), hi + 1);
  }
  return gs;
}

// `gs` with the masks of the features that apply to each glyph of the
// syllable from `start` to `end` with the base at `base`.
function with_initial_masks(f: truetype.Font, p: Plan, gs0: Glyph[], start: i32, end: i32, base: i32): Glyph[] {
  let gs = side_masked(p, gs0, start, end, base);
  if (p.old_spec && p.config.script == "deva") {
    gs = eyelash_masked(gs, start, base);
  }
  if (p.pref && base + 2 < end) {
    gs = pref_masked(f, p, gs, base, end);
  }
  return zwnj_masked(gs, start, end);
}

// `gs` with the mask `m` added to the glyph at `k`.
function with_mask(gs: Glyph[], k: i32, m: i32): Glyph[] {
  return gs.with(k, Glyph { ...gs[k], mask: gs[k].mask | m });
}

// `gs` with rphf on the reph, the half forms before the base, and the
// below-base, above-base and post-base forms after it. With the new
// specification the below-base forms also apply before the base, but not
// in Telugu and Kannada.
function side_masked(p: Plan, gs0: Glyph[], start: i32, end: i32, base: i32): Glyph[] {
  let gs = gs0;
  let i = start;
  while (i < end && pos_of(gs[i]) == POS_RA_TO_BECOME_REPH) {
    gs = with_mask(gs, i, RPHF);
    i += 1;
  }
  let pre = if (p.old_spec || p.config.post_only) { HALF } else { HALF | BLWF };
  for k in start..base {
    gs = with_mask(gs, k, pre);
  }
  for k in base + 1..end {
    gs = with_mask(gs, k, BLWF | ABVF | PSTF);
  }
  return gs;
}

// The eyelash Ra of the old specification: a Ra and a halant before the
// base, with no ZWJ after them, take the below-base form.
function eyelash_masked(gs0: Glyph[], start: i32, base: i32): Glyph[] {
  let gs = gs0;
  for k in start..base - 1 {
    if (cat_of(gs[k]) == CAT_RA && cat_of(gs[k + 1]) == CAT_H && (k + 2 == base || cat_of(gs[k + 2]) != CAT_ZWJ)) {
      gs = with_mask(with_mask(gs, k, BLWF), k + 1, BLWF);
    }
  }
  return gs;
}

// `gs` with pref on the first two glyphs after the base that pref joins.
function pref_masked(f: truetype.Font, p: Plan, gs: Glyph[], base: i32, end: i32): Glyph[] {
  for k in base + 1..end - 1 {
    if (f.would_substitute(p.gsub, "pref", [gs[k].id, gs[k + 1].id], p.zero)) {
      return with_mask(with_mask(gs, k, PREF), k + 1, PREF);
    }
  }
  return gs;
}

// A ZWNJ stops the half forms before it, back to a consonant.
function zwnj_masked(gs0: Glyph[], start: i32, end: i32): Glyph[] {
  let gs = gs0;
  for k in start + 1..end {
    if (!is_one_of(gs[k], joiner_bits()) || cat_of(gs[k]) != CAT_ZWNJ) {
      continue;
    }
    let j = k;
    while (true) {
      j -= 1;
      gs = gs.with(j, Glyph { ...gs[j], mask: gs[j].mask & ~HALF });
      if (j <= start || is_one_of(gs[j], consonant_bits())) {
        break;
      }
    }
  }
  return gs;
}

// The glyphs `gs` after the final reordering of each syllable.
function finally_reordered(p: Plan, gs0: Glyph[]): Glyph[] {
  let gs = gs0;
  let start = 0;
  while (start < gs.len()) {
    let end = syllable_end(gs, start);
    gs = final_syllable(p, gs, start, end);
    start = end;
  }
  return gs;
}

// The base of the syllable from `start` to `end` after the basic features,
// and whether a pre-base-reordering consonant can still move. A pref
// candidate that did not ligate makes the glyph after its halants the base.
// In Malayalam a consonant after a halant that took no below-base form is
// the base.
function final_base(p: Plan, gs0: Glyph[], start: i32, end: i32): (Glyph[], i32, boolean) {
  let base = start;
  while (base < end && pos_of(gs0[base]) < POS_BASE_C) {
    base += 1;
  }
  if (base == end) {
    return (gs0, adjusted_base(gs0, start, end, base), p.pref);
  }
  let (gs, b, try_pref) = pref_rebased(gs0, base, end, p.pref);
  base = b;
  if (base < end && p.config.script == "mlym") {
    let (gs1, b1) = below_forms_skipped(gs, base, end);
    gs = gs1;
    base = b1;
  }
  if (base < end && start < base && pos_of(gs[base]) > POS_BASE_C) {
    base -= 1;
  }
  return (gs, adjusted_base(gs, start, end, base), try_pref);
}

// `gs`, the base and whether pref can still move, after a pref candidate
// that did not ligate makes the glyph after its halants the base.
function pref_rebased(gs: Glyph[], base: i32, end: i32, try_pref: boolean): (Glyph[], i32, boolean) {
  let to = pref_base(gs, base, end, try_pref);
  if (to < 0) {
    return (gs, base, try_pref);
  }
  return (if (to < end) { gs.with(to, with_pos(gs[to], POS_BASE_C)) } else { gs }, to, false);
}

// `gs` and the base after the Malayalam step: each halant after the base,
// with joiners around it, that has a below-base consonant after it makes
// that consonant the base.
function below_forms_skipped(gs0: Glyph[], base0: i32, end: i32): (Glyph[], i32) {
  let gs = gs0;
  let base = base0;
  let i = base + 1;
  while (i < end) {
    i = after_joiners(gs, i, end);
    if (i == end || !is_one_of(gs[i], cat_bit(CAT_H))) {
      break;
    }
    i = after_joiners(gs, i + 1, end);
    if (i < end && is_one_of(gs[i], consonant_bits()) && pos_of(gs[i]) == POS_BELOW_C) {
      base = i;
      gs = gs.with(base, with_pos(gs[base], POS_BASE_C));
    }
    i += 1;
  }
  return (gs, base);
}

// The first index from `i` that is not a joiner, or `end`.
function after_joiners(gs: Glyph[], i0: i32, end: i32): i32 {
  let i = i0;
  while (i < end && is_one_of(gs[i], joiner_bits())) {
    i += 1;
  }
  return i;
}

// The glyph after the halants of a pref candidate after `base` that did not
// ligate, or -1 when the candidate ligated, or there is none.
function pref_base(gs: Glyph[], base: i32, end: i32, try_pref: boolean): i32 {
  if (!try_pref || base + 1 >= end) {
    return -1;
  }
  for i in base + 1..end {
    if ((gs[i].mask & PREF) == 0) {
      continue;
    }
    if (has_prop(gs[i], truetype.SUBSTITUTED) && ligated_alone(gs[i])) {
      return -1;
    }
    let b = i;
    while (b < end && is_one_of(gs[b], cat_bit(CAT_H))) {
      b += 1;
    }
    return b;
  }
  return -1;
}

// The base `base` moved back over a ZWJ at the end of the syllable, and
// over the nuktas and halants before it.
function adjusted_base(gs: Glyph[], start: i32, end: i32, base0: i32): i32 {
  let base = base0;
  if (base == end && start < base && is_one_of(gs[base - 1], cat_bit(CAT_ZWJ))) {
    base -= 1;
  }
  while (base < end && start < base && is_one_of(gs[base], cat_bit(CAT_N) | cat_bit(CAT_H))) {
    base -= 1;
  }
  return base;
}

// `gs` after the final reordering of the syllable from `start` to `end`.
function final_syllable(p: Plan, gs0: Glyph[], start: i32, end: i32): Glyph[] {
  let (gs1, base0, try_pref) = final_base(p, viramas_restored(p, gs0, start, end), start, end);
  let gs = gs1;
  let base = base0;
  if (start + 1 < end && start < base) {
    let (gs2, b) = pre_matras_moved(p, gs, start, end, base);
    gs = gs2;
    base = b;
  }
  let (gs3, b3) = reph_moved(p, gs, start, end, base);
  gs = gs3;
  base = b3;
  if (try_pref && base + 1 < end) {
    gs = pref_moved(p, gs, start, end, base);
  }
  // A pre-base matra at the start of a word takes the init feature.
  if (pos_of(gs[start]) == POS_PRE_M && (start == 0 || !letter_like(gs[start - 1].var >> 9))) {
    gs = with_mask(gs, start, INIT);
  }
  return gs;
}

// `gs` with each virama glyph that a ligature and a multiple substitution
// gave back made a halant again.
function viramas_restored(p: Plan, gs0: Glyph[], start: i32, end: i32): Glyph[] {
  let gs = gs0;
  for i in start..end {
    if (p.virama != 0 && gs[i].id == p.virama && has_prop(gs[i], truetype.LIGATED) && has_prop(gs[i], truetype.MULTIPLIED)) {
      gs = gs.with(i, Glyph { ...gs[i], var: gs[i].var & ~31 | CAT_H, prop: gs[i].prop & ~(truetype.LIGATED | truetype.MULTIPLIED) });
    }
  }
  return gs;
}

// `gs` with the reph at `start` moved to its place, and the new base. A Ra
// and a virama move only when they ligated, and a repha only when it did
// not.
function reph_moved(p: Plan, gs: Glyph[], start: i32, end: i32, base: i32): (Glyph[], i32) {
  let reph = start + 1 < end && pos_of(gs[start]) == POS_RA_TO_BECOME_REPH;
  if (!reph || cat_of(gs[start]) == CAT_REPHA == ligated_alone(gs[start])) {
    return (gs, base);
  }
  let to = reph_target(p, gs, start, end, base);
  let b = if (start < base && base <= to) { base - 1 } else { base };
  return (moved(merged(gs, start, to + 1), start, to), b);
}

// `gs` with each pre-base matra moved after the last halant before the
// base that no ZWJ follows, and the new base.
function pre_matras_moved(p: Plan, gs0: Glyph[], start: i32, end: i32, base0: i32): (Glyph[], i32) {
  let gs = gs0;
  let base = base0;
  let new_pos = pre_matra_target(p, gs, start, end, base);
  if (start < new_pos && pos_of(gs[new_pos]) != POS_PRE_M) {
    let i = new_pos;
    while (i > start) {
      if (pos_of(gs[i - 1]) == POS_PRE_M) {
        let old = i - 1;
        base = if (old < base && base <= new_pos) { base - 1 } else { base };
        gs = merged(moved(gs, old, new_pos), new_pos, end.min(base + 1));
        new_pos -= 1;
      }
      i -= 1;
    }
    return (gs, base);
  }
  for i in start..base {
    if (pos_of(gs[i]) == POS_PRE_M) {
      return (merged(gs, i, end.min(base + 1)), base);
    }
  }
  return (gs, base);
}

// The place for the pre-base matras: the last halant before the base that
// does not belong to a matra and that no ZWJ follows, or `start`. In Tamil
// and Malayalam, the glyph before the base.
function pre_matra_target(p: Plan, gs: Glyph[], start: i32, end: i32, base: i32): i32 {
  let at = if (base == end) { base - 2 } else { base - 1 };
  if (no_half_forms(p)) {
    return at;
  }
  while (true) {
    at = last_mover(gs, start, at);
    if (!is_one_of(gs[at], cat_bit(CAT_H)) || pos_of(gs[at]) == POS_PRE_M) {
      return start;
    }
    if (!(at + 1 < end && cat_of(gs[at + 1]) == CAT_ZWJ && at > start)) {
      return at;
    }
    at -= 1;
  }
  return start;
}

// Whether the script has no half forms or explicit viramas: what half forms
// there are Chillus or ligated viramas, which a matra goes after.
function no_half_forms(p: Plan): boolean {
  return p.config.script == "taml" || p.config.script == "mlym";
}

// The last matra or halant from `at` back to `start`, or `start`.
function last_mover(gs: Glyph[], start: i32, at0: i32): i32 {
  let at = at0;
  while (at > start && !is_one_of(gs[at], cat_bit(CAT_M) | cat_bit(CAT_MPST) | cat_bit(CAT_H))) {
    at -= 1;
  }
  return at;
}

// Where the reph at `start` goes, by the reph position of the script: after
// the first halant between it and the base, or after the main consonant,
// or before the post-base forms, or else at the end of the syllable before
// the syllable modifiers.
function reph_target(p: Plan, gs: Glyph[], start: i32, end: i32, base: i32): i32 {
  let rp = p.config.reph_pos;
  let after = after_halant(gs, start, base);
  if (after >= 0) {
    return after;
  }
  if (rp == REPH_AFTER_MAIN || rp == REPH_AFTER_SUB) {
    let to = base;
    while (to + 1 < end && before_reph(pos_of(gs[to + 1]), rp)) {
      to += 1;
    }
    if (to < end) {
      return to;
    }
  }
  return reph_end(gs, start, end, base);
}

// The index after the first halant between `start` and the base, and after
// a joiner that follows it, or -1.
function after_halant(gs: Glyph[], start: i32, base: i32): i32 {
  let to = start + 1;
  while (to < base && !is_one_of(gs[to], cat_bit(CAT_H))) {
    to += 1;
  }
  if (to >= base) {
    return -1;
  }
  return if (to + 1 < base && is_one_of(gs[to + 1], joiner_bits())) { to + 1 } else { to };
}

// Whether a reph of the position `rp` goes after a glyph at `pos`.
function before_reph(pos: i32, rp: i32): boolean {
  if (rp == REPH_AFTER_MAIN) {
    return pos <= POS_AFTER_MAIN;
  }
  return pos != POS_POST_C && pos != POS_AFTER_POST && pos != POS_SMVD;
}

// The place for a reph at the end of the syllable: before the syllable
// modifiers, and before a halant after a matra.
function reph_end(gs: Glyph[], start: i32, end: i32, base: i32): i32 {
  let to = end - 1;
  while (to > start && pos_of(gs[to]) == POS_SMVD) {
    to -= 1;
  }
  if (!is_one_of(gs[to], cat_bit(CAT_H))) {
    return to;
  }
  let i = base + 1;
  while (i < to) {
    if (in_set(gs[i], cat_bit(CAT_M) | cat_bit(CAT_MPST))) {
      to -= 1;
    }
    i += 1;
  }
  return to;
}

// `gs` with the pre-base-reordering consonant that pref formed moved
// before the base, after the last halant or matra before it. In Tamil and
// Malayalam it goes just before the base.
function pref_moved(p: Plan, gs: Glyph[], start: i32, end: i32, base: i32): Glyph[] {
  for i in base + 1..end {
    if ((gs[i].mask & PREF) == 0) {
      continue;
    }
    if (!ligated_alone(gs[i])) {
      return gs;
    }
    let new_pos = if (no_half_forms(p)) { base } else { last_mover_before(gs, start, base) };
    if (new_pos > start && is_one_of(gs[new_pos - 1], cat_bit(CAT_H)) && new_pos < end && is_one_of(gs[new_pos], joiner_bits())) {
      new_pos += 1;
    }
    return moved(merged(gs, new_pos, i + 1), i, new_pos);
  }
  return gs;
}

// The index after the last matra or halant before `base`, or `start`.
function last_mover_before(gs: Glyph[], start: i32, base: i32): i32 {
  let at = base;
  while (at > start && !is_one_of(gs[at - 1], cat_bit(CAT_M) | cat_bit(CAT_MPST) | cat_bit(CAT_H))) {
    at -= 1;
  }
  return at;
}

// Whether the character `cp` is of a general category that HarfBuzz counts
// as part of a word: a letter, a mark, a format character, a private use
// character, a surrogate or an unassigned code point.
function letter_like(cp: i32): boolean {
  let c = cp as char;
  if (c.is_letter() || c.is_mark()) {
    return true;
  }
  for name in ["Cf", "Co", "Cs", "Cn"] {
    if let Some(ranges) = unicode.property_ranges(name) {
      let k = 0;
      while (k + 1 < ranges.len()) {
        if (cp >= ranges[k] && cp <= ranges[k + 1]) {
          return true;
        }
        k += 2;
      }
    }
  }
  return false;
}
```

### `positions`

After the GPOS lookups, a mark has no advance, except in the Indic shaper. In a font with no GPOS table, a mark of a left-to-right run also moves back by its advance. Thus it stays on its base. A default ignorable glyph has no advance and no offset.

Then each glyph that a lookup attached takes an offset of the glyph that it attaches to: a mark takes the x offset, and the glyph of a cursive join the y offset. The mark took the y offset when it attached. A mark also takes the advances between the two glyphs, so that its offset is from its own pen. A right-to-left run does this from its last glyph. Last, each default ignorable glyph becomes the glyph of the space.

```fern
<<positions>>=
// The most glyphs in a chain of attachments.
const MAX_CHAIN: i32 = 64;

// `r` with the advance of each mark at 0 when `zero_marks`, no advance and
// no offset for each default ignorable glyph, and each attachment added to
// the offsets of its glyph.
function finished(f: truetype.Font, r: truetype.Run, zero_marks: boolean): truetype.Run {
  let p = r.pos;
  for i in 0..r.glyphs.len() {
    if (zero_marks && f.glyph_class(r.glyphs[i]) == 3) {
      let back = if (f.gpos < 0 && !p.rtl) { p.advances[i] } else { 0 };
      p = truetype.Positions { ...p, advances: p.advances.with(i, 0), dx: p.dx.with(i, p.dx[i] - back) };
    }
    if ((r.props[i] & truetype.IGNORABLE) != 0) {
      p = truetype.Positions { ...p, advances: p.advances.with(i, 0), dx: p.dx.with(i, 0), dy: p.dy.with(i, 0) };
    }
  }
  let n = r.glyphs.len();
  let rtl = p.rtl;
  for k in 0..n {
    let i = if (rtl) { n - 1 - k } else { k };
    p = propagated(p, i, MAX_CHAIN);
  }
  return hidden(f, truetype.Run { ...r, pos: p });
}

// `r` with the glyph of the space for each default ignorable glyph, or with
// no default ignorable glyphs in a font with no space.
function hidden(f: truetype.Font, r: truetype.Run): truetype.Run {
  let space = f.glyph(0x20);
  let gs = r.glyphs;
  let any = false;
  for i in 0..gs.len() {
    if ((r.props[i] & truetype.IGNORABLE) != 0) {
      gs = gs.with(i, space);
      any = true;
    }
  }
  return if (!any || space != 0) { truetype.Run { ...r, glyphs: gs } } else { without_ignorables(r) };
}

// `r` with no default ignorable glyphs. A cluster that only such a glyph
// held joins the cluster before it, or at the start, the cluster after it.
// A right-to-left run does this in visual order, from its last glyph, as
// HarfBuzz does.
function without_ignorables(r: truetype.Run): truetype.Run {
  let n = r.glyphs.len();
  let order: i32[] = [];
  for k in 0..n {
    order = order.append(if (r.pos.rtl) { n - 1 - k } else { k });
  }
  let cs = picked(r.clusters, order);
  let keep: i32[] = [];
  let out: i32[] = [];
  for k in 0..n {
    if ((r.props[order[k]] & truetype.IGNORABLE) == 0) {
      keep = keep.append(order[k]);
      out = out.append(cs[k]);
    } else if (k + 1 < n && cs[k + 1] == cs[k]) {
      continue;
    } else if (out.len() > 0) {
      out = merged_back(out, cs[k]);
    } else {
      cs = merged_forward(cs, k);
    }
  }
  if (r.pos.rtl) {
    return kept(r, reversed_ints(keep), reversed_ints(out));
  }
  return kept(r, keep, out);
}

// `out` with the cluster `cl` in place of its last cluster, at its end, when
// `cl` comes before it.
function merged_back(out0: i32[], cl: i32): i32[] {
  let out = out0;
  let old = out[out.len() - 1];
  let j = out.len();
  while (cl < old && j > 0 && out[j - 1] == old) {
    out = out.with(j - 1, cl);
    j -= 1;
  }
  return out;
}

// `cs` with the cluster after `k` joined to the cluster at `k`.
function merged_forward(cs0: i32[], k: i32): i32[] {
  let cs = cs0;
  let old = if (k + 1 < cs.len()) { cs[k + 1] } else { cs[k] };
  let cl = cs[k].min(old);
  let j = k + 1;
  while (j < cs.len() && cs[j] == old) {
    cs = cs.with(j, cl);
    j += 1;
  }
  return cs;
}

function reversed_ints(xs: i32[]): i32[] {
  let out: i32[] = [];
  let k = xs.len();
  while (k > 0) {
    k -= 1;
    out = out.append(xs[k]);
  }
  return out;
}

// The glyphs of `r` at the indices `keep`, with the clusters `clusters`.
function kept(r: truetype.Run, keep: i32[], clusters: i32[]): truetype.Run {
  let p = r.pos;
  let pos = truetype.Positions { ...p, advances: picked(p.advances, keep), dx: picked(p.dx, keep), dy: picked(p.dy, keep), chain: picked(p.chain, keep), attach: picked(p.attach, keep) };
  return truetype.Run { ...r, glyphs: picked(r.glyphs, keep), clusters: clusters, masks: picked(r.masks, keep), ligs: picked(r.ligs, keep), props: picked(r.props, keep), vars: picked(r.vars, keep), pos: pos };
}

// The items of `xs` at the indices `keep`.
function picked(xs: i32[], keep: i32[]): i32[] {
  let out: i32[] = [];
  for i in keep {
    out = out.append(xs[i]);
  }
  return out;
}

// `p` with the glyph at `i` moved by the glyph that it attaches to, after
// that glyph moves by its own attachment: a mark by the x offset and the
// advances between them, and the glyph of a cursive join by the y offset.
function propagated(own p: truetype.Positions, i: i32, depth: i32): truetype.Positions {
  let chain = p.chain[i];
  if (chain == 0) {
    return p;
  }
  let out = truetype.Positions { ...p, chain: p.chain.with(i, 0) };
  let j = i + chain;
  if (j < 0 || j >= p.chain.len() || depth == 0) {
    return out;
  }
  out = propagated(out, j, depth - 1);
  if (out.attach[i] == 2) {
    return truetype.Positions { ...out, dy: out.dy.with(i, out.dy[i] + out.dy[j]) };
  }
  let dx = out.dx[i] + out.dx[j];
  let (from, to, sign) = if (out.rtl) { (j + 1, i + 1, 1) } else { (j, i, -1) };
  for k in from..to {
    dx += sign * out.advances[k];
  }
  return truetype.Positions { ...out, dx: out.dx.with(i, dx) };
}
```
