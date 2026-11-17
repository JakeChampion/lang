# Text shaping

This module gives the glyphs of a font for a text, and their positions. It does the OpenType substitution of the glyphs for the joining forms of Arabic, for ligatures and for marks. Then it moves the glyphs: for the kerning, the cursive joins and the marks on their bases.

## Model

`shape` takes a text in logical order and the OpenType tag of its script, such as `"arab"`. It gives a `truetype.Run`: the glyphs in logical order, with the cluster of each glyph and its position. A cluster is the index of the first character that the glyph shows. A mark has the cluster of its base.

A position is the advance of the glyph and its offset from its pen, in font units. A glyph goes at its pen and its offset, and the pen then moves by its advance. In a right-to-left run, the glyph that is last in logical order goes first, at the left.

Shaping has these steps, in the order of HarfBuzz:

1. The marks after each base go in the order of their combining class. Among the Arabic marks, the shadda goes first. In Arabic, the modifier combining marks, such as the hamza above, then go first among the marks of the same class.
2. Each character takes the glyph that the character map of the font gives.
3. In Arabic, each letter takes a form: isolated, initial, medial or final. It is the joining form of the letter between its neighbours, from its Unicode joining type. A mark is transparent: it does not stop the letters on its two sides from joining.
4. The lookups of the GSUB features apply in stages. A lookup of a form applies only to the letters of that form.
5. Each glyph takes its advance from the font. Then the lookups of the GPOS features apply in one stage: `abvm`, `blwm`, `curs`, `dist`, `kern`, `mark` and `mkmk`.
6. Each mark, of the GDEF class 3, has no advance. Each glyph that a lookup attached takes the offsets of the glyph that it attaches to.

The Arabic stages are `ccmp` and `locl`, then `isol`, `fina`, `medi`, `init`, `rlig` and `calt` one at a time, then `rclt`, `liga`, `clig` and `mset`. Other scripts have the stage `rvrn`, then one stage of `ccmp`, `locl`, `rlig`, `calt`, `clig`, `liga` and `rclt`.

## Contracts

The glyphs, the clusters and the positions are those of HarfBuzz. That is true for the GSUB lookups of the types 1 to 8 and the GPOS lookups of the types 1 to 9. The test `tests/stdlib/shape_test.fern` compares them for a set of words. An Arabic run goes from right to left, and a run of another script from left to right.

A font with no GPOS table has no mark positioning: HarfBuzz then moves the marks by their combining classes, and `shape` does not. `shape` does not compose or decompose characters, and it does not mirror the brackets of a right-to-left text. A character that the font does not have takes the glyph 0.

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

<<marks>>

<<joining>>

<<stages>>

<<positions>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module gives the glyphs of a font for a text.
import "std/truetype";
import "std/unicode";

// The bits of the masks of a run: one for the features that apply to each
// glyph, and one for each joining form.
const GLOBAL: i32 = 1;

const ISOL: i32 = 2;

const FINA: i32 = 4;

const MEDI: i32 = 8;

const INIT: i32 = 16;
```

### `marks`

HarfBuzz sorts a sequence of marks by a combining class that it changes for some scripts. For Arabic, the shadda takes the class 27 and the marks of the classes 27 to 32 move up by one. The sort is stable, and it does not change a sequence of more than 32 marks.

```fern
<<marks>>=
// The most marks in a sequence that the sort changes.
const MAX_MARKS: i32 = 32;

// The combining class of `c`, as HarfBuzz changes it for Arabic.
function sort_class(c: char): i32 {
  let k = unicode.combining_class(c);
  if (k == 33) {
    return 27;
  }
  if (k >= 27 && k <= 32) {
    return k + 1;
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

```fern
<<stages>>=
// The GPOS features, which apply in one stage.
function position_features(): string[] {
  return ["abvm", "blwm", "curs", "dist", "kern", "mark", "mkmk"];
}

// The features of each stage for the script `script`.
function stages(script: string): string[][] {
  if (script == "arab") {
    return [["ccmp", "locl"], ["isol"], ["fina"], ["medi"], ["init"], ["rlig"], ["calt"], ["rclt", "liga", "clig", "mset"]];
  }
  return [["rvrn"], ["ccmp", "locl", "rlig", "calt", "clig", "liga", "rclt"]];
}

// The mask of the feature `tag`.
function feature_mask(tag: string): i32 {
  if (tag == "isol") {
    return ISOL;
  }
  if (tag == "fina") {
    return FINA;
  }
  if (tag == "medi") {
    return MEDI;
  }
  if (tag == "init") {
    return INIT;
  }
  return GLOBAL;
}

// The glyphs of `text` in the font `f`, for the script `script`, an
// OpenType tag such as "arab" or "latn".
pub function shape(f: truetype.Font, text: string, script: string): truetype.Run {
  let arabic = script == "arab";
  let given: char[] = [];
  for c in text.chars() {
    given = given.append(c);
  }
  let order = mark_order(given, arabic);
  let cps: char[] = [];
  for k in order {
    cps = cps.append(given[k]);
  }
  let shapes = forms(cps);
  let glyphs: i32[] = [];
  let clusters: i32[] = [];
  let masks: i32[] = [];
  for i in 0..cps.len() {
    glyphs = glyphs.append(f.glyph(cps[i] as i32));
    let base = i > 0 && cps[i].is_mark();
    clusters = clusters.append(if (base) { clusters[i - 1] } else { order[i] });
    let form = if (arabic) { shapes[i] } else { 0 };
    masks = masks.append(GLOBAL | form);
  }
  let r = truetype.run_of(glyphs, clusters, masks);
  for stage in stages(script) {
    r = applied_stage(f, r, script, stage, false);
  }
  r = f.with_advances(r, arabic);
  r = applied_stage(f, r, script, position_features(), true);
  return finished(f, r);
}

// `r` after the lookups of the features `tags`, in the order of their
// indices: those of GPOS with `gpos`, else those of GSUB. A lookup of two
// features applies with the masks of both.
function applied_stage(f: truetype.Font, r: truetype.Run, script: string, tags: string[], gpos: boolean): truetype.Run {
  let lookups: i32[] = [];
  let masks: i32[] = [];
  for tag in tags {
    for li in if (gpos) { f.position_lookups(script, tag) } else { f.feature_lookups(script, tag) } {
      let at = 0;
      while (at < lookups.len() && lookups[at] < li) {
        at += 1;
      }
      if (at < lookups.len() && lookups[at] == li) {
        masks = masks.with(at, masks[at] | feature_mask(tag));
        continue;
      }
      lookups = inserted_at(lookups, at, li);
      masks = inserted_at(masks, at, feature_mask(tag));
    }
  }
  let run = r;
  for k in 0..lookups.len() {
    run = if (gpos) { f.position(run, lookups[k], masks[k]) } else { f.substitute(run, lookups[k], masks[k]) };
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

### `positions`

After the GPOS lookups, a mark has no advance. In a font with no GPOS table, a mark of a left-to-right run also moves back by its advance. Thus it stays on its base. Then each glyph that a lookup attached takes the offsets of the glyph that it attaches to. A mark also takes the advances between the two glyphs, so that its offset is from its own pen.

```fern
<<positions>>=
// The most glyphs in a chain of attachments.
const MAX_CHAIN: i32 = 64;

// `r` with the advance of each mark at 0, and each attachment added to the
// offsets of its glyph.
function finished(f: truetype.Font, r: truetype.Run): truetype.Run {
  let p = r.pos;
  for i in 0..r.glyphs.len() {
    if (f.glyph_class(r.glyphs[i]) == 3) {
      let back = if (f.gpos < 0 && !p.rtl) { p.advances[i] } else { 0 };
      p = truetype.Positions { ...p, advances: p.advances.with(i, 0), dx: p.dx.with(i, p.dx[i] - back) };
    }
  }
  for i in 0..r.glyphs.len() {
    p = propagated(p, i, MAX_CHAIN);
  }
  return truetype.Run { ...r, pos: p };
}

// `p` with the glyph at `i` moved by the glyph that it attaches to, after
// that glyph moves by its own attachment.
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
  return truetype.Positions { ...out, dx: out.dx.with(i, dx), dy: out.dy.with(i, out.dy[i] + out.dy[j]) };
}
```
