# Parsing expression grammars

This module evaluates PEG patterns.

## Model

A grammar maps rule names to patterns and names a start rule. A match records its final position and named captures.

## Contracts

Grammar construction checks rule references before evaluation. A missing rule is a `GrammarError` that names it. A failed match records the furthest input position reached by the evaluator.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum Pattern>>

<<4 pub function (e: PegError) to_string(): string>>

<<5 impl error.Error for PegError>>

<<6 pub function (e: PegError) message(): string>>

<<7 struct PegState>>

<<8 function __peg_fail(st: PegState, pos: i32): (i32, PegState)>>

<<9 function __peg_run(g: Grammar, p: Pattern, input: string, pos: i32, st: PegState, depth: i32, seen_n: strin...>>

<<10 function __peg_check_refs(p: Pattern, rules: Map[string, Pattern]): Option[string]>>

<<11 pub function peg_grammar(rules: Map[string, Pattern], start: string): Result[Grammar, GrammarError]>>

<<12 function __peg_result(r: i32, st: PegState, input: string): Result[PegMatch, PegError]>>

<<13 pub function peg_match(g: Grammar, input: string): Result[PegMatch, PegError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module evaluates PEG patterns.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/map";
import "core/int";
import "core/cmp";
import "std/error";
import "std/utf8";
```

### `3 pub enum Pattern`

```fern
<<3 pub enum Pattern>>=
pub enum Pattern {
  PLit(string),
  PAny,
  PRange(i32, i32),
  PSet(string),
  PNotSet(string),
  PSeq(Pattern[]),
  PChoice(Pattern[]),
  PStar(Pattern),
  PPlus(Pattern),
  POpt(Pattern),
  PNot(Pattern),
  PAnd(Pattern),
  PRef(string),
  PCap(string, Pattern),
  PEof,
}

pub struct Grammar { rules: Map[string, Pattern], start: string }

pub struct PegMatch { pos: i32, caps: Map[string, string] }

pub struct PegError { furthest: i32 }

pub struct GrammarError { rule: string }
```

### `4 pub function (e: PegError) to_string(): string`

```fern
<<4 pub function (e: PegError) to_string(): string>>=
pub function (e: PegError) to_string(): string {
  return e.message();
}
```

### `5 impl error.Error for PegError`

```fern
<<5 impl error.Error for PegError>>=
impl error.Error for PegError {}

impl cmp.Display for PegError {}

impl error.Error for GrammarError {}

impl cmp.Display for GrammarError {}
```

### `6 pub function (e: PegError) message(): string`

```fern
<<6 pub function (e: PegError) message(): string>>=
pub function (e: PegError) message(): string {
  return "peg: no match (reached byte " + int.int_to_string(e.furthest) + ")";
}

pub function (e: GrammarError) message(): string {
  return "peg: unknown rule \"" + e.rule + "\"";
}

pub function (e: GrammarError) to_string(): string {
  return e.message();
}
```

### `7 struct PegState`

```fern
<<7 struct PegState>>=
struct PegState { tn: string[], ts: i32[], te: i32[], furthest: i32 }
```

### `8 function __peg_fail(st: PegState, pos: i32): (i32, PegState)`

```fern
<<8 function __peg_fail(st: PegState, pos: i32): (i32, PegState)>>=
function __peg_fail(st: PegState, pos: i32): (i32, PegState) {
  if (pos > st.furthest) {
    return (0 - 1, PegState { ...st, furthest: pos });
  }
  return (0 - 1, st);
}

function __peg_merge_furthest(keep: PegState, from: PegState): PegState {
  if (from.furthest > keep.furthest) {
    return PegState { ...keep, furthest: from.furthest };
  }
  return keep;
}
```

### `9 function __peg_run(g: Grammar, p: Pattern, input: string, pos: i32, st: PegState, depth: i32, seen_n: strin...`

```fern
<<9 function __peg_run(g: Grammar, p: Pattern, input: string, pos: i32, st: PegState, depth: i32, seen_n: strin...>>=
function __peg_run(g: Grammar, p: Pattern, input: string, pos: i32, st: PegState, depth: i32, seen_n: string[], seen_p: i32[]): (i32, PegState) {
  match (p) {
    PLit(s) => {
      let n: i32 = s.len();
      if (pos + n > input.len()) {
        return __peg_fail(st, pos);
      }
      let i: i32 = 0;
      while (i < n) {
        if (input[pos + i] != s[i]) {
          return __peg_fail(st, pos);
        }
        i = i + 1;
      }
      return (pos + n, st);
    },
    PAny => {
      if (pos >= input.len()) {
        return __peg_fail(st, pos);
      }
      return (pos + 1, st);
    },
    PRange(lo, hi) => {
      if (pos >= input.len()) {
        return __peg_fail(st, pos);
      }
      let c: i32 = input[pos] as i32;
      if (c < lo || c > hi) {
        return __peg_fail(st, pos);
      }
      return (pos + 1, st);
    },
    PSet(cs) => {
      if (pos >= input.len()) {
        return __peg_fail(st, pos);
      }
      let c: i32 = input[pos] as i32;
      let i: i32 = 0;
      while (i < cs.len()) {
        if (cs[i] as i32 == c) {
          return (pos + 1, st);
        }
        i = i + 1;
      }
      return __peg_fail(st, pos);
    },
    PNotSet(cs) => {
      if (pos >= input.len()) {
        return __peg_fail(st, pos);
      }
      let c: i32 = input[pos] as i32;
      let i: i32 = 0;
      while (i < cs.len()) {
        if (cs[i] as i32 == c) {
          return __peg_fail(st, pos);
        }
        i = i + 1;
      }
      return (pos + 1, st);
    },
    PSeq(ps) => {
      let cur: i32 = pos;
      let s2: PegState = st;
      let i: i32 = 0;
      while (i < ps.len()) {
        let (r, s3) = __peg_run(g, ps[i], input, cur, s2, depth, seen_n, seen_p);
        if (r < 0) {
          return (r, s3);
        }
        cur = r;
        s2 = s3;
        i = i + 1;
      }
      return (cur, s2);
    },
    PChoice(ps) => {
      let s2: PegState = st;
      let i: i32 = 0;
      while (i < ps.len()) {
        let (r, s3) = __peg_run(g, ps[i], input, pos, s2, depth, seen_n, seen_p);
        if (r >= 0) {
          return (r, s3);
        }
        s2 = __peg_merge_furthest(s2, s3);
        i = i + 1;
      }
      return __peg_fail(s2, pos);
    },
    PStar(sub) => {
      let cur: i32 = pos;
      let s2: PegState = st;
      while (true) {
        let (r, s3) = __peg_run(g, sub, input, cur, s2, depth, seen_n, seen_p);
        if (r < 0) {
          return (cur, __peg_merge_furthest(s2, s3));
        }
        if (r == cur) {
          return (cur, s3);
        }
        cur = r;
        s2 = s3;
      }
      return (cur, s2);
    },
    PPlus(sub) => {
      let (first, s2) = __peg_run(g, sub, input, pos, st, depth, seen_n, seen_p);
      if (first < 0) {
        return (first, s2);
      }
      return __peg_run(g, PStar(sub), input, first, s2, depth, seen_n, seen_p);
    },
    POpt(sub) => {
      let (r, s2) = __peg_run(g, sub, input, pos, st, depth, seen_n, seen_p);
      if (r >= 0) {
        return (r, s2);
      }
      return (pos, __peg_merge_furthest(st, s2));
    },
    PNot(sub) => {
      let (r, s2) = __peg_run(g, sub, input, pos, st, depth, seen_n, seen_p);
      if (r >= 0) {
        return __peg_fail(st, pos);
      }
      return (pos, st);
    },
    PAnd(sub) => {
      let (r, s2) = __peg_run(g, sub, input, pos, st, depth, seen_n, seen_p);
      if (r < 0) {
        return (r, s2);
      }
      return (pos, st);
    },
    PRef(name) => {
      if (depth >= 2048) {
        return __peg_fail(st, pos);
      }
      let si: i32 = 0;
      while (si < seen_n.len()) {
        if (seen_p[si] == pos && seen_n[si] == name) {
          return __peg_fail(st, pos);
        }
        si = si + 1;
      }
      match (g.rules.get(name)) {
        Some(rp) => {
          return __peg_run(g, rp, input, pos, st, depth + 1, seen_n.append(name), seen_p.append(pos));
        },
        None => {
          return __peg_fail(st, pos);
        }
      }
    },
    PCap(name, sub) => {
      let (r, s2) = __peg_run(g, sub, input, pos, st, depth, seen_n, seen_p);
      if (r < 0) {
        return (r, s2);
      }
      return (r, PegState {
        tn: s2.tn.append(name),
        ts: s2.ts.append(pos),
        te: s2.te.append(r),
        furthest: s2.furthest,
      });
    },
    PEof => {
      if (pos == input.len()) {
        return (pos, st);
      }
      return __peg_fail(st, pos);
    }
  }
  return __peg_fail(st, pos);
}
```

### `10 function __peg_check_refs(p: Pattern, rules: Map[string, Pattern]): Option[string]`

```fern
<<10 function __peg_check_refs(p: Pattern, rules: Map[string, Pattern]): Option[string]>>=
function __peg_check_refs(p: Pattern, rules: Map[string, Pattern]): Option[string] {
  match (p) {
    PSeq(ps) => {
      return __peg_check_all(ps, rules);
    },
    PChoice(ps) => {
      return __peg_check_all(ps, rules);
    },
    PStar(sub) => {
      return __peg_check_refs(sub, rules);
    },
    PPlus(sub) => {
      return __peg_check_refs(sub, rules);
    },
    POpt(sub) => {
      return __peg_check_refs(sub, rules);
    },
    PNot(sub) => {
      return __peg_check_refs(sub, rules);
    },
    PAnd(sub) => {
      return __peg_check_refs(sub, rules);
    },
    PCap(name, sub) => {
      return __peg_check_refs(sub, rules);
    },
    PRef(name) => {
      if (rules.contains(name)) {
        return None;
      }
      return Some(name);
    },
    _ => {
      return None;
    }
  }
  return None;
}

function __peg_check_all(ps: Pattern[], rules: Map[string, Pattern]): Option[string] {
  for p in ps {
    let Some(bad) = __peg_check_refs(p, rules) else {
      continue;
    };
    return Some(bad);
  }
  return None;
}
```

### `11 pub function peg_grammar(rules: Map[string, Pattern], start: string): Result[Grammar, GrammarError]`

```fern
<<11 pub function peg_grammar(rules: Map[string, Pattern], start: string): Result[Grammar, GrammarError]>>=
pub function peg_grammar(rules: Map[string, Pattern], start: string): Result[Grammar, GrammarError] {
  if (!rules.contains(start)) {
    return Err(GrammarError { rule: start });
  }
  for name in rules.keys() {
    let Some(p) = rules.get(name) else {
      continue;
    };
    let Some(bad) = __peg_check_refs(p, rules) else {
      continue;
    };
    return Err(GrammarError { rule: bad });
  }
  return Ok(Grammar { rules: rules, start: start });
}
```

### `12 function __peg_result(r: i32, st: PegState, input: string): Result[PegMatch, PegError]`

```fern
<<12 function __peg_result(r: i32, st: PegState, input: string): Result[PegMatch, PegError]>>=
function __peg_result(r: i32, st: PegState, input: string): Result[PegMatch, PegError] {
  let caps: Map[string, string] = map_new(8);
  if (r < 0) {
    return Err(PegError { furthest: st.furthest });
  }
  let i: i32 = 0;
  while (i < st.tn.len()) {
    if (st.ts[i] != st.te[i] && (!utf8.is_char_boundary(input, st.ts[i]) || !utf8.is_char_boundary(input, st.te[i]))) {
      let furthest: i32 = st.furthest;
      if (r > furthest) {
        furthest = r;
      }
      return Err(PegError { furthest: furthest });
    }
    i = i + 1;
  }
  i = 0;
  while (i < st.tn.len()) {
    caps = caps.insert(st.tn[i], slice_unchecked(input, st.ts[i], st.te[i]) + "");
    i = i + 1;
  }
  return Ok(PegMatch { pos: r, caps: caps });
}
```

### `13 pub function peg_match(g: Grammar, input: string): Result[PegMatch, PegError]`

```fern
<<13 pub function peg_match(g: Grammar, input: string): Result[PegMatch, PegError]>>=
pub function peg_match(g: Grammar, input: string): Result[PegMatch, PegError] {
  let tn: string[] = [];
  let ts: i32[] = [];
  let te: i32[] = [];
  let st: PegState = PegState { tn: tn, ts: ts, te: te, furthest: 0 };
  let (r, s2) = __peg_run(g, PRef(g.start), input, 0, st, 0, [], []);
  return __peg_result(r, s2, input);
}

pub function peg_match_pattern(p: Pattern, input: string): Result[PegMatch, PegError] {
  let rules: Map[string, Pattern] = map_new(2);
  rules = rules.insert("__start", p);
  let g: Grammar = Grammar { rules: rules, start: "__start" };
  return peg_match(g, input);
}

pub function peg_digit(): Pattern {
  return PRange(48, 57);
}

pub function peg_lower(): Pattern {
  return PRange(97, 122);
}

pub function peg_upper(): Pattern {
  return PRange(65, 90);
}

pub function peg_alpha(): Pattern {
  return PChoice([PRange(65, 90), PRange(97, 122)]);
}

pub function peg_alnum(): Pattern {
  return PChoice([PRange(48, 57), PRange(65, 90), PRange(97, 122)]);
}

pub function peg_space(): Pattern {
  return PSet(" \t\r\n");
}
```
