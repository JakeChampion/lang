# Perl-compatible regular expressions

This module matches the regular expressions of PCRE2 10.42 with a backtracking engine.

## Model

`compile` parses a pattern with the syntax of PCRE2 and gives a program. A search tries each start from left to right. At a start, the engine runs the program and keeps a stack of the choices that it did not take. When a step fails, the engine goes back to the last choice. Thus the first match is the match of Perl and PCRE2, with the priority of the alternatives and of the repetitions. A match can need a look back or a reference to an earlier group, which a DFA cannot do.

A look-ahead, a look-behind and an atomic group run as a search of their own. When that search succeeds, its choices are gone, as in PCRE2. A possessive repetition is an atomic group.

A call of a group, such as `(?1)`, keeps its choices, as in PCRE2 10.30 and later: a later failure can go back into the call. The call has a frame that holds the captures at the call, and they come back at its end. A call of the whole pattern, `(?R)`, and a call that ends with `(*ACCEPT)`, drop their choices, as PCRE2 does. A call of a group in the same group at the same offset gives the error of PCRE2.

The verbs (*COMMIT), (*PRUNE), (*SKIP), (*SKIP:NAME), (*THEN) and (*MARK:NAME) put an entry on the stack. When the search backtracks onto the entry, the verb acts, and the stack carries its effect back. Each entry then acts as the frame of PCRE2 at the same point acts: a branch can catch (*THEN), a call can end the effect of a verb in it, and a look-around has the rules of PCRE2 for each verb. (*COMMIT) ends the search, (*PRUNE) moves to the next start, and (*SKIP) moves the next start to its offset. A callout, such as `(?C1)`, matches the empty string, because no callout function is set.

The options at the start of a pattern, such as (*UTF), (*UCP), (*CRLF), (*NO_START_OPT) and (*LIMIT_MATCH=n), are those of PCRE2 10.42. (*NO_AUTO_POSSESS), (*NO_DOTSTAR_ANCHOR) and (*NO_JIT) change no match of this engine.

With `utf`, the pattern and the text are UTF-8, and each item matches a character. A byte that does not start a valid character matches no item, as with `PCRE2_MATCH_INVALID_UTF`. `\d`, `\s`, `\w`, `\b` and the POSIX classes then use the properties of Unicode, as with `PCRE2_UCP`. Without `utf`, each item matches a byte, and the classes are those of ASCII.

## Contracts

A pattern that PCRE2 refuses gives a `CompileError` with the text and the offset of PCRE2's error. An attempt at a start that takes more choices than the match limit gives `MatchLimit`, as PCRE2 does. The default limit is ten million, as in PCRE2. A limit of the depth or of the heap in the pattern counts the entries of the stack. A search does not try a start where the rest of the text is shorter than the least length of a match, as PCRE2 does. Each error has the text of PCRE2.

Both error types implement `core/error.Error` and `core/cmp.Display`.
Their `message()` methods return the same text as `to_string()`.

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
import "core/error";
import "core/cmp";

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

pub function (e: CompileError) message(): string {
  return e.to_string();
}

impl error.Error for CompileError {}

impl cmp.Display for CompileError {}

// The error of a search: too many steps, a call of a group in the same
// group at the same offset, or the depth or heap limit of the pattern.
pub enum MatchError { MatchLimit, RecurseLoop, DepthLimit, HeapLimit }

pub function (e: MatchError) to_string(): string {
  let text = match (e) {
    MatchLimit => "match limit exceeded",
    RecurseLoop => "nested recursion at the same subject position",
    DepthLimit => "matching depth limit exceeded",
    HeapLimit => "heap limit exceeded",
  };
  return "PCRE2: error matching: " + text;
}

pub function (e: MatchError) message(): string {
  return e.to_string();
}

impl error.Error for MatchError {}

impl cmp.Display for MatchError {}

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
// `alts` holds two words for each branch of an alternation that (*THEN) can
// end: the offset in the pattern where the branch ends, and the group of
// the call whose body it is, or -2. `nl` is the newline convention.
// `notempty` is 1 for (*NOTEMPTY) and 2 for (*NOTEMPTY_ATSTART). A limit
// of the pattern on the depth or the heap is -1 when it has none.
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
  fold: boolean,
  nl: i32,
  anchored: boolean,
  first: u8[],
  req: i32,
  minlen: i32,
  word: PSet,
  limit: i32,
  depth_limit: i32,
  heap_limit: i32,
  notempty: i32,
  has_then: boolean,
  crlf_lit: boolean,
  alts: i32[],
  verbs: boolean,
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
  NAlt(PAltData),
  NGroup(PGroupData),
  NRep(PRepData),
  NAtomic(PAtomicData),
  NLook(PLookData),
  NBack(PBackData),
  NAssert(i32),
  NKeep,
  NCond(PCondData),
  NCall(PCallData),
  NAccept(PAcceptData),
  NFail,
  NVerb(PVerbData),
  NBackup(i32),
  NEmpty,
}

struct PGroupData { idx: i32, node: PNode }

// The branches of an alternation, and the offset where each one ends: its
// `|`, or the end of the group. The ends are empty when the parser did not
// read the alternation from the pattern.
struct PAltData { items: PNode[], ends: i32[] }

// (*ACCEPT): the groups that it closes, innermost first, and whether it is
// in an assertion, where it ends only the assertion.
struct PAcceptData { closes: i32[], in_look: boolean }

// A verb that acts when the search backtracks onto it: V_COMMIT, V_PRUNE,
// V_SKIP, V_SKIP_ARG, V_THEN or V_MARK. `name` indexes the names of the
// marks. `at` is the offset of the verb in the pattern.
struct PVerbData { kind: i32, name: i32, at: i32 }

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

// `many` is the offset of a third branch, which is an error, or -1.
struct PCondData { group: i32, name: string, look: Option[PLookData], yes: PNode, no: PNode, at: i32, many: i32 }

// The conditions (?(R)...) and (?(DEFINE)...).
const COND_RECURSE: i32 = -2;

const COND_DEFINE: i32 = -3;

// The condition (?(R&name)...), and (?(Rn)...) as COND_RECURSE_N - n.
const COND_RECURSE_NAME: i32 = -99;

const COND_RECURSE_N: i32 = -100;

const LOOK_AHEAD: i32 = 1;

const LOOK_NOT_AHEAD: i32 = 2;

const LOOK_BEHIND: i32 = 3;

const LOOK_NOT_BEHIND: i32 = 4;

// The look-arounds of (*napla:...) and (*naplb:...), which keep their
// choices.
const LOOK_AHEAD_NA: i32 = 5;

const LOOK_BEHIND_NA: i32 = 6;

const ATOMIC: i32 = 0;

// A look-around that is the condition of a group: its kind plus COND.
const COND: i32 = 8;

const V_COMMIT: i32 = 1;

const V_PRUNE: i32 = 2;

const V_SKIP: i32 = 3;

const V_SKIP_ARG: i32 = 4;

const V_THEN: i32 = 5;

const V_MARK: i32 = 6;

// The newline conventions.
const NL_LF: i32 = 0;

const NL_ANYCRLF: i32 = 1;

const NL_CR: i32 = 2;

const NL_CRLF: i32 = 3;

const NL_ANY: i32 = 4;

const NL_NUL: i32 = 5;

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
// `ucp` gives the classes of Unicode, and `fold` the case folding of
// Unicode. `open` holds two words for each capture group that the parser is
// in: its number and the depth of the look-arounds at its start. `marks`
// holds the names of the verbs. `crlf_lit` records a literal CR or LF.
struct PCtx {
  p: string,
  utf: boolean,
  ucp: boolean,
  fold: boolean,
  bsr_crlf: boolean,
  has_then: boolean,
  err: Cell[Option[CompileError]],
  groups: Cell[i32],
  names: Cell[string[]],
  name_groups: Cell[i32[]],
  sets: Cell[PSet[]],
  total: i32,
  bodies: Cell[PGroupData[]],
  glen: Cell[i32[]],
  open: Cell[i32[]],
  looks: Cell[i32],
  marks: Cell[string[]],
  crlf_lit: Cell[boolean],
  accept: Cell[boolean],
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
    138 => "number after (?C is greater than 255",
    139 => "closing parenthesis for (?C expected",
    148 => "subpattern name is too long (maximum 32 code units)",
    150 => "invalid range in character class",
    155 => "missing opening brace after \\o",
    157 => "\\g is not followed by a braced, angle-bracketed, or quoted name/number or by a plain number",
    160 => "(*VERB) not recognized or malformed",
    161 => "subpattern number is too big",
    162 => "subpattern name expected",
    164 => "non-octal character in \\o{} (closing brace missing?)",
    166 => "(*MARK) must have an argument",
    167 => "non-hex character in \\x{} (closing brace missing?)",
    168 => "\\c must be followed by a printable ASCII character",
    169 => "\\k is not followed by a braced, angle-bracketed, or quoted name",
    171 => "\\N is not supported in a class",
    173 => "disallowed Unicode code point (>= 0xd800 && <= 0xdfff)",
    176 => "name is too long in (*MARK), (*PRUNE), (*SKIP), or (*THEN)",
    178 => "digits missing in \\x{} or \\o{} or \\N{U+}",
    181 => "missing terminating delimiter for callout with string argument",
    182 => "unrecognized string delimiter follows (?C",
    194 => "invalid hyphen in option setting",
    195 => "(*alpha_assertion) not recognized",
    193 => "\\N{U+dddd} is supported only in Unicode (UTF) mode",
    199 => "\\K is not allowed in lookarounds (but see PCRE2_EXTRA_ALLOW_LOOKAROUND_BSK)",
    _ => "internal error",
  };
}

pub function compile(pattern: string, o: Options): Result[Pcre, CompileError] {
  let s = __pc_start_options(pattern, o);
  if (s.err >= 0) {
    return Err(CompileError { offset: s.err, code: 160, message: __pc_message(160) });
  }
  let none: string[] = [];
  let gs: i32[] = [];
  let ss: PSet[] = [];
  let bodies: PGroupData[] = [];
  let cx = PCtx {
    p: pattern, utf: s.utf, ucp: s.ucp, fold: s.utf || s.ucp, bsr_crlf: s.bsr_crlf, has_then: __pc_has(pattern, "(*THEN"), err: cell_new(None),
    groups: cell_new(0), names: cell_new(none), name_groups: cell_new(gs), sets: cell_new(ss), total: __pc_count_groups(pattern),
    bodies: cell_new(bodies), glen: cell_new(gs), open: cell_new(gs), looks: cell_new(0), marks: cell_new(none), crlf_lit: cell_new(false),
    accept: cell_new(false),
  };
  let f = (if (o.caseless) { F_I } else { 0 }) | (if (o.multiline) { F_M } else { 0 }) | (if (o.dotall) { F_S } else { 0 }) | (if (o.extended) { F_X } else { 0 });
  let r = __pc_alt(cx, s.at, f, 0);
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
  return __pc_build(cx, r.node, s);
}

// The options at the start of a pattern, such as (*UTF) and
// (*LIMIT_MATCH=n), with those of `Options`. `at` is the offset after them.
// `err` is the offset of a malformed limit, or -1. A limit is -1 when the
// pattern does not set it.
struct PStart {
  at: i32,
  err: i32,
  utf: boolean,
  ucp: boolean,
  nl: i32,
  bsr_crlf: boolean,
  notempty: i32,
  no_start: boolean,
  limit: i32,
  depth: i32,
  heap: i32,
}

// The names of the options in the order that PCRE2 tries them. A name
// that ends in "=" takes a number.
const START_NAMES: string[] = [
  "UTF8)", "UTF)", "UCP)", "NOTEMPTY)", "NOTEMPTY_ATSTART)", "NO_AUTO_POSSESS)", "NO_DOTSTAR_ANCHOR)", "NO_JIT)", "NO_START_OPT)",
  "LIMIT_HEAP=", "LIMIT_MATCH=", "LIMIT_DEPTH=", "LIMIT_RECURSION=", "CR)", "LF)", "CRLF)", "ANY)", "NUL)", "ANYCRLF)", "BSR_ANYCRLF)",
  "BSR_UNICODE)",
];

// One arm for each option.
// fern-lint: allow cyclomatic-complexity
function __pc_start_options(p: string, o: Options): PStart {
  let nl = if (o.crlf) { NL_ANYCRLF } else { NL_LF };
  let s = PStart {
    at: 0, err: -1, utf: o.utf, ucp: o.utf, nl: nl, bsr_crlf: false, notempty: 0, no_start: false, limit: o.match_limit, depth: -1,
    heap: -1,
  };
  while (s.at + 1 < p.len() && p[s.at] == 40u8 && p[s.at + 1] == 42u8) {
    let k = 0;
    while (k < START_NAMES.len() && !__pc_at(p, s.at + 2, START_NAMES[k])) {
      k += 1;
    }
    if (k == START_NAMES.len()) {
      return s;
    }
    let at = s.at + 2 + START_NAMES[k].len();
    let n = 0;
    if (k >= 9 && k <= 12) {
      if (at >= p.len() || p[at] < 48u8 || p[at] > 57u8) {
        return PStart { ...s, err: at };
      }
      let big = 0i64;
      while (at < p.len() && p[at] >= 48u8 && p[at] <= 57u8 && big <= 429496728i64) {
        big = big * 10i64 + (p[at] as i64 - 48i64);
        at += 1;
      }
      if (at >= p.len() || p[at] != 41u8) {
        return PStart { ...s, err: at + 1 };
      }
      at += 1;
      n = if (big > 2147483647i64) { 2147483647 } else { big as i32 };
    }
    s = match (k) {
      0 | 1 => PStart { ...s, at: at, utf: true },
      2 => PStart { ...s, at: at, ucp: true },
      3 => PStart { ...s, at: at, notempty: 1 },
      4 => PStart { ...s, at: at, notempty: if (s.notempty == 1) { 1 } else { 2 } },
      8 => PStart { ...s, at: at, no_start: true },
      9 => PStart { ...s, at: at, heap: n },
      10 => PStart { ...s, at: at, limit: if (n < s.limit) { n } else { s.limit } },
      11 | 12 => PStart { ...s, at: at, depth: n },
      13 => PStart { ...s, at: at, nl: NL_CR },
      14 => PStart { ...s, at: at, nl: NL_LF },
      15 => PStart { ...s, at: at, nl: NL_CRLF },
      16 => PStart { ...s, at: at, nl: NL_ANY },
      17 => PStart { ...s, at: at, nl: NL_NUL },
      18 => PStart { ...s, at: at, nl: NL_ANYCRLF },
      19 => PStart { ...s, at: at, bsr_crlf: true },
      20 => PStart { ...s, at: at, bsr_crlf: false },
      _ => PStart { ...s, at: at },
    };
  }
  return s;
}

// Whether `word` is in `p` at `at`.
function __pc_at(p: string, at: i32, word: string): boolean {
  if (at + word.len() > p.len()) {
    return false;
  }
  for k in 0..word.len() {
    if (p[at + k] != word[k]) {
      return false;
    }
  }
  return true;
}

// Whether `word` is in `p`.
function __pc_has(p: string, word: string): boolean {
  let at = __memchr(p, word[0] as i32, 0);
  while (at >= 0) {
    if (__pc_at(p, at, word)) {
      return true;
    }
    at = __memchr(p, word[0] as i32, at + 1);
  }
  return false;
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
  let ends: i32[] = [];
  let pos = at;
  let f = f0;
  while (true) {
    let s = __pc_seq(cx, pos, f, depth);
    branches = branches.append(s.node);
    pos = s.pos;
    ends = ends.append(pos);
    f = s.f;
    if (__pc_failed(cx) || pos >= cx.p.len() || cx.p[pos] != 124u8) {
      break;
    }
    pos += 1;
  }
  let node = if (branches.len() == 1) { branches[0] } else { NAlt(PAltData { items: branches, ends: ends }) };
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
  __pc_note_crlf(cx, cp);
  if ((f & F_I) == 0 || !__pc_has_case(cx, cp)) {
    return NChar(cp);
  }
  return NSet(__pc_add_set(cx, __pc_set_of(cx, [cp, cp], false, true)));
}

// Records a literal CR or LF, after which a search does not skip from a CR
// to its LF.
function __pc_note_crlf(cx: PCtx, cp: i32): void {
  if (cp == 10 || cp == 13) {
    cx.crlf_lit.set(true);
  }
}

function __pc_has_case(cx: PCtx, cp: i32): boolean {
  if (cp < 128 || !cx.fold) {
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

// Of the verbs, only (*ACCEPT) takes a quantifier.
function __pc_repeatable(x: PNode): boolean {
  return match (x) {
    NAssert(k) => false,
    NKeep => false,
    NEmpty => false,
    NFail => false,
    NVerb(_) => false,
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
    return __pc_verb(cx, at, f, depth);
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
  if (c == 67) {
    return __pc_callout(cx, at, f);
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
  if (group <= 0 && n > 0) {
    __pc_fail(cx, end, 115);
    return no;
  }
  return PParse { node: NCall(PCallData { group: group, name: "", at: end }), pos: end + 1, f: f };
}

// A verb such as (*COMMIT) or (*MARK:NAME), or an assertion such as
// (*pla:...), at `at`, which holds `(`. A verb with a name is the verb
// alone, but for (*MARK) and (*SKIP), because no search reads the name.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_verb(cx: PCtx, at: i32, f: i32, depth: i32): PParse {
  let p = cx.p;
  let no = PParse { node: NEmpty, pos: p.len(), f: f };
  if (at + 2 >= p.len() || p[at + 2] == 41u8) {
    __pc_fail(cx, at + 1, 109);
    return no;
  }
  let i = at + 2;
  while (i < p.len() && __pc_name_byte(p[i] as i32)) {
    i += 1;
  }
  if (i - at - 2 > 32) {
    __pc_fail(cx, i, 148);
    return no;
  }
  let name = slice_unchecked(p, at + 2, i) + "";
  if (p[at + 2] >= 97u8 && p[at + 2] <= 122u8) {
    return __pc_alpha(cx, at, name, i, f, depth);
  }
  let kind = match (name) {
    "" | "MARK" => V_MARK,
    "COMMIT" => V_COMMIT,
    "PRUNE" => V_PRUNE,
    "SKIP" => V_SKIP,
    "THEN" => V_THEN,
    "ACCEPT" => 7,
    "F" | "FAIL" => 8,
    _ => 0,
  };
  if (i >= p.len() || p[i] != 58u8 && p[i] != 41u8 || kind == 0) {
    __pc_fail(cx, i, 160);
    return no;
  }
  let pos = i;
  if (p[pos] == 58u8 && pos + 1 < p.len() && p[pos + 1] == 41u8) {
    pos += 1;
  }
  if (kind == V_MARK && p[pos] != 58u8) {
    __pc_fail(cx, pos, 166);
    return no;
  }
  let mark = -1;
  if (p[pos] == 58u8) {
    let close = __memchr(p, 41, pos + 1);
    if (close < 0) {
      __pc_fail(cx, p.len(), 160);
      return no;
    }
    if (close - pos - 1 > 255) {
      __pc_fail(cx, close, 176);
      return no;
    }
    mark = __pc_mark_name(cx, slice_unchecked(p, pos + 1, close) + "");
    pos = close;
  }
  let vkind = if (kind == V_SKIP && mark >= 0) { V_SKIP_ARG } else { kind };
  let node = match (kind) {
    7 => __pc_accept(cx),
    8 => NFail,
    _ => NVerb(PVerbData { kind: vkind, name: if (vkind == V_MARK || vkind == V_SKIP_ARG) { mark } else { -1 }, at: at }),
  };
  return PParse { node: node, pos: pos + 1, f: f };
}

// The number of the name of a mark, the same for the same name.
function __pc_mark_name(cx: PCtx, name: string): i32 {
  let ms = cx.marks.get();
  for k in 0..ms.len() {
    if (ms[k] == name) {
      return k;
    }
  }
  cx.marks.set(ms.append(name));
  return ms.len();
}

// (*ACCEPT), which closes each capture group that it is in, up to the
// look-around that it is in.
function __pc_accept(cx: PCtx): PNode {
  cx.accept.set(true);
  let open = cx.open.get();
  let closes: i32[] = [];
  let k = open.len() - 2;
  while (k >= 0 && open[k + 1] >= cx.looks.get()) {
    closes = closes.append(open[k]);
    k -= 2;
  }
  return NAccept(PAcceptData { closes: closes, in_look: cx.looks.get() > 0 });
}

// An assertion such as (*pla:...) at `at`, whose name ends at `i`. The
// script runs, such as (*sr:...), need the Script_Extensions property, and
// give the error of a name that is not known.
function __pc_alpha(cx: PCtx, at: i32, name: string, i: i32, f: i32, depth: i32): PParse {
  let kind = __pc_alpha_kind(name);
  if (i >= cx.p.len() || cx.p[i] != 58u8 || kind < 0) {
    __pc_fail(cx, i, 195);
    return PParse { node: NEmpty, pos: cx.p.len(), f: f };
  }
  if (kind == ATOMIC) {
    let b = __pc_body(cx, i + 1, f, depth, -1);
    return PParse { ...b, node: NAtomic(PAtomicData { node: b.node }) };
  }
  return __pc_look(cx, at, i + 1, f, depth, kind);
}

// The kind of the group of an alphabetic assertion name, or -1.
// One arm for each name of PCRE2.
// fern-lint: allow cyclomatic-complexity
function __pc_alpha_kind(name: string): i32 {
  return match (name) {
    "pla" | "positive_lookahead" => LOOK_AHEAD,
    "nla" | "negative_lookahead" => LOOK_NOT_AHEAD,
    "plb" | "positive_lookbehind" => LOOK_BEHIND,
    "nlb" | "negative_lookbehind" => LOOK_NOT_BEHIND,
    "napla" | "non_atomic_positive_lookahead" => LOOK_AHEAD_NA,
    "naplb" | "non_atomic_positive_lookbehind" => LOOK_BEHIND_NA,
    "atomic" => ATOMIC,
    _ => -1,
  };
}

// A callout such as (?C1) or (?C"text") at `at`, which holds `(`. With no
// callout function, as in ripgrep, a callout matches the empty string.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_callout(cx: PCtx, at: i32, f: i32): PParse {
  let p = cx.p;
  let no = PParse { node: NEmpty, pos: p.len(), f: f };
  let i = at + 3;
  if (i >= p.len()) {
    __pc_fail(cx, i, 114);
    return no;
  }
  let c = p[i] as i32;
  if (c >= 48 && c <= 57) {
    let n = 0;
    while (i < p.len() && p[i] >= 48u8 && p[i] <= 57u8) {
      n = n * 10 + (p[i] as i32 - 48);
      i += 1;
      if (n > 255) {
        __pc_fail(cx, i, 138);
        return no;
      }
    }
  } else if (__memchr("`'\"^%#${", c, 0) >= 0) {
    let stop = if (c == 123) { 125 } else { c };
    let j = i + 1;
    while (true) {
      let k = __memchr(p, stop, j);
      if (k < 0) {
        __pc_fail(cx, i, 181);
        return no;
      }
      if (k + 1 < p.len() && p[k + 1] as i32 == stop) {
        j = k + 2;
        continue;
      }
      i = k + 1;
      break;
    }
  } else if (c != 41) {
    __pc_fail(cx, i, 182);
    return no;
  }
  if (i >= p.len() || p[i] != 41u8) {
    __pc_fail(cx, i, 139);
    return no;
  }
  return PParse { node: NEmpty, pos: i + 1, f: f };
}

// The body of a group from `at` and its `)`. A group number of -1 does not
// capture.
function __pc_body(cx: PCtx, at: i32, f: i32, depth: i32, idx: i32): PParse {
  let open = cx.open.get();
  if (idx > 0) {
    cx.open.set(open.append(idx).append(cx.looks.get()));
  }
  let r = __pc_alt(cx, at, f, depth + 1);
  cx.open.set(open);
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
  let ends: i32[] = [];
  let pos = at;
  let f = f0;
  while (true) {
    cx.groups.set(base);
    let s = __pc_seq(cx, pos, f, depth + 1);
    branches = branches.append(s.node);
    top = if (cx.groups.get() > top) { cx.groups.get() } else { top };
    pos = s.pos;
    ends = ends.append(pos);
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
  let node = if (branches.len() == 1) { branches[0] } else { NAlt(PAltData { items: branches, ends: ends }) };
  return PParse { node: NGroup(PGroupData { idx: -1, node: node }), pos: pos + 1, f: f0 };
}

// A look-around whose group starts at `at`, with its body at `body`.
function __pc_look(cx: PCtx, at: i32, body: i32, f: i32, depth: i32, kind: i32): PParse {
  cx.looks.set(cx.looks.get() + 1);
  let b = __pc_body(cx, body, f, depth, -1);
  cx.looks.set(cx.looks.get() - 1);
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
// The offset of a condition after a callout at `at`, as in (?(?C1)(?=a)b),
// or `at` with no callout. It is -1 after an error.
function __pc_cond_callout(cx: PCtx, at: i32, f: i32): i32 {
  let p = cx.p;
  if (at + 1 >= p.len() || p[at] != 63u8 || p[at + 1] != 67u8) {
    return at;
  }
  let c = __pc_callout(cx, at - 1, f);
  if (__pc_failed(cx)) {
    return -1;
  }
  if (c.pos + 1 >= p.len() || p[c.pos] != 40u8 || p[c.pos + 1] != 63u8) {
    __pc_fail(cx, c.pos, 128);
    return -1;
  }
  return c.pos + 1;
}

function __pc_cond(cx: PCtx, at: i32, f: i32, depth: i32): PParse {
  let p = cx.p;
  let group = -1;
  let name = "";
  let look: Option[PLookData] = None;
  let pos = at;
  let lat = __pc_cond_callout(cx, at, f);
  if (lat < 0) {
    return PParse { node: NEmpty, pos: p.len(), f: f };
  }
  if (lat < p.len() && p[lat] == 63u8) {
    let l = __pc_group(cx, lat - 1, f, depth);
    if (__pc_failed(cx)) {
      return l;
    }
    let NLook(ld) = l.node else {
      __pc_fail(cx, lat + 1, 128);
      return PParse { node: NEmpty, pos: p.len(), f: f };
    };
    look = Some(ld);
    pos = l.pos;
  } else {
    let (g, nm, end, ref_at) = __pc_cond_ref(cx, at);
    if (end < 0) {
      return PParse { node: NEmpty, pos: p.len(), f: f };
    }
    group = g;
    name = nm;
    pos = end;
    at = ref_at;
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
  let many = -1;
  if let NAlt(a) = r.node {
    many = if (a.items.len() > 2) { r.pos } else { -1 };
    yes = a.items[0];
    no = a.items[1];
  }
  return PParse { node: NCond(PCondData { group: group, name: name, look: look, yes: yes, no: no, at: at, many: many }), pos: r.pos + 1, f: f };
}

// The reference of a condition at `at`: a number, <name>, 'name', R, Rn,
// R&name or a bare name. The result is the group or a COND_ value, the
// name, the offset after its `)`, and the offset of an error of the
// reference.
// Each branch reads one form of the syntax of PCRE2, in the order that PCRE2 checks them.
// fern-lint: allow cyclomatic-complexity
function __pc_cond_ref(cx: PCtx, at: i32): (i32, string, i32, i32) {
  let p = cx.p;
  if (at < p.len() && (p[at] == 60u8 || p[at] == 39u8)) {
    let (name, end) = __pc_name(cx, at + 1, if (p[at] == 60u8) { 62 } else { 39 });
    if (end < 0) {
      return (-1, "", -1, 0);
    }
    if (end + 1 >= p.len() || p[end + 1] != 41u8) {
      __pc_fail(cx, end + 1, 124);
      return (-1, "", -1, 0);
    }
    return (-1, name, end + 2, at + 1);
  }
  if (at + 1 < p.len() && p[at] == 82u8 && p[at + 1] == 41u8) {
    return (COND_RECURSE, "", at + 2, at);
  }
  if (at + 1 < p.len() && p[at] == 82u8 && p[at + 1] == 38u8) {
    let (name, end) = __pc_name(cx, at + 2, 41);
    if (end < 0) {
      return (-1, "", -1, 0);
    }
    return (COND_RECURSE_NAME, name, end + 1, at + 2);
  }
  if (at + 1 < p.len() && p[at] == 82u8 && p[at + 1] >= 48u8 && p[at + 1] <= 57u8) {
    let (n, i, big) = __pc_number(p, at + 1);
    if (i < p.len() && p[i] == 41u8) {
      return (COND_RECURSE_N - n, "", i + 1, at);
    }
  }
  if (at + 6 < p.len() && slice_unchecked(p, at, at + 7) == "DEFINE)") {
    return (COND_DEFINE, "", at + 7, at);
  }
  let (n, i, big) = __pc_number(p, at);
  if (i > at) {
    if (i >= p.len() || p[i] != 41u8) {
      __pc_fail(cx, i, 124);
      return (-1, "", -1, 0);
    }
    if (n == 0) {
      __pc_fail(cx, at - 1, 115);
      return (-1, "", -1, 0);
    }
    return (n, "", i + 1, at - 1);
  }
  let (name, end) = __pc_name(cx, at, 41);
  if (end < 0) {
    return (-1, "", -1, 0);
  }
  return (-1, name, end + 1, at);
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
    if (group <= 0 && n > 0) {
      __pc_fail(cx, end, 115);
      return no;
    }
    return PParse { node: NCall(PCallData { group: group, name: "", at: end }), pos: end + 1, f: f };
  }
  let (name, nend) = __pc_name(cx, j, stop);
  if (nend < 0) {
    return no;
  }
  return PParse { node: NCall(PCallData { group: -1, name: name, at: j }), pos: nend + 1, f: f };
}

// \R: a newline sequence, \r\n first. After (*BSR_ANYCRLF), only CR and
// LF are newlines.
function __pc_newline_seq(cx: PCtx): PNode {
  let rs = if (cx.bsr_crlf) { [10, 10, 13, 13] } else { [10, 13, 133, 133, 8232, 8233] };
  let vs = __pc_add_set(cx, __pc_set_of(cx, rs, false, false));
  let none: i32[] = [];
  return NAtomic(PAtomicData { node: NAlt(PAltData { items: [NSeq([NChar(13), NChar(10)]), NSet(vs)], ends: none }) });
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
    "d" | "D" => if (cx.ucp) { __pc_prop("Nd") } else { [48, 57] },
    "s" | "S" => if (cx.ucp) { __pc_property_ranges("Xsp") } else { [9, 13, 32, 32] },
    "w" | "W" => if (cx.ucp) { __pc_property_ranges("Xwd") } else { [48, 57, 65, 90, 95, 95, 97, 122] },
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
        __pc_note_crlf(cx, cp);
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
      __pc_note_crlf(cx, lo.cp);
      __pc_note_crlf(cx, hi.cp);
      ranges = ranges.append(lo.cp).append(hi.cp);
      i = hi.end;
      continue;
    }
    __pc_note_crlf(cx, lo.cp);
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
  if (cx.ucp) {
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
  let rs = if (fold) { if (cx.fold) { __pc_fold_ranges(rs0) } else { __pc_ascii_fold(rs0) } } else { __pc_normal(rs0) };
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

The check after the parse finds the errors that need the whole pattern: a reference to a group that does not exist, a look-behind that has no fixed length, and a condition with more than two branches. The compiler then writes the program, and finds the bytes that can start a match and the least length of a match.

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
    NAlt(a) => {
      for y in a.items {
        __pc_check(cx, y, in_look);
      }
    },
    NGroup(g) => {
      __pc_check(cx, g.node, in_look);
    },
    NRep(r) => {
      __pc_check(cx, r.node, in_look);
    },
    NAtomic(a) => {
      __pc_check(cx, a.node, in_look);
    },
    NLook(l) => {
      __pc_check(cx, l.node, true);
      if (__pc_is_behind(l.kind)) {
        __pc_check_behind(cx, l);
      }
    },
    NBack(b) => {
      if (__pc_ref_group(cx, b.group, b.name) < 0) {
        __pc_fail(cx, b.at, 115);
      }
    },
    NCond(c) => {
      if (c.look.is_none() && __pc_cond_group(cx, c) == -1) {
        __pc_fail(cx, c.at, 115);
      }
      if let Some(l) = c.look {
        __pc_check(cx, NLook(l), in_look);
      }
      __pc_check(cx, c.yes, in_look);
      __pc_check(cx, c.no, in_look);
      if (c.many >= 0) {
        __pc_fail(cx, c.many, 127);
      }
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
    _ => {}
  }
}

function __pc_is_behind(kind: i32): boolean {
  return kind == LOOK_BEHIND || kind == LOOK_NOT_BEHIND || kind == LOOK_BEHIND_NA;
}

// The branches of an alternation, or the node as one branch.
function __pc_branches(x: PNode): PNode[] {
  return match (x) {
    NAlt(a) => a.items,
    _ => [x],
  };
}

// The operand of OP_COND for a condition that is not a look-around, or -1
// when its group does not exist.
function __pc_cond_group(cx: PCtx, c: PCondData): i32 {
  if (c.group == COND_RECURSE || c.group == COND_DEFINE) {
    return c.group;
  }
  if (c.group <= COND_RECURSE_N) {
    let n = COND_RECURSE_N - c.group;
    return if (n >= 1 && n <= cx.groups.get()) { c.group } else { -1 };
  }
  let n = __pc_ref_group(cx, if (c.group == COND_RECURSE_NAME) { -1 } else { c.group }, c.name);
  return if (n < 0) { -1 } else if (c.group == COND_RECURSE_NAME) { COND_RECURSE_N - n } else { n };
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
  for b in __pc_branches(l.node) {
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
    NAlt(a) => {
      let n = __pc_length(cx, a.items[0]);
      for y in a.items {
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

// The end of the body of a called group: the captures come back, and the
// choices of the body stay.
const OP_RET: i32 = 18;

// The end of a call of the whole pattern, which drops the choices of the
// call, as PCRE2 does.
const OP_RET_ATOMIC: i32 = 19;

const OP_VERB: i32 = 20;

// A branch of an alternation that (*THEN) can end. Its choice holds the
// next branch.
const OP_BRANCH: i32 = 21;

// The start of a branch of a look-behind: back by a count of characters.
const OP_BACKUP: i32 = 22;

// The end of the body of a non-atomic look-around: back to the offset in a
// loop register.
const OP_RESTORE: i32 = 23;

// The end of the body of a non-atomic look-behind: the offset must be the
// one in a loop register.
const OP_AT_REG: i32 = 24;

// The program under construction: the instructions, the searches of their
// own, the next free loop register, the rows of `alts`, and the context.
struct PGen { code: i32[], subs: i32[], regs: i32, alts: i32[], cx: PCtx }

function __pc_emit(own g: PGen, op: i32, a: i32, b: i32): PGen {
  return PGen { ...g, code: g.code.append(op).append(a).append(b) };
}

function __pc_here(g: PGen): i32 {
  return g.code.len() / 3;
}

function __pc_patch(own g: PGen, at: i32, slot: i32, v: i32): PGen {
  return PGen { ...g, code: g.code.with(3 * at + slot, v) };
}

// The code of each group that the pattern calls, after the code of the
// pattern, and the start of the code of each group, or -1. Group 0 is the
// whole pattern.
function __pc_gen_calls(own g: PGen, cx: PCtx, root: PNode): (PGen, i32[]) {
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
    g = match (body) {
      NAlt(a) => __pc_gen_alt(g, a, n),
      _ => __pc_gen(g, body),
    };
    g = __pc_emit(g, if (n == 0) { OP_RET_ATOMIC } else { OP_RET }, 0, 0);
    todo = __pc_called(cx, body, todo);
  }
  return (g, calls);
}

function __pc_build(cx: PCtx, root: PNode, s: PStart): Result[Pcre, CompileError] {
  let empty: i32[] = [];
  let g = PGen { code: empty, subs: empty, regs: 0, alts: empty, cx: cx };
  g = __pc_emit(g, OP_SAVE, 0, 0);
  g = __pc_gen(g, root);
  g = __pc_emit(__pc_emit(g, OP_SAVE, 1, 0), OP_MATCH, 0, 0);
  let (called, calls) = __pc_gen_calls(g, cx, root);
  g = called;
  if (g.code.len() > 3000000) {
    return Err(CompileError { offset: cx.p.len(), code: 120, message: __pc_message(120) });
  }
  let ngroups = cx.groups.get();
  let none: u8[] = [];
  let first = if (s.no_start) { none } else { __pc_start_bytes(cx, root) };
  return Ok(Pcre {
    code: g.code, subs: g.subs, calls: calls, sets: cx.sets.get(), ngroups: ngroups, names: cx.names.get(), name_groups: cx.name_groups.get(),
    nslots: 2 * (ngroups + 1) + g.regs, utf: s.utf, fold: s.utf || s.ucp, nl: s.nl, anchored: __pc_anchored(root), first: first,
    req: if (s.no_start || cx.accept.get()) { -1 } else { __pc_req(root) },
    minlen: if (s.no_start || cx.accept.get() || !__pc_takes_char(root)) { 0 } else { __pc_min_len(cx, root) },
    word: __pc_set_of(cx, if (s.ucp) { __pc_property_ranges("Xwd") } else { [48, 57, 65, 90, 95, 95, 97, 122] }, false, false),
    limit: s.limit, depth_limit: s.depth, heap_limit: s.heap, notempty: s.notempty, has_then: cx.has_then, crlf_lit: cx.crlf_lit.get(),
    alts: g.alts, verbs: __pc_has_op(g.code, OP_VERB),
  });
}

// Whether the program has an instruction of `op`.
function __pc_has_op(code: i32[], op: i32): boolean {
  let k = 0;
  while (k < code.len()) {
    if (code[k] == op) {
      return true;
    }
    k += 3;
  }
  return false;
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
    NAlt(a) => {
      let o = out;
      for y in a.items {
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
    NAlt(a) => {
      return __pc_gen_alt(g, a, -2);
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
      return __pc_gen_sub(g, ATOMIC, a.node);
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
    NAccept(a) => {
      for k in a.closes {
        g = __pc_emit(g, OP_SAVE, 2 * k + 1, 0);
      }
      return __pc_emit(g, OP_ACCEPT, if (a.in_look) { 1 } else { 0 }, 0);
    },
    NVerb(v) => {
      return __pc_emit(g, OP_VERB, v.kind, if (v.kind == V_THEN) { v.at } else { v.name });
    },
    NBackup(n) => {
      return __pc_emit(g, OP_BACKUP, n, 0);
    },
    NEmpty => {
      return g;
    }
  }
  return g;
}

// An alternation: a choice before each branch but the last. When the
// pattern has (*THEN), each branch starts with OP_BRANCH, whose choice
// holds the next branch, so that (*THEN) can find the branch that it is
// in. `rec` is the group of the call whose body this is, or -2.
function __pc_gen_alt(own g: PGen, a: PAltData, rec: i32): PGen {
  let items = a.items;
  if (g.cx.has_then && a.ends.len() == items.len()) {
    let starts: i32[] = [];
    let ends: i32[] = [];
    for k in 0..items.len() {
      starts = starts.append(__pc_here(g));
      let row = g.alts.len() / 2;
      g = PGen { ...g, alts: g.alts.append(a.ends[k]).append(rec) };
      g = __pc_emit(g, OP_BRANCH, -1, row);
      g = __pc_gen(g, items[k]);
      if (k + 1 < items.len()) {
        ends = ends.append(__pc_here(g));
        g = __pc_emit(g, OP_JMP, 0, 0);
      }
    }
    for k in 0..starts.len() - 1 {
      g = __pc_patch(g, starts[k], 1, starts[k + 1]);
    }
    let out = __pc_here(g);
    for j in ends {
      g = __pc_patch(g, j, 1, out);
    }
    return g;
  }
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
    return __pc_gen_sub(g, ATOMIC, NRep(PRepData { ...r, kind: 0 }));
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
// `subs` holds the kind, the start of the body and the next instruction.
function __pc_gen_sub(own g: PGen, kind: i32, body: PNode): PGen {
  let id = g.subs.len() / 3;
  let at = __pc_here(g);
  g = __pc_emit(g, OP_SUB, id, 0);
  let subs = g.subs.append(kind).append(at + 1).append(0);
  g = PGen { ...g, subs: subs };
  g = __pc_gen(g, body);
  g = __pc_emit(g, OP_END, 0, 0);
  let next = __pc_here(g);
  let done = g.subs.with(3 * id + 2, next);
  return PGen { ...g, subs: done };
}

// A look-around. A non-atomic one keeps its choices: a loop register holds
// the offset at its start.
function __pc_gen_look(own g: PGen, l: PLookData): PGen {
  if (l.kind != LOOK_AHEAD_NA && l.kind != LOOK_BEHIND_NA) {
    let body = __pc_look_body(g.cx, l);
    return __pc_gen_sub(g, l.kind, body);
  }
  let behind = l.kind == LOOK_BEHIND_NA;
  let reg = g.regs;
  g = PGen { ...g, regs: reg + 1 };
  g = __pc_emit(g, OP_MARK, reg, 0);
  let body = __pc_look_body(g.cx, PLookData { ...l, kind: if (behind) { LOOK_BEHIND } else { LOOK_AHEAD } });
  g = __pc_gen(g, body);
  if (behind) {
    g = __pc_emit(g, OP_AT_REG, reg, 0);
  }
  return __pc_emit(g, OP_RESTORE, reg, 0);
}

// The body of a look-around. Each branch of a look-behind first moves back
// by its length.
function __pc_look_body(cx: PCtx, l: PLookData): PNode {
  if (!__pc_is_behind(l.kind)) {
    return l.node;
  }
  let items: PNode[] = [];
  for b in __pc_branches(l.node) {
    items = items.append(NSeq([NBackup(__pc_length(cx, b)), b]));
  }
  if (items.len() == 1) {
    return items[0];
  }
  let ends = match (l.node) {
    NAlt(a) => a.ends,
    _ => [],
  };
  return NAlt(PAltData { items: items, ends: ends });
}

// A conditional group: OP_COND tests a group, or a search of its own tests
// a look-around. Each one jumps to the second branch when the condition is
// false.
function __pc_gen_cond(own g: PGen, c: PCondData): PGen {
  let at = __pc_here(g);
  if let Some(l) = c.look {
    let body = __pc_look_body(g.cx, l);
    g = __pc_gen_sub(g, COND + l.kind, body);
  } else {
    let group = __pc_cond_group(g.cx, c);
    g = __pc_emit(g, OP_COND, group, 0);
  }
  g = __pc_gen(g, c.yes);
  let jump = __pc_here(g);
  g = __pc_emit(g, OP_JMP, 0, 0);
  let no = __pc_here(g);
  g = __pc_patch(g, at, 2, no);
  g = __pc_gen(g, c.no);
  let end = __pc_here(g);
  return __pc_patch(g, jump, 1, end);
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
    NAlt(a) => {
      for y in a.items {
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
    NAlt(a) => {
      for y in a.items {
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
    NAlt(a) => {
      let out = m;
      for y in a.items {
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
    NEmpty => {
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

// The marks of the first bytes of the characters of `s`. With UTF-8, a
// character above U+007F marks its lead byte.
function __pc_set_firsts(cx: PCtx, s: PSet, m: u8[]): u8[] {
  let out = m;
  let top = if (cx.utf) { 128 } else { 256 };
  for c in 0..top {
    if ((s.bits[c >> 5] >> (c & 31) & 1) != 0) {
      out = out.with(c, 1u8);
    }
  }
  if (!cx.utf) {
    return out;
  }
  if (s.neg) {
    for c in 194..245 {
      out = out.with(c, 1u8);
    }
    return out;
  }
  for c in 128..256 {
    if ((s.bits[c >> 5] >> (c & 31) & 1) != 0) {
      out = out.with(192 | c >> 6, 1u8);
    }
  }
  for k in 0..s.ranges.len() / 2 {
    for b in __pc_lead_byte(s.ranges[2 * k], true)..__pc_lead_byte(s.ranges[2 * k + 1], true) + 1 {
      out = out.with(b, 1u8);
    }
  }
  return out;
}

// Whether each match of `x` takes a character, as the compiler of PCRE2
// sees it: a call and a back reference take none. PCRE2 has no least
// length for a pattern that can match an empty string.
// One arm for each kind of node.
// fern-lint: allow cyclomatic-complexity
function __pc_takes_char(x: PNode): boolean {
  match (x) {
    NChar(_) => {
      return true;
    },
    NSet(_) => {
      return true;
    },
    NAny(_) => {
      return true;
    },
    NSeq(items) => {
      for y in items {
        if (__pc_takes_char(y)) {
          return true;
        }
      }
      return false;
    },
    NAlt(a) => {
      for y in a.items {
        if (!__pc_takes_char(y)) {
          return false;
        }
      }
      return true;
    },
    NGroup(g) => {
      return __pc_takes_char(g.node);
    },
    NAtomic(a) => {
      return __pc_takes_char(a.node);
    },
    NRep(r) => {
      return r.min > 0 && __pc_takes_char(r.node);
    },
    NCond(c) => {
      return c.group != COND_DEFINE && __pc_takes_char(c.yes) && __pc_takes_char(c.no);
    },
    _ => {
      return false;
    }
  }
  return false;
}

// The least count of characters of a match, as PCRE2 finds it.
function __pc_min_len(cx: PCtx, root: PNode): i32 {
  return __pc_min_branches(cx, __pc_branches(root), [0]);
}

// The least length of the branches of a group. A branch that calls a group
// that `open` holds does not lower it, but for the first branch.
function __pc_min_branches(cx: PCtx, items: PNode[], open: i32[]): i32 {
  let length = -1;
  for y in items {
    let (n, rec) = __pc_min_item(cx, y, open);
    if (length < 0 || !rec && n < length) {
      length = n;
    }
    if (length == 0) {
      return 0;
    }
  }
  return length;
}

// The least length of an item, and whether it calls a group that `open`
// holds, which counts 0.
// One arm for each kind of node.
// fern-lint: allow cyclomatic-complexity
function __pc_min_item(cx: PCtx, x: PNode, open: i32[]): (i32, boolean) {
  match (x) {
    NChar(_) => {
      return (1, false);
    },
    NSet(_) => {
      return (1, false);
    },
    NAny(_) => {
      return (1, false);
    },
    NSeq(items) => {
      let n = 0;
      let rec = false;
      for y in items {
        let (k, r) = __pc_min_item(cx, y, open);
        n = if (n + k > 65535) { 65535 } else { n + k };
        rec = rec || r;
      }
      return (n, rec);
    },
    NAlt(a) => {
      return (__pc_min_branches(cx, a.items, open), false);
    },
    NGroup(g) => {
      return (__pc_min_branches(cx, __pc_branches(g.node), if (g.idx > 0) { open.append(g.idx) } else { open }), false);
    },
    NAtomic(a) => {
      return (__pc_min_branches(cx, __pc_branches(a.node), open), false);
    },
    NRep(r) => {
      let (k, rec) = __pc_min_item(cx, r.node, open);
      return (if (r.min > 0 && k > 65535 / r.min) { 65535 } else { r.min * k }, rec);
    },
    NBack(b) => {
      return __pc_min_group(cx, __pc_ref_group(cx, b.group, b.name), open);
    },
    NCall(c) => {
      return __pc_min_group(cx, if (c.group == 0) { 0 } else { __pc_ref_group(cx, c.group, c.name) }, open);
    },
    NCond(c) => {
      if (c.group == COND_DEFINE) {
        return (0, false);
      }
      return (__pc_min_branches(cx, [c.yes, c.no], open), false);
    },
    _ => {
      return (0, false);
    }
  }
  return (0, false);
}

function __pc_min_group(cx: PCtx, n: i32, open: i32[]): (i32, boolean) {
  if (n < 0) {
    return (0, false);
  }
  if (open.contains(n)) {
    return (0, true);
  }
  return (__pc_min_branches(cx, __pc_branches(__pc_body_of(cx, n)), open.append(n)), false);
}

// The bytes that can start a match, as 256 marks, or [] for each byte.
// When an item such as a verb hides the first bytes, PCRE2 still uses the
// first character of the pattern, if each match starts with it.
function __pc_start_bytes(cx: PCtx, root: PNode): u8[] {
  let none: u8[] = [];
  if (__pc_nullable(root)) {
    return none;
  }
  let marks = __pc_first(cx, root);
  if (marks.len() > 0) {
    return marks;
  }
  let c = __pc_first_cu(cx, root);
  if (c < 0) {
    return none;
  }
  if ((c & FC_CASELESS) != 0) {
    let lower = c & 255;
    return __pc_zero_marks().with(lower, 1u8).with(lower - 32, 1u8);
  }
  return __pc_zero_marks().with(__pc_lead_byte(c, cx.utf), 1u8);
}

// The first character of each match, as PCRE2 finds it: FC_UNSET when no
// item gives one yet, FC_NONE when there is none, or the character. An
// ASCII letter with its other case has FC_CASELESS.
const FC_UNSET: i32 = -1;

const FC_NONE: i32 = -2;

const FC_CASELESS: i32 = 16777216;

// One arm for each kind of node.
// fern-lint: allow cyclomatic-complexity
function __pc_first_cu(cx: PCtx, x: PNode): i32 {
  match (x) {
    NChar(c) => {
      return c;
    },
    NSet(s) => {
      return __pc_set_cu(cx, cx.sets.get()[s]);
    },
    NSeq(items) => {
      for y in items {
        let r = __pc_first_cu(cx, y);
        if (r != FC_UNSET) {
          return r;
        }
      }
      return FC_UNSET;
    },
    NAlt(a) => {
      let r = __pc_first_cu(cx, a.items[0]);
      for y in a.items {
        if (__pc_first_cu(cx, y) != r) {
          return FC_NONE;
        }
      }
      return r;
    },
    NGroup(g) => {
      return __pc_first_cu(cx, g.node);
    },
    NAtomic(a) => {
      return __pc_first_cu(cx, a.node);
    },
    NRep(r) => {
      let c = __pc_first_cu(cx, r.node);
      return if (r.min > 0 || c == FC_UNSET) { c } else { FC_NONE };
    },
    NLook(_) => {
      return FC_UNSET;
    },
    NAssert(k) => {
      return if (k == A_MBOL) { FC_NONE } else { FC_UNSET };
    },
    NKeep => {
      return FC_UNSET;
    },
    NVerb(_) => {
      return FC_UNSET;
    },
    NFail => {
      return FC_UNSET;
    },
    NEmpty => {
      return FC_UNSET;
    },
    _ => {
      return FC_NONE;
    }
  }
  return FC_NONE;
}

// The one character of a set, or an ASCII letter and its other case, or
// FC_NONE. With the classes of Unicode, K and S have a third case.
function __pc_set_cu(cx: PCtx, s: PSet): i32 {
  if (s.neg || s.ranges.len() > 0) {
    return FC_NONE;
  }
  let found = __pc_set_members(s, 3);
  if (found.len() == 1) {
    return found[0];
  }
  if (found.len() == 2 && __pc_case_pair(cx, found[0], found[1])) {
    return found[1] | FC_CASELESS;
  }
  return FC_NONE;
}

// The first `most` bytes of the set `s`.
function __pc_set_members(s: PSet, most: i32): i32[] {
  let found: i32[] = [];
  for c in 0..256 {
    if ((s.bits[c >> 5] >> (c & 31) & 1) != 0 && found.len() < most) {
      found = found.append(c);
    }
  }
  return found;
}

// Whether `upper` and `lower` are the two cases of an ASCII letter that is
// the first code unit of PCRE2 without its case. With Unicode case folding,
// k and s have other cases too.
function __pc_case_pair(cx: PCtx, upper: i32, lower: i32): boolean {
  return upper == lower - 32 && lower >= 97 && lower <= 122 && !(cx.fold && (lower == 107 || lower == 115));
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

`__pc_run` runs the program from an instruction at an offset. Its stack holds three words for each entry. A choice holds the instruction and the offset to go back to. An entry of a group or of a loop register holds the old value to put back. Other entries record a call, the end of a call, a branch that (*THEN) can end, a look-ahead that matched, and a verb. The count of the choices is the count of the match limit, and the searches of their own add their choices to it.

```fern
<<matcher>>=
// The state of a search: the text, the start of the search for \G, and
// the count of the steps of the attempt, or minus the error.
struct PRun { re: Pcre, text: string, start: i32, steps: Cell[i32] }

// For a pattern with verbs, the first VS_WORDS words of the frames hold
// what a verb passes back: the count of the (*SKIP:NAME) verbs that the
// attempt passed and of those that it ignores, the offset of (*SKIP), the
// name of (*SKIP:NAME), the offset of (*THEN) in the pattern, and the group
// of the call that the verb was in, or -1.
const VS_WORDS: i32 = 6;

const VS_SKIPS: i32 = 0;

const VS_IGNORE: i32 = 1;

const VS_POS: i32 = 2;

const VS_NAME: i32 = 3;

const VS_THEN: i32 = 4;

const VS_REC: i32 = 5;

// The results of a run. A verb gives its kind plus 2.
const R_NOMATCH: i32 = 0;

const R_MATCH: i32 = 1;

const R_ACCEPT: i32 = 2;

const R_COMMIT: i32 = 3;

const R_SKIP: i32 = 5;

const R_SKIP_ARG: i32 = 6;

const R_THEN: i32 = 7;

const R_ERROR: i32 = 8;

// The entries of the stack that are not choices: the old value of a slot,
// a call and a return with the frame before them, a branch, a look-ahead
// that matched, and a verb.
const E_UNDO: i32 = -1;

const E_CALL: i32 = -2;

const E_RET: i32 = -3;

const E_BRANCH: i32 = -4;

const E_LOOK: i32 = -5;

const E_VERB: i32 = -6;

// The errors of a run.
const ERR_LIMIT: i32 = 1;

const ERR_LOOP: i32 = 2;

const ERR_DEPTH: i32 = 3;

const ERR_HEAP: i32 = 4;

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
// Each attempt has the match limit, and a verb can move the next attempt
// or stop the search.
// fern-lint: allow cyclomatic-complexity
pub function (re: Pcre) captures_at(text: string, start: i32): Result[Option[i32[]], MatchError] {
  let n = text.len();
  if (re.req >= 0 && __memchr(text, re.req, if (start < 0) { 0 } else { start }) < 0) {
    return Ok(None);
  }
  if (re.heap_limit >= 0 && re.heap_limit as i64 * 1024i64 < __pc_frame_size(re) as i64) {
    return Err(HeapLimit);
  }
  let at = if (start < 0) { 0 } else { start };
  if (re.utf) {
    while (at < n && (text[at] as i32 & 192) == 128) {
      at += 1;
    }
  }
  let origin = at;
  let run = PRun { re: re, text: text, start: origin, steps: cell_new(0) };
  let none: i32[] = [];
  let frames = if (re.verbs) { [0, 0, 0, 0, 0, -1] } else { none };
  let base = frames.len();
  while (at <= n) {
    if (re.first.len() > 0) {
      at = __scan_set(text, at, re.first);
      if (at >= n) {
        return Ok(None);
      }
    }
    if (n - at < re.minlen) {
      return Ok(None);
    }
    run.steps.set(2);
    if (re.verbs) {
      frames = frames.with(VS_SKIPS, 0);
    }
    if (re.limit < 2) {
      return Err(MatchLimit);
    }
    let (rc, end, out, fs) = __pc_run(run, 0, at, __pc_slots(re.nslots), -1, frames, -1, base, 0, -1);
    frames = fs;
    if (rc == R_ERROR) {
      return Err(__pc_match_error(-run.steps.get()));
    }
    if (rc == R_MATCH) {
      return Ok(Some(out.take(2 * (re.ngroups + 1))));
    }
    if (rc == R_ACCEPT) {
      return Ok(Some(out.take(2 * (re.ngroups + 1)).with(1, end)));
    }
    if (rc == R_COMMIT) {
      return Ok(None);
    }
    let next = at;
    if (rc == R_SKIP_ARG) {
      frames = frames.with(VS_IGNORE, frames[VS_SKIPS]);
    } else if (rc == R_SKIP && frames[VS_POS] > at) {
      next = frames[VS_POS];
    } else {
      if (re.verbs) {
        frames = frames.with(VS_IGNORE, 0);
      }
      next = __pc_next_start(re, text, at);
    }
    if (re.anchored || next > n) {
      return Ok(None);
    }
    if (next > origin && next < n && text[next - 1] == 13u8 && text[next] == 10u8 && !re.crlf_lit && (re.nl == NL_ANYCRLF || re.nl == NL_ANY || re.nl == NL_CRLF)) {
      next += 1;
    }
    at = next;
  }
  return Ok(None);
}

function __pc_match_error(code: i32): MatchError {
  return match (code) {
    2 => RecurseLoop,
    3 => DepthLimit,
    4 => HeapLimit,
    _ => MatchLimit,
  };
}

// The bytes of a frame of PCRE2 10.42 on a 64-bit host, for the heap limit.
function __pc_frame_size(re: Pcre): i32 {
  return 128 + 16 * re.ngroups;
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

// A step that adds a choice: false, with the error, when the attempt goes
// above the match limit, or above the depth or the heap limit of the
// pattern. The count of the entries of the stack is the depth.
function __pc_step(r: PRun, sp: i32): boolean {
  let n = r.steps.get() + 1;
  r.steps.set(n);
  return __pc_limits(r, sp, n);
}

// Whether `n` steps and a stack of `sp` words are within the limits, or
// false with the error.
function __pc_limits(r: PRun, sp: i32, n: i32): boolean {
  let re = r.re;
  let err = if (n > re.limit) {
    ERR_LIMIT
  } else if (re.depth_limit >= 0 && sp / 3 >= re.depth_limit) {
    ERR_DEPTH
  } else if (re.heap_limit >= 0 && (sp / 3 + 1) as i64 * (__pc_frame_size(re) as i64) > re.heap_limit as i64 * 1024i64) {
    ERR_HEAP
  } else {
    0
  };
  if (err > 0) {
    r.steps.set(-err);
  }
  return err == 0;
}

// Whether a match that ends at `pos` can be empty, under (*NOTEMPTY) and
// (*NOTEMPTY_ATSTART).
function __pc_empty_ok(r: PRun, slots: i32[], pos: i32): boolean {
  return r.re.notempty == 0 || pos != slots[0] || r.re.notempty == 2 && slots[0] != r.start;
}

// Writes `v` at `at` of `a`, which is at most one word longer after it.
function __pc_put(own a: i32[], at: i32, v: i32): i32[] {
  if (at < a.len()) {
    return a.with(at, v);
  }
  return a.append(v);
}

// A frame of a call at `at` of `frames`: the group, the instruction after
// the call, the frame of the caller, the offset of the call, the height of
// the stack before the call, and the slots at the call.
function __pc_frame(own frames: i32[], at: i32, group: i32, ret: i32, parent: i32, pos: i32, sp: i32, slots: i32[]): i32[] {
  frames = __pc_put(frames, at, group);
  frames = __pc_put(frames, at + 1, ret);
  frames = __pc_put(frames, at + 2, parent);
  frames = __pc_put(frames, at + 3, pos);
  frames = __pc_put(frames, at + 4, sp);
  for k in 0..slots.len() {
    frames = __pc_put(frames, at + 5 + k, slots[k]);
  }
  return frames;
}

// The slots that the frame at `fr` holds.
function __pc_frame_slots(frames: i32[], fr: i32, n: i32): i32[] {
  let out: i32[] = [];
  for k in 0..n {
    out = out.append(frames[fr + 5 + k]);
  }
  return out;
}

// Runs the program from `pc0` at `pos0`. A look-behind gives `target`, the
// offset where its body must end. `frames` holds the frames of the calls,
// `fr` is the frame of the current call or -1, and `ftop` the end of the
// frames in use. `depth` counts the searches of their own around this one,
// and `base` is the frame of the call when this search started. The result
// is the kind of the end of the run, the offset
// at the end, the slots, and the frames. A choice holds the instruction
// and the offset to go back to. A verb that the search backtracks onto
// sets `mode`, and the stack then carries its effect back: each entry acts
// as PCRE2 acts for the verb at the same point.
// One branch for each opcode, in one loop, so that a step calls no function.
// fern-lint: allow cyclomatic-complexity
function __pc_run(r: PRun, pc0: i32, pos0: i32, slots0: i32[], target: i32, frames0: i32[], fr0: i32, ftop0: i32, depth: i32, base: i32): (i32, i32, i32[], i32[]) {
  let re = r.re;
  let code = re.code;
  let text = r.text;
  let slots = slots0;
  let frames = frames0;
  let fr = fr0;
  let ftop = ftop0;
  let st: i32[] = [];
  let sp = 0;
  let pc = pc0;
  let pos = pos0;
  let regs = 2 * (re.ngroups + 1);
  let mode = R_NOMATCH;
  while (true) {
    let op = code[3 * pc];
    let a = code[3 * pc + 1];
    let ok = true;
    let leave = false;
    if (op == OP_CHAR) {
      let (c, n) = __pc_char_at(re, text, pos);
      ok = c == a;
      pos += n;
      pc += 1;
    } else if (op == OP_SET) {
      let (c, n) = __pc_char_at(re, text, pos);
      ok = c >= 0 && __pc_in_set(re.sets[a], c);
      pos += n;
      pc += 1;
    } else if (op == OP_ANY) {
      let (c, n) = __pc_char_at(re, text, pos);
      ok = c >= 0 && (a == 1 || (if (re.nl == NL_LF) { c != 10 } else { __pc_nl_at(re, text, pos) == 0 }));
      pos += n;
      pc += 1;
    } else if (op == OP_SPLIT) {
      st = __pc_push(st, sp, code[3 * pc + 2], pos, -1);
      sp += 3;
      let n = r.steps.get() + 1;
      r.steps.set(n);
      if ((n > re.limit || re.depth_limit >= 0 || re.heap_limit >= 0) && !__pc_limits(r, sp, n)) {
        return (R_ERROR, pos, slots, frames);
      }
      pc = a;
    } else if (op == OP_JMP) {
      pc = a;
    } else if (op == OP_SAVE || op == OP_MARK) {
      let slot = if (op == OP_SAVE) { a } else { regs + a };
      st = __pc_push(st, sp, E_UNDO, slot, slots[slot]);
      sp += 3;
      slots = slots.with(slot, pos);
      pc += 1;
    } else if (op == OP_CHECK) {
      pc = if (slots[regs + a] == pos) { code[3 * pc + 2] } else { pc + 1 };
    } else if (op == OP_ASSERT) {
      ok = __pc_assert(r, a, pos);
      pc += 1;
    } else if (op == OP_BACK) {
      let end = __pc_back(r, slots, a, code[3 * pc + 2] == 1, pos);
      ok = end >= 0;
      pos = end;
      pc += 1;
    } else if (op == OP_SUB) {
      let kind = re.subs[3 * a];
      let (res, spos, sslots, sframes) = __pc_sub(r, a, pos, slots, frames, fr, ftop, depth);
      frames = sframes;
      if (res == R_ERROR) {
        return (R_ERROR, pos, slots, frames);
      }
      if (res == R_ACCEPT && (fr < 0 || fr == base)) {
        return (R_ACCEPT, spos, sslots, frames);
      }
      if (res == R_ACCEPT) {
        pos = spos;
        slots = slots.with(0, sslots[0]);
        leave = true;
      } else if (res >= R_COMMIT) {
        mode = res;
        ok = false;
      } else if (res == 1 || kind > COND) {
        let (next, top) = __pc_took(st, sp, slots, sslots, res == 1 && re.has_then && (kind == LOOK_AHEAD || kind == LOOK_BEHIND));
        st = next;
        sp = top;
        slots = sslots;
        pos = spos;
        pc = if (res == 0) { code[3 * pc + 2] } else { re.subs[3 * a + 2] };
      } else {
        ok = false;
      }
    } else if (op == OP_KEEP) {
      st = __pc_push(st, sp, E_UNDO, 0, slots[0]);
      sp += 3;
      slots = slots.with(0, pos);
      pc += 1;
    } else if (op == OP_COND) {
      pc = if (__pc_holds(frames, fr, slots, a)) { pc + 1 } else { code[3 * pc + 2] };
    } else if (op == OP_CALL) {
      if (!__pc_step(r, sp)) {
        return (R_ERROR, pos, slots, frames);
      }
      if (__pc_loops(frames, fr, a, pos)) {
        r.steps.set(-ERR_LOOP);
        return (R_ERROR, pos, slots, frames);
      }
      frames = __pc_frame(frames, ftop, a, pc + 1, fr, pos, sp, slots);
      st = __pc_push(st, sp, E_CALL, fr, ftop);
      sp += 3;
      fr = ftop;
      ftop += 5 + re.nslots;
      pc = re.calls[a];
    } else if (op == OP_RET) {
      let (next, top, kept) = __pc_returned(st, sp, slots, frames, fr, re.nslots);
      st = next;
      sp = top;
      slots = kept;
      st = __pc_push(st, sp, E_RET, fr, 0);
      sp += 3;
      pc = frames[fr + 1];
      fr = frames[fr + 2];
    } else if (op == OP_RET_ATOMIC) {
      leave = true;
    } else if (op == OP_VERB) {
      let (next, top, f) = __pc_verb_passed(st, sp, frames, a, pc, pos);
      st = next;
      sp = top;
      frames = f;
      pc += 1;
    } else if (op == OP_BRANCH) {
      st = __pc_push(st, sp, E_BRANCH, pc, pos);
      sp += 3;
      if (a >= 0 && !__pc_step(r, sp)) {
        return (R_ERROR, pos, slots, frames);
      }
      pc += 1;
    } else if (op == OP_BACKUP) {
      pos = __pc_back_chars(re, text, pos, a);
      ok = pos >= 0;
      pc += 1;
    } else if (op == OP_RESTORE) {
      pos = slots[regs + a];
      pc += 1;
    } else if (op == OP_AT_REG) {
      ok = pos == slots[regs + a];
      pc += 1;
    } else if (op == OP_FAIL) {
      ok = false;
    } else if (op == OP_ACCEPT) {
      if (a == 1 || fr >= 0 && fr == base) {
        return (R_ACCEPT, pos, slots, frames);
      }
      if (fr >= 0) {
        leave = true;
      } else if (__pc_empty_ok(r, slots, pos)) {
        return (R_ACCEPT, pos, slots, frames);
      } else {
        ok = false;
      }
    } else if (op == OP_MATCH) {
      if (__pc_empty_ok(r, slots, pos)) {
        return (R_MATCH, pos, slots, frames);
      }
      ok = false;
    } else {
      if (target < 0 || pos == target) {
        return (R_MATCH, pos, slots, frames);
      }
      ok = false;
    }
    if (leave) {
      // The end of a call that drops its choices: (*ACCEPT), or the end of
      // a call of the whole pattern. The captures come back, but for \K.
      let s0 = slots[0];
      slots = __pc_frame_slots(frames, fr, re.nslots);
      sp = frames[fr + 4];
      pc = frames[fr + 1];
      ftop = fr;
      fr = frames[fr + 2];
      if (slots[0] != s0) {
        st = __pc_push(st, sp, E_UNDO, 0, slots[0]);
        sp += 3;
        slots = slots.with(0, s0);
      }
    } else if (!ok) {
      while (sp > 0 && st[sp - 3] == E_UNDO) {
        sp -= 3;
        slots = slots.with(st[sp + 1], st[sp + 2]);
      }
      if (sp == 0) {
        return (mode, pos, slots, frames);
      }
      if (mode == R_NOMATCH && st[sp - 3] >= 0) {
        sp -= 3;
        pc = st[sp];
        pos = st[sp + 1];
      } else {
        let u = __pc_unwind(re, PBack { st: st, sp: sp, slots: slots, frames: frames, fr: fr, ftop: ftop, mode: mode, pc: pc, pos: pos, back: false });
        if (!u.back) {
          return (u.mode, pos, u.slots, u.frames);
        }
        st = u.st;
        sp = u.sp;
        slots = u.slots;
        frames = u.frames;
        fr = u.fr;
        ftop = u.ftop;
        mode = u.mode;
        pc = u.pc;
        pos = u.pos;
      }
    }
  }
  return (R_NOMATCH, pos, slots, frames);
}

// Whether the condition `a` of a conditional group holds: a call, a call of
// a group, or a group that has matched.
function __pc_holds(frames: i32[], fr: i32, slots: i32[], a: i32): boolean {
  if (a == COND_RECURSE) {
    return fr >= 0;
  }
  if (a <= COND_RECURSE_N) {
    return fr >= 0 && frames[fr] == COND_RECURSE_N - a;
  }
  return a >= 0 && slots[2 * a + 1] >= 0;
}

// The stack and the verb state after the run passes the verb `kind` at
// `pc`. A (*SKIP:NAME) that the rerun of the search ignores leaves no entry.
function __pc_verb_passed(own st: i32[], sp: i32, own frames: i32[], kind: i32, pc: i32, pos: i32): (i32[], i32, i32[]) {
  if (kind == V_SKIP_ARG) {
    frames = frames.with(VS_SKIPS, frames[VS_SKIPS] + 1);
  }
  if (kind != V_SKIP_ARG || frames[VS_SKIPS] > frames[VS_IGNORE]) {
    return (__pc_push(st, sp, E_VERB, pc, pos), sp + 3, frames);
  }
  return (st, sp, frames);
}

// The state of a run that goes back through its stack to a choice. `back`
// says that it found one.
struct PBack { st: i32[], sp: i32, slots: i32[], frames: i32[], fr: i32, ftop: i32, mode: i32, pc: i32, pos: i32, back: boolean }

// Goes back through the stack to the next choice that the mode of `b`
// takes. Each entry on the way undoes its step, and a verb or a branch
// changes the mode as PCRE2 does.
// One branch for each kind of entry of the stack.
// fern-lint: allow cyclomatic-complexity
function __pc_unwind(re: Pcre, own b: PBack): PBack {
  let code = re.code;
  let st = b.st;
  let sp = b.sp;
  let slots = b.slots;
  let frames = b.frames;
  let fr = b.fr;
  let ftop = b.ftop;
  let mode = b.mode;
  while (sp > 0) {
    sp -= 3;
    let e = st[sp];
    if (e >= 0) {
      if (mode == R_NOMATCH) {
        return PBack { st: st, sp: sp, slots: slots, frames: frames, fr: fr, ftop: ftop, mode: mode, pc: e, pos: st[sp + 1], back: true };
      }
    } else if (e == E_UNDO) {
      slots = slots.with(st[sp + 1], st[sp + 2]);
    } else if (e == E_CALL) {
      let group = frames[st[sp + 2]];
      fr = st[sp + 1];
      ftop = st[sp + 2];
      if (mode != R_NOMATCH && frames[VS_REC] == group) {
        mode = R_NOMATCH;
      }
    } else if (e == E_RET) {
      fr = st[sp + 1];
    } else if (e == E_BRANCH) {
      let next = code[3 * st[sp + 1] + 1];
      mode = __pc_branch_mode(re, frames, mode, code[3 * st[sp + 1] + 2]);
      if (mode == R_NOMATCH && next >= 0) {
        return PBack { st: st, sp: sp, slots: slots, frames: frames, fr: fr, ftop: ftop, mode: mode, pc: next, pos: st[sp + 2], back: true };
      }
    } else if (e == E_LOOK) {
      if (mode == R_THEN) {
        mode = R_NOMATCH;
      }
    } else {
      let (m, f) = __pc_verb_mode(code, frames, fr, mode, st[sp + 1], st[sp + 2]);
      mode = m;
      frames = f;
    }
  }
  return PBack { st: st, sp: sp, slots: slots, frames: frames, fr: fr, ftop: ftop, mode: mode, pc: b.pc, pos: b.pos, back: false };
}

// The mode after the run goes back onto the alternatives of row `row`: a
// (*THEN) of one of their branches ends at them.
function __pc_branch_mode(re: Pcre, frames: i32[], mode: i32, row: i32): i32 {
  let rec = re.alts[2 * row + 1];
  if (mode == R_THEN && frames[VS_THEN] < re.alts[2 * row] && (rec == -2 || rec == frames[VS_REC])) {
    return R_NOMATCH;
  }
  return mode;
}

// The mode and the verb state after the run goes back onto the verb at
// `vpc`, which it passed at `at`. A (*MARK) ends a (*SKIP:NAME) with its
// name.
function __pc_verb_mode(code: i32[], own frames: i32[], fr: i32, mode: i32, vpc: i32, at: i32): (i32, i32[]) {
  let kind = code[3 * vpc + 1];
  if (mode == R_NOMATCH && kind != V_MARK) {
    frames = frames.with(VS_REC, if (fr >= 0) { frames[fr] } else { -1 }).with(VS_POS, at);
    if (kind == V_SKIP_ARG) {
      frames = frames.with(VS_NAME, code[3 * vpc + 2]);
    } else if (kind == V_THEN) {
      frames = frames.with(VS_THEN, code[3 * vpc + 2]);
    }
    return (kind + 2, frames);
  }
  if (mode == R_SKIP_ARG && kind == V_MARK && code[3 * vpc + 2] == frames[VS_NAME]) {
    return (R_SKIP, frames.with(VS_POS, at));
  }
  return (mode, frames);
}

// The stack after an item of its own matched with the slots `after`: an
// undo for each slot that changed, and with `look` an entry that ends a
// (*THEN) at the look-around.
function __pc_took(own st: i32[], sp: i32, slots: i32[], after: i32[], look: boolean): (i32[], i32) {
  for k in 0..slots.len() {
    if (after[k] != slots[k]) {
      st = __pc_push(st, sp, E_UNDO, k, slots[k]);
      sp += 3;
    }
  }
  if (look) {
    st = __pc_push(st, sp, E_LOOK, 0, 0);
    sp += 3;
  }
  return (st, sp);
}

// Whether a call of `group` at `pos` is in a call of the same group at the
// same offset, which PCRE2 refuses as a loop.
function __pc_loops(frames: i32[], fr: i32, group: i32, pos: i32): boolean {
  let f = fr;
  while (f >= 0 && frames[f] != group) {
    f = frames[f + 2];
  }
  return f >= 0 && frames[f + 3] == pos;
}

// The stack and the slots at the end of the call of frame `fr`: the
// captures of the call go back to those at the call, each with an undo.
function __pc_returned(own st: i32[], sp: i32, own slots: i32[], frames: i32[], fr: i32, n: i32): (i32[], i32, i32[]) {
  for k in 2..n {
    let old = frames[fr + 5 + k];
    if (slots[k] != old) {
      st = __pc_push(st, sp, E_UNDO, k, slots[k]);
      sp += 3;
      slots = slots.with(k, old);
    }
  }
  return (st, sp, slots);
}

function __pc_push(own st: i32[], sp: i32, a: i32, b: i32, c: i32): i32[] {
  if (sp + 3 <= st.len()) {
    return st.with(sp, a).with(sp + 1, b).with(sp + 2, c);
  }
  return st.append(a).append(b).append(c);
}

// A search of its own: an atomic group, a look-around, or the look-around
// of a condition. The result is 1 when the item matches or the condition is
// true, 0 when not, R_ACCEPT or R_ERROR, or a verb whose effect goes on in
// the search outside. Then come the offset after the item, the slots and
// the frames. The captures of a condition stay when its look-around
// matches, as in PCRE2, also for a negative one.
// One branch for each kind of search and each result, as in PCRE2.
// fern-lint: allow cyclomatic-complexity
function __pc_sub(r: PRun, id: i32, pos: i32, slots: i32[], frames: i32[], fr: i32, ftop: i32, depth: i32): (i32, i32, i32[], i32[]) {
  let kind = r.re.subs[3 * id];
  if (depth >= 1000) {
    r.steps.set(-ERR_DEPTH);
    return (R_ERROR, pos, slots, frames);
  }
  let look = if (kind > COND) { kind - COND } else { kind };
  let target = if (look == LOOK_BEHIND || look == LOOK_NOT_BEHIND) { pos } else { -1 };
  let (rc, end, out, fs) = __pc_run(r, r.re.subs[3 * id + 1], pos, slots, target, frames, fr, ftop, depth + 1, fr);
  if (rc == R_ERROR) {
    return (R_ERROR, pos, slots, fs);
  }
  if (kind == ATOMIC) {
    return match (rc) {
      0 => (0, pos, slots, fs),
      1 => (1, end, out, fs),
      _ => (rc, end, out, fs),
    };
  }
  let positive = look == LOOK_AHEAD || look == LOOK_BEHIND;
  if (rc == R_SKIP_ARG || positive && kind < COND && rc >= R_COMMIT && rc != R_THEN) {
    return (rc, pos, slots, fs);
  }
  let matched = rc == R_MATCH || rc == R_ACCEPT;
  let keep = matched && (positive || kind > COND);
  return (if (matched == positive) { 1 } else { 0 }, pos, if (keep) { out } else { slots }, fs);
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

// The length of the newline that starts at `pos`, or 0.
// One branch for each newline convention.
// fern-lint: allow cyclomatic-complexity
function __pc_nl_at(re: Pcre, text: string, pos: i32): i32 {
  if (pos >= text.len()) {
    return 0;
  }
  let c = text[pos] as i32;
  let nl = re.nl;
  if (nl == NL_LF || nl == NL_CR || nl == NL_NUL) {
    return if (c == (if (nl == NL_LF) { 10 } else if (nl == NL_CR) { 13 } else { 0 })) { 1 } else { 0 };
  }
  if (c == 13 && pos + 1 < text.len() && text[pos + 1] == 10u8) {
    return 2;
  }
  if (nl == NL_CRLF) {
    return 0;
  }
  if (c == 10 || c == 13 || nl == NL_ANY && (c == 11 || c == 12)) {
    return 1;
  }
  if (nl == NL_ANYCRLF || c < 128) {
    return 0;
  }
  let (cp, n) = __pc_char_at(re, text, pos);
  return if (cp == 133 || cp == 8232 || cp == 8233) { n } else { 0 };
}

// Whether a newline ends at `pos`.
// One branch for each newline convention.
// fern-lint: allow cyclomatic-complexity
function __pc_nl_before(re: Pcre, text: string, pos: i32): boolean {
  if (pos <= 0) {
    return false;
  }
  let c = text[pos - 1] as i32;
  let nl = re.nl;
  if (nl == NL_LF || nl == NL_CR || nl == NL_NUL) {
    return c == (if (nl == NL_LF) { 10 } else if (nl == NL_CR) { 13 } else { 0 });
  }
  if (nl == NL_CRLF) {
    return c == 10 && pos >= 2 && text[pos - 2] == 13u8;
  }
  if (c == 10 || c == 13 || nl == NL_ANY && (c == 11 || c == 12)) {
    return true;
  }
  if (nl == NL_ANYCRLF || c < 128 && re.utf) {
    return false;
  }
  let b = __pc_char_before(re, text, pos);
  return b == 133 || b == 8232 || b == 8233;
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
    return pos == 0 || pos < n && (if (r.re.nl == NL_LF) { text[pos - 1] == 10u8 } else { __pc_nl_before(r.re, text, pos) });
  }
  if (kind == A_END) {
    return pos == n;
  }
  if (kind == A_EOL || kind == A_END_NL) {
    return pos == n || pos + __pc_nl_at(r.re, text, pos) == n && pos < n;
  }
  if (kind == A_MEOL) {
    return pos == n || (if (r.re.nl == NL_LF) { text[pos] == 10u8 } else { __pc_nl_at(r.re, text, pos) > 0 });
  }
  if (kind == A_G) {
    return pos == r.start;
  }
  let before = __pc_is_word(r.re, __pc_char_before(r.re, text, pos));
  let (c, w) = __pc_char_at(r.re, text, pos);
  let after = __pc_is_word(r.re, c);
  return before != after == (kind == A_WORD);
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
  if (a < 128 || !re.fold) {
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
