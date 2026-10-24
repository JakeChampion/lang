# HTTP cache validators

This module evaluates conditional requests and constructs HTTP cache headers.

## Model

Entity tags distinguish weak and strong validators. A validator set combines an optional tag with an optional modification time.

## Contracts

Precondition evaluation returns an explicit decision. Strong and weak comparisons have separate methods. The cache-control parser retains the response policy.

## Executable example

```fern test name=std-http-cache-example
import "std/http/cache";

function main(): i32 {
  let tag = cache.strong("version-1");
  if (tag.to_string() != "\"version-1\"") { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct ETag>>

<<4 pub function strong(opaque: string): ETag>>

<<5 function __etagc_only(s: string): boolean>>

<<6 pub function (a: ETag) strong_eq(b: ETag): boolean>>

<<7 pub struct Validators>>

<<8 pub function validators(): Validators>>

<<9 pub enum Precondition>>

<<10 pub function evaluate(req: HttpRequest, v: Validators, now: Instant): Precondition>>

<<11 function __safe(method: string): boolean>>

<<12 pub function if_range(req: HttpRequest, v: Validators, now: Instant): boolean>>

<<13 function __carry(out: HttpResponse, from: HttpResponse, name: string): HttpResponse>>

<<14 pub function precondition_failed(): HttpResponse>>

<<15 pub const DELTA_MAX: i64>>

<<16 pub function cache_control(): CacheControl>>

<<17 function __flag(out: string[], name: string, on: boolean): string[]>>

<<18 pub function parse_cache_control(h: HeaderMap): CacheControl>>

<<19 function __directive(c: CacheControl, name: string, value: string, has_value: boolean): CacheControl>>

<<20 pub function (r: HttpResponse) with_cache_control(c: CacheControl): HttpResponse>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module evaluates conditional requests and constructs HTTP cache headers.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/http";
import "std/time";
import "std/headers";
```

### `3 pub struct ETag`

```fern
<<3 pub struct ETag>>=
pub struct ETag { weak: boolean, opaque: string }
```

### `4 pub function strong(opaque: string): ETag`

```fern
<<4 pub function strong(opaque: string): ETag>>=
pub function strong(opaque: string): ETag {
  return ETag { weak: false, opaque: opaque };
}

pub function weak(opaque: string): ETag {
  return ETag { weak: true, opaque: opaque };
}

pub function (e: ETag) to_string(): string {
  if (e.weak) {
    return "W/\"" + e.opaque + "\"";
  }
  return "\"" + e.opaque + "\"";
}

pub function parse_etag(s: string): Option[ETag] {
  let t: string = s.trim();
  let is_weak: boolean = t.starts_with("W/");
  if (is_weak) {
    t = t.slice_snap(2, t.len());
  }
  if (t.len() < 2 || t[0] != 34 as u8 || t[t.len() - 1] != 34 as u8) {
    return None;
  }
  let opaque: string = t.slice_snap(1, t.len() - 1);
  if (!__etagc_only(opaque)) {
    return None;
  }
  return Some(ETag { weak: is_weak, opaque: opaque });
}
```

### `5 function __etagc_only(s: string): boolean`

```fern
<<5 function __etagc_only(s: string): boolean>>=
function __etagc_only(s: string): boolean {
  let i: i32 = 0;
  while (i < s.len()) {
    let b: u8 = s[i];
    if (b < 33 as u8 || b == 34 as u8 || b == 127 as u8) {
      return false;
    }
    i = i + 1;
  }
  return true;
}
```

### `6 pub function (a: ETag) strong_eq(b: ETag): boolean`

```fern
<<6 pub function (a: ETag) strong_eq(b: ETag): boolean>>=
pub function (a: ETag) strong_eq(b: ETag): boolean {
  return !a.weak && !b.weak && a.opaque == b.opaque;
}

pub function (a: ETag) weak_eq(b: ETag): boolean {
  return a.opaque == b.opaque;
}
```

### `7 pub struct Validators`

```fern
<<7 pub struct Validators>>=
pub struct Validators { etag: Option[ETag], last_modified: Option[Instant] }
```

### `8 pub function validators(): Validators`

```fern
<<8 pub function validators(): Validators>>=
pub function validators(): Validators {
  return Validators { etag: None, last_modified: None };
}
```

### `9 pub enum Precondition`

```fern
<<9 pub enum Precondition>>=
pub enum Precondition { Proceed, NotModified, PreconditionFailed }
```

### `10 pub function evaluate(req: HttpRequest, v: Validators, now: Instant): Precondition`

```fern
<<10 pub function evaluate(req: HttpRequest, v: Validators, now: Instant): Precondition>>=
pub function evaluate(req: HttpRequest, v: Validators, now: Instant): Precondition {
  if (req.headers.has("if-match")) {
    if (!__matches_strong(req.headers.get_list("if-match"), v)) {
      return PreconditionFailed;
    }
  } else if (__modified_after(req.headers, "if-unmodified-since", v, now)) {
    return PreconditionFailed;
  }
  if (req.headers.has("if-none-match")) {
    if (!__matches_weak(req.headers.get_list("if-none-match"), v)) {
      return Proceed;
    }
    if (__safe(req.method)) {
      return NotModified;
    }
    return PreconditionFailed;
  }
  if (__safe(req.method) && __modified_by(req.headers, v, now)) {
    return NotModified;
  }
  return Proceed;
}

pub function evaluate_absent(req: HttpRequest): Precondition {
  if (req.headers.has("if-match")) {
    return PreconditionFailed;
  }
  return Proceed;
}
```

### `11 function __safe(method: string): boolean`

```fern
<<11 function __safe(method: string): boolean>>=
function __safe(method: string): boolean {
  return method == "GET" || method == "HEAD";
}

function __matches_strong(members: string[], v: Validators): boolean {
  for m in members {
    if (m == "*" || __tag_matches(m, v, true)) {
      return true;
    }
  }
  return false;
}

function __matches_weak(members: string[], v: Validators): boolean {
  for m in members {
    if (m == "*" || __tag_matches(m, v, false)) {
      return true;
    }
  }
  return false;
}

function __tag_matches(member: string, v: Validators, strong_cmp: boolean): boolean {
  match (v.etag) {
    Some(current) => {
      match (parse_etag(member)) {
        Some(t) => {
          if (strong_cmp) {
            return t.strong_eq(current);
          }
          return t.weak_eq(current);
        },
        None => {
          return false;
        }
      }
    },
    None => {
      return false;
    }
  }
}

function __modified_after(h: HeaderMap, name: string, v: Validators, now: Instant): boolean {
  match (__field_date(h, name, now)) {
    Some(d) => {
      match (v.last_modified) {
        Some(lm) => {
          return lm.sec > d.sec;
        },
        None => {
          return false;
        }
      }
    },
    None => {
      return false;
    }
  }
}

function __modified_by(h: HeaderMap, v: Validators, now: Instant): boolean {
  match (__field_date(h, "if-modified-since", now)) {
    Some(d) => {
      match (v.last_modified) {
        Some(lm) => {
          return lm.sec <= d.sec;
        },
        None => {
          return false;
        }
      }
    },
    None => {
      return false;
    }
  }
}

function __field_date(h: HeaderMap, name: string, now: Instant): Option[Instant] {
  let values: string[] = h.get_all(name);
  if (values.len() != 1) {
    return None;
  }
  return time.http_date_parse_at(values[0].trim(), now);
}
```

### `12 pub function if_range(req: HttpRequest, v: Validators, now: Instant): boolean`

```fern
<<12 pub function if_range(req: HttpRequest, v: Validators, now: Instant): boolean>>=
pub function if_range(req: HttpRequest, v: Validators, now: Instant): boolean {
  let values: string[] = req.headers.get_all("if-range");
  if (values.len() == 0) {
    return true;
  }
  if (values.len() > 1) {
    return false;
  }
  let value: string = values[0].trim();
  if (value.starts_with("\"")) {
    return __tag_matches(value, v, true);
  }
  match (time.http_date_parse_at(value, now)) {
    Some(d) => {
      match (v.last_modified) {
        Some(lm) => {
          return lm.sec == d.sec;
        },
        None => {
          return false;
        }
      }
    },
    None => {
      return false;
    }
  }
}

pub function respond(req: HttpRequest, v: Validators, now: Instant, resp: HttpResponse): HttpResponse {
  if (resp.status < 200 || resp.status > 299) {
    return resp;
  }
  let current: HttpResponse = resp.with_validators(v, now);
  match (evaluate(req, v, now)) {
    Proceed => {
      return current;
    },
    NotModified => {
      return not_modified(current);
    },
    PreconditionFailed => {
      return precondition_failed();
    }
  }
}

pub function (r: HttpResponse) with_validators(v: Validators, now: Instant): HttpResponse {
  let out: HttpResponse = r;
  match (v.etag) {
    Some(e) => {
      out = out.with_header("ETag", e.to_string());
    },
    None => {}
  }
  match (v.last_modified) {
    Some(lm) => {
      let sent: Instant = lm;
      if (lm.sec > now.sec) {
        sent = now;
      }
      out = out.with_header("Last-Modified", sent.format_http_date());
    },
    None => {}
  }
  return out;
}

pub function not_modified(resp: HttpResponse): HttpResponse {
  let out: HttpResponse = HttpResponse { status: 304, body: BodyText(""), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
  for name in ["Cache-Control", "Content-Location", "ETag", "Expires", "Vary"] {
    out = __carry(out, resp, name);
  }
  if (!resp.headers.has("etag")) {
    out = __carry(out, resp, "Last-Modified");
  }
  return out;
}
```

### `13 function __carry(out: HttpResponse, from: HttpResponse, name: string): HttpResponse`

```fern
<<13 function __carry(out: HttpResponse, from: HttpResponse, name: string): HttpResponse>>=
function __carry(out: HttpResponse, from: HttpResponse, name: string): HttpResponse {
  let r: HttpResponse = out;
  for v in from.headers.get_all(name) {
    r = r.with_appended_header(name, v);
  }
  return r;
}
```

### `14 pub function precondition_failed(): HttpResponse`

```fern
<<14 pub function precondition_failed(): HttpResponse>>=
pub function precondition_failed(): HttpResponse {
  return HttpResponse { status: 412, body: BodyText(""), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
}
```

### `15 pub const DELTA_MAX: i64`

```fern
<<15 pub const DELTA_MAX: i64>>=
pub const DELTA_MAX: i64 = 2147483648;

pub struct CacheControl {
  max_age: Option[i64],
  s_maxage: Option[i64],
  max_stale: Option[i64],
  min_fresh: Option[i64],
  stale_while_revalidate: Option[i64],
  stale_if_error: Option[i64],
  no_cache: boolean,
  no_cache_fields: string[],
  private: boolean,
  private_fields: string[],
  public: boolean,
  no_store: boolean,
  no_transform: boolean,
  must_revalidate: boolean,
  proxy_revalidate: boolean,
  must_understand: boolean,
  immutable: boolean,
  only_if_cached: boolean,
}
```

### `16 pub function cache_control(): CacheControl`

```fern
<<16 pub function cache_control(): CacheControl>>=
pub function cache_control(): CacheControl {
  let none: string[] = [];
  return CacheControl {
    max_age: None,
    s_maxage: None,
    max_stale: None,
    min_fresh: None,
    stale_while_revalidate: None,
    stale_if_error: None,
    no_cache: false,
    no_cache_fields: none,
    private: false,
    private_fields: none,
    public: false,
    no_store: false,
    no_transform: false,
    must_revalidate: false,
    proxy_revalidate: false,
    must_understand: false,
    immutable: false,
    only_if_cached: false,
  };
}

pub function (c: CacheControl) to_string(): string {
  let out: string[] = [];
  out = __flag(out, "no-store", c.no_store);
  out = __qualified(out, "no-cache", c.no_cache, c.no_cache_fields);
  out = __qualified(out, "private", c.private, c.private_fields);
  out = __flag(out, "public", c.public);
  out = __flag(out, "must-revalidate", c.must_revalidate);
  out = __flag(out, "proxy-revalidate", c.proxy_revalidate);
  out = __flag(out, "must-understand", c.must_understand);
  out = __flag(out, "no-transform", c.no_transform);
  out = __flag(out, "immutable", c.immutable);
  out = __flag(out, "only-if-cached", c.only_if_cached);
  out = __delta(out, "max-age", c.max_age);
  out = __delta(out, "s-maxage", c.s_maxage);
  out = __max_stale(out, c.max_stale);
  out = __delta(out, "min-fresh", c.min_fresh);
  out = __delta(out, "stale-while-revalidate", c.stale_while_revalidate);
  out = __delta(out, "stale-if-error", c.stale_if_error);
  return __join(out);
}
```

### `17 function __flag(out: string[], name: string, on: boolean): string[]`

```fern
<<17 function __flag(out: string[], name: string, on: boolean): string[]>>=
function __flag(out: string[], name: string, on: boolean): string[] {
  if (on) {
    return out.append(name);
  }
  return out;
}

function __qualified(out: string[], name: string, on: boolean, fields: string[]): string[] {
  if (!on) {
    return out;
  }
  if (fields.len() == 0) {
    return out.append(name);
  }
  return out.append(name + "=\"" + __join(fields) + "\"");
}

function __delta(out: string[], name: string, n: Option[i64]): string[] {
  match (n) {
    Some(v) => {
      return out.append(name + "=" + v.to_string());
    },
    None => {
      return out;
    }
  }
}

function __max_stale(out: string[], n: Option[i64]): string[] {
  match (n) {
    Some(v) => {
      if (v >= DELTA_MAX) {
        return out.append("max-stale");
      }
      return out.append("max-stale=" + v.to_string());
    },
    None => {
      return out;
    }
  }
}

function __join(parts: string[]): string {
  let out: string = "";
  for p in parts {
    if (out.len() > 0) {
      out = out + ", ";
    }
    out = out + p;
  }
  return out;
}
```

### `18 pub function parse_cache_control(h: HeaderMap): CacheControl`

```fern
<<18 pub function parse_cache_control(h: HeaderMap): CacheControl>>=
pub function parse_cache_control(h: HeaderMap): CacheControl {
  let c: CacheControl = cache_control();
  for member in h.get_list("cache-control") {
    let name: string = member.to_lower();
    let value: string = "";
    let has_value: boolean = false;
    if let Some(eq) = member.index_of("=") {
      name = member.slice_snap(0, eq).trim().to_lower();
      value = headers.unquote(member.slice_snap(eq + 1, member.len()).trim());
      has_value = true;
    }
    c = __directive(c, name, value, has_value);
  }
  return c;
}
```

### `19 function __directive(c: CacheControl, name: string, value: string, has_value: boolean): CacheControl`

```fern
<<19 function __directive(c: CacheControl, name: string, value: string, has_value: boolean): CacheControl>>=
function __directive(c: CacheControl, name: string, value: string, has_value: boolean): CacheControl {
  if (__is_delta_name(name)) {
    return __delta_directive(c, name, value, has_value);
  }
  if (name == "no-cache" || name == "private") {
    return __qualified_directive(c, name, value, has_value);
  }
  return __flag_directive(__flag_directive_more(c, name), name);
}

function __is_delta_name(name: string): boolean {
  return name == "max-age" || name == "s-maxage" || name == "max-stale" || name == "min-fresh" || name == "stale-while-revalidate" || name == "stale-if-error";
}

function __delta_directive(c: CacheControl, name: string, value: string, has_value: boolean): CacheControl {
  let n: Option[i64] = __delta_seconds(value);
  if (name == "max-stale" && !has_value) {
    n = Some(DELTA_MAX);
  }
  if (__is_none(n)) {
    return c;
  }
  if (name == "max-age" && __is_none(c.max_age)) {
    return CacheControl { ...c, max_age: n };
  }
  if (name == "s-maxage" && __is_none(c.s_maxage)) {
    return CacheControl { ...c, s_maxage: n };
  }
  if (name == "max-stale" && __is_none(c.max_stale)) {
    return CacheControl { ...c, max_stale: n };
  }
  return __delta_directive_more(c, name, n);
}

function __delta_directive_more(c: CacheControl, name: string, n: Option[i64]): CacheControl {
  if (name == "min-fresh" && __is_none(c.min_fresh)) {
    return CacheControl { ...c, min_fresh: n };
  }
  if (name == "stale-while-revalidate" && __is_none(c.stale_while_revalidate)) {
    return CacheControl { ...c, stale_while_revalidate: n };
  }
  if (name == "stale-if-error" && __is_none(c.stale_if_error)) {
    return CacheControl { ...c, stale_if_error: n };
  }
  return c;
}

function __is_none(n: Option[i64]): boolean {
  match (n) {
    Some(_) => {
      return false;
    },
    None => {
      return true;
    }
  }
}

function __delta_seconds(s: string): Option[i64] {
  if (s.len() == 0) {
    return None;
  }
  let n: i64 = 0;
  let i: i32 = 0;
  while (i < s.len()) {
    let b: u8 = s[i];
    if (b < 48 as u8 || b > 57 as u8) {
      return None;
    }
    if (n < DELTA_MAX) {
      n = n * (10 as i64) + (b - 48 as u8) as i64;
    }
    i = i + 1;
  }
  if (n > DELTA_MAX) {
    return Some(DELTA_MAX);
  }
  return Some(n);
}

function __qualified_directive(c: CacheControl, name: string, value: string, has_value: boolean): CacheControl {
  let fields: string[] = [];
  if (has_value) {
    fields = headers.split_list(value);
  }
  if (name == "no-cache" && !c.no_cache) {
    return CacheControl { ...c, no_cache: true, no_cache_fields: fields };
  }
  if (name == "private" && !c.private) {
    return CacheControl { ...c, private: true, private_fields: fields };
  }
  return c;
}

function __flag_directive(c: CacheControl, name: string): CacheControl {
  if (name == "no-store") {
    return CacheControl { ...c, no_store: true };
  }
  if (name == "no-transform") {
    return CacheControl { ...c, no_transform: true };
  }
  if (name == "must-revalidate") {
    return CacheControl { ...c, must_revalidate: true };
  }
  if (name == "proxy-revalidate") {
    return CacheControl { ...c, proxy_revalidate: true };
  }
  return c;
}

function __flag_directive_more(c: CacheControl, name: string): CacheControl {
  if (name == "must-understand") {
    return CacheControl { ...c, must_understand: true };
  }
  if (name == "public") {
    return CacheControl { ...c, public: true };
  }
  if (name == "immutable") {
    return CacheControl { ...c, immutable: true };
  }
  if (name == "only-if-cached") {
    return CacheControl { ...c, only_if_cached: true };
  }
  return c;
}
```

### `20 pub function (r: HttpResponse) with_cache_control(c: CacheControl): HttpResponse`

```fern
<<20 pub function (r: HttpResponse) with_cache_control(c: CacheControl): HttpResponse>>=
pub function (r: HttpResponse) with_cache_control(c: CacheControl): HttpResponse {
  let v: string = c.to_string();
  if (v.len() == 0) {
    return r;
  }
  return r.with_header("Cache-Control", v);
}

pub function (r: HttpResponse) with_vary(field: string): HttpResponse {
  let present: string[] = r.headers.get_list("vary");
  if (field == "*") {
    return r.with_header("Vary", "*");
  }
  for f in present {
    if (f == "*" || f.eq_ignore_ascii_case(field)) {
      return r;
    }
  }
  return r.with_header("Vary", __join(present.append(field)));
}
```
