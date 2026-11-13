# Regular expression evaluation

This module evaluates regular expressions.

## Model

A compiled expression keeps a program for a Pike VM, its case policy, and capture metadata. Each search runs this program, so a span and its captures come from the same thread. A search runs its threads in one pass over the text. A new thread starts at each offset where a match can start, until a thread finds a match. The threads are in one array of words, and a step does not allocate. Thus a search takes time in proportion to the length of the text and the size of the program.

Match values identify input spans. A compiled expression also keeps the set of bytes that can start a match. A search does not try a match at a byte outside that set.

## Match rule

A search gives the leftmost-first match, as Rust `regex`, Go `regexp` and RE2 do. The match starts at the leftmost offset where a match exists. At that offset, the search gives the match of the thread with the highest priority. Alternatives have priority from left to right, and `*`, `+`, `?` and `{n,m}` prefer one more repeat to one fewer. A thread stops at a state of the program that a thread of higher priority visited at the same offset. Thus a repeat that matches no byte ends its loop.

For example, `foo|foobar` gives `foo` on "foobar", `(|a)*` gives an empty match on "aa", and `(?:b?(?:|a))*` gives "b" on "ba". PCRE uses the same rule. But it can give a different match when the body of a loop can match no byte.

`longest` gives a copy of an expression that uses the leftmost-longest rule of POSIX. It gives `foobar` for `foo|foobar`. It is a method and not a second `compile`, because the rule controls the search and not the syntax. The pattern, its errors and its group numbers are the same under the two rules. Go has the same shape in `(*Regexp).Longest`, but a `Regex` is a value, so `longest` returns a copy.

Each search method uses the rule of its expression: `find`, `find_at`, `find_all`, `captures`, `captures_all`, `replace` and its peers, and `split`. The captures come from the thread that gives the span of the match. Under `longest`, they come from the thread of highest priority that reaches the longest end. `is_full_match` asks if a match of the full text exists, so the rule does not change its result.

## Contracts

Search and complete-match operations have different purposes. Compilation returns a typed error for invalid patterns. Text output methods report invalid UTF-8 through a typed error. Byte output methods preserve raw bytes.

`next_candidate` gives the first offset at or after a byte offset where a match can start, or -1. A caller can skip the text before that offset. `find_at` starts a search at a byte offset. Its assertions examine the full text, so `^` matches only at offset 0. `\b{start}` and `\b{end}` match at the start and at the end of a word. `\b{start-half}` matches where no word byte comes before, and `\b{end-half}` matches where no word byte comes after.

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


<<10 function __rx_search_from(prog: Regex, text: string, from: i32): Option[Match]>>


<<12 function __rx_advance(m: RMatch): i32>>


<<14 function __rx_finish(h: usize): u8[]>>


<<16 function __rx_replace_all_from(prog: RProg, text: string, repl: string, first: Option[RMatch]): u8[]>>


<<18 function __rx_replace_span(text: string, repl: string, m: RMatch): u8[]>>


<<20 struct RSplit>>

<<21 function __rx_split(a: i32, b: i32): RInst>>

<<22 packed program>>

<<23 function __rx_exec(prog: Regex, t: string, from: i32, ns: i32, rule: RRule, anchored: boolean, own m: i32[]): i32[]>>



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
import "core/error";
import "std/i32";
import "std/string";
import "std/utf8" as utf8;
```

### `3 struct RGroupData`

```fern
<<3 struct RGroupData>>=
struct RGroupData { idx: i32, node: RNode }

enum RNode {
  RChar(i32),
  RAny,
  RClass(u8[]),
  RStart,
  REnd,
  RBound(i32),
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
  let c = p[i] as i32;
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
  let i = 0;
  while (i < n) {
    items = items.append(node);
    i += 1;
  }
  if (!has_max) {
    items = items.append(RStar(node));
  } else {
    let k = n;
    while (k < m) {
      items = items.append(ROpt(node));
      k += 1;
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
  let j = pos + 1;
  let n = 0;
  let have_n = false;
  while (j < p.len() && p[j] as i32 >= 48 && p[j] as i32 <= 57) {
    let digit = p[j] as i32 - 48;
    if (n > MAX_REPEAT / 10 || n == MAX_REPEAT / 10 && digit > MAX_REPEAT % 10) {
      return __rx_fail(errors, RepeatLimit, pos, p.len(), a.g);
    }
    n = n * 10 + digit;
    j += 1;
    have_n = true;
  }
  if (!have_n) {
    return __rx_fail(errors, InvalidRepeat, pos, p.len(), a.g);
  }
  let m = n;
  let has_max = true;
  if (j < p.len() && p[j] == 44) {  // ','
    j += 1;
    if (j < p.len() && p[j] == 125) {  // `{n,}`
      has_max = false;
    } else {
      m = 0;
      let have_m = false;
      while (j < p.len() && p[j] as i32 >= 48 && p[j] as i32 <= 57) {
        let digit = p[j] as i32 - 48;
        if (m > MAX_REPEAT / 10 || m == MAX_REPEAT / 10 && digit > MAX_REPEAT % 10) {
          return __rx_fail(errors, RepeatLimit, pos, p.len(), a.g);
        }
        m = m * 10 + digit;
        have_m = true;
        j += 1;
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
  let a = __rx_atom(errors, depth, p, i, g);
  let pos = a.pos;
  if (pos < p.len()) {
    let c = p[pos] as i32;
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
  let pos = i;
  let gi = g;
  while (pos < p.len() && p[pos] != 124 && p[pos] != 41) {  // not '|' / ')'
    let q = __rx_quant(errors, depth, p, pos, gi);
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
  let first = __rx_seq(errors, depth, p, i, g);
  let branches = [first.node];
  let pos = first.pos;
  let gi = first.g;
  while (pos < p.len() && p[pos] == 124) {  // '|'
    let nxt = __rx_seq(errors, depth, p, pos + 1, gi);
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
  let i = 0;
  let n = pattern.len();
  while (i < n) {
    let c = pattern[i] as i32;
    if (c == 92) {  // '\' escape — skip next byte
      i += 2;
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
          let ne = i + 3;
          while (ne < n && pattern[ne] != 62) {  // to '>'
            ne += 1;
          }
          if (ne < n) {  // named capturing group
            names = names.append(slice_unchecked(pattern, i + 3, ne) + "");
            i = ne + 1;
          } else {
            i += 1;  // `(?<…` no '>' → non-capturing
          }
        } else {
          i += 1;  // `(?:` / `(?i)` / … → non-capturing
        }
      } else {
        names = names.append("");  // bare capturing group
        i += 1;
      }
    } else {
      i += 1;
    }
  }
  return names;
}

function __rx_class(mask: u8[], byte: i32, ci: boolean): boolean {
  let at = byte;
  if (ci) {
    at += 256;
  }
  return mask[at] != 0;
}
```


### `10 function __rx_search_from(prog: Regex, text: string, from: i32): Option[Match]`

```fern
<<10 function __rx_search_from(prog: Regex, text: string, from: i32): Option[Match]>>=
function __rx_search_from(prog: Regex, text: string, from: i32): Option[Match] {
  return __rx_match(__rx_exec(prog, text, from, 1, __rx_rule(prog), false, []));
}

// The span in the result of `__rx_exec`.
function __rx_match(m: i32[]): Option[Match] {
  if (!__rx_found(m)) {
    return None;
  }
  return Some(Match { start: m[0], end: m[1] });
}

// The rule of a search. `RuleExists` stops at the first match that a thread
// reaches, for a caller that needs only to know if a match exists.
@derive(cmp.Eq)
enum RRule { RuleFirst, RuleLongest, RuleExists }

function __rx_rule(prog: Regex): RRule {
  if (prog.leftmost_longest) {
    return RuleLongest;
  }
  return RuleFirst;
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
  let bytes = buf_take_bytes(h);
  buf_free(h);
  return bytes;
}

function __rx_bytes(text: string, lo: i32, hi: i32): u8[] {
  let h = buf_new(hi - lo);
  buf_push_range(h, text, lo, hi);
  return __rx_finish(h);
}
```


### `16 function __rx_replace_all_from(prog: RProg, text: string, repl: string, first: Option[RMatch]): u8[]`

```fern
<<16 function __rx_replace_all_from(prog: RProg, text: string, repl: string, first: Option[RMatch]): u8[]>>=
function __rx_replace_all_from(prog: Regex, text: string, repl: string, first: Option[Match]): u8[] {
  let h = buf_new(text.len());
  let rule = __rx_rule(prog);
  let mem: i32[] = [];
  let last = 0;
  let next = first;
  while (true) {
    let Some(m) = next else {
      break;
    };
    buf_push_range(h, text, last, m.start);
    buf_push(h, repl);
    last = m.end;
    let nxt = __rx_advance(m);
    if (m.end <= m.start && nxt <= text.len() && nxt > last) {
      buf_push_range(h, text, last, nxt);
      last = nxt;
    }
    if (nxt > text.len()) {
      break;
    }
    mem = __rx_exec(prog, text, nxt, 1, rule, false, mem);
    next = __rx_match(mem);
  }
  buf_push_range(h, text, last, text.len());
  return __rx_finish(h);
}
```


### `18 function __rx_replace_span(text: string, repl: string, m: RMatch): u8[]`

```fern
<<18 function __rx_replace_span(text: string, repl: string, m: RMatch): u8[]>>=
function __rx_replace_span(text: string, repl: string, m: Match): u8[] {
  let h = buf_new(text.len());
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
    RBound(k) => {
      return prog.append(IAssert(k));
    },
    RGroup(gd) => {
      let pg = prog.append(ISave(gd.idx + gd.idx));
      pg = __rx_emit(pg, gd.node);
      return pg.append(ISave(gd.idx + gd.idx + 1));
    },
    RSeq(xs) => {
      let ps = prog;
      let k = 0;
      while (k < xs.len()) {
        ps = __rx_emit(ps, xs[k]);
        k += 1;
      }
      return ps;
    },
    RAlt(xs) => {
      let pa = prog;
      let jmps: i32[] = [];
      let k2 = 0;
      let nb = xs.len();
      while (k2 < nb) {
        if (k2 < nb - 1) {
          let sp = pa.len();
          pa = pa.append(__rx_split(0, 0));
          pa = __rx_emit(pa, xs[k2]);
          jmps = jmps.append(pa.len());
          pa = pa.append(IJmp(0));
          pa = pa.with(sp, __rx_split(sp + 1, pa.len()));
        } else {
          pa = __rx_emit(pa, xs[k2]);
        }
        k2 += 1;
      }
      let alt_end = pa.len();
      let j = 0;
      while (j < jmps.len()) {
        pa = pa.with(jmps[j], IJmp(alt_end));
        j += 1;
      }
      return pa;
    },
    RStar(inner) => {
      let s1 = prog.len();
      let pst = prog.append(__rx_split(0, 0));
      pst = __rx_emit(pst, inner);
      let s2 = pst.len();
      pst = pst.append(__rx_split(0, 0));
      let send = pst.len();
      pst = pst.with(s1, __rx_split(s1 + 1, send));
      return pst.with(s2, __rx_split(s1 + 1, send));
    },
    RPlus(inner) => {
      let b1 = prog.len();
      let pp = __rx_emit(prog, inner);
      let sp2 = pp.len();
      pp = pp.append(__rx_split(0, 0));
      return pp.with(sp2, __rx_split(b1, sp2 + 1));
    },
    ROpt(inner) => {
      let s3 = prog.len();
      let po = prog.append(__rx_split(0, 0));
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
  let before = false;
  if (ti > 0) {
    before = __rx_is_word(t[ti - 1] as i32);
  }
  let after = false;
  if (ti < t.len()) {
    after = __rx_is_word(t[ti] as i32);
  }
  return __rx_word_assert(k, before, after);
}

// Kinds 2 to 7: `\b`, `\B`, `\b{start}`, `\b{end}`, `\b{start-half}` and
// `\b{end-half}`.
function __rx_word_assert(k: i32, before: boolean, after: boolean): boolean {
  if (k == 2) {
    return before != after;
  }
  if (k == 3) {
    return before == after;
  }
  if (k == 4) {
    return !before && after;
  }
  if (k == 5) {
    return before && !after;
  }
  if (k == 6) {
    return !before;
  }
  return !after;
}
```

### `22 packed program`

```fern
<<22 packed program>>=
// The packed program has three words for each instruction: the opcode and
// two operands. A char matches the byte in operand a or in operand b. A class
// matches the bytes that the eight words at operand a of `masks` mark. A
// split prefers operand a to operand b. A save writes the slot in operand a,
// and an assertion has its kind in operand a.
const RX_CHAR: i32 = 0;

const RX_ANY: i32 = 1;

const RX_CLASS: i32 = 2;

const RX_MATCH: i32 = 3;

const RX_SPLIT: i32 = 4;

const RX_JMP: i32 = 5;

const RX_SAVE: i32 = 6;

const RX_ASSERT: i32 = 7;

// A search gets new memory when its generation stamp passes this value.
const RX_GEN_LIMIT: i32 = 1073741824;

function __rx_pack(prog: RInst[], ci: boolean): (i32[], i32[]) {
  let code = __alloc_i32(prog.len() * 3);
  let masks: i32[] = [];
  for pc in 0..prog.len() {
    let op = RX_MATCH;
    let a = 0;
    let b = 0;
    match (prog[pc]) {
      IChar(c) => {
        op = RX_CHAR;
        a = __rx_fold(c, ci);
        b = a;
        if (ci && a >= 97 && a <= 122) {
          b = a - 32;
        }
      },
      IAny => {
        op = RX_ANY;
      },
      IClass(tok) => {
        op = RX_CLASS;
        a = masks.len();
        masks = __rx_pack_mask(masks, tok, ci);
      },
      IAssert(k) => {
        op = RX_ASSERT;
        a = k;
      },
      ISplit(sp) => {
        op = RX_SPLIT;
        a = sp.a;
        b = sp.b;
      },
      IJmp(to) => {
        op = RX_JMP;
        a = to;
      },
      ISave(slot) => {
        op = RX_SAVE;
        a = slot;
      },
      IMatch => {}
    }
    code = code.with(3 * pc, op);
    code = code.with(3 * pc + 1, a);
    code = code.with(3 * pc + 2, b);
  }
  return (code, masks);
}

// Eight words of 32 bits, one bit for each byte of the class.
function __rx_pack_mask(own masks: i32[], tok: u8[], ci: boolean): i32[] {
  let base = 0;
  if (ci) {
    base = 256;
  }
  for w in 0..8 {
    let word = 0;
    for bit in 0..32 {
      if (tok[base + w * 32 + bit] != 0) {
        word = word | 1 << bit;
      }
    }
    masks = masks.append(word);
  }
  return masks;
}
```

### `23 function __rx_exec(prog: Regex, t: string, from: i32, ns: i32, rule: RRule, anchored: boolean, own m: i32[]): i32[]`

The search runs the program once over the text, as a Pike VM. Each list of threads holds at most one thread for each instruction, so all the state of a search fits in one array of `i32` words. The array has this layout, for a program of `n` instructions and `ns` capture slots:

| Words | Contents |
| --- | --- |
| 0 to 2 | the start of the match or -1, its end, and the last generation stamp |
| `n` | the generation stamp of each instruction: the step that visited it last |
| `ns` | the slots of the match |
| `n` and `n` | the instructions of the threads of the current list and of the next list, in priority order |
| `n * ns` and `n * ns` | the slots of the threads of the two lists, by instruction |
| `2 * n + 2` | the stack of the closure: an instruction to visit, or a slot to restore and its value |
| `ns` | the slots of the path that the closure follows |

A step makes a new generation stamp. Thus an instruction is visited in this step when its stamp is the stamp of the step, and the search does not clear a set of visited states. A caller that searches again with the same program gives the array back, and the search does not allocate.

```fern
<<23 function __rx_exec(prog: Regex, t: string, from: i32, ns: i32, rule: RRule, anchored: boolean, own m: i32[]): i32[]>>=
// Runs the program on `t` from `from` and gives `m` back with the result in
// its first words. A search for a span alone gives 1 for `ns`. `RuleExists`
// can give 0, and then the start is only not -1. A slot at or after `ns` is
// not recorded. `m` stays empty when no offset can start a match.
//
// The threads of a list are in priority order. A thread that comes to a
// (pc, offset) state that a thread of higher priority visited stops. A new
// thread starts at each candidate offset until a thread finds a match. It has
// a lower priority than each older thread, so the leftmost start wins. Under
// `RuleFirst` the first thread that reaches the match wins and stops the
// threads after it. Under `RuleLongest` the first thread that reaches the
// longest end at the leftmost start wins, and `ns` is at least 1. When
// `anchored` is true, a thread starts at `from` only.
//
// The step and the closure are one loop, so a step calls no function for a
// thread.
// fern-lint: allow cyclomatic-complexity
function __rx_exec(prog: Regex, t: string, from: i32, ns: i32, rule: RRule, anchored: boolean, own m: i32[]): i32[] {
  let next = from;
  if (!anchored) {
    next = prog.next_candidate(t, from);
  }
  if (next < 0) {
    if (m.len() > 0) {
      m = m.with(0, -1);
    }
    return m;
  }
  let code = prog.code;
  let masks = prog.masks;
  let n = code.len() / 3;
  let res = 3 + n;
  let cl = res + ns;
  let nl = cl + n;
  let cs = nl + n;
  let nsl = cs + n * ns;
  let stk = nsl + n * ns;
  let sc = stk + 2 * n + 2;
  if (m.len() < sc + ns || m[2] > RX_GEN_LIMIT) {
    m = __alloc_i32(sc + ns);
  }
  let longest = rule == RuleLongest;
  let exists = rule == RuleExists;
  let gen = m[2];
  let len = t.len();
  let best_start = -1;
  let best_end = -1;
  let ccount = 0;
  let pos = next - 1;
  while (true) {
    gen += 1;
    let npos = pos + 1;
    let b = -1;
    if (pos >= 0 && pos < len) {
      b = t[pos] as i32;
    }
    let ncount = 0;
    let k = 0;
    while (k <= ccount) {
      let entry = -1;
      if (k < ccount) {
        let pc = m[cl + k];
        let at = cs + pc * ns;
        let op = code[3 * pc];
        if (longest && best_end >= 0 && m[at] > best_start) {
          op = -1;  // a thread that starts after the match
        }
        if (op == RX_CHAR) {
          if (b == code[3 * pc + 1] || b == code[3 * pc + 2]) {
            entry = pc + 1;
          }
        } else if (op == RX_CLASS) {
          if (b >= 0 && (masks[code[3 * pc + 1] + (b >> 5)] >> (b & 31) & 1) != 0) {
            entry = pc + 1;
          }
        } else if (op == RX_ANY) {
          if (b >= 0) {
            entry = pc + 1;
          }
        } else if (op == RX_MATCH) {
          if (!longest || pos > best_end) {
            best_end = pos;
            best_start = pos;
            if (ns > 0) {
              best_start = m[at];
            }
            for s in 0..ns {
              m = m.with(res + s, m[at + s]);
            }
          }
          if (exists) {
            break;
          }
          if (!longest) {
            k = ccount;  // the threads after this one have a lower priority
          }
        }
        if (entry >= 0) {
          for s in 0..ns {
            m = m.with(sc + s, m[at + s]);
          }
        }
      } else if (best_end < 0 && (npos == next || !anchored && npos > next && __rx_can_start(prog, t, npos))) {
        entry = 0;
        for s in 0..ns {
          m = m.with(sc + s, -1);
        }
      }
      k += 1;
      if (entry < 0) {
        continue;
      }
      // The closure of `entry` at `npos` adds the threads of the next list in
      // priority order: a split visits operand a and all that follows it
      // before operand b.
      m = m.with(stk, entry);
      let sp = 2;
      while (sp > 0) {
        sp -= 2;
        let pc = m[stk + sp];
        if (pc < 0) {
          m = m.with(sc - pc - 1, m[stk + sp + 1]);
          continue;
        }
        while (m[3 + pc] != gen) {
          m = m.with(3 + pc, gen);
          let op = code[3 * pc];
          if (op == RX_SPLIT) {
            m = m.with(stk + sp, code[3 * pc + 2]);
            sp += 2;
            pc = code[3 * pc + 1];
          } else if (op == RX_JMP) {
            pc = code[3 * pc + 1];
          } else if (op == RX_SAVE) {
            let slot = code[3 * pc + 1];
            if (slot < ns) {
              m = m.with(stk + sp, -slot - 1);
              m = m.with(stk + sp + 1, m[sc + slot]);
              sp += 2;
              m = m.with(sc + slot, npos);
            }
            pc += 1;
          } else if (op == RX_ASSERT) {
            if (!__rx_assert_ok(code[3 * pc + 1], t, npos)) {
              break;
            }
            pc += 1;
          } else {
            m = m.with(nl + ncount, pc);
            ncount += 1;
            let to = nsl + pc * ns;
            for s in 0..ns {
              m = m.with(to + s, m[sc + s]);
            }
            break;
          }
        }
      }
    }
    if (exists && best_end >= 0) {
      break;
    }
    if (ncount == 0) {
      if (best_end >= 0 || anchored) {
        break;
      }
      next = prog.next_candidate(t, npos + 1);
      if (next < 0) {
        break;
      }
      pos = next - 1;  // no thread lives, so go to the next candidate
      ccount = 0;
      continue;
    }
    let tl = cl;
    cl = nl;
    nl = tl;
    let ts = cs;
    cs = nsl;
    nsl = ts;
    ccount = ncount;
    pos = npos;
  }
  if (best_end < 0) {
    best_start = -1;
  }
  m = m.with(0, best_start);
  m = m.with(1, best_end);
  return m.with(2, gen);
}

// True when a match can start at `at`. `next_candidate` gives the first such
// offset, but it can read far past `at`.
function __rx_can_start(prog: Regex, t: string, at: i32): boolean {
  if (prog.first.len() == 0) {
    return at <= t.len();
  }
  return at < t.len() && prog.first[t[at] as i32] != 0 && __rx_has_prefix(prog, t, at);
}

// True when the result of `__rx_exec` holds a match.
function __rx_found(m: i32[]): boolean {
  return m.len() > 0 && m[0] >= 0;
}

// The slots of the match in the result of `__rx_exec`.
function __rx_slots(prog: Regex, m: i32[], ns: i32): i32[] {
  let at = 3 + prog.code.len() / 3;
  let out = __alloc_i32(ns);
  for s in 0..ns {
    out = out.with(s, m[at + s]);
  }
  return out;
}

function __rx_csearch_from(prog: Regex, t: string, from: i32): i32[] {
  let ns = (prog.ngroups + 1) * 2;
  let m = __rx_exec(prog, t, from, ns, __rx_rule(prog), false, []);
  if (!__rx_found(m)) {
    return [];
  }
  return __rx_slots(prog, m, ns);
}
```


### `26 function __rx_span(c: RCaps, i: i32): (i32, i32)`

```fern
<<26 function __rx_span(c: RCaps, i: i32): (i32, i32)>>=
function __rx_span(c: Captures, i: i32): (i32, i32) {
  if (i < 0 || i >= c.starts.len() || c.starts[i] < 0 || c.ends[i] < 0) {
    return (-1, -1);
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
  return -1;
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
  let i = 0;
  while (i <= ngroups) {
    starts = starts.append(caps[i + i]);
    ends = ends.append(caps[i + i + 1]);
    i += 1;
  }
  return Some(Captures { text: text, starts: starts, ends: ends, names: names });
}
```


### `32 function __rx_template_ref(tmpl: string, i: i32, c: RCaps): (i32, i32)`

```fern
<<32 function __rx_template_ref(tmpl: string, i: i32, c: RCaps): (i32, i32)>>=
function __rx_template_ref(tmpl: string, i: i32, c: Captures): (i32, i32) {
  if (i + 1 >= tmpl.len()) {
    return (-1, i);
  }
  let d = tmpl[i + 1] as i32;
  if (d == 36) {
    return (-2, i + 2);
  }
  if (d >= 48 && d <= 57) {
    return (d - 48, i + 2);
  }
  if (d == 123) {
    let ne = i + 2;
    while (ne < tmpl.len() && tmpl[ne] != 125) {
      ne += 1;
    }
    if (ne < tmpl.len()) {
      return (__rx_named_index(c, slice_unchecked(tmpl, i + 2, ne) + ""), ne + 1);
    }
  }
  return (-1, i);
}

function __rx_expand_into(h: usize, tmpl: string, c: Captures): void {
  let lit = 0;
  let i = 0;
  while (i < tmpl.len()) {
    if (tmpl[i] != 36) {
      i += 1;
      continue;
    }
    let ref = __rx_template_ref(tmpl, i, c);
    if (ref.1 == i) {
      i += 1;
      continue;
    }
    buf_push_range(h, tmpl, lit, i);
    if (ref.0 == -2) {
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
  let h = buf_new(text.len());
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
  let h = buf_new(text.len());
  let last = 0;
  let mi = 0;
  while (mi < all.len()) {
    let m = all[mi];
    buf_push_range(h, text, last, m.starts[0]);
    __rx_expand_into(h, tmpl, m);
    last = m.ends[0];
    mi += 1;
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
  UnknownBoundary,
}

pub enum TextError { InvalidUtf8Output }

function __rx_child_size(child: RNode, overhead: i32, budget: i32): i32 {
  let size = __rx_program_size(child, budget - overhead);
  if (size < 0) {
    return -1;
  }
  return overhead + size;
}

function __rx_class_end(pattern: string, start: i32): Option[i32] {
  let i = start + 1;
  if (i < pattern.len() && pattern[i] == 94) {
    i += 1;
  }
  if (i < pattern.len() && pattern[i] == 93) {
    i += 1;
  }
  while (i < pattern.len()) {
    if (pattern[i] == 92) {
      i += 2;
    } else if (pattern[i] == 93) {
      return Some(i);
    } else {
      i += 1;
    }
  }
  return None;
}

function __rx_class_unit(token: string, at: i32): (i32, i32, i32) {
  let byte = token[at] as i32;
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
  let i = 1;
  if (token[i] == 94) {
    i += 1;
  }
  let end = token.len() - 1;
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
  let d = p[i + 1] as i32;
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
    return __rx_boundary(errors, p, i, g);
  }
  if (d == 66) {  // \B
    return RParse { node: RBound(3), pos: i + 2, g: g };
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

// `\b`, `\b{start}`, `\b{end}`, `\b{start-half}` or `\b{end-half}` at `i`.
function __rx_boundary(errors: Cell[Option[CompileError]], p: string, i: i32, g: i32): RParse {
  if (i + 2 >= p.len() || p[i + 2] != 123) {
    return RParse { node: RBound(2), pos: i + 2, g: g };
  }
  let close = __memchr(p, 125, i + 3);
  if (close < 0) {
    return __rx_fail(errors, UnknownBoundary, i, p.len(), g);
  }
  let name = slice_unchecked(p, i + 3, close);
  let kind = 0;
  if (name == "start") {
    kind = 4;
  } else if (name == "end") {
    kind = 5;
  } else if (name == "start-half") {
    kind = 6;
  } else if (name == "end-half") {
    kind = 7;
  } else {
    return __rx_fail(errors, UnknownBoundary, i, p.len(), g);
  }
  return RParse { node: RBound(kind), pos: close + 1, g: g };
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
      let ne = i + 3;
      while (ne < p.len() && p[ne] != 62) {  // scan to '>'
        ne += 1;
      }
      if (ne < p.len()) {  // found '>'
        let gi = __rx_alt(errors, depth + 1, p, ne + 1, g + 1);
        let gpos = gi.pos;
        if (gpos < p.len() && p[gpos] == 41) {  // ')'
          gpos += 1;
        } else {
          return __rx_fail(errors, UnclosedGroup, i, p.len(), g);
        }
        return RParse { node: RGroup(RGroupData { idx: g, node: gi.node }), pos: gpos, g: gi.g };
      }
    }
    if (i + 2 >= p.len() || p[i + 2] != 58) {
      return __rx_fail(errors, UnsupportedGroup, i, p.len(), g);
    }
    let nb = i + 3;
    let ni = __rx_alt(errors, depth + 1, p, nb, g);
    let npos = ni.pos;
    if (npos < p.len() && p[npos] == 41) {  // ')'
      npos += 1;
    } else {
      return __rx_fail(errors, UnclosedGroup, i, p.len(), g);
    }
    return RParse { node: ni.node, pos: npos, g: ni.g };
  }
  // `( … )` — capturing: claim index `g`, then parse the body (which
  // numbers any nested groups after this one, left to right).
  let inner = __rx_alt(errors, depth + 1, p, i + 1, g + 1);
  let pos = inner.pos;
  if (pos < p.len() && p[pos] == 41) {  // ')'
    pos += 1;
  } else {
    return __rx_fail(errors, UnclosedGroup, i, p.len(), g);
  }
  return RParse { node: RGroup(RGroupData { idx: g, node: inner.node }), pos: pos, g: inner.g };
}

function __rx_mark_range(own mask: u8[], lo: i32, hi: i32, value: u8): u8[] {
  let byte = lo;
  while (byte <= hi) {
    mask = mask.with(byte, value);
    mask = mask.with(byte + 256, value);
    if (byte >= 65 && byte <= 90) {
      mask = mask.with(byte + 32 + 256, value);
    } else if (byte >= 97 && byte <= 122) {
      mask = mask.with(byte - 32 + 256, value);
    }
    byte += 1;
  }
  return mask;
}

function __rx_mask(token: string, errors: Cell[Option[CompileError]], offset: i32): u8[] {
  let units = __rx_class_units(token, errors, offset);
  let out = __alloc_u8(512);
  let fill = if (token[1] == 94) { 1 } else { 0 };
  let value = 1 - fill;
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
  let total = overhead;
  if (total > budget) {
    return -1;
  }
  for child in nodes {
    let size = __rx_program_size(child, budget - total);
    if (size < 0) {
      return -1;
    }
    total += size;
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
      let overhead = 0;
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
  let negated = kind < 0;
  if (negated) {
    kind = -kind;
  }
  let found = false;
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

// True when `n` can match without a byte.
function __rx_nullable(n: RNode): boolean {
  match (n) {
    RSeq(xs) => {
      return __rx_nullable_count(xs) == xs.len();
    },
    RAlt(xs) => {
      return __rx_nullable_count(xs) > 0;
    },
    RPlus(inner) => {
      return __rx_nullable(inner);
    },
    RGroup(gd) => {
      return __rx_nullable(gd.node);
    },
    _ => {
      return !__rx_consumes(n);
    }
  }
}

function __rx_nullable_count(xs: RNode[]): i32 {
  let count = 0;
  for x in xs {
    if (__rx_nullable(x)) {
      count += 1;
    }
  }
  return count;
}

// True for a node that always matches one byte.
function __rx_consumes(n: RNode): boolean {
  match (n) {
    RChar(_) => {
      return true;
    },
    RAny => {
      return true;
    },
    RClass(_) => {
      return true;
    },
    _ => {
      return false;
    }
  }
}

// Marks in `set` each byte that can begin a match of `n`.
function __rx_first_into(n: RNode, ci: boolean, own set: u8[]): u8[] {
  match (n) {
    RSeq(xs) => {
      for x in xs {
        set = __rx_first_into(x, ci, set);
        if (!__rx_nullable(x)) {
          return set;
        }
      }
      return set;
    },
    RAlt(xs) => {
      for x in xs {
        set = __rx_first_into(x, ci, set);
      }
      return set;
    },
    RStar(inner) => {
      return __rx_first_into(inner, ci, set);
    },
    RPlus(inner) => {
      return __rx_first_into(inner, ci, set);
    },
    ROpt(inner) => {
      return __rx_first_into(inner, ci, set);
    },
    RGroup(gd) => {
      return __rx_first_into(gd.node, ci, set);
    },
    _ => {
      return __rx_first_byte_into(n, ci, set);
    }
  }
}

// Marks the bytes that a node of one byte matches. An assertion marks none.
function __rx_first_byte_into(n: RNode, ci: boolean, own set: u8[]): u8[] {
  if (!__rx_consumes(n)) {
    return set;
  }
  for b in 0..256 {
    if (__rx_byte_ok(n, b, ci)) {
      set = set.with(b, 1);
    }
  }
  return set;
}

// True when `b` matches `n`, a node of one byte.
function __rx_byte_ok(n: RNode, b: i32, ci: boolean): boolean {
  match (n) {
    RChar(c) => {
      return __rx_fold(b, ci) == __rx_fold(c, ci);
    },
    RClass(tok) => {
      return __rx_class(tok, b, ci);
    },
    _ => {
      return true;
    }
  }
}

function __rx_first_set(root: RNode, ci: boolean): u8[] {
  if (__rx_nullable(root)) {
    return [];
  }
  let set = __rx_first_into(root, ci, __alloc_u8(256));
  for b in 0..256 {
    if (set[b] == 0) {
      return set;
    }
  }
  return [];
}

// The bytes of the literal that starts each match of `root`.
function __rx_prefix(root: RNode, ci: boolean): u8[] {
  let out: u8[] = [];
  match (root) {
    RChar(c) => {
      return [__rx_fold(c, ci) as u8];
    },
    RSeq(xs) => {
      for x in xs {
        let RChar(c) = x else {
          return out;
        };
        out = out.append(__rx_fold(c, ci) as u8);
      }
      return out;
    },
    _ => {
      return out;
    }
  }
}

function __rx_has_prefix(prog: Regex, text: string, at: i32): boolean {
  if (at + prog.prefix.len() > text.len()) {
    return false;
  }
  for k in 0..prog.prefix.len() {
    if (__rx_fold(text[at + k] as i32, prog.ci) != prog.prefix[k] as i32) {
      return false;
    }
  }
  return true;
}

function __rx_lead(first: u8[]): i32 {
  let lead = -1;
  for b in 0..first.len() {
    if (first[b] != 0) {
      if (lead >= 0) {
        return -1;
      }
      lead = b;
    }
  }
  return lead;
}

function __rx_text(bytes: u8[]): Result[string, TextError] {
  match (utf8.from_bytes(bytes)) {
    Ok(text) => {
      return Ok(text);
    },
    Err(_) => {
      return Err(InvalidUtf8Output);
    }
  }
  return Err(InvalidUtf8Output);
}

pub function (prog: Regex) captures(text: string): Option[Captures] {
  let caps = __rx_csearch_from(prog, text, 0);
  return __rx_caps_wrap(caps, text, prog.ngroups, prog.names);
}

pub function (prog: Regex) captures_all(text: string): Captures[] {
  let rule = __rx_rule(prog);
  let ns = (prog.ngroups + 1) * 2;
  let m: i32[] = [];
  let out: Captures[] = [];
  let pos = 0;
  while (pos <= text.len()) {
    m = __rx_exec(prog, text, pos, ns, rule, false, m);
    if (!__rx_found(m)) {
      return out;
    }
    let caps = __rx_slots(prog, m, ns);
    let Some(c) = __rx_caps_wrap(caps, text, prog.ngroups, prog.names) else {
      return out;
    };
    out = out.append(c);
    pos = __rx_advance(Match { start: m[0], end: m[1] });
  }
  return out;
}

pub function compile(pattern: string): Result[Regex, CompileError] {
  let errors = cell_new(None);
  let start = 0;
  let ci = false;
  if (pattern.starts_with("(?i)")) {
    start = 4;
    ci = true;
  }
  let r = __rx_alt(errors, 0, pattern, start, 1);
  if let Some(e) = errors.get() {
    return Err(e);
  }
  if (r.pos != pattern.len()) {
    return Err(CompileError { kind: UnexpectedClose, offset: r.pos });
  }
  if (__rx_program_size(r.node, MAX_PROGRAM - 3) < 0) {
    return Err(CompileError { kind: ProgramLimit, offset: pattern.len() });
  }
  let first = __rx_first_set(r.node, ci);
  let (code, masks) = __rx_pack(__rx_cprog(r.node), ci);
  return Ok(Regex {
    ci: ci, ngroups: r.g - 1, names: __rx_group_names(pattern), code: code, masks: masks,
    first: first, lead: __rx_lead(first), prefix: __rx_prefix(r.node, ci), leftmost_longest: false,
  });
}

pub function (prog: Regex) count(text: string): i32 {
  let rule = __rx_rule(prog);
  let m: i32[] = [];
  let count = 0;
  let pos = 0;
  while (pos <= text.len()) {
    m = __rx_exec(prog, text, pos, 1, rule, false, m);
    if (!__rx_found(m)) {
      return count;
    }
    count += 1;
    pos = __rx_advance(Match { start: m[0], end: m[1] });
  }
  return count;
}

pub function (prog: Regex) find(text: string): Option[Match] {
  return __rx_search_from(prog, text, 0);
}

// The first match that starts at or after `start`. The assertions see the
// whole text, so `^` does not match at `start` unless `start` is 0.
pub function (prog: Regex) find_at(text: string, start: i32): Option[Match] {
  if (start > text.len()) {
    return None;
  }
  if (start < 0) {
    return __rx_search_from(prog, text, 0);
  }
  return __rx_search_from(prog, text, start);
}

// The first offset at or after `from` where a match can start, or -1. The
// text before that offset holds no match. A pattern that can match an empty
// span can start at each offset.
pub function (prog: Regex) next_candidate(text: string, from: i32): i32 {
  if (from > text.len()) {
    return -1;
  }
  if (prog.first.len() == 0) {
    return from;
  }
  let ti = from;
  while (true) {
    if (prog.lead >= 0) {
      ti = __memchr(text, prog.lead, ti);
      if (ti < 0) {
        return -1;
      }
    } else {
      ti = __scan_set(text, ti, prog.first);
      if (ti >= text.len()) {
        return -1;
      }
    }
    if (__rx_has_prefix(prog, text, ti)) {
      return ti;
    }
    ti += 1;
  }
  return -1;
}

pub function (prog: Regex) find_all(text: string): Match[] {
  let rule = __rx_rule(prog);
  let m: i32[] = [];
  let out: Match[] = [];
  let pos = 0;
  while (pos <= text.len()) {
    m = __rx_exec(prog, text, pos, 1, rule, false, m);
    let Some(found) = __rx_match(m) else {
      return out;
    };
    out = out.append(found);
    pos = __rx_advance(found);
  }
  return out;
}

pub function (c: Captures) group(i: i32): Result[Option[string], TextError] {
  let (s, e) = __rx_span(c, i);
  if (s < 0) {
    return Ok(None);
  }
  if (s == e) {
    return Ok(Some(""));
  }
  if (!utf8.is_char_boundary(c.text, s) || !utf8.is_char_boundary(c.text, e)) {
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
  let i = 0;
  while (i < c.names.len()) {
    if (c.names[i] == name) {  // names[i] is group i+1
      return Some(i + 1);
    }
    i += 1;
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

// The longest match from offset 0 ends at the end of the text exactly when a
// match of the full text exists.
pub function (prog: Regex) is_full_match(text: string): boolean {
  let m = __rx_exec(prog, text, 0, 1, RuleLongest, true, []);
  return __rx_found(m) && m[1] == text.len();
}

// A copy of `prog` that gives the longest match at the leftmost start, as
// POSIX does.
pub function (prog: Regex) longest(): Regex {
  return Regex {
    ci: prog.ci, ngroups: prog.ngroups, names: prog.names, code: prog.code, masks: prog.masks,
    first: prog.first, lead: prog.lead, prefix: prog.prefix, leftmost_longest: true,
  };
}

pub function (prog: Regex) is_match(text: string): boolean {
  return __rx_found(__rx_exec(prog, text, 0, 0, RuleExists, false, []));
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
  let first = __rx_search_from(prog, text, 0);
  match (first) {
    None => {
      return Ok(text);
    },
    _ => {}
  }
  return __rx_text(__rx_replace_all_from(prog, text, repl, first));
}

pub function (prog: Regex) replace_all_bytes(text: string, repl: string): u8[] {
  return __rx_replace_all_from(prog, text, repl, __rx_search_from(prog, text, 0));
}

pub function (prog: Regex) replace_all_groups(text: string, tmpl: string): Result[string, TextError] {
  let all = prog.captures_all(text);
  if (all.len() == 0) {
    return Ok(text);
  }
  return __rx_text(__rx_replace_captures(text, tmpl, all));
}

pub function (prog: Regex) replace_all_groups_bytes(text: string, tmpl: string): u8[] {
  let all = prog.captures_all(text);
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
  let rule = __rx_rule(prog);
  let mem: i32[] = [];
  let out: string[] = [];
  let last = 0;
  let pos = 0;
  while (pos <= text.len()) {
    mem = __rx_exec(prog, text, pos, 1, rule, false, mem);
    let Some(m) = __rx_match(mem) else {
      if (!utf8.is_char_boundary(text, last)) {
        return Err(InvalidUtf8Output);
      }
      out = out.append(slice_unchecked(text, last, text.len()) + "");
      return Ok(out);
    };
    if (m.end > m.start) {  // non-empty separator
      if (last == m.start) {
        out = out.append("");
      } else if (!utf8.is_char_boundary(text, last) || !utf8.is_char_boundary(text, m.start)) {
        return Err(InvalidUtf8Output);
      } else {
        out = out.append(slice_unchecked(text, last, m.start) + "");
      }
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
  let rule = __rx_rule(prog);
  let mem: i32[] = [];
  let out: u8[][] = [];
  let last = 0;
  let pos = 0;
  while (pos <= text.len()) {
    mem = __rx_exec(prog, text, pos, 1, rule, false, mem);
    let Some(m) = __rx_match(mem) else {
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
  let why = "invalid pattern";
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
    },
    UnknownBoundary => {
      why = "unknown word boundary";
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

// `first` marks each byte that can begin a match. It is empty when a match
// can be empty or can begin with any byte. `lead` is the one byte that
// `first` marks, or -1. Each match starts with the bytes of `prefix`, folded
// to lower case under `ci`. `leftmost_longest` selects the longest match at
// the leftmost start in place of the first.
pub struct Regex { ci: boolean, ngroups: i32, names: string[], code: i32[], masks: i32[], first: u8[], lead: i32, prefix: u8[], leftmost_longest: boolean }
```

### Module introduction

```fern
<<module introduction>>=
// Compiled regular expressions with checked syntax and UTF-8 output.
```
