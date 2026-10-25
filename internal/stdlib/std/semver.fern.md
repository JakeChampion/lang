# Semantic version values

This module implements the `Semantic Versioning 2.0.0` rules.

## Model

A version separates major, minor, patch, prerelease identifiers, and build metadata. Comparison follows `Semantic Versioning 2.0.0` precedence.

## Contracts

Build metadata does not affect precedence. The parser rejects invalid numeric forms and reports the byte offset and error kind.

## Executable example

```fern test name=std-semver-example
import "std/semver";

function main(): i32 {
  let Ok(a) = semver.parse("1.2.3-alpha") else { return 1; };
  let Ok(b) = semver.parse("1.2.3") else { return 2; };
  if (a.compare(b) >= 0) { return 3; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 pub enum ParseErrorKind>>

<<3 pub function (e: ParseError) message(): string>>

<<4 function __sv_error(offset: i32, kind: ParseErrorKind): ParseError>>

<<5 pub struct SemVer>>

<<6 function __sv_all_digits(s: string): boolean>>

<<7 pub function parse(s: string): Result[SemVer, ParseError]>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module implements the `Semantic Versioning 2.0.0` rules.
import "std/result";
import "std/string";
import "std/error";
import "core/cmp";
import "std/i32";
```

### `2 pub enum ParseErrorKind`

```fern
<<2 pub enum ParseErrorKind>>=
@derive(cmp.Eq)
pub enum ParseErrorKind { ExpectedCore, ExpectedNumber, LeadingZero, NumberOutOfRange, EmptyIdentifier, InvalidByte(u8) }

@derive(cmp.Eq)
pub struct ParseError { offset: i32, kind: ParseErrorKind }

impl error.Error for ParseError {}

impl cmp.Display for ParseError {}
```

### `3 pub function (e: ParseError) message(): string`

```fern
<<3 pub function (e: ParseError) message(): string>>=
pub function (e: ParseError) message(): string {
  return e.to_string();
}

pub function (e: ParseError) to_string(): string {
  let reason: string = "expected major.minor.patch";
  match (e.kind) {
    ParseErrorKind.ExpectedCore => {},
    ParseErrorKind.ExpectedNumber => {
      reason = "expected a numeric component";
    },
    ParseErrorKind.LeadingZero => {
      reason = "numeric identifier has a leading zero";
    },
    ParseErrorKind.NumberOutOfRange => {
      reason = "numeric component exceeds i32";
    },
    ParseErrorKind.EmptyIdentifier => {
      reason = "empty identifier";
    },
    ParseErrorKind.InvalidByte(byte) => {
      reason = "invalid byte " + (byte as i32).to_string();
    }
  }
  return "semver byte " + e.offset.to_string() + ": " + reason;
}
```

### `4 function __sv_error(offset: i32, kind: ParseErrorKind): ParseError`

```fern
<<4 function __sv_error(offset: i32, kind: ParseErrorKind): ParseError>>=
function __sv_error(offset: i32, kind: ParseErrorKind): ParseError {
  return ParseError { offset: offset, kind: kind };
}
```

### `5 pub struct SemVer`

```fern
<<5 pub struct SemVer>>=
pub struct SemVer {
  major: i32,
  minor: i32,
  patch: i32,
  prerelease: string,
  build: string,
}
```

### `6 function __sv_all_digits(s: string): boolean`

```fern
<<6 function __sv_all_digits(s: string): boolean>>=
function __sv_all_digits(s: string): boolean {
  if (s.len() == 0) {
    return false;
  }
  let i: i32 = 0;
  while (i < s.len()) {
    if (s[i] < 48 || s[i] > 57) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __sv_num(s: string, offset: i32): Result[i32, ParseError] {
  if (s.len() == 0) {
    return Err(__sv_error(offset, ParseErrorKind.ExpectedNumber));
  }
  for i in 0..s.len() {
    if (s[i] < 48 || s[i] > 57) {
      return Err(__sv_error(offset + i, ParseErrorKind.InvalidByte(s[i])));
    }
  }
  if (s.len() > 1 && s[0] == 48) {
    return Err(__sv_error(offset, ParseErrorKind.LeadingZero));
  }
  match (i32.parse_i32(s)) {
    Ok(number) => {
      return Ok(number);
    },
    Err(_) => {
      return Err(__sv_error(offset, ParseErrorKind.NumberOutOfRange));
    }
  }
}

function __sv_identifiers(s: string, offset: i32, prerelease: boolean): Result[boolean, ParseError] {
  let start: i32 = 0;
  for i in 0..s.len() + 1 {
    if (i == s.len() || s[i] == 46) {
      if (i == start) {
        return Err(__sv_error(offset + i, ParseErrorKind.EmptyIdentifier));
      }
      let identifier: string = __sv_slice(s, start, i);
      if (prerelease && identifier.len() > 1 && identifier[0] == 48 && __sv_all_digits(identifier)) {
        return Err(__sv_error(offset + start, ParseErrorKind.LeadingZero));
      }
      start = i + 1;
    } else {
      let byte: u8 = s[i];
      if (!(byte >= 48 && byte <= 57 || byte >= 65 && byte <= 90 || byte >= 97 && byte <= 122 || byte == 45)) {
        return Err(__sv_error(offset + i, ParseErrorKind.InvalidByte(byte)));
      }
    }
  }
  return Ok(true);
}

function __sv_slice(s: string, start: i32, end: i32): string {
  let b: u8[] = [];
  let i: i32 = start;
  while (i < end) {
    b = b.append(s[i] as u8);
    i = i + 1;
  }
  return string_from_bytes_unchecked(b);
}

function __sv_str_cmp(a: string, b: string): i32 {
  let n: i32 = a.len();
  if (b.len() < n) {
    n = b.len();
  }
  let i: i32 = 0;
  while (i < n) {
    if (a[i] < b[i]) {
      return 0 - 1;
    }
    if (a[i] > b[i]) {
      return 1;
    }
    i = i + 1;
  }
  if (a.len() < b.len()) {
    return 0 - 1;
  }
  if (a.len() > b.len()) {
    return 1;
  }
  return 0;
}

function __sv_significant_digits(s: string): string {
  let i: i32 = 0;
  while (i < s.len() && s[i] == 48) {
    i = i + 1;
  }
  return __sv_slice(s, i, s.len());
}

function __sv_cmp_numeric(x: string, y: string): i32 {
  let xs: string = __sv_significant_digits(x);
  let ys: string = __sv_significant_digits(y);
  if (xs.len() != ys.len()) {
    if (xs.len() < ys.len()) {
      return 0 - 1;
    }
    return 1;
  }
  return __sv_str_cmp(xs, ys);
}

function __sv_cmp_ident(x: string, y: string): i32 {
  let xn: boolean = __sv_all_digits(x);
  let yn: boolean = __sv_all_digits(y);
  if (xn && yn) {
    return __sv_cmp_numeric(x, y);
  }
  if (xn && !yn) {
    return 0 - 1;
  }
  if (!xn && yn) {
    return 1;
  }
  return __sv_str_cmp(x, y);
}
```

### `7 pub function parse(s: string): Result[SemVer, ParseError]`

```fern
<<7 pub function parse(s: string): Result[SemVer, ParseError]>>=
// parse checks the complete SemVer syntax. Error offsets count bytes.
// Numeric core components must fit i32. Numeric prerelease identifiers have no size limit.
pub function parse(s: string): Result[SemVer, ParseError] {
  let core_pre: string = s;
  let build: string = "";
  if let Some(plus) = s.index_of("+") {
    core_pre = __sv_slice(s, 0, plus);
    build = __sv_slice(s, plus + 1, s.len());
    let valid: boolean = __sv_identifiers(build, plus + 1, false)?;
  }
  let core: string = core_pre;
  let pre: string = "";
  if let Some(dash) = core_pre.index_of("-") {
    core = __sv_slice(core_pre, 0, dash);
    pre = __sv_slice(core_pre, dash + 1, core_pre.len());
    let valid: boolean = __sv_identifiers(pre, dash + 1, true)?;
  }
  let parts: string[] = core.split(".");
  if (parts.len() != 3) {
    return Err(__sv_error(core.len(), ParseErrorKind.ExpectedCore));
  }
  let major: i32 = __sv_num(parts[0], 0)?;
  let minor: i32 = __sv_num(parts[1], parts[0].len() + 1)?;
  let patch: i32 = __sv_num(parts[2], parts[0].len() + parts[1].len() + 2)?;
  return Ok(SemVer { major: major, minor: minor, patch: patch, prerelease: pre, build: build });
}

pub function (a: SemVer) compare(b: SemVer): i32 {
  if (a.major != b.major) {
    if (a.major < b.major) {
      return 0 - 1;
    }
    return 1;
  }
  if (a.minor != b.minor) {
    if (a.minor < b.minor) {
      return 0 - 1;
    }
    return 1;
  }
  if (a.patch != b.patch) {
    if (a.patch < b.patch) {
      return 0 - 1;
    }
    return 1;
  }
  let ap: boolean = a.prerelease.len() > 0;
  let bp: boolean = b.prerelease.len() > 0;
  if (!ap && !bp) {
    return 0;
  }
  if (!ap && bp) {
    return 1;
  }
  if (ap && !bp) {
    return 0 - 1;
  }
  let ai: string[] = a.prerelease.split(".");
  let bi: string[] = b.prerelease.split(".");
  let n: i32 = ai.len();
  if (bi.len() < n) {
    n = bi.len();
  }
  let i: i32 = 0;
  while (i < n) {
    let c: i32 = __sv_cmp_ident(ai[i], bi[i]);
    if (c != 0) {
      return c;
    }
    i = i + 1;
  }
  if (ai.len() < bi.len()) {
    return 0 - 1;
  }
  if (ai.len() > bi.len()) {
    return 1;
  }
  return 0;
}

pub function (a: SemVer) eq(b: SemVer): boolean {
  return a.compare(b) == 0;
}

pub function (a: SemVer) lt(b: SemVer): boolean {
  return a.compare(b) < 0;
}

pub function (a: SemVer) gt(b: SemVer): boolean {
  return a.compare(b) > 0;
}

pub function (v: SemVer) to_string(): string {
  let out: string = v.major.to_string() + "." + v.minor.to_string() + "." + v.patch.to_string();
  if (v.prerelease.len() > 0) {
    out = out + "-" + v.prerelease;
  }
  if (v.build.len() > 0) {
    out = out + "+" + v.build;
  }
  return out;
}
```
