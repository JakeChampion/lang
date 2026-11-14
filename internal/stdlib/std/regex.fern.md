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

`inner` gives the literal bytes that each match holds, when the prefix of the pattern does not hold as many. `next_inner` finds the next copy of those bytes. A caller that searches line by line can skip each line that does not hold them.

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

<<errors of regex-syntax>>
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

// The decimal at `at`, and the offset after it. As in regex-syntax, white
// space can come before and after the digits, and under `(?x)` between
// them. A missing number gives -1, and a number over MAX_REPEAT records an
// error and gives -2.
function __rx_decimal(cx: RCtx, at: i32, pos: i32, f: i32): (i32, i32) {
  let p = cx.p;
  let j = __rx_white(p, at, 0);
  let start = j;
  let n = 0;
  while (j < p.len() && p[j] >= 48 && p[j] <= 57) {
    n = n * 10 + (p[j] as i32 - 48);
    if (n > MAX_REPEAT) {
      __rx_error(cx, RepeatLimit, pos);
      return (-2, j);
    }
    j = __rx_skip(p, j + 1, f);
  }
  if (j == start) {
    return (-1, j);
  }
  return (n, __rx_white(p, j, f));
}

// The offset after the white space at `i`, and under `(?x)` after the
// comments too.
function __rx_white(p: string, i: i32, f: i32): i32 {
  let j = i;
  while (j < p.len() && __sx_is_ws(__sx_cp(p, j))) {
    j = __rx_skip(p, j + __sx_len(p, j), f);
  }
  return j;
}

// `{n}`, `{n,}` or `{n,m}` at `pos`.
function __rx_bounds(cx: RCtx, pos: i32, f: i32): RBounds {
  let p = cx.p;
  let bad = RBounds { n: 0, m: 0, inf: false, end: -2 };
  let (n, j) = __rx_decimal(cx, __rx_skip(p, pos + 1, f), pos, f);
  if (n == -2) {
    return bad;
  }
  let b = RBounds { n: n, m: n, inf: false, end: j + 1 };
  if (n >= 0 && j < p.len() && p[j] == 44) {  // ','
    b = __rx_upper_bound(cx, pos, f, n, j);
    if (b.end == -2) {
      return bad;
    }
  }
  if (n < 0 || b.end > p.len() || p[b.end - 1] != 125 || !b.inf && b.m < n) {
    __rx_error(cx, InvalidRepeat, pos);
    return bad;
  }
  return RBounds { ...b, end: __rx_skip(p, b.end, f) };
}

// The bounds `{n,}` or `{n,m}` from the `,` at `comma`.
function __rx_upper_bound(cx: RCtx, pos: i32, f: i32, n: i32, comma: i32): RBounds {
  let p = cx.p;
  let k = __rx_skip(p, comma + 1, f);
  let (m, e) = if (k < p.len() && p[k] == 125) { (-1, k) } else { __rx_decimal(cx, k, pos, f) };
  return RBounds { n: n, m: m, inf: m < 0, end: if (m == -2) { -2 } else { e + 1 } };
}

// The repetition at `pos`: `*`, `+`, `?` or a count.
function __rx_quantifier(cx: RCtx, pos: i32, f: i32): RBounds {
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
    return __rx_bounds(cx, pos, f);
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
    let b = __rx_quantifier(cx, pos, r.f);
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
  let n = __rx_utf8_len(s);
  out = out.append(n);
  for k in 0..4 {
    if (k < n) {
      out = out.append(__rx_utf8_byte(s, n, k)).append(__rx_utf8_byte(e, n, k));
    } else {
      out = out.append(0).append(0);
    }
  }
  return out;
}

// The count of bytes of the UTF-8 form of the code point `c`.
function __rx_utf8_len(c: i32): i32 {
  if (c < 128) {
    return 1;
  }
  if (c < 2048) {
    return 2;
  }
  if (c < 65536) {
    return 3;
  }
  return 4;
}

// The byte `k` of the UTF-8 form of the code point `c`, which has `n` bytes.
function __rx_utf8_byte(c: i32, n: i32, k: i32): i32 {
  let rest = c >> 6 * (n - 1 - k);
  if (k > 0) {
    return 128 | rest & 63;
  }
  if (n == 2) {
    return 192 | rest;
  }
  if (n == 3) {
    return 224 | rest;
  }
  if (n == 4) {
    return 240 | rest;
  }
  return c;
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
  let heads: Map[i64, i32] = map_new(64);
  let chain: i32[] = [];
  let canon = __alloc_i32(trie.len());
  let out: i32[][] = [];
  let k = trie.len() - 1;
  while (k >= 0) {
    let es = __rx_merge_edges(trie[k], canon);
    let h = __rx_edges_hash(es);
    let head = heads.get_or(h, -1);
    let id = head;
    while (id >= 0 && !__rx_same_edges(out[id], es)) {
      id = chain[id];
    }
    if (id < 0) {
      id = out.len();
      out = out.append(es);
      chain = chain.append(head);
      heads = heads.insert(h, id);
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

// A hash of the edges `es`. Two nodes with one hash are compared word by
// word.
function __rx_edges_hash(es: i32[]): i64 {
  let h = 1469598103934665603i64;
  for x in es {
    h = (h ^ x as i64) * 1099511628211i64;
  }
  return h;
}

function __rx_same_edges(a: i32[], b: i32[]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  for i in 0..a.len() {
    if (a[i] != b[i]) {
      return false;
    }
  }
  return true;
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

`compile` fills each state of a small table at once: the count of instructions times the count of columns is at most `RX_DFA_FILLS`, and the table has at most `RX_DFA_FULL` words. A DFA has about one state for each instruction, so the product tells the work of the fill before it starts. A class of Unicode gives many columns, and its table stays lazy. A search with such a table does not read a cell, and it does not check for a state that is not filled. The dead state and the quit state have the flag `RX_DFA_STOP`, so the search reads one word of flags for each byte, as it does for a match. Thus a search of a short text costs as little as it did before the DFAs were lazy.

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

// The flag of the dead state and the quit state: a search stops at them.
const RX_DFA_STOP: i32 = 4;

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
  tab = tab.append(RX_DFA_STOP);
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

// The limits of a table that `compile` fills completely: the instructions of
// its program times its columns, and the words of the table. A fill costs
// time on each compile, also in the interpreter.
const RX_DFA_FILLS: i32 = 4096;

const RX_DFA_FULL: i32 = 8192;

// The table with each state that a search can reach, or [] when the
// instructions times the columns are more than RX_DFA_FILLS or the table has
// more than RX_DFA_FULL words. With such a table, a search does not check for
// a state that is not filled. The quit state stays where a Unicode word
// assertion stops the DFA.
function __rx_dfa_full(dfa: RDfa): i32[] {
  let empty: i32[] = [];
  if (dfa.init.len() == 0 || dfa.code.len() / 3 * dfa.init[0] > RX_DFA_FILLS) {
    return empty;
  }
  let limit = RX_DFA_FULL;
  let d = dfa.tab.get();
  dfa.tab.set(RX_NO_TABLE);
  if (d.len() == 0) {
    return empty;
  }
  d = __rx_dfa_fill_starts(dfa, d);
  let s = RX_DFA_HEAD + 2 * (d[0] + 3);
  while (s < d.len() && d.len() <= limit) {
    d = __rx_dfa_fill_row(dfa, d, s, limit);
    s += d[0] + 3 + d[s + d[0] + 2];
  }
  let ok = d.len() <= limit;
  dfa.tab.set(d);
  return if (ok) { d } else { empty };
}

// Fills each first state.
function __rx_dfa_fill_starts(dfa: RDfa, own d: i32[]): i32[] {
  let slots = if (d[1] == 1) { d[2] + 1 } else { (d[2] + 1) * d[1] };
  for si in 0..slots {
    if (d[RX_DFA_START + si] == 0) {
      d = __rx_dfa_fill_start(dfa, d, si);
    }
  }
  return d;
}

// Fills each transition of the state at `s` that is not known, until the
// table has more than `limit` words.
function __rx_dfa_fill_row(dfa: RDfa, own d: i32[], s: i32, limit: i32): i32[] {
  let col = 0;
  while (col < d[0] && d.len() <= limit) {
    if (d[s + 1 + col] == 0) {
      d = __rx_dfa_fill(dfa, d, s, col);
    }
    col += 1;
  }
  return d;
}

// The end of the leftmost-first match that starts at or after the candidate
// offset `c`, -1 when there is none, or -2 when the DFA stops. With
// `exists`, the end of the first match that the DFA finds.
//
// A program with a full table runs __rx_full_end, which has no fill and no
// quit state. The others run the lazy DFA.
@inline
function __rx_dfa_end(prog: Regex, t: string, c: i32, exists: boolean): i32 {
  if (prog.full.len() > 0) {
    return __rx_full_end(prog, t, c, exists);
  }
  return __rx_lazy_end(prog, t, c, exists);
}

// The end of a match as __rx_dfa_end finds it, with the full table.
// fern-lint: allow cyclomatic-complexity
function __rx_full_end(prog: Regex, t: string, c: i32, exists: boolean): i32 {
  let d = prog.full;
  let nctx = d[1];
  let len = t.len();
  let pos = c;
  let cand = c;
  let s = d[RX_DFA_START + __rx_dfa_first(d, t, pos, nctx)];
  let skip = prog.first.len() > 0;
  let end = -1;
  while (true) {
    let f = d[s];
    if ((f & RX_DFA_MATCH) != 0) {
      end = pos;
      if (exists) {
        break;
      }
    }
    if ((f & RX_DFA_STOP) != 0) {
      return if (s == RX_DFA_HEAD) { end } else { -2 };
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
        s = d[RX_DFA_START + __rx_dfa_first(d, t, pos, nctx)];
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
    s = d[s + 1 + d[RX_DFA_COLS + t[pos] as i32] + kind];
    pos += 1;
  }
  return end;
}

// The end of a match as __rx_dfa_end finds it, with the lazy DFA.
//
// The search takes the table out of its cell, so that a fill changes it in
// place, and puts it back at the end. The loop calls a function for a byte
// only to fill a transition.
// fern-lint: allow cyclomatic-complexity
function __rx_lazy_end(prog: Regex, t: string, c: i32, exists: boolean): i32 {
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

// The index of the first state of a forward search at `p`. A program with
// no assertion has one context, so each first state is the same.
@inline
function __rx_dfa_first(d: i32[], t: string, p: i32, nctx: i32): i32 {
  if (nctx == 1) {
    return 0;
  }
  let before = if (p == 0) { d[2] } else { d[RX_DFA_LA + t[p - 1] as i32] };
  let after = if (p >= t.len()) { nctx - 1 } else { d[RX_DFA_LA + t[p] as i32] };
  return before * nctx + after;
}

// The index of the first state of a reverse search at `e`. The bytes after
// `e` come before it in the reverse text, and the end of the text is its
// start.
@inline
function __rx_dfa_last(d: i32[], t: string, e: i32, nctx: i32): i32 {
  if (nctx == 1) {
    return 0;
  }
  let before = if (e == t.len()) { d[2] } else { d[RX_DFA_LA + t[e] as i32] };
  let after = if (e == 0) { nctx - 1 } else { d[RX_DFA_LA + t[e - 1] as i32] };
  return before * nctx + after;
}

// The leftmost start at or after `lo` of a match that ends at `e`, -1 when
// there is none, or -2 when the DFA stops.
// It reads the text from `e` to the left.
@inline
function __rx_dfa_start(prog: Regex, t: string, e: i32, lo: i32): i32 {
  if (prog.rfull.len() > 0) {
    return __rx_full_start(prog.rfull, t, e, lo);
  }
  return __rx_lazy_start(prog, t, e, lo);
}

// The start of a match as __rx_dfa_start finds it, with the full table `d`.
function __rx_full_start(d: i32[], t: string, e: i32, lo: i32): i32 {
  let nctx = d[1];
  let s = d[RX_DFA_START + __rx_dfa_last(d, t, e, nctx)];
  let p = e;
  let start = -1;
  while (true) {
    let f = d[s];
    if ((f & RX_DFA_STOP) != 0) {
      return if (s == RX_DFA_HEAD) { start } else { -2 };
    }
    if ((f & RX_DFA_MATCH) != 0) {
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
    s = d[s + 1 + d[RX_DFA_COLS + t[p - 1] as i32] + kind];
    p -= 1;
  }
  return start;
}

// The start of a match as __rx_dfa_start finds it, with the lazy DFA.
// fern-lint: allow cyclomatic-complexity
function __rx_lazy_start(prog: Regex, t: string, e: i32, lo: i32): i32 {
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
  if (prog.full.len() > 0) {
    return __rx_full_count(prog, t);
  }
  return __rx_lazy_count(prog, t);
}

// The count of the matches as __rx_dfa_count finds it, with the full table.
function __rx_full_count(prog: Regex, t: string): i32 {
  let count = 0;
  let pos = 0;
  while (pos <= t.len()) {
    let c = prog.next_candidate(t, pos);
    if (c < 0) {
      return count;
    }
    let e = __rx_full_end(prog, t, c, false);
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

// The count of the matches as __rx_dfa_count finds it, with the lazy DFA.
@noinline
function __rx_lazy_count(prog: Regex, t: string): i32 {
  let count = 0;
  let pos = 0;
  while (pos <= t.len()) {
    let c = prog.next_candidate(t, pos);
    if (c < 0) {
      return count;
    }
    let e = __rx_lazy_end(prog, t, c, false);
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
    while (hi < tmpl.len() && __rx_is_word(tmpl[hi] as i32)) {
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

// `\pL`, `\p{...}`, `\PL` or `\P{...}` at `i`. `\P` gives the complement.
// `!=` selects a value as `=` does, as in regex-syntax 0.8.2. A property
// needs `(?u)`.
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
  if ((f & RX_X) != 0) {
    return __rx_hex_spaced(cx, i, f, width);
  }
  let lo = i + 2;
  let hi = lo + width;
  let end = hi;
  if (lo < p.len() && p[lo] == 123) {
    lo += 1;
    hi = __memchr(p, 125, lo);
    end = hi + 1;
  }
  return __rx_hex_item(cx, i, f, if (hi < lo || hi > p.len()) { -1 } else { __rx_hex(p, lo, hi) }, end);
}

// The item of the code point `cp` of a hexadecimal escape at `i`, or an
// error for a value that is not a character.
function __rx_hex_item(cx: RCtx, i: i32, f: i32, cp: i32, end: i32): RItem {
  if (cp < 0 || cp > __rx_top(f) || cp >= 55296 && cp <= 57343) {
    __rx_error(cx, UnknownEscape, i);
    return __rx_bad_item();
  }
  return RItem { rs: [], cp: cp, pos: end };
}

// A hexadecimal escape under `(?x)`, where white space and comments can
// come between its parts.
function __rx_hex_spaced(cx: RCtx, i: i32, f: i32, width: i32): RItem {
  let p = cx.p;
  let j = __rx_skip(p, i + 2, f);
  let brace = j < p.len() && p[j] == 123;
  let hex = "";
  if (brace) {
    j = __rx_skip(p, j + 1, f);
  }
  while (j < p.len() && (if (brace) { p[j] != 125 } else { hex.len() < width })) {
    hex += slice_unchecked(p, j, j + 1);
    j = __rx_skip(p, j + 1, f);
  }
  if (brace) {
    j = if (j < p.len()) { __rx_skip(p, j + 1, f) } else { p.len() + 1 };
  }
  return __rx_hex_item(cx, i, f, if (j > p.len()) { -1 } else { __rx_hex(hex, 0, hex.len()) }, j);
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
  let after = __rx_skip(p, pos + 1, f);
  let named = after < p.len() && __sx_boundary_char(p[after]);
  if (at == 2 && pos < p.len() && p[pos] == 123 && named) {
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
  if (lo.pos < 0 || lo.cp < 0 || dash + 1 >= p.len() || p[dash] != 45 || p[dash + 1] == 93 || p[dash + 1] == 45) {
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
  let n = prog.prefix.len();
  if (at + n > text.len() || n > 0 && __rx_fold(text[at] as i32, prog.ci) != prog.prefix[0] as i32) {
    return __rx_loose_at(prog, text, at);
  }
  for k in 1..n {
    if (__rx_fold(text[at + k] as i32, prog.ci) != prog.prefix[k] as i32) {
      return false;
    }
  }
  return true;
}

// A byte outside ASCII at `at` can start a match of a loose prefix.
function __rx_loose_at(prog: Regex, text: string, at: i32): boolean {
  return prog.loose && at < text.len() && text[at] >= 128;
}

// The byte `b`, in lower case when `ci` and it is an ASCII capital.
@inline
function __rx_fold(b: i32, ci: boolean): i32 {
  return if (ci && b >= 65 && b <= 90) { b + 32 } else { b };
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
  let req = __rx_required(r.node).best;
  let inner = if (!pre.folded && !pre.loose && pre.bytes.len() >= req.len()) { [] } else { req };
  let rev = __rx_dfa_of(rcode, rmasks, false);
  return Ok(Regex {
    ngroups: r.g - 1, names: cx.names.get(), code: code, masks: masks, fwd: fwd, rev: rev, first: first,
    lead: __rx_lead(first), prefix: pre.bytes, ci: pre.folded, loose: pre.loose, leftmost_longest: false,
    empty: __rx_nullable(r.node), uword: __rx_uword(code), full: __rx_dfa_full(fwd),
    rfull: __rx_dfa_full(rev), rare: if (pre.folded || pre.loose) { -1 } else { __rx_rare(pre.bytes) },
    inner: inner, inner_rare: __rx_rarest(inner),
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
  if (prog.rare > 0) {
    return __rx_rare_candidate(prog, text, from);
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

// The next start at or after `from` where the prefix is, from each place
// of its rarest byte.
function __rx_rare_candidate(prog: Regex, text: string, from: i32): i32 {
  let r = prog.rare;
  let b = prog.prefix[r] as i32;
  let ti = from;
  while (ti + r < text.len()) {
    let at = __memchr(text, b, ti + r);
    if (at < 0) {
      return -1;
    }
    if (__rx_has_prefix(prog, text, at - r)) {
      return at - r;
    }
    ti = at - r + 1;
  }
  return -1;
}

// The offset of the byte of `prefix` that is least frequent in text, or -1
// when the first byte is that byte or the prefix is shorter than two bytes.
// The rank of a byte is lower for a byte that text holds more often.
function __rx_rare(prefix: u8[]): i32 {
  if (prefix.len() < 2) {
    return -1;
  }
  let best = __rx_rarest(prefix);
  return if (best == 0) { -1 } else { best };
}

// The offset of the byte of `bytes` that is least frequent in text, or 0.
function __rx_rarest(bytes: u8[]): i32 {
  let best = 0;
  for k in 1..bytes.len() {
    if (__rx_byte_rank(bytes[k] as i32) < __rx_byte_rank(bytes[best] as i32)) {
      best = k;
    }
  }
  return best;
}

// The bytes that each match of the pattern holds, from the longest run of
// literal bytes that each match goes through. They are empty when the
// prefix of the pattern holds as many bytes.
pub function (prog: Regex) inner(): u8[] {
  return prog.inner;
}

// The offset of the next copy of the bytes of `inner` at or after `from`,
// or -1. With no such bytes, the offset is `from`.
pub function (prog: Regex) next_inner(text: string, from: i32): i32 {
  let lit = prog.inner;
  let n = lit.len();
  if (n == 0) {
    return from;
  }
  let r = prog.inner_rare;
  let b = lit[r] as i32;
  let ti = from;
  while (ti + n <= text.len()) {
    let at = __memchr(text, b, ti + r);
    if (at < 0 || at - r + n > text.len()) {
      return -1;
    }
    if (__rx_bytes_at(text, at - r, lit)) {
      return at - r;
    }
    ti = at - r + 1;
  }
  return -1;
}

function __rx_bytes_at(text: string, at: i32, lit: u8[]): boolean {
  for k in 0..lit.len() {
    if (text[at + k] != lit[k]) {
      return false;
    }
  }
  return true;
}

// The literal bytes that each match of a node holds: `whole` when each
// match is exactly those bytes, and the longest run in `best`.
struct RReq { exact: boolean, whole: u8[], best: u8[] }

function __rx_required(x: RNode): RReq {
  let none: u8[] = [];
  return match (x) {
    RChar(b) => RReq { exact: true, whole: [b as u8], best: [b as u8] },
    RBound(_) => RReq { exact: true, whole: none, best: none },
    RSeq(items) => __rx_required_seq(items),
    RPlus(y) => RReq { exact: false, whole: none, best: __rx_required(y).best },
    RLazy(y) => __rx_required(y),
    RGroup(g) => __rx_required(g.node),
    _ => RReq { exact: false, whole: none, best: none },
  };
}

function __rx_required_seq(items: RNode[]): RReq {
  let run: u8[] = [];
  let best: u8[] = [];
  let exact = true;
  for x in items {
    let r = __rx_required(x);
    if (r.exact) {
      run = run.concat(r.whole);
    } else {
      exact = false;
      best = __rx_longer(__rx_longer(best, run), r.best);
      run = [];
    }
  }
  return RReq { exact: exact, whole: if (exact) { run } else { [] }, best: __rx_longer(best, run) };
}

function __rx_longer(a: u8[], b: u8[]): u8[] {
  return if (b.len() > a.len()) { b } else { a };
}

// How often text holds a byte, from 0 for rare to 255 for frequent: white
// space, then the lower case letters in the order of English, the marks of
// punctuation and the digits, then the capital letters.
function __rx_byte_rank(b: i32): i32 {
  if (b == 32 || b == 10) {
    return 255;
  }
  let lower = __memchr("etaoinshrdlcumwfgypbvkjxqz", b, 0);
  if (lower >= 0) {
    return 250 - lower;
  }
  let upper = __memchr("ETAOINSHRDLCUMWFGYPBVKJXQZ", b, 0);
  if (upper >= 0) {
    return 180 - upper;
  }
  let mark = __memchr(",.()_;:\"'-=/0123456789{}[]<>*&!#+", b, 0);
  if (mark >= 0) {
    return 220 - mark;
  }
  return if (b >= 128) { 160 } else { 100 };
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

// Whether a match can hold the byte `b`. A line anchor of `(?m)` also holds
// the line feed, and a line anchor of `(?mR)` also holds the carriage return.
pub function (prog: Regex) can_match_byte(b: u8): boolean {
  for pc in 0..prog.code.len() / 3 {
    if (__rx_has_byte(prog.code, prog.masks, pc, b as i32)) {
      return true;
    }
  }
  return false;
}

// Whether the pattern has the ASCII byte `b` as a literal: a character, or a
// class that holds only that character.
pub function (prog: Regex) has_literal_byte(b: u8): boolean {
  for pc in 0..prog.code.len() / 3 {
    let at = 3 * pc;
    if (prog.code[at] == RX_CHAR && prog.code[at + 1] == b as i32 && prog.code[at + 2] == b as i32) {
      return true;
    }
  }
  return false;
}

function __rx_has_byte(code: i32[], masks: i32[], pc: i32, byte: i32): boolean {
  let op = code[3 * pc];
  let a = code[3 * pc + 1];
  if (op == RX_CHAR) {
    return byte == a || byte == code[3 * pc + 2];
  }
  if (op == RX_CLASS) {
    return __rx_mask_bit(masks, a, byte) == 1;
  }
  if (op == RX_ASSERT) {
    return a >= RX_A_LINE && byte == 10 || a >= RX_A_CRLF && byte == 13;
  }
  if (op != RX_SPARSE) {
    return false;
  }
  let e = a + 1;
  while (e < a + 1 + 3 * masks[a]) {
    if (byte >= masks[e] && byte <= masks[e + 1]) {
      return true;
    }
    e += 3;
  }
  return false;
}

// The search with a full table stays in a small function, because the code
// of the compiler saves each register at the entry of a function.
pub function (prog: Regex) is_match(text: string): boolean {
  if (prog.full.len() > 0) {
    let c = prog.next_candidate(text, 0);
    if (c < 0) {
      return false;
    }
    let e = __rx_full_end(prog, text, c, true);
    if (e != -2) {
      return e >= 0;
    }
  }
  return __rx_lazy_match(prog, text);
}

@noinline
function __rx_lazy_match(prog: Regex, text: string): boolean {
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
// of Unicode when an assertion needs them. `rare` is the offset in `prefix`
// of the byte that is least frequent in text, or -1 when the search looks
// for the first byte. Each match holds the bytes of `inner`, and `inner_rare`
// is the offset of their rarest byte.
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
  full: i32[],
  rfull: i32[],
  rare: i32,
  inner: u8[],
  inner_rare: i32,
}
```

### Errors of regex-syntax

`check` parses a pattern with the rules of regex-syntax 0.8.2, the parser of the `regex` crate. It gives the first error that regex-syntax reports, with the same text and the same span. ripgrep writes these errors, and a tool that reads the patterns of ripgrep can write them too. The parse follows the order of regex-syntax: a parse error comes first, then a group that is too deep, then a Unicode class that regex-syntax does not find. `report` writes the error as regex-syntax does, with a line of `^` under each span.

```fern
<<errors of regex-syntax>>=
// An error of a pattern as the regex crate reports it: the text of the
// error, the pattern, the span of the error in bytes, and a second span, or
// -1 for none.
pub struct SyntaxError { message: string, pattern: string, start: i32, end: i32, aux_start: i32, aux_end: i32 }

// A class to look up after the parse: a Unicode class (kind 0) with the
// text of its name, or a literal outside ASCII in a set (kind 1). `unicode`
// says whether `(?u)` is on there.
struct SxCheck { kind: i32, start: i32, end: i32, text: string, unicode: boolean }

// The state of a check: the pattern, the first error, the names of the
// groups with their spans, the classes to look up, and the span of the
// first group that is too deep.
struct SxCtx {
  p: string,
  err: Cell[Option[SyntaxError]],
  names: Cell[string[]],
  name_spans: Cell[i32[]],
  checks: Cell[SxCheck[]],
  deep: Cell[i32[]],
}

// An item of a set, or a primitive: its kind, its offsets, and for a
// literal its code point. `hex` marks a byte that `\x` gives.
struct SxItem { kind: i32, start: i32, end: i32, cp: i32, hex: boolean }

const SX_LITERAL: i32 = 0;

const SX_ASSERTION: i32 = 1;

const SX_CLASS: i32 = 2;

const SX_NEST_LIMIT: i32 = 250;

const SX_EOF: string = "incomplete escape sequence, reached end of pattern prematurely";

const SX_REPEAT_MISSING: string = "repetition operator missing expression";

const SX_UNCLOSED_COUNT: string = "unclosed counted repetition";

const SX_UNCLOSED_CLASS: string = "unclosed character class";

// check gives the first error of `pattern` that the regex crate reports,
// or None when it accepts the pattern. `unicode` is false for a pattern
// that starts in the mode of `(?-u)`. A property that the crate knows, but
// that this module does not, is not an error here.
pub function check(pattern: string, unicode: boolean): Option[SyntaxError] {
  let none: string[] = [];
  let spans: i32[] = [];
  let checks: SxCheck[] = [];
  let cx = SxCtx { p: pattern, err: cell_new(None), names: cell_new(none), name_spans: cell_new(spans), checks: cell_new(checks), deep: cell_new(spans) };
  if (__sx_alt(cx, 0, -1, false, unicode, 0) < 0) {
    return cx.err.get();
  }
  let deep = cx.deep.get();
  if (deep.len() == 2) {
    return Some(__sx_error(cx, "exceed the maximum number of nested parentheses/brackets (250)", deep[0], deep[1]));
  }
  for c in cx.checks.get() {
    let why = __sx_lookup(c);
    if (why != "") {
      return Some(__sx_error(cx, why, c.start, c.end));
    }
  }
  return None;
}

function __sx_error(cx: SxCtx, message: string, start: i32, end: i32): SyntaxError {
  return SyntaxError { message: message, pattern: cx.p, start: start, end: end, aux_start: -1, aux_end: -1 };
}

// Records the first error and gives -1.
function __sx_fail(cx: SxCtx, message: string, start: i32, end: i32): i32 {
  return __sx_fail2(cx, message, start, end, -1, -1);
}

function __sx_fail2(cx: SxCtx, message: string, start: i32, end: i32, aux_start: i32, aux_end: i32): i32 {
  if let None = cx.err.get() {
    cx.err.set(Some(SyntaxError { message: message, pattern: cx.p, start: start, end: end, aux_start: aux_start, aux_end: aux_end }));
  }
  return -1;
}

// The count of bytes of the character of UTF-8 at `i`.
function __sx_len(p: string, i: i32): i32 {
  let c = p[i];
  let n = if (c < 192u8) { 1 } else if (c < 224u8) { 2 } else if (c < 240u8) { 3 } else { 4 };
  return if (i + n > p.len()) { p.len() - i } else { n };
}

// The code point at `i`.
function __sx_cp(p: string, i: i32): i32 {
  let Some(dec) = utf8.decode_at(p, i) else {
    return p[i] as i32;
  };
  return dec.0;
}

// Whether the code point is White_Space, as Rust's char::is_whitespace.
function __sx_is_ws(cp: i32): boolean {
  if (cp < 128) {
    return cp == 32 || cp >= 9 && cp <= 13;
  }
  return __rx_in([133, 133, 160, 160, 5760, 5760, 8192, 8202, 8232, 8233, 8239, 8239, 8287, 8287, 12288, 12288], cp);
}

// The offset after the white space and the comments at `i`, under `(?x)`.
function __sx_space(p: string, i0: i32, ws: boolean): i32 {
  if (!ws) {
    return i0;
  }
  let i = i0;
  while (i < p.len()) {
    if (__sx_is_ws(__sx_cp(p, i))) {
      i += __sx_len(p, i);
    } else if (p[i] == 35u8) {
      let nl = __memchr(p, 10, i);
      i = if (nl < 0) { p.len() } else { nl + 1 };
    } else {
      return i;
    }
  }
  return i;
}

// The offset after the character at `i` and the space after it.
function __sx_bump(p: string, i: i32, ws: boolean): i32 {
  if (i >= p.len()) {
    return i;
  }
  return __sx_space(p, i + __sx_len(p, i), ws);
}

function __sx_at(p: string, i: i32, text: string): boolean {
  if (i + text.len() > p.len()) {
    return false;
  }
  for k in 0..text.len() {
    if (p[i + k] != text[k]) {
      return false;
    }
  }
  return true;
}

// The alternation from `i0` to the `)` of the group that opens at `open`,
// or to the end of the pattern when `open` is -1. It gives the offset of
// that `)` or of the end, or -1 after an error. `last` is 0 for an empty
// concatenation, 1 after flags, and 2 after an expression.
function __sx_alt(cx: SxCtx, i0: i32, open: i32, ws0: boolean, u0: boolean, depth: i32): i32 {
  let p = cx.p;
  let i = i0;
  let ws = ws0;
  let u = u0;
  let last = 0;
  while (true) {
    i = __sx_space(p, i, ws);
    if (i >= p.len() || p[i] == 41u8) {
      return __sx_end(cx, i, open);
    }
    if (p[i] == 124u8) {
      i += 1;
      last = 0;
    } else if (p[i] == 40u8) {
      let (next, nws, nu, flags) = __sx_group(cx, i, ws, u, depth + 1);
      i = next;
      ws = nws;
      u = nu;
      last = if (flags) { 1 } else { 2 };
    } else {
      i = __sx_simple(cx, i, ws, u, last);
      last = 2;
    }
    if (i < 0) {
      return -1;
    }
  }
  return -1;
}

// The end of an alternation at `i`: the end of the pattern, or a `)`. The
// group that opens at `open` must close there, and only there.
function __sx_end(cx: SxCtx, i: i32, open: i32): i32 {
  if (i >= cx.p.len()) {
    return if (open >= 0) { __sx_fail(cx, "unclosed group", open, open + 1) } else { i };
  }
  return if (open < 0) { __sx_fail(cx, "unopened group", i, i + 1) } else { i };
}

// A repetition, a class or a primitive at `i`.
function __sx_simple(cx: SxCtx, i: i32, ws: boolean, u: boolean, last: i32): i32 {
  let p = cx.p;
  let c = p[i];
  if (c == 63u8 || c == 42u8 || c == 43u8) {
    if (last != 2) {
      return __sx_fail(cx, SX_REPEAT_MISSING, i, i);
    }
    return if (i + 1 < p.len() && p[i + 1] == 63u8) { i + 2 } else { i + 1 };
  }
  if (c == 123u8) {
    return __sx_counted(cx, i, ws, last);
  }
  if (c == 91u8) {
    return __sx_class(cx, i, ws, u);
  }
  if (c == 92u8) {
    return __sx_escape(cx, i, ws, u).end;
  }
  return i + __sx_len(p, i);
}

// The group at `i`: the offset after it, the flags `x` and `u` after it,
// and whether it only sets flags.
function __sx_group(cx: SxCtx, open: i32, ws: boolean, u: boolean, depth: i32): (i32, boolean, boolean, boolean) {
  let p = cx.p;
  let j = __sx_space(p, open + 1, ws);
  if (__sx_look(cx, open, j) < 0) {
    return (-1, ws, u, false);
  }
  if (__sx_at(p, j, "?P<") || __sx_at(p, j, "?<")) {
    let body = __sx_capture_name(cx, if (p[j + 1] == 80u8) { j + 3 } else { j + 2 });
    return (__sx_body(cx, open, body, ws, u, depth), ws, u, false);
  }
  if (!__sx_at(p, j, "?")) {
    return (__sx_body(cx, open, j, ws, u, depth), ws, u, false);
  }
  return __sx_flag_group(cx, open, j, ws, u, depth);
}

// The group at `open` with flags from `j`: a group that sets flags for its
// body, or a group that only sets flags.
function __sx_flag_group(cx: SxCtx, open: i32, j: i32, ws: boolean, u: boolean, depth: i32): (i32, boolean, boolean, boolean) {
  let p = cx.p;
  if (j + 1 >= p.len()) {
    return (__sx_fail(cx, "unclosed group", open, open + 1), ws, u, false);
  }
  let (end, fws, fu, count) = __sx_flags(cx, j + 1, ws, u);
  if (end < 0 || p[end] == 58u8) {
    return (__sx_body(cx, open, if (end < 0) { -1 } else { end + 1 }, fws, fu, depth), ws, u, false);
  }
  if (count == 0) {
    return (__sx_fail(cx, SX_REPEAT_MISSING, j, j), ws, u, false);
  }
  return (end + 1, fws, fu, true);
}

// Records the error of a look-around at `j` in the group that opens at
// `open`, and gives -1, or gives `j`.
function __sx_look(cx: SxCtx, open: i32, j: i32): i32 {
  for look in ["?=", "?!", "?<=", "?<!"] {
    if (__sx_at(cx.p, j, look)) {
      return __sx_fail(cx, "look-around, including look-ahead and look-behind, is not supported", open, j + look.len());
    }
  }
  return j;
}

// The body of the group that opens at `open`, from `body`, and the offset
// after its `)`, or -1. The first group deeper than the limit is recorded.
function __sx_body(cx: SxCtx, open: i32, body: i32, ws: boolean, u: boolean, depth: i32): i32 {
  if (body < 0) {
    return -1;
  }
  if (depth > SX_NEST_LIMIT && cx.deep.get().len() == 0) {
    cx.deep.set([open, -1]);
  }
  let close = __sx_alt(cx, body, open, ws, u, depth);
  if (close < 0) {
    return -1;
  }
  let deep = cx.deep.get();
  if (deep.len() == 2 && deep[0] == open) {
    cx.deep.set([open, close + 1]);
  }
  return close + 1;
}

// The name of a capture group at `j`, and the offset after its `>`.
function __sx_capture_name(cx: SxCtx, j0: i32): i32 {
  let p = cx.p;
  let j = j0;
  while (j < p.len() && p[j] != 62u8) {
    if (!__sx_name_char(__sx_cp(p, j), j == j0)) {
      return __sx_fail(cx, "invalid capture group character", j, j + __sx_len(p, j));
    }
    j += __sx_len(p, j);
  }
  if (j >= p.len()) {
    return __sx_fail(cx, "unclosed capture group name", j, j);
  }
  if (j == j0) {
    return __sx_fail(cx, "empty capture group name", j0, j0);
  }
  let name = slice_unchecked(p, j0, j) + "";
  let names = cx.names.get();
  for k in 0..names.len() {
    if (names[k] == name) {
      let s = cx.name_spans.get();
      return __sx_fail2(cx, "duplicate capture group name", j0, j, s[2 * k], s[2 * k + 1]);
    }
  }
  cx.names.set(names.append(name));
  cx.name_spans.set(cx.name_spans.get().append(j0).append(j));
  return j + 1;
}

// Whether a character can be in the name of a group: a letter or `_` first,
// and after it also a digit, `.`, `[` or `]`.
function __sx_name_char(cp: i32, first: boolean): boolean {
  if (cp >= 128) {
    return __rx_in(__rx_prop("Alphabetic"), cp) || !first && __rx_in(__rx_prop("N"), cp);
  }
  if (cp == 95 || __rx_in([65, 90, 97, 122], cp)) {
    return true;
  }
  return !first && __rx_in([46, 46, 48, 57, 91, 91, 93, 93], cp);
}

// The flags of `(?flags)` or `(?flags:` from `j`: the offset of the `)` or
// the `:`, the flags `x` and `u` after them, and the count of items.
function __sx_flags(cx: SxCtx, j0: i32, ws: boolean, u: boolean): (i32, boolean, boolean, i32) {
  let p = cx.p;
  let j = j0;
  let items: i32[] = [];
  let neg = -1;
  let fws = ws;
  let fu = u;
  while (p[j] != 58u8 && p[j] != 41u8) {
    let c = p[j] as i32;
    neg = if (c == 45) { j } else { -1 };
    let why = __sx_flag_error(p, j, items);
    if (why != "") {
      let first = __sx_first_flag(p, j, items);
      return (__sx_fail2(cx, why, j, j + __sx_len(p, j), first, if (first < 0) { -1 } else { first + 1 }), ws, u, 0);
    }
    let off = __memchr(slice_unchecked(p, j0, j) + "", 45, 0) >= 0;
    fws = if (c == 120) { !off } else { fws };
    fu = if (c == 117) { !off } else { fu };
    items = items.append(j);
    j += __sx_len(p, j);
    if (j >= p.len()) {
      return (__sx_fail(cx, "expected flag but got end of regex", j, j), ws, u, 0);
    }
  }
  if (neg >= 0) {
    return (__sx_fail(cx, "dangling flag negation operator", neg, neg + 1), ws, u, 0);
  }
  return (j, fws, fu, items.len());
}

// The message of the flag at `j` when it is not valid, or "".
function __sx_flag_error(p: string, j: i32, items: i32[]): string {
  let c = p[j] as i32;
  if (c != 45 && __memchr("imsUuRx", c, 0) < 0) {
    return "unrecognized flag";
  }
  if (__sx_first_flag(p, j, items) < 0) {
    return "";
  }
  return if (c == 45) { "flag negation operator repeated" } else { "duplicate flag" };
}

// The offset of the earlier item with the same flag as the one at `j`, or
// -1. An entry of -1 in `items` is not an item.
function __sx_first_flag(p: string, j: i32, items: i32[]): i32 {
  for k in items {
    if (k >= 0 && p[k] == p[j]) {
      return k;
    }
  }
  return -1;
}

// The counted repetition at `i`, and the offset after it.
function __sx_counted(cx: SxCtx, start: i32, ws: boolean, last: i32): i32 {
  let p = cx.p;
  if (last != 2) {
    return __sx_fail(cx, SX_REPEAT_MISSING, start, start);
  }
  let j = __sx_bump(p, start, ws);
  if (j >= p.len()) {
    return __sx_fail(cx, SX_UNCLOSED_COUNT, start, j);
  }
  let (lo, a) = __sx_decimal(cx, j, ws);
  if (a < 0) {
    return -1;
  }
  let (hi, b) = if (a < p.len() && p[a] == 44u8) { __sx_upper(cx, start, a, ws) } else { (lo, a) };
  if (b < 0) {
    return -1;
  }
  return __sx_count_end(cx, start, b, ws, lo > hi);
}

// The `}` of a counted repetition at `b`, a `?` after it, and the offset
// after them. `inverted` says that the range is not valid.
function __sx_count_end(cx: SxCtx, start: i32, b: i32, ws: boolean, inverted: boolean): i32 {
  let p = cx.p;
  if (b >= p.len() || p[b] != 125u8) {
    return __sx_fail(cx, SX_UNCLOSED_COUNT, start, b);
  }
  let j = __sx_bump(p, b, ws);
  if (j < p.len() && p[j] == 63u8) {
    j += 1;
  }
  if (inverted) {
    return __sx_fail(cx, "invalid repetition count range, the start must be <= the end", start, j);
  }
  return j;
}

// The upper bound of a counted repetition from the `,` at `comma`, or the
// largest value for none, and the offset after it.
function __sx_upper(cx: SxCtx, start: i32, comma: i32, ws: boolean): (i64, i32) {
  let p = cx.p;
  let j = __sx_bump(p, comma, ws);
  if (j >= p.len()) {
    return (0i64, __sx_fail(cx, SX_UNCLOSED_COUNT, start, j));
  }
  if (p[j] == 125u8) {
    return (4294967295i64, j);
  }
  return __sx_decimal(cx, j, ws);
}

// The decimal number at `j` with the white space around it, and the
// offset after it, or -1.
function __sx_decimal(cx: SxCtx, j0: i32, ws: boolean): (i64, i32) {
  let p = cx.p;
  let start = __sx_white(p, j0, false);
  let j = start;
  let v = 0i64;
  while (j < p.len() && p[j] >= 48u8 && p[j] <= 57u8) {
    v = if (v > 4294967295i64) { v } else { v * 10i64 + (p[j] as i64 - 48i64) };
    j = __sx_bump(p, j, ws);
  }
  let end = j;
  j = __sx_white(p, j, ws);
  if (end == start) {
    return (0i64, __sx_fail(cx, "repetition quantifier expects a valid decimal", start, end));
  }
  if (v > 4294967295i64) {
    return (0i64, __sx_fail(cx, "decimal literal invalid", start, end));
  }
  return (v, j);
}

// The offset after the white space at `j`. With `ws`, a comment after a
// space is skipped too.
function __sx_white(p: string, j0: i32, ws: boolean): i32 {
  let j = j0;
  while (j < p.len() && __sx_is_ws(__sx_cp(p, j))) {
    j = if (ws) { __sx_bump(p, j, ws) } else { j + __sx_len(p, j) };
  }
  return j;
}

// The escape at `start`. A Unicode class is recorded for the lookup.
function __sx_escape(cx: SxCtx, start: i32, ws: boolean, u: boolean): SxItem {
  let p = cx.p;
  let bad = SxItem { kind: -1, start: start, end: -1, cp: 0, hex: false };
  let j = start + 1;
  if (j >= p.len()) {
    __sx_fail(cx, SX_EOF, start, j);
    return bad;
  }
  let c = p[j] as i32;
  if (c >= 48 && c <= 57) {
    __sx_fail(cx, "backreferences are not supported", start, j + 1);
    return bad;
  }
  if (c == 120 || c == 117 || c == 85) {
    return __sx_hex(cx, start, j, ws);
  }
  if (c == 112 || c == 80) {
    return __sx_unicode(cx, start, j, ws, u);
  }
  if (__memchr("dswDSW", c, 0) >= 0) {
    return SxItem { kind: SX_CLASS, start: start, end: j + 1, cp: 0, hex: false };
  }
  return __sx_escaped_char(cx, start, j, ws);
}

// The escape of one character at `j`: a literal, an assertion, or an
// escape that regex-syntax does not know.
function __sx_escaped_char(cx: SxCtx, start: i32, j: i32, ws: boolean): SxItem {
  let p = cx.p;
  let c = p[j] as i32;
  let end = j + __sx_len(p, j);
  let item = SxItem { kind: SX_LITERAL, start: start, end: end, cp: c, hex: false };
  let special = __memchr("aftnrv", c, 0);
  if (__sx_escapable(c) || special >= 0) {
    return SxItem { ...item, cp: if (special >= 0) { [7, 12, 9, 10, 13, 11][special] } else { c } };
  }
  if (__memchr("AzB<>", c, 0) >= 0) {
    return SxItem { ...item, kind: SX_ASSERTION };
  }
  if (c == 98) {
    let after = if (end < p.len() && p[end] == 123u8) { __sx_word_boundary(cx, start, end, ws) } else { end };
    return SxItem { ...item, kind: if (after < 0) { -1 } else { SX_ASSERTION }, end: after };
  }
  __sx_fail(cx, "unrecognized escape sequence", start, end);
  return SxItem { ...item, kind: -1, end: -1 };
}

// Whether `\c` is a literal: a mark of the syntax, or another ASCII
// character that is not a letter, a digit, `<` or `>`.
function __sx_escapable(c: i32): boolean {
  if (c >= 128 || c == 60 || c == 62) {
    return false;
  }
  return !(c >= 48 && c <= 57 || c >= 65 && c <= 90 || c >= 97 && c <= 122);
}

// `\b{...}` from the `{` at `brace`, and the offset after it. A `{` that a
// letter or a `-` does not follow starts a repetition.
function __sx_word_boundary(cx: SxCtx, start: i32, brace: i32, ws: boolean): i32 {
  let p = cx.p;
  let k = __sx_bump(p, brace, ws);
  if (k >= p.len()) {
    return __sx_fail(cx, "found either the beginning of a special word boundary or a bounded repetition on a \\b with an opening brace, but no closing brace", start, k);
  }
  if (!__sx_boundary_char(p[k])) {
    return brace;
  }
  let contents = k;
  let name = "";
  while (k < p.len() && __sx_boundary_char(p[k])) {
    name += slice_unchecked(p, k, k + 1);
    k = __sx_bump(p, k, ws);
  }
  if (k >= p.len() || p[k] != 125u8) {
    return __sx_fail(cx, "special word boundary assertion is either unclosed or contains an invalid character", brace, k);
  }
  if (!__sx_has("start end start-half end-half", name)) {
    return __sx_fail(cx, "unrecognized special word boundary assertion, valid choices are: start, end, start-half or end-half", contents, k);
  }
  return k + 1;
}

function __sx_boundary_char(c: u8): boolean {
  return c >= 65u8 && c <= 90u8 || c >= 97u8 && c <= 122u8 || c == 45u8;
}

function __sx_hex_digit(c: u8): i32 {
  let l = c | 32u8;
  if (c >= 48u8 && c <= 57u8) {
    return c as i32 - 48;
  }
  return if (l >= 97u8 && l <= 102u8) { l as i32 - 87 } else { -1 };
}

// `\x`, `\u` or `\U` from the letter at `j`.
function __sx_hex(cx: SxCtx, start: i32, j: i32, ws: boolean): SxItem {
  let p = cx.p;
  let bad = SxItem { kind: -1, start: start, end: -1, cp: 0, hex: false };
  let k = __sx_bump(p, j, ws);
  if (k >= p.len()) {
    __sx_fail(cx, SX_EOF, k, k);
    return bad;
  }
  let x = p[j] == 120u8;
  if (p[k] == 123u8) {
    return __sx_hex_brace(cx, start, k, ws, x);
  }
  let first = k;
  let v = 0i64;
  for n in 0..__sx_hex_width(p[j]) {
    k = if (n > 0) { __sx_bump(p, k, ws) } else { k };
    let d = if (k < p.len()) { __sx_hex_digit(p[k]) } else { -2 };
    if (d < 0) {
      __sx_fail(cx, if (d == -2) { SX_EOF } else { "invalid hexadecimal digit" }, k, if (d == -2) { k } else { k + __sx_len(p, k) });
      return bad;
    }
    v = v * 16i64 + d as i64;
  }
  return __sx_hex_value(cx, start, first, __sx_bump(p, k, ws), v, x);
}

// The literal of the value `v` of the digits from `first` to `end`, or an
// error for a value that is not a character.
function __sx_hex_value(cx: SxCtx, start: i32, first: i32, end: i32, v: i64, x: boolean): SxItem {
  if (!__sx_scalar(v)) {
    __sx_fail(cx, "hexadecimal literal is not a Unicode scalar value", first, end);
    return SxItem { kind: -1, start: start, end: -1, cp: 0, hex: false };
  }
  return SxItem { kind: SX_LITERAL, start: start, end: end, cp: v as i32, hex: x && v <= 255i64 };
}

// The count of digits of `\x`, `\u` or `\U`.
function __sx_hex_width(c: u8): i32 {
  return if (c == 120u8) { 2 } else if (c == 117u8) { 4 } else { 8 };
}

function __sx_scalar(v: i64): boolean {
  return v <= 1114111i64 && (v < 55296i64 || v > 57343i64);
}

// The digits of `\x{...}` from the `{` at `brace`.
function __sx_hex_brace(cx: SxCtx, start: i32, brace: i32, ws: boolean, x: boolean): SxItem {
  let p = cx.p;
  let bad = SxItem { kind: -1, start: start, end: -1, cp: 0, hex: false };
  let k = __sx_bump(p, brace, ws);
  let v = 0i64;
  let count = 0;
  while (k < p.len() && p[k] != 125u8) {
    let d = __sx_hex_digit(p[k]);
    if (d < 0) {
      __sx_fail(cx, "invalid hexadecimal digit", k, k + __sx_len(p, k));
      return bad;
    }
    count += 1;
    v = if (count > 8) { 4294967296i64 } else { v * 16i64 + d as i64 };
    k = __sx_bump(p, k, ws);
  }
  if (k >= p.len()) {
    __sx_fail(cx, SX_EOF, brace, k);
    return bad;
  }
  if (count == 0) {
    __sx_fail(cx, "hexadecimal literal empty", brace, __sx_bump(p, k, ws));
    return bad;
  }
  let item = __sx_hex_value(cx, start, brace + 1, k, v, x);
  return if (item.end < 0) { item } else { SxItem { ...item, end: __sx_bump(p, k, ws) } };
}

// `\pN`, `\p{...}`, `\PN` or `\P{...}` from the letter at `j`.
function __sx_unicode(cx: SxCtx, start: i32, j: i32, ws: boolean, u: boolean): SxItem {
  let p = cx.p;
  let bad = SxItem { kind: -1, start: start, end: -1, cp: 0, hex: false };
  let k = __sx_bump(p, j, ws);
  if (k >= p.len()) {
    __sx_fail(cx, SX_EOF, k, k);
    return bad;
  }
  let name = "";
  if (p[k] == 123u8) {
    while (true) {
      k = __sx_bump(p, k, ws);
      if (k >= p.len() || p[k] == 125u8) {
        break;
      }
      name += slice_unchecked(p, k, k + __sx_len(p, k));
    }
    if (k >= p.len()) {
      __sx_fail(cx, SX_EOF, k, k);
      return bad;
    }
    k += 1;
  } else {
    if (p[k] == 92u8) {
      __sx_fail(cx, "invalid Unicode character class", k, k + 1);
      return bad;
    }
    name = slice_unchecked(p, k, k + __sx_len(p, k)) + "";
    k = __sx_bump(p, k, ws);
  }
  cx.checks.set(cx.checks.get().append(SxCheck { kind: 0, start: start, end: k, text: name, unicode: u }));
  return SxItem { kind: SX_CLASS, start: start, end: k, cp: 0, hex: false };
}

// The bracketed class at `i`, and the offset after its last `]`. `opens`
// holds the first and the end offset of each open bracket.
function __sx_class(cx: SxCtx, i: i32, ws: boolean, u: boolean): i32 {
  let p = cx.p;
  let j = __sx_class_open(cx, i, ws);
  let opens = [i, j];
  while (j >= 0) {
    j = __sx_space(p, j, ws);
    if (j >= p.len()) {
      return __sx_fail(cx, SX_UNCLOSED_CLASS, opens[opens.len() - 2], opens[opens.len() - 1]);
    }
    if (p[j] == 93u8) {
      opens = opens.take(opens.len() - 2);
      j += 1;
      if (opens.len() == 0) {
        return j;
      }
    } else if (p[j] == 91u8 && __sx_posix(p, j) == 0) {
      let next = __sx_class_open(cx, j, ws);
      opens = opens.append(j).append(next);
      j = next;
    } else {
      j = __sx_set_step(cx, j, ws, u, opens[opens.len() - 2], opens[opens.len() - 1]);
    }
  }
  return -1;
}

// One step in a set at `j` that does not open or close a bracket: a class
// of POSIX, a set operation, an item or a range.
function __sx_set_step(cx: SxCtx, j: i32, ws: boolean, u: boolean, os: i32, oe: i32): i32 {
  let p = cx.p;
  let c = p[j];
  if (c == 91u8) {
    return __sx_posix(p, j);
  }
  if ((c == 38u8 || c == 45u8 || c == 126u8) && j + 1 < p.len() && p[j + 1] == c) {
    return j + 2;
  }
  return __sx_class_range(cx, j, ws, u, os, oe);
}

// The `[` at `start`, a `^`, and the `-` and the `]` after it that are
// literals. It gives the offset after them.
function __sx_class_open(cx: SxCtx, start: i32, ws: boolean): i32 {
  let p = cx.p;
  let j = __sx_bump(p, start, ws);
  if (j >= p.len()) {
    return __sx_fail(cx, SX_UNCLOSED_CLASS, start, j);
  }
  if (p[j] == 94u8) {
    j = __sx_bump(p, j, ws);
    if (j >= p.len()) {
      return __sx_fail(cx, SX_UNCLOSED_CLASS, start, j);
    }
  }
  let dash = false;
  while (p[j] == 45u8) {
    dash = true;
    j = __sx_bump(p, j, ws);
    if (j >= p.len()) {
      return __sx_fail(cx, SX_UNCLOSED_CLASS, start, start);
    }
  }
  if (!dash && p[j] == 93u8) {
    j = __sx_bump(p, j, ws);
    if (j >= p.len()) {
      return __sx_fail(cx, SX_UNCLOSED_CLASS, start, j);
    }
  }
  return j;
}

// `[:name:]` or `[:^name:]` at `j` with a name of POSIX, and the offset
// after it, or 0.
function __sx_posix(p: string, j: i32): i32 {
  if (j + 2 >= p.len() || p[j + 1] != 58u8) {
    return 0;
  }
  let from = if (p[j + 2] == 94u8) { j + 3 } else { j + 2 };
  let colon = __memchr(p, 58, from);
  if (colon < 0 || !__sx_at(p, colon, ":]")) {
    return 0;
  }
  return if (__rx_posix_ranges(slice_unchecked(p, from, colon) + "").len() == 0) { 0 } else { colon + 2 };
}

// An item of a set at `j`, or a range of two literals.
function __sx_class_range(cx: SxCtx, j: i32, ws: boolean, u: boolean, os: i32, oe: i32): i32 {
  let p = cx.p;
  let a = __sx_set_item(cx, j, ws, u);
  let k = if (a.end < 0) { -1 } else { __sx_space(p, a.end, ws) };
  if (k >= p.len()) {
    return __sx_fail(cx, SX_UNCLOSED_CLASS, os, oe);
  }
  let next = if (k < 0) { 0 } else { __sx_peek(p, k, ws) };
  if (k < 0 || p[k] != 45u8 || next == 93 || next == 45) {
    return __sx_set_single(cx, a, k, u);
  }
  k = __sx_bump(p, k, ws);
  if (k >= p.len()) {
    return __sx_fail(cx, SX_UNCLOSED_CLASS, os, oe);
  }
  let b = __sx_set_item(cx, k, ws, u);
  if (b.end < 0) {
    return -1;
  }
  return __sx_set_range(cx, a, b, u);
}

// One item of a set that is not in a range, and the offset after it.
function __sx_set_single(cx: SxCtx, a: SxItem, k: i32, u: boolean): i32 {
  if (k < 0) {
    return -1;
  }
  if (a.kind == SX_ASSERTION) {
    return __sx_fail(cx, "invalid escape sequence found in character class", a.start, a.end);
  }
  __sx_set_literal(cx, a, u);
  return k;
}

// The range of the items `a` and `b`, and the offset after it.
function __sx_set_range(cx: SxCtx, a: SxItem, b: SxItem, u: boolean): i32 {
  for x in [a, b] {
    if (x.kind != SX_LITERAL) {
      return __sx_fail(cx, "invalid range boundary, must be a literal", x.start, x.end);
    }
  }
  if (a.cp > b.cp) {
    return __sx_fail(cx, "invalid character class range, the start must be <= the end", a.start, b.end);
  }
  __sx_set_literal(cx, a, u);
  __sx_set_literal(cx, b, u);
  return b.end;
}

function __sx_set_item(cx: SxCtx, j: i32, ws: boolean, u: boolean): SxItem {
  let p = cx.p;
  if (p[j] == 92u8) {
    return __sx_escape(cx, j, ws, u);
  }
  let n = __sx_len(p, j);
  return SxItem { kind: SX_LITERAL, start: j, end: j + n, cp: __sx_cp(p, j), hex: false };
}

// Records a literal of a set outside ASCII, which `(?-u)` does not allow.
function __sx_set_literal(cx: SxCtx, x: SxItem, u: boolean): void {
  if (x.kind == SX_LITERAL && x.cp > 127 && !x.hex) {
    cx.checks.set(cx.checks.get().append(SxCheck { kind: 1, start: x.start, end: x.end, text: "", unicode: u }));
  }
}

// The character after the one at `k`, as regex-syntax peeks it under
// `(?x)`, or -1 at the end.
function __sx_peek(p: string, k: i32, ws: boolean): i32 {
  let start = k + __sx_len(p, k);
  if (!ws) {
    return if (start < p.len()) { __sx_cp(p, start) } else { -1 };
  }
  let i = start;
  let comment = false;
  while (i < p.len()) {
    let cp = __sx_cp(p, i);
    if (__sx_is_ws(cp)) {
      i += __sx_len(p, i);
    } else if (!comment && cp == 35) {
      comment = true;
      i += 1;
    } else {
      return cp;
    }
  }
  return if (start < p.len()) { __sx_cp(p, start) } else { -1 };
}

// The message of a class that the lookup does not find, or "".
function __sx_lookup(c: SxCheck): string {
  if (!c.unicode) {
    return "Unicode not allowed here";
  }
  if (c.kind != 0) {
    return "";
  }
  return __sx_property(c.text);
}

// The message of a property that regex-syntax does not find, or "". A
// property that it knows and this module does not is not an error here.
function __sx_property(text: string): string {
  if let Some(_) = unicode.property_ranges(text) {
    return "";
  }
  let (op, width) = __sx_op(text);
  if (op < 0) {
    let key = __sx_key(text);
    let canon = __sx_canonical(key);
    let binary = !__sx_has("cf sc lc", key) && canon != "" && __sx_has(SX_BOOLS, canon);
    return if (binary) { "" } else { "Unicode property not found" };
  }
  let canon = __sx_canonical(__sx_key(slice_unchecked(text, 0, op) + ""));
  if (canon == "") {
    return "Unicode property not found";
  }
  let value = __sx_key(slice_unchecked(text, op + width, text.len()) + "");
  return if (__sx_known_value(canon, value)) { "" } else { "Unicode property value not found" };
}

// Whether regex-syntax knows the value of a property that this module does
// not find.
function __sx_known_value(canon: string, value: string): boolean {
  return match (canon) {
    "Age" => __sx_has(SX_AGES, value),
    "Grapheme_Cluster_Break" => __sx_has(SX_GCB, value),
    "Sentence_Break" => __sx_has(SX_SB, value),
    "Word_Break" => __sx_has(SX_WB, value),
    "Script_Extensions" => unicode.property_ranges("sc=" + value) != None,
    _ => false,
  };
}

function __sx_op(text: string): (i32, i32) {
  for i in 0..text.len() {
    if (text[i] == 33u8 && i + 1 < text.len() && text[i + 1] == 61u8) {
      return (i, 2);
    }
  }
  let colon = __memchr(text, 58, 0);
  if (colon >= 0) {
    return (colon, 1);
  }
  return (__memchr(text, 61, 0), 1);
}

// The loose form of a name, as regex-syntax makes it.
function __sx_key(name: string): string {
  let from = if (name.len() >= 2 && (name[0] | 32u8) == 105u8 && (name[1] | 32u8) == 115u8) { 2 } else { 0 };
  let out = "";
  for i in from..name.len() {
    if (__sx_name_byte(name[i])) {
      out += slice_unchecked(name, i, i + 1).to_ascii_lower();
    }
  }
  return if (from == 2 && out == "c") { "isc" } else { out };
}

// Whether a byte of a name counts: not a space, an underscore, a hyphen or
// a byte outside ASCII.
function __sx_name_byte(c: u8): boolean {
  return c != 32u8 && c != 95u8 && c != 45u8 && c < 128u8;
}

// Whether the list of names with a space between them holds `name`.
function __sx_has(list: string, name: string): boolean {
  let at = 0;
  while (at <= list.len()) {
    let sp = __memchr(list, 32, at);
    let end = if (sp < 0) { list.len() } else { sp };
    if (end - at == name.len() && __sx_at(list, at, name)) {
      return true;
    }
    if (sp < 0) {
      return false;
    }
    at = sp + 1;
  }
  return false;
}

// The canonical name of the property of a loose name, or "".
function __sx_canonical(key: string): string {
  let at = 0;
  while (at < SX_PROPERTIES.len()) {
    let eq = __memchr(SX_PROPERTIES, 61, at);
    let semi = __memchr(SX_PROPERTIES, 59, eq);
    if (eq - at == key.len() && __sx_at(SX_PROPERTIES, at, key)) {
      return slice_unchecked(SX_PROPERTIES, eq + 1, semi) + "";
    }
    at = semi + 1;
  }
  return "";
}

// The line and the column from 1 of the byte offset `at` of the pattern.
// A column counts characters.
function __sx_position(p: string, at: i32): (i32, i32) {
  let line = 1;
  let col = 1;
  let i = 0;
  while (i < at && i < p.len()) {
    if (p[i] == 10u8) {
      line += 1;
      col = 1;
    } else {
      col += 1;
    }
    i += __sx_len(p, i);
  }
  return (line, col);
}

// The lines of the pattern as Rust's str::lines gives them: a CR before a
// line feed does not count, and a last empty line is not there.
function __sx_lines(p: string): string[] {
  let out: string[] = [];
  let at = 0;
  while (at < p.len()) {
    let nl = __memchr(p, 10, at);
    let end = if (nl < 0) { p.len() } else { nl };
    let stop = if (nl >= 0 && end > at && p[end - 1] == 13u8) { end - 1 } else { end };
    out = out.append(slice_unchecked(p, at, stop) + "");
    at = end + 1;
  }
  return out;
}

impl error.Error for SyntaxError {}

impl cmp.Display for SyntaxError {}

pub function (e: SyntaxError) message(): string {
  return e.report();
}

pub function (e: SyntaxError) to_string(): string {
  return e.report();
}

// report gives the text of the error as the regex crate writes it: the
// pattern with a line of `^` under each span, and the message.
pub function (e: SyntaxError) report(): string {
  let p = e.pattern;
  let spans = [e.start, e.end];
  if (e.aux_start >= 0) {
    spans = if (e.aux_start < e.start) { [e.aux_start, e.aux_end, e.start, e.end] } else { spans.append(e.aux_start).append(e.aux_end) };
  }
  let notes = __sx_notes(p, spans);
  if (__memchr(p, 10, 0) < 0) {
    return "regex parse error:\n" + notes + "error: " + e.message;
  }
  let divider = "~".repeat(79);
  let out = "regex parse error:\n" + divider + "\n" + notes + divider + "\n";
  let multi = __sx_multi_line(p, spans);
  if (multi.len() > 0) {
    out += multi.join("\n") + "\n";
  }
  return out + "error: " + e.message;
}

// Each line of the pattern after its number, with the line of `^` under
// the spans on it.
function __sx_notes(p: string, spans: i32[]): string {
  let lines = __sx_lines(p);
  let count = lines.len() + (if (p.len() > 0 && p[p.len() - 1] == 10u8) { 1 } else { 0 });
  let width = if (count <= 1) { 0 } else { count.to_string().len() };
  let notes = "";
  for i in 0..lines.len() {
    let n = (i + 1).to_string();
    notes += if (width > 0) { " ".repeat(width - n.len()) + n + ": " } else { "    " };
    notes += lines[i] + "\n";
    let marks = __sx_marks(p, spans, i + 1, if (width == 0) { 4 } else { 2 + width });
    notes += if (marks != "") { marks + "\n" } else { "" };
  }
  return notes;
}

// The notes of the spans that cover more than one line.
function __sx_multi_line(p: string, spans: i32[]): string[] {
  let out: string[] = [];
  let k = 0;
  while (k < spans.len()) {
    let (sl, sc) = __sx_position(p, spans[k]);
    let (el, ec) = __sx_position(p, spans[k + 1]);
    if (sl != el) {
      out = out.append(f"on line {sl} (column {sc}) through line {el} (column {ec - 1})");
    }
    k += 2;
  }
  return out;
}

// The line of `^` under the spans on line `n`, or "".
function __sx_marks(p: string, spans: i32[], n: i32, pad: i32): string {
  let out = "";
  let pos = 0;
  let k = 0;
  while (k < spans.len()) {
    let (sl, sc) = __sx_position(p, spans[k]);
    let (el, ec) = __sx_position(p, spans[k + 1]);
    if (sl == n && el == n) {
      if (out == "") {
        out = " ".repeat(pad);
      }
      while (pos < sc - 1) {
        out += " ";
        pos += 1;
      }
      let len = if (ec - sc > 1) { ec - sc } else { 1 };
      out += "^".repeat(len);
      pos += len;
    }
    k += 2;
  }
  return out;
}

// The loose names of the properties that regex-syntax 0.8.2 knows, each with "=", its canonical name and ";".
const SX_PROPERTIES: string = "age=Age;ahex=ASCII_Hex_Digit;alpha=Alphabetic;alphabetic=Alphabetic;asciihexdigit=ASCII_Hex_Digit;bc=Bidi_Class;bidic=Bidi_Control;bidiclass=Bidi_Class;bidicontrol=Bidi_Control;bidim=Bidi_Mirrored;bidimirrored=Bidi_Mirrored;bidimirroringglyph=Bidi_Mirroring_Glyph;bidipairedbracket=Bidi_Paired_Bracket;bidipairedbrackettype=Bidi_Paired_Bracket_Type;blk=Block;block=Block;bmg=Bidi_Mirroring_Glyph;bpb=Bidi_Paired_Bracket;bpt=Bidi_Paired_Bracket_Type;canonicalcombiningclass=Canonical_Combining_Class;cased=Cased;casefolding=Case_Folding;caseignorable=Case_Ignorable;ccc=Canonical_Combining_Class;ce=Composition_Exclusion;cf=Case_Folding;changeswhencasefolded=Changes_When_Casefolded;changeswhencasemapped=Changes_When_Casemapped;changeswhenlowercased=Changes_When_Lowercased;changeswhennfkccasefolded=Changes_When_NFKC_Casefolded;changeswhentitlecased=Changes_When_Titlecased;changeswhenuppercased=Changes_When_Uppercased;ci=Case_Ignorable;cjkaccountingnumeric=kAccountingNumeric;cjkcompatibilityvariant=kCompatibilityVariant;cjkiicore=kIICore;cjkirggsource=kIRG_GSource;cjkirghsource=kIRG_HSource;cjkirgjsource=kIRG_JSource;cjkirgkpsource=kIRG_KPSource;cjkirgksource=kIRG_KSource;cjkirgmsource=kIRG_MSource;cjkirgssource=kIRG_SSource;cjkirgtsource=kIRG_TSource;cjkirguksource=kIRG_UKSource;cjkirgusource=kIRG_USource;cjkirgvsource=kIRG_VSource;cjkothernumeric=kOtherNumeric;cjkprimarynumeric=kPrimaryNumeric;cjkrsunicode=kRSUnicode;compex=Full_Composition_Exclusion;compositionexclusion=Composition_Exclusion;cwcf=Changes_When_Casefolded;cwcm=Changes_When_Casemapped;cwkcf=Changes_When_NFKC_Casefolded;cwl=Changes_When_Lowercased;cwt=Changes_When_Titlecased;cwu=Changes_When_Uppercased;dash=Dash;decompositionmapping=Decomposition_Mapping;decompositiontype=Decomposition_Type;defaultignorablecodepoint=Default_Ignorable_Code_Point;dep=Deprecated;deprecated=Deprecated;di=Default_Ignorable_Code_Point;dia=Diacritic;diacritic=Diacritic;dm=Decomposition_Mapping;dt=Decomposition_Type;ea=East_Asian_Width;eastasianwidth=East_Asian_Width;ebase=Emoji_Modifier_Base;ecomp=Emoji_Component;emod=Emoji_Modifier;emoji=Emoji;emojicomponent=Emoji_Component;emojimodifier=Emoji_Modifier;emojimodifierbase=Emoji_Modifier_Base;emojipresentation=Emoji_Presentation;epres=Emoji_Presentation;equideo=Equivalent_Unified_Ideograph;equivalentunifiedideograph=Equivalent_Unified_Ideograph;expandsonnfc=Expands_On_NFC;expandsonnfd=Expands_On_NFD;expandsonnfkc=Expands_On_NFKC;expandsonnfkd=Expands_On_NFKD;ext=Extender;extendedpictographic=Extended_Pictographic;extender=Extender;extpict=Extended_Pictographic;fcnfkc=FC_NFKC_Closure;fcnfkcclosure=FC_NFKC_Closure;fullcompositionexclusion=Full_Composition_Exclusion;gc=General_Category;gcb=Grapheme_Cluster_Break;generalcategory=General_Category;graphemebase=Grapheme_Base;graphemeclusterbreak=Grapheme_Cluster_Break;graphemeextend=Grapheme_Extend;graphemelink=Grapheme_Link;grbase=Grapheme_Base;grext=Grapheme_Extend;grlink=Grapheme_Link;hangulsyllabletype=Hangul_Syllable_Type;hex=Hex_Digit;hexdigit=Hex_Digit;hst=Hangul_Syllable_Type;hyphen=Hyphen;idc=ID_Continue;idcontinue=ID_Continue;ideo=Ideographic;ideographic=Ideographic;ids=ID_Start;idsb=IDS_Binary_Operator;idsbinaryoperator=IDS_Binary_Operator;idst=IDS_Trinary_Operator;idstart=ID_Start;idstrinaryoperator=IDS_Trinary_Operator;indicpositionalcategory=Indic_Positional_Category;indicsyllabiccategory=Indic_Syllabic_Category;inpc=Indic_Positional_Category;insc=Indic_Syllabic_Category;isc=ISO_Comment;jamoshortname=Jamo_Short_Name;jg=Joining_Group;joinc=Join_Control;joincontrol=Join_Control;joininggroup=Joining_Group;joiningtype=Joining_Type;jsn=Jamo_Short_Name;jt=Joining_Type;kaccountingnumeric=kAccountingNumeric;kcompatibilityvariant=kCompatibilityVariant;kiicore=kIICore;kirggsource=kIRG_GSource;kirghsource=kIRG_HSource;kirgjsource=kIRG_JSource;kirgkpsource=kIRG_KPSource;kirgksource=kIRG_KSource;kirgmsource=kIRG_MSource;kirgssource=kIRG_SSource;kirgtsource=kIRG_TSource;kirguksource=kIRG_UKSource;kirgusource=kIRG_USource;kirgvsource=kIRG_VSource;kothernumeric=kOtherNumeric;kprimarynumeric=kPrimaryNumeric;krsunicode=kRSUnicode;lb=Line_Break;lc=Lowercase_Mapping;linebreak=Line_Break;loe=Logical_Order_Exception;logicalorderexception=Logical_Order_Exception;lower=Lowercase;lowercase=Lowercase;lowercasemapping=Lowercase_Mapping;math=Math;na=Name;na1=Unicode_1_Name;name=Name;namealias=Name_Alias;nchar=Noncharacter_Code_Point;nfcqc=NFC_Quick_Check;nfcquickcheck=NFC_Quick_Check;nfdqc=NFD_Quick_Check;nfdquickcheck=NFD_Quick_Check;nfkccasefold=NFKC_Casefold;nfkccf=NFKC_Casefold;nfkcqc=NFKC_Quick_Check;nfkcquickcheck=NFKC_Quick_Check;nfkdqc=NFKD_Quick_Check;nfkdquickcheck=NFKD_Quick_Check;noncharactercodepoint=Noncharacter_Code_Point;nt=Numeric_Type;numerictype=Numeric_Type;numericvalue=Numeric_Value;nv=Numeric_Value;oalpha=Other_Alphabetic;ocomment=ISO_Comment;odi=Other_Default_Ignorable_Code_Point;ogrext=Other_Grapheme_Extend;oidc=Other_ID_Continue;oids=Other_ID_Start;olower=Other_Lowercase;omath=Other_Math;otheralphabetic=Other_Alphabetic;otherdefaultignorablecodepoint=Other_Default_Ignorable_Code_Point;othergraphemeextend=Other_Grapheme_Extend;otheridcontinue=Other_ID_Continue;otheridstart=Other_ID_Start;otherlowercase=Other_Lowercase;othermath=Other_Math;otheruppercase=Other_Uppercase;oupper=Other_Uppercase;patsyn=Pattern_Syntax;patternsyntax=Pattern_Syntax;patternwhitespace=Pattern_White_Space;patws=Pattern_White_Space;pcm=Prepended_Concatenation_Mark;prependedconcatenationmark=Prepended_Concatenation_Mark;qmark=Quotation_Mark;quotationmark=Quotation_Mark;radical=Radical;regionalindicator=Regional_Indicator;ri=Regional_Indicator;sb=Sentence_Break;sc=Script;scf=Simple_Case_Folding;script=Script;scriptextensions=Script_Extensions;scx=Script_Extensions;sd=Soft_Dotted;sentencebreak=Sentence_Break;sentenceterminal=Sentence_Terminal;sfc=Simple_Case_Folding;simplecasefolding=Simple_Case_Folding;simplelowercasemapping=Simple_Lowercase_Mapping;simpletitlecasemapping=Simple_Titlecase_Mapping;simpleuppercasemapping=Simple_Uppercase_Mapping;slc=Simple_Lowercase_Mapping;softdotted=Soft_Dotted;space=White_Space;stc=Simple_Titlecase_Mapping;sterm=Sentence_Terminal;suc=Simple_Uppercase_Mapping;tc=Titlecase_Mapping;term=Terminal_Punctuation;terminalpunctuation=Terminal_Punctuation;titlecasemapping=Titlecase_Mapping;uc=Uppercase_Mapping;uideo=Unified_Ideograph;unicode1name=Unicode_1_Name;unicoderadicalstroke=kRSUnicode;unifiedideograph=Unified_Ideograph;upper=Uppercase;uppercase=Uppercase;uppercasemapping=Uppercase_Mapping;urs=kRSUnicode;variationselector=Variation_Selector;verticalorientation=Vertical_Orientation;vo=Vertical_Orientation;vs=Variation_Selector;wb=Word_Break;whitespace=White_Space;wordbreak=Word_Break;wspace=White_Space;xidc=XID_Continue;xidcontinue=XID_Continue;xids=XID_Start;xidstart=XID_Start;xonfc=Expands_On_NFC;xonfd=Expands_On_NFD;xonfkc=Expands_On_NFKC;xonfkd=Expands_On_NFKD;";

// The canonical names of the binary properties of regex-syntax 0.8.2.
const SX_BOOLS: string = "ASCII_Hex_Digit Alphabetic Bidi_Control Bidi_Mirrored Case_Ignorable Cased Changes_When_Casefolded Changes_When_Casemapped Changes_When_Lowercased Changes_When_Titlecased Changes_When_Uppercased Dash Default_Ignorable_Code_Point Deprecated Diacritic Emoji Emoji_Component Emoji_Modifier Emoji_Modifier_Base Emoji_Presentation Extended_Pictographic Extender Grapheme_Base Grapheme_Extend Grapheme_Link Hex_Digit Hyphen IDS_Binary_Operator IDS_Trinary_Operator ID_Continue ID_Start Ideographic Join_Control Logical_Order_Exception Lowercase Math Noncharacter_Code_Point Other_Alphabetic Other_Default_Ignorable_Code_Point Other_Grapheme_Extend Other_ID_Continue Other_ID_Start Other_Lowercase Other_Math Other_Uppercase Pattern_Syntax Pattern_White_Space Prepended_Concatenation_Mark Quotation_Mark Radical Regional_Indicator Sentence_Terminal Soft_Dotted Terminal_Punctuation Unified_Ideograph Uppercase Variation_Selector White_Space XID_Continue XID_Start";

// The loose values of Age in regex-syntax 0.8.2 that select code points.
const SX_AGES: string = "1.1 10.0 11.0 12.0 12.1 13.0 14.0 15.0 2.0 2.1 3.0 3.1 3.2 4.0 4.1 5.0 5.1 5.2 6.0 6.1 6.2 6.3 7.0 8.0 9.0 v100 v11 v110 v120 v121 v130 v140 v150 v20 v21 v30 v31 v32 v40 v41 v50 v51 v52 v60 v61 v62 v63 v70 v80 v90";

// The loose values of Grapheme_Cluster_Break in regex-syntax 0.8.2 other than Other.
const SX_GCB: string = "cn control cr eb ebase ebasegaz ebg em emodifier ex extend gaz glueafterzwj l lf lv lvt pp prepend regionalindicator ri sm spacingmark t v zwj";

// The loose values of Sentence_Break in regex-syntax 0.8.2 other than Other.
const SX_SB: string = "at aterm cl close cr ex extend fo format le lf lo lower nu numeric oletter sc scontinue se sep sp st sterm up upper";

// The loose values of Word_Break in regex-syntax 0.8.2 other than Other.
const SX_WB: string = "aletter cr doublequote dq eb ebase ebasegaz ebg em emodifier ex extend extendnumlet fo format gaz glueafterzwj hebrewletter hl ka katakana le lf mb midletter midnum midnumlet ml mn newline nl nu numeric regionalindicator ri singlequote sq wsegspace zwj";
```

### Module introduction

```fern
<<module introduction>>=
// Compiled regular expressions with checked syntax and UTF-8 output.
```
