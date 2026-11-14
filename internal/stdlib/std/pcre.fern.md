# Perl-compatible regular expressions

This module matches the regular expressions of PCRE2 10.42 with a backtracking engine.

## Model

`compile` parses a pattern with the syntax of PCRE2 and gives a program. A search tries each start from left to right. At a start, the engine runs the program and keeps a stack of the choices that it did not take. When a step fails, the engine goes back to the last choice. Thus the first match is the match of Perl and PCRE2, with the priority of the alternatives and of the repetitions. A match can need a look back or a reference to an earlier group, which a DFA cannot do.

A look-ahead, a look-behind and an atomic group run as a search of their own. When that search succeeds, its choices are gone, as in PCRE2. A possessive repetition is an atomic group.

With `utf`, the pattern and the text are UTF-8, and each item matches a character. A byte that does not start a valid character matches no item, as with `PCRE2_MATCH_INVALID_UTF`. `\d`, `\s`, `\w`, `\b` and the POSIX classes then use the properties of Unicode, as with `PCRE2_UCP`. Without `utf`, each item matches a byte, and the classes are those of ASCII.

## Contracts

A pattern that PCRE2 refuses gives a `CompileError` with the text and the offset of PCRE2's error. A search that takes more choices than the match limit gives `MatchLimit`, as PCRE2 does. The default limit is ten million, as in PCRE2. Its text is that of PCRE2.

A search gives the leftmost match. `find_at` starts at a byte offset, and the assertions see the whole text. `captures_at` gives the start and the end of each group, or -1 for a group that did not match.

## Executable example

```fern test name=std-pcre-example
import "std/pcre";

function main(): i32 {
  let Ok(re) = pcre.compile("(\\w)\\1", pcre.options()) else { return 1; };
  let Ok(Some(m)) = re.find_at("abccd", 0) else { return 2; };
  if (m.start != 2 || m.end != 4) { return 3; }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
// This module matches the regular expressions of PCRE2 10.42 with a
// backtracking engine.

<<types>>

<<parser>>

<<classes>>

<<compiler>>

<<matcher>>
```

### Types

```fern
<<types>>=
import "std/option";
import "std/unicode";

// The options of PCRE2 that a search can set: PCRE2_CASELESS,
// PCRE2_MULTILINE, PCRE2_DOTALL, PCRE2_EXTENDED, PCRE2_UTF with PCRE2_UCP, the
// newline convention ANYCRLF in place of LF, and the match limit.
pub struct Options { caseless: boolean, multiline: boolean, dotall: boolean, extended: boolean, utf: boolean, crlf: boolean, match_limit: i32 }

// The options with each one off, and the default match limit of PCRE2.
pub function options(): Options {
  return Options { caseless: false, multiline: false, dotall: false, extended: false, utf: false, crlf: false, match_limit: MATCH_LIMIT };
}

// The error of a pattern: the offset where PCRE2 finds it, the number of
// the error and its text.
pub struct CompileError { offset: i32, code: i32, message: string }

pub function (e: CompileError) to_string(): string {
  return "PCRE2: error compiling pattern at offset " + e.offset.to_string() + ": " + e.message;
}

// The error of a search that takes too many steps.
pub enum MatchError { MatchLimit }

pub function (e: MatchError) to_string(): string {
  return "PCRE2: error matching: match limit exceeded";
}

pub struct Match { start: i32, end: i32 }

// The version of PCRE2 whose syntax and matches the module follows.
pub function version(): string {
  return "10.42";
}

// A compiled pattern. `code` holds the program, three words for each
// instruction, and `subs` four words for each search of its own. `nslots`
// counts the words of a state: two for each group, then one for each loop
// that can match an empty string. `first` marks each byte that can start a
// match, or is empty. A match holds the byte `req`, unless it is -1.
pub struct Pcre {
  code: i32[],
  subs: i32[],
  calls: i32[],
  sets: PSet[],
  ngroups: i32,
  names: string[],
  name_groups: i32[],
  nslots: i32,
  utf: boolean,
  crlf: boolean,
  anchored: boolean,
  first: u8[],
  req: i32,
  word: PSet,
  limit: i32,
}

// The number of the groups, without group 0.
pub function (re: Pcre) group_count(): i32 {
  return re.ngroups;
}

// The number of the group of a name, or -1.
pub function (re: Pcre) group_index(name: string): i32 {
  for k in 0..re.names.len() {
    if (re.names[k] == name) {
      return re.name_groups[k];
    }
  }
  return -1;
}

// The names of the groups, in the order of their numbers.
pub function (re: Pcre) group_names(): string[] {
  return re.names;
}

// A set of characters: the bits of the first 256 code points, and the
// ranges of the others, which `neg` makes the complement.
struct PSet { bits: i32[], ranges: i32[], neg: boolean }

enum PNode {
  NChar(i32),
  NSet(i32),
  NAny(boolean),
  NSeq(PNode[]),
  NAlt(PNode[]),
  NGroup(PGroupData),
  NRep(PRepData),
  NAtomic(PAtomicData),
  NLook(PLookData),
  NBack(PBackData),
  NAssert(i32),
  NKeep,
  NCond(PCondData),
  NCall(PCallData),
  NAccept,
  NFail,
  NEmpty,
}

struct PGroupData { idx: i32, node: PNode }

// A repetition: `kind` is 0 for greedy, 1 for lazy and 2 for possessive. A
// `max` of -1 has no limit.
struct PRepData { node: PNode, min: i32, max: i32, kind: i32 }

struct PAtomicData { node: PNode }

// A look-around: `kind` is LOOK_AHEAD, LOOK_NOT_AHEAD, LOOK_BEHIND or
// LOOK_NOT_BEHIND.
struct PLookData { kind: i32, node: PNode, at: i32 }

// A back reference to a group by number, or by name when `name` is not "".
// `at` is the offset of the reference, for an error.
struct PBackData { group: i32, name: string, caseless: boolean, at: i32 }

// A conditional group: the condition is that a group is set, or with `look`
// that the look-around matches.
// A call of a group as a subroutine: by number, 0 for the whole pattern,
// or by name. `at` is the offset for an error.
struct PCallData { group: i32, name: string, at: i32 }

struct PCondData { group: i32, name: string, look: Option[PLookData], yes: PNode, no: PNode, at: i32 }

// The conditions (?(R)...) and (?(DEFINE)...).
const COND_RECURSE: i32 = -2;

const COND_DEFINE: i32 = -3;

const LOOK_AHEAD: i32 = 1;

const LOOK_NOT_AHEAD: i32 = 2;

const LOOK_BEHIND: i32 = 3;

const LOOK_NOT_BEHIND: i32 = 4;

const ATOMIC: i32 = 0;

// The assertions.
const A_BOL: i32 = 1;

const A_MBOL: i32 = 2;

const A_EOL: i32 = 3;

const A_MEOL: i32 = 4;

const A_START: i32 = 5;

const A_END: i32 = 6;

const A_END_NL: i32 = 7;

const A_G: i32 = 8;

const A_WORD: i32 = 9;

const A_NOT_WORD: i32 = 10;

// The flags of a pattern.
const F_I: i32 = 1;

const F_M: i32 = 2;

const F_S: i32 = 4;

const F_X: i32 = 8;

const F_XX: i32 = 16;

const F_N: i32 = 32;

const F_U: i32 = 64;

const MAX_REPEAT: i32 = 65535;

const MATCH_LIMIT: i32 = 10000000;
```

### Parser

The parser reads the pattern from left to right, as PCRE2 does, and it reports the first error that PCRE2 reports. The context holds that error, the count of the groups and their names.

```fern
<<parser>>=
struct PCtx {
  p: string,
  utf: boolean,
  err: Cell[Option[CompileError]],
  groups: Cell[i32],
  names: Cell[string[]],
  name_groups: Cell[i32[]],
  sets: Cell[PSet[]],
  total: i32,
  bodies: Cell[PGroupData[]],
  glen: Cell[i32[]],
}

struct PParse { node: PNode, pos: i32, f: i32 }

// Records an error, unless an earlier one is there.
function __pc_fail(cx: PCtx, at: i32, code: i32): void {
  if (cx.err.get().is_none()) {
    cx.err.set(Some(CompileError { offset: at, code: code, message: __pc_message(code) }));
  }
}

function __pc_failed(cx: PCtx): boolean {
  return cx.err.get().is_some();
}

// One arm for each name.
// fern-lint: allow cyclomatic-complexity
function __pc_message(code: i32): string {
  return match (code) {
    101 => "\\ at end of pattern",
    102 => "\\c at end of pattern",
    103 => "unrecognized character follows \\",
    104 => "numbers out of order in {} quantifier",
    105 => "number too big in {} quantifier",
    106 => "missing terminating ] for character class",
    107 => "escape sequence is invalid in character class",
    108 => "range out of order in character class",
    109 => "quantifier does not follow a repeatable item",
    111 => "unrecognized character after (? or (?-",
    112 => "POSIX named classes are supported only within a class",
    113 => "POSIX collating elements are not supported",
    114 => "missing closing parenthesis",
    115 => "reference to non-existent subpattern",
    118 => "missing ) after (?# comment",
    119 => "parentheses are too deeply nested",
    120 => "regular expression is too large",
    122 => "unmatched closing parenthesis",
    124 => "missing closing parenthesis for condition",
    125 => "lookbehind assertion is not fixed length",
    126 => "a relative value of zero is not allowed",
    127 => "conditional subpattern contains more than two branches",
    128 => "assertion expected after (?( or (?(?C)",
    130 => "unknown POSIX class name",
    134 => "character code point value in \\x{} or \\o{} is too large",
    137 => "PCRE2 does not support \\F, \\L, \\l, \\N{name}, \\U, or \\u",
    141 => "unrecognized character after (?P",
    142 => "syntax error in subpattern name (missing terminator?)",
    143 => "two named subpatterns have the same name (PCRE2_DUPNAMES not set)",
    144 => "subpattern name must start with a non-digit",
    146 => "malformed \\P or \\p sequence",
    147 => "unknown property after \\P or \\p",
    148 => "subpattern name is too long (maximum 32 code units)",
    150 => "invalid range in character class",
    155 => "missing opening brace after \\o",
    157 => "\\g is not followed by a braced, angle-bracketed, or quoted name/number or by a plain number",
    160 => "(*VERB) not recognized or malformed",
    161 => "subpattern number is too big",
    162 => "subpattern name expected",
    164 => "non-octal character in \\o{} (closing brace missing?)",
    167 => "non-hex character in \\x{} (closing brace missing?)",
    168 => "\\c must be followed by a printable ASCII character",
    169 => "\\k is not followed by a braced, angle-bracketed, or quoted name",
    171 => "\\N is not supported in a class",
    173 => "disallowed Unicode code point (>= 0xd800 && <= 0xdfff)",
    178 => "digits missing in \\x{} or \\o{} or \\N{U+}",
    194 => "invalid hyphen in option setting",
    193 => "\\N{U+dddd} is supported only in Unicode (UTF) mode",
    199 => "\\K is not allowed in lookarounds (but see PCRE2_EXTRA_ALLOW_LOOKAROUND_BSK)",
    _ => "internal error",
  };
}

pub function compile(pattern: string, o: Options): Result[Pcre, CompileError] {
  let none: string[] = [];
  let gs: i32[] = [];
  let ss: PSet[] = [];
  let bodies: PGroupData[] = [];
  let cx = PCtx {
    p: pattern, utf: o.utf, err: cell_new(None), groups: cell_new(0), names: cell_new(none), name_groups: cell_new(gs),
    sets: cell_new(ss), total: __pc_count_groups(pattern), bodies: cell_new(bodies), glen: cell_new(gs),
  };
  let f = (if (o.caseless) { F_I } else { 0 }) | (if (o.multiline) { F_M } else { 0 }) | (if (o.dotall) { F_S } else { 0 }) | (if (o.extended) { F_X } else { 0 });
  let r = __pc_alt(cx, 0, f, 0);
  if (!__pc_failed(cx) && r.pos < pattern.len()) {
    __pc_fail(cx, r.pos, 122);
  }
  if (!__pc_failed(cx)) {
    cx.glen.set(__pc_group_lengths(cx));
    __pc_check(cx, r.node, false);
  }
  if let Some(e) = cx.err.get() {
    return Err(e);
  }
  return __pc_build(cx, r.node, o);
}

// The count of the capture groups of the pattern, for the rule that reads
// `\10` as a reference when the pattern has ten groups.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_count_groups(p: string): i32 {
  let n = 0;
  let i = 0;
  let in_class = false;
  while (i < p.len()) {
    let c = p[i] as i32;
    if (c == 92) {
      i += 2;
      continue;
    }
    if (in_class) {
      in_class = c != 93;
    } else if (c == 91) {
      in_class = true;
    } else if (c == 40 && (i + 1 >= p.len() || p[i + 1] != 63u8 && p[i + 1] != 42u8) && !(i >= 2 && p[i - 1] == 63u8 && p[i - 2] == 40u8)) {
      n += 1;
    } else if (c == 40 && i + 2 < p.len() && p[i + 1] == 63u8 && (p[i + 2] == 60u8 && i + 3 < p.len() && p[i + 3] != 61u8 && p[i + 3] != 33u8 || p[i + 2] == 39u8 || p[i + 2] == 80u8 && i + 3 < p.len() && p[i + 3] == 60u8)) {
      n += 1;
    }
    i += 1;
  }
  return n;
}

// Alternatives up to a `)` or the end. An option set in one alternative
// holds for the next ones.
function __pc_alt(cx: PCtx, at: i32, f0: i32, depth: i32): PParse {
  if (depth > 250) {
    __pc_fail(cx, at, 119);
    return PParse { node: NEmpty, pos: cx.p.len(), f: f0 };
  }
  let branches: PNode[] = [];
  let pos = at;
  let f = f0;
  while (true) {
    let s = __pc_seq(cx, pos, f, depth);
    branches = branches.append(s.node);
    pos = s.pos;
    f = s.f;
    if (__pc_failed(cx) || pos >= cx.p.len() || cx.p[pos] != 124u8) {
      break;
    }
    pos += 1;
  }
  let node = if (branches.len() == 1) { branches[0] } else { NAlt(branches) };
  return PParse { node: node, pos: pos, f: f };
}

// The items of one alternative, up to a `|`, a `)` or the end.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_seq(cx: PCtx, at: i32, f0: i32, depth: i32): PParse {
  let items: PNode[] = [];
  let pos = at;
  let f = f0;
  let p = cx.p;
  while (pos < p.len() && !__pc_failed(cx)) {
    let c = p[pos] as i32;
    if (c == 124 || c == 41) {
      break;
    }
    if ((f & F_X) != 0 && (c == 32 || c == 9 || c == 10 || c == 11 || c == 12 || c == 13)) {
      pos += 1;
      continue;
    }
    if ((f & F_X) != 0 && c == 35) {
      while (pos < p.len() && p[pos] != 10u8) {
        pos += 1;
      }
      continue;
    }
    if (c == 92 && pos + 1 < p.len() && p[pos + 1] == 81u8) {
      let q = __pc_quoted(cx, pos + 2, f);
      items = items.concat(q.0);
      pos = q.1;
      continue;
    }
    if (c == 92 && pos + 1 < p.len() && p[pos + 1] == 69u8) {
      pos += 2;
      continue;
    }
    if (c == 40 && pos + 2 < p.len() && p[pos + 1] == 63u8 && __pc_is_flag_group(p, pos + 2)) {
      let fl = __pc_flags(cx, pos + 2, f);
      if (fl.1 < p.len() && p[fl.1] == 41u8) {
        f = fl.0;
        pos = fl.1 + 1;
        continue;
      }
    }
    if (c == 40 && pos + 2 < p.len() && p[pos + 1] == 63u8 && p[pos + 2] == 35u8) {
      let close = __memchr(p, 41, pos);
      if (close < 0) {
        __pc_fail(cx, p.len(), 118);
        break;
      }
      pos = close + 1;
      continue;
    }
    let a = __pc_atom(cx, pos, f, depth);
    if (__pc_failed(cx)) {
      break;
    }
    let q = __pc_quant(cx, a, f);
    items = items.append(q.node);
    pos = q.pos;
  }
  if (items.len() == 1) {
    return PParse { node: items[0], pos: pos, f: f };
  }
  return PParse { node: NSeq(items), pos: pos, f: f };
}

// The literal characters of \Q...\E, from `at`.
function __pc_quoted(cx: PCtx, at: i32, f: i32): (PNode[], i32) {
  let out: PNode[] = [];
  let pos = at;
  let p = cx.p;
  while (pos < p.len()) {
    if (p[pos] == 92u8 && pos + 1 < p.len() && p[pos + 1] == 69u8) {
      return (out, pos + 2);
    }
    let (cp, n) = __pc_pat_char(cx, pos);
    out = out.append(__pc_char_node(cx, cp, f));
    pos += n;
  }
  return (out, pos);
}

// Whether a group at `at`, after `(?`, only sets options: (?i) or (?i-s:...).
function __pc_is_flag_group(p: string, at: i32): boolean {
  let c = p[at] as i32;
  if (c == 45 && at + 1 < p.len() && p[at + 1] >= 48u8 && p[at + 1] <= 57u8) {
    return false;
  }
  return c == 94 || c == 45 || __memchr("imnsxJU", c, 0) >= 0;
}

// The options after the letters at `at`, and the offset after them.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_flags(cx: PCtx, at: i32, f0: i32): (i32, i32) {
  let p = cx.p;
  let f = f0;
  let pos = at;
  let on = true;
  if (pos < p.len() && p[pos] == 94u8) {
    f &= F_U;
    pos += 1;
  }
  while (pos < p.len()) {
    let c = p[pos] as i32;
    let bit = match (c) {
      105 => F_I,
      109 => F_M,
      110 => F_N,
      115 => F_S,
      120 => F_X,
      85 => F_U,
      74 => 0,
      _ => -1,
    };
    if (c == 45) {
      if (!on || at < pos && p[at] == 94u8) {
        __pc_fail(cx, pos + 1, 194);
        return (f, p.len());
      }
      on = false;
      pos += 1;
      continue;
    }
    if (bit < 0) {
      break;
    }
    if (c == 120 && pos + 1 < p.len() && p[pos + 1] == 120u8) {
      f = if (on) { f | F_X | F_XX } else { f & ~(F_X | F_XX) };
      pos += 2;
      continue;
    }
    f = if (on) { f | bit } else { f & ~bit };
    if (c == 120 && !on) {
      f &= ~F_XX;
    }
    pos += 1;
  }
  if (pos >= p.len()) {
    __pc_fail(cx, pos, 114);
    return (f, p.len());
  }
  if (p[pos] != 41u8 && p[pos] != 58u8) {
    __pc_fail(cx, pos, 111);
    return (f, p.len());
  }
  return (f, pos);
}

// The node of a literal character under the options `f`.
function __pc_char_node(cx: PCtx, cp: i32, f: i32): PNode {
  if ((f & F_I) == 0 || !__pc_has_case(cx, cp)) {
    return NChar(cp);
  }
  return NSet(__pc_add_set(cx, __pc_set_of(cx, [cp, cp], false, true)));
}

function __pc_has_case(cx: PCtx, cp: i32): boolean {
  if (cp < 128 || !cx.utf) {
    return cp < 128 && ((cp | 32) >= 97 && (cp | 32) <= 122);
  }
  return unicode.simple_fold(cp as char) as i32 != cp;
}

// The character at `at` of the pattern, and its count of bytes.
function __pc_pat_char(cx: PCtx, at: i32): (i32, i32) {
  let b = cx.p[at] as i32;
  if (!cx.utf || b < 128) {
    return (b, 1);
  }
  return __pc_decode(cx.p, at);
}

// One item: a character, a class, a group, an escape or an assertion.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_atom(cx: PCtx, at: i32, f: i32, depth: i32): PParse {
  let p = cx.p;
  let c = p[at] as i32;
  if (c == 40) {
    return __pc_group(cx, at, f, depth);
  }
  if (c == 91) {
    return __pc_class(cx, at, f);
  }
  if (c == 92) {
    return __pc_escape(cx, at, f);
  }
  if (c == 46) {
    return PParse { node: NAny((f & F_S) != 0), pos: at + 1, f: f };
  }
  if (c == 94) {
    return PParse { node: NAssert(if ((f & F_M) != 0) { A_MBOL } else { A_BOL }), pos: at + 1, f: f };
  }
  if (c == 36) {
    return PParse { node: NAssert(if ((f & F_M) != 0) { A_MEOL } else { A_EOL }), pos: at + 1, f: f };
  }
  if (c == 42 || c == 43 || c == 63) {
    __pc_fail(cx, at, 109);
    return PParse { node: NEmpty, pos: at + 1, f: f };
  }
  if (c == 123 && __pc_brace(p, at).ok) {
    __pc_fail(cx, __pc_brace(p, at).end - 1, 109);
    return PParse { node: NEmpty, pos: at + 1, f: f };
  }
  let (cp, n) = __pc_pat_char(cx, at);
  return PParse { node: __pc_char_node(cx, cp, f), pos: at + n, f: f };
}

// A quantifier {n}, {n,} or {n,m} at `at`: whether it is one, its bounds,
// the offset after it, and the offset of a number that is too big.
struct PBrace { ok: boolean, min: i32, max: i32, end: i32, big: i32, order: boolean }

// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_brace(p: string, at: i32): PBrace {
  let no = PBrace { ok: false, min: 0, max: 0, end: at, big: -1, order: true };
  let (lo, i, lbig) = __pc_number(p, at + 1);
  if (i == at + 1) {
    return no;
  }
  if (i < p.len() && p[i] == 125u8) {
    return PBrace { ok: true, min: lo, max: lo, end: i + 1, big: lbig, order: true };
  }
  if (i >= p.len() || p[i] != 44u8) {
    return no;
  }
  let (hi, j, hbig) = __pc_number(p, i + 1);
  if (j >= p.len() || p[j] != 125u8) {
    return no;
  }
  let big = if (lbig >= 0) { lbig } else { hbig };
  if (j == i + 1) {
    return PBrace { ok: true, min: lo, max: -1, end: j + 1, big: big, order: true };
  }
  return PBrace { ok: true, min: lo, max: hi, end: j + 1, big: big, order: hi >= lo || big >= 0 };
}

// The decimal number at `at`, the offset after it, and the offset after its
// digits when it is above 65535, or -1.
function __pc_number(p: string, at: i32): (i32, i32, i32) {
  let n = 0;
  let i = at;
  let big = -1;
  while (i < p.len() && p[i] >= 48u8 && p[i] <= 57u8) {
    n = if (n > MAX_REPEAT) { n } else { n * 10 + (p[i] as i32 - 48) };
    i += 1;
  }
  if (n > MAX_REPEAT) {
    big = i;
  }
  return (n, i, big);
}

// A quantifier after the item `a`, if one comes next.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_quant(cx: PCtx, a: PParse, f: i32): PParse {
  let p = cx.p;
  let pos = a.pos;
  if ((f & F_X) != 0) {
    pos = __pc_skip_space(p, pos);
  }
  if (pos >= p.len()) {
    return a;
  }
  let c = p[pos] as i32;
  let min = 0;
  let max = -1;
  let end = pos + 1;
  if (c == 43) {
    min = 1;
  } else if (c == 63) {
    max = 1;
  } else if (c == 123) {
    let b = __pc_brace(p, pos);
    if (!b.ok) {
      return a;
    }
    if (b.big >= 0) {
      __pc_fail(cx, b.big, 105);
      return a;
    }
    if (!b.order) {
      __pc_fail(cx, b.end - 1, 104);
      return a;
    }
    min = b.min;
    max = b.max;
    end = b.end;
  } else if (c != 42) {
    return a;
  }
  if (!__pc_repeatable(a.node)) {
    __pc_fail(cx, end - 1, 109);
    return a;
  }
  let kind = if ((f & F_U) != 0) { 1 } else { 0 };
  if (end < p.len() && p[end] == 63u8) {
    kind = 1 - kind;
    end += 1;
  } else if (end < p.len() && p[end] == 43u8) {
    kind = 2;
    end += 1;
  }
  let node = __pc_repeat_node(a.node, min, max, kind);
  let again = if ((f & F_X) != 0) { __pc_skip_space(p, end) } else { end };
  if (again < p.len() && (p[again] == 42u8 || p[again] == 43u8 || p[again] == 63u8)) {
    __pc_fail(cx, again, 109);
  } else if (again < p.len() && p[again] == 123u8 && __pc_brace(p, again).ok) {
    __pc_fail(cx, __pc_brace(p, again).end - 1, 109);
  }
  return PParse { node: node, pos: end, f: f };
}

// A repetition of an assertion is the assertion, or nothing when it can be
// skipped.
function __pc_repeat_node(x: PNode, min: i32, max: i32, kind: i32): PNode {
  if (__pc_zero_width(x)) {
    return if (min == 0) { NRep(PRepData { node: x, min: 0, max: 1, kind: kind }) } else { x };
  }
  return NRep(PRepData { node: x, min: min, max: max, kind: kind });
}

function __pc_zero_width(x: PNode): boolean {
  return match (x) {
    NLook(_) => true,
    NAssert(_) => true,
    NKeep => true,
    _ => false,
  };
}

function __pc_repeatable(x: PNode): boolean {
  return match (x) {
    NAssert(k) => false,
    NKeep => false,
    NEmpty => false,
    _ => true,
  };
}

// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_skip_space(p: string, at: i32): i32 {
  let i = at;
  while (i < p.len()) {
    let c = p[i] as i32;
    if (c == 32 || c == 9 || c == 10 || c == 11 || c == 12 || c == 13) {
      i += 1;
    } else if (c == 35) {
      while (i < p.len() && p[i] != 10u8) {
        i += 1;
      }
    } else {
      return i;
    }
  }
  return i;
}

// A group at `at`, which holds `(`.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_group(cx: PCtx, at: i32, f: i32, depth: i32): PParse {
  let p = cx.p;
  if (at + 1 < p.len() && p[at + 1] == 42u8) {
    return __pc_verb(cx, at, f);
  }
  if (at + 1 >= p.len() || p[at + 1] != 63u8) {
    if ((f & F_N) != 0) {
      return __pc_body(cx, at + 1, f, depth, -1);
    }
    return __pc_body(cx, at + 1, f, depth, __pc_new_group(cx, ""));
  }
  if (at + 2 >= p.len()) {
    __pc_fail(cx, p.len(), 114);
    return PParse { node: NEmpty, pos: p.len(), f: f };
  }
  let c = p[at + 2] as i32;
  if (c == 58) {
    return __pc_body(cx, at + 3, f, depth, -1);
  }
  if (c == 124) {
    return __pc_reset(cx, at + 3, f, depth);
  }
  if (c == 62) {
    let b = __pc_body(cx, at + 3, f, depth, -1);
    return PParse { ...b, node: NAtomic(PAtomicData { node: b.node }) };
  }
  if (c == 61 || c == 33) {
    return __pc_look(cx, at, at + 3, f, depth, if (c == 61) { LOOK_AHEAD } else { LOOK_NOT_AHEAD });
  }
  if (c == 60 && at + 3 < p.len() && (p[at + 3] == 61u8 || p[at + 3] == 33u8)) {
    return __pc_look(cx, at, at + 4, f, depth, if (p[at + 3] == 61u8) { LOOK_BEHIND } else { LOOK_NOT_BEHIND });
  }
  if (c == 60 || c == 39) {
    return __pc_named(cx, at + 3, f, depth, if (c == 60) { 62 } else { 39 });
  }
  if (c == 80) {
    return __pc_python(cx, at + 3, f, depth);
  }
  if (c == 40) {
    return __pc_cond(cx, at + 3, f, depth);
  }
  if (c == 82 || c == 38 || c >= 48 && c <= 57 || c == 43 || c == 45 && at + 3 < p.len() && p[at + 3] >= 48u8 && p[at + 3] <= 57u8) {
    return __pc_call(cx, at + 2, f);
  }
  if (__pc_is_flag_group(p, at + 2)) {
    let fl = __pc_flags(cx, at + 2, f);
    if (__pc_failed(cx)) {
      return PParse { node: NEmpty, pos: p.len(), f: f };
    }
    let b = __pc_body(cx, fl.1 + 1, fl.0, depth, -1);
    return PParse { ...b, f: f };
  }
  __pc_fail(cx, at + 2, 111);
  return PParse { node: NEmpty, pos: p.len(), f: f };
}

// A call of a group at `at`, after `(?`: R, a number, +n, -n or &name.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_call(cx: PCtx, at: i32, f: i32): PParse {
  let p = cx.p;
  let no = PParse { node: NEmpty, pos: p.len(), f: f };
  let c = p[at] as i32;
  if (c == 82) {
    if (at + 1 >= p.len() || p[at + 1] != 41u8) {
      __pc_fail(cx, at + 1, 158);
      return no;
    }
    return PParse { node: NCall(PCallData { group: 0, name: "", at: at }), pos: at + 2, f: f };
  }
  if (c == 38) {
    let (name, end) = __pc_name(cx, at + 1, 41);
    if (end < 0) {
      return no;
    }
    return PParse { node: NCall(PCallData { group: -1, name: name, at: at + 1 }), pos: end + 1, f: f };
  }
  let sign = if (c == 43 || c == 45) { 1 } else { 0 };
  let (n, end, big) = __pc_number(p, at + sign);
  if (end >= p.len() || p[end] != 41u8) {
    __pc_fail(cx, end, 115);
    return no;
  }
  if (sign == 1 && n == 0) {
    __pc_fail(cx, end, 126);
    return no;
  }
  let group = if (c == 43) { cx.groups.get() + n } else if (c == 45) { cx.groups.get() - n + 1 } else { n };
  return PParse { node: NCall(PCallData { group: if (group <= 0 && n > 0) { -2 } else { group }, name: "", at: end }), pos: end + 1, f: f };
}

// A verb such as (*FAIL) at `at`.
function __pc_verb(cx: PCtx, at: i32, f: i32): PParse {
  let p = cx.p;
  let close = __memchr(p, 41, at);
  let name = if (close < 0) { "" } else { slice_unchecked(p, at + 2, close) + "" };
  if (name == "FAIL" || name == "F") {
    return PParse { node: NFail, pos: close + 1, f: f };
  }
  if (name == "ACCEPT") {
    return PParse { node: NAccept, pos: close + 1, f: f };
  }
  __pc_fail(cx, if (close < 0) { p.len() } else { close }, 160);
  return PParse { node: NEmpty, pos: p.len(), f: f };
}

// The body of a group from `at` and its `)`. A group number of -1 does not
// capture.
function __pc_body(cx: PCtx, at: i32, f: i32, depth: i32, idx: i32): PParse {
  let r = __pc_alt(cx, at, f, depth + 1);
  if (__pc_failed(cx)) {
    return r;
  }
  if (r.pos >= cx.p.len()) {
    __pc_fail(cx, cx.p.len(), 114);
    return r;
  }
  if (idx > 0) {
    cx.bodies.set(cx.bodies.get().append(PGroupData { idx: idx, node: r.node }));
  }
  return PParse { node: NGroup(PGroupData { idx: idx, node: r.node }), pos: r.pos + 1, f: f };
}

// A group of (?|...), where each alternative numbers its groups from the
// same number.
function __pc_reset(cx: PCtx, at: i32, f0: i32, depth: i32): PParse {
  let base = cx.groups.get();
  let top = base;
  let branches: PNode[] = [];
  let pos = at;
  let f = f0;
  while (true) {
    cx.groups.set(base);
    let s = __pc_seq(cx, pos, f, depth + 1);
    branches = branches.append(s.node);
    top = if (cx.groups.get() > top) { cx.groups.get() } else { top };
    pos = s.pos;
    f = s.f;
    if (__pc_failed(cx) || pos >= cx.p.len() || cx.p[pos] != 124u8) {
      break;
    }
    pos += 1;
  }
  cx.groups.set(top);
  if (!__pc_failed(cx) && pos >= cx.p.len()) {
    __pc_fail(cx, pos, 114);
  }
  let node = if (branches.len() == 1) { branches[0] } else { NAlt(branches) };
  return PParse { node: NGroup(PGroupData { idx: -1, node: node }), pos: pos + 1, f: f0 };
}

// A look-around whose group starts at `at`, with its body at `body`.
function __pc_look(cx: PCtx, at: i32, body: i32, f: i32, depth: i32, kind: i32): PParse {
  let b = __pc_body(cx, body, f, depth, -1);
  let NGroup(g) = b.node else {
    return b;
  };
  return PParse { ...b, node: NLook(PLookData { kind: kind, node: g.node, at: at }) };
}

// A new capture group, with a name or "".
function __pc_new_group(cx: PCtx, name: string): i32 {
  let n = cx.groups.get() + 1;
  cx.groups.set(n);
  if (name != "") {
    cx.names.set(cx.names.get().append(name));
    cx.name_groups.set(cx.name_groups.get().append(n));
  }
  return n;
}

// The name of a group at `at`, which ends with `stop`: the name, and the
// offset of the terminator, or -1 after an error.
function __pc_name(cx: PCtx, at: i32, stop: i32): (string, i32) {
  let p = cx.p;
  if (at < p.len() && p[at] >= 48u8 && p[at] <= 57u8) {
    __pc_fail(cx, at, 144);
    return ("", -1);
  }
  let i = at;
  while (i < p.len() && __pc_name_byte(p[i] as i32)) {
    i += 1;
  }
  if (i == at) {
    __pc_fail(cx, i, 162);
    return ("", -1);
  }
  if (i - at > 32) {
    __pc_fail(cx, i, 148);
    return ("", -1);
  }
  if (i >= p.len() || p[i] as i32 != stop) {
    __pc_fail(cx, i, 142);
    return ("", -1);
  }
  return (slice_unchecked(p, at, i) + "", i);
}

function __pc_name_byte(c: i32): boolean {
  return c >= 48 && c <= 57 || (c | 32) >= 97 && (c | 32) <= 122 || c == 95;
}

// A named group (?<name>...) or (?'name'...), with the name at `at`.
function __pc_named(cx: PCtx, at: i32, f: i32, depth: i32, stop: i32): PParse {
  let (name, end) = __pc_name(cx, at, stop);
  if (end < 0) {
    return PParse { node: NEmpty, pos: cx.p.len(), f: f };
  }
  for n in cx.names.get() {
    if (n == name) {
      __pc_fail(cx, end + 1, 143);
      return PParse { node: NEmpty, pos: cx.p.len(), f: f };
    }
  }
  return __pc_body(cx, end + 1, f, depth, __pc_new_group(cx, name));
}

// (?P<name>...) and (?P=name), after `(?P`.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_python(cx: PCtx, at: i32, f: i32, depth: i32): PParse {
  let p = cx.p;
  if (at < p.len() && p[at] == 60u8) {
    return __pc_named(cx, at + 1, f, depth, 62);
  }
  if (at < p.len() && p[at] == 62u8) {
    let (name, end) = __pc_name(cx, at + 1, 41);
    if (end < 0) {
      return PParse { node: NEmpty, pos: p.len(), f: f };
    }
    return PParse { node: NCall(PCallData { group: -1, name: name, at: at + 1 }), pos: end + 1, f: f };
  }
  if (at < p.len() && p[at] == 61u8) {
    let (name, end) = __pc_name(cx, at + 1, 41);
    if (end < 0) {
      return PParse { node: NEmpty, pos: p.len(), f: f };
    }
    return PParse { node: NBack(PBackData { group: -1, name: name, caseless: (f & F_I) != 0, at: at + 1 }), pos: end + 1, f: f };
  }
  __pc_fail(cx, if (at < p.len()) { at } else { p.len() }, if (at < p.len()) { 141 } else { 114 });
  return PParse { node: NEmpty, pos: p.len(), f: f };
}

// A conditional group, after `(?(`.
function __pc_cond(cx: PCtx, at: i32, f: i32, depth: i32): PParse {
  let p = cx.p;
  let group = -1;
  let name = "";
  let look: Option[PLookData] = None;
  let pos = at;
  if (at < p.len() && p[at] == 63u8) {
    let l = __pc_group(cx, at - 1, f, depth);
    if (__pc_failed(cx)) {
      return l;
    }
    let NLook(ld) = l.node else {
      __pc_fail(cx, at + 1, 128);
      return PParse { node: NEmpty, pos: p.len(), f: f };
    };
    look = Some(ld);
    pos = l.pos;
  } else {
    let (g, nm, end) = __pc_cond_ref(cx, at);
    if (end < 0) {
      return PParse { node: NEmpty, pos: p.len(), f: f };
    }
    group = g;
    name = nm;
    pos = end;
  }
  let r = __pc_alt(cx, pos, f, depth + 1);
  if (__pc_failed(cx)) {
    return r;
  }
  if (r.pos >= p.len()) {
    __pc_fail(cx, p.len(), 114);
    return r;
  }
  let yes = r.node;
  let no = NEmpty;
  if let NAlt(bs) = r.node {
    if (bs.len() > 2) {
      __pc_fail(cx, r.pos, 127);
      return r;
    }
    yes = bs[0];
    no = bs[1];
  }
  return PParse { node: NCond(PCondData { group: group, name: name, look: look, yes: yes, no: no, at: at }), pos: r.pos + 1, f: f };
}

// The reference of a condition at `at`: a number, <name>, 'name' or a bare
// name, and the offset after its `)`.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_cond_ref(cx: PCtx, at: i32): (i32, string, i32) {
  let p = cx.p;
  if (at < p.len() && (p[at] == 60u8 || p[at] == 39u8)) {
    let (name, end) = __pc_name(cx, at + 1, if (p[at] == 60u8) { 62 } else { 39 });
    if (end < 0) {
      return (-1, "", -1);
    }
    if (end + 1 >= p.len() || p[end + 1] != 41u8) {
      __pc_fail(cx, end + 1, 124);
      return (-1, "", -1);
    }
    return (-1, name, end + 2);
  }
  if (at + 1 < p.len() && p[at] == 82u8 && p[at + 1] == 41u8) {
    return (COND_RECURSE, "", at + 2);
  }
  if (at + 6 < p.len() && slice_unchecked(p, at, at + 7) == "DEFINE)") {
    return (COND_DEFINE, "", at + 7);
  }
  let (n, i, big) = __pc_number(p, at);
  if (i > at) {
    if (i >= p.len() || p[i] != 41u8) {
      __pc_fail(cx, i, 124);
      return (-1, "", -1);
    }
    if (n == 0 || n > cx.total) {
      __pc_fail(cx, at - 1, 115);
      return (-1, "", -1);
    }
    return (n, "", i + 1);
  }
  let (name, end) = __pc_name(cx, at, 41);
  if (end < 0) {
    return (-1, "", -1);
  }
  return (-1, name, end + 1);
}
```

### Escapes and classes

```fern
<<classes>>=
// An escape at `at`, which holds `\`.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_escape(cx: PCtx, at: i32, f: i32): PParse {
  let p = cx.p;
  if (at + 1 >= p.len()) {
    __pc_fail(cx, p.len(), 101);
    return PParse { node: NEmpty, pos: p.len(), f: f };
  }
  let c = p[at + 1] as i32;
  let next = at + 2;
  let assert = match (c) {
    98 => A_WORD,
    66 => A_NOT_WORD,
    65 => A_START,
    122 => A_END,
    90 => A_END_NL,
    71 => A_G,
    _ => 0,
  };
  if (assert > 0) {
    return PParse { node: NAssert(assert), pos: next, f: f };
  }
  if (c == 75) {
    return PParse { node: NKeep, pos: next, f: f };
  }
  if (c == 78 && (next >= p.len() || p[next] != 123u8)) {
    return PParse { node: NAny(false), pos: next, f: f };
  }
  if (c == 78 && next + 2 < p.len() && p[next + 1] == 85u8 && p[next + 2] == 43u8) {
    if (!cx.utf) {
      __pc_fail(cx, next, 193);
      return PParse { node: NEmpty, pos: p.len(), f: f };
    }
    let it = __pc_braced_number(cx, at, 16);
    if (__pc_failed(cx)) {
      return PParse { node: NEmpty, pos: p.len(), f: f };
    }
    return PParse { node: __pc_char_node(cx, it.cp, f), pos: it.end, f: f };
  }
  if (c == 82) {
    return PParse { node: __pc_newline_seq(cx), pos: next, f: f };
  }
  if (c == 88) {
    return PParse { node: __pc_grapheme(cx), pos: next, f: f };
  }
  if (c == 103 || c == 107 || c >= 49 && c <= 57 && __pc_is_backref(cx, at + 1)) {
    return __pc_backref(cx, at, f);
  }
  let it = __pc_escape_item(cx, at, f, false);
  if (__pc_failed(cx)) {
    return PParse { node: NEmpty, pos: p.len(), f: f };
  }
  if (it.set >= 0) {
    return PParse { node: NSet(it.set), pos: it.end, f: f };
  }
  return PParse { node: __pc_char_node(cx, it.cp, f), pos: it.end, f: f };
}

// Whether the digits after `\` at `at` are a back reference: a number below
// 10, a number that starts with 8 or 9, or a number of a group before.
function __pc_is_backref(cx: PCtx, at: i32): boolean {
  let (n, end, big) = __pc_number(cx.p, at);
  return n < 10 || cx.p[at] >= 56u8 || n <= cx.total;
}

// A back reference at `at`: \1, \g1, \g{1}, \g{-1}, \g{name}, \k<name>,
// \k'name' or \k{name}.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_backref(cx: PCtx, at: i32, f: i32): PParse {
  let p = cx.p;
  let ci = (f & F_I) != 0;
  let c = p[at + 1] as i32;
  let no = PParse { node: NEmpty, pos: p.len(), f: f };
  if (c >= 49 && c <= 57) {
    let (n, end, big) = __pc_number(p, at + 1);
    return PParse { node: NBack(PBackData { group: n, name: "", caseless: ci, at: at + 1 }), pos: end, f: f };
  }
  let i = at + 2;
  if (c == 107) {
    if (i >= p.len() || p[i] != 60u8 && p[i] != 39u8 && p[i] != 123u8) {
      __pc_fail(cx, i, 169);
      return no;
    }
    let stop = if (p[i] == 60u8) { 62 } else if (p[i] == 39u8) { 39 } else { 125 };
    let (name, end) = __pc_name(cx, i + 1, stop);
    if (end < 0) {
      return no;
    }
    return PParse { node: NBack(PBackData { group: -1, name: name, caseless: ci, at: i + 1 }), pos: end + 1, f: f };
  }
  let braced = i < p.len() && p[i] == 123u8;
  let j = if (braced) { i + 1 } else { i };
  let neg = j < p.len() && p[j] == 45u8;
  let plus = j < p.len() && p[j] == 43u8;
  let (n, end, big) = __pc_number(p, if (neg || plus) { j + 1 } else { j });
  if (end > j + (if (neg || plus) { 1 } else { 0 })) {
    if (braced && (end >= p.len() || p[end] != 125u8)) {
      __pc_fail(cx, end, 157);
      return no;
    }
    let after = if (braced) { end + 1 } else { end };
    if (n == 0) {
      __pc_fail(cx, after, if (neg) { 126 } else { 115 });
      return no;
    }
    let group = if (neg) { cx.groups.get() - n + 1 } else { n };
    if (neg && group <= 0) {
      __pc_fail(cx, after, 115);
      return no;
    }
    return PParse { node: NBack(PBackData { group: group, name: "", caseless: ci, at: after }), pos: after, f: f };
  }
  if (braced) {
    let (name, nend) = __pc_name(cx, j, 125);
    if (nend < 0) {
      return no;
    }
    return PParse { node: NBack(PBackData { group: -1, name: name, caseless: ci, at: j }), pos: nend + 1, f: f };
  }
  if (i < p.len() && (p[i] == 60u8 || p[i] == 39u8)) {
    return __pc_g_call(cx, i, f);
  }
  __pc_fail(cx, i, 157);
  return no;
}

// \g<n>, \g'n', \g<+n>, \g<-n> or \g<name> at `at`, which holds the
// bracket: a call of a group.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_g_call(cx: PCtx, at: i32, f: i32): PParse {
  let p = cx.p;
  let no = PParse { node: NEmpty, pos: p.len(), f: f };
  let stop = if (p[at] == 60u8) { 62 } else { 39 };
  let j = at + 1;
  let sign = if (j < p.len() && (p[j] == 43u8 || p[j] == 45u8)) { 1 } else { 0 };
  let (n, end, big) = __pc_number(p, j + sign);
  if (end > j + sign) {
    if (end >= p.len() || p[end] as i32 != stop) {
      __pc_fail(cx, end, 157);
      return no;
    }
    let c = if (sign == 1) { p[j] as i32 } else { 0 };
    let group = if (c == 43) { cx.groups.get() + n } else if (c == 45) { cx.groups.get() - n + 1 } else { n };
    return PParse { node: NCall(PCallData { group: if (group <= 0 && n > 0) { -2 } else { group }, name: "", at: end }), pos: end + 1, f: f };
  }
  let (name, nend) = __pc_name(cx, j, stop);
  if (nend < 0) {
    return no;
  }
  return PParse { node: NCall(PCallData { group: -1, name: name, at: j }), pos: nend + 1, f: f };
}

// \R: a newline sequence, \r\n first.
function __pc_newline_seq(cx: PCtx): PNode {
  let vs = __pc_add_set(cx, __pc_set_of(cx, [10, 13, 133, 133, 8232, 8233], false, false));
  return NAtomic(PAtomicData { node: NAlt([NSeq([NChar(13), NChar(10)]), NSet(vs)]) });
}

// \X: a character that is not a mark, and the marks after it.
function __pc_grapheme(cx: PCtx): PNode {
  let marks = match (unicode.property_ranges("M")) { Some(rs) => rs, None => [] };
  let m = __pc_add_set(cx, __pc_set_of(cx, marks, false, false));
  let rest = NRep(PRepData { node: NSet(m), min: 0, max: -1, kind: 2 });
  return NAtomic(PAtomicData { node: NSeq([NAny(true), rest]) });
}

// The result of an escape: a character, or a set, and the offset after it.
struct PItem { cp: i32, set: i32, end: i32 }

// An escape that gives a character or a class, outside or inside a class.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_escape_item(cx: PCtx, at: i32, f: i32, in_class: boolean): PItem {
  let p = cx.p;
  let c = p[at + 1] as i32;
  let next = at + 2;
  let simple = match (c) {
    97 => 7,
    101 => 27,
    102 => 12,
    110 => 10,
    114 => 13,
    116 => 9,
    _ => -1,
  };
  if (simple >= 0) {
    return PItem { cp: simple, set: -1, end: next };
  }
  if (c == 98 && in_class) {
    return PItem { cp: 8, set: -1, end: next };
  }
  if (c == 48) {
    return __pc_octal(p, at + 1, 3);
  }
  if (c >= 49 && c <= 57) {
    if (c >= 56) {
      return PItem { cp: c, set: -1, end: next };
    }
    return __pc_octal(p, at + 1, 3);
  }
  if (c == 111) {
    return __pc_braced_number(cx, at, 8);
  }
  if (c == 120) {
    if (next < p.len() && p[next] == 123u8) {
      return __pc_braced_number(cx, at, 16);
    }
    return __pc_hex2(p, next);
  }
  if (c == 99) {
    if (next >= p.len()) {
      __pc_fail(cx, next, 102);
      return PItem { cp: 0, set: -1, end: next };
    }
    let x = p[next] as i32;
    if (x < 32 || x > 126) {
      __pc_fail(cx, next, 168);
      return PItem { cp: 0, set: -1, end: next };
    }
    let up = if (x >= 97 && x <= 122) { x - 32 } else { x };
    return PItem { cp: up ^ 64, set: -1, end: next + 1 };
  }
  if (c == 112 || c == 80) {
    return __pc_property(cx, at, f, c == 80);
  }
  let named = __pc_named_class(c);
  if (named != "") {
    return PItem { cp: -1, set: __pc_add_set(cx, __pc_class_set(cx, named, (f & F_I) != 0)), end: next };
  }
  if (c == 78 && in_class) {
    __pc_fail(cx, next, 171);
    return PItem { cp: 0, set: -1, end: next };
  }
  if (c == 76 || c == 108 || c == 85 || c == 117 || c == 78) {
    __pc_fail(cx, next, 137);
    return PItem { cp: 0, set: -1, end: next };
  }
  if (c >= 48 && c <= 57 || (c | 32) >= 97 && (c | 32) <= 122) {
    __pc_fail(cx, at + 1, 103);
    return PItem { cp: 0, set: -1, end: next };
  }
  let (cp, n) = __pc_pat_char(cx, at + 1);
  return PItem { cp: cp, set: -1, end: at + 1 + n };
}

// The letter of a class escape: \d, \D, \s, \S, \w, \W, \h, \H, \v and \V.
// One arm for each name.
// fern-lint: allow cyclomatic-complexity
function __pc_named_class(c: i32): string {
  return match (c) {
    100 => "d",
    68 => "D",
    115 => "s",
    83 => "S",
    119 => "w",
    87 => "W",
    104 => "h",
    72 => "H",
    118 => "v",
    86 => "V",
    _ => "",
  };
}

// Up to `max` octal digits at `at`.
function __pc_octal(p: string, at: i32, max: i32): PItem {
  let n = 0;
  let i = at;
  while (i < p.len() && i < at + max && p[i] >= 48u8 && p[i] <= 55u8) {
    n = n * 8 + (p[i] as i32 - 48);
    i += 1;
  }
  return PItem { cp: n, set: -1, end: i };
}

// Up to two hex digits at `at`.
function __pc_hex2(p: string, at: i32): PItem {
  let n = 0;
  let i = at;
  while (i < p.len() && i < at + 2 && __pc_hex(p[i] as i32) >= 0) {
    n = n * 16 + __pc_hex(p[i] as i32);
    i += 1;
  }
  return PItem { cp: n, set: -1, end: i };
}

function __pc_hex(c: i32): i32 {
  if (c >= 48 && c <= 57) {
    return c - 48;
  }
  if ((c | 32) >= 97 && (c | 32) <= 102) {
    return (c | 32) - 87;
  }
  return -1;
}

// \x{...} or \o{...} at `at`, in base `base`.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_braced_number(cx: PCtx, at: i32, base: i32): PItem {
  let p = cx.p;
  let open = at + 2;
  if (open >= p.len() || p[open] != 123u8) {
    __pc_fail(cx, at + 1, 155);
    return PItem { cp: 0, set: -1, end: open };
  }
  let i = if (p[at + 1] == 78u8) { open + 3 } else { open + 1 };
  let n = 0;
  let max = if (cx.utf) { 1114111 } else { 255 };
  while (i < p.len() && p[i] != 125u8) {
    let d = if (base == 16) { __pc_hex(p[i] as i32) } else if (p[i] >= 48u8 && p[i] <= 55u8) { p[i] as i32 - 48 } else { -1 };
    if (d < 0) {
      __pc_fail(cx, i, if (base == 16) { 167 } else { 164 });
      return PItem { cp: 0, set: -1, end: i };
    }
    n = if (n > max) { n } else { n * base + d };
    i += 1;
  }
  if (i >= p.len()) {
    __pc_fail(cx, i, if (base == 16) { 167 } else { 164 });
    return PItem { cp: 0, set: -1, end: i };
  }
  if (i == open + 1 || p[at + 1] == 78u8 && i == open + 3) {
    __pc_fail(cx, i, 178);
    return PItem { cp: 0, set: -1, end: i };
  }
  if (n > max) {
    __pc_fail(cx, i, 134);
    return PItem { cp: 0, set: -1, end: i };
  }
  if (cx.utf && n >= 55296 && n <= 57343) {
    __pc_fail(cx, i, 173);
    return PItem { cp: 0, set: -1, end: i };
  }
  return PItem { cp: n, set: -1, end: i + 1 };
}

// \p{...}, \P{...}, \pL or \PL at `at`.
function __pc_property(cx: PCtx, at: i32, f: i32, negated: boolean): PItem {
  let p = cx.p;
  let i = at + 2;
  if (i >= p.len()) {
    __pc_fail(cx, i, 146);
    return PItem { cp: 0, set: -1, end: i };
  }
  let name = "";
  let end = i + 1;
  let neg = negated;
  if (p[i] == 123u8) {
    let close = __memchr(p, 125, i);
    if (close < 0) {
      __pc_fail(cx, p.len(), 146);
      return PItem { cp: 0, set: -1, end: p.len() };
    }
    let from = i + 1;
    if (from < close && p[from] == 94u8) {
      neg = !neg;
      from += 1;
    }
    name = slice_unchecked(p, from, close) + "";
    end = close + 1;
  } else {
    name = slice_unchecked(p, i, i + 1) + "";
  }
  let ranges = __pc_property_ranges(name);
  if (ranges.len() == 1) {
    __pc_fail(cx, end, 147);
    return PItem { cp: 0, set: -1, end: end };
  }
  return PItem { cp: -1, set: __pc_add_set(cx, __pc_set_of(cx, ranges, neg, false)), end: end };
}

// The ranges of a property of PCRE2, or [-1] for a name that it does not
// know. L& is a cased letter, and Xan, Xps, Xsp and Xwd are the classes of
// PCRE2.
// One arm for each name.
// fern-lint: allow cyclomatic-complexity
function __pc_property_ranges(name: string): i32[] {
  if (name == "L&" || name == "Lc" || name == "LC") {
    return __pc_union(__pc_prop("Lu"), __pc_union(__pc_prop("Ll"), __pc_prop("Lt")));
  }
  if (name == "Xan") {
    return __pc_union(__pc_prop("L"), __pc_prop("N"));
  }
  if (name == "Xwd") {
    return __pc_union(__pc_union(__pc_prop("L"), __pc_prop("N")), [95, 95]);
  }
  if (name == "Xps" || name == "Xsp") {
    return __pc_union(__pc_prop("Z"), [9, 13]);
  }
  if (name == "Any") {
    return [0, 1114111];
  }
  match (unicode.property_ranges(name)) {
    Some(rs) => {
      return rs;
    },
    None => {
      return [-1];
    }
  }
  return [-1];
}

function __pc_prop(name: string): i32[] {
  return match (unicode.property_ranges(name)) { Some(rs) => rs, None => [] };
}

// The set of a class escape: \d, \s, \w, \h, \v and their complements.
// One arm for each name.
// fern-lint: allow cyclomatic-complexity
function __pc_class_set(cx: PCtx, name: string, caseless: boolean): PSet {
  let neg = name == "D" || name == "S" || name == "W" || name == "H" || name == "V";
  let base = match (name) {
    "d" | "D" => if (cx.utf) { __pc_prop("Nd") } else { [48, 57] },
    "s" | "S" => if (cx.utf) { __pc_property_ranges("Xsp") } else { [9, 13, 32, 32] },
    "w" | "W" => if (cx.utf) { __pc_property_ranges("Xwd") } else { [48, 57, 65, 90, 95, 95, 97, 122] },
    "h" | "H" => [9, 9, 32, 32, 160, 160, 5760, 5760, 6158, 6158, 8192, 8202, 8239, 8239, 8287, 8287, 12288, 12288],
    _ => [10, 13, 133, 133, 8232, 8233],
  };
  return __pc_set_of(cx, base, neg, false);
}

// A class at `at`, which holds `[`.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_class(cx: PCtx, at: i32, f: i32): PParse {
  let p = cx.p;
  let i = at + 1;
  let neg = false;
  if (i < p.len() && p[i] == 94u8) {
    neg = true;
    i += 1;
  }
  let ranges: i32[] = [];
  let subs: PSet[] = [];
  let first = true;
  let ci = (f & F_I) != 0;
  while (true) {
    if (i >= p.len()) {
      __pc_fail(cx, p.len(), 106);
      return PParse { node: NEmpty, pos: p.len(), f: f };
    }
    let c = p[i] as i32;
    if (c == 93 && !first) {
      i += 1;
      break;
    }
    first = false;
    if ((f & F_XX) != 0 && (c == 32 || c == 9)) {
      i += 1;
      continue;
    }
    if (c == 91 && i + 1 < p.len() && (p[i + 1] == 58u8 || p[i + 1] == 46u8 || p[i + 1] == 61u8)) {
      let r = __pc_posix(cx, i);
      if (__pc_failed(cx)) {
        return PParse { node: NEmpty, pos: p.len(), f: f };
      }
      if (r.set >= 0) {
        subs = subs.append(cx.sets.get()[r.set]);
        i = r.end;
        continue;
      }
    }
    if (c == 92 && i + 1 < p.len() && p[i + 1] == 81u8) {
      let close = __pc_find_e(p, i + 2);
      let k = i + 2;
      while (k < close) {
        let (cp, n) = __pc_pat_char(cx, k);
        ranges = ranges.append(cp).append(cp);
        k += n;
      }
      i = if (close < p.len()) { close + 2 } else { close };
      continue;
    }
    if (c == 92 && i + 1 < p.len() && p[i + 1] == 69u8) {
      i += 2;
      continue;
    }
    let lo = __pc_class_char(cx, i, f);
    if (__pc_failed(cx)) {
      return PParse { node: NEmpty, pos: p.len(), f: f };
    }
    if (lo.set >= 0) {
      subs = subs.append(cx.sets.get()[lo.set]);
      i = lo.end;
      if (i + 1 < p.len() && p[i] == 45u8 && p[i + 1] != 93u8) {
        __pc_fail(cx, i, 150);
        return PParse { node: NEmpty, pos: p.len(), f: f };
      }
      continue;
    }
    i = lo.end;
    if (i + 1 < p.len() && p[i] == 45u8 && p[i + 1] != 93u8) {
      let hi = __pc_class_char(cx, i + 1, f);
      if (__pc_failed(cx)) {
        return PParse { node: NEmpty, pos: p.len(), f: f };
      }
      if (hi.set >= 0) {
        __pc_fail(cx, i, 150);
        return PParse { node: NEmpty, pos: p.len(), f: f };
      }
      if (hi.cp < lo.cp) {
        __pc_fail(cx, i + 1, 108);
        return PParse { node: NEmpty, pos: p.len(), f: f };
      }
      ranges = ranges.append(lo.cp).append(hi.cp);
      i = hi.end;
      continue;
    }
    ranges = ranges.append(lo.cp).append(lo.cp);
  }
  let rs = if (ci) { __pc_fold_ranges(__pc_normal(ranges)) } else { __pc_normal(ranges) };
  let set = __pc_set_of(cx, rs, false, false);
  for s in subs {
    set = __pc_set_union(set, s);
  }
  if (neg) {
    set = __pc_set_not(set);
  }
  return PParse { node: NSet(__pc_add_set(cx, set)), pos: i, f: f };
}

function __pc_find_e(p: string, at: i32): i32 {
  let i = at;
  while (i + 1 < p.len()) {
    if (p[i] == 92u8 && p[i + 1] == 69u8) {
      return i;
    }
    i += 1;
  }
  return p.len();
}

// One character of a class at `at`, or the set of a class escape.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_class_char(cx: PCtx, at: i32, f: i32): PItem {
  let p = cx.p;
  if (p[at] == 92u8) {
    if (at + 1 >= p.len()) {
      __pc_fail(cx, p.len(), 101);
      return PItem { cp: 0, set: -1, end: p.len() };
    }
    let c = p[at + 1] as i32;
    if (c == 82 || c == 88 || c == 66 || c == 65 || c == 90 || c == 122 || c == 71 || c == 75 || c == 103 || c == 107) {
      __pc_fail(cx, at + 1, 107);
      return PItem { cp: 0, set: -1, end: at + 2 };
    }
    return __pc_escape_item(cx, at, f, true);
  }
  let (cp, n) = __pc_pat_char(cx, at);
  return PItem { cp: cp, set: -1, end: at + n };
}

// A POSIX class such as [:alpha:] at `at`, or a set of -1 when the bracket
// does not start one.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_posix(cx: PCtx, at: i32): PItem {
  let p = cx.p;
  let kind = p[at + 1];
  let i = at + 2;
  while (i + 1 < p.len() && !(p[i] == kind && p[i + 1] == 93u8)) {
    if (p[i] == 93u8 || p[i] == 92u8) {
      return PItem { cp: 0, set: -1, end: at };
    }
    i += 1;
  }
  if (i + 1 >= p.len()) {
    return PItem { cp: 0, set: -1, end: at };
  }
  if (kind != 58u8) {
    __pc_fail(cx, at, 113);
    return PItem { cp: 0, set: -1, end: i + 2 };
  }
  let from = at + 2;
  let neg = from < i && p[from] == 94u8;
  let name = slice_unchecked(p, if (neg) { from + 1 } else { from }, i) + "";
  let rs = __pc_posix_ranges(cx, name);
  if (rs.len() == 1) {
    __pc_fail(cx, from, 130);
    return PItem { cp: 0, set: -1, end: i + 2 };
  }
  return PItem { cp: -1, set: __pc_add_set(cx, __pc_set_of(cx, rs, neg, false)), end: i + 2 };
}

// One arm for each name.
// fern-lint: allow cyclomatic-complexity
function __pc_posix_ranges(cx: PCtx, name: string): i32[] {
  if (cx.utf) {
    let u = match (name) {
      "alpha" => "L",
      "lower" => "Ll",
      "upper" => "Lu",
      "digit" => "Nd",
      "alnum" => "Xan",
      "space" => "Xps",
      "word" => "Xwd",
      "cntrl" => "Cc",
      _ => "",
    };
    if (u != "") {
      return __pc_property_ranges(u);
    }
  }
  return match (name) {
    "alpha" => [65, 90, 97, 122],
    "lower" => [97, 122],
    "upper" => [65, 90],
    "digit" => [48, 57],
    "alnum" => [48, 57, 65, 90, 97, 122],
    "space" => [9, 13, 32, 32],
    "word" => [48, 57, 65, 90, 95, 95, 97, 122],
    "blank" => [9, 9, 32, 32],
    "cntrl" => [0, 31, 127, 127],
    "graph" => [33, 126],
    "print" => [32, 126],
    "punct" => [33, 47, 58, 64, 91, 96, 123, 126],
    "xdigit" => [48, 57, 65, 70, 97, 102],
    "ascii" => [0, 127],
    _ => [-1],
  };
}

// Ranges in order, with no two that touch.
function __pc_normal(rs: i32[]): i32[] {
  let n = rs.len() / 2;
  let lo: i32[] = [];
  let hi: i32[] = [];
  for k in 0..n {
    let a = rs[2 * k];
    let b = rs[2 * k + 1];
    let j = lo.len();
    lo = lo.append(a);
    hi = hi.append(b);
    while (j > 0 && lo[j - 1] > a) {
      lo = lo.with(j, lo[j - 1]).with(j - 1, a);
      hi = hi.with(j, hi[j - 1]).with(j - 1, b);
      j -= 1;
    }
  }
  let out: i32[] = [];
  for k in 0..lo.len() {
    let m = out.len();
    if (m > 0 && lo[k] <= out[m - 1] + 1) {
      if (hi[k] > out[m - 1]) {
        out = out.with(m - 1, hi[k]);
      }
    } else {
      out = out.append(lo[k]).append(hi[k]);
    }
  }
  return out;
}

function __pc_union(a: i32[], b: i32[]): i32[] {
  return __pc_normal(a.concat(b));
}

// The ranges with the characters that simple case folding makes equal to
// their characters.
function __pc_fold_ranges(rs: i32[]): i32[] {
  let out = rs;
  for k in 0..rs.len() / 2 {
    let lo = rs[2 * k];
    let hi = rs[2 * k + 1];
    if (lo > 125251) {
      continue;
    }
    for c in unicode.fold_equivalents(lo as char, (if (hi > 125251) { 125251 } else { hi }) as char) {
      out = out.append(c as i32).append(c as i32);
    }
  }
  return __pc_normal(out);
}

// The set of the ranges `rs`, with `neg` for the complement. With `fold`,
// the ranges also get the other cases of their characters.
function __pc_set_of(cx: PCtx, rs0: i32[], neg: boolean, fold: boolean): PSet {
  let rs = if (fold) { if (cx.utf) { __pc_fold_ranges(rs0) } else { __pc_ascii_fold(rs0) } } else { __pc_normal(rs0) };
  let bits: i32[] = [0, 0, 0, 0, 0, 0, 0, 0];
  let high: i32[] = [];
  for k in 0..rs.len() / 2 {
    let lo = rs[2 * k];
    let hi = rs[2 * k + 1];
    let c = lo;
    while (c <= hi && c < 256) {
      bits = bits.with(c >> 5, bits[c >> 5] | 1 << (c & 31));
      c += 1;
    }
    if (hi >= 256) {
      high = high.append(if (lo < 256) { 256 } else { lo }).append(hi);
    }
  }
  if (neg) {
    for w in 0..8 {
      bits = bits.with(w, ~bits[w]);
    }
  }
  return PSet { bits: bits, ranges: high, neg: neg };
}

function __pc_ascii_fold(rs: i32[]): i32[] {
  let out = rs;
  for k in 0..rs.len() / 2 {
    for c in rs[2 * k]..rs[2 * k + 1] + 1 {
      if (c >= 65 && c <= 90 || c >= 97 && c <= 122) {
        out = out.append(c ^ 32).append(c ^ 32);
      }
      if (c > 255) {
        break;
      }
    }
  }
  return __pc_normal(out);
}

function __pc_set_union(a: PSet, b: PSet): PSet {
  let bits: i32[] = [];
  for w in 0..8 {
    bits = bits.append(a.bits[w] | b.bits[w]);
  }
  if (!a.neg && !b.neg) {
    return PSet { bits: bits, ranges: __pc_normal(a.ranges.concat(b.ranges)), neg: false };
  }
  let ar = __pc_set_high(a);
  let br = __pc_set_high(b);
  return PSet { bits: bits, ranges: __pc_normal(ar.concat(br)), neg: false };
}

// The ranges above 255 of a set, with its complement made explicit.
function __pc_set_high(s: PSet): i32[] {
  if (!s.neg) {
    return s.ranges;
  }
  let out: i32[] = [];
  let next = 256;
  for k in 0..s.ranges.len() / 2 {
    if (s.ranges[2 * k] > next) {
      out = out.append(next).append(s.ranges[2 * k] - 1);
    }
    next = s.ranges[2 * k + 1] + 1;
  }
  if (next <= 1114111) {
    out = out.append(next).append(1114111);
  }
  return out;
}

function __pc_set_not(s: PSet): PSet {
  let bits: i32[] = [];
  for w in 0..8 {
    bits = bits.append(~s.bits[w]);
  }
  return PSet { bits: bits, ranges: s.ranges, neg: !s.neg };
}

function __pc_add_set(cx: PCtx, s: PSet): i32 {
  let all = cx.sets.get();
  cx.sets.set(all.append(s));
  return all.len();
}
```

### Compiler

The check after the parse finds the errors that need the whole pattern: a reference to a group that does not exist, and a look-behind that has no fixed length. The compiler then writes the program.

```fern
<<compiler>>=
// One arm for each kind of node.
// fern-lint: allow cyclomatic-complexity
function __pc_check(cx: PCtx, x: PNode, in_look: boolean): void {
  match (x) {
    NSeq(items) => {
      for y in items {
        __pc_check(cx, y, in_look);
      }
    },
    NAlt(items) => {
      for y in items {
        __pc_check(cx, y, in_look);
      }
    },
    NGroup(g) => __pc_check(cx, g.node, in_look),
    NRep(r) => __pc_check(cx, r.node, in_look),
    NAtomic(a) => __pc_check(cx, a.node, in_look),
    NLook(l) => {
      __pc_check(cx, l.node, true);
      if (l.kind == LOOK_BEHIND || l.kind == LOOK_NOT_BEHIND) {
        __pc_check_behind(cx, l);
      }
    },
    NBack(b) => {
      if (__pc_ref_group(cx, b.group, b.name) < 0) {
        __pc_fail(cx, b.at, 115);
      }
    },
    NCond(c) => {
      if (c.look.is_none() && c.group != COND_RECURSE && c.group != COND_DEFINE && __pc_ref_group(cx, c.group, c.name) < 0) {
        __pc_fail(cx, c.at - 1, 115);
      }
      if let Some(l) = c.look {
        __pc_check(cx, NLook(l), in_look);
      }
      __pc_check(cx, c.yes, in_look);
      __pc_check(cx, c.no, in_look);
    },
    NKeep => {
      if (in_look) {
        __pc_fail(cx, cx.p.len(), 199);
      }
    },
    NCall(c) => {
      if (c.group != 0 && __pc_ref_group(cx, c.group, c.name) < 0) {
        __pc_fail(cx, c.at, 115);
      }
    },
    _ => {},
  }
}

// The number of the group of a reference, or -1.
function __pc_ref_group(cx: PCtx, group: i32, name: string): i32 {
  if (name == "") {
    return if (group >= 1 && group <= cx.groups.get()) { group } else { -1 };
  }
  let ns = cx.names.get();
  for k in 0..ns.len() {
    if (ns[k] == name) {
      return cx.name_groups.get()[k];
    }
  }
  return -1;
}

// Each alternative of a look-behind has a fixed length.
function __pc_check_behind(cx: PCtx, l: PLookData): void {
  let branches = match (l.node) {
    NAlt(items) => items,
    _ => [l.node],
  };
  for b in branches {
    if (__pc_length(cx, b) < 0) {
      __pc_fail(cx, l.at, 125);
      return;
    }
  }
}

// The fixed length of each group in characters, or -1, by number.
function __pc_group_lengths(cx: PCtx): i32[] {
  let out: i32[] = [];
  for k in 0..cx.groups.get() + 1 {
    out = out.append(-1);
  }
  for b in cx.bodies.get() {
    out = out.with(b.idx, __pc_length(cx, b.node));
  }
  return out;
}

// The fixed length of a node in characters, or -1.
// One arm for each kind of node.
// fern-lint: allow cyclomatic-complexity
function __pc_length(cx: PCtx, x: PNode): i32 {
  match (x) {
    NChar(_) => {
      return 1;
    },
    NSet(_) => {
      return 1;
    },
    NAny(_) => {
      return 1;
    },
    NSeq(items) => {
      let n = 0;
      for y in items {
        let k = __pc_length(cx, y);
        if (k < 0) {
          return -1;
        }
        n += k;
      }
      return n;
    },
    NAlt(items) => {
      let n = __pc_length(cx, items[0]);
      for y in items {
        if (__pc_length(cx, y) != n) {
          return -1;
        }
      }
      return n;
    },
    NGroup(g) => {
      return __pc_length(cx, g.node);
    },
    NRep(r) => {
      let k = __pc_length(cx, r.node);
      return if (r.max != r.min || k < 0) { -1 } else { k * r.min };
    },
    NAtomic(a) => {
      return __pc_length(cx, a.node);
    },
    NCall(_) => {
      return -1;
    },
    NBack(b) => {
      let k = __pc_ref_group(cx, b.group, b.name);
      let ls = cx.glen.get();
      return if (k > 0 && k < ls.len()) { ls[k] } else { -1 };
    },
    NCond(c) => {
      let a = __pc_length(cx, c.yes);
      return if (a == __pc_length(cx, c.no)) { a } else { -1 };
    },
    _ => {
      return 0;
    }
  }
  return 0;
}

// The opcodes. Each instruction has three words: the opcode and two operands.
const OP_MATCH: i32 = 0;

const OP_CHAR: i32 = 1;

const OP_SET: i32 = 2;

const OP_ANY: i32 = 3;

const OP_SPLIT: i32 = 4;

const OP_JMP: i32 = 5;

const OP_SAVE: i32 = 6;

const OP_ASSERT: i32 = 7;

const OP_BACK: i32 = 8;

const OP_SUB: i32 = 9;

const OP_END: i32 = 10;

const OP_KEEP: i32 = 11;

const OP_FAIL: i32 = 12;

const OP_MARK: i32 = 13;

const OP_CHECK: i32 = 14;

const OP_COND: i32 = 15;

const OP_CALL: i32 = 16;

const OP_ACCEPT: i32 = 17;

// The program under construction: the instructions, the searches of their
// own, the next free loop register, and the context.
struct PGen { code: i32[], subs: i32[], regs: i32, cx: PCtx, nl: boolean }

function __pc_emit(own g: PGen, op: i32, a: i32, b: i32): PGen {
  return PGen { ...g, code: g.code.append(op).append(a).append(b) };
}

function __pc_here(g: PGen): i32 {
  return g.code.len() / 3;
}

function __pc_patch(own g: PGen, at: i32, slot: i32, v: i32): PGen {
  return PGen { ...g, code: g.code.with(3 * at + slot, v) };
}

function __pc_build(cx: PCtx, root: PNode, o: Options): Result[Pcre, CompileError] {
  let empty: i32[] = [];
  let g = PGen { code: empty, subs: empty, regs: 0, cx: cx, nl: o.crlf };
  g = __pc_emit(g, OP_SAVE, 0, 0);
  g = __pc_gen(g, root);
  g = __pc_emit(__pc_emit(g, OP_SAVE, 1, 0), OP_MATCH, 0, 0);
  let calls = __pc_minus_ones(cx.groups.get() + 1);
  let todo = __pc_called(cx, root, []);
  let k = 0;
  while (k < todo.len()) {
    let n = todo[k];
    k += 1;
    if (calls[n] >= 0) {
      continue;
    }
    let body = if (n == 0) { root } else { __pc_body_of(cx, n) };
    calls = calls.with(n, __pc_here(g));
    g = __pc_gen(g, body);
    g = __pc_emit(g, OP_END, 0, 0);
    todo = __pc_called(cx, body, todo);
  }
  if (g.code.len() > 3000000) {
    return Err(CompileError { offset: cx.p.len(), code: 120, message: __pc_message(120) });
  }
  let ngroups = cx.groups.get();
  let first = if (__pc_nullable(root)) { [] } else { __pc_first(cx, root) };
  return Ok(Pcre {
    code: g.code, subs: g.subs, calls: calls, sets: cx.sets.get(), ngroups: ngroups, names: cx.names.get(), name_groups: cx.name_groups.get(),
    nslots: 2 * (ngroups + 1) + g.regs, utf: o.utf, crlf: o.crlf, anchored: __pc_anchored(root), first: first, req: __pc_req(root),
    word: __pc_set_of(cx, if (o.utf) { __pc_property_ranges("Xwd") } else { [48, 57, 65, 90, 95, 95, 97, 122] }, false, false),
    limit: o.match_limit,
  });
}

function __pc_minus_ones(n: i32): i32[] {
  let out: i32[] = [];
  for k in 0..n {
    out = out.append(-1);
  }
  return out;
}

function __pc_body_of(cx: PCtx, n: i32): PNode {
  for b in cx.bodies.get() {
    if (b.idx == n) {
      return b.node;
    }
  }
  return NEmpty;
}

// `out` with the groups that `x` calls.
// One arm for each kind of node.
// fern-lint: allow cyclomatic-complexity
function __pc_called(cx: PCtx, x: PNode, out: i32[]): i32[] {
  match (x) {
    NCall(c) => {
      return out.append(if (c.group == 0) { 0 } else { __pc_ref_group(cx, c.group, c.name) });
    },
    NSeq(items) => {
      let o = out;
      for y in items {
        o = __pc_called(cx, y, o);
      }
      return o;
    },
    NAlt(items) => {
      let o = out;
      for y in items {
        o = __pc_called(cx, y, o);
      }
      return o;
    },
    NGroup(d) => {
      return __pc_called(cx, d.node, out);
    },
    NRep(r) => {
      return __pc_called(cx, r.node, out);
    },
    NAtomic(a) => {
      return __pc_called(cx, a.node, out);
    },
    NLook(l) => {
      return __pc_called(cx, l.node, out);
    },
    NCond(c) => {
      let o = __pc_called(cx, c.no, __pc_called(cx, c.yes, out));
      if let Some(l) = c.look {
        o = __pc_called(cx, l.node, o);
      }
      return o;
    },
    _ => {
      return out;
    }
  }
  return out;
}

// One arm for each kind of node.
// fern-lint: allow cyclomatic-complexity
function __pc_gen(own g: PGen, x: PNode): PGen {
  match (x) {
    NChar(c) => {
      return __pc_emit(g, OP_CHAR, c, 0);
    },
    NSet(s) => {
      return __pc_emit(g, OP_SET, s, 0);
    },
    NAny(all) => {
      return __pc_emit(g, OP_ANY, if (all) { 1 } else { 0 }, 0);
    },
    NSeq(items) => {
      for y in items {
        g = __pc_gen(g, y);
      }
      return g;
    },
    NAlt(items) => {
      return __pc_gen_alt(g, items);
    },
    NGroup(d) => {
      if (d.idx < 0) {
        return __pc_gen(g, d.node);
      }
      g = __pc_emit(g, OP_SAVE, 2 * d.idx, 0);
      g = __pc_gen(g, d.node);
      return __pc_emit(g, OP_SAVE, 2 * d.idx + 1, 0);
    },
    NRep(r) => {
      return __pc_gen_rep(g, r);
    },
    NAtomic(a) => {
      return __pc_gen_sub(g, ATOMIC, a.node, 0);
    },
    NLook(l) => {
      return __pc_gen_look(g, l);
    },
    NBack(b) => {
      let group = __pc_ref_group(g.cx, b.group, b.name);
      return __pc_emit(g, OP_BACK, group, if (b.caseless) { 1 } else { 0 });
    },
    NAssert(k) => {
      return __pc_emit(g, OP_ASSERT, k, 0);
    },
    NKeep => {
      return __pc_emit(g, OP_KEEP, 0, 0);
    },
    NCond(c) => {
      return __pc_gen_cond(g, c);
    },
    NCall(c) => {
      let group = if (c.group == 0) { 0 } else { __pc_ref_group(g.cx, c.group, c.name) };
      return __pc_emit(g, OP_CALL, group, 0);
    },
    NFail => {
      return __pc_emit(g, OP_FAIL, 0, 0);
    },
    NAccept => {
      return __pc_emit(g, OP_ACCEPT, 0, 0);
    },
    NEmpty => {
      return g;
    }
  }
  return g;
}

function __pc_gen_alt(own g: PGen, items: PNode[]): PGen {
  let jumps: i32[] = [];
  for k in 0..items.len() {
    if (k + 1 < items.len()) {
      let split = __pc_here(g);
      g = __pc_emit(g, OP_SPLIT, split + 1, 0);
      g = __pc_gen(g, items[k]);
      jumps = jumps.append(__pc_here(g));
      g = __pc_emit(g, OP_JMP, 0, 0);
      let next = __pc_here(g);
      g = __pc_patch(g, split, 2, next);
    } else {
      g = __pc_gen(g, items[k]);
    }
  }
  let end = __pc_here(g);
  for j in jumps {
    g = __pc_patch(g, j, 1, end);
  }
  return g;
}

// A repetition: the copies of the minimum, then the optional copies, or a
// loop. A loop of a node that can match an empty string stops when an
// iteration matches none.
// The loop has a form for each kind of repetition.
// fern-lint: allow cyclomatic-complexity
function __pc_gen_rep(own g: PGen, r: PRepData): PGen {
  if (r.kind == 2) {
    return __pc_gen_sub(g, ATOMIC, NRep(PRepData { ...r, kind: 0 }), 0);
  }
  let lazy = r.kind == 1;
  for k in 0..r.min {
    g = __pc_gen(g, r.node);
  }
  if (r.max < 0) {
    let top = __pc_here(g);
    g = __pc_emit(g, OP_SPLIT, 0, 0);
    let body = __pc_here(g);
    let nullable = __pc_nullable(r.node);
    let reg = g.regs;
    if (nullable) {
      g = PGen { ...g, regs: reg + 1 };
      g = __pc_emit(g, OP_MARK, reg, 0);
    }
    g = __pc_gen(g, r.node);
    let check = __pc_here(g);
    if (nullable) {
      g = __pc_emit(g, OP_CHECK, reg, 0);
    }
    g = __pc_emit(g, OP_JMP, top, 0);
    let exit = __pc_here(g);
    if (nullable) {
      g = __pc_patch(g, check, 2, exit);
    }
    return __pc_patch(__pc_patch(g, top, 1, if (lazy) { exit } else { body }), top, 2, if (lazy) { body } else { exit });
  }
  let splits: i32[] = [];
  for k in r.min..r.max {
    splits = splits.append(__pc_here(g));
    g = __pc_emit(g, OP_SPLIT, 0, 0);
    g = __pc_gen(g, r.node);
  }
  let exit = __pc_here(g);
  for s in splits {
    g = __pc_patch(__pc_patch(g, s, 1, if (lazy) { exit } else { s + 1 }), s, 2, if (lazy) { s + 1 } else { exit });
  }
  return g;
}

// A search of its own: the instruction, its body and an OP_END. Its row of
// `subs` holds the kind, the start of the body, the next instruction and
// the length of a look-behind.
function __pc_gen_sub(own g: PGen, kind: i32, body: PNode, len: i32): PGen {
  let id = g.subs.len() / 4;
  let at = __pc_here(g);
  g = __pc_emit(g, OP_SUB, id, 0);
  let subs = g.subs.append(kind).append(at + 1).append(0).append(len);
  g = PGen { ...g, subs: subs };
  g = __pc_gen(g, body);
  g = __pc_emit(g, OP_END, 0, 0);
  let next = __pc_here(g);
  let done = g.subs.with(4 * id + 2, next);
  return PGen { ...g, subs: done };
}

// A look-behind is one search for each length of its alternatives.
function __pc_gen_look(own g: PGen, l: PLookData): PGen {
  if (l.kind == LOOK_AHEAD || l.kind == LOOK_NOT_AHEAD) {
    return __pc_gen_sub(g, l.kind, l.node, 0);
  }
  let branches = match (l.node) {
    NAlt(items) => items,
    _ => [l.node],
  };
  if (l.kind == LOOK_NOT_BEHIND) {
    for b in branches {
      let len = __pc_length(g.cx, b);
      g = __pc_gen_sub(g, LOOK_NOT_BEHIND, b, len);
    }
    return g;
  }
  if (branches.len() == 1) {
    let len = __pc_length(g.cx, l.node);
    return __pc_gen_sub(g, LOOK_BEHIND, l.node, len);
  }
  let as_alt: PNode[] = [];
  for b in branches {
    as_alt = as_alt.append(NLook(PLookData { kind: LOOK_BEHIND, node: b, at: l.at }));
  }
  return __pc_gen_alt(g, as_alt);
}

// A conditional group: OP_COND tests a group, and else jumps to the second
// branch. A condition of a look-around is a search of its own before the
// first branch.
function __pc_gen_cond(own g: PGen, c: PCondData): PGen {
  if let Some(l) = c.look {
    let yes_no = NAlt([NSeq([NLook(l), c.yes]), NSeq([NLook(PLookData { kind: __pc_not(l.kind), node: l.node, at: l.at }), c.no])]);
    return __pc_gen(g, yes_no);
  }
  let at = __pc_here(g);
  let group = if (c.group == COND_RECURSE || c.group == COND_DEFINE) { c.group } else { __pc_ref_group(g.cx, c.group, c.name) };
  g = __pc_emit(g, OP_COND, group, 0);
  g = __pc_gen(g, c.yes);
  let jump = __pc_here(g);
  g = __pc_emit(g, OP_JMP, 0, 0);
  let no = __pc_here(g);
  g = __pc_patch(g, at, 2, no);
  g = __pc_gen(g, c.no);
  let end = __pc_here(g);
  return __pc_patch(g, jump, 1, end);
}

function __pc_not(kind: i32): i32 {
  return match (kind) {
    1 => 2,
    2 => 1,
    3 => 4,
    _ => 3,
  };
}

// One arm for each kind of node.
// fern-lint: allow cyclomatic-complexity
function __pc_nullable(x: PNode): boolean {
  match (x) {
    NChar(_) => {
      return false;
    },
    NSet(_) => {
      return false;
    },
    NAny(_) => {
      return false;
    },
    NSeq(items) => {
      for y in items {
        if (!__pc_nullable(y)) {
          return false;
        }
      }
      return true;
    },
    NAlt(items) => {
      for y in items {
        if (__pc_nullable(y)) {
          return true;
        }
      }
      return false;
    },
    NGroup(g) => {
      return __pc_nullable(g.node);
    },
    NRep(r) => {
      return r.min == 0 || __pc_nullable(r.node);
    },
    NAtomic(a) => {
      return __pc_nullable(a.node);
    },
    NBack(_) => {
      return true;
    },
    NCall(_) => {
      return true;
    },
    NCond(c) => {
      return __pc_nullable(c.yes) || __pc_nullable(c.no);
    },
    NFail => {
      return false;
    },
    _ => {
      return true;
    }
  }
  return true;
}

// Whether each match starts at the start of the text: each alternative
// starts with \A, or with ^ outside multi-line mode.
function __pc_anchored(x: PNode): boolean {
  match (x) {
    NAssert(k) => {
      return k == A_BOL || k == A_START;
    },
    NSeq(items) => {
      return items.len() > 0 && __pc_anchored(items[0]);
    },
    NAlt(items) => {
      for y in items {
        if (!__pc_anchored(y)) {
          return false;
        }
      }
      return true;
    },
    NGroup(g) => {
      return __pc_anchored(g.node);
    },
    NAtomic(a) => {
      return __pc_anchored(a.node);
    },
    _ => {
      return false;
    }
  }
  return false;
}

// The bytes that can start a match of a node that cannot match an empty
// string, as 256 marks, or [] for each byte.
function __pc_first(cx: PCtx, x: PNode): u8[] {
  let none: u8[] = [];
  let marks = __pc_first_into(cx, x, __pc_zero_marks());
  return if (marks.len() == 0) { none } else { marks };
}

function __pc_zero_marks(): u8[] {
  let m: u8[] = [];
  for k in 0..256 {
    m = m.append(0u8);
  }
  return m;
}

// The first bytes of `x` added to `m`, or [] when any byte can start it.
// One arm for each kind of node.
// fern-lint: allow cyclomatic-complexity
function __pc_first_into(cx: PCtx, x: PNode, m: u8[]): u8[] {
  let none: u8[] = [];
  if (m.len() == 0) {
    return m;
  }
  match (x) {
    NChar(c) => {
      return m.with(__pc_lead_byte(c, cx.utf), 1u8);
    },
    NSet(s) => {
      return __pc_set_firsts(cx, cx.sets.get()[s], m);
    },
    NSeq(items) => {
      let out = m;
      for y in items {
        out = __pc_first_into(cx, y, out);
        if (!__pc_nullable(y)) {
          return out;
        }
      }
      return out;
    },
    NAlt(items) => {
      let out = m;
      for y in items {
        out = __pc_first_into(cx, y, out);
      }
      return out;
    },
    NGroup(g) => {
      return __pc_first_into(cx, g.node, m);
    },
    NRep(r) => {
      return __pc_first_into(cx, r.node, m);
    },
    NAtomic(a) => {
      return __pc_first_into(cx, a.node, m);
    },
    NLook(_) => {
      return m;
    },
    NAssert(_) => {
      return m;
    },
    NKeep => {
      return m;
    },
    _ => {
      return none;
    }
  }
  return none;
}

function __pc_lead_byte(c: i32, utf: boolean): i32 {
  if (!utf || c < 128) {
    return c & 255;
  }
  if (c < 2048) {
    return 192 | c >> 6;
  }
  if (c < 65536) {
    return 224 | c >> 12;
  }
  return 240 | c >> 18;
}

function __pc_set_firsts(cx: PCtx, s: PSet, m: u8[]): u8[] {
  let out = m;
  let top = if (cx.utf) { 128 } else { 256 };
  for c in 0..top {
    if ((s.bits[c >> 5] >> (c & 31) & 1) != 0) {
      out = out.with(c, 1u8);
    }
  }
  if (cx.utf) {
    for c in 194..245 {
      out = out.with(c, 1u8);
    }
  }
  return out;
}

// A byte that each match holds: the last literal byte of the top level of
// the pattern that each match goes through, or -1.
function __pc_req(x: PNode): i32 {
  match (x) {
    NChar(c) => {
      return if (c < 128) { c } else { -1 };
    },
    NSeq(items) => {
      let k = items.len() - 1;
      while (k >= 0) {
        let r = __pc_req(items[k]);
        if (r >= 0) {
          return r;
        }
        k -= 1;
      }
      return -1;
    },
    NGroup(g) => {
      return __pc_req(g.node);
    },
    NAtomic(a) => {
      return __pc_req(a.node);
    },
    NRep(r) => {
      return if (r.min > 0) { __pc_req(r.node) } else { -1 };
    },
    _ => {
      return -1;
    }
  }
  return -1;
}
```

### Matcher

`__pc_run` runs the program from an instruction at an offset. Its stack holds three words for each entry. A choice holds the instruction and the offset to go back to. An entry of a group or of a loop register holds the old value to put back. The count of the choices is the count of the match limit, and the searches of their own add their choices to it.

```fern
<<matcher>>=
// The state of a search: the text, the start of the attempt for \G, and
// the count of the steps.
struct PRun { re: Pcre, text: string, start: i32, steps: Cell[i32], depth: i32 }

// The first match at or after `start`, or an error after too many steps.
pub function (re: Pcre) find_at(text: string, start: i32): Result[Option[Match], MatchError] {
  let caps = re.captures_at(text, start)?;
  return match (caps) {
    Some(c) => Ok(Some(Match { start: c[0], end: c[1] })),
    None => Ok(None),
  };
}

// Whether a match is in the text.
pub function (re: Pcre) is_match(text: string): Result[boolean, MatchError] {
  return Ok(re.find_at(text, 0)?.is_some());
}

// The start and the end of each group of the first match at or after
// `start`, group 0 first, or None. A group that did not match has -1.
// The tests of the start of a match come before each attempt, as in PCRE2.
// fern-lint: allow cyclomatic-complexity
pub function (re: Pcre) captures_at(text: string, start: i32): Result[Option[i32[]], MatchError] {
  let steps = cell_new(0);
  let n = text.len();
  if (re.req >= 0 && __memchr(text, re.req, if (start < 0) { 0 } else { start }) < 0) {
    return Ok(None);
  }
  let at = if (start < 0) { 0 } else { start };
  if (re.utf) {
    while (at < n && (text[at] as i32 & 192) == 128) {
      at += 1;
    }
  }
  let origin = at;
  while (at <= n) {
    if (re.first.len() > 0) {
      at = __scan_set(text, at, re.first);
      if (at >= n) {
        return Ok(None);
      }
    }
    let run = PRun { re: re, text: text, start: origin, steps: steps, depth: 0 };
    let slots = __pc_slots(re.nslots);
    let (ok, end, out) = __pc_run(run, 0, at, slots, -1);
    if (steps.get() > re.limit) {
      return Err(MatchLimit);
    }
    if (ok) {
      return Ok(Some(out.take(2 * (re.ngroups + 1))));
    }
    if (re.anchored || at >= n) {
      return Ok(None);
    }
    at = __pc_next_start(re, text, at);
  }
  return Ok(None);
}

function __pc_slots(n: i32): i32[] {
  let s: i32[] = [];
  for k in 0..n {
    s = s.append(-1);
  }
  return s;
}

// The next start after `at`: the next character with UTF-8.
function __pc_next_start(re: Pcre, text: string, at: i32): i32 {
  if (!re.utf) {
    return at + 1;
  }
  let i = at + 1;
  while (i < text.len() && (text[i] as i32 & 192) == 128) {
    i += 1;
  }
  return i;
}

// Runs the program from `pc0` at `pos0`. A look-behind gives `target`, the
// offset where its body must end. The result is whether it matched, the
// offset at the end, and the slots of the match.
// One branch for each opcode, in one loop, so that a step calls no function.
// fern-lint: allow cyclomatic-complexity
function __pc_run(r: PRun, pc0: i32, pos0: i32, slots0: i32[], target: i32): (boolean, i32, i32[]) {
  let code = r.re.code;
  let text = r.text;
  let slots = slots0;
  let st: i32[] = [];
  let sp = 0;
  let pc = pc0;
  let pos = pos0;
  while (true) {
    let op = code[3 * pc];
    let a = code[3 * pc + 1];
    let ok = true;
    if (op == OP_CHAR) {
      let (c, n) = __pc_char_at(r.re, text, pos);
      ok = c == a;
      pos += n;
      pc += 1;
    } else if (op == OP_SET) {
      let (c, n) = __pc_char_at(r.re, text, pos);
      ok = c >= 0 && __pc_in_set(r.re.sets[a], c);
      pos += n;
      pc += 1;
    } else if (op == OP_ANY) {
      let (c, n) = __pc_char_at(r.re, text, pos);
      ok = c >= 0 && (a == 1 || !__pc_is_nl(r.re, c));
      pos += n;
      pc += 1;
    } else if (op == OP_SPLIT) {
      st = __pc_push(st, sp, code[3 * pc + 2], pos, -1);
      sp += 3;
      r.steps.set(r.steps.get() + 1);
      if (r.steps.get() > r.re.limit) {
        return (false, pos, slots);
      }
      pc = a;
    } else if (op == OP_JMP) {
      pc = a;
    } else if (op == OP_SAVE || op == OP_MARK) {
      let slot = if (op == OP_SAVE) { a } else { 2 * (r.re.ngroups + 1) + a };
      st = __pc_push(st, sp, -1, slot, slots[slot]);
      sp += 3;
      slots = slots.with(slot, pos);
      pc += 1;
    } else if (op == OP_CHECK) {
      pc = if (slots[2 * (r.re.ngroups + 1) + a] == pos) { code[3 * pc + 2] } else { pc + 1 };
    } else if (op == OP_ASSERT) {
      ok = __pc_assert(r, a, pos);
      pc += 1;
    } else if (op == OP_BACK) {
      let end = __pc_back(r, slots, a, code[3 * pc + 2] == 1, pos);
      ok = end >= 0;
      pos = end;
      pc += 1;
    } else if (op == OP_SUB) {
      let (sok, spos, sslots) = __pc_sub(r, a, pos, slots);
      if (r.steps.get() > r.re.limit) {
        return (false, pos, slots);
      }
      ok = sok;
      if (sok) {
        for k in 0..slots.len() {
          if (sslots[k] != slots[k]) {
            st = __pc_push(st, sp, -1, k, slots[k]);
            sp += 3;
          }
        }
        slots = sslots;
        pos = spos;
      }
      pc = r.re.subs[4 * a + 2];
    } else if (op == OP_KEEP) {
      st = __pc_push(st, sp, -1, 0, slots[0]);
      sp += 3;
      slots = slots.with(0, pos);
      pc += 1;
    } else if (op == OP_COND) {
      let yes = if (a == COND_RECURSE) { r.depth > 0 } else { a >= 0 && slots[2 * a + 1] >= 0 };
      pc = if (yes) { pc + 1 } else { code[3 * pc + 2] };
    } else if (op == OP_CALL) {
      if (r.depth >= 250) {
        r.steps.set(r.re.limit + 1);
        return (false, pos, slots);
      }
      let (cok, cpos, cslots) = __pc_run(PRun { ...r, depth: r.depth + 1 }, r.re.calls[a], pos, slots, -1);
      if (r.steps.get() > r.re.limit) {
        return (false, pos, slots);
      }
      ok = cok;
      if (cok) {
        pos = cpos;
      }
      pc += 1;
    } else if (op == OP_FAIL) {
      ok = false;
    } else if (op == OP_ACCEPT) {
      for k in 0..r.re.ngroups + 1 {
        if (slots[2 * k] >= 0 && slots[2 * k + 1] < 0) {
          slots = slots.with(2 * k + 1, pos);
        }
      }
      return (true, pos, slots);
    } else {
      if (target < 0 || pos == target) {
        return (true, pos, slots);
      }
      ok = false;
    }
    if (!ok) {
      let back = false;
      while (sp > 0) {
        sp -= 3;
        if (st[sp] >= 0) {
          pc = st[sp];
          pos = st[sp + 1];
          back = true;
          break;
        }
        slots = slots.with(st[sp + 1], st[sp + 2]);
      }
      if (!back) {
        return (false, pos, slots);
      }
    }
  }
  return (false, pos, slots);
}

function __pc_push(own st: i32[], sp: i32, a: i32, b: i32, c: i32): i32[] {
  if (sp + 3 <= st.len()) {
    return st.with(sp, a).with(sp + 1, b).with(sp + 2, c);
  }
  return st.append(a).append(b).append(c);
}

// A search of its own: an atomic group or a look-around. It gives whether
// the item matches, the offset after it, and the slots.
function __pc_sub(r: PRun, id: i32, pos: i32, slots: i32[]): (boolean, i32, i32[]) {
  let subs = r.re.subs;
  let kind = subs[4 * id];
  let body = subs[4 * id + 1];
  if (kind == ATOMIC || kind == LOOK_AHEAD) {
    let (ok, end, out) = __pc_run(r, body, pos, slots, -1);
    return (ok, if (kind == ATOMIC) { end } else { pos }, if (ok) { out } else { slots });
  }
  if (kind == LOOK_NOT_AHEAD) {
    let (ok, end, out) = __pc_run(r, body, pos, slots, -1);
    return (!ok, pos, slots);
  }
  let from = __pc_back_chars(r.re, r.text, pos, subs[4 * id + 3]);
  let ok = false;
  let out = slots;
  if (from >= 0) {
    let (bok, end, bout) = __pc_run(r, body, from, slots, pos);
    ok = bok;
    out = bout;
  }
  if (kind == LOOK_NOT_BEHIND) {
    return (!ok, pos, slots);
  }
  return (ok, pos, if (ok) { out } else { slots });
}

// The offset `n` characters before `pos`, or -1.
function __pc_back_chars(re: Pcre, text: string, pos: i32, n: i32): i32 {
  let at = pos;
  for k in 0..n {
    if (at <= 0) {
      return -1;
    }
    at -= 1;
    if (re.utf) {
      while (at > 0 && (text[at] as i32 & 192) == 128) {
        at -= 1;
      }
    }
  }
  return at;
}

// The character at `pos` and its count of bytes, or -1 at the end and at a
// byte that does not start a valid UTF-8 character.
function __pc_char_at(re: Pcre, text: string, pos: i32): (i32, i32) {
  if (pos >= text.len()) {
    return (-1, 0);
  }
  let b = text[pos] as i32;
  if (!re.utf || b < 128) {
    return (b, 1);
  }
  return __pc_decode(text, pos);
}

// Each branch checks one rule of UTF-8.
// fern-lint: allow cyclomatic-complexity
function __pc_decode(text: string, pos: i32): (i32, i32) {
  let b = text[pos] as i32;
  let n = if (b >= 194 && b < 224) { 2 } else if (b >= 224 && b < 240) { 3 } else if (b >= 240 && b < 245) { 4 } else { 0 };
  if (n == 0 || pos + n > text.len()) {
    return (-1, 1);
  }
  let c = b & (if (n == 2) { 31 } else if (n == 3) { 15 } else { 7 });
  for k in 1..n {
    let x = text[pos + k] as i32;
    if ((x & 192) != 128) {
      return (-1, 1);
    }
    c = c << 6 | x & 63;
  }
  if (n == 3 && (c < 2048 || c >= 55296 && c <= 57343) || n == 4 && (c < 65536 || c > 1114111)) {
    return (-1, 1);
  }
  return (c, n);
}

// The character before `pos`, or -1.
function __pc_char_before(re: Pcre, text: string, pos: i32): i32 {
  if (pos <= 0) {
    return -1;
  }
  if (!re.utf) {
    return text[pos - 1] as i32;
  }
  let at = pos - 1;
  while (at > 0 && pos - at < 4 && (text[at] as i32 & 192) == 128) {
    at -= 1;
  }
  let (c, n) = __pc_char_at(re, text, at);
  return if (at + n == pos) { c } else { -1 };
}

function __pc_in_set(s: PSet, c: i32): boolean {
  if (c < 256) {
    return (s.bits[c >> 5] >> (c & 31) & 1) != 0;
  }
  let lo = 0;
  let hi = s.ranges.len() / 2;
  while (lo < hi) {
    let mid = (lo + hi) / 2;
    if (c < s.ranges[2 * mid]) {
      hi = mid;
    } else if (c > s.ranges[2 * mid + 1]) {
      lo = mid + 1;
    } else {
      return !s.neg;
    }
  }
  return s.neg;
}

function __pc_is_nl(re: Pcre, c: i32): boolean {
  return c == 10 || re.crlf && c == 13;
}

function __pc_is_word(re: Pcre, c: i32): boolean {
  if (c < 0) {
    return false;
  }
  return __pc_in_set(re.word, c);
}

// One branch for each assertion.
// fern-lint: allow cyclomatic-complexity
function __pc_assert(r: PRun, kind: i32, pos: i32): boolean {
  let text = r.text;
  let n = text.len();
  if (kind == A_BOL || kind == A_START) {
    return pos == 0;
  }
  if (kind == A_MBOL) {
    return pos == 0 || pos < n && __pc_is_nl(r.re, text[pos - 1] as i32) && !(r.re.crlf && text[pos - 1] == 13u8 && text[pos] == 10u8);
  }
  if (kind == A_END) {
    return pos == n;
  }
  if (kind == A_EOL || kind == A_END_NL) {
    return pos == n || pos == n - 1 && __pc_is_nl(r.re, text[pos] as i32) || r.re.crlf && pos == n - 2 && text[pos] == 13u8 && text[pos + 1] == 10u8;
  }
  if (kind == A_MEOL) {
    return pos == n || __pc_is_nl(r.re, text[pos] as i32);
  }
  if (kind == A_G) {
    return pos == r.start;
  }
  let before = __pc_is_word(r.re, __pc_char_before(r.re, text, pos));
  let (c, w) = __pc_char_at(r.re, text, pos);
  let after = __pc_is_word(r.re, c);
  return (before != after) == (kind == A_WORD);
}

// The offset after a reference to `group` at `pos`, or -1.
function __pc_back(r: PRun, slots: i32[], group: i32, caseless: boolean, pos: i32): i32 {
  let s = slots[2 * group];
  let e = slots[2 * group + 1];
  if (s < 0 || e < 0) {
    return -1;
  }
  let text = r.text;
  if (!caseless) {
    if (pos + e - s > text.len()) {
      return -1;
    }
    for k in 0..e - s {
      if (text[s + k] != text[pos + k]) {
        return -1;
      }
    }
    return pos + e - s;
  }
  let i = s;
  let j = pos;
  while (i < e) {
    let (a, an) = __pc_char_at(r.re, text, i);
    let (b, bn) = __pc_char_at(r.re, text, j);
    if (b < 0 || !__pc_same_fold(r.re, a, b)) {
      return -1;
    }
    i += an;
    j += bn;
  }
  return j;
}

function __pc_same_fold(re: Pcre, a: i32, b: i32): boolean {
  if (a == b) {
    return true;
  }
  if (a < 128 || !re.utf) {
    return a < 128 && b < 128 && (a | 32) == (b | 32) && (a | 32) >= 97 && (a | 32) <= 122;
  }
  let c = unicode.simple_fold(a as char) as i32;
  while (c != a) {
    if (c == b) {
      return true;
    }
    c = unicode.simple_fold(c as char) as i32;
  }
  return false;
}
```
