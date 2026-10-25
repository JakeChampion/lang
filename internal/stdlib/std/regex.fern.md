# Regular expression evaluation

This module evaluates regular expressions.

## Model

A compiled expression retains its syntax tree, case policy, and capture metadata. Match values identify input spans.

## Contracts

Search and complete-match operations have different purposes. Compilation returns a typed error for invalid patterns. Text output methods report invalid UTF-8 through a typed error. Byte output methods preserve raw bytes.

## Executable example

```fern test name=std-regex-example
import "std/regex";

function main(): i32 {
  let Ok(prog) = regex.compile("a+b") else { return 1; };
  if (!prog.is_match("aaab") || prog.is_match("ac")) { return 2; }
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


<<7 function __rx_group_names(pattern: string): string[]>>

<<8 function __rx_match(n: RNode, t: string, ti: i32, ci: boolean): i32[]>>


<<10 function __rx_search_from(root: RNode, text: string, from: i32, ci: boolean): Option[RMatch]>>


<<12 function __rx_advance(m: RMatch): i32>>


<<14 function __rx_finish(h: usize): u8[]>>


<<16 function __rx_replace_all_from(prog: RProg, text: string, repl: string, first: Option[RMatch]): u8[]>>


<<18 function __rx_replace_span(text: string, repl: string, m: RMatch): u8[]>>


<<20 struct RSplit>>

<<21 function __rx_split(a: i32, b: i32): RInst>>

<<22 struct RThread>>

<<23 function __rx_addthread(st: RAdd, prog: RInst[], pc: i32, caps: i32[], t: string, ti: i32): RAdd>>



<<26 function __rx_span(c: RCaps, i: i32): (i32, i32)>>


<<28 function __rx_named_index(c: RCaps, name: string): i32>>


<<30 function __rx_caps_wrap(caps: i32[], text: string, ngroups: i32, names: string[]): Option[RCaps]>>


<<32 function __rx_template_ref(tmpl: string, i: i32, c: RCaps): (i32, i32)>>


<<34 function __rx_replace_capture(text: string, tmpl: string, c: RCaps): u8[]>>


<<36 function __rx_replace_captures(text: string, tmpl: string, all: RCaps[]): u8[]>>
<<typed decode errors>>
```


### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/cmp";
import "std/error";
import "std/i32";
import "std/string";
import "std/utf8" as utf8;
```

### `3 struct RGroupData`

```fern
<<3 struct RGroupData>>=
struct RGroupData { idx: i32, node: RNode }

pub enum RNode {
  RChar(i32),
  RAny,
  RClass(u8[]),
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
function __rx_atom(errors: Cell[Option[CompileError]], depth: i32, p: string, i: i32, g: i32): RParse {
  let c: i32 = p[i] as i32;
  if (c == 92) {
    return __rx_escape(errors, p, i, g);
  }
  if (c == 42 || c == 43 || c == 63 || c == 123) {
    return __rx_fail(errors, MissingAtom, i, p.len(), g);
  }
  if (c == 40) {
    return __rx_group(errors, depth, p, i, g);
  }
  if (c == 91) {
    let Some(end) = __rx_class_end(p, i) else {
      return __rx_fail(errors, UnclosedClass, i, p.len(), g);
    };
    return RParse { node: RClass(__rx_mask(slice_unchecked(p, i, end + 1) + "", errors, i)), pos: end + 1, g: g };
  }
  if (c == 46) {  // '.'
    return RParse { node: RAny, pos: i + 1, g: g };
  }
  if (c == 94) {  // '^'
    return RParse { node: RStart, pos: i + 1, g: g };
  }
  if (c == 36) {  // '$'
    return RParse { node: REnd, pos: i + 1, g: g };
  }
  return RParse { node: RChar(c), pos: i + 1, g: g };  // literal
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
function __rx_count(errors: Cell[Option[CompileError]], depth: i32, p: string, pos: i32, a: RParse): RParse {
  let j: i32 = pos + 1;
  let n: i32 = 0;
  let have_n: boolean = false;
  while (j < p.len() && p[j] as i32 >= 48 && p[j] as i32 <= 57) {
    let digit: i32 = p[j] as i32 - 48;
    if (n > MAX_REPEAT / 10 || n == MAX_REPEAT / 10 && digit > MAX_REPEAT % 10) {
      return __rx_fail(errors, RepeatLimit, pos, p.len(), a.g);
    }
    n = n * 10 + digit;
    j = j + 1;
    have_n = true;
  }
  if (!have_n) {
    return __rx_fail(errors, InvalidRepeat, pos, p.len(), a.g);
  }
  let m: i32 = n;
  let has_max: boolean = true;
  if (j < p.len() && p[j] == 44) {  // ','
    j = j + 1;
    if (j < p.len() && p[j] == 125) {  // `{n,}`
      has_max = false;
    } else {
      m = 0;
      let have_m: boolean = false;
      while (j < p.len() && p[j] as i32 >= 48 && p[j] as i32 <= 57) {
        let digit: i32 = p[j] as i32 - 48;
        if (m > MAX_REPEAT / 10 || m == MAX_REPEAT / 10 && digit > MAX_REPEAT % 10) {
          return __rx_fail(errors, RepeatLimit, pos, p.len(), a.g);
        }
        m = m * 10 + digit;
        have_m = true;
        j = j + 1;
      }
      if (!have_m) {
        return __rx_fail(errors, InvalidRepeat, pos, p.len(), a.g);
      }
    }
  }
  if (j >= p.len() || p[j] != 125) {
    return __rx_fail(errors, InvalidRepeat, pos, p.len(), a.g);
  }
  if (has_max && m < n) {
    return __rx_fail(errors, InvalidRepeat, pos, p.len(), a.g);
  }
  return RParse { node: __rx_repeat(a.node, n, m, has_max), pos: j + 1, g: a.g };
}

function __rx_quant(errors: Cell[Option[CompileError]], depth: i32, p: string, i: i32, g: i32): RParse {
  let a: RParse = __rx_atom(errors, depth, p, i, g);
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
    if (c == 123) {  // '{'
      return __rx_count(errors, depth, p, pos, a);
    }
  }
  return a;
}

function __rx_seq(errors: Cell[Option[CompileError]], depth: i32, p: string, i: i32, g: i32): RParse {
  let items: RNode[] = [];
  let pos: i32 = i;
  let gi: i32 = g;
  while (pos < p.len() && p[pos] != 124 && p[pos] != 41) {  // not '|' / ')'
    let q: RParse = __rx_quant(errors, depth, p, pos, gi);
    items = items.append(q.node);
    pos = q.pos;
    gi = q.g;
  }
  if (items.len() == 1) {
    return RParse { node: items[0], pos: pos, g: gi };
  }
  return RParse { node: RSeq(items), pos: pos, g: gi };
}

function __rx_alt(errors: Cell[Option[CompileError]], depth: i32, p: string, i: i32, g: i32): RParse {
  let first: RParse = __rx_seq(errors, depth, p, i, g);
  let branches: RNode[] = [first.node];
  let pos: i32 = first.pos;
  let gi: i32 = first.g;
  while (pos < p.len() && p[pos] == 124) {  // '|'
    let nxt: RParse = __rx_seq(errors, depth, p, pos + 1, gi);
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


### `7 function __rx_group_names(pattern: string): string[]`

```fern
<<7 function __rx_group_names(pattern: string): string[]>>=
function __rx_group_names(pattern: string): string[] {
  let names: string[] = [];
  let i: i32 = 0;
  let n: i32 = pattern.len();
  while (i < n) {
    let c: i32 = pattern[i] as i32;
    if (c == 92) {  // '\' escape — skip next byte
      i = i + 2;
    } else if (c == 91) {  // '[' character class
      match (__rx_class_end(pattern, i)) {
        Some(end) => {
          i = end + 1;
        },
        None => {
          i = n;
        }
      }
    } else if (c == 40) {  // '('
      if (i + 1 < n && pattern[i + 1] == 63) {  // '(?'
        if (i + 3 < n && pattern[i + 2] == 60
          && pattern[i + 3] != 61 && pattern[i + 3] != 33) {  // '(?<' name
          let ne: i32 = i + 3;
          while (ne < n && pattern[ne] != 62) {  // to '>'
            ne = ne + 1;
          }
          if (ne < n) {  // named capturing group
            names = names.append(slice_unchecked(pattern, i + 3, ne) + "");
            i = ne + 1;
          } else {
            i = i + 1;  // `(?<…` no '>' → non-capturing
          }
        } else {
          i = i + 1;  // `(?:` / `(?i)` / … → non-capturing
        }
      } else {
        names = names.append("");  // bare capturing group
        i = i + 1;
      }
    } else {
      i = i + 1;
    }
  }
  return names;
}

function __rx_class(mask: u8[], byte: i32, ci: boolean): boolean {
  let at: i32 = byte;
  if (ci) {
    at = at + 256;
  }
  return mask[at] != 0;
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


### `10 function __rx_search_from(root: RNode, text: string, from: i32, ci: boolean): Option[RMatch]`

```fern
<<10 function __rx_search_from(root: RNode, text: string, from: i32, ci: boolean): Option[RMatch]>>=
function __rx_search_from(root: RNode, text: string, from: i32, ci: boolean): Option[Match] {
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
      return Some(Match { start: ti, end: best });
    }
    if (ti >= text.len()) {
      return None;
    }
    ti = ti + 1;
  }
  return None;
}
```


### `12 function __rx_advance(m: RMatch): i32`

```fern
<<12 function __rx_advance(m: RMatch): i32>>=
function __rx_advance(m: Match): i32 {
  if (m.end > m.start) {
    return m.end;
  }
  return m.start + 1;
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


### `16 function __rx_replace_all_from(prog: RProg, text: string, repl: string, first: Option[RMatch]): u8[]`

```fern
<<16 function __rx_replace_all_from(prog: RProg, text: string, repl: string, first: Option[RMatch]): u8[]>>=
function __rx_replace_all_from(prog: Regex, text: string, repl: string, first: Option[Match]): u8[] {
  let h: usize = buf_new(text.len());
  let last: i32 = 0;
  let next: Option[Match] = first;
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


### `18 function __rx_replace_span(text: string, repl: string, m: RMatch): u8[]`

```fern
<<18 function __rx_replace_span(text: string, repl: string, m: RMatch): u8[]>>=
function __rx_replace_span(text: string, repl: string, m: Match): u8[] {
  let h: usize = buf_new(text.len());
  buf_push_range(h, text, 0, m.start);
  buf_push(h, repl);
  buf_push_range(h, text, m.end, text.len());
  return __rx_finish(h);
}
```


### `20 struct RSplit`

```fern
<<20 struct RSplit>>=
struct RSplit { a: i32, b: i32 }

enum RInst {
  IChar(i32),
  IAny,
  IClass(u8[]),
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



### `26 function __rx_span(c: RCaps, i: i32): (i32, i32)`

```fern
<<26 function __rx_span(c: RCaps, i: i32): (i32, i32)>>=
function __rx_span(c: Captures, i: i32): (i32, i32) {
  if (i < 0 || i >= c.starts.len() || c.starts[i] < 0 || c.ends[i] < 0) {
    return (0 - 1, 0 - 1);
  }
  return (c.starts[i], c.ends[i]);
}
```


### `28 function __rx_named_index(c: RCaps, name: string): i32`

```fern
<<28 function __rx_named_index(c: RCaps, name: string): i32>>=
function __rx_named_index(c: Captures, name: string): i32 {
  if let Some(i) = c.group_index(name) {
    return i;
  }
  return 0 - 1;
}
```


### `30 function __rx_caps_wrap(caps: i32[], text: string, ngroups: i32, names: string[]): Option[RCaps]`

```fern
<<30 function __rx_caps_wrap(caps: i32[], text: string, ngroups: i32, names: string[]): Option[RCaps]>>=
function __rx_caps_wrap(caps: i32[], text: string, ngroups: i32, names: string[]): Option[Captures] {
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
  return Some(Captures { text: text, starts: starts, ends: ends, names: names });
}
```


### `32 function __rx_template_ref(tmpl: string, i: i32, c: RCaps): (i32, i32)`

```fern
<<32 function __rx_template_ref(tmpl: string, i: i32, c: RCaps): (i32, i32)>>=
function __rx_template_ref(tmpl: string, i: i32, c: Captures): (i32, i32) {
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

function __rx_expand_into(h: usize, tmpl: string, c: Captures): void {
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


### `34 function __rx_replace_capture(text: string, tmpl: string, c: RCaps): u8[]`

```fern
<<34 function __rx_replace_capture(text: string, tmpl: string, c: RCaps): u8[]>>=
function __rx_replace_capture(text: string, tmpl: string, c: Captures): u8[] {
  let h: usize = buf_new(text.len());
  buf_push_range(h, text, 0, c.starts[0]);
  __rx_expand_into(h, tmpl, c);
  buf_push_range(h, text, c.ends[0], text.len());
  return __rx_finish(h);
}
```


### `36 function __rx_replace_captures(text: string, tmpl: string, all: RCaps[]): u8[]`

```fern
<<36 function __rx_replace_captures(text: string, tmpl: string, all: RCaps[]): u8[]>>=
function __rx_replace_captures(text: string, tmpl: string, all: Captures[]): u8[] {
  let h: usize = buf_new(text.len());
  let last: i32 = 0;
  let mi: i32 = 0;
  while (mi < all.len()) {
    let m: Captures = all[mi];
    buf_push_range(h, text, last, m.starts[0]);
    __rx_expand_into(h, tmpl, m);
    last = m.ends[0];
    mi = mi + 1;
  }
  buf_push_range(h, text, last, text.len());
  return __rx_finish(h);
}
```

### Typed decode errors

```fern
<<typed decode errors>>=
pub const MAX_NESTING: i32 = 128;

pub const MAX_PROGRAM: i32 = 100000;

pub const MAX_REPEAT: i32 = 1000;

@derive(cmp.Eq)
pub enum CompileErrorKind {
  TrailingEscape,
  UnclosedGroup,
  UnclosedClass,
  UnexpectedClose,
  MissingAtom,
  InvalidRepeat,
  RepeatLimit,
  UnsupportedGroup,
  NestingLimit,
  InvalidClassRange,
  ProgramLimit,
}

pub enum TextError { InvalidUtf8Output }

function __rx_child_size(child: RNode, overhead: i32, budget: i32): i32 {
  let size: i32 = __rx_program_size(child, budget - overhead);
  if (size < 0) {
    return -1;
  }
  return overhead + size;
}

function __rx_class_end(pattern: string, start: i32): Option[i32] {
  let i: i32 = start + 1;
  if (i < pattern.len() && pattern[i] == 94) {
    i = i + 1;
  }
  if (i < pattern.len() && pattern[i] == 93) {
    i = i + 1;
  }
  while (i < pattern.len()) {
    if (pattern[i] == 92) {
      i = i + 2;
    } else if (pattern[i] == 93) {
      return Some(i);
    } else {
      i = i + 1;
    }
  }
  return None;
}

function __rx_class_unit(token: string, at: i32): (i32, i32, i32) {
  let byte: i32 = token[at] as i32;
  if (byte != 92) {
    return (0, byte, at + 1);
  }
  byte = token[at + 1] as i32;
  if (byte == 100) {
    return (1, 0, at + 2);
  }
  if (byte == 68) {
    return (-1, 0, at + 2);
  }
  if (byte == 119) {
    return (2, 0, at + 2);
  }
  if (byte == 87) {
    return (-2, 0, at + 2);
  }
  if (byte == 115) {
    return (3, 0, at + 2);
  }
  if (byte == 83) {
    return (-3, 0, at + 2);
  }
  if (byte == 110) {
    byte = 10;
  } else if (byte == 116) {
    byte = 9;
  } else if (byte == 114) {
    byte = 13;
  }
  return (0, byte, at + 2);
}

function __rx_class_units(token: string, errors: Cell[Option[CompileError]], offset: i32): RClassUnit[] {
  let out: RClassUnit[] = [];
  let i: i32 = 1;
  if (token[i] == 94) {
    i = i + 1;
  }
  let end: i32 = token.len() - 1;
  while (i < end) {
    let (kind, lo, next) = __rx_class_unit(token, i);
    if (next + 1 < end && token[next] == 45) {
      let (upper_kind, hi, after) = __rx_class_unit(token, next + 1);
      if (kind != 0 || upper_kind != 0 || hi < lo) {
        __rx_fail(errors, InvalidClassRange, offset + i, offset + token.len(), 0);
        return [];
      }
      out = out.append(RClassUnit { kind: 0, lo: lo, hi: hi });
      i = after;
    } else {
      out = out.append(RClassUnit { kind: kind, lo: lo, hi: lo });
      i = next;
    }
  }
  return out;
}

function __rx_escape(errors: Cell[Option[CompileError]], p: string, i: i32, g: i32): RParse {
  // '\' escape
  if (i + 1 >= p.len()) {
    return __rx_fail(errors, TrailingEscape, i, p.len(), g);
  }
  let d: i32 = p[i + 1] as i32;
  // Shorthand classes (and their negations) desugar to an equivalent
  // `[ … ]` class token the matcher already understands.
  if (d == 100) {  // \d
    return RParse { node: RClass(__rx_mask("[0-9]", errors, i)), pos: i + 2, g: g };
  }
  if (d == 68) {  // \D
    return RParse { node: RClass(__rx_mask("[^0-9]", errors, i)), pos: i + 2, g: g };
  }
  if (d == 119) {  // \w
    return RParse { node: RClass(__rx_mask("[A-Za-z0-9_]", errors, i)), pos: i + 2, g: g };
  }
  if (d == 87) {  // \W
    return RParse { node: RClass(__rx_mask("[^A-Za-z0-9_]", errors, i)), pos: i + 2, g: g };
  }
  if (d == 115) {  // \s
    return RParse { node: RClass(__rx_mask("[ \t\n\r]", errors, i)), pos: i + 2, g: g };
  }
  if (d == 83) {  // \S
    return RParse { node: RClass(__rx_mask("[^ \t\n\r]", errors, i)), pos: i + 2, g: g };
  }
  // Word-boundary assertions (zero-width).
  if (d == 98) {  // \b
    return RParse { node: RBound(true), pos: i + 2, g: g };
  }
  if (d == 66) {  // \B
    return RParse { node: RBound(false), pos: i + 2, g: g };
  }
  // Escaped control bytes.
  if (d == 110) {  // \n
    return RParse { node: RChar(10), pos: i + 2, g: g };
  }
  if (d == 116) {  // \t
    return RParse { node: RChar(9), pos: i + 2, g: g };
  }
  if (d == 114) {  // \r
    return RParse { node: RChar(13), pos: i + 2, g: g };
  }
  // Any other escaped byte is that literal byte — covers the
  // metacharacters `\. \* \+ \? \( \) \[ \] \| \^ \$ \\` etc.
  // NOTE: this includes `\1`…`\9` — they are literal digit bytes,
  // NOT backreferences (which this engine does not support).
  return RParse { node: RChar(d), pos: i + 2, g: g };
}

function __rx_fail(errors: Cell[Option[CompileError]], kind: CompileErrorKind, offset: i32, end: i32, g: i32): RParse {
  if let None = errors.get() {
    errors.set(Some(CompileError { kind: kind, offset: offset }));
  }
  return RParse { node: RSeq([]), pos: end, g: g };
}

function __rx_group(errors: Cell[Option[CompileError]], depth: i32, p: string, i: i32, g: i32): RParse {
  // '('
  if (depth >= MAX_NESTING) {
    return __rx_fail(errors, NestingLimit, i, p.len(), g);
  }
  // Only named captures and non-capturing groups use the `(?` prefix.
  if (i + 1 < p.len() && p[i + 1] == 63) {  // '?'
    if (i + 3 < p.len() && p[i + 2] == 60
      && p[i + 3] != 61 && p[i + 3] != 33) {  // '?' '<' !'=' !'!'
      let ne: i32 = i + 3;
      while (ne < p.len() && p[ne] != 62) {  // scan to '>'
        ne = ne + 1;
      }
      if (ne < p.len()) {  // found '>'
        let gi: RParse = __rx_alt(errors, depth + 1, p, ne + 1, g + 1);
        let gpos: i32 = gi.pos;
        if (gpos < p.len() && p[gpos] == 41) {  // ')'
          gpos = gpos + 1;
        } else {
          return __rx_fail(errors, UnclosedGroup, i, p.len(), g);
        }
        return RParse { node: RGroup(RGroupData { idx: g, node: gi.node }), pos: gpos, g: gi.g };
      }
    }
    if (i + 2 >= p.len() || p[i + 2] != 58) {
      return __rx_fail(errors, UnsupportedGroup, i, p.len(), g);
    }
    let nb: i32 = i + 3;
    let ni: RParse = __rx_alt(errors, depth + 1, p, nb, g);
    let npos: i32 = ni.pos;
    if (npos < p.len() && p[npos] == 41) {  // ')'
      npos = npos + 1;
    } else {
      return __rx_fail(errors, UnclosedGroup, i, p.len(), g);
    }
    return RParse { node: ni.node, pos: npos, g: ni.g };
  }
  // `( … )` — capturing: claim index `g`, then parse the body (which
  // numbers any nested groups after this one, left to right).
  let inner: RParse = __rx_alt(errors, depth + 1, p, i + 1, g + 1);
  let pos: i32 = inner.pos;
  if (pos < p.len() && p[pos] == 41) {  // ')'
    pos = pos + 1;
  } else {
    return __rx_fail(errors, UnclosedGroup, i, p.len(), g);
  }
  return RParse { node: RGroup(RGroupData { idx: g, node: inner.node }), pos: pos, g: inner.g };
}

function __rx_mark_range(own mask: u8[], lo: i32, hi: i32, value: u8): u8[] {
  let byte: i32 = lo;
  while (byte <= hi) {
    mask = mask.with(byte, value);
    mask = mask.with(byte + 256, value);
    if (byte >= 65 && byte <= 90) {
      mask = mask.with(byte + 32 + 256, value);
    } else if (byte >= 97 && byte <= 122) {
      mask = mask.with(byte - 32 + 256, value);
    }
    byte = byte + 1;
  }
  return mask;
}

function __rx_mask(token: string, errors: Cell[Option[CompileError]], offset: i32): u8[] {
  let units = __rx_class_units(token, errors, offset);
  let out: u8[] = __alloc_u8(512);
  let fill: u8 = if (token[1] == 94) { 1 } else { 0 };
  let value: u8 = 1 - fill;
  if (fill != 0) {
    for i in 0..512 {
      out = out.with(i, fill);
    }
  }
  for unit in units {
    if (unit.kind == 0) {
      out = __rx_mark_range(out, unit.lo, unit.hi, value);
    } else {
      for byte in 0..256 {
        if (__rx_shorthand_matches(unit.kind, byte)) {
          out = out.with(byte, value);
          out = out.with(byte + 256, value);
        }
      }
    }
  }
  return out;
}

function __rx_nodes_size(nodes: RNode[], overhead: i32, budget: i32): i32 {
  let total: i32 = overhead;
  if (total > budget) {
    return -1;
  }
  for child in nodes {
    let size: i32 = __rx_program_size(child, budget - total);
    if (size < 0) {
      return -1;
    }
    total = total + size;
  }
  return total;
}

function __rx_program_size(node: RNode, budget: i32): i32 {
  if (budget < 0) {
    return -1;
  }
  match (node) {
    RSeq(nodes) => {
      return __rx_nodes_size(nodes, 0, budget);
    },
    RAlt(nodes) => {
      let overhead: i32 = 0;
      if (nodes.len() > 0) {
        overhead = (nodes.len() - 1) * 2;
      }
      return __rx_nodes_size(nodes, overhead, budget);
    },
    RGroup(group) => {
      return __rx_child_size(group.node, 2, budget);
    },
    RStar(child) => {
      return __rx_child_size(child, 2, budget);
    },
    RPlus(child) => {
      return __rx_child_size(child, 1, budget);
    },
    ROpt(child) => {
      return __rx_child_size(child, 1, budget);
    },
    _ => {
      if (budget < 1) {
        return -1;
      }
      return 1;
    }
  }
}

function __rx_shorthand_matches(kind: i32, byte: i32): boolean {
  let negated: boolean = kind < 0;
  if (negated) {
    kind = -kind;
  }
  let found: boolean = false;
  if (kind == 1) {
    found = byte >= 48 && byte <= 57;
  } else if (kind == 2) {
    found = __rx_is_word(byte);
  } else {
    found = byte == 32 || byte == 9 || byte == 10 || byte == 13;
  }
  if (negated) {
    return !found;
  }
  return found;
}

function __rx_text(bytes: u8[]): Result[string, TextError] {
  match (utf8.from_bytes(bytes)) {
    Some(text) => {
      return Ok(text);
    },
    None => {
      return Err(InvalidUtf8Output);
    }
  }
  return Err(InvalidUtf8Output);
}

pub function (prog: Regex) captures(text: string): Option[Captures] {
  let insts: RInst[] = prog.program;
  let nslots: i32 = (prog.ngroups + 1) * 2;
  let caps: i32[] = __rx_csearch_from(insts, text, 0, prog.ci, nslots);
  return __rx_caps_wrap(caps, text, prog.ngroups, prog.names);
}

pub function (prog: Regex) captures_all(text: string): Captures[] {
  let insts: RInst[] = prog.program;
  let nslots: i32 = (prog.ngroups + 1) * 2;
  let out: Captures[] = [];
  let pos: i32 = 0;
  while (pos <= text.len()) {
    let caps: i32[] = __rx_csearch_from(insts, text, pos, prog.ci, nslots);
    let Some(c) = __rx_caps_wrap(caps, text, prog.ngroups, prog.names) else {
      return out;
    };
    out = out.append(c);
    // Slots 0/1 are the whole-match span; advance through the shared
    // helper so this stays mechanically in step with Regex.find_all.
    pos = __rx_advance(Match { start: caps[0], end: caps[1] });
  }
  return out;
}

pub function compile(pattern: string): Result[Regex, CompileError] {
  let errors: Cell[Option[CompileError]] = cell_new(None);
  let start: i32 = 0;
  let ci: boolean = false;
  if (pattern.starts_with("(?i)")) {
    start = 4;
    ci = true;
  }
  let r: RParse = __rx_alt(errors, 0, pattern, start, 1);
  if let Some(e) = errors.get() {
    return Err(e);
  }
  if (r.pos != pattern.len()) {
    return Err(CompileError { kind: UnexpectedClose, offset: r.pos });
  }
  if (__rx_program_size(r.node, MAX_PROGRAM - 3) < 0) {
    return Err(CompileError { kind: ProgramLimit, offset: pattern.len() });
  }
  return Ok(Regex { root: r.node, ci: ci, ngroups: r.g - 1, names: __rx_group_names(pattern), program: __rx_cprog(r.node) });
}

pub function (prog: Regex) count(text: string): i32 {
  return prog.find_all(text).len();
}

pub function (prog: Regex) find(text: string): Option[Match] {
  return __rx_search_from(prog.root, text, 0, prog.ci);
}

pub function (prog: Regex) find_all(text: string): Match[] {
  let out: Match[] = [];
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

pub function (c: Captures) group(i: i32): Result[Option[string], TextError] {
  let (s, e) = __rx_span(c, i);
  if (s < 0) {
    return Ok(None);
  }
  if (s != e && (!utf8.is_char_boundary(c.text, s) || !utf8.is_char_boundary(c.text, e))) {
    return Err(InvalidUtf8Output);
  }
  return Ok(Some(slice_unchecked(c.text, s, e) + ""));
}

pub function (c: Captures) group_bytes(i: i32): Option[u8[]] {
  let (s, e) = __rx_span(c, i);
  if (s < 0) {
    return None;
  }
  return Some(__rx_bytes(c.text, s, e));
}

pub function (c: Captures) group_count(): i32 {
  return c.starts.len() - 1;
}

pub function (c: Captures) group_end(i: i32): Option[i32] {
  if (i < 0 || i >= c.ends.len() || c.ends[i] < 0) {
    return None;
  }
  return Some(c.ends[i]);
}

pub function (c: Captures) group_index(name: string): Option[i32] {
  let i: i32 = 0;
  while (i < c.names.len()) {
    if (c.names[i] == name) {  // names[i] is group i+1
      return Some(i + 1);
    }
    i = i + 1;
  }
  return None;
}

pub function (c: Captures) group_named(name: string): Result[Option[string], TextError] {
  return c.group(__rx_named_index(c, name));
}

pub function (c: Captures) group_named_bytes(name: string): Option[u8[]] {
  return c.group_bytes(__rx_named_index(c, name));
}

pub function (c: Captures) group_named_end(name: string): Option[i32] {
  return c.group_end(__rx_named_index(c, name));
}

pub function (c: Captures) group_named_start(name: string): Option[i32] {
  return c.group_start(__rx_named_index(c, name));
}

pub function (c: Captures) group_start(i: i32): Option[i32] {
  if (i < 0 || i >= c.starts.len() || c.starts[i] < 0) {
    return None;
  }
  return Some(c.starts[i]);
}

pub function (c: Captures) has_group(i: i32): boolean {
  if let Some(_) = c.group_start(i) {
    return true;
  }
  return false;
}

pub function (c: Captures) has_group_named(name: string): boolean {
  return c.has_group(__rx_named_index(c, name));
}

pub function (prog: Regex) index_of(text: string): Option[i32] {
  if let Some(m) = prog.find(text) {
    return Some(m.start);
  }
  return None;
}

pub function (prog: Regex) is_full_match(text: string): boolean {
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

pub function (prog: Regex) is_match(text: string): boolean {
  if let Some(_) = prog.find(text) {
    return true;
  }
  return false;
}

pub function (e: CompileError) message(): string {
  return e.to_string();
}

pub function (e: TextError) message(): string {
  return e.to_string();
}

pub function (prog: Regex) replace(text: string, repl: string): Result[string, TextError] {
  let Some(m) = prog.find(text) else {
    return Ok(text);
  };
  return __rx_text(__rx_replace_span(text, repl, m));
}

pub function (prog: Regex) replace_all(text: string, repl: string): Result[string, TextError] {
  let first: Option[Match] = __rx_search_from(prog.root, text, 0, prog.ci);
  match (first) {
    None => {
      return Ok(text);
    },
    _ => {}
  }
  return __rx_text(__rx_replace_all_from(prog, text, repl, first));
}

pub function (prog: Regex) replace_all_bytes(text: string, repl: string): u8[] {
  return __rx_replace_all_from(prog, text, repl, __rx_search_from(prog.root, text, 0, prog.ci));
}

pub function (prog: Regex) replace_all_groups(text: string, tmpl: string): Result[string, TextError] {
  let all: Captures[] = prog.captures_all(text);
  if (all.len() == 0) {
    return Ok(text);
  }
  return __rx_text(__rx_replace_captures(text, tmpl, all));
}

pub function (prog: Regex) replace_all_groups_bytes(text: string, tmpl: string): u8[] {
  let all: Captures[] = prog.captures_all(text);
  return __rx_replace_captures(text, tmpl, all);
}

pub function (prog: Regex) replace_bytes(text: string, repl: string): u8[] {
  let Some(m) = prog.find(text) else {
    return __rx_bytes(text, 0, text.len());
  };
  return __rx_replace_span(text, repl, m);
}

pub function (prog: Regex) replace_groups(text: string, tmpl: string): Result[string, TextError] {
  let Some(c) = prog.captures(text) else {
    return Ok(text);
  };
  return __rx_text(__rx_replace_capture(text, tmpl, c));
}

pub function (prog: Regex) replace_groups_bytes(text: string, tmpl: string): u8[] {
  let Some(c) = prog.captures(text) else {
    return __rx_bytes(text, 0, text.len());
  };
  return __rx_replace_capture(text, tmpl, c);
}

pub function (prog: Regex) split(text: string): Result[string[], TextError] {
  let out: string[] = [];
  let last: i32 = 0;
  let pos: i32 = 0;
  while (pos <= text.len()) {
    let Some(m) = __rx_search_from(prog.root, text, pos, prog.ci) else {
      if (!utf8.is_char_boundary(text, last)) {
        return Err(InvalidUtf8Output);
      }
      out = out.append(slice_unchecked(text, last, text.len()) + "");
      return Ok(out);
    };
    if (m.end > m.start) {  // non-empty separator
      if (last != m.start && (!utf8.is_char_boundary(text, last) || !utf8.is_char_boundary(text, m.start))) {
        return Err(InvalidUtf8Output);
      }
      out = out.append(slice_unchecked(text, last, m.start) + "");
      last = m.end;
      pos = m.end;
    } else {
      pos = m.start + 1;  // skip a zero-width match
    }
  }
  if (!utf8.is_char_boundary(text, last)) {
    return Err(InvalidUtf8Output);
  }
  out = out.append(slice_unchecked(text, last, text.len()) + "");
  return Ok(out);
}

pub function (prog: Regex) split_bytes(text: string): u8[][] {
  let out: u8[][] = [];
  let last: i32 = 0;
  let pos: i32 = 0;
  while (pos <= text.len()) {
    let Some(m) = __rx_search_from(prog.root, text, pos, prog.ci) else {
      out = out.append(__rx_bytes(text, last, text.len()));
      return out;
    };
    if (m.end > m.start) {  // non-empty separator
      out = out.append(__rx_bytes(text, last, m.start));
      last = m.end;
      pos = m.end;
    } else {
      pos = m.start + 1;  // skip a zero-width match
    }
  }
  out = out.append(__rx_bytes(text, last, text.len()));
  return out;
}

pub function (e: CompileError) to_string(): string {
  let why: string = "invalid pattern";
  match (e.kind) {
    TrailingEscape => {
      why = "escape has no following byte";
    },
    UnclosedGroup => {
      why = "group has no closing parenthesis";
    },
    UnclosedClass => {
      why = "class has no closing bracket";
    },
    UnexpectedClose => {
      why = "unexpected closing parenthesis";
    },
    MissingAtom => {
      why = "quantifier has no preceding atom";
    },
    InvalidRepeat => {
      why = "invalid repetition count";
    },
    RepeatLimit => {
      why = "repetition count exceeds " + MAX_REPEAT.to_string();
    },
    UnsupportedGroup => {
      why = "unsupported group syntax";
    },
    NestingLimit => {
      why = "group depth exceeds " + MAX_NESTING.to_string();
    },
    InvalidClassRange => {
      why = "invalid character class range";
    },
    ProgramLimit => {
      why = "compiled pattern exceeds " + MAX_PROGRAM.to_string() + " instructions";
    }
  }
  return "regex byte " + e.offset.to_string() + ": " + why;
}

pub function (e: TextError) to_string(): string {
  return "regex output is not valid UTF-8";
}

impl cmp.Display for CompileError {}

impl cmp.Display for TextError {}

impl error.Error for CompileError {}

impl error.Error for TextError {}

pub struct Captures { text: string, starts: i32[], ends: i32[], names: string[] }

@derive(cmp.Eq)
pub struct CompileError { kind: CompileErrorKind, offset: i32 }

pub struct Match { start: i32, end: i32 }

struct RClassUnit { kind: i32, lo: i32, hi: i32 }

pub struct Regex { root: RNode, ci: boolean, ngroups: i32, names: string[], program: RInst[] }
```

### Module introduction

```fern
<<module introduction>>=
// Compiled regular expressions with checked syntax and UTF-8 output.
```
