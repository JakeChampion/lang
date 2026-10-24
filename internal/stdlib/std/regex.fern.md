# Regular expression evaluation

This module evaluates regular expressions.

## Model

A compiled expression retains its syntax tree, case policy, and capture metadata. Match values identify input spans.

## Contracts

Search and complete-match operations have different purposes. Invalid patterns can produce optional errors through the public helper contracts.

## Executable example

```fern test name=std-regex-example
import "std/regex";

function main(): i32 {
  if (!regex.regex_match("a+b", "aaab") || regex.regex_match("a+b", "ac")) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 struct RGroupData>>

<<4 function __rx_atom(p: string, i: i32, g: i32): RParse>>

<<5 function __rx_count(p: string, pos: i32, a: RParse): RParse>>

<<6 pub struct RProg>>

<<7 function __rx_group_names(pattern: string): string[]>>

<<8 function __rx_match(n: RNode, t: string, ti: i32, ci: boolean): i32[]>>

<<9 pub struct RMatch>>

<<10 function __rx_search_from(root: RNode, text: string, from: i32, ci: boolean): Option[RMatch]>>

<<11 pub function regex_search(pattern: string, text: string): Option[RMatch]>>

<<12 function __rx_advance(m: RMatch): i32>>

<<13 pub function regex_find_all(pattern: string, text: string): RMatch[]>>

<<14 function __rx_finish(h: usize): u8[]>>

<<15 pub function regex_replace_all(pattern: string, text: string, repl: string): Option[string]>>

<<16 function __rx_replace_all_from(prog: RProg, text: string, repl: string, first: Option[RMatch]): u8[]>>

<<17 pub function regex_split(text: string, pattern: string): Option[string[]]>>

<<18 function __rx_replace_span(text: string, repl: string, m: RMatch): u8[]>>

<<19 pub function regex_replace_bytes(pattern: string, text: string, repl: string): u8[]>>

<<20 struct RSplit>>

<<21 function __rx_split(a: i32, b: i32): RInst>>

<<22 struct RThread>>

<<23 function __rx_addthread(st: RAdd, prog: RInst[], pc: i32, caps: i32[], t: string, ti: i32): RAdd>>

<<24 pub struct RCaps>>

<<25 pub function (c: RCaps) group_count(): i32>>

<<26 function __rx_span(c: RCaps, i: i32): (i32, i32)>>

<<27 pub function (c: RCaps) group(i: i32): Option[string]>>

<<28 function __rx_named_index(c: RCaps, name: string): i32>>

<<29 pub function (c: RCaps) group_named(name: string): Option[string]>>

<<30 function __rx_caps_wrap(caps: i32[], text: string, ngroups: i32, names: string[]): Option[RCaps]>>

<<31 pub function regex_captures(pattern: string, text: string): Option[RCaps]>>

<<32 function __rx_template_ref(tmpl: string, i: i32, c: RCaps): (i32, i32)>>

<<33 pub function regex_replace_groups(pattern: string, text: string, tmpl: string): Option[string]>>

<<34 function __rx_replace_capture(text: string, tmpl: string, c: RCaps): u8[]>>

<<35 pub function regex_replace_all_groups(pattern: string, text: string, tmpl: string): Option[string]>>

<<36 function __rx_replace_captures(text: string, tmpl: string, all: RCaps[]): u8[]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module evaluates regular expressions.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/utf8" as utf8;
```

### `3 struct RGroupData`

```fern
<<3 struct RGroupData>>=
struct RGroupData { idx: i32, node: RNode }

pub enum RNode {
  RChar(i32),
  RAny,
  RClass(string),
  RStart,
  REnd,
  RBound(boolean),
  RSeq(RNode[]),
  RAlt(RNode[]),
  RStar(RNode),
  RPlus(RNode),
  ROpt(RNode),
  RGroup(RGroupData),
}

struct RParse { node: RNode, pos: i32, g: i32 }
```

### `4 function __rx_atom(p: string, i: i32, g: i32): RParse`

```fern
<<4 function __rx_atom(p: string, i: i32, g: i32): RParse>>=
function __rx_atom(p: string, i: i32, g: i32): RParse {
  let c: i32 = p[i] as i32;
  if (c == 92) {
    if (i + 1 >= p.len()) {
      return RParse { node: RChar(92), pos: i + 1, g: g };
    }
    let d: i32 = p[i + 1] as i32;
    if (d == 100) {
      return RParse { node: RClass("[0-9]"), pos: i + 2, g: g };
    }
    if (d == 68) {
      return RParse { node: RClass("[^0-9]"), pos: i + 2, g: g };
    }
    if (d == 119) {
      return RParse { node: RClass("[A-Za-z0-9_]"), pos: i + 2, g: g };
    }
    if (d == 87) {
      return RParse { node: RClass("[^A-Za-z0-9_]"), pos: i + 2, g: g };
    }
    if (d == 115) {
      return RParse { node: RClass("[ \t\n\r]"), pos: i + 2, g: g };
    }
    if (d == 83) {
      return RParse { node: RClass("[^ \t\n\r]"), pos: i + 2, g: g };
    }
    if (d == 98) {
      return RParse { node: RBound(true), pos: i + 2, g: g };
    }
    if (d == 66) {
      return RParse { node: RBound(false), pos: i + 2, g: g };
    }
    if (d == 110) {
      return RParse { node: RChar(10), pos: i + 2, g: g };
    }
    if (d == 116) {
      return RParse { node: RChar(9), pos: i + 2, g: g };
    }
    if (d == 114) {
      return RParse { node: RChar(13), pos: i + 2, g: g };
    }
    return RParse { node: RChar(d), pos: i + 2, g: g };
  }
  if (c == 40) {
    if (i + 1 < p.len() && p[i + 1] == 63) {
      if (i + 3 < p.len() && p[i + 2] == 60
        && p[i + 3] != 61 && p[i + 3] != 33) {
        let ne: i32 = i + 3;
        while (ne < p.len() && p[ne] != 62) {
          ne = ne + 1;
        }
        if (ne < p.len()) {
          let gi: RParse = __rx_alt(p, ne + 1, g + 1);
          let gpos: i32 = gi.pos;
          if (gpos < p.len() && p[gpos] == 41) {
            gpos = gpos + 1;
          }
          return RParse { node: RGroup(RGroupData { idx: g, node: gi.node }), pos: gpos, g: gi.g };
        }
      }
      let nb: i32 = i + 1;
      if (i + 2 < p.len() && p[i + 2] == 58) {
        nb = i + 3;
      }
      let ni: RParse = __rx_alt(p, nb, g);
      let npos: i32 = ni.pos;
      if (npos < p.len() && p[npos] == 41) {
        npos = npos + 1;
      }
      return RParse { node: ni.node, pos: npos, g: ni.g };
    }
    let inner: RParse = __rx_alt(p, i + 1, g + 1);
    let pos: i32 = inner.pos;
    if (pos < p.len() && p[pos] == 41) {
      pos = pos + 1;
    }
    return RParse { node: RGroup(RGroupData { idx: g, node: inner.node }), pos: pos, g: inner.g };
  }
  if (c == 91) {
    let j: i32 = i + 1;
    if (j < p.len() && p[j] == 94) {
      j = j + 1;
    }
    if (j < p.len() && p[j] == 93) {
      j = j + 1;
    }
    while (j < p.len() && p[j] != 93) {
      j = j + 1;
    }
    let endb: i32 = j;
    if (endb < p.len()) {
      endb = endb + 1;
    }
    return RParse { node: RClass(slice_unchecked(p, i, endb) + ""), pos: endb, g: g };
  }
  if (c == 46) {
    return RParse { node: RAny, pos: i + 1, g: g };
  }
  if (c == 94) {
    return RParse { node: RStart, pos: i + 1, g: g };
  }
  if (c == 36) {
    return RParse { node: REnd, pos: i + 1, g: g };
  }
  return RParse { node: RChar(c), pos: i + 1, g: g };
}

function __rx_repeat(node: RNode, n: i32, m: i32, has_max: boolean): RNode {
  let items: RNode[] = [];
  let i: i32 = 0;
  while (i < n) {
    items = items.append(node);
    i = i + 1;
  }
  if (!has_max) {
    items = items.append(RStar(node));
  } else {
    let k: i32 = n;
    while (k < m) {
      items = items.append(ROpt(node));
      k = k + 1;
    }
  }
  if (items.len() == 1) {
    return items[0];
  }
  return RSeq(items);
}
```

### `5 function __rx_count(p: string, pos: i32, a: RParse): RParse`

```fern
<<5 function __rx_count(p: string, pos: i32, a: RParse): RParse>>=
function __rx_count(p: string, pos: i32, a: RParse): RParse {
  let j: i32 = pos + 1;
  let n: i32 = 0;
  let have_n: boolean = false;
  while (j < p.len() && p[j] as i32 >= 48 && p[j] as i32 <= 57) {
    n = n * 10 + (p[j] as i32 - 48);
    j = j + 1;
    have_n = true;
  }
  if (!have_n) {
    return a;
  }
  let m: i32 = n;
  let has_max: boolean = true;
  if (j < p.len() && p[j] == 44) {
    j = j + 1;
    if (j < p.len() && p[j] == 125) {
      has_max = false;
    } else {
      m = 0;
      while (j < p.len() && p[j] as i32 >= 48 && p[j] as i32 <= 57) {
        m = m * 10 + (p[j] as i32 - 48);
        j = j + 1;
      }
    }
  }
  if (j >= p.len() || p[j] != 125) {
    return a;
  }
  return RParse { node: __rx_repeat(a.node, n, m, has_max), pos: j + 1, g: a.g };
}

function __rx_quant(p: string, i: i32, g: i32): RParse {
  let a: RParse = __rx_atom(p, i, g);
  let pos: i32 = a.pos;
  if (pos < p.len()) {
    let c: i32 = p[pos] as i32;
    if (c == 42) {
      return RParse { node: RStar(a.node), pos: pos + 1, g: a.g };
    }
    if (c == 43) {
      return RParse { node: RPlus(a.node), pos: pos + 1, g: a.g };
    }
    if (c == 63) {
      return RParse { node: ROpt(a.node), pos: pos + 1, g: a.g };
    }
    if (c == 123) {
      return __rx_count(p, pos, a);
    }
  }
  return a;
}

function __rx_seq(p: string, i: i32, g: i32): RParse {
  let items: RNode[] = [];
  let pos: i32 = i;
  let gi: i32 = g;
  while (pos < p.len() && p[pos] != 124 && p[pos] != 41) {
    let q: RParse = __rx_quant(p, pos, gi);
    items = items.append(q.node);
    pos = q.pos;
    gi = q.g;
  }
  if (items.len() == 1) {
    return RParse { node: items[0], pos: pos, g: gi };
  }
  return RParse { node: RSeq(items), pos: pos, g: gi };
}

function __rx_alt(p: string, i: i32, g: i32): RParse {
  let first: RParse = __rx_seq(p, i, g);
  let branches: RNode[] = [first.node];
  let pos: i32 = first.pos;
  let gi: i32 = first.g;
  while (pos < p.len() && p[pos] == 124) {
    let nxt: RParse = __rx_seq(p, pos + 1, gi);
    branches = branches.append(nxt.node);
    pos = nxt.pos;
    gi = nxt.g;
  }
  if (branches.len() == 1) {
    return RParse { node: branches[0], pos: pos, g: gi };
  }
  return RParse { node: RAlt(branches), pos: pos, g: gi };
}

function __rx_is_word(c: i32): boolean {
  if (c >= 48 && c <= 57) {
    return true;
  }
  if (c >= 65 && c <= 90) {
    return true;
  }
  if (c >= 97 && c <= 122) {
    return true;
  }
  if (c == 95) {
    return true;
  }
  return false;
}

function __rx_has(xs: i32[], v: i32): boolean {
  let i: i32 = 0;
  while (i < xs.len()) {
    if (xs[i] == v) {
      return true;
    }
    i = i + 1;
  }
  return false;
}

function __rx_fold(c: i32, ci: boolean): i32 {
  if (ci && c >= 65 && c <= 90) {
    return c + 32;
  }
  return c;
}
```

### `6 pub struct RProg`

```fern
<<6 pub struct RProg>>=
pub struct RProg { root: RNode, ci: boolean, ngroups: i32, names: string[] }
```

### `7 function __rx_group_names(pattern: string): string[]`

```fern
<<7 function __rx_group_names(pattern: string): string[]>>=
function __rx_group_names(pattern: string): string[] {
  let names: string[] = [];
  let i: i32 = 0;
  let n: i32 = pattern.len();
  while (i < n) {
    let c: i32 = pattern[i] as i32;
    if (c == 92) {
      i = i + 2;
    } else if (c == 91) {
      let j: i32 = i + 1;
      if (j < n && pattern[j] == 94) {
        j = j + 1;
      }
      if (j < n && pattern[j] == 93) {
        j = j + 1;
      }
      while (j < n && pattern[j] != 93) {
        j = j + 1;
      }
      i = j + 1;
    } else if (c == 40) {
      if (i + 1 < n && pattern[i + 1] == 63) {
        if (i + 3 < n && pattern[i + 2] == 60
          && pattern[i + 3] != 61 && pattern[i + 3] != 33) {
          let ne: i32 = i + 3;
          while (ne < n && pattern[ne] != 62) {
            ne = ne + 1;
          }
          if (ne < n) {
            names = names.append(slice_unchecked(pattern, i + 3, ne) + "");
            i = ne + 1;
          } else {
            i = i + 1;
          }
        } else {
          i = i + 1;
        }
      } else {
        names = names.append("");
        i = i + 1;
      }
    } else {
      i = i + 1;
    }
  }
  return names;
}

function __rx_compile(pattern: string): RProg {
  let names: string[] = __rx_group_names(pattern);
  if (pattern.starts_with("(?i)")) {
    let r: RParse = __rx_alt(pattern, 4, 1);
    return RProg { root: r.node, ci: true, ngroups: r.g - 1, names: names };
  }
  let r0: RParse = __rx_alt(pattern, 0, 1);
  return RProg { root: r0.node, ci: false, ngroups: r0.g - 1, names: names };
}

function __rx_class(tok: string, c0: i32, ci: boolean): boolean {
  let c: i32 = __rx_fold(c0, ci);
  let j: i32 = 1;
  let neg: boolean = false;
  if (j < tok.len() && tok[j] == 94) {
    neg = true;
    j = j + 1;
  }
  let found: boolean = false;
  let end: i32 = tok.len() - 1;
  while (j < end) {
    if (tok[j + 1] == 45 && j + 2 < end) {
      if (c >= __rx_fold(tok[j] as i32, ci) && c <= __rx_fold(tok[j + 2] as i32, ci)) {
        found = true;
      }
      j = j + 3;
    } else {
      if (c == __rx_fold(tok[j] as i32, ci)) {
        found = true;
      }
      j = j + 1;
    }
  }
  if (neg) {
    return !found;
  }
  return found;
}

function __rx_closure(inner: RNode, t: string, ti: i32, allow_zero: boolean, ci: boolean): i32[] {
  let seen: i32[] = [];
  let frontier: i32[] = [];
  if (allow_zero) {
    seen = seen.append(ti);
    frontier = frontier.append(ti);
  } else {
    let first: i32[] = __rx_match(inner, t, ti, ci);
    let s: i32 = 0;
    while (s < first.len()) {
      if (!__rx_has(seen, first[s])) {
        seen = seen.append(first[s]);
        frontier = frontier.append(first[s]);
      }
      s = s + 1;
    }
  }
  while (frontier.len() > 0) {
    let nf: i32[] = [];
    let m: i32 = 0;
    while (m < frontier.len()) {
      let sub: i32[] = __rx_match(inner, t, frontier[m], ci);
      let s2: i32 = 0;
      while (s2 < sub.len()) {
        if (!__rx_has(seen, sub[s2])) {
          seen = seen.append(sub[s2]);
          nf = nf.append(sub[s2]);
        }
        s2 = s2 + 1;
      }
      m = m + 1;
    }
    frontier = nf;
  }
  return seen;
}
```

### `8 function __rx_match(n: RNode, t: string, ti: i32, ci: boolean): i32[]`

```fern
<<8 function __rx_match(n: RNode, t: string, ti: i32, ci: boolean): i32[]>>=
function __rx_match(n: RNode, t: string, ti: i32, ci: boolean): i32[] {
  match (n) {
    RChar(c) => {
      let e: i32[] = [];
      if (ti < t.len() && __rx_fold(t[ti] as i32, ci) == __rx_fold(c, ci)) {
        e = e.append(ti + 1);
      }
      return e;
    },
    RAny => {
      let e: i32[] = [];
      if (ti < t.len()) {
        e = e.append(ti + 1);
      }
      return e;
    },
    RClass(tok) => {
      let e: i32[] = [];
      if (ti < t.len() && __rx_class(tok, t[ti] as i32, ci)) {
        e = e.append(ti + 1);
      }
      return e;
    },
    RStart => {
      let e: i32[] = [];
      if (__rx_assert_ok(0, t, ti)) {
        e = e.append(ti);
      }
      return e;
    },
    REnd => {
      let e: i32[] = [];
      if (__rx_assert_ok(1, t, ti)) {
        e = e.append(ti);
      }
      return e;
    },
    RBound(want) => {
      let k: i32 = 3;
      if (want) {
        k = 2;
      }
      let e: i32[] = [];
      if (__rx_assert_ok(k, t, ti)) {
        e = e.append(ti);
      }
      return e;
    },
    RSeq(xs) => {
      let cur: i32[] = [ti];
      let k: i32 = 0;
      while (k < xs.len()) {
        let nextset: i32[] = [];
        let m: i32 = 0;
        while (m < cur.len()) {
          let sub: i32[] = __rx_match(xs[k], t, cur[m], ci);
          let s: i32 = 0;
          while (s < sub.len()) {
            if (!__rx_has(nextset, sub[s])) {
              nextset = nextset.append(sub[s]);
            }
            s = s + 1;
          }
          m = m + 1;
        }
        cur = nextset;
        k = k + 1;
      }
      return cur;
    },
    RAlt(xs) => {
      let out: i32[] = [];
      let k: i32 = 0;
      while (k < xs.len()) {
        let sub: i32[] = __rx_match(xs[k], t, ti, ci);
        let s: i32 = 0;
        while (s < sub.len()) {
          if (!__rx_has(out, sub[s])) {
            out = out.append(sub[s]);
          }
          s = s + 1;
        }
        k = k + 1;
      }
      return out;
    },
    ROpt(inner) => {
      let out: i32[] = [ti];
      let sub: i32[] = __rx_match(inner, t, ti, ci);
      let s: i32 = 0;
      while (s < sub.len()) {
        if (!__rx_has(out, sub[s])) {
          out = out.append(sub[s]);
        }
        s = s + 1;
      }
      return out;
    },
    RStar(inner) => {
      return __rx_closure(inner, t, ti, true, ci);
    },
    RPlus(inner) => {
      return __rx_closure(inner, t, ti, false, ci);
    },
    RGroup(gd) => {
      return __rx_match(gd.node, t, ti, ci);
    }
  }
}
```

### `9 pub struct RMatch`

```fern
<<9 pub struct RMatch>>=
pub struct RMatch { start: i32, end: i32 }
```

### `10 function __rx_search_from(root: RNode, text: string, from: i32, ci: boolean): Option[RMatch]`

```fern
<<10 function __rx_search_from(root: RNode, text: string, from: i32, ci: boolean): Option[RMatch]>>=
function __rx_search_from(root: RNode, text: string, from: i32, ci: boolean): Option[RMatch] {
  let ti: i32 = from;
  while (true) {
    let ends: i32[] = __rx_match(root, text, ti, ci);
    if (ends.len() > 0) {
      let best: i32 = ends[0];
      let i: i32 = 1;
      while (i < ends.len()) {
        if (ends[i] > best) {
          best = ends[i];
        }
        i = i + 1;
      }
      return Some(RMatch { start: ti, end: best });
    }
    if (ti >= text.len()) {
      return None;
    }
    ti = ti + 1;
  }
  return None;
}
```

### `11 pub function regex_search(pattern: string, text: string): Option[RMatch]`

```fern
<<11 pub function regex_search(pattern: string, text: string): Option[RMatch]>>=
pub function regex_search(pattern: string, text: string): Option[RMatch] {
  let prog: RProg = __rx_compile(pattern);
  return __rx_search_from(prog.root, text, 0, prog.ci);
}

pub function regex_match(pattern: string, text: string): boolean {
  if let Some(_) = regex_search(pattern, text) {
    return true;
  }
  return false;
}

pub function regex_find(pattern: string, text: string): Option[i32] {
  if let Some(m) = regex_search(pattern, text) {
    return Some(m.start);
  }
  return None;
}
```

### `12 function __rx_advance(m: RMatch): i32`

```fern
<<12 function __rx_advance(m: RMatch): i32>>=
function __rx_advance(m: RMatch): i32 {
  if (m.end > m.start) {
    return m.end;
  }
  return m.start + 1;
}
```

### `13 pub function regex_find_all(pattern: string, text: string): RMatch[]`

```fern
<<13 pub function regex_find_all(pattern: string, text: string): RMatch[]>>=
pub function regex_find_all(pattern: string, text: string): RMatch[] {
  let prog: RProg = __rx_compile(pattern);
  let out: RMatch[] = [];
  let pos: i32 = 0;
  while (pos <= text.len()) {
    let Some(m) = __rx_search_from(prog.root, text, pos, prog.ci) else {
      return out;
    };
    out = out.append(m);
    pos = __rx_advance(m);
  }
  return out;
}
```

### `14 function __rx_finish(h: usize): u8[]`

```fern
<<14 function __rx_finish(h: usize): u8[]>>=
function __rx_finish(h: usize): u8[] {
  let bytes: u8[] = buf_take_bytes(h);
  buf_free(h);
  return bytes;
}

function __rx_bytes(text: string, lo: i32, hi: i32): u8[] {
  let h: usize = buf_new(hi - lo);
  buf_push_range(h, text, lo, hi);
  return __rx_finish(h);
}
```

### `15 pub function regex_replace_all(pattern: string, text: string, repl: string): Option[string]`

```fern
<<15 pub function regex_replace_all(pattern: string, text: string, repl: string): Option[string]>>=
pub function regex_replace_all(pattern: string, text: string, repl: string): Option[string] {
  let prog: RProg = __rx_compile(pattern);
  let first: Option[RMatch] = __rx_search_from(prog.root, text, 0, prog.ci);
  match (first) {
    None => {
      return Some(text);
    },
    _ => {}
  }
  return utf8.from_bytes(__rx_replace_all_from(prog, text, repl, first));
}

pub function regex_replace_all_bytes(pattern: string, text: string, repl: string): u8[] {
  let prog: RProg = __rx_compile(pattern);
  return __rx_replace_all_from(prog, text, repl, __rx_search_from(prog.root, text, 0, prog.ci));
}
```

### `16 function __rx_replace_all_from(prog: RProg, text: string, repl: string, first: Option[RMatch]): u8[]`

```fern
<<16 function __rx_replace_all_from(prog: RProg, text: string, repl: string, first: Option[RMatch]): u8[]>>=
function __rx_replace_all_from(prog: RProg, text: string, repl: string, first: Option[RMatch]): u8[] {
  let h: usize = buf_new(text.len());
  let last: i32 = 0;
  let next: Option[RMatch] = first;
  while (true) {
    let Some(m) = next else {
      break;
    };
    buf_push_range(h, text, last, m.start);
    buf_push(h, repl);
    last = m.end;
    let nxt: i32 = __rx_advance(m);
    if (m.end <= m.start && nxt <= text.len() && nxt > last) {
      buf_push_range(h, text, last, nxt);
      last = nxt;
    }
    if (nxt > text.len()) {
      break;
    }
    next = __rx_search_from(prog.root, text, nxt, prog.ci);
  }
  buf_push_range(h, text, last, text.len());
  return __rx_finish(h);
}
```

### `17 pub function regex_split(text: string, pattern: string): Option[string[]]`

```fern
<<17 pub function regex_split(text: string, pattern: string): Option[string[]]>>=
pub function regex_split(text: string, pattern: string): Option[string[]] {
  let prog: RProg = __rx_compile(pattern);
  let out: string[] = [];
  let last: i32 = 0;
  let pos: i32 = 0;
  while (pos <= text.len()) {
    let Some(m) = __rx_search_from(prog.root, text, pos, prog.ci) else {
      if (!utf8.is_char_boundary(text, last)) {
        return None;
      }
      out = out.append(slice_unchecked(text, last, text.len()) + "");
      return Some(out);
    };
    if (m.end > m.start) {
      if (last != m.start && (!utf8.is_char_boundary(text, last) || !utf8.is_char_boundary(text, m.start))) {
        return None;
      }
      out = out.append(slice_unchecked(text, last, m.start) + "");
      last = m.end;
      pos = m.end;
    } else {
      pos = m.start + 1;
    }
  }
  if (!utf8.is_char_boundary(text, last)) {
    return None;
  }
  out = out.append(slice_unchecked(text, last, text.len()) + "");
  return Some(out);
}

pub function regex_split_bytes(text: string, pattern: string): u8[][] {
  let prog: RProg = __rx_compile(pattern);
  let out: u8[][] = [];
  let last: i32 = 0;
  let pos: i32 = 0;
  while (pos <= text.len()) {
    let Some(m) = __rx_search_from(prog.root, text, pos, prog.ci) else {
      out = out.append(__rx_bytes(text, last, text.len()));
      return out;
    };
    if (m.end > m.start) {
      out = out.append(__rx_bytes(text, last, m.start));
      last = m.end;
      pos = m.end;
    } else {
      pos = m.start + 1;
    }
  }
  out = out.append(__rx_bytes(text, last, text.len()));
  return out;
}

pub function regex_full_match(pattern: string, text: string): boolean {
  let prog: RProg = __rx_compile(pattern);
  let ends: i32[] = __rx_match(prog.root, text, 0, prog.ci);
  let i: i32 = 0;
  while (i < ends.len()) {
    if (ends[i] == text.len()) {
      return true;
    }
    i = i + 1;
  }
  return false;
}

pub function regex_count(pattern: string, text: string): i32 {
  return regex_find_all(pattern, text).len();
}

pub function regex_replace(pattern: string, text: string, repl: string): Option[string] {
  let Some(m) = regex_search(pattern, text) else {
    return Some(text);
  };
  return utf8.from_bytes(__rx_replace_span(text, repl, m));
}
```

### `18 function __rx_replace_span(text: string, repl: string, m: RMatch): u8[]`

```fern
<<18 function __rx_replace_span(text: string, repl: string, m: RMatch): u8[]>>=
function __rx_replace_span(text: string, repl: string, m: RMatch): u8[] {
  let h: usize = buf_new(text.len());
  buf_push_range(h, text, 0, m.start);
  buf_push(h, repl);
  buf_push_range(h, text, m.end, text.len());
  return __rx_finish(h);
}
```

### `19 pub function regex_replace_bytes(pattern: string, text: string, repl: string): u8[]`

```fern
<<19 pub function regex_replace_bytes(pattern: string, text: string, repl: string): u8[]>>=
pub function regex_replace_bytes(pattern: string, text: string, repl: string): u8[] {
  let Some(m) = regex_search(pattern, text) else {
    return __rx_bytes(text, 0, text.len());
  };
  return __rx_replace_span(text, repl, m);
}
```

### `20 struct RSplit`

```fern
<<20 struct RSplit>>=
struct RSplit { a: i32, b: i32 }

enum RInst {
  IChar(i32),
  IAny,
  IClass(string),
  IAssert(i32),
  ISplit(RSplit),
  IJmp(i32),
  ISave(i32),
  IMatch,
}
```

### `21 function __rx_split(a: i32, b: i32): RInst`

```fern
<<21 function __rx_split(a: i32, b: i32): RInst>>=
function __rx_split(a: i32, b: i32): RInst {
  return ISplit(RSplit { a: a, b: b });
}

function __rx_emit(prog: RInst[], n: RNode): RInst[] {
  match (n) {
    RChar(c) => {
      return prog.append(IChar(c));
    },
    RAny => {
      return prog.append(IAny);
    },
    RClass(tok) => {
      return prog.append(IClass(tok));
    },
    RStart => {
      return prog.append(IAssert(0));
    },
    REnd => {
      return prog.append(IAssert(1));
    },
    RBound(want) => {
      if (want) {
        return prog.append(IAssert(2));
      }
      return prog.append(IAssert(3));
    },
    RGroup(gd) => {
      let pg: RInst[] = prog.append(ISave(gd.idx + gd.idx));
      pg = __rx_emit(pg, gd.node);
      return pg.append(ISave(gd.idx + gd.idx + 1));
    },
    RSeq(xs) => {
      let ps: RInst[] = prog;
      let k: i32 = 0;
      while (k < xs.len()) {
        ps = __rx_emit(ps, xs[k]);
        k = k + 1;
      }
      return ps;
    },
    RAlt(xs) => {
      let pa: RInst[] = prog;
      let jmps: i32[] = [];
      let k2: i32 = 0;
      let nb: i32 = xs.len();
      while (k2 < nb) {
        if (k2 < nb - 1) {
          let sp: i32 = pa.len();
          pa = pa.append(__rx_split(0, 0));
          pa = __rx_emit(pa, xs[k2]);
          jmps = jmps.append(pa.len());
          pa = pa.append(IJmp(0));
          pa = pa.with(sp, __rx_split(sp + 1, pa.len()));
        } else {
          pa = __rx_emit(pa, xs[k2]);
        }
        k2 = k2 + 1;
      }
      let alt_end: i32 = pa.len();
      let j: i32 = 0;
      while (j < jmps.len()) {
        pa = pa.with(jmps[j], IJmp(alt_end));
        j = j + 1;
      }
      return pa;
    },
    RStar(inner) => {
      let s1: i32 = prog.len();
      let pst: RInst[] = prog.append(__rx_split(0, 0));
      pst = __rx_emit(pst, inner);
      let s2: i32 = pst.len();
      pst = pst.append(__rx_split(0, 0));
      let send: i32 = pst.len();
      pst = pst.with(s1, __rx_split(s1 + 1, send));
      return pst.with(s2, __rx_split(s1 + 1, send));
    },
    RPlus(inner) => {
      let b1: i32 = prog.len();
      let pp: RInst[] = __rx_emit(prog, inner);
      let sp2: i32 = pp.len();
      pp = pp.append(__rx_split(0, 0));
      return pp.with(sp2, __rx_split(b1, sp2 + 1));
    },
    ROpt(inner) => {
      let s3: i32 = prog.len();
      let po: RInst[] = prog.append(__rx_split(0, 0));
      po = __rx_emit(po, inner);
      return po.with(s3, __rx_split(s3 + 1, po.len()));
    }
  }
}

function __rx_cprog(root: RNode): RInst[] {
  let prog: RInst[] = [];
  prog = prog.append(ISave(0));
  prog = __rx_emit(prog, root);
  prog = prog.append(ISave(1));
  return prog.append(IMatch);
}

function __rx_assert_ok(k: i32, t: string, ti: i32): boolean {
  if (k == 0) {
    return ti == 0;
  }
  if (k == 1) {
    return ti >= t.len();
  }
  let before: boolean = false;
  if (ti > 0) {
    before = __rx_is_word(t[ti - 1] as i32);
  }
  let after: boolean = false;
  if (ti < t.len()) {
    after = __rx_is_word(t[ti] as i32);
  }
  if (k == 2) {
    return before != after;
  }
  return before == after;
}

function __rx_fill(n: i32, v: i32): i32[] {
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < n) {
    out = out.append(v);
    i = i + 1;
  }
  return out;
}
```

### `22 struct RThread`

```fern
<<22 struct RThread>>=
struct RThread { pc: i32, caps: i32[] }

struct RAdd { seen: i32[], gen: i32, threads: RThread[] }
```

### `23 function __rx_addthread(st: RAdd, prog: RInst[], pc: i32, caps: i32[], t: string, ti: i32): RAdd`

```fern
<<23 function __rx_addthread(st: RAdd, prog: RInst[], pc: i32, caps: i32[], t: string, ti: i32): RAdd>>=
function __rx_addthread(st: RAdd, prog: RInst[], pc: i32, caps: i32[], t: string, ti: i32): RAdd {
  let sn: i32[] = st.seen;
  let g: i32 = st.gen;
  let ths: RThread[] = st.threads;
  if (sn[pc] == g) {
    return RAdd { seen: sn, gen: g, threads: ths };
  }
  sn = sn.with(pc, g);
  match (prog[pc]) {
    IJmp(a) => {
      return __rx_addthread(RAdd { seen: sn, gen: g, threads: ths }, prog, a, caps, t, ti);
    },
    ISplit(sp) => {
      let st2: RAdd = __rx_addthread(RAdd { seen: sn, gen: g, threads: ths }, prog, sp.a, caps, t, ti);
      return __rx_addthread(st2, prog, sp.b, caps, t, ti);
    },
    ISave(slot) => {
      return __rx_addthread(RAdd { seen: sn, gen: g, threads: ths }, prog, pc + 1, caps.with(slot, ti), t, ti);
    },
    IAssert(k) => {
      if (__rx_assert_ok(k, t, ti)) {
        return __rx_addthread(RAdd { seen: sn, gen: g, threads: ths }, prog, pc + 1, caps, t, ti);
      }
      return RAdd { seen: sn, gen: g, threads: ths };
    },
    _ => {
      return RAdd { seen: sn, gen: g, threads: ths.append(RThread { pc: pc, caps: caps }) };
    }
  }
}

function __rx_cvm_at(prog: RInst[], t: string, ti0: i32, ci: boolean, init: i32[]): i32[] {
  let seen: i32[] = __rx_fill(prog.len(), 0);
  let gen: i32 = 1;
  let st: RAdd = __rx_addthread(RAdd { seen: seen, gen: gen, threads: [] }, prog, 0, init, t, ti0);
  let clist: RThread[] = st.threads;
  seen = st.seen;
  let best: i32[] = [];
  let pos: i32 = ti0;
  while (clist.len() > 0) {
    gen = gen + 1;
    let nst: RAdd = RAdd { seen: seen, gen: gen, threads: [] };
    let k: i32 = 0;
    while (k < clist.len()) {
      let th: RThread = clist[k];
      match (prog[th.pc]) {
        IChar(c) => {
          if (pos < t.len() && __rx_fold(t[pos] as i32, ci) == __rx_fold(c, ci)) {
            nst = __rx_addthread(nst, prog, th.pc + 1, th.caps, t, pos + 1);
          }
        },
        IAny => {
          if (pos < t.len()) {
            nst = __rx_addthread(nst, prog, th.pc + 1, th.caps, t, pos + 1);
          }
        },
        IClass(tok) => {
          if (pos < t.len() && __rx_class(tok, t[pos] as i32, ci)) {
            nst = __rx_addthread(nst, prog, th.pc + 1, th.caps, t, pos + 1);
          }
        },
        IMatch => {
          if (best.len() == 0 || pos > best[1]) {
            best = th.caps;
          }
        },
        IAssert(_) => {},
        ISplit(_) => {},
        IJmp(_) => {},
        ISave(_) => {}
      }
      k = k + 1;
    }
    clist = nst.threads;
    seen = nst.seen;
    if (pos >= t.len()) {
      clist = [];
    }
    pos = pos + 1;
  }
  return best;
}

function __rx_csearch_from(prog: RInst[], t: string, from: i32, ci: boolean, nslots: i32): i32[] {
  let init: i32[] = __rx_fill(nslots, 0 - 1);
  let ti: i32 = from;
  while (ti <= t.len()) {
    let caps: i32[] = __rx_cvm_at(prog, t, ti, ci, init);
    if (caps.len() > 0) {
      return caps;
    }
    ti = ti + 1;
  }
  return [];
}
```

### `24 pub struct RCaps`

```fern
<<24 pub struct RCaps>>=
pub struct RCaps { text: string, starts: i32[], ends: i32[], names: string[] }
```

### `25 pub function (c: RCaps) group_count(): i32`

```fern
<<25 pub function (c: RCaps) group_count(): i32>>=
pub function (c: RCaps) group_count(): i32 {
  return c.starts.len() - 1;
}

pub function (c: RCaps) group_start(i: i32): Option[i32] {
  if (i < 0 || i >= c.starts.len() || c.starts[i] < 0) {
    return None;
  }
  return Some(c.starts[i]);
}

pub function (c: RCaps) group_end(i: i32): Option[i32] {
  if (i < 0 || i >= c.ends.len() || c.ends[i] < 0) {
    return None;
  }
  return Some(c.ends[i]);
}

pub function (c: RCaps) has_group(i: i32): boolean {
  if let Some(_) = c.group_start(i) {
    return true;
  }
  return false;
}
```

### `26 function __rx_span(c: RCaps, i: i32): (i32, i32)`

```fern
<<26 function __rx_span(c: RCaps, i: i32): (i32, i32)>>=
function __rx_span(c: RCaps, i: i32): (i32, i32) {
  if (i < 0 || i >= c.starts.len() || c.starts[i] < 0 || c.ends[i] < 0) {
    return (0 - 1, 0 - 1);
  }
  return (c.starts[i], c.ends[i]);
}
```

### `27 pub function (c: RCaps) group(i: i32): Option[string]`

```fern
<<27 pub function (c: RCaps) group(i: i32): Option[string]>>=
pub function (c: RCaps) group(i: i32): Option[string] {
  let (s, e) = __rx_span(c, i);
  if (s < 0) {
    return Some("");
  }
  if (s != e && (!utf8.is_char_boundary(c.text, s) || !utf8.is_char_boundary(c.text, e))) {
    return None;
  }
  return Some(slice_unchecked(c.text, s, e) + "");
}

pub function (c: RCaps) group_bytes(i: i32): u8[] {
  let (s, e) = __rx_span(c, i);
  if (s < 0) {
    return [];
  }
  return __rx_bytes(c.text, s, e);
}

pub function (c: RCaps) group_index(name: string): Option[i32] {
  let i: i32 = 0;
  while (i < c.names.len()) {
    if (c.names[i] == name) {
      return Some(i + 1);
    }
    i = i + 1;
  }
  return None;
}
```

### `28 function __rx_named_index(c: RCaps, name: string): i32`

```fern
<<28 function __rx_named_index(c: RCaps, name: string): i32>>=
function __rx_named_index(c: RCaps, name: string): i32 {
  if let Some(i) = c.group_index(name) {
    return i;
  }
  return 0 - 1;
}
```

### `29 pub function (c: RCaps) group_named(name: string): Option[string]`

```fern
<<29 pub function (c: RCaps) group_named(name: string): Option[string]>>=
pub function (c: RCaps) group_named(name: string): Option[string] {
  return c.group(__rx_named_index(c, name));
}

pub function (c: RCaps) group_named_bytes(name: string): u8[] {
  return c.group_bytes(__rx_named_index(c, name));
}

pub function (c: RCaps) group_named_start(name: string): Option[i32] {
  return c.group_start(__rx_named_index(c, name));
}

pub function (c: RCaps) group_named_end(name: string): Option[i32] {
  return c.group_end(__rx_named_index(c, name));
}

pub function (c: RCaps) has_group_named(name: string): boolean {
  return c.has_group(__rx_named_index(c, name));
}
```

### `30 function __rx_caps_wrap(caps: i32[], text: string, ngroups: i32, names: string[]): Option[RCaps]`

```fern
<<30 function __rx_caps_wrap(caps: i32[], text: string, ngroups: i32, names: string[]): Option[RCaps]>>=
function __rx_caps_wrap(caps: i32[], text: string, ngroups: i32, names: string[]): Option[RCaps] {
  if (caps.len() == 0) {
    return None;
  }
  let starts: i32[] = [];
  let ends: i32[] = [];
  let i: i32 = 0;
  while (i <= ngroups) {
    starts = starts.append(caps[i + i]);
    ends = ends.append(caps[i + i + 1]);
    i = i + 1;
  }
  return Some(RCaps { text: text, starts: starts, ends: ends, names: names });
}
```

### `31 pub function regex_captures(pattern: string, text: string): Option[RCaps]`

```fern
<<31 pub function regex_captures(pattern: string, text: string): Option[RCaps]>>=
pub function regex_captures(pattern: string, text: string): Option[RCaps] {
  let prog: RProg = __rx_compile(pattern);
  let insts: RInst[] = __rx_cprog(prog.root);
  let nslots: i32 = (prog.ngroups + 1) * 2;
  let caps: i32[] = __rx_csearch_from(insts, text, 0, prog.ci, nslots);
  return __rx_caps_wrap(caps, text, prog.ngroups, prog.names);
}

pub function regex_captures_all(pattern: string, text: string): RCaps[] {
  let prog: RProg = __rx_compile(pattern);
  let insts: RInst[] = __rx_cprog(prog.root);
  let nslots: i32 = (prog.ngroups + 1) * 2;
  let out: RCaps[] = [];
  let pos: i32 = 0;
  while (pos <= text.len()) {
    let caps: i32[] = __rx_csearch_from(insts, text, pos, prog.ci, nslots);
    let Some(c) = __rx_caps_wrap(caps, text, prog.ngroups, prog.names) else {
      return out;
    };
    out = out.append(c);
    pos = __rx_advance(RMatch { start: caps[0], end: caps[1] });
  }
  return out;
}
```

### `32 function __rx_template_ref(tmpl: string, i: i32, c: RCaps): (i32, i32)`

```fern
<<32 function __rx_template_ref(tmpl: string, i: i32, c: RCaps): (i32, i32)>>=
function __rx_template_ref(tmpl: string, i: i32, c: RCaps): (i32, i32) {
  if (i + 1 >= tmpl.len()) {
    return (0 - 1, i);
  }
  let d: i32 = tmpl[i + 1] as i32;
  if (d == 36) {
    return (0 - 2, i + 2);
  }
  if (d >= 48 && d <= 57) {
    return (d - 48, i + 2);
  }
  if (d == 123) {
    let ne: i32 = i + 2;
    while (ne < tmpl.len() && tmpl[ne] != 125) {
      ne = ne + 1;
    }
    if (ne < tmpl.len()) {
      return (__rx_named_index(c, slice_unchecked(tmpl, i + 2, ne) + ""), ne + 1);
    }
  }
  return (0 - 1, i);
}

function __rx_expand_into(h: usize, tmpl: string, c: RCaps): void {
  let lit: i32 = 0;
  let i: i32 = 0;
  while (i < tmpl.len()) {
    if (tmpl[i] != 36) {
      i = i + 1;
      continue;
    }
    let ref: (i32, i32) = __rx_template_ref(tmpl, i, c);
    if (ref.1 == i) {
      i = i + 1;
      continue;
    }
    buf_push_range(h, tmpl, lit, i);
    if (ref.0 == 0 - 2) {
      buf_push_byte(h, 36);
    } else {
      let (lo, hi) = __rx_span(c, ref.0);
      if (lo >= 0) {
        buf_push_range(h, c.text, lo, hi);
      }
    }
    i = ref.1;
    lit = i;
  }
  buf_push_range(h, tmpl, lit, tmpl.len());
}
```

### `33 pub function regex_replace_groups(pattern: string, text: string, tmpl: string): Option[string]`

```fern
<<33 pub function regex_replace_groups(pattern: string, text: string, tmpl: string): Option[string]>>=
pub function regex_replace_groups(pattern: string, text: string, tmpl: string): Option[string] {
  let Some(c) = regex_captures(pattern, text) else {
    return Some(text);
  };
  return utf8.from_bytes(__rx_replace_capture(text, tmpl, c));
}

pub function regex_replace_groups_bytes(pattern: string, text: string, tmpl: string): u8[] {
  let Some(c) = regex_captures(pattern, text) else {
    return __rx_bytes(text, 0, text.len());
  };
  return __rx_replace_capture(text, tmpl, c);
}
```

### `34 function __rx_replace_capture(text: string, tmpl: string, c: RCaps): u8[]`

```fern
<<34 function __rx_replace_capture(text: string, tmpl: string, c: RCaps): u8[]>>=
function __rx_replace_capture(text: string, tmpl: string, c: RCaps): u8[] {
  let h: usize = buf_new(text.len());
  buf_push_range(h, text, 0, c.starts[0]);
  __rx_expand_into(h, tmpl, c);
  buf_push_range(h, text, c.ends[0], text.len());
  return __rx_finish(h);
}
```

### `35 pub function regex_replace_all_groups(pattern: string, text: string, tmpl: string): Option[string]`

```fern
<<35 pub function regex_replace_all_groups(pattern: string, text: string, tmpl: string): Option[string]>>=
pub function regex_replace_all_groups(pattern: string, text: string, tmpl: string): Option[string] {
  let all: RCaps[] = regex_captures_all(pattern, text);
  if (all.len() == 0) {
    return Some(text);
  }
  return utf8.from_bytes(__rx_replace_captures(text, tmpl, all));
}

pub function regex_replace_all_groups_bytes(pattern: string, text: string, tmpl: string): u8[] {
  let all: RCaps[] = regex_captures_all(pattern, text);
  return __rx_replace_captures(text, tmpl, all);
}
```

### `36 function __rx_replace_captures(text: string, tmpl: string, all: RCaps[]): u8[]`

```fern
<<36 function __rx_replace_captures(text: string, tmpl: string, all: RCaps[]): u8[]>>=
function __rx_replace_captures(text: string, tmpl: string, all: RCaps[]): u8[] {
  let h: usize = buf_new(text.len());
  let last: i32 = 0;
  let mi: i32 = 0;
  while (mi < all.len()) {
    let m: RCaps = all[mi];
    buf_push_range(h, text, last, m.starts[0]);
    __rx_expand_into(h, tmpl, m);
    last = m.ends[0];
    mi = mi + 1;
  }
  buf_push_range(h, text, last, text.len());
  return __rx_finish(h);
}
```
