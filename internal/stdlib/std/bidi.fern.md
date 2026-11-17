# The Unicode Bidirectional Algorithm

This module gives the order in which to show a text that mixes left-to-right and right-to-left scripts, such as English with Hebrew or Arabic. It implements the Unicode Bidirectional Algorithm, UAX #9.

## Model

Each character has a bidi class, which `std/unicode` gives. `L` is a left-to-right letter, and `R` and `AL` are right-to-left letters. `EN` and `AN` are numbers, `WS` and `ON` are neutral, and other classes are the explicit formatting characters. `levels` gives the embedding level of each character of a paragraph. An even level is left to right, and an odd level is right to left.

The paragraph has a direction: `LTR`, `RTL`, or `AUTO`. With `AUTO`, the first strong letter of the paragraph sets it, and a paragraph with no strong letter is left to right. `paragraph_level` gives the level that `AUTO` finds.

`visual_order` gives the indices of the characters of one line in the order to show them, from left to right. Before it reorders, it applies the rule L1 of UAX #9: the white space at the end of the line and before each separator gets the paragraph level. A character that the algorithm removes, such as an embedding mark, has the level `REMOVED` and is not in the order.

`mirrored` gives the glyph to show for a character at a right-to-left level, such as `)` for `(`.

`resolve` gives the levels of a text once. Then `line` gives each line of the text in the order to show it, with the mirrored glyphs. `order` gives the indices of the characters of the line in that order. A line is a part of one paragraph. `display` gives a text of one line in the order to show it.

## Contracts

The functions work on code points, one level for each code point. A text with more than one paragraph has a paragraph separator, class `B`, between the paragraphs. `levels` resolves each paragraph with its own direction, and the separator has the level of its paragraph.

The explicit embeddings nest to the depth of 125, as UAX #9 sets. The pairs of brackets are found to the depth of 63.

## Executable example

```fern test name=std-bidi-example
import "std/bidi";

function main(): i32 {
  // "abc" then the Hebrew letters alef, bet and gimel.
  let text = "abc " + "\u{5D0}\u{5D1}\u{5D2}";
  let ls = bidi.levels(text, bidi.LTR);
  if (ls != [0, 0, 0, 0, 1, 1, 1]) { return 1; }
  if (bidi.visual_order(text, ls, bidi.LTR) != [0, 1, 2, 3, 6, 5, 4]) { return 2; }
  if (bidi.paragraph_level("\u{5D0}b", bidi.AUTO) != 1) { return 3; }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<classes>>

<<paragraphs>>

<<explicit levels>>

<<run sequences>>

<<weak types>>

<<neutral types>>

<<lines>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module orders a text of mixed directions for display (UAX #9).
import "std/unicode";
```

### `classes`

The constants are the bidi classes in the order of `unicode.bidi_class`.

```fern
<<classes>>=
pub const L: i32 = 0;

pub const R: i32 = 1;

pub const AL: i32 = 2;

pub const EN: i32 = 3;

pub const ES: i32 = 4;

pub const ET: i32 = 5;

pub const AN: i32 = 6;

pub const CS: i32 = 7;

pub const NSM: i32 = 8;

pub const BN: i32 = 9;

pub const B: i32 = 10;

pub const S: i32 = 11;

pub const WS: i32 = 12;

pub const ON: i32 = 13;

pub const LRE: i32 = 14;

pub const LRO: i32 = 15;

pub const RLE: i32 = 16;

pub const RLO: i32 = 17;

pub const PDF: i32 = 18;

pub const LRI: i32 = 19;

pub const RLI: i32 = 20;

pub const FSI: i32 = 21;

pub const PDI: i32 = 22;

// The direction of a paragraph.
pub const LTR: i32 = 0;

pub const RTL: i32 = 1;

pub const AUTO: i32 = 2;

// The level of a character that the rule X9 removes.
pub const REMOVED: i32 = -1;

const MAX_DEPTH: i32 = 125;

const MAX_BRACKETS: i32 = 63;

// The code points of a text.
pub function code_points(text: string): i32[] {
  let out: i32[] = [];
  for c in text.chars() {
    out = out.append(c as i32);
  }
  return out;
}

pub function classes_of(cps: i32[]): i32[] {
  let out: i32[] = [];
  for cp in cps {
    out = out.append(unicode.bidi_class(cp as char));
  }
  return out;
}

function is_isolate_start(t: i32): boolean {
  return t == LRI || t == RLI || t == FSI;
}

function is_embedding(t: i32): boolean {
  return t == LRE || t == RLE || t == LRO || t == RLO;
}

function is_removed(t: i32): boolean {
  return is_embedding(t) || t == PDF || t == BN;
}

// The neutral and isolate classes of the rules N1 and N2.
function is_ni(t: i32): boolean {
  return t == B || t == S || t == WS || t == ON || t == LRI || t == RLI || t == FSI || t == PDI;
}

// The direction of a strong class for the rules N0 to N2: EN and AN count
// as R. It is -1 for each other class.
function strong_dir(t: i32): i32 {
  if (t == L) {
    return L;
  }
  if (t == R || t == AL || t == EN || t == AN) {
    return R;
  }
  return -1;
}
```

### `paragraphs`

The rules P2 and P3: the first strong letter sets the level of a paragraph. The letters inside an isolate do not count. The same search gives the direction of an FSI, from the character after it to its matching PDI.

```fern
<<paragraphs>>=
// The direction of the first strong letter in `cls` from `start` to `end`,
// outside each isolate: L, R, or -1 when there is none. For an FSI, `end`
// is its matching PDI.
function first_strong(cls: i32[], start: i32, end: i32): i32 {
  let depth = 0;
  let i = start;
  while (i < end) {
    let t = cls[i];
    if (is_isolate_start(t)) {
      depth += 1;
    } else if (t == PDI) {
      // A PDI that matches no isolate does not count.
      if (depth > 0) {
        depth -= 1;
      }
    } else if (depth == 0) {
      if (t == L) {
        return L;
      }
      if (t == R || t == AL) {
        return R;
      }
    }
    i += 1;
  }
  return -1;
}

function level_of_direction(cls: i32[], start: i32, end: i32, direction: i32): i32 {
  if (direction == LTR || direction == RTL) {
    return direction;
  }
  if (first_strong(cls, start, end) == R) {
    return 1;
  }
  return 0;
}

// The level of the first paragraph of `text` in the direction `direction`.
pub function paragraph_level(text: string, direction: i32): i32 {
  return paragraph_level_of(classes_of(code_points(text)), direction);
}

// The level of the first paragraph with the classes `cls`.
pub function paragraph_level_of(cls: i32[], direction: i32): i32 {
  return level_of_direction(cls, 0, paragraph_end(cls, 0), direction);
}

// The end of the paragraph that starts at `start`: after its separator.
function paragraph_end(cls: i32[], start: i32): i32 {
  let i = start;
  while (i < cls.len()) {
    if (cls[i] == B) {
      return i + 1;
    }
    i += 1;
  }
  return cls.len();
}

// The levels of each code point of `text`.
pub function levels(text: string, direction: i32): i32[] {
  let cps = code_points(text);
  return levels_of(cps, classes_of(cps), direction);
}

// The levels of the code points `cps` with the classes `cls`, each
// paragraph in the direction `direction`.
pub function levels_of(cps: i32[], cls: i32[], direction: i32): i32[] {
  let out: i32[] = [];
  let start = 0;
  while (start < cls.len()) {
    let end = paragraph_end(cls, start);
    let para = level_of_direction(cls, start, end, direction);
    for level in paragraph_levels(cps, cls, start, end, para) {
      out = out.append(level);
    }
    start = end;
  }
  return out;
}

// The levels of one paragraph, from `start` to `end`, at the level `para`.
function paragraph_levels(cps: i32[], cls0: i32[], start: i32, end: i32, para: i32): i32[] {
  let pcps: i32[] = [];
  let cls: i32[] = [];
  for i in start..end {
    pcps = pcps.append(cps[i]);
    cls = cls.append(cls0[i]);
  }
  let matches = matching_pdis(cls);
  let ex = explicit_levels(cls, matches, para);
  let types = ex.types;
  let lv = ex.levels;
  for seq in run_sequences(cls, lv, matches, para) {
    let w = weak_types(seq, types);
    types = neutral_types(pcps, seq, w, cls, types);
    lv = implicit_levels(seq, types, lv);
  }
  for i in 0..cls.len() {
    if (is_removed(cls[i])) {
      lv = lv.with(i, REMOVED);
    } else if (cls[i] == B) {
      lv = lv.with(i, para);
    }
  }
  return lv;
}

// The index of the PDI that matches each isolate initiator, or -1, by the
// rule BD9.
function matching_pdis(cls: i32[]): i32[] {
  let out: i32[] = [];
  let open: i32[] = [];
  for i in 0..cls.len() {
    out = out.append(-1);
    if (is_isolate_start(cls[i])) {
      open = open.append(i);
    } else if (cls[i] == PDI && open.len() > 0) {
      out = out.with(open[open.len() - 1], i);
      open = open.take(open.len() - 1);
    }
  }
  return out;
}
```

### `explicit levels`

The rules X1 to X8 keep a stack of the embeddings, the overrides and the isolates. An override sets the class of each character in it to `L` or `R`. An embedding that goes past the depth of 125 counts as an overflow, and its PDF ends the overflow.

```fern
<<explicit levels>>=
struct Explicit { levels: i32[], types: i32[] }

// One entry of the directional status stack: the level, the override
// class or -1, and whether an isolate started it.
struct Status { level: i32, over: i32, isolate: boolean }

function next_odd(level: i32): i32 {
  return level + 1 | 1;
}

function next_even(level: i32): i32 {
  return level + 2 & -2;
}

// The state of the rules X1 to X8: the directional status stack and the
// counts of the overflow isolates, the overflow embeddings and the valid
// isolates.
struct Embedding { stack: Status[], overflow_isolates: i32, overflow_embeds: i32, valid_isolates: i32 }

function explicit_levels(cls: i32[], matches: i32[], para: i32): Explicit {
  let s = Embedding { stack: [Status { level: para, over: -1, isolate: false }], overflow_isolates: 0, overflow_embeds: 0, valid_isolates: 0 };
  let levels: i32[] = [];
  let types: i32[] = [];
  for i in 0..cls.len() {
    let t = cls[i];
    let top = top_of(s);
    let level = top.level;
    let ty = t;
    if (is_embedding(t)) {
      s = push_embedding(s, t);
    } else if (is_isolate_start(t)) {
      ty = overridden(top, t);
      s = push_isolate(s, isolate_rtl(cls, matches, i));
    } else if (t == PDI) {
      s = pop_isolate(s);
      level = top_of(s).level;
      ty = overridden(top_of(s), t);
    } else if (t == PDF) {
      s = pop_embedding(s);
    } else if (t == B) {
      // X8: a paragraph separator ends each embedding.
      level = para;
    } else if (t != BN) {
      ty = overridden(top, t);
    }
    levels = levels.append(level);
    types = types.append(ty);
  }
  return Explicit { levels: levels, types: types };
}

function top_of(s: Embedding): Status {
  return s.stack[s.stack.len() - 1];
}

function overridden(st: Status, t: i32): i32 {
  return if (st.over >= 0) { st.over } else { t };
}

// True when a new entry at `level` fits on the stack.
function fits(s: Embedding, level: i32): boolean {
  return level <= MAX_DEPTH && s.overflow_isolates == 0 && s.overflow_embeds == 0;
}

// X2 to X5: an embedding or an override.
function push_embedding(s: Embedding, t: i32): Embedding {
  let top = top_of(s);
  let next = if (t == RLE || t == RLO) { next_odd(top.level) } else { next_even(top.level) };
  if (fits(s, next)) {
    let over = if (t == RLO) { R } else if (t == LRO) { L } else { -1 };
    return Embedding { ...s, stack: s.stack.append(Status { level: next, over: over, isolate: false }) };
  }
  if (s.overflow_isolates == 0) {
    return Embedding { ...s, overflow_embeds: s.overflow_embeds + 1 };
  }
  return s;
}

// True when the isolate initiator at `i` is right to left. An FSI takes
// the direction of the first strong letter up to its matching PDI.
function isolate_rtl(cls: i32[], matches: i32[], i: i32): boolean {
  if (cls[i] != FSI) {
    return cls[i] == RLI;
  }
  let end = if (matches[i] >= 0) { matches[i] } else { cls.len() };
  return first_strong(cls, i + 1, end) == R;
}

// X5a to X5c: an isolate.
function push_isolate(s: Embedding, rtl: boolean): Embedding {
  let top = top_of(s);
  let next = if (rtl) { next_odd(top.level) } else { next_even(top.level) };
  if (fits(s, next)) {
    return Embedding { ...s, stack: s.stack.append(Status { level: next, over: -1, isolate: true }), valid_isolates: s.valid_isolates + 1 };
  }
  return Embedding { ...s, overflow_isolates: s.overflow_isolates + 1 };
}

// X6a: a PDI ends its isolate and each embedding in it.
function pop_isolate(s: Embedding): Embedding {
  if (s.overflow_isolates > 0) {
    return Embedding { ...s, overflow_isolates: s.overflow_isolates - 1 };
  }
  if (s.valid_isolates == 0) {
    return s;
  }
  let stack = s.stack;
  while (!stack[stack.len() - 1].isolate) {
    stack = stack.take(stack.len() - 1);
  }
  return Embedding { ...s, stack: stack.take(stack.len() - 1), overflow_embeds: 0, valid_isolates: s.valid_isolates - 1 };
}

// X7: a PDF ends the embedding or the override that is on top.
function pop_embedding(s: Embedding): Embedding {
  if (s.overflow_isolates > 0) {
    return s;
  }
  if (s.overflow_embeds > 0) {
    return Embedding { ...s, overflow_embeds: s.overflow_embeds - 1 };
  }
  if (top_of(s).isolate || s.stack.len() < 2) {
    return s;
  }
  return Embedding { ...s, stack: s.stack.take(s.stack.len() - 1) };
}
```

### `run sequences`

The rule X10: a level run is a longest run of characters at one level, with no character that X9 removes. An isolating run sequence joins the level run that ends with an isolate initiator to the level run that starts with its matching PDI. `sos` and `eos` are the classes at the start and at the end of a sequence: the direction of the higher of its level and the level beside it.

```fern
<<run sequences>>=
// An isolating run sequence: the indices of its characters, its level,
// and the classes `sos` and `eos` at its ends.
struct Sequence { at: i32[], level: i32, sos: i32, eos: i32 }

// `cls` are the original classes: an override does not change where an
// isolate starts or ends.
function run_sequences(cls: i32[], lv: i32[], matches: i32[], para: i32): Sequence[] {
  let runs = level_runs(cls, lv);
  // The run that starts with each index.
  let run_at = filled(cls.len(), -1);
  for r in 0..runs.len() {
    run_at = run_at.with(runs[r][0], r);
  }
  let matched = filled(cls.len(), 0);
  for m in matches {
    if (m >= 0) {
      matched = matched.with(m, 1);
    }
  }
  let out: Sequence[] = [];
  for r in 0..runs.len() {
    let first = runs[r][0];
    if (cls[first] != PDI || matched[first] == 0) {
      out = out.append(sequence_of(joined_runs(runs, run_at, r, cls, matches), cls, lv, matches, para));
    }
  }
  return out;
}

function filled(n: i32, v: i32): i32[] {
  let out: i32[] = [];
  for i in 0..n {
    out = out.append(v);
  }
  return out;
}

// The level runs: the indices of each longest run of characters at one
// level, with no character that X9 removes.
function level_runs(cls: i32[], lv: i32[]): i32[][] {
  let runs: i32[][] = [];
  let run: i32[] = [];
  for i in 0..cls.len() {
    if (is_removed(cls[i])) {
      continue;
    }
    if (run.len() > 0 && lv[i] != lv[run[0]]) {
      runs = runs.append(run);
      run = [];
    }
    run = run.append(i);
  }
  if (run.len() > 0) {
    runs = runs.append(run);
  }
  return runs;
}

// The indices of the run `r`, and of each run that the isolate at the end
// of the run before it matches.
function joined_runs(runs: i32[][], run_at: i32[], r: i32, cls: i32[], matches: i32[]): i32[] {
  let at: i32[] = [];
  let cur = r;
  while (cur >= 0) {
    for i in runs[cur] {
      at = at.append(i);
    }
    let last = at[at.len() - 1];
    cur = if (is_isolate_start(cls[last]) && matches[last] >= 0) { run_at[matches[last]] } else { -1 };
  }
  return at;
}

function dir_of_level(level: i32): i32 {
  return if (level % 2 == 1) { R } else { L };
}

function sequence_of(at: i32[], types: i32[], lv: i32[], matches: i32[], para: i32): Sequence {
  let first = at[0];
  let last = at[at.len() - 1];
  let level = lv[first];
  let before = para;
  let j = first - 1;
  while (j >= 0 && is_removed(types[j])) {
    j -= 1;
  }
  if (j >= 0) {
    before = lv[j];
  }
  let after = para;
  if (!is_isolate_start(types[last])) {
    let k = last + 1;
    while (k < types.len() && is_removed(types[k])) {
      k += 1;
    }
    if (k < types.len()) {
      after = lv[k];
    }
  }
  return Sequence { at: at, level: level, sos: dir_of_level(level.max(before)), eos: dir_of_level(lv[last].max(after)) };
}
```

### `weak types`

The rules W1 to W7 resolve the numbers, the separators and the marks of one isolating run sequence. `t` holds the class of each character of the sequence, in its order.

```fern
<<weak types>>=
function weak_types(seq: Sequence, types: i32[]): i32[] {
  let t: i32[] = [];
  for i in seq.at {
    t = t.append(types[i]);
  }
  return w7_european_after_l(w6_neutral(w5_terminators(w4_separators(w3_arabic_letters(w2_arabic_numbers(w1_marks(t, seq.sos), seq.sos))))), seq.sos);
}

// W1: a mark takes the class before it.
function w1_marks(t0: i32[], sos: i32): i32[] {
  let t = t0;
  for k in 0..t.len() {
    if (t[k] == NSM) {
      let prev = if (k == 0) { sos } else { t[k - 1] };
      t = t.with(k, if (is_isolate_start(prev) || prev == PDI) { ON } else { prev });
    }
  }
  return t;
}

// W2: a European number after an Arabic letter is an Arabic number.
function w2_arabic_numbers(t0: i32[], sos: i32): i32[] {
  let t = t0;
  let strong = sos;
  for k in 0..t.len() {
    let c = t[k];
    if (c == L || c == R || c == AL) {
      strong = c;
    }
    if (c == EN && strong == AL) {
      t = t.with(k, AN);
    }
  }
  return t;
}

// W3: an Arabic letter is R.
function w3_arabic_letters(t0: i32[]): i32[] {
  let t = t0;
  for k in 0..t.len() {
    if (t[k] == AL) {
      t = t.with(k, R);
    }
  }
  return t;
}

// W4: one separator between two numbers of one kind.
function w4_separators(t0: i32[]): i32[] {
  let t = t0;
  for k in 1..(t.len() - 1).max(1) {
    t = t.with(k, separated(t[k], t[k - 1], t[k + 1]));
  }
  return t;
}

// The class of the separator `c` between the classes `a` and `b`.
function separated(c: i32, a: i32, b: i32): i32 {
  if (c == ES && a == EN && b == EN) {
    return EN;
  }
  if (c == CS && a == b && (a == EN || a == AN)) {
    return a;
  }
  return c;
}

// W5: terminators beside a European number.
function w5_terminators(t0: i32[]): i32[] {
  let t = t0;
  let n = t.len();
  let k = 0;
  while (k < n) {
    let end = k;
    while (end < n && t[end] == ET) {
      end += 1;
    }
    if (end > k && (k > 0 && t[k - 1] == EN || end < n && t[end] == EN)) {
      for m in k..end {
        t = t.with(m, EN);
      }
    }
    k = end.max(k + 1);
  }
  return t;
}

// W6: the separators and terminators that are left are neutral.
function w6_neutral(t0: i32[]): i32[] {
  let t = t0;
  for m in 0..t.len() {
    let c = t[m];
    if (c == ES || c == ET || c == CS) {
      t = t.with(m, ON);
    }
  }
  return t;
}

// W7: a European number after a left-to-right letter is L.
function w7_european_after_l(t0: i32[], sos: i32): i32[] {
  let t = t0;
  let strong = sos;
  for m in 0..t.len() {
    let c = t[m];
    if (c == L || c == R) {
      strong = c;
    }
    if (c == EN && strong == L) {
      t = t.with(m, L);
    }
  }
  return t;
}
```

### `neutral types`

The rule N0 resolves the pairs of brackets: a pair takes the embedding direction when a strong class of that direction is inside it. With only the other direction inside, it takes that direction when the context before it has it too. The rules N1 and N2 resolve the neutral characters that are left by the strong classes beside them, or by the embedding direction. Then I1 and I2 give the levels.

```fern
<<neutral types>>=
// The classes `types` of all the characters, with the classes `t0` of the
// sequence `seq` resolved.
function neutral_types(cps: i32[], seq: Sequence, t0: i32[], cls: i32[], types: i32[]): i32[] {
  let t = brackets(cps, seq, t0, cls);
  let n = t.len();
  let e = dir_of_level(seq.level);
  let k = 0;
  while (k < n) {
    let end = k;
    while (end < n && is_ni(t[end])) {
      end += 1;
    }
    if (end > k) {
      t = neutral_run(t, seq, k, end, e);
    }
    k = end.max(k + 1);
  }
  let out = types;
  for m in 0..n {
    out = out.with(seq.at[m], t[m]);
  }
  return out;
}

// N1 and N2: the neutral characters from `k` up to `end` take the
// direction of the strong classes on both sides when it is the same, or
// else the embedding direction `e`.
function neutral_run(t0: i32[], seq: Sequence, k: i32, end: i32, e: i32): i32[] {
  let t = t0;
  let before = if (k == 0) { seq.sos } else { strong_dir(t[k - 1]) };
  let after = if (end == t.len()) { seq.eos } else { strong_dir(t[end]) };
  let dir = if (before == after && before >= 0) { before } else { e };
  for m in k..end {
    t = t.with(m, dir);
  }
  return t;
}

// U+2329 and U+232A are the same brackets as U+3008 and U+3009.
function canonical_bracket(cp: i32): i32 {
  if (cp == 9001) {
    return 12296;
  }
  if (cp == 9002) {
    return 12297;
  }
  return cp;
}

// The pairs of brackets of the sequence, as the positions of the opening
// and of the closing bracket, sorted by the opening one.
function bracket_pairs(cps: i32[], seq: Sequence, t: i32[]): i32[][] {
  let open_cp: i32[] = [];
  let open_at: i32[] = [];
  let pairs: i32[][] = [];
  for m in 0..t.len() {
    if (t[m] != ON) {
      continue;
    }
    let cp = cps[seq.at[m]];
    let kind = unicode.bidi_bracket_type(cp as char);
    if (kind == 1) {
      if (open_cp.len() == MAX_BRACKETS) {
        break;
      }
      open_cp = open_cp.append(canonical_bracket(unicode.bidi_paired_bracket(cp as char)));
      open_at = open_at.append(m);
    } else if (kind == 2) {
      let s = open_cp.len() - 1;
      while (s >= 0 && open_cp[s] != canonical_bracket(cp)) {
        s -= 1;
      }
      if (s >= 0) {
        pairs = pairs.append([open_at[s], m]);
        let kept_cp: i32[] = [];
        let kept_at: i32[] = [];
        for q in 0..s {
          kept_cp = kept_cp.append(open_cp[q]);
          kept_at = kept_at.append(open_at[q]);
        }
        open_cp = kept_cp;
        open_at = kept_at;
      }
    }
  }
  return sort_pairs(pairs);
}

function sort_pairs(pairs: i32[][]): i32[][] {
  let out: i32[][] = [];
  for p in pairs {
    let k = out.len();
    while (k > 0 && out[k - 1][0] > p[0]) {
      k -= 1;
    }
    let next: i32[][] = [];
    for q in 0..k {
      next = next.append(out[q]);
    }
    next = next.append(p);
    for q in k..out.len() {
      next = next.append(out[q]);
    }
    out = next;
  }
  return out;
}

function brackets(cps: i32[], seq: Sequence, t0: i32[], cls: i32[]): i32[] {
  let t = t0;
  let e = dir_of_level(seq.level);
  for p in bracket_pairs(cps, seq, t0) {
    let dir = pair_direction(t, seq, p[0], p[1], e);
    if (dir >= 0) {
      t = set_bracket(set_bracket(t, seq, cls, p[0], dir), seq, cls, p[1], dir);
    }
  }
  return t;
}

// N0: the direction of the pair of brackets at `open` and `close`, or -1
// when no strong class is inside it.
function pair_direction(t: i32[], seq: Sequence, open: i32, close: i32, e: i32): i32 {
  let found_other = false;
  for m in open + 1..close {
    let d = strong_dir(t[m]);
    if (d == e) {
      return e;
    }
    found_other = found_other || d >= 0;
  }
  if (!found_other) {
    return -1;
  }
  // The pair takes the direction of the context before it.
  let m = open - 1;
  while (m >= 0) {
    let d = strong_dir(t[m]);
    if (d >= 0) {
      return d;
    }
    m -= 1;
  }
  return seq.sos;
}

// The class `dir` for the bracket at `m`, and for each mark after it.
function set_bracket(t0: i32[], seq: Sequence, cls: i32[], m: i32, dir: i32): i32[] {
  let t = t0.with(m, dir);
  let k = m + 1;
  while (k < t.len() && cls[seq.at[k]] == NSM) {
    t = t.with(k, dir);
    k += 1;
  }
  return t;
}

function implicit_levels(seq: Sequence, types: i32[], lv0: i32[]): i32[] {
  let lv = lv0;
  for i in seq.at {
    let t = types[i];
    let level = lv[i];
    if (level % 2 == 0) {
      if (t == R) {
        level += 1;
      } else if (t == AN || t == EN) {
        level += 2;
      }
    } else if (t == L || t == EN || t == AN) {
      level += 1;
    }
    lv = lv.with(i, level);
  }
  return lv;
}
```

### `lines`

The rule L1 gives the paragraph level to each segment separator and each paragraph separator. It does the same for each run of white space and isolate marks before a separator and at the end of the line. The rule L2 reverses each run at a level or higher, from the highest level down to the lowest odd level.

```fern
<<lines>>=
// The indices of the characters of `text`, which is one line, in the order
// to show them. `ls` are the levels that `levels` gives for the text.
pub function visual_order(text: string, ls: i32[], direction: i32): i32[] {
  let cls = classes_of(code_points(text));
  return visual_order_of(cls, ls, paragraph_level_of(cls, direction));
}

// The order of a line with the original classes `cls`, the levels `ls`
// and the paragraph level `para`.
pub function visual_order_of(cls: i32[], ls: i32[], para: i32): i32[] {
  let lv = line_levels(cls, ls, para);
  let order: i32[] = [];
  let levels: i32[] = [];
  let high = 0;
  let low_odd = MAX_DEPTH + 2;
  for i in 0..lv.len() {
    if (lv[i] != REMOVED) {
      order = order.append(i);
      levels = levels.append(lv[i]);
      high = high.max(lv[i]);
      if (lv[i] % 2 == 1) {
        low_odd = low_odd.min(lv[i]);
      }
    }
  }
  let level = high;
  while (level >= low_odd) {
    let k = 0;
    while (k < order.len()) {
      if (levels[k] >= level) {
        let end = k;
        while (end < order.len() && levels[end] >= level) {
          end += 1;
        }
        let a = k;
        let b = end - 1;
        while (a < b) {
          let oa = order[a];
          let la = levels[a];
          order = order.with(a, order[b]).with(b, oa);
          levels = levels.with(a, levels[b]).with(b, la);
          a += 1;
          b -= 1;
        }
        k = end;
      } else {
        k += 1;
      }
    }
    level -= 1;
  }
  return order;
}

// The levels after the rule L1.
pub function line_levels(cls: i32[], ls: i32[], para: i32): i32[] {
  let lv = ls;
  let trailing = true;
  let i = cls.len() - 1;
  while (i >= 0) {
    let t = cls[i];
    if (t == S || t == B) {
      lv = lv.with(i, para);
      trailing = true;
    } else if (trailing && (t == WS || is_isolate_start(t) || t == PDI || is_removed(t))) {
      if (lv[i] != REMOVED) {
        lv = lv.with(i, para);
      }
    } else {
      trailing = false;
    }
    i -= 1;
  }
  return lv;
}

// The glyph to show for `c` at a right-to-left level: its mirror, or `c`.
pub function mirrored(c: char): char {
  let m = unicode.bidi_mirror(c);
  if (m < 0) {
    return c;
  }
  return m as char;
}

// A text with the level of each of its code points, to show one line at a
// time.
pub struct Resolved { cps: i32[], cls: i32[], levels: i32[], direction: i32 }

// The levels of `text`, each paragraph in the direction `direction`.
pub function resolve(text: string, direction: i32): Resolved {
  let cps = code_points(text);
  let cls = classes_of(cps);
  if (direction == RTL || has_rtl(cls)) {
    return Resolved { cps: cps, cls: cls, levels: levels_of(cps, cls, direction), direction: direction };
  }
  let ls: i32[] = [];
  for t in cls {
    ls = ls.append(if (is_removed(t)) { REMOVED } else { 0 });
  }
  return Resolved { cps: cps, cls: cls, levels: ls, direction: direction };
}

// True when a class in `cls` can give a level other than 0 to a paragraph
// that is left to right. With none, each level is 0 or REMOVED.
function has_rtl(cls: i32[]): boolean {
  for t in cls {
    if (t == R || t == AL || t == AN || t >= LRE) {
      return true;
    }
  }
  return false;
}

// The level of the paragraph that has the code point `i`.
pub function (r: Resolved) paragraph_level_at(i: i32): i32 {
  let start = i.min(r.cls.len());
  while (start > 0 && r.cls[start - 1] != B) {
    start -= 1;
  }
  return level_of_direction(r.cls, start, paragraph_end(r.cls, start), r.direction);
}

// The indices of the code points from `a` up to `b`, one line of one
// paragraph, in the order to show them.
pub function (r: Resolved) order(a: i32, b: i32): i32[] {
  let out: i32[] = [];
  for i in visual_order_of(r.cls.take(b).drop(a), r.levels.take(b).drop(a), r.paragraph_level_at(a)) {
    out = out.append(a + i);
  }
  return out;
}

// The line from `a` up to `b` in the order to show it, with the mirrored
// glyphs.
pub function (r: Resolved) line(a: i32, b: i32): string {
  let out = "";
  for i in r.order(a, b) {
    let c = r.cps[i] as char;
    if (r.levels[i] % 2 == 1) {
      c = mirrored(c);
    }
    out += c.to_string();
  }
  return out;
}

// The line `text` in the order to show it, with the mirrored glyphs.
pub function display(text: string, direction: i32): string {
  let r = resolve(text, direction);
  return r.line(0, r.cps.len());
}
```
