# Shell-style pattern matches

This module compares strings with shell glob patterns.

## Model

The matcher interprets shell glob syntax against a supplied string. Pattern evaluation does not access the filesystem.

`is_match` reads a pattern each time it runs. In it, `*`, `?` and a class do not match `/`. `**` matches any text, and a backslash is a literal.

`compile` reads a pattern once, with the syntax of the globset crate of ripgrep. ripgrep reads ignore files, `-g` globs and file types with this syntax. The `GlobOptions` select the separator rule, the escapes and the case:

- `?` matches one byte, and `*` matches zero or more bytes. With `literal_separator`, neither matches `/`.
- `**` is recursive only as a whole path component. `**/` at the start matches zero or more directories. `/**` at the end matches what a directory holds. `/**/` matches one `/` or more directories. Elsewhere `**` is two `*`.
- `[a-z]`, `[!a-z]` and `[^a-z]` are classes. A `]` or a `-` first in a class is a literal, and so is a `-` last.
- `{a,b}` matches `a` or `b`. Groups do not nest, and an empty choice does not match.
- With `backslash_escape`, `\` makes the next character a literal.
- With `case_insensitive`, ASCII letters match in either case.

A compiled glob tests a literal pattern, a base name, or the suffix of a base name with one comparison. It tests other patterns with an automaton of the pattern. Its time is linear in the length of the text.

## Contracts

A match tests the pattern against the complete input. Callers that require filesystem expansion must enumerate paths separately.

`compile` returns a `GlobError` for a class or a group that is not closed, for a `}` with no group open, and for nested groups. The groups of one glob can give 4096 combinations at most, and `compile` returns a `GlobError` for more. It also returns one for a range whose end is before its start, and for a `\` at the end. Its `message` is that of ripgrep.

## Executable example

```fern test name=std-glob-example
import "std/glob";

function main(): i32 {
  if (!glob.is_match("*.fern", "main.fern")) { return 1; }
  if (glob.is_match("a?c", "ac")) { return 2; }
  let opts = glob.GlobOptions { literal_separator: true, backslash_escape: true, case_insensitive: false };
  let Ok(g) = glob.compile("**/*.{rs,c}", opts) else { return 3; };
  if (!g.matches("src/main.rs") || g.matches("src/main.h")) { return 4; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 function __glob_class(p: string, start: i32, ch: i32): (boolean, i32)>>

<<3 pub function is_match(pattern: string, text: string): boolean>>

<<4 pub function compile(pattern: string, opts: GlobOptions): Result[Glob, GlobError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module compares strings with shell glob patterns.
import "core/cmp";
import "core/error";
import "std/array";
import "std/u8";
```

### `2 function __glob_class(p: string, start: i32, ch: i32): (boolean, i32)`

```fern
<<2 function __glob_class(p: string, start: i32, ch: i32): (boolean, i32)>>=
function __glob_class(p: string, start: i32, ch: i32): (boolean, i32) {
  let i = start + 1;
  let negate = false;
  if (i < p.len() && (p[i] == 33 || p[i] == 94)) {
    negate = true;
    i += 1;
  }
  let matched = false;
  let first = true;
  while (i < p.len() && (p[i] != 93 || first)) {
    first = false;
    if (i + 2 < p.len() && p[i + 1] == 45 && p[i + 2] != 93) {
      if (ch >= p[i] as i32 && ch <= p[i + 2] as i32) {
        matched = true;
      }
      i += 3;
    } else {
      if (ch == p[i] as i32) {
        matched = true;
      }
      i += 1;
    }
  }
  let next = i;
  if (i < p.len()) {
    next = i + 1;
  }
  if (negate) {
    return (!matched, next);
  }
  return (matched, next);
}

// Each step maps the text positions that the pattern so far can reach
// to the positions after one more pattern token.
function __glob_match(p: string, t: string): boolean {
  let cur: boolean[] = [true];
  for k in 0..t.len() {
    cur = cur.append(false);
  }
  let pi = 0;
  while (pi < p.len()) {
    let pc = p[pi] as i32;
    if (pc == 42 && pi + 1 < p.len() && p[pi + 1] == 42) {
      while (pi < p.len() && p[pi] == 42) {
        pi += 1;
      }
      let slash = pi < p.len() && p[pi] == 47;
      cur = __glob_globstar(cur, t, slash);
      if (slash) {
        pi += 1;
      }
    } else if (pc == 42) {
      cur = __glob_star(cur, t);
      pi += 1;
    } else {
      cur = __glob_one(cur, p, pi, t);
      pi = pi + 1;
      if (pc == 91) {
        pi = __glob_class(p, pi - 1, 0).1;
      }
    }
    if (!__glob_any(cur)) {
      return false;
    }
  }
  return cur[t.len()];
}

function __glob_any(cur: boolean[]): boolean {
  for v in cur {
    if (v) {
      return true;
    }
  }
  return false;
}

function __glob_globstar(cur: boolean[], t: string, slash: boolean): boolean[] {
  let next: boolean[] = [];
  let seen = false;
  for k in 0..cur.len() {
    if (slash) {
      next = next.append(cur[k] || seen && t[k - 1] == 47);
    } else {
      next = next.append(cur[k] || seen);
    }
    seen = seen || cur[k];
  }
  return next;
}

function __glob_star(cur: boolean[], t: string): boolean[] {
  let next: boolean[] = [];
  let carry = false;
  for k in 0..cur.len() {
    let v = cur[k] || carry;
    carry = v && k < t.len() && t[k] != 47;
    next = next.append(v);
  }
  return next;
}

function __glob_one(cur: boolean[], p: string, pi: i32, t: string): boolean[] {
  let pc = p[pi] as i32;
  let next: boolean[] = [false];
  for k in 0..t.len() {
    let ch = t[k] as i32;
    if (!cur[k]) {
      next = next.append(false);
    } else if (pc == 63) {
      next = next.append(ch != 47);
    } else if (pc == 91) {
      next = next.append(ch != 47 && __glob_class(p, pi, ch).0);
    } else {
      next = next.append(ch == pc);
    }
  }
  return next;
}
```

### `3 pub function is_match(pattern: string, text: string): boolean`

```fern
<<3 pub function is_match(pattern: string, text: string): boolean>>=
pub function is_match(pattern: string, text: string): boolean {
  return __glob_match(pattern, text);
}
```

### `4 pub function compile(pattern: string, opts: GlobOptions): Result[Glob, GlobError]`

```fern
<<4 pub function compile(pattern: string, opts: GlobOptions): Result[Glob, GlobError]>>=
// The options of a compiled glob. With `literal_separator`, `*`, `?` and
// `[...]` do not match `/`. With `backslash_escape`, `\` makes the next
// character a literal. With `case_insensitive`, ASCII letters match in
// either case.
pub struct GlobOptions {
  literal_separator: boolean,
  backslash_escape: boolean,
  case_insensitive: boolean,
}

// A glob that does not compile: the glob and the reason.
pub struct GlobError { glob: string, reason: string }

pub function (e: GlobError) message(): string {
  return f"error parsing glob '{e.glob}': {e.reason}";
}

pub function (e: GlobError) to_string(): string {
  return e.message();
}

impl error.Error for GlobError {}

impl cmp.Display for GlobError {}

// A compiled glob. It matches when one of its variants matches. A variant is
// the glob with one choice for each `{a,b}` group.
pub struct Glob { variants: GlobVariant[], classes: u8[], fold: boolean, sep: boolean }

// One variant: a literal test, or the states of an automaton.
struct GlobVariant { kind: i32, lit: string, states: u8[], args: i32[] }

// One token of a parsed glob. `arg` is the byte of a literal, the index of
// a class, or the index of a `{a,b}` group.
struct GlobTok { kind: i32, arg: i32 }

// The parse state: the token lists (the glob, then each open alternative),
// the closed groups, the class tables, and the offset of the next byte.
struct GlobParse { stack: GlobTok[][], groups: GlobTok[][][], classes: u8[], at: i32 }

const GLOB_LIT: i32 = 0;

const GLOB_ANY: i32 = 1;

const GLOB_CLASS: i32 = 2;

const GLOB_STAR: i32 = 3;

// `**/` at the start, `/**` at the end, and `/**/` in the middle.
const GLOB_PREFIX: i32 = 4;

const GLOB_SUFFIX: i32 = 5;

const GLOB_MIDDLE: i32 = 6;

const GLOB_GROUP: i32 = 7;

const VARIANT_EXACT: i32 = 0;

const VARIANT_BASENAME: i32 = 1;

const VARIANT_BASENAME_SUFFIX: i32 = 2;

const VARIANT_NAME_SUFFIX: i32 = 3;

const VARIANT_STATES: i32 = 4;

// The states of the automaton. The second state of a two-state token comes
// right after its first state.
const STATE_LIT: u8 = 0;

const STATE_ANY: u8 = 1;

const STATE_CLASS: u8 = 2;

const STATE_STAR: u8 = 3;

const STATE_PREFIX: u8 = 4;

const STATE_PREFIX_LOOP: u8 = 5;

const STATE_SUFFIX: u8 = 6;

const STATE_SUFFIX_LOOP: u8 = 7;

const STATE_MIDDLE: u8 = 8;

const STATE_MIDDLE_LOOP: u8 = 9;

const STATE_ALL: u8 = 10;

const STATE_END: u8 = 11;

function __glob_err(pattern: string, reason: string): GlobError {
  return GlobError { glob: pattern, reason: reason };
}

function __glob_top(p: GlobParse): GlobTok[] {
  return p.stack[p.stack.len() - 1];
}

function __glob_push(p: GlobParse, kind: i32, arg: i32): GlobParse {
  let k = p.stack.len() - 1;
  return GlobParse { ...p, stack: p.stack.with(k, p.stack[k].append(GlobTok { kind: kind, arg: arg })) };
}

function __glob_stars(p: GlobParse, at: i32): GlobParse {
  return GlobParse { ...__glob_push(__glob_push(p, GLOB_STAR, 0), GLOB_STAR, 0), at: at };
}

// `**` at `p.at`. As in the globset crate, `**` is recursive only as a whole
// path component. Elsewhere it is two `*`.
function __glob_double_star(p: GlobParse, pattern: string): GlobParse {
  let after = p.at + 2;
  let peek = if (after < pattern.len()) { pattern[after] as i32 } else { -1 };
  if (__glob_top(p).len() == 0) {
    return __glob_leading_stars(p, peek, after);
  }
  let prev = if (p.at > 0) { pattern[p.at - 1] as i32 } else { -1 };
  let grouped = p.stack.len() >= 2;
  if (!__glob_after_component(prev, grouped)) {
    return __glob_stars(p, after);
  }
  let suffix = __glob_before_end(peek, grouped);
  if (!suffix && peek != 47) {
    return __glob_stars(p, after);
  }
  let next = if (peek == 47) { after + 1 } else { after };
  return GlobParse { ...__glob_recursive_token(p, suffix), at: next };
}

// Whether `**` follows a `/`, or a `{` or a `,` of a group.
function __glob_after_component(prev: i32, grouped: boolean): boolean {
  return prev == 47 || grouped && (prev == 44 || prev == 123);
}

// Whether `**` ends the glob, or a choice of a group.
function __glob_before_end(peek: i32, grouped: boolean): boolean {
  return peek < 0 || grouped && (peek == 44 || peek == 125);
}

// `**` at the start of the glob or of a choice: a recursive prefix when a
// `/` or the end follows it.
function __glob_leading_stars(p: GlobParse, peek: i32, after: i32): GlobParse {
  if (peek >= 0 && peek != 47) {
    return __glob_stars(p, after);
  }
  let skip = if (peek == 47) { 1 } else { 0 };
  return GlobParse { ...__glob_push(p, GLOB_PREFIX, 0), at: after + skip };
}

// The `/` token before `**` becomes a recursive suffix or a recursive
// middle. A recursive token before it stays.
function __glob_recursive_token(p: GlobParse, suffix: boolean): GlobParse {
  let top = __glob_top(p);
  let last = top[top.len() - 1].kind;
  let q = GlobParse { ...p, stack: p.stack.with(p.stack.len() - 1, top.take(top.len() - 1)) };
  let kind = if (last == GLOB_PREFIX || last == GLOB_SUFFIX) { last } else if (suffix) { GLOB_SUFFIX } else { GLOB_MIDDLE };
  return __glob_push(q, kind, 0);
}

function __glob_star_at(p: GlobParse, pattern: string): GlobParse {
  if (p.at + 1 < pattern.len() && pattern[p.at + 1] == b'*') {
    return __glob_double_star(p, pattern);
  }
  return GlobParse { ...__glob_push(p, GLOB_STAR, 0), at: p.at + 1 };
}

function __glob_byte_text(b: i32): string {
  return unsafe { string_from_bytes_unchecked([b as u8]) };
}

// The ranges of the class that starts at `[` at `start`, two bounds for each
// range, and the offset after the `]`. As in the globset crate, a `]` or a
// `-` first is a literal, and so is a `-` last.
function __glob_ranges(pattern: string, start: i32): Result[(i32[], i32), GlobError] {
  let at = start;
  let ranges: i32[] = [];
  let first = true;
  let in_range = false;
  loop {
    if (at >= pattern.len()) {
      return Err(__glob_err(pattern, "unclosed character class; missing ']'"));
    }
    let c = pattern[at] as i32;
    at += 1;
    if (c == 93 && !first) {
      break;
    }
    if (in_range) {
      let lo = ranges[ranges.len() - 2];
      if (c < lo) {
        return Err(__glob_err(pattern, f"invalid range; '{__glob_byte_text(lo)}' > '{__glob_byte_text(c)}'"));
      }
      ranges = ranges.with(ranges.len() - 1, c);
      in_range = false;
    } else if (c == 45 && !first) {
      in_range = true;
    } else {
      ranges = ranges.append(c).append(c);
    }
    first = false;
  }
  if (in_range) {
    ranges = ranges.append(45).append(45);
  }
  return Ok((ranges, at));
}

// A class from the `[` at `p.at`, added as a table of 256 bytes.
function __glob_class_at(p: GlobParse, pattern: string): Result[GlobParse, GlobError] {
  let at = p.at + 1;
  let negated = at < pattern.len() && (pattern[at] == b'!' || pattern[at] == b'^');
  if (negated) {
    at += 1;
  }
  let (ranges, next) = __glob_ranges(pattern, at)?;
  let in_class: u8 = if (negated) { 0 } else { 1 };
  let table = __alloc_u8(256);
  if (negated) {
    for b in 0..256 {
      table = table.with(b, 1);
    }
  }
  let k = 0;
  while (k < ranges.len()) {
    for b in ranges[k]..ranges[k + 1] + 1 {
      table = table.with(b, in_class);
    }
    k += 2;
  }
  let index = p.classes.len() / 256;
  let q = GlobParse { ...p, classes: array.concat(p.classes, table), at: next };
  return Ok(__glob_push(q, GLOB_CLASS, index));
}

// A `}`: the open alternatives become one group.
function __glob_close_group(p: GlobParse, pattern: string): Result[GlobParse, GlobError] {
  if (p.stack.len() <= 1) {
    return Err(__glob_err(pattern, "unopened alternate group; use '[}]' to match an unopened alternate group"));
  }
  let alts: GlobTok[][] = [];
  for k in 1..p.stack.len() {
    if (p.stack[k].len() > 0) {
      alts = alts.append(p.stack[k]);
    }
  }
  let q = GlobParse { ...p, stack: [p.stack[0]], groups: p.groups.append(alts), at: p.at + 1 };
  return Ok(__glob_push(q, GLOB_GROUP, p.groups.len()));
}

function __glob_escape_at(p: GlobParse, pattern: string, opts: GlobOptions): Result[GlobParse, GlobError] {
  if (!opts.backslash_escape) {
    return Ok(GlobParse { ...__glob_push(p, GLOB_LIT, 92), at: p.at + 1 });
  }
  if (p.at + 1 >= pattern.len()) {
    return Err(__glob_err(pattern, "dangling '\\'"));
  }
  return Ok(GlobParse { ...__glob_push(p, GLOB_LIT, pattern[p.at + 1] as i32), at: p.at + 2 });
}

function __glob_open(p: GlobParse, pattern: string): Result[GlobParse, GlobError] {
  if (p.stack.len() > 1) {
    return Err(__glob_err(pattern, "nested alternate groups are not allowed"));
  }
  let empty: GlobTok[] = [];
  return Ok(GlobParse { ...p, stack: p.stack.append(empty), at: p.at + 1 });
}

function __glob_comma(p: GlobParse): GlobParse {
  if (p.stack.len() <= 1) {
    return GlobParse { ...__glob_push(p, GLOB_LIT, 44), at: p.at + 1 };
  }
  let empty: GlobTok[] = [];
  return GlobParse { ...p, stack: p.stack.append(empty), at: p.at + 1 };
}

// One step of the parse at `p.at`.
function __glob_step(p: GlobParse, pattern: string, opts: GlobOptions): Result[GlobParse, GlobError] {
  let c = pattern[p.at];
  return match (c) {
    b'?' => Ok(GlobParse { ...__glob_push(p, GLOB_ANY, 0), at: p.at + 1 }),
    b'*' => Ok(__glob_star_at(p, pattern)),
    b'[' => __glob_class_at(p, pattern),
    b'{' => __glob_open(p, pattern),
    b'}' => __glob_close_group(p, pattern),
    b',' => Ok(__glob_comma(p)),
    b'\\' => __glob_escape_at(p, pattern, opts),
    _ => Ok(GlobParse { ...__glob_push(p, GLOB_LIT, c as i32), at: p.at + 1 }),
  };
}

// The most variants of one glob. Each group multiplies the count, so a
// pattern of 24 groups of two would have 16777216.
const GLOB_MAX_VARIANTS: i32 = 4096;

// The count of the variants of `tokens`, or None for more than
// GLOB_MAX_VARIANTS.
function __glob_variant_count(tokens: GlobTok[], groups: GlobTok[][][]): Option[i32] {
  let n = 1;
  for t in tokens {
    if (t.kind == GLOB_GROUP && groups[t.arg].len() > 0) {
      n *= groups[t.arg].len();
      if (n > GLOB_MAX_VARIANTS) {
        return None;
      }
    }
  }
  return Some(n);
}

// The token lists of the `count` variants: one choice for each group, with
// the choices of the first group in order.
function __glob_variants(tokens: GlobTok[], groups: GlobTok[][][], count: i32): GlobTok[][] {
  let out: GlobTok[][] = [];
  for i in 0..count {
    let v: GlobTok[] = [];
    let stride = count;
    for t in tokens {
      if (t.kind != GLOB_GROUP) {
        v = v.append(t);
      } else if (groups[t.arg].len() > 0) {
        let alts = groups[t.arg];
        stride = stride / alts.len();
        for a in alts[i / stride % alts.len()] {
          v = v.append(a);
        }
      }
    }
    out = out.append(v);
  }
  return out;
}

// The literal of `tokens` from `from`, or None when a token is not a literal
// or, with `name`, the literal holds a `/`.
function __glob_literal(tokens: GlobTok[], from: i32, name: boolean): Option[string] {
  let bytes = __alloc_u8(tokens.len() - from);
  for k in from..tokens.len() {
    let t = tokens[k];
    if (t.kind != GLOB_LIT || name && t.arg == 47) {
      return None;
    }
    bytes = bytes.with(k - from, t.arg as u8);
  }
  // The bytes are those of the pattern, less the ASCII escapes.
  return Some(unsafe { string_from_bytes_unchecked(bytes) });
}

function __glob_lit_variant(kind: i32, lit: string): GlobVariant {
  return GlobVariant { kind: kind, lit: lit, states: [], args: [] };
}

// The fastest test for one variant. The literal forms are those of the
// globset crate: a whole path, a base name, and a suffix of a base name.
function __glob_variant(tokens: GlobTok[], sep: boolean): GlobVariant {
  if let Some(lit) = __glob_literal(tokens, 0, false) {
    return __glob_lit_variant(VARIANT_EXACT, lit);
  }
  let (kind, skip) = __glob_form(tokens, sep);
  if (kind >= 0 && tokens.len() > skip) {
    if let Some(lit) = __glob_literal(tokens, skip, true) {
      return __glob_lit_variant(kind, lit);
    }
  }
  return __glob_states(tokens);
}

// The literal form that the tokens can have, and the count of tokens
// before the literal, or -1: `**/lit`, `**/*lit`, or `*lit`.
function __glob_form(tokens: GlobTok[], sep: boolean): (i32, i32) {
  let first = if (tokens.len() > 0) { tokens[0].kind } else { -1 };
  let second = if (tokens.len() > 1) { tokens[1].kind } else { -1 };
  if (first == GLOB_PREFIX && second == GLOB_STAR && sep) {
    return (VARIANT_BASENAME_SUFFIX, 2);
  }
  if (first == GLOB_PREFIX) {
    return (VARIANT_BASENAME, 1);
  }
  if (first == GLOB_STAR && sep) {
    return (VARIANT_NAME_SUFFIX, 1);
  }
  return (-1, 0);
}

function __glob_state_kind(kind: i32): u8 {
  return match (kind) {
    GLOB_LIT => STATE_LIT,
    GLOB_ANY => STATE_ANY,
    GLOB_CLASS => STATE_CLASS,
    GLOB_STAR => STATE_STAR,
    GLOB_PREFIX => STATE_PREFIX,
    GLOB_SUFFIX => STATE_SUFFIX,
    _ => STATE_MIDDLE,
  };
}

// The automaton of one variant. A recursive token takes two states.
function __glob_states(tokens: GlobTok[]): GlobVariant {
  let states: u8[] = [];
  let args: i32[] = [];
  if (tokens.len() == 1 && tokens[0].kind == GLOB_PREFIX) {
    return GlobVariant { kind: VARIANT_STATES, lit: "", states: [STATE_ALL, STATE_END], args: [0, 0] };
  }
  for t in tokens {
    let s = __glob_state_kind(t.kind);
    states = states.append(s);
    args = args.append(t.arg);
    if (s == STATE_PREFIX || s == STATE_SUFFIX || s == STATE_MIDDLE) {
      states = states.append(s + 1);
      args = args.append(0);
    }
  }
  return GlobVariant { kind: VARIANT_STATES, lit: "", states: states.append(STATE_END), args: args.append(0) };
}

// Compiles `pattern` with the syntax of the globset crate: `?`, `*`, `**`,
// `[...]`, `{a,b}` and, with `backslash_escape`, `\`.
pub function compile(pattern: string, opts: GlobOptions): Result[Glob, GlobError] {
  let empty: GlobTok[] = [];
  let p = GlobParse { stack: [empty], groups: [], classes: [], at: 0 };
  while (p.at < pattern.len()) {
    p = __glob_step(p, pattern, opts)?;
  }
  if (p.stack.len() > 1) {
    return Err(__glob_err(pattern, "unclosed alternate group; missing '}' (maybe escape '{' with '[{]'?)"));
  }
  let Some(count) = __glob_variant_count(p.stack[0], p.groups) else {
    return Err(__glob_err(pattern, f"more than {GLOB_MAX_VARIANTS} alternates"));
  };
  let variants: GlobVariant[] = [];
  for v in __glob_variants(p.stack[0], p.groups, count) {
    variants = variants.append(__glob_variant(v, opts.literal_separator));
  }
  return Ok(Glob { variants: variants, classes: p.classes, fold: opts.case_insensitive, sep: opts.literal_separator });
}

function __glob_eq(a: u8, b: u8, fold: boolean): boolean {
  return a == b || fold && a.to_ascii_lower() == b.to_ascii_lower();
}

// Whether `text` ends with `lit` from `from` on.
function __glob_ends(text: string, from: i32, lit: string, fold: boolean): boolean {
  let start = text.len() - lit.len();
  if (start < from) {
    return false;
  }
  for k in 0..lit.len() {
    if (!__glob_eq(text[start + k], lit[k], fold)) {
      return false;
    }
  }
  return true;
}

function __glob_base(text: string): i32 {
  if (text.len() == 0) {
    return 0;
  }
  return __rmemchr(text, 47, text.len() - 1) + 1;
}

function __glob_member(g: Glob, class: i32, b: u8): boolean {
  let base = class * 256;
  return g.classes[base + b as i32] == 1 || g.fold && (g.classes[base + b.to_ascii_lower() as i32] == 1 || g.classes[base + b.to_ascii_upper() as i32] == 1);
}

// Whether one byte `b` lets state `i` of a single-state token advance.
function __glob_takes(g: Glob, v: GlobVariant, i: i32, b: u8): boolean {
  let s = v.states[i];
  if (s == STATE_LIT) {
    return __glob_eq(b, v.args[i] as u8, g.fold);
  }
  if (s == STATE_CLASS) {
    return __glob_member(g, v.args[i], b);
  }
  if (g.sep) {
    return b != b'/';
  }
  return b != b'\n';
}

// The states after byte `b` from the states in `cur`, into `next`.
function __glob_step_states(g: Glob, v: GlobVariant, cur: u8[], own next: u8[], b: u8): u8[] {
  for i in 0..v.states.len() {
    if (cur[i] == 0) {
      continue;
    }
    let rel = __glob_rel(g, v, i, b);
    for k in 0..3 {
      if ((rel >> k & 1) == 1) {
        next = next.with(i + k, 1);
      }
    }
  }
  return __glob_closure(v, next);
}

// The states that state `i` reaches on byte `b`, as three bits: state i,
// state i + 1 and state i + 2.
function __glob_rel(g: Glob, v: GlobVariant, i: i32, b: u8): i32 {
  let s = v.states[i];
  if (s > STATE_STAR) {
    return __glob_rel_recursive(s, b);
  }
  if (!__glob_takes(g, v, i, b)) {
    return 0;
  }
  return if (s == STATE_STAR) { 1 } else { 2 };
}

// The moves of the states of `**/`, `/**` and `/**/`. Their `.*` parts do
// not match a newline, as in the regular expressions of the globset crate.
function __glob_rel_recursive(s: u8, b: u8): i32 {
  if (b == b'/') {
    return __glob_rel_slash(s);
  }
  if (b == b'\n') {
    return 0;
  }
  return match (s) {
    STATE_PREFIX => 2,
    STATE_PREFIX_LOOP | STATE_SUFFIX_LOOP | STATE_MIDDLE_LOOP | STATE_ALL => 1,
    _ => 0,
  };
}

// The moves of the states of `**/`, `/**` and `/**/` on a `/`.
function __glob_rel_slash(s: u8): i32 {
  return match (s) {
    STATE_PREFIX | STATE_MIDDLE => 6,
    STATE_SUFFIX => 2,
    STATE_PREFIX_LOOP | STATE_MIDDLE_LOOP => 3,
    STATE_SUFFIX_LOOP | STATE_ALL => 1,
    _ => 0,
  };
}

// The state that state `s` at offset 0 reaches with no byte: 1, 2, or 0
// for none.
function __glob_eps(s: u8): i32 {
  return match (s) {
    STATE_STAR | STATE_SUFFIX_LOOP | STATE_ALL => 1,
    STATE_PREFIX => 2,
    _ => 0,
  };
}

// Adds the states that a state reaches with no byte. Each such move goes
// forward, so one pass in order is enough.
function __glob_closure(v: GlobVariant, own set: u8[]): u8[] {
  for i in 0..v.states.len() {
    let to = __glob_eps(v.states[i]);
    if (set[i] == 1 && to > 0) {
      set = set.with(i + to, 1);
    }
  }
  return set;
}

// The automaton with one byte for each state. It serves an automaton of
// more than 64 states.
function __glob_run(g: Glob, v: GlobVariant, text: string): boolean {
  let n = v.states.len();
  if (n <= 64) {
    return __glob_run_bits(g, v, text);
  }
  let cur = __glob_closure(v, __alloc_u8(n).with(0, 1));
  let next = __alloc_u8(n);
  for k in 0..text.len() {
    next = __glob_step_states(g, v, cur, next, text[k]);
    if (!__glob_any_u8(next)) {
      return false;
    }
    let done = cur;
    cur = next;
    next = done;
    for i in 0..n {
      next = next.with(i, 0);
    }
  }
  return cur[n - 1] == 1;
}

function __glob_any_u8(set: u8[]): boolean {
  for x in set {
    if (x != 0) {
      return true;
    }
  }
  return false;
}

// The automaton with one bit of a u64 for each state. It allocates nothing,
// and it stops at the first byte that leaves no state.
function __glob_run_bits(g: Glob, v: GlobVariant, text: string): boolean {
  let cur = __glob_close_bits(v, 1u64);
  for k in 0..text.len() {
    let b = text[k];
    let next = 0u64;
    let rest = cur;
    while (rest != 0u64) {
      let i = __ctz64(rest);
      rest &= rest - 1u64;
      next |= __glob_rel(g, v, i, b) as u64 << i as u64;
    }
    if (next == 0u64) {
      return false;
    }
    cur = __glob_close_bits(v, next);
  }
  return (cur >> (v.states.len() - 1) as u64 & 1u64) == 1u64;
}

// Adds the states that a state reaches with no byte. Each such move goes
// forward, so the loop takes each state once.
function __glob_close_bits(v: GlobVariant, set0: u64): u64 {
  let set = set0;
  let rest = set0;
  while (rest != 0u64) {
    let i = __ctz64(rest);
    rest &= rest - 1u64;
    let to = __glob_eps(v.states[i]);
    let bit = 1u64 << (i + to) as u64;
    if (to > 0 && (set & bit) == 0u64) {
      set |= bit;
      rest |= bit;
    }
  }
  return set;
}

function __glob_variant_matches(g: Glob, v: GlobVariant, text: string): boolean {
  return match (v.kind) {
    VARIANT_EXACT => text.len() == v.lit.len() && __glob_ends(text, 0, v.lit, g.fold),
    VARIANT_BASENAME => text.len() - __glob_base(text) == v.lit.len() && __glob_ends(text, 0, v.lit, g.fold),
    VARIANT_BASENAME_SUFFIX => __glob_ends(text, __glob_base(text), v.lit, g.fold),
    VARIANT_NAME_SUFFIX => __memchr(text, 47, 0) < 0 && __glob_ends(text, 0, v.lit, g.fold),
    _ => __glob_run(g, v, text),
  };
}

// Whether `text`, a whole path, matches the compiled glob.
pub function (g: Glob) matches(text: string): boolean {
  for v in g.variants {
    if (__glob_variant_matches(g, v, text)) {
      return true;
    }
  }
  return false;
}
```
