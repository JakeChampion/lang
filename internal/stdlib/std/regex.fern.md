# Regular expression evaluation

This module evaluates regular expressions.

## Model

A compiled expression keeps a program for a Pike VM, its case policy, and capture metadata. A search for captures runs this program, so the span and the captures of a match come from the same thread. The VM runs its threads in one pass over the text. A new thread starts at each offset where a match can start, until a thread finds a match. The threads are in one array of words, and a step does not allocate. Thus a search takes time in proportion to the length of the text and the size of the program.

A compiled expression also keeps a forward DFA and a reverse DFA of the program. They are lazy: a search adds the states that it needs. A DFA gives the same matches as the Pike VM, and a step of a DFA is one lookup in a table. A search for a span or for a match uses the DFAs. A search for captures runs the Pike VM from the start that the DFAs find.

The text is UTF-8, and a pattern matches characters, as in the `regex` crate. A set of characters becomes an automaton over the bytes of their UTF-8 forms. Thus each search reads bytes, and the DFAs stay tables of bytes. A byte that does not start a valid UTF-8 character matches no character.

Match values identify input spans. A compiled expression also keeps the set of bytes that can start a match. A search does not try a match at a byte outside that set.

## Match rule

A search gives the leftmost-first match, as Rust `regex`, Go `regexp` and RE2 do. The match starts at the leftmost offset where a match exists. At that offset, the search gives the match of the thread with the highest priority. Alternatives have priority from left to right, and `*`, `+`, `?` and `{n,m}` prefer one more repeat to one fewer. A thread stops at a state of the program that a thread of higher priority visited at the same offset. Thus a repeat that matches no byte ends its loop.

For example, `foo|foobar` gives `foo` on "foobar", `(|a)*` gives an empty match on "aa", and `(?:b?(?:|a))*` gives "b" on "ba". PCRE uses the same rule. But it can give a different match when the body of a loop can match no byte.

`longest` gives a copy of an expression that uses the leftmost-longest rule of POSIX. It gives `foobar` for `foo|foobar`. It is a method and not a second `compile`, because the rule controls the search and not the syntax. The pattern, its errors and its group numbers are the same under the two rules. Go has the same shape in `(*Regexp).Longest`, but a `Regex` is a value, so `longest` returns a copy.

Each search method uses the rule of its expression: `find`, `find_at`, `find_all`, `captures`, `captures_all`, `replace` and its peers, and `split`. The captures come from the thread that gives the span of the match. Under `longest`, they come from the thread of highest priority that reaches the longest end. `is_full_match` asks if a match of the full text exists, so the rule does not change its result.

## Syntax

The syntax is that of the Rust `regex` crate:

- A literal character, `.`, `^` and `$`, groups `(...)`, `(?:...)`, `(?<name>...)` and `(?P<name>...)`, alternatives `|`, and the repetitions `*`, `+`, `?`, `{n}`, `{n,}` and `{n,m}`. A `?` after a repetition makes it lazy: it prefers one fewer repeat to one more.
- The classes `\d`, `\s` and `\w`, and their complements `\D`, `\S` and `\W`.
- A general category, a script or a binary property of Unicode: `\p{...}` or `\pL`, and `\P{...}` or `\PL` for the complement. Examples are `\p{Greek}`, `\p{Lu}`, `\p{sc=Cyrillic}` and `\p{White_Space}`.
- Bracket classes such as `[a-z\d]` and `[^\p{L}]`. They can hold nested classes and the ASCII classes such as `[:alpha:]`. `&&`, `--` and `~~` give the intersection, the difference and the symmetric difference.
- The assertions `\b`, `\B`, `\<`, `\>`, `\A`, `\z`, `\b{start}`, `\b{end}`, `\b{start-half}` and `\b{end-half}`.
- The escapes `\n`, `\t`, `\r`, `\f`, `\v` and `\a`, and an escaped ASCII mark that is not a letter or a digit.
- A code point in hexadecimal: `\x7F`, `\x{10FFFF}`, `\u007F`, `\u{7F}`, `\U0000007F` or `\U{7F}`.
- The flags `i` (case-insensitive), `m` (`^` and `$` at each line), `s` (`.` matches `\n`), `x` (white space and `#` comments do not count), `R` (lines end with `\r\n`), `U` (the repetitions are lazy, and `?` makes one greedy) and `u` (Unicode). `(?flags)` sets them for the rest of the group, `(?flags:...)` for the group, and `-` turns off the flags after it.

Unicode is on at the start. `\w`, `\d`, `\s`, `\b` and the classes then match characters of Unicode. `(?i)` uses the simple case folding of Unicode, so `(?i)k` also matches KELVIN SIGN.

`(?-u)` gives the semantics of bytes and ASCII: `.` and each class match one byte, and `\xFF` is the byte 0xFF.

## Contracts

Search and complete-match operations have different purposes. Compilation returns a typed error for invalid patterns. Text output methods report invalid UTF-8 through a typed error. Byte output methods preserve raw bytes.

A template of `replace_groups`, its peers and `expand_bytes` reads `$n`, `$name`, `${n}`, `${name}` and `$$` as the `regex` crate does. A name is the longest run of letters, digits and `_`, so `$1a` is the group `1a`. A group that did not match gives no text.

`next_candidate` gives the first offset at or after a byte offset where a match can start, or -1. A caller can skip the text before that offset.

`find_at` starts a search at a byte offset. Its assertions examine the full text, so `^` matches only at offset 0. `\b{start}` and `\b{end}` match at the start and at the end of a word. `\b{start-half}` matches where no word character comes before, and `\b{end-half}` matches where no word character comes after.

A search for a span uses a lazy DFA, and a word assertion of Unicode examines a whole character on each side. A DFA does not do that. When a byte outside ASCII is next to such an assertion, the DFA stops, and the Pike VM does the search.

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


<<character sets>>


<<10 function __rx_search_from(prog: Regex, text: string, from: i32): Option[Match]>>


<<12 function __rx_advance(m: RMatch): i32>>


<<14 function __rx_finish(h: usize): u8[]>>


<<16 function __rx_replace_all_from(prog: RProg, text: string, repl: string, first: Option[RMatch]): u8[]>>


<<18 function __rx_replace_span(text: string, repl: string, m: RMatch): u8[]>>


<<20 struct RSplit>>

<<21 function __rx_split(a: i32, b: i32): RInst>>

<<22 packed program>>

<<23 function __rx_exec(prog: Regex, t: string, from: i32, ns: i32, rule: RRule, anchored: boolean, own m: i32[]): i32[]>>

<<lazy DFA>>



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
import "core/map";
import "std/i32";
import "std/string";
import "std/unicode";
import "std/utf8" as utf8;
```

### `3 struct RGroupData`

A parse gives a tree of nodes. A literal byte is `RChar`, and an ASCII letter under `(?i)` is `RFold`, with its lower case byte. A set of bytes is `RClass`, a mask of 256 bytes. A set of characters that has a character outside ASCII is `RUni`: an automaton over the bytes of the UTF-8 forms of its characters, as the `regex` crate builds it. Thus each search reads bytes, and the DFAs stay tables of bytes.

`RLazy` holds a repetition that prefers one fewer repeat to one more.

```fern
<<3 struct RGroupData>>=
struct RGroupData { idx: i32, node: RNode }

// A set of characters. `ranges` holds the first and the last code point of
// each range, in order. Each node of `nodes` is a list of edges of three
// words: the first and the last byte of the edge, and the node that it goes
// to, or -1 for the end of the character. Node 0 is the start.
struct RUniData { ranges: i32[], nodes: i32[][] }

enum RNode {
  RChar(i32),
  RFold(i32),
  RClass(u8[]),
  RUni(RUniData),
  RBound(i32),
  RSeq(RNode[]),
  RAlt(RNode[]),
  RStar(RNode),
  RPlus(RNode),
  ROpt(RNode),
  RLazy(RNode),
  RGroup(RGroupData),
}

// A parsed node, the offset after it, the next group number, and the flags
// after it.
struct RParse { node: RNode, pos: i32, g: i32, f: i32 }

// The first error of a parse, the names of the groups in order, and the
// pattern.
struct RCtx { errors: Cell[Option[CompileError]], names: Cell[string[]], p: string }

// The flags of a pattern: `(?i)`, `(?m)`, `(?s)`, `(?x)`, `(?u)`, `(?R)` and
// `(?U)`. `(?u)` is on at the start.
const RX_I: i32 = 1;

const RX_M: i32 = 2;

const RX_S: i32 = 4;

const RX_X: i32 = 8;

const RX_U: i32 = 16;

const RX_R: i32 = 32;

const RX_SWAP: i32 = 64;

// The kinds of assertion: the start and the end of the text, the six word
// assertions of ASCII, the same six of Unicode, and the start and the end of
// a line, without and with `(?R)`.
const RX_A_WORD: i32 = 2;

const RX_A_UWORD: i32 = 8;

const RX_A_LINE: i32 = 14;

const RX_A_CRLF: i32 = 16;

const RX_MAX_CP: i32 = 1114111;
```

### `4 function __rx_atom(p: string, i: i32, g: i32): RParse`

```fern
<<4 function __rx_atom(p: string, i: i32, g: i32): RParse>>=
function __rx_atom(cx: RCtx, depth: i32, i: i32, g: i32, f: i32): RParse {
  let c = cx.p[i] as i32;
  if (c == 92) {  // '\'
    return __rx_escape(cx, i, g, f);
  }
  if (c == 42 || c == 43 || c == 63 || c == 123) {
    return __rx_fail(cx, MissingAtom, i, g, f);
  }
  if (c == 40) {  // '('
    return __rx_group(cx, depth, i, g, f);
  }
  if (c == 91) {  // '['
    return __rx_bracket(cx, i, g, f);
  }
  if (c == 46 || c == 94 || c == 36) {  // '.', '^', '$'
    return RParse { node: __rx_meta(c, f), pos: i + 1, g: g, f: f };
  }
  return __rx_literal(cx.p, i, g, f);
}

// `.`, `^` or `$` under the flags `f`.
function __rx_meta(c: i32, f: i32): RNode {
  if (c == 46) {
    return __rx_dot(f);
  }
  let end = if (c == 36) { 1 } else { 0 };
  if ((f & RX_M) == 0) {
    return RBound(end);
  }
  if ((f & RX_R) != 0) {
    return RBound(RX_A_CRLF + end);
  }
  return RBound(RX_A_LINE + end);
}

// `.`: each character but `\n`, or with `(?R)` but `\r` and `\n`. `(?s)`
// adds them, and `(?-u)` makes each character a byte.
function __rx_dot(f: i32): RNode {
  let top = if ((f & RX_U) != 0) { RX_MAX_CP } else { 255 };
  if ((f & RX_S) != 0) {
    return __rx_set([0, top], f);
  }
  if ((f & RX_R) != 0) {
    return __rx_set([0, 9, 11, 12, 14, top], f);
  }
  return __rx_set([0, 9, 11, top], f);
}

// The literal character at `i`. A byte that does not start a valid UTF-8
// character is that byte.
function __rx_literal(p: string, i: i32, g: i32, f: i32): RParse {
  let Some(dec) = utf8.decode_at(p, i) else {
    return RParse { node: RChar(p[i] as i32), pos: i + 1, g: g, f: f };
  };
  if (dec.1 > 1 && (f & RX_U) == 0) {
    return RParse { node: __rx_bytes_node(p, i, i + dec.1), pos: i + dec.1, g: g, f: f };
  }
  return RParse { node: __rx_cp_node(dec.0, f), pos: i + dec.1, g: g, f: f };
}

function __rx_bytes_node(p: string, lo: i32, hi: i32): RNode {
  let items: RNode[] = [];
  for k in lo..hi {
    items = items.append(RChar(p[k] as i32));
  }
  return RSeq(items);
}

// The node of the code point `cp`, or of the byte `cp` under `(?-u)`.
// `(?i)` adds the characters of its case orbit.
function __rx_cp_node(cp: i32, f: i32): RNode {
  if ((f & RX_I) != 0) {
    let rs = __rx_fold_ranges([cp, cp], f);
    if (rs.len() > 2) {
      return __rx_set(rs, f);
    }
  }
  if (cp < 128 || (f & RX_U) == 0) {
    return RChar(cp);
  }
  let bytes = utf8.encode(cp as char);
  return __rx_bytes_node(bytes, 0, bytes.len());
}

// A repetition of `node` from `b.n` to `b.m` times, or with no end when
// `b.inf`. `{n,m}` is n copies and then m - n optional copies.
function __rx_repeat(node: RNode, b: RBounds, lazy: boolean): RNode {
  if (b.inf && b.n <= 1) {
    let rep = if (b.n == 0) { RStar(node) } else { RPlus(node) };
    return __rx_greed(rep, lazy);
  }
  let items: RNode[] = [];
  for _ in 0..b.n {
    items = items.append(node);
  }
  if (b.inf) {
    items = items.append(__rx_greed(RStar(node), lazy));
  } else {
    for _ in b.n..b.m {
      items = items.append(__rx_greed(ROpt(node), lazy));
    }
  }
  if (items.len() == 1) {
    return items[0];
  }
  return RSeq(items);
}

function __rx_greed(n: RNode, lazy: boolean): RNode {
  if (lazy) {
    return RLazy(n);
  }
  return n;
}
```

### `5 function __rx_count(p: string, pos: i32, a: RParse): RParse`

```fern
<<5 function __rx_count(p: string, pos: i32, a: RParse): RParse>>=
// The bounds of a repetition. `end` is the offset after it, -1 when no
// repetition is there, and -2 after an error.
struct RBounds { n: i32, m: i32, inf: boolean, end: i32 }

// The decimal at `at`, and the offset after it. A missing number gives -1,
// and a number over MAX_REPEAT records an error and gives -2.
function __rx_decimal(cx: RCtx, at: i32, pos: i32): (i32, i32) {
  let p = cx.p;
  let j = at;
  let n = 0;
  while (j < p.len() && p[j] >= 48 && p[j] <= 57) {
    n = n * 10 + (p[j] as i32 - 48);
    if (n > MAX_REPEAT) {
      __rx_error(cx, RepeatLimit, pos);
      return (-2, j);
    }
    j += 1;
  }
  if (j == at) {
    return (-1, j);
  }
  return (n, j);
}

// `{n}`, `{n,}` or `{n,m}` at `pos`.
function __rx_bounds(cx: RCtx, pos: i32): RBounds {
  let p = cx.p;
  let bad = RBounds { n: 0, m: 0, inf: false, end: -2 };
  let (n, j) = __rx_decimal(cx, pos + 1, pos);
  if (n == -2) {
    return bad;
  }
  let b = RBounds { n: n, m: n, inf: false, end: j + 1 };
  if (n >= 0 && j < p.len() && p[j] == 44) {  // ','
    let (m, k) = __rx_decimal(cx, j + 1, pos);
    if (m == -2) {
      return bad;
    }
    b = RBounds { n: n, m: m, inf: m < 0, end: k + 1 };
  }
  if (n < 0 || b.end > p.len() || p[b.end - 1] != 125 || !b.inf && b.m < n) {
    __rx_error(cx, InvalidRepeat, pos);
    return bad;
  }
  return b;
}

// The repetition at `pos`: `*`, `+`, `?` or a count.
function __rx_quantifier(cx: RCtx, pos: i32): RBounds {
  let c = cx.p[pos] as i32;
  if (c == 42) {
    return RBounds { n: 0, m: 0, inf: true, end: pos + 1 };
  }
  if (c == 43) {
    return RBounds { n: 1, m: 0, inf: true, end: pos + 1 };
  }
  if (c == 63) {
    return RBounds { n: 0, m: 1, inf: false, end: pos + 1 };
  }
  if (c == 123) {  // '{'
    return __rx_bounds(cx, pos);
  }
  return RBounds { n: 0, m: 0, inf: false, end: -1 };
}

// An atom and the repetitions after it. A `?` after a repetition makes it
// lazy, and `(?U)` swaps the two.
function __rx_quant(cx: RCtx, depth: i32, i: i32, g: i32, f: i32): RParse {
  let r = __rx_atom(cx, depth, i, g, f);
  while (true) {
    let pos = __rx_skip(cx.p, r.pos, r.f);
    if (pos >= cx.p.len()) {
      return r;
    }
    let b = __rx_quantifier(cx, pos);
    if (b.end == -1) {
      return r;
    }
    if (b.end < 0) {
      return RParse { ...r, pos: cx.p.len() };
    }
    let lazy = (r.f & RX_SWAP) != 0;
    let end = b.end;
    if (end < cx.p.len() && cx.p[end] == 63) {
      lazy = !lazy;
      end += 1;
    }
    r = RParse { ...r, node: __rx_repeat(r.node, b, lazy), pos: end };
  }
  return r;
}

// The offset of the next byte of the pattern at or after `i`. Under `(?x)`,
// it skips white space and a comment from `#` to the end of the line.
function __rx_skip(p: string, i: i32, f: i32): i32 {
  if ((f & RX_X) == 0) {
    return i;
  }
  let pos = i;
  while (pos < p.len()) {
    let c = p[pos] as i32;
    if (c == 35) {  // '#'
      let nl = __memchr(p, 10, pos);
      pos = if (nl < 0) { p.len() } else { nl + 1 };
    } else if (c == 32 || c >= 9 && c <= 13) {
      pos += 1;
    } else {
      return pos;
    }
  }
  return pos;
}

function __rx_seq(cx: RCtx, depth: i32, i: i32, g: i32, f: i32): RParse {
  let p = cx.p;
  let items: RNode[] = [];
  let r = RParse { node: RSeq([]), pos: __rx_skip(p, i, f), g: g, f: f };
  while (r.pos < p.len() && p[r.pos] != 124 && p[r.pos] != 41) {  // not '|' / ')'
    r = __rx_quant(cx, depth, r.pos, r.g, r.f);
    match (r.node) {
      RSeq(xs) => {
        items = items.concat(xs);
      },
      _ => {
        items = items.append(r.node);
      }
    }
    r = RParse { ...r, pos: __rx_skip(p, r.pos, r.f) };
  }
  if (items.len() == 1) {
    return RParse { ...r, node: items[0] };
  }
  return RParse { ...r, node: RSeq(items) };
}

// The flags that a branch sets stay set in the branches after it, up to the
// end of the group.
function __rx_alt(cx: RCtx, depth: i32, i: i32, g: i32, f: i32): RParse {
  let first = __rx_seq(cx, depth, i, g, f);
  let branches = [first.node];
  let r = first;
  while (r.pos < cx.p.len() && cx.p[r.pos] == 124) {  // '|'
    r = __rx_seq(cx, depth, r.pos + 1, r.g, r.f);
    branches = branches.append(r.node);
  }
  if (branches.len() == 1) {
    return first;
  }
  return RParse { ...r, node: RAlt(branches) };
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
```


### Character sets

A set of characters is a list of ranges of code points in order, two words for each range. Under `(?-u)`, a set holds bytes. `(?i)` adds the case orbit of each character, from the simple case folding of `std/unicode`.

A set that holds a character outside ASCII becomes an automaton over the bytes of the UTF-8 forms of its characters. The ranges split into sequences of byte ranges, as in the `utf8-ranges` crate. The sequences, in order, make a trie, and each subtree that repeats becomes one node. The `\w` of Unicode thus has about 320 nodes.

The reverse program reads a character from its last byte. It has a node for each node of the automaton, with each edge turned around. A byte can take more than one of these edges, and only a DFA runs the reverse program.

```fern
<<character sets>>=
// The node of the set `rs`. A set of ASCII bytes, or of bytes under `(?-u)`,
// is a mask. Another set is an automaton over the UTF-8 forms of its
// characters.
function __rx_set(rs: i32[], f: i32): RNode {
  let n = rs.len();
  let unicode = (f & RX_U) != 0;
  if (n == 2 && rs[0] == rs[1] && (rs[0] < 128 || !unicode)) {
    return RChar(rs[0]);
  }
  if (n == 4 && rs[0] == rs[1] && rs[2] == rs[3] && rs[0] >= 65 && rs[0] <= 90 && rs[2] == rs[0] + 32) {
    return RFold(rs[2]);
  }
  if (!unicode || n == 0 || rs[n - 1] < 128) {
    return RClass(__rx_mask(rs));
  }
  return RUni(RUniData { ranges: rs, nodes: __rx_utf8_nodes(rs) });
}

// The mask of the bytes of `rs`.
function __rx_mask(rs: i32[]): u8[] {
  let mask = __alloc_u8(256);
  let k = 0;
  while (k < rs.len()) {
    let hi = if (rs[k + 1] > 255) { 255 } else { rs[k + 1] };
    for b in rs[k]..hi + 1 {
      mask = mask.with(b, 1);
    }
    k += 2;
  }
  return mask;
}

// `rs` in order, with the ranges that overlap or touch made one.
function __rx_norm(rs: i32[]): i32[] {
  let keys: i64[] = [];
  let k = 0;
  while (k < rs.len()) {
    keys = keys.append(rs[k] as i64 * 4194304i64 + rs[k + 1] as i64);
    k += 2;
  }
  keys = cmp.sort_in_place(keys);
  let out: i32[] = [];
  for key in keys {
    let lo = (key / 4194304i64) as i32;
    let hi = (key % 4194304i64) as i32;
    let n = out.len();
    if (n > 0 && lo <= out[n - 1] + 1) {
      if (hi > out[n - 1]) {
        out = out.with(n - 1, hi);
      }
    } else {
      out = out.append(lo).append(hi);
    }
  }
  return out;
}

// The last code point of the domain: each character, or each byte under
// `(?-u)`.
function __rx_top(f: i32): i32 {
  if ((f & RX_U) != 0) {
    return RX_MAX_CP;
  }
  return 255;
}

// The members of the domain that are not in `rs`, which is in order. The
// surrogates are not characters.
function __rx_negate(rs: i32[], f: i32): i32[] {
  let out: i32[] = [];
  let next = 0;
  let k = 0;
  while (k < rs.len()) {
    if (rs[k] > next) {
      out = out.append(next).append(rs[k] - 1);
    }
    next = rs[k + 1] + 1;
    k += 2;
  }
  if (next <= __rx_top(f)) {
    out = out.append(next).append(__rx_top(f));
  }
  if ((f & RX_U) != 0) {
    return __rx_intersect(out, [0, 55295, 57344, RX_MAX_CP]);
  }
  return out;
}

// The members of both `a` and `b`, which are in order.
function __rx_intersect(a: i32[], b: i32[]): i32[] {
  let out: i32[] = [];
  let i = 0;
  let j = 0;
  while (i < a.len() && j < b.len()) {
    let lo = if (a[i] > b[j]) { a[i] } else { b[j] };
    let hi = if (a[i + 1] < b[j + 1]) { a[i + 1] } else { b[j + 1] };
    if (lo <= hi) {
      out = out.append(lo).append(hi);
    }
    if (a[i + 1] < b[j + 1]) {
      i += 2;
    } else {
      j += 2;
    }
  }
  return out;
}

// `rs` with the case orbit of each character, or under `(?-u)` with the
// other case of each ASCII letter.
function __rx_fold_ranges(rs: i32[], f: i32): i32[] {
  let out = rs;
  let k = 0;
  while (k < rs.len()) {
    if ((f & RX_U) != 0) {
      for c in unicode.fold_equivalents(rs[k] as char, rs[k + 1] as char) {
        out = out.append(c as i32).append(c as i32);
      }
    } else {
      out = __rx_ascii_fold(out, rs[k], rs[k + 1], 65, 32);
      out = __rx_ascii_fold(out, rs[k], rs[k + 1], 97, -32);
    }
    k += 2;
  }
  return __rx_norm(out);
}

// Adds the letters of `lo` to `hi` that are from `from` to `from` + 25,
// moved by `by`.
function __rx_ascii_fold(own out: i32[], lo: i32, hi: i32, from: i32, by: i32): i32[] {
  let a = if (lo > from) { lo } else { from };
  let b = if (hi < from + 25) { hi } else { from + 25 };
  if (a <= b) {
    out = out.append(a + by).append(b + by);
  }
  return out;
}

// The automaton of the characters of `rs`, which has no surrogate.
function __rx_utf8_nodes(rs: i32[]): i32[][] {
  let seqs: i32[] = [];
  let k = 0;
  while (k < rs.len()) {
    seqs = __rx_utf8_seqs(seqs, rs[k], rs[k + 1]);
    k += 2;
  }
  return __rx_minimize(__rx_trie(seqs));
}

// Appends to `out` the sequences of byte ranges of the UTF-8 forms of the
// code points from `lo` to `hi`, in order. Each sequence has nine words: the
// count of bytes, and the first and the last value of each byte.
function __rx_utf8_seqs(own out: i32[], lo: i32, hi: i32): i32[] {
  let todo = [lo, hi];
  let top = 2;
  while (top > 0) {
    top -= 2;
    let s = todo[top];
    let e = todo[top + 1];
    let mid = __rx_utf8_split(s, e);
    if (mid < 0) {
      out = __rx_utf8_seq(out, s, e);
    } else {
      todo = __rx_put(todo, top, mid + 1, e);
      todo = __rx_put(todo, top + 2, s, mid);
      top += 4;
    }
  }
  return out;
}

// `xs` with `a` and `b` at `at`. It grows when it is too short.
function __rx_put(own xs: i32[], at: i32, a: i32, b: i32): i32[] {
  while (xs.len() < at + 2) {
    xs = xs.append(0);
  }
  xs = xs.with(at, a);
  return xs.with(at + 1, b);
}

// The last code point of the first part of the range from `s` to `e`, or -1
// when the UTF-8 forms of the range are one sequence of byte ranges.
function __rx_utf8_split(s: i32, e: i32): i32 {
  for max in [127, 2047, 65535] {
    if (s <= max && max < e) {
      return max;
    }
  }
  if (e <= 127) {
    return -1;
  }
  for i in 1..4 {
    let m = (1 << 6 * i) - 1;
    if ((s & ~m) != (e & ~m)) {
      if ((s & m) != 0) {
        return s | m;
      }
      if ((e & m) != m) {
        return (e & ~m) - 1;
      }
    }
  }
  return -1;
}

function __rx_utf8_seq(own out: i32[], s: i32, e: i32): i32[] {
  let a = utf8.encode(s as char);
  let b = utf8.encode(e as char);
  out = out.append(a.len());
  for k in 0..4 {
    if (k < a.len()) {
      out = out.append(a[k] as i32).append(b[k] as i32);
    } else {
      out = out.append(0).append(0);
    }
  }
  return out;
}

// The trie of the sequences of `seqs`, which are in order. A sequence shares
// the nodes of the byte ranges that it has in common with the last one. Its
// other ranges come after the edges that a node already has.
function __rx_trie(seqs: i32[]): i32[][] {
  let empty: i32[] = [];
  let nodes = [empty];
  let at = 0;
  while (at < seqs.len()) {
    let node = 0;
    let n = seqs[at];
    for b in 0..n {
      let lo = seqs[at + 1 + 2 * b];
      let hi = seqs[at + 2 + 2 * b];
      let es = nodes[node];
      let last = es.len() - 3;
      if (b + 1 < n && last >= 0 && es[last] == lo && es[last + 1] == hi && es[last + 2] >= 0) {
        node = es[last + 2];
      } else {
        let child = if (b + 1 < n) { nodes.len() } else { -1 };
        nodes = nodes.with(node, es.append(lo).append(hi).append(child));
        if (child >= 0) {
          nodes = nodes.append(empty);
          node = child;
        }
      }
    }
    at += 9;
  }
  return nodes;
}

// The nodes of `trie` with each subtree that repeats made one node. A child
// comes after its parent, and the start stays node 0.
function __rx_minimize(trie: i32[][]): i32[][] {
  let ids: Map[string, i32] = map_new(64);
  let canon = __alloc_i32(trie.len());
  let out: i32[][] = [];
  let k = trie.len() - 1;
  while (k >= 0) {
    let es = __rx_merge_edges(trie[k], canon);
    let key = __rx_edges_key(es);
    let id = ids.get_or(key, -1);
    if (id < 0) {
      id = out.len();
      out = out.append(es);
      ids = ids.insert(key, id);
    }
    canon = canon.with(k, id);
    k -= 1;
  }
  let n = out.len();
  let result: i32[][] = [];
  let j = n - 1;
  while (j >= 0) {
    let es = out[j];
    let e = 0;
    while (e < es.len()) {
      if (es[e + 2] >= 0) {
        es = es.with(e + 2, n - 1 - es[e + 2]);
      }
      e += 3;
    }
    result = result.append(es);
    j -= 1;
  }
  return result;
}

// The edges `es` with each child made its node in `canon`, and the edges
// that touch and go to one node made one edge.
function __rx_merge_edges(es: i32[], canon: i32[]): i32[] {
  let out: i32[] = [];
  let e = 0;
  while (e < es.len()) {
    let c = if (es[e + 2] < 0) { -1 } else { canon[es[e + 2]] };
    let n = out.len();
    if (n > 0 && out[n - 1] == c && out[n - 2] + 1 == es[e]) {
      out = out.with(n - 2, es[e + 1]);
    } else {
      out = out.append(es[e]).append(es[e + 1]).append(c);
    }
    e += 3;
  }
  return out;
}

function __rx_edges_key(es: i32[]): string {
  let parts: string[] = [];
  for x in es {
    parts = parts.append(x.to_string());
  }
  return parts.join(",");
}
```


### `10 function __rx_search_from(prog: Regex, text: string, from: i32): Option[Match]`

```fern
<<10 function __rx_search_from(prog: Regex, text: string, from: i32): Option[Match]>>=
function __rx_search_from(prog: Regex, text: string, from: i32): Option[Match] {
  if (__rx_has_dfa(prog) && !prog.leftmost_longest) {
    let (start, end) = __rx_dfa_span(prog, text, from);
    if (start == -2) {
      return __rx_match(__rx_exec(prog, text, from, 1, RuleFirst, false, []));
    }
    if (start < 0) {
      return None;
    }
    return Some(Match { start: start, end: end });
  }
  return __rx_match(__rx_next(prog, text, from, 1, __rx_rule(prog), []));
}

// The first match at or after `from` in the first words of `m`, as
// `__rx_exec` gives it, with `ns` slots. The DFAs find the span. The Pike VM
// runs from its start for the captures and for the longest match, and from
// `from` when a DFA stops.
function __rx_next(prog: Regex, t: string, from: i32, ns: i32, rule: RRule, own m: i32[]): i32[] {
  if (!__rx_has_dfa(prog)) {
    return __rx_exec(prog, t, from, ns, rule, false, m);
  }
  let (start, end) = __rx_dfa_span(prog, t, from);
  if (start == -2) {
    return __rx_exec(prog, t, from, ns, rule, false, m);
  }
  if (start >= 0 && (ns > 1 || rule == RuleLongest)) {
    return __rx_exec(prog, t, start, ns, rule, true, m);
  }
  if (m.len() < 3) {
    m = __alloc_i32(3);
  }
  m = m.with(0, start);
  return m.with(1, end);
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
    mem = __rx_next(prog, text, nxt, 1, rule, mem);
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
  IFold(i32),
  IClass(u8[]),
  ISparse(i32[]),
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

// With `rev`, the program of the reversed pattern: it matches the bytes of a
// match from the last to the first. An assertion keeps its kind, and the
// reverse DFA turns its context around.
function __rx_emit(prog: RInst[], n: RNode, rev: boolean): RInst[] {
  match (n) {
    RChar(c) => {
      return prog.append(IChar(c));
    },
    RFold(c) => {
      return prog.append(IFold(c));
    },
    RClass(tok) => {
      return prog.append(IClass(tok));
    },
    RUni(u) => {
      if (rev) {
        return __rx_emit_uni_rev(prog, u.nodes);
      }
      return __rx_emit_uni(prog, u.nodes);
    },
    RBound(k) => {
      return prog.append(IAssert(k));
    },
    RLazy(inner) => {
      return __rx_emit_loop(prog, inner, rev, true);
    },
    RGroup(gd) => {
      let pg = prog.append(ISave(gd.idx + gd.idx));
      pg = __rx_emit(pg, gd.node, rev);
      return pg.append(ISave(gd.idx + gd.idx + 1));
    },
    RSeq(xs) => {
      let ps = prog;
      let k = 0;
      while (k < xs.len()) {
        let x = if (rev) { xs[xs.len() - 1 - k] } else { xs[k] };
        ps = __rx_emit(ps, x, rev);
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
          pa = __rx_emit(pa, xs[k2], rev);
          jmps = jmps.append(pa.len());
          pa = pa.append(IJmp(0));
          pa = pa.with(sp, __rx_split(sp + 1, pa.len()));
        } else {
          pa = __rx_emit(pa, xs[k2], rev);
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
    _ => {
      return __rx_emit_loop(prog, n, rev, false);
    }
  }
}

// A split that prefers `a` to `b`, or `b` to `a` when `lazy`.
function __rx_pref(a: i32, b: i32, lazy: boolean): RInst {
  if (lazy) {
    return __rx_split(b, a);
  }
  return __rx_split(a, b);
}

// `*`, `+` or `?`. A lazy one prefers to leave the loop.
function __rx_emit_loop(prog: RInst[], n: RNode, rev: boolean, lazy: boolean): RInst[] {
  match (n) {
    RStar(inner) => {
      let s1 = prog.len();
      let pst = prog.append(__rx_split(0, 0));
      pst = __rx_emit(pst, inner, rev);
      let s2 = pst.len();
      pst = pst.append(__rx_split(0, 0));
      let send = pst.len();
      pst = pst.with(s1, __rx_pref(s1 + 1, send, lazy));
      return pst.with(s2, __rx_pref(s1 + 1, send, lazy));
    },
    RPlus(inner) => {
      let b1 = prog.len();
      let pp = __rx_emit(prog, inner, rev);
      let sp2 = pp.len();
      pp = pp.append(__rx_split(0, 0));
      return pp.with(sp2, __rx_pref(b1, sp2 + 1, lazy));
    },
    ROpt(inner) => {
      let s3 = prog.len();
      let po = prog.append(__rx_split(0, 0));
      po = __rx_emit(po, inner, rev);
      return po.with(s3, __rx_pref(s3 + 1, po.len(), lazy));
    },
    _ => {
      return prog;
    }
  }
}

// The instructions of the automaton `nodes`: node k at the next offset plus
// k, and the end of a character after the last node.
function __rx_emit_uni(prog: RInst[], nodes: i32[][]): RInst[] {
  let base = prog.len();
  let n = nodes.len();
  let out = prog;
  for es in nodes {
    let abs = es;
    let e = 0;
    while (e < es.len()) {
      abs = abs.with(e + 2, base + (if (es[e + 2] < 0) { n } else { es[e + 2] }));
      e += 3;
    }
    out = out.append(ISparse(abs));
  }
  return out;
}

// The instructions of the reverse of the automaton `nodes`. The end of a
// character comes first, node k is at the next offset plus 1 + k, and the
// start of a character is after them. Each edge goes the other way, so a
// byte can take more than one edge.
function __rx_emit_uni_rev(prog: RInst[], nodes: i32[][]): RInst[] {
  let base = prog.len();
  let n = nodes.len();
  let empty: i32[] = [];
  let rev: i32[][] = [];
  for _ in 0..n + 1 {
    rev = rev.append(empty);
  }
  for m in 0..n {
    let es = nodes[m];
    let to = if (m == 0) { base + n + 1 } else { base + 1 + m };
    let e = 0;
    while (e < es.len()) {
      let src = es[e + 2] + 1;
      rev = rev.with(src, rev[src].append(es[e]).append(es[e + 1]).append(to));
      e += 3;
    }
  }
  let out = prog;
  for es in rev {
    out = out.append(ISparse(es));
  }
  return out;
}

function __rx_cprog(root: RNode, rev: boolean): RInst[] {
  let prog: RInst[] = [];
  prog = prog.append(ISave(0));
  prog = __rx_emit(prog, root, rev);
  prog = prog.append(ISave(1));
  return prog.append(IMatch);
}

// Whether the assertion of kind `k` holds at `ti` of `t`.
function __rx_assert_ok(prog: Regex, k: i32, t: string, ti: i32): boolean {
  if (k >= RX_A_UWORD && k < RX_A_LINE) {
    return __rx_word_assert(k - 6, __rx_uword_before(prog.uword, t, ti), __rx_uword_after(prog.uword, t, ti));
  }
  return __rx_ctx_ok(k, __rx_ctx_at(t, ti));
}

// The context of offset `ti`. Bit 1 is the start of the text and bit 2 its
// end. Bits 4, 16 and 64 are a word byte, `\n` and `\r` before the offset,
// and bits 8, 32 and 128 the same after it.
function __rx_ctx_at(t: string, ti: i32): i32 {
  let ctx = if (ti == 0) { 1 } else { __rx_byte_ctx(t[ti - 1] as i32) };
  if (ti >= t.len()) {
    return ctx | 2;
  }
  return ctx | __rx_byte_ctx(t[ti] as i32) << 1;
}

// The context bits of a byte before an offset.
function __rx_byte_ctx(b: i32): i32 {
  if (__rx_is_word(b)) {
    return 4;
  }
  if (b == 10) {
    return 16;
  }
  if (b == 13) {
    return 64;
  }
  return 0;
}

// Whether the character after `ti` is a word character of Unicode. A byte
// that does not start a valid character is not.
function __rx_uword_after(word: i32[], t: string, ti: i32): boolean {
  if (ti >= t.len()) {
    return false;
  }
  if (t[ti] < 128) {
    return __rx_is_word(t[ti] as i32);
  }
  let Some(dec) = utf8.decode_at(t, ti) else {
    return false;
  };
  return __rx_in(word, dec.0);
}

// Whether the character before `ti` is a word character of Unicode.
function __rx_uword_before(word: i32[], t: string, ti: i32): boolean {
  if (ti == 0) {
    return false;
  }
  if (t[ti - 1] < 128) {
    return __rx_is_word(t[ti - 1] as i32);
  }
  let st = ti - 1;
  while (st > 0 && st > ti - 4 && t[st] >= 128 && t[st] < 192) {
    st -= 1;
  }
  let Some(dec) = utf8.decode_at(t, st) else {
    return false;
  };
  return st + dec.1 == ti && __rx_in(word, dec.0);
}

// Whether the ranges `rs` hold `cp`.
function __rx_in(rs: i32[], cp: i32): boolean {
  let lo = 0;
  let hi = rs.len() / 2 - 1;
  while (lo <= hi) {
    let mid = lo + (hi - lo) / 2;
    if (cp < rs[2 * mid]) {
      hi = mid - 1;
    } else if (cp > rs[2 * mid + 1]) {
      lo = mid + 1;
    } else {
      return true;
    }
  }
  return false;
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
// sparse instruction has its edges at operand a of `masks`: their count, and
// then the first byte, the last byte and the next instruction of each edge.
// A split prefers operand a to operand b. A save writes the slot in operand
// a, and an assertion has its kind in operand a.
const RX_CHAR: i32 = 0;

const RX_SPARSE: i32 = 1;

const RX_CLASS: i32 = 2;

const RX_MATCH: i32 = 3;

const RX_SPLIT: i32 = 4;

const RX_JMP: i32 = 5;

const RX_SAVE: i32 = 6;

const RX_ASSERT: i32 = 7;

// A search gets new memory when its generation stamp passes this value.
const RX_GEN_LIMIT: i32 = 1073741824;

function __rx_pack(prog: RInst[]): (i32[], i32[]) {
  let code = __alloc_i32(prog.len() * 3);
  let masks: i32[] = [];
  for pc in 0..prog.len() {
    let op = RX_MATCH;
    let a = 0;
    let b = 0;
    match (prog[pc]) {
      IChar(c) => {
        op = RX_CHAR;
        a = c;
        b = c;
      },
      IFold(c) => {
        op = RX_CHAR;
        a = c;
        b = c - 32;
      },
      IClass(tok) => {
        op = RX_CLASS;
        a = masks.len();
        masks = __rx_pack_mask(masks, tok);
      },
      ISparse(edges) => {
        op = RX_SPARSE;
        a = masks.len();
        masks = masks.append(edges.len() / 3);
        for x in edges {
          masks = masks.append(x);
        }
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
function __rx_pack_mask(own masks: i32[], tok: u8[]): i32[] {
  for w in 0..8 {
    let word = 0;
    for bit in 0..32 {
      if (tok[w * 32 + bit] != 0) {
        word |= 1 << bit;
      }
    }
    masks = masks.append(word);
  }
  return masks;
}

// The next instruction of the first edge of the sparse instruction at `at`
// of `masks` that takes `b`, or -1.
function __rx_sparse(masks: i32[], at: i32, b: i32): i32 {
  let end = at + 1 + 3 * masks[at];
  let e = at + 1;
  while (e < end) {
    if (b >= masks[e] && b <= masks[e + 1]) {
      return masks[e + 2];
    }
    e += 3;
  }
  return -1;
}
```

### `23 function __rx_exec(prog: Regex, t: string, from: i32, ns: i32, rule: RRule, anchored: boolean, own m: i32[]): i32[]`

The search runs the program once over the text, as a Pike VM. A list of threads holds at most one thread for each instruction. Thus all the state of a search fits in one array of `i32` words. The array has this layout, for a program of `n` instructions and `ns` capture slots:

| Words | Contents |
| --- | --- |
| 0 to 2 | the start of the match or -1, its end, and the last generation stamp |
| `n` | the generation stamp of each instruction: the step that visited it last |
| `ns` | the slots of the match |
| `n` and `n` | the instructions of the threads of the current list and of the next list, in priority order |
| `n * ns` and `n * ns` | the slots of the threads of the two lists, by instruction |
| `2 * n + 2` | the stack of the closure: an instruction to visit, or a slot to restore and its value |
| `ns` | the slots of the path that the closure follows |

A step makes a new generation stamp. Thus a step visited an instruction when the stamp of the instruction is the stamp of the step. The search does not clear a set of visited states. A caller that searches again with the same program gives the array back, and the search does not allocate.

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
// The step and the closure are one loop, so a step calls a function for a
// thread only at a sparse instruction.
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
        } else if (op == RX_SPARSE) {
          if (b >= 0) {
            entry = __rx_sparse(masks, code[3 * pc + 1], b);
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
            if (!__rx_assert_ok(prog, code[3 * pc + 1], t, npos)) {
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
  let m = __rx_next(prog, t, from, ns, __rx_rule(prog), []);
  if (!__rx_found(m)) {
    return [];
  }
  return __rx_slots(prog, m, ns);
}
```


### The lazy DFAs

`compile` also makes two DFAs of the program, a forward DFA and a reverse DFA. The forward DFA finds the end of the leftmost-first match, and the reverse DFA runs from that end to the start of the match. A search for a span uses the two DFAs, and a search for captures runs the Pike VM from the start that they find. A state of a DFA stands for a list of threads that the Pike VM can have. Thus a step of a DFA does the work of a step of the VM with one lookup in a table.

The DFAs are lazy, as in the `regex` crate. A table holds only the states that the searches found. A transition that is not known yet is 0, and a search that comes to it adds the state that it goes to. Thus the automaton of a large set of characters, such as `\w`, costs only the states that the text uses. The table is in a `Cell` of the compiled value, so a `Regex` stays a value that a worker can take. A table that grows over `RX_DFA_WORDS` words starts again with no state.

A state of the forward DFA keeps its threads in priority order. A state also records if a new thread starts at the next offset. A thread starts at each offset until a state holds a match. A match stops the threads after it, and it stops the new threads. Thus the forward DFA follows the rule of the Pike VM.

The search records the offset of the last state that holds a match, and it stops at a state that holds no thread. When the search comes back to a state that only starts a thread, it goes to the next candidate offset.

The reverse DFA runs the program of the reversed pattern, from the end of the match to the left, and it keeps each thread. It holds a match at each offset where a match that ends at the end offset starts. The leftmost of these offsets is the start of the leftmost-first match, because no match starts before the leftmost start.

An assertion examines the bytes on each side of an offset. A column of the table is a class of bytes together with the kind of the byte that follows. The kinds are the classes of bytes that the assertions of the program tell apart: a word byte, `\n`, `\r`, another byte, and the end of the text. A first state comes from the kinds on each side of its offset.

A word assertion of Unicode examines the characters on each side, and a DFA knows only bytes. Thus a byte outside ASCII next to an offset stops such a DFA. Then the Pike VM does that search, as in the `regex` crate.

A program over `RX_DFA_PROGRAM` instructions has no DFA, and the Pike VM does all of its searches.

| Words | Contents |
| --- | --- |
| 0, 1, 2 | the count of columns, the count of contexts after an offset, and the count of byte kinds |
| 3 to 258 | the first column of the class of each byte |
| 259 to 514 | the kind of each byte |
| 515 to 550 | the offset of the first state for each context, or 0 |
| 551 to 556 | the context bits of each kind |
| 557 to 812 | a byte of each class |
| 813 | the state that the last fill found |
| from 814 | the dead state, the quit state, and then the other states |

A state is a word of flags, the offset of the next state for each column, and its key: the flag of a new thread, the count of threads, and the threads.

```fern
<<lazy DFA>>=
// A DFA of one direction. `init` is its table with no state, and empty when
// the program has no DFA. `tab` is the table now, `idx` the hash index of
// its states, and `work` the memory of a closure.
struct RDfa { code: i32[], masks: i32[], forward: boolean, init: i32[], tab: Cell[i32[]], idx: Cell[i32[]], work: Cell[i32[]] }

// The limits of a DFA.
const RX_DFA_PROGRAM: i32 = 20000;

const RX_DFA_WORDS: i32 = 1048576;

const RX_DFA_COLS: i32 = 3;

const RX_DFA_LA: i32 = 259;

const RX_DFA_START: i32 = 515;

const RX_DFA_PROPS: i32 = 551;

const RX_DFA_REP: i32 = 557;

const RX_DFA_NEXT: i32 = 813;

const RX_DFA_HEAD: i32 = 814;

// The value of a cell while a search holds its table.
const RX_NO_TABLE: i32[] = [];

// Flags of a DFA state: it holds a match, or it only starts a thread.
const RX_DFA_MATCH: i32 = 1;

const RX_DFA_RESTART: i32 = 2;

// The context bit of a byte outside ASCII before an offset, for a Unicode
// word assertion. The DFA stops at it.
const RX_DFA_QUIT: i32 = 256;

// The DFA of a packed program.
function __rx_dfa_of(code: i32[], masks: i32[], forward: boolean): RDfa {
  let empty: i32[] = [];
  let init = if (code.len() == 0 || code.len() / 3 > RX_DFA_PROGRAM) { empty } else { __rx_dfa_init(code, masks) };
  return RDfa {
    code: code, masks: masks, forward: forward, init: init, tab: cell_new(init), idx: cell_new(__rx_dfa_index(init)),
    work: cell_new(empty),
  };
}

function __rx_has_dfa(prog: Regex): boolean {
  return prog.fwd.init.len() > 0;
}

// The table of a DFA with only the dead state and the quit state.
function __rx_dfa_init(code: i32[], masks: i32[]): i32[] {
  let need = __rx_dfa_needs(code);
  let tab = __alloc_i32(RX_DFA_HEAD);
  let nkinds = 0;
  for b in 0..256 {
    let props = __rx_dfa_props(b, need);
    let kind = nkinds;
    for k in 0..nkinds {
      if (tab[RX_DFA_PROPS + k] == props) {
        kind = k;
      }
    }
    if (kind == nkinds) {
      tab = tab.with(RX_DFA_PROPS + kind, props);
      nkinds += 1;
    }
    tab = tab.with(RX_DFA_LA + b, kind);
  }
  let nctx = if (need < 0) { 1 } else { nkinds + 1 };
  let marks = __rx_dfa_bounds(code, masks, tab);
  let cls = 0;
  for b in 0..256 {
    if (marks[b] != 0) {
      cls += 1;
      tab = tab.with(RX_DFA_REP + cls, b);
    }
    tab = tab.with(RX_DFA_COLS + b, cls * nctx);
  }
  let ncols = (cls + 1) * nctx;
  tab = tab.with(0, ncols);
  tab = tab.with(1, nctx);
  tab = tab.with(2, nkinds);
  tab = __rx_dfa_row(tab, RX_DFA_HEAD, ncols, 0);
  return __rx_dfa_row(tab, RX_DFA_HEAD + ncols + 3, ncols, -1);
}

// Appends a state that each transition keeps, with a key of `flag` and no
// thread. No list of threads has the key -1.
function __rx_dfa_row(own tab: i32[], self: i32, ncols: i32, flag: i32): i32[] {
  tab = tab.append(0);
  for _ in 0..ncols {
    tab = tab.append(self);
  }
  tab = tab.append(flag);
  return tab.append(0);
}

// The context bits that the assertions of a program read, or -1 for a
// program with no assertion.
function __rx_dfa_needs(code: i32[]): i32 {
  let need = -1;
  for pc in 0..code.len() / 3 {
    if (code[3 * pc] == RX_ASSERT) {
      let base = if (need < 0) { 0 } else { need };
      need = base | __rx_assert_needs(code[3 * pc + 1]);
    }
  }
  return need;
}

function __rx_assert_needs(k: i32): i32 {
  if (k < RX_A_WORD) {
    return 0;
  }
  if (k < RX_A_UWORD) {
    return 4;
  }
  if (k < RX_A_LINE) {
    return 4 | RX_DFA_QUIT;
  }
  if (k < RX_A_CRLF) {
    return 16;
  }
  return 80;
}

// The context bits of byte `b` before an offset, of those in `need`.
function __rx_dfa_props(b: i32, need: i32): i32 {
  if (need <= 0) {
    return 0;
  }
  if (b >= 128) {
    return RX_DFA_QUIT & need;
  }
  return __rx_byte_ctx(b) & need;
}

// The bytes that start a class: where an instruction that takes bytes, or
// the kind of a byte, changes from the byte before.
function __rx_dfa_bounds(code: i32[], masks: i32[], tab: i32[]): u8[] {
  let marks = __alloc_u8(257);
  for b in 1..256 {
    if (tab[RX_DFA_LA + b] != tab[RX_DFA_LA + b - 1]) {
      marks = marks.with(b, 1);
    }
  }
  for pc in 0..code.len() / 3 {
    marks = __rx_dfa_mark(marks, code, masks, pc);
  }
  return marks.with(0, 0);
}

function __rx_dfa_mark(own marks: u8[], code: i32[], masks: i32[], pc: i32): u8[] {
  let op = code[3 * pc];
  let a = code[3 * pc + 1];
  if (op == RX_CHAR) {
    return __rx_mark_range(__rx_mark_range(marks, a, a), code[3 * pc + 2], code[3 * pc + 2]);
  }
  if (op == RX_CLASS) {
    for b in 1..256 {
      if (__rx_mask_bit(masks, a, b) != __rx_mask_bit(masks, a, b - 1)) {
        marks = marks.with(b, 1);
      }
    }
  } else if (op == RX_SPARSE) {
    let e = a + 1;
    while (e < a + 1 + 3 * masks[a]) {
      marks = __rx_mark_range(marks, masks[e], masks[e + 1]);
      e += 3;
    }
  }
  return marks;
}

function __rx_mark_range(own marks: u8[], lo: i32, hi: i32): u8[] {
  marks = marks.with(lo, 1);
  return marks.with(hi + 1, 1);
}

function __rx_mask_bit(masks: i32[], at: i32, b: i32): i32 {
  return masks[at + (b >> 5)] >> (b & 31) & 1;
}

// The hash index of the dead state of `tab`: the count of states, and then
// the offset of a state in each slot, or 0.
function __rx_dfa_index(tab: i32[]): i32[] {
  let empty: i32[] = [];
  if (tab.len() == 0) {
    return empty;
  }
  return __rx_dfa_insert(__alloc_i32(65), tab, RX_DFA_HEAD);
}

function __rx_dfa_insert(own idx: i32[], tab: i32[], off: i32): i32[] {
  let mask = idx.len() - 2;
  let ko = off + tab[0] + 1;
  let i = __rx_dfa_hash(tab[ko], tab, ko + 2, tab[ko + 1]) & mask;
  while (idx[1 + i] != 0) {
    i = i + 1 & mask;
  }
  idx = idx.with(1 + i, off);
  idx = idx.with(0, idx[0] + 1);
  if (idx[0] * 2 <= mask) {
    return idx;
  }
  let grown = __alloc_i32(2 * mask + 3);
  for k in 1..idx.len() {
    if (idx[k] != 0) {
      grown = __rx_dfa_insert(grown, tab, idx[k]);
    }
  }
  return grown;
}

// The offset of the state with the key `flag` and the `count` threads at
// `at` of `w`, or 0.
function __rx_dfa_find(idx: i32[], tab: i32[], flag: i32, w: i32[], at: i32, count: i32): i32 {
  let mask = idx.len() - 2;
  let i = __rx_dfa_hash(flag, w, at, count) & mask;
  while (idx[1 + i] != 0) {
    let off = idx[1 + i];
    if (__rx_dfa_same(tab, off + tab[0] + 1, flag, w, at, count)) {
      return off;
    }
    i = i + 1 & mask;
  }
  return 0;
}

function __rx_dfa_hash(flag: i32, xs: i32[], at: i32, count: i32): i32 {
  let h = flag * 31 + count + 7;
  for k in 0..count {
    h = (h * 31 + xs[at + k] + 1) % 1000003;
  }
  return h;
}

function __rx_dfa_same(keys: i32[], ko: i32, flag: i32, xs: i32[], at: i32, count: i32): boolean {
  if (keys[ko] != flag || keys[ko + 1] != count) {
    return false;
  }
  for k in 0..count {
    if (keys[ko + 2 + k] != xs[at + k]) {
      return false;
    }
  }
  return true;
}

// The context of an offset with a byte of kind `before` on its left and of
// kind `after` on its right, or -1 when a kind stops the DFA. The kind after
// the last kind is the start of the text on the left and its end on the
// right. The reverse DFA reads from the right, so it turns the context
// around.
function __rx_dfa_ctx(dfa: RDfa, d: i32[], before: i32, after: i32): i32 {
  let nk = d[2];
  let b = if (before == nk) { 1 } else { d[RX_DFA_PROPS + before] };
  let a = if (after == nk) { 2 } else { d[RX_DFA_PROPS + after] << 1 };
  if (((b | a) & 3 * RX_DFA_QUIT) != 0) {
    return -1;
  }
  if (dfa.forward) {
    return b | a;
  }
  return (b & 1) << 1 | (b & 84) << 1 | (a & 2) >> 1 | (a & 168) >> 1;
}

// The memory of a closure, with a new generation stamp and an empty list.
// The first words are the stamp and the length of the list. Then come a
// stamp for each instruction, the list, and a stack.
function __rx_dfa_work(dfa: RDfa): i32[] {
  let n = dfa.code.len() / 3;
  let w = dfa.work.get();
  let empty: i32[] = [];
  dfa.work.set(empty);
  if (w.len() < 3 + 3 * n + 1 || w[0] > RX_GEN_LIMIT) {
    w = __alloc_i32(3 + 3 * n + 1);
  }
  w = w.with(0, w[0] + 1);
  return w.with(1, 0);
}

// Fills the transition of the state at `s` for the column `col`. The state
// that it goes to is in word RX_DFA_NEXT of the table.
function __rx_dfa_fill(dfa: RDfa, own d: i32[], s: i32, col: i32): i32[] {
  let nctx = d[1];
  let byte = d[RX_DFA_REP + col / nctx];
  let ctx = __rx_dfa_ctx(dfa, d, d[RX_DFA_LA + byte], col % nctx);
  let quit = RX_DFA_HEAD + d[0] + 3;
  if (ctx < 0) {
    d = d.with(s + 1 + col, quit);
    return d.with(RX_DFA_NEXT, quit);
  }
  let w = __rx_dfa_work(dfa);
  let ko = s + d[0] + 1;
  let inject = d[ko] != 0;
  for k in 0..d[ko + 1] {
    let pc = d[ko + 2 + k];
    if (dfa.code[3 * pc] != RX_MATCH) {
      w = __rx_dfa_step(dfa, pc, byte, ctx, w);
    } else if (dfa.forward) {
      inject = false;
      break;
    }
  }
  if (inject) {
    w = __rx_dfa_close(dfa.code, 0, ctx, w);
  }
  d = __rx_dfa_state(dfa, d, w, inject);
  dfa.work.set(w);
  if (d[RX_DFA_NEXT] == quit) {
    return d;
  }
  return d.with(s + 1 + col, d[RX_DFA_NEXT]);
}

// Fills the first state of the start context `si`, and puts it in word
// RX_DFA_NEXT. The first state of the forward DFA starts a thread.
function __rx_dfa_fill_start(dfa: RDfa, own d: i32[], si: i32): i32[] {
  let nctx = d[1];
  let ctx = __rx_dfa_ctx(dfa, d, si / nctx, si % nctx);
  let quit = RX_DFA_HEAD + d[0] + 3;
  if (ctx < 0) {
    d = d.with(RX_DFA_START + si, quit);
    return d.with(RX_DFA_NEXT, quit);
  }
  let w = __rx_dfa_work(dfa);
  w = __rx_dfa_close(dfa.code, 0, ctx, w);
  d = __rx_dfa_state(dfa, d, w, dfa.forward);
  dfa.work.set(w);
  let to = d[RX_DFA_NEXT];
  if (to == quit) {
    return d;
  }
  d = d.with(RX_DFA_START + si, to);
  if (d[to + d[0] + 1] != 0) {
    d = d.with(to, d[to] | RX_DFA_RESTART);
  }
  return d;
}

// Adds the threads that the thread at `pc` gives after `byte`.
function __rx_dfa_step(dfa: RDfa, pc: i32, byte: i32, ctx: i32, own w: i32[]): i32[] {
  let code = dfa.code;
  let masks = dfa.masks;
  let op = code[3 * pc];
  let a = code[3 * pc + 1];
  if (op == RX_SPARSE) {
    let e = a + 1;
    while (e < a + 1 + 3 * masks[a]) {
      if (byte >= masks[e] && byte <= masks[e + 1]) {
        w = __rx_dfa_close(code, masks[e + 2], ctx, w);
      }
      e += 3;
    }
    return w;
  }
  if (op == RX_CHAR && (byte == a || byte == code[3 * pc + 2]) || op == RX_CLASS && __rx_mask_bit(masks, a, byte) == 1) {
    return __rx_dfa_close(code, pc + 1, ctx, w);
  }
  return w;
}

// Puts the state of the threads in `w` in word RX_DFA_NEXT, and adds it
// when it is new. In the forward DFA, a match stops the threads after it
// and the new threads. A table over RX_DFA_WORDS starts again from `init`,
// with the quit state.
function __rx_dfa_state(dfa: RDfa, own d: i32[], w: i32[], start: boolean): i32[] {
  let code = dfa.code;
  let list = 3 + code.len() / 3;
  let count = w[1];
  let inject = start;
  let matched = false;
  for k in 0..count {
    if (code[3 * w[list + k]] == RX_MATCH) {
      matched = true;
      if (dfa.forward) {
        count = k + 1;
        inject = false;
      }
      break;
    }
  }
  let flag = if (inject) { 1 } else { 0 };
  let idx = dfa.idx.get();
  let empty: i32[] = [];
  dfa.idx.set(empty);
  let off = __rx_dfa_find(idx, d, flag, w, list, count);
  if (off == 0 && d.len() + d[0] + 3 + count > RX_DFA_WORDS) {
    dfa.idx.set(__rx_dfa_index(dfa.init));
    return dfa.init.with(RX_DFA_NEXT, RX_DFA_HEAD + d[0] + 3);
  }
  if (off == 0) {
    off = d.len();
    d = d.append(if (matched) { RX_DFA_MATCH } else { 0 });
    for _ in 0..d[0] {
      d = d.append(0);
    }
    d = d.append(flag);
    d = d.append(count);
    for k in 0..count {
      d = d.append(w[list + k]);
    }
    idx = __rx_dfa_insert(idx, d, off);
  }
  dfa.idx.set(idx);
  return d.with(RX_DFA_NEXT, off);
}

// Adds the threads of the closure of `entry` to the list in `w`, in priority
// order.
function __rx_dfa_close(code: i32[], entry: i32, ctx: i32, own w: i32[]): i32[] {
  let n = code.len() / 3;
  let gen = w[0];
  let out = 3 + n;
  let stk = out + n;
  let count = w[1];
  w = w.with(stk, entry);
  let sp = 1;
  while (sp > 0) {
    sp -= 1;
    let pc = w[stk + sp];
    while (w[3 + pc] != gen) {
      w = w.with(3 + pc, gen);
      let op = code[3 * pc];
      if (op == RX_SPLIT) {
        w = w.with(stk + sp, code[3 * pc + 2]);
        sp += 1;
        pc = code[3 * pc + 1];
      } else if (op == RX_JMP) {
        pc = code[3 * pc + 1];
      } else if (op == RX_SAVE) {
        pc += 1;
      } else if (op == RX_ASSERT) {
        if (!__rx_ctx_ok(code[3 * pc + 1], ctx)) {
          break;
        }
        pc += 1;
      } else {
        w = w.with(out + count, pc);
        count += 1;
        break;
      }
    }
  }
  return w.with(1, count);
}

// Whether the assertion of kind `k` holds in the context `ctx`. A Unicode
// word assertion reads the bytes as ASCII, because a byte outside ASCII
// next to the offset stops the DFA first.
function __rx_ctx_ok(k: i32, ctx: i32): boolean {
  if (k < RX_A_WORD) {
    return (ctx & 1 << k) != 0;
  }
  if (k < RX_A_LINE) {
    let w = if (k >= RX_A_UWORD) { k - 6 } else { k };
    return __rx_word_assert(w, (ctx & 4) != 0, (ctx & 8) != 0);
  }
  if (k == RX_A_LINE) {
    return (ctx & 17) != 0;
  }
  if (k == RX_A_LINE + 1) {
    return (ctx & 34) != 0;
  }
  if (k == RX_A_CRLF) {
    return (ctx & 17) != 0 || (ctx & 64) != 0 && (ctx & 32) == 0;
  }
  return (ctx & 130) != 0 || (ctx & 32) != 0 && (ctx & 64) == 0;
}

// The end of the leftmost-first match that starts at or after the candidate
// offset `c`, -1 when there is none, or -2 when the DFA stops. With
// `exists`, the end of the first match that the DFA finds.
//
// The search takes the table out of its cell, so that a fill changes it in
// place, and puts it back at the end. The loop calls a function for a byte
// only to fill a transition.
// fern-lint: allow cyclomatic-complexity
function __rx_dfa_end(prog: Regex, t: string, c: i32, exists: boolean): i32 {
  let d = prog.fwd.tab.get();
  prog.fwd.tab.set(RX_NO_TABLE);
  if (d.len() == 0) {
    return -2;
  }
  let nctx = d[1];
  let quit = RX_DFA_HEAD + d[0] + 3;
  let len = t.len();
  let pos = c;
  let cand = c;
  let skip = prog.first.len() > 0;
  let end = -1;
  let s = 0;
  let si = __rx_dfa_first(d, t, pos, nctx);
  while (true) {
    if (si >= 0) {
      s = d[RX_DFA_START + si];
      if (s == 0) {
        d = __rx_dfa_fill_start(prog.fwd, d, si);
        s = d[RX_DFA_NEXT];
      }
      si = -1;
      if (s <= quit) {
        end = if (s == quit) { -2 } else { end };
        break;
      }
    }
    let f = d[s];
    if ((f & RX_DFA_MATCH) != 0) {
      end = pos;
      if (exists) {
        break;
      }
    }
    if (pos >= len) {
      break;
    }
    if ((f & RX_DFA_RESTART) != 0 && skip && pos > cand) {
      cand = prog.next_candidate(t, pos);
      if (cand < 0) {
        break;
      }
      if (cand > pos) {
        pos = cand;
        si = __rx_dfa_first(d, t, pos, nctx);
        continue;
      }
    }
    let kind = 0;
    if (nctx > 1) {
      kind = nctx - 1;
      if (pos + 1 < len) {
        kind = d[RX_DFA_LA + t[pos + 1] as i32];
      }
    }
    let col = d[RX_DFA_COLS + t[pos] as i32] + kind;
    let nxt = d[s + 1 + col];
    if (nxt <= quit) {
      if (nxt == 0) {
        d = __rx_dfa_fill(prog.fwd, d, s, col);
        nxt = d[RX_DFA_NEXT];
      }
      if (nxt <= quit) {
        end = if (nxt == quit) { -2 } else { end };
        break;
      }
    }
    s = nxt;
    pos += 1;
  }
  prog.fwd.tab.set(d);
  return end;
}

// The index of the first state of a forward search at `p`.
function __rx_dfa_first(d: i32[], t: string, p: i32, nctx: i32): i32 {
  let before = if (p == 0) { d[2] } else { d[RX_DFA_LA + t[p - 1] as i32] };
  if (nctx == 1) {
    return before;
  }
  let after = if (p >= t.len()) { nctx - 1 } else { d[RX_DFA_LA + t[p] as i32] };
  return before * nctx + after;
}

// The index of the first state of a reverse search at `e`. The bytes after
// `e` come before it in the reverse text, and the end of the text is its
// start.
function __rx_dfa_last(d: i32[], t: string, e: i32, nctx: i32): i32 {
  let before = if (e == t.len()) { d[2] } else { d[RX_DFA_LA + t[e] as i32] };
  if (nctx == 1) {
    return before;
  }
  let after = if (e == 0) { nctx - 1 } else { d[RX_DFA_LA + t[e - 1] as i32] };
  return before * nctx + after;
}

// The leftmost start at or after `lo` of a match that ends at `e`, -1 when
// there is none, or -2 when the DFA stops.
// It reads the text from `e` to the left.
// fern-lint: allow cyclomatic-complexity
function __rx_dfa_start(prog: Regex, t: string, e: i32, lo: i32): i32 {
  let d = prog.rev.tab.get();
  prog.rev.tab.set(RX_NO_TABLE);
  if (d.len() == 0) {
    return -2;
  }
  let nctx = d[1];
  let quit = RX_DFA_HEAD + d[0] + 3;
  let si = __rx_dfa_last(d, t, e, nctx);
  let s = d[RX_DFA_START + si];
  if (s == 0) {
    d = __rx_dfa_fill_start(prog.rev, d, si);
    s = d[RX_DFA_NEXT];
  }
  let p = e;
  let start = if (s == quit) { -2 } else { -1 };
  while (s > quit) {
    if ((d[s] & RX_DFA_MATCH) != 0) {
      start = p;
    }
    if (p <= lo) {
      break;
    }
    let kind = 0;
    if (nctx > 1) {
      kind = nctx - 1;
      if (p > 1) {
        kind = d[RX_DFA_LA + t[p - 2] as i32];
      }
    }
    let col = d[RX_DFA_COLS + t[p - 1] as i32] + kind;
    let nxt = d[s + 1 + col];
    if (nxt <= quit) {
      if (nxt == 0) {
        d = __rx_dfa_fill(prog.rev, d, s, col);
        nxt = d[RX_DFA_NEXT];
      }
      if (nxt <= quit) {
        start = if (nxt == quit) { -2 } else { start };
        break;
      }
    }
    s = nxt;
    p -= 1;
  }
  prog.rev.tab.set(d);
  return start;
}

// The count of the matches from their ends alone. No match is empty, so each
// search starts at the end of the last match.
function __rx_dfa_count(prog: Regex, t: string): i32 {
  let count = 0;
  let pos = 0;
  while (pos <= t.len()) {
    let c = prog.next_candidate(t, pos);
    if (c < 0) {
      return count;
    }
    let e = __rx_dfa_end(prog, t, c, false);
    if (e == -2) {
      return count + __rx_count_from(prog, t, pos);
    }
    if (e < 0) {
      return count;
    }
    count += 1;
    pos = e;
  }
  return count;
}

// The span of the leftmost-first match at or after `from`, (-1, -1) when
// there is none, or (-2, -2) when a DFA stops.
function __rx_dfa_span(prog: Regex, t: string, from: i32): (i32, i32) {
  let c = prog.next_candidate(t, from);
  if (c < 0) {
    return (-1, -1);
  }
  let e = __rx_dfa_end(prog, t, c, false);
  if (e < 0) {
    return (e, e);
  }
  let s = __rx_dfa_start(prog, t, e, c);
  if (s == -2) {
    return (-2, -2);
  }
  return (s, e);
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
// The group of the reference at the `$` at `i` of a template, and the
// offset after the reference, as the `regex` crate reads it: `$$`, `${name}`,
// or `$` and the longest run of letters, digits and `_`. A name of digits
// is a number. The group is -2 for `$$`, and -1 for a group that the
// expression does not have. A `$` before anything else is no reference,
// and the offset stays `i`.
function __rx_template_ref(tmpl: string, i: i32, c: Captures): (i32, i32) {
  if (i + 1 < tmpl.len() && tmpl[i + 1] == 36) {
    return (-2, i + 2);
  }
  let lo = i + 1;
  let hi = lo;
  let end = lo;
  if (lo < tmpl.len() && tmpl[lo] == 123) {
    lo += 1;
    hi = __memchr(tmpl, 125, lo);
    end = hi + 1;
  } else {
    while (hi < tmpl.len() && (__rx_is_word(tmpl[hi] as i32))) {
      hi += 1;
    }
    end = hi;
  }
  if (hi <= lo) {
    return (-1, i);
  }
  return (__rx_group_ref(c, slice_unchecked(tmpl, lo, hi) + ""), end);
}

// The group of a name in a template: a number, or the name of a group.
function __rx_group_ref(c: Captures, name: string): i32 {
  let n = 0;
  for k in 0..name.len() {
    let d = name[k] as i32 - 48;
    if (d < 0 || d > 9 || n > c.starts.len()) {
      return __rx_named_index(c, name);
    }
    n = n * 10 + d;
  }
  return n;
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
  UnknownEscape,
  UnknownProperty,
}

pub enum TextError { InvalidUtf8Output }

function __rx_child_size(child: RNode, overhead: i32, budget: i32): i32 {
  let size = __rx_program_size(child, budget - overhead);
  if (size < 0) {
    return -1;
  }
  return overhead + size;
}

// Records the first error of a parse.
function __rx_error(cx: RCtx, kind: CompileErrorKind, offset: i32): void {
  if let None = cx.errors.get() {
    cx.errors.set(Some(CompileError { kind: kind, offset: offset }));
  }
}

// Records an error and ends the parse.
function __rx_fail(cx: RCtx, kind: CompileErrorKind, offset: i32, g: i32, f: i32): RParse {
  __rx_error(cx, kind, offset);
  return RParse { node: RSeq([]), pos: cx.p.len(), g: g, f: f };
}

// An item of a set: one character `cp`, or the set `rs` when `cp` is -1.
// `pos` is the offset after it, or -1 after an error.
struct RItem { rs: i32[], cp: i32, pos: i32 }

function __rx_bad_item(): RItem {
  return RItem { rs: [], cp: -1, pos: -1 };
}

// The escape at `i`.
function __rx_escape(cx: RCtx, i: i32, g: i32, f: i32): RParse {
  let p = cx.p;
  if (i + 1 < p.len() && __memchr("bB<>Az", p[i + 1] as i32, 0) >= 0) {
    return __rx_assertion(cx, i, g, f);
  }
  let item = __rx_class_escape(cx, i, f);
  if (item.pos < 0) {
    return RParse { node: RSeq([]), pos: p.len(), g: g, f: f };
  }
  if (item.cp < 0) {
    return RParse { node: __rx_set(item.rs, f), pos: item.pos, g: g, f: f };
  }
  return RParse { node: __rx_cp_node(item.cp, f), pos: item.pos, g: g, f: f };
}

// The escape at `i` as an item of a set: `\d`, `\s`, `\w`, their
// complements, a property, or one character.
function __rx_class_escape(cx: RCtx, i: i32, f: i32): RItem {
  let p = cx.p;
  if (i + 1 >= p.len()) {
    __rx_error(cx, TrailingEscape, i);
    return __rx_bad_item();
  }
  let d = p[i + 1] as i32;
  let perl = __rx_perl(d, f);
  if (perl.len() > 0) {
    return RItem { rs: perl, cp: -1, pos: i + 2 };
  }
  if (d == 112 || d == 80) {  // \p \P
    return __rx_property(cx, i, f);
  }
  return __rx_char_escape(cx, i, f);
}

// The set of `\d`, `\s` or `\w` for the letter `d`, or the complement for
// its capital. Another letter gives [].
function __rx_perl(d: i32, f: i32): i32[] {
  let lower = d | 32;
  let uni = (f & RX_U) != 0;
  let rs: i32[] = [];
  if (lower == 100) {
    rs = if (uni) { __rx_prop("Nd") } else { [48, 57] };
  } else if (lower == 115) {
    rs = if (uni) { __rx_prop("White_Space") } else { [9, 13, 32, 32] };
  } else if (lower == 119) {
    rs = if (uni) { unicode.word_ranges() } else { [48, 57, 65, 90, 95, 95, 97, 122] };
  } else {
    return rs;
  }
  if (d < 97) {
    return __rx_negate(rs, f);
  }
  return rs;
}

function __rx_prop(name: string): i32[] {
  let Some(rs) = unicode.property_ranges(name) else {
    return [];
  };
  return rs;
}

// `\pL`, `\p{...}`, `\PL` or `\P{...}` at `i`. `\P` and `!=` give the
// complement. A property needs `(?u)`.
function __rx_property(cx: RCtx, i: i32, f: i32): RItem {
  let p = cx.p;
  let lo = i + 2;
  let hi = i + 3;
  if (lo < p.len() && p[lo] == 123) {  // '{'
    lo += 1;
    hi = __memchr(p, 125, lo);
  }
  if (hi < lo || hi > p.len() || (f & RX_U) == 0) {
    __rx_error(cx, UnknownProperty, i);
    return __rx_bad_item();
  }
  let name = slice_unchecked(p, lo, hi) + "";
  let negated = p[i + 1] == 80;
  let bang = __memchr(name, 33, 0);
  if (bang >= 0 && bang + 1 < name.len() && name[bang + 1] == 61) {
    name = slice_unchecked(name, 0, bang) + slice_unchecked(name, bang + 1, name.len());
    negated = !negated;
  }
  let Some(found) = unicode.property_ranges(name) else {
    __rx_error(cx, UnknownProperty, i);
    return __rx_bad_item();
  };
  let rs = if ((f & RX_I) != 0) { __rx_fold_ranges(found, f) } else { found };
  if (negated) {
    rs = __rx_negate(rs, f);
  }
  return RItem { rs: rs, cp: -1, pos: if (p[lo - 1] == 123) { hi + 1 } else { hi } };
}

// The character of the escape at `i`: a control character, a code point in
// hexadecimal, or an ASCII mark that is not a letter or a digit.
function __rx_char_escape(cx: RCtx, i: i32, f: i32): RItem {
  let d = cx.p[i + 1] as i32;
  let at = __memchr("ntrfva", d, 0);
  if (at >= 0) {
    return RItem { rs: [], cp: [10, 9, 13, 12, 11, 7][at], pos: i + 2 };
  }
  if (d == 120 || d == 117 || d == 85) {  // x u U
    return __rx_hex_escape(cx, i, f);
  }
  if (d < 128 && (d == 95 || !__rx_is_word(d))) {
    return RItem { rs: [], cp: d, pos: i + 2 };
  }
  __rx_error(cx, UnknownEscape, i);
  return __rx_bad_item();
}

// `\xHH`, `\uHHHH`, `\UHHHHHHHH`, or one of them with the digits in braces.
// Under `(?-u)`, the value is a byte.
function __rx_hex_escape(cx: RCtx, i: i32, f: i32): RItem {
  let p = cx.p;
  let width = if (p[i + 1] == 120) { 2 } else if (p[i + 1] == 117) { 4 } else { 8 };
  let lo = i + 2;
  let hi = lo + width;
  let end = hi;
  if (lo < p.len() && p[lo] == 123) {
    lo += 1;
    hi = __memchr(p, 125, lo);
    end = hi + 1;
  }
  let cp = if (hi < lo || hi > p.len()) { -1 } else { __rx_hex(p, lo, hi) };
  if (cp < 0 || cp > __rx_top(f) || cp >= 55296 && cp <= 57343) {
    __rx_error(cx, UnknownEscape, i);
    return __rx_bad_item();
  }
  return RItem { rs: [], cp: cp, pos: end };
}

// The value of the hexadecimal digits from `lo` to `hi`, or -1.
function __rx_hex(p: string, lo: i32, hi: i32): i32 {
  if (hi == lo || hi - lo > 8) {
    return -1;
  }
  let v = 0;
  for k in lo..hi {
    let c = p[k] as i32 | 32;
    let digit = if (c >= 48 && c <= 57) { c - 48 } else if (c >= 97 && c <= 102) { c - 87 } else { -1 };
    if (digit < 0 || v > RX_MAX_CP) {
      return -1;
    }
    v = v * 16 + digit;
  }
  return v;
}

// `\b`, `\B`, `\<`, `\>`, `\A` or `\z` at `i`, and `\b{start}`, `\b{end}`,
// `\b{start-half}` and `\b{end-half}`. A word assertion is of Unicode under
// `(?u)`.
function __rx_assertion(cx: RCtx, i: i32, g: i32, f: i32): RParse {
  let p = cx.p;
  let at = __memchr("AzbB<>", p[i + 1] as i32, 0);
  if (at < 2) {
    return RParse { node: RBound(at), pos: i + 2, g: g, f: f };
  }
  let k = at;
  let pos = i + 2;
  if (at == 2 && pos < p.len() && p[pos] == 123) {
    let close = __memchr(p, 125, pos);
    k = if (close < 0) { -1 } else { __rx_boundary_kind(slice_unchecked(p, pos + 1, close) + "") };
    if (k < 0) {
      return __rx_fail(cx, UnknownBoundary, i, g, f);
    }
    pos = close + 1;
  }
  let shift = if ((f & RX_U) != 0) { RX_A_UWORD - RX_A_WORD } else { 0 };
  return RParse { node: RBound(k + shift), pos: pos, g: g, f: f };
}

function __rx_boundary_kind(name: string): i32 {
  return match (name) {
    "start" => 4,
    "end" => 5,
    "start-half" => 6,
    "end-half" => 7,
    _ => -1,
  };
}

// The bracket class at `i`.
function __rx_bracket(cx: RCtx, i: i32, g: i32, f: i32): RParse {
  let item = __rx_class_body(cx, i, f);
  if (item.pos < 0) {
    return RParse { node: RSeq([]), pos: cx.p.len(), g: g, f: f };
  }
  return RParse { node: __rx_set(item.rs, f), pos: item.pos, g: g, f: f };
}

// The class from the `[` at `i` to its `]`: lists of items, with `&&`, `--`
// or `~~` between two lists for the intersection, the difference or the
// symmetric difference. A `^` after the `[` gives the complement.
function __rx_class_body(cx: RCtx, i: i32, f: i32): RItem {
  let p = cx.p;
  let pos = i + 1;
  let negated = pos < p.len() && p[pos] == 94;
  if (negated) {
    pos += 1;
  }
  let acc = __rx_class_list(cx, i, pos, f);
  while (acc.pos >= 0 && __rx_set_op(p, acc.pos) != 0) {
    let rhs = __rx_class_list(cx, i, acc.pos + 2, f);
    if (rhs.pos < 0) {
      return rhs;
    }
    acc = RItem { rs: __rx_combine(__rx_set_op(p, acc.pos), acc.rs, rhs.rs, f), cp: -1, pos: rhs.pos };
  }
  if (acc.pos < 0) {
    return acc;
  }
  let rs = if ((f & RX_I) != 0) { __rx_fold_ranges(acc.rs, f) } else { acc.rs };
  if (negated) {
    rs = __rx_negate(rs, f);
  }
  return RItem { rs: rs, cp: -1, pos: acc.pos + 1 };
}

// The items of a class from `start` to its `]` or to a set operation. `open`
// is the offset of the `[`. A `]` that comes first is a literal.
function __rx_class_list(cx: RCtx, open: i32, start: i32, f: i32): RItem {
  let p = cx.p;
  let rs: i32[] = [];
  let pos = start;
  while (true) {
    pos = __rx_skip(p, pos, f);
    if (pos >= p.len()) {
      __rx_error(cx, UnclosedClass, open);
      return __rx_bad_item();
    }
    if (p[pos] == 93 && pos > start || __rx_set_op(p, pos) != 0) {
      return RItem { rs: __rx_norm(rs), cp: -1, pos: pos };
    }
    let item = __rx_class_range(cx, pos, f);
    if (item.pos < 0) {
      return item;
    }
    rs = rs.concat(item.rs);
    pos = item.pos;
  }
  return __rx_bad_item();
}

// `&&`, `--` or `~~` at `pos`: 1, 2 or 3, else 0.
function __rx_set_op(p: string, pos: i32): i32 {
  if (pos + 1 >= p.len() || p[pos] != p[pos + 1]) {
    return 0;
  }
  return __memchr("&-~", p[pos] as i32, 0) + 1;
}

function __rx_combine(op: i32, a: i32[], b: i32[], f: i32): i32[] {
  if (op == 1) {
    return __rx_intersect(a, b);
  }
  let minus = __rx_intersect(a, __rx_negate(b, f));
  if (op == 2) {
    return minus;
  }
  return __rx_norm(minus.concat(__rx_intersect(b, __rx_negate(a, f))));
}

// An item at `pos`, or a range of two characters with `-` between them.
function __rx_class_range(cx: RCtx, pos: i32, f: i32): RItem {
  let p = cx.p;
  let lo = __rx_class_item(cx, pos, f);
  let dash = lo.pos;
  if (lo.pos < 0 || lo.cp < 0 || dash + 1 >= p.len() || p[dash] != 45 || p[dash + 1] == 93) {
    if (lo.cp < 0) {
      return lo;
    }
    return RItem { rs: [lo.cp, lo.cp], cp: -1, pos: lo.pos };
  }
  let hi = __rx_class_item(cx, dash + 1, f);
  if (hi.pos >= 0 && hi.cp < lo.cp) {
    __rx_error(cx, InvalidClassRange, pos);
    return __rx_bad_item();
  }
  return RItem { rs: [lo.cp, hi.cp], cp: -1, pos: hi.pos };
}

// One item of a class at `pos`: a character, an escape, a POSIX class such
// as `[:alpha:]`, or a nested class.
function __rx_class_item(cx: RCtx, pos: i32, f: i32): RItem {
  let p = cx.p;
  let c = p[pos] as i32;
  if (c == 91) {  // '['
    let posix = __rx_posix(p, pos, f);
    if (posix.pos > 0) {
      return posix;
    }
    return __rx_class_body(cx, pos, f);
  }
  if (c == 92) {  // '\'
    return __rx_class_escape(cx, pos, f);
  }
  let Some(dec) = utf8.decode_at(p, pos) else {
    return RItem { rs: [], cp: c, pos: pos + 1 };
  };
  return RItem { rs: [], cp: dec.0, pos: pos + dec.1 };
}

// The ASCII class `[:name:]` or `[:^name:]` at `pos`. Another `[` gives an
// item with `pos` 0.
function __rx_posix(p: string, pos: i32, f: i32): RItem {
  let none = RItem { rs: [], cp: -1, pos: 0 };
  if (pos + 1 >= p.len() || p[pos + 1] != 58) {
    return none;
  }
  let close = __memchr(p, 58, pos + 2);
  if (close < 0 || close + 1 >= p.len() || p[close + 1] != 93) {
    return none;
  }
  let negated = p[pos + 2] == 94;
  let from = if (negated) { pos + 3 } else { pos + 2 };
  let rs = __rx_posix_ranges(slice_unchecked(p, from, close) + "");
  if (rs.len() == 0) {
    return none;
  }
  return RItem { rs: if (negated) { __rx_negate(rs, f) } else { rs }, cp: -1, pos: close + 2 };
}

function __rx_posix_ranges(name: string): i32[] {
  let none: i32[] = [];
  return match (name) {
    "alnum" => [48, 57, 65, 90, 97, 122],
    "alpha" => [65, 90, 97, 122],
    "ascii" => [0, 127],
    "blank" => [9, 9, 32, 32],
    "cntrl" => [0, 31, 127, 127],
    "digit" => [48, 57],
    "graph" => [33, 126],
    "lower" => [97, 122],
    "print" => [32, 126],
    "punct" => [33, 47, 58, 64, 91, 96, 123, 126],
    "space" => [9, 13, 32, 32],
    "upper" => [65, 90],
    "word" => [48, 57, 65, 90, 95, 95, 97, 122],
    "xdigit" => [48, 57, 65, 70, 97, 102],
    _ => none,
  };
}

function __rx_group(cx: RCtx, depth: i32, i: i32, g: i32, f: i32): RParse {
  if (depth >= MAX_NESTING) {
    return __rx_fail(cx, NestingLimit, i, g, f);
  }
  if (i + 1 < cx.p.len() && cx.p[i + 1] == 63) {  // "(?"
    return __rx_special_group(cx, depth, i, g, f);
  }
  return __rx_capture(cx, depth, i, i + 1, g, f, "");
}

// The capturing group at `i` whose body starts at `body`. It claims number
// `g`, and the groups in its body come after it.
function __rx_capture(cx: RCtx, depth: i32, i: i32, body: i32, g: i32, f: i32, name: string): RParse {
  cx.names.set(cx.names.get().append(name));
  let inner = __rx_alt(cx, depth + 1, body, g + 1, f);
  if (inner.pos >= cx.p.len() || cx.p[inner.pos] != 41) {
    return __rx_fail(cx, UnclosedGroup, i, g, f);
  }
  return RParse { node: RGroup(RGroupData { idx: g, node: inner.node }), pos: inner.pos + 1, g: inner.g, f: f };
}

// A group that starts with `(?`: `(?<name>...)`, `(?P<name>...)`, flags
// for the rest of the group, or flags and a group that does not capture.
function __rx_special_group(cx: RCtx, depth: i32, i: i32, g: i32, f: i32): RParse {
  let p = cx.p;
  let at = i + 2;
  if (at < p.len() && p[at] == 80) {  // 'P'
    at += 1;
  }
  if (at < p.len() && p[at] == 60) {  // '<'
    let close = __memchr(p, 62, at + 1);
    if (close <= at + 1 || p[at + 1] == 61 || p[at + 1] == 33) {
      return __rx_fail(cx, UnsupportedGroup, i, g, f);
    }
    return __rx_capture(cx, depth, i, close + 1, g, f, slice_unchecked(p, at + 1, close) + "");
  }
  let (nf, end) = __rx_flags(cx, i, f);
  if (end < 0) {
    return RParse { node: RSeq([]), pos: p.len(), g: g, f: f };
  }
  if (p[end] == 41) {  // ')'
    return RParse { node: RSeq([]), pos: end + 1, g: g, f: nf };
  }
  let inner = __rx_alt(cx, depth + 1, end + 1, g, nf);
  if (inner.pos >= p.len() || p[inner.pos] != 41) {
    return __rx_fail(cx, UnclosedGroup, i, g, f);
  }
  return RParse { node: inner.node, pos: inner.pos + 1, g: inner.g, f: f };
}

// The flags of `(?flags)` or `(?flags:` at `i`, and the offset of its `)` or
// `:`, or -1 after an error. A `-` turns off the flags after it.
function __rx_flags(cx: RCtx, i: i32, f: i32): (i32, i32) {
  let p = cx.p;
  let out = f;
  let off = false;
  let count = 0;
  let pos = i + 2;
  while (pos < p.len() && p[pos] != 41 && p[pos] != 58) {
    let bit = __rx_flag_bit(p[pos] as i32);
    if (p[pos] == 45 && !off) {
      off = true;
      count = 0;
    } else if (bit == 0) {
      __rx_error(cx, UnsupportedGroup, i);
      return (f, -1);
    } else {
      out = if (off) { out & ~bit } else { out | bit };
      count += 1;
    }
    pos += 1;
  }
  if (pos >= p.len() || count == 0 && (off || p[pos] == 41)) {
    __rx_error(cx, UnsupportedGroup, i);
    return (f, -1);
  }
  return (out, pos);
}

// The bit of a flag letter, or 0.
function __rx_flag_bit(c: i32): i32 {
  let at = __memchr("imsxuRU", c, 0);
  if (at < 0) {
    return 0;
  }
  return 1 << at;
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
    RLazy(child) => {
      return __rx_program_size(child, budget);
    },
    RUni(u) => {
      if (budget <= u.nodes.len()) {
        return -1;
      }
      return u.nodes.len() + 1;
    },
    _ => {
      if (budget < 1) {
        return -1;
      }
      return 1;
    }
  }
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
    RLazy(inner) => {
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

// True for a node that always matches at least one byte.
function __rx_consumes(n: RNode): boolean {
  match (n) {
    RChar(_) => {
      return true;
    },
    RFold(_) => {
      return true;
    },
    RClass(_) => {
      return true;
    },
    RUni(_) => {
      return true;
    },
    _ => {
      return false;
    }
  }
}

// Marks in `set` each byte that can begin a match of `n`.
function __rx_first_into(n: RNode, own set: u8[]): u8[] {
  match (n) {
    RSeq(xs) => {
      for x in xs {
        set = __rx_first_into(x, set);
        if (!__rx_nullable(x)) {
          return set;
        }
      }
      return set;
    },
    RAlt(xs) => {
      for x in xs {
        set = __rx_first_into(x, set);
      }
      return set;
    },
    RStar(inner) => {
      return __rx_first_into(inner, set);
    },
    RPlus(inner) => {
      return __rx_first_into(inner, set);
    },
    ROpt(inner) => {
      return __rx_first_into(inner, set);
    },
    RLazy(inner) => {
      return __rx_first_into(inner, set);
    },
    RGroup(gd) => {
      return __rx_first_into(gd.node, set);
    },
    _ => {
      return __rx_first_byte_into(n, set);
    }
  }
}

// Marks the first bytes of a node that matches a character. An assertion
// marks none.
function __rx_first_byte_into(n: RNode, own set: u8[]): u8[] {
  match (n) {
    RChar(c) => {
      return set.with(c, 1);
    },
    RFold(c) => {
      set = set.with(c, 1);
      return set.with(c - 32, 1);
    },
    RClass(mask) => {
      for b in 0..256 {
        if (mask[b] != 0) {
          set = set.with(b, 1);
        }
      }
      return set;
    },
    RUni(u) => {
      return __rx_mark_edges(set, u.nodes[0]);
    },
    _ => {
      return set;
    }
  }
}

// Marks the bytes of the edges `es`.
function __rx_mark_edges(own set: u8[], es: i32[]): u8[] {
  let e = 0;
  while (e < es.len()) {
    for b in es[e]..es[e + 1] + 1 {
      set = set.with(b, 1);
    }
    e += 3;
  }
  return set;
}

function __rx_first_set(root: RNode): u8[] {
  if (__rx_nullable(root)) {
    return [];
  }
  let set = __rx_first_into(root, __alloc_u8(256));
  for b in 0..256 {
    if (set[b] == 0) {
      return set;
    }
  }
  return [];
}

// The literal that starts each match: its bytes, whether they hold ASCII
// letters in either case as lower case bytes, and whether a match that
// starts with a byte outside ASCII can skip it.
struct RPrefix { bytes: u8[], folded: boolean, loose: boolean }

// The prefix of `root`. A folded prefix holds no letter of one case. A
// first set of characters whose ASCII members are a letter in its two cases,
// such as the case orbit of `s` or `k`, starts a loose prefix. A match that
// starts with one of its other characters skips the prefix.
function __rx_prefix(root: RNode): RPrefix {
  let xs = match (root) {
    RSeq(items) => items,
    _ => [root],
  };
  let out = RPrefix { bytes: [], folded: false, loose: false };
  let letters = false;
  for x in xs {
    let (c, kind) = __rx_prefix_byte(x, out.bytes.len() == 0);
    if (!__rx_prefix_fits(c, kind > 0, letters, out.folded)) {
      return out;
    }
    letters = letters || kind == 0 && __rx_is_letter(c);
    out = RPrefix { bytes: out.bytes.append(c as u8), folded: out.folded || kind > 0, loose: out.loose || kind == 2 };
  }
  return out;
}

// The byte of `x` in a prefix, or -1, and its kind: 0 for a byte, 1 for a
// folded letter, and 2 for the first set of a loose prefix.
function __rx_prefix_byte(x: RNode, first: boolean): (i32, i32) {
  return match (x) {
    RChar(b) => (b, 0),
    RFold(b) => (b, 1),
    RUni(u) => (if (first) { __rx_ascii_pair(u.ranges) } else { -1 }, 2),
    _ => (-1, 0),
  };
}

// Whether the byte `c` can extend a prefix. A folded prefix has no letter
// of one case.
function __rx_prefix_fits(c: i32, fold: boolean, letters: boolean, folded: boolean): boolean {
  if (c < 0) {
    return false;
  }
  if (fold) {
    return !letters;
  }
  return !folded || !__rx_is_letter(c);
}

function __rx_is_letter(c: i32): boolean {
  return (c | 32) >= 97 && (c | 32) <= 122;
}

// The lower case letter of a set whose ASCII members are a letter in its
// two cases, and which has a member outside ASCII, or -1.
function __rx_ascii_pair(rs: i32[]): i32 {
  if (rs.len() < 6 || rs[0] != rs[1] || rs[2] != rs[3] || rs[4] < 128) {
    return -1;
  }
  if (rs[0] < 65 || rs[0] > 90 || rs[2] != rs[0] + 32) {
    return -1;
  }
  return rs[2];
}

function __rx_has_prefix(prog: Regex, text: string, at: i32): boolean {
  if (prog.loose && at < text.len() && text[at] >= 128) {
    return true;
  }
  if (at + prog.prefix.len() > text.len()) {
    return false;
  }
  for k in 0..prog.prefix.len() {
    let b = text[at + k] as i32;
    if (prog.ci && b >= 65 && b <= 90) {
      b += 32;
    }
    if (b != prog.prefix[k] as i32) {
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

// The word ranges of Unicode when the program has a Unicode word
// assertion, else [].
function __rx_uword(code: i32[]): i32[] {
  for pc in 0..code.len() / 3 {
    let k = code[3 * pc + 1];
    if (code[3 * pc] == RX_ASSERT && k >= RX_A_UWORD && k < RX_A_LINE) {
      return unicode.word_ranges();
    }
  }
  return [];
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

// The captures of the first match that starts at or after `start`. The
// assertions see the whole text, as for `find_at`.
pub function (prog: Regex) captures_at(text: string, start: i32): Option[Captures] {
  if (start > text.len()) {
    return None;
  }
  let caps = __rx_csearch_from(prog, text, if (start < 0) { 0 } else { start });
  return __rx_caps_wrap(caps, text, prog.ngroups, prog.names);
}

// `tmpl` with each reference to a group made the text of that group, as
// `replace_groups` expands it. A group that did not match is empty.
pub function (c: Captures) expand_bytes(tmpl: string): u8[] {
  let h = buf_new(tmpl.len());
  __rx_expand_into(h, tmpl, c);
  return __rx_finish(h);
}

pub function (prog: Regex) captures_all(text: string): Captures[] {
  let rule = __rx_rule(prog);
  let ns = (prog.ngroups + 1) * 2;
  let m: i32[] = [];
  let out: Captures[] = [];
  let pos = 0;
  while (pos <= text.len()) {
    m = __rx_next(prog, text, pos, ns, rule, m);
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
  let none: string[] = [];
  let cx = RCtx { errors: cell_new(None), names: cell_new(none), p: pattern };
  let r = __rx_alt(cx, 0, 0, 1, RX_U);
  if let Some(e) = cx.errors.get() {
    return Err(e);
  }
  if (r.pos != pattern.len()) {
    return Err(CompileError { kind: UnexpectedClose, offset: r.pos });
  }
  if (__rx_program_size(r.node, MAX_PROGRAM - 3) < 0) {
    return Err(CompileError { kind: ProgramLimit, offset: pattern.len() });
  }
  let first = __rx_first_set(r.node);
  let (code, masks) = __rx_pack(__rx_cprog(r.node, false));
  let fwd = __rx_dfa_of(code, masks, true);
  let empty: i32[] = [];
  let (rcode, rmasks) = if (fwd.init.len() > 0) { __rx_pack(__rx_cprog(r.node, true)) } else { (empty, empty) };
  let pre = __rx_prefix(r.node);
  return Ok(Regex {
    ngroups: r.g - 1, names: cx.names.get(), code: code, masks: masks, fwd: fwd, rev: __rx_dfa_of(rcode, rmasks, false),
    first: first, lead: __rx_lead(first), prefix: pre.bytes, ci: pre.folded, loose: pre.loose, leftmost_longest: false,
    empty: __rx_nullable(r.node), uword: __rx_uword(code),
  });
}

pub function (prog: Regex) count(text: string): i32 {
  if (__rx_has_dfa(prog) && !prog.leftmost_longest && !prog.empty) {
    return __rx_dfa_count(prog, text);
  }
  return __rx_count_from(prog, text, 0);
}

// The count of the matches at or after `from`.
function __rx_count_from(prog: Regex, text: string, from: i32): i32 {
  let rule = __rx_rule(prog);
  let m: i32[] = [];
  let count = 0;
  let pos = from;
  while (pos <= text.len()) {
    m = __rx_next(prog, text, pos, 1, rule, m);
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
    m = __rx_next(prog, text, pos, 1, rule, m);
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
  if (__rx_has_dfa(prog)) {
    let s = __rx_dfa_start(prog, text, text.len(), 0);
    if (s != -2) {
      return s == 0;
    }
  }
  let m = __rx_exec(prog, text, 0, 1, RuleLongest, true, []);
  return __rx_found(m) && m[1] == text.len();
}

// A copy of `prog` that gives the longest match at the leftmost start, as
// POSIX does.
pub function (prog: Regex) longest(): Regex {
  return Regex { ...prog, leftmost_longest: true };
}

pub function (prog: Regex) is_match(text: string): boolean {
  if (__rx_has_dfa(prog)) {
    let c = prog.next_candidate(text, 0);
    if (c < 0) {
      return false;
    }
    let e = __rx_dfa_end(prog, text, c, true);
    if (e != -2) {
      return e >= 0;
    }
    return __rx_found(__rx_exec(prog, text, c, 0, RuleExists, false, []));
  }
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
    mem = __rx_next(prog, text, pos, 1, rule, mem);
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
    mem = __rx_next(prog, text, pos, 1, rule, mem);
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
    },
    UnknownEscape => {
      why = "unknown escape";
    },
    UnknownProperty => {
      why = "unknown Unicode property";
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

// `fwd` and `rev` are the forward and the reverse DFA. `first` marks each
// byte that can begin a match. It is empty when a match can be empty or can
// begin with any byte. `lead` is the one byte that `first` marks, or -1.
// Each match starts with the bytes of `prefix`, and `ci` says that they are
// ASCII letters in either case, as lower case bytes. With `loose`, a match
// that starts with a byte outside ASCII can skip the prefix. `leftmost_longest`
// selects the longest match at the leftmost start in place of the first.
// `empty` says that a match can be empty. `uword` holds the word characters
// of Unicode when an assertion needs them.
pub struct Regex {
  ngroups: i32,
  names: string[],
  code: i32[],
  masks: i32[],
  fwd: RDfa,
  rev: RDfa,
  first: u8[],
  lead: i32,
  prefix: u8[],
  ci: boolean,
  loose: boolean,
  leftmost_longest: boolean,
  empty: boolean,
  uword: i32[],
}
```

### Module introduction

```fern
<<module introduction>>=
// Compiled regular expressions with checked syntax and UTF-8 output.
```
