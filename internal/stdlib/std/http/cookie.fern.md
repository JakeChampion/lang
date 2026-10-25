# HTTP cookies and cookie storage

This module reads, writes, and stores HTTP cookies.

## Model

Request cookie pairs and response cookie attributes have separate parse rules. A cookie value retains path, domain, expiry, and security attributes.

## Contracts

Length limits bound cookie fields and attributes. SameSite and partition policy remain explicit. The cookie jar uses request context to select applicable values.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum SameSite>>

<<4 pub function new(name: string, value: string): SetCookie>>

<<5 pub enum CookieError>>

<<6 pub function (e: CookieError) to_string(): string>>

<<7 impl error.Error for CookieError>>

<<8 pub function (e: CookieError) message(): string>>

<<9 impl http.ToResponse for CookieError>>

<<10 pub function (c: SetCookie) serialize(): Result[string, CookieError]>>

<<11 function __flags(c: SetCookie): string>>

<<12 pub function (r: HttpResponse) with_cookie(c: SetCookie): Result[HttpResponse, CookieError]>>

<<13 pub struct Cookie>>

<<14 pub function (i: Ignored) message(): string>>

<<15 pub struct Jar>>

<<16 pub function jar(): Jar>>

<<17 struct __Parsed>>

<<18 function __url(request_url: string): Result[Url, Ignored]>>

<<19 function __max_age(v: string): Option[i32]>>

<<20 pub function parse_date(s: string): Option[i64]>>

<<21 function __year_of(year: i32): i32>>

<<22 function __place(j: Jar, u: Url, p: __Parsed, now: i64): Result[Cookie, Ignored]>>

<<23 pub function domain_matches(host: string, domain: string): boolean>>

<<24 function __is_ip(host: string): boolean>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads, writes, and stores HTTP cookies.

import "std/i32";
import "std/unicode";
// This module reads, writes, and stores HTTP cookies.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/http";
import "std/headers";
import "std/string";
import "std/array";
import "std/url";
import "std/time";
import "std/publicsuffix";
import "core/int";
import "std/error";
import "core/cmp";
```

### `3 pub enum SameSite`

```fern
<<3 pub enum SameSite>>=
pub enum SameSite { Unset, Strict, Lax, CrossSite }

pub struct SetCookie { name: string, value: string, path: string, domain: string, max_age: i32, expires: i64, secure: boolean, http_only: boolean, same_site: SameSite, partitioned: boolean }

pub const MAX_AGE_CAP: i32 = 34560000;

pub const PAIR_CAP: i32 = 4096;

pub const ATTRIBUTE_CAP: i32 = 1024;
```

### `4 pub function new(name: string, value: string): SetCookie`

```fern
<<4 pub function new(name: string, value: string): SetCookie>>=
pub function new(name: string, value: string): SetCookie {
  return SetCookie { name: name, value: value, path: "/", domain: "", max_age: -1, expires: 0 as i64, secure: false, http_only: true, same_site: Lax, partitioned: false };
}

pub function delete(name: string): SetCookie {
  return SetCookie { ...new(name, ""), max_age: 0 };
}
```

### `5 pub enum CookieError`

```fern
<<5 pub enum CookieError>>=
pub enum CookieError {
  BadName(string),
  BadValueByte(string),
  BadAttribute(string),
  TooLong(i32),
  Lifetime(i32),
  Prefix(string),
}
```

### `6 pub function (e: CookieError) to_string(): string`

```fern
<<6 pub function (e: CookieError) to_string(): string>>=
pub function (e: CookieError) to_string(): string {
  return e.message();
}
```

### `7 impl error.Error for CookieError`

```fern
<<7 impl error.Error for CookieError>>=
impl error.Error for CookieError {}

impl cmp.Display for CookieError {}
```

### `8 pub function (e: CookieError) message(): string`

```fern
<<8 pub function (e: CookieError) message(): string>>=
pub function (e: CookieError) message(): string {
  match (e) {
    BadName(name) => {
      return "a cookie name must be a non-empty token: " + name;
    },
    BadValueByte(name) => {
      return "the value of cookie " + name + " holds a byte a cookie value may not";
    },
    BadAttribute(attr) => {
      return "a cookie's " + attr + " attribute is not one a browser keeps";
    },
    TooLong(n) => {
      return "a cookie's name and value are past " + int.int_to_string(n) + " bytes";
    },
    Lifetime(n) => {
      return "a cookie's Max-Age is past " + int.int_to_string(n) + " seconds, 400 days";
    },
    Prefix(name) => {
      return "cookie " + name + " lacks what its prefix requires";
    }
  }
  return "";
}
```

### `9 impl http.ToResponse for CookieError`

```fern
<<9 impl http.ToResponse for CookieError>>=
impl http.ToResponse for CookieError {
  function to_response(self: CookieError): HttpResponse {
    return http.problem(500, "", self.message());
  }
}
```

### `10 pub function (c: SetCookie) serialize(): Result[string, CookieError]`

```fern
<<10 pub function (c: SetCookie) serialize(): Result[string, CookieError]>>=
pub function (c: SetCookie) serialize(): Result[string, CookieError] {
  __pair_ok(c)?;
  __attributes_ok(c)?;
  __prefix_ok(c)?;
  let out: string = c.name + "=" + c.value;
  if (c.path.len() > 0) {
    out = out + "; Path=" + c.path;
  }
  if (c.domain.len() > 0) {
    out = out + "; Domain=" + c.domain;
  }
  if (c.expires != 0 as i64) {
    out = out + "; Expires=" + time.instant_from_unix(c.expires).format_http_date();
  }
  if (c.max_age >= 0) {
    out = out + "; Max-Age=" + int.int_to_string(c.max_age);
  }
  return Ok(out + __flags(c));
}
```

### `11 function __flags(c: SetCookie): string`

```fern
<<11 function __flags(c: SetCookie): string>>=
function __flags(c: SetCookie): string {
  let out: string = "";
  if (c.secure) {
    out = out + "; Secure";
  }
  if (c.http_only) {
    out = out + "; HttpOnly";
  }
  let site: string = __same_site(c.same_site);
  if (site.len() > 0) {
    out = out + "; SameSite=" + site;
  }
  if (c.partitioned) {
    out = out + "; Partitioned";
  }
  return out;
}

function __same_site(s: SameSite): string {
  match (s) {
    Unset => {
      return "";
    },
    Strict => {
      return "Strict";
    },
    Lax => {
      return "Lax";
    },
    CrossSite => {
      return "None";
    }
  }
  return "";
}

function __pair_ok(c: SetCookie): Result[boolean, CookieError] {
  if (c.name.len() == 0 || !__is_token(c.name)) {
    return Err(BadName(c.name));
  }
  if (!__is_cookie_value(c.value)) {
    return Err(BadValueByte(c.name));
  }
  if (c.name.len() + c.value.len() > PAIR_CAP) {
    return Err(TooLong(PAIR_CAP));
  }
  return Ok(true);
}

function __attributes_ok(c: SetCookie): Result[boolean, CookieError] {
  if (!__is_attribute_value(c.path)) {
    return Err(BadAttribute("Path"));
  }
  if (!__is_attribute_value(c.domain)) {
    return Err(BadAttribute("Domain"));
  }
  if (c.max_age > MAX_AGE_CAP) {
    return Err(Lifetime(MAX_AGE_CAP));
  }
  if (c.partitioned && !c.secure) {
    return Err(BadAttribute("Partitioned"));
  }
  if (__same_site(c.same_site) == "None" && !c.secure) {
    return Err(BadAttribute("SameSite"));
  }
  return Ok(true);
}

function __prefix_ok(c: SetCookie): Result[boolean, CookieError] {
  if (c.name.starts_with("__Host-") && (!c.secure || c.path != "/" || c.domain.len() > 0)) {
    return Err(Prefix(c.name));
  }
  if (c.name.starts_with("__Secure-") && !c.secure) {
    return Err(Prefix(c.name));
  }
  return Ok(true);
}

function __is_token(s: string): boolean {
  let i: i32 = 0;
  while (i < s.len()) {
    if (!__is_tchar(s[i] as i32)) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __is_tchar(b: i32): boolean {
  if (b < 33 || b > 126) {
    return false;
  }
  return !"\"(),/:;<=>?@[\\]{}".contains(string_from_bytes_unchecked([b as u8]));
}

function __is_cookie_value(v: string): boolean {
  let s: string = v;
  if (s.len() >= 2 && s.starts_with("\"") && s.ends_with("\"")) {
    s = s.slice_snap(1, s.len() - 1);
  }
  let i: i32 = 0;
  while (i < s.len()) {
    if (!__is_cookie_octet(s[i] as i32)) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __is_cookie_octet(b: i32): boolean {
  return b == 33 || b >= 35 && b <= 43 || b >= 45 && b <= 58 || b >= 60 && b <= 91 || b >= 93 && b <= 126;
}

function __is_attribute_value(v: string): boolean {
  if (v.len() > ATTRIBUTE_CAP) {
    return false;
  }
  let i: i32 = 0;
  while (i < v.len()) {
    let b: i32 = v[i] as i32;
    if (b < 32 || b >= 127 || b == 59) {
      return false;
    }
    i = i + 1;
  }
  return true;
}
```

### `12 pub function (r: HttpResponse) with_cookie(c: SetCookie): Result[HttpResponse, CookieError]`

```fern
<<12 pub function (r: HttpResponse) with_cookie(c: SetCookie): Result[HttpResponse, CookieError]>>=
pub function (r: HttpResponse) with_cookie(c: SetCookie): Result[HttpResponse, CookieError] {
  let v: string = c.serialize()?;
  return Ok(HttpResponse { ...r, headers: r.headers.append("Set-Cookie", v) });
}

pub function (r: HttpRequest) cookie(name: string): Option[string] {
  for h in r.headers.get_all("cookie") {
    for pair in h.split(";") {
      let p: string = pair.trim();
      if let Some(eq) = p.index_of("=") {
        if (eq > 0 && p.slice_snap(0, eq) == name) {
          return Some(p.slice_snap(eq + 1, p.len()));
        }
      }
    }
  }
  return None;
}
```

### `13 pub struct Cookie`

```fern
<<13 pub struct Cookie>>=
pub struct Cookie { name: string, value: string, domain: string, path: string, host_only: boolean, secure: boolean, http_only: boolean, partitioned: boolean, expires: i64, created: i64, accessed: i64 }

pub enum Ignored { NotACookie, ForeignDomain, PublicSuffixDomain, InsecureOrigin, SecureKept, PrefixBroken, UnsecuredPartition }
```

### `14 pub function (i: Ignored) message(): string`

```fern
<<14 pub function (i: Ignored) message(): string>>=
pub function (i: Ignored) message(): string {
  match (i) {
    NotACookie => {
      return "not a cookie";
    },
    ForeignDomain => {
      return "a Domain the host is not under";
    },
    PublicSuffixDomain => {
      return "a Domain that is a public suffix";
    },
    InsecureOrigin => {
      return "Secure over plain http";
    },
    SecureKept => {
      return "a Secure cookie is not replaced over plain http";
    },
    PrefixBroken => {
      return "lacks what its prefix requires";
    },
    UnsecuredPartition => {
      return "Partitioned without Secure";
    }
  }
  return "";
}
```

### `15 pub struct Jar`

```fern
<<15 pub struct Jar>>=
pub struct Jar { rows: Cell[string], suffix: (string) => boolean, per_domain: i32, total: i32 }
```

### `16 pub function jar(): Jar`

```fern
<<16 pub function jar(): Jar>>=
pub function jar(): Jar {
  return jar_with((host: string): boolean => publicsuffix.is_public_suffix(host), 50, 3000);
}

pub function jar_with(suffix: (string) => boolean, per_domain: i32, total: i32): Jar {
  return Jar { rows: cell_new(""), suffix: suffix, per_domain: per_domain, total: total };
}

pub function (j: Jar) store(request_url: string, h: HeaderMap, now: i64): i32 {
  let n: i32 = 0;
  for v in h.get_all("set-cookie") {
    match (j.set(request_url, v, now)) {
      Ok(_) => {
        n = n + 1;
      },
      Err(_) => {}
    }
  }
  return n;
}

pub function (j: Jar) set(request_url: string, set_cookie: string, now: i64): Result[Cookie, Ignored] {
  let u: Url = __url(request_url)?;
  let parsed: __Parsed = __parse(set_cookie, now)?;
  let c: Cookie = __place(j, u, parsed, now)?;
  if (u.scheme.to_lower() != "https") {
    if (c.secure) {
      return Err(InsecureOrigin);
    }
    if (__secure_shadowed(j.all(now), c)) {
      return Err(SecureKept);
    }
  }
  __prefixes_ok(c, parsed.has_path)?;
  j.rows.set(__joined(__evicted(__replaced(j.all(now), c), j, c.domain)));
  return Ok(c);
}

pub function (j: Jar) header(request_url: string, now: i64): Option[string] {
  let u: Url = Url { scheme: "", host: "", port: 0, path: "", query: "", fragment: "" };
  match (url.url_parse(request_url)) {
    Some(p) => {
      u = p;
    },
    None => {
      return None;
    }
  }
  let host: string = u.host.to_lower();
  let path: string = __request_path(u);
  let secure: boolean = u.scheme.to_lower() == "https";
  let all: Cookie[] = j.all(now);
  let picked: Cookie[] = [];
  let i: i32 = 0;
  while (i < all.len()) {
    if (__reaches(all[i], host, path, secure)) {
      picked = __ordered(picked, all[i]);
      all = all.with(i, Cookie { ...all[i], accessed: now });
    }
    i = i + 1;
  }
  if (picked.len() == 0) {
    return None;
  }
  j.rows.set(__joined(all));
  return Some(__pairs(picked));
}

pub function (j: Jar) all(now: i64): Cookie[] {
  let out: Cookie[] = [];
  for line in j.rows.get().split("\n") {
    if (line.len() > 0) {
      let c: Cookie = __restored(line);
      if (c.expires < 0 as i64 || c.expires > now) {
        out = out.append(c);
      }
    }
  }
  return out;
}

pub function (j: Jar) clear(): void {
  j.rows.set("");
}
```

### `17 struct __Parsed`

```fern
<<17 struct __Parsed>>=
struct __Parsed { name: string, value: string, domain: string, path: string, has_path: boolean, secure: boolean, http_only: boolean, partitioned: boolean, expires: i64, max_age_set: boolean }
```

### `18 function __url(request_url: string): Result[Url, Ignored]`

```fern
<<18 function __url(request_url: string): Result[Url, Ignored]>>=
function __url(request_url: string): Result[Url, Ignored] {
  match (url.url_parse(request_url)) {
    Some(u) => {
      if (u.host.len() == 0) {
        return Err(NotACookie);
      }
      return Ok(u);
    },
    None => {
      return Err(NotACookie);
    }
  }
  return Err(NotACookie);
}

function __parse(s: string, now: i64): Result[__Parsed, Ignored] {
  if (__has_ctl(s)) {
    return Err(NotACookie);
  }
  let parts: string[] = s.split(";");
  let pair: string = parts[0].trim();
  let name: string = "";
  let value: string = pair;
  if let Some(eq) = pair.index_of("=") {
    name = pair.slice_snap(0, eq).trim();
    value = pair.slice_snap(eq + 1, pair.len()).trim();
  }
  if (name.len() + value.len() == 0 || name.len() + value.len() > PAIR_CAP) {
    return Err(NotACookie);
  }
  let p: __Parsed = __Parsed { name: name, value: value, domain: "", path: "", has_path: false, secure: false, http_only: false, partitioned: false, expires: 0 - 1 as i64, max_age_set: false };
  let i: i32 = 1;
  while (i < parts.len()) {
    p = __attribute(p, parts[i], now);
    i = i + 1;
  }
  return Ok(p);
}

function __has_ctl(s: string): boolean {
  let i: i32 = 0;
  while (i < s.len()) {
    let b: i32 = s[i] as i32;
    if (b < 32 && b != 9 || b == 127) {
      return true;
    }
    i = i + 1;
  }
  return false;
}

function __attribute(p: __Parsed, raw: string, now: i64): __Parsed {
  let name: string = raw.trim();
  let value: string = "";
  if let Some(eq) = raw.index_of("=") {
    name = raw.slice_snap(0, eq).trim();
    value = raw.slice_snap(eq + 1, raw.len()).trim();
  }
  if (value.len() > ATTRIBUTE_CAP) {
    return p;
  }
  let key: string = name.to_lower();
  if (key == "expires" || key == "max-age") {
    return __lifetime(p, key, value, now);
  }
  if (key == "domain" && value.len() > 0) {
    return __Parsed { ...p, domain: value.trim_start_chars(".").to_lower() };
  }
  if (key == "path") {
    return __Parsed { ...p, path: value, has_path: value.starts_with("/") };
  }
  return __flag(p, key);
}

function __flag(p: __Parsed, key: string): __Parsed {
  if (key == "secure") {
    return __Parsed { ...p, secure: true };
  }
  if (key == "httponly") {
    return __Parsed { ...p, http_only: true };
  }
  if (key == "partitioned") {
    return __Parsed { ...p, partitioned: true };
  }
  return p;
}

function __lifetime(p: __Parsed, key: string, value: string, now: i64): __Parsed {
  let cap: i64 = now + MAX_AGE_CAP as i64 * (1000 as i64);
  if (key == "max-age") {
    match (__max_age(value)) {
      Some(n) => {
        let at: i64 = now + n as i64 * (1000 as i64);
        if (n <= 0) {
          at = 0 as i64;
        }
        return __Parsed { ...p, expires: __min(at, cap), max_age_set: true };
      },
      None => {
        return p;
      }
    }
  }
  if (p.max_age_set) {
    return p;
  }
  match (parse_date(value)) {
    Some(sec) => {
      return __Parsed { ...p, expires: __min(sec * (1000 as i64), cap) };
    },
    None => {
      return p;
    }
  }
  return p;
}
```

### `19 function __max_age(v: string): Option[i32]`

```fern
<<19 function __max_age(v: string): Option[i32]>>=
function __max_age(v: string): Option[i32] {
  if (v.len() == 0) {
    return None;
  }
  let digits: string = v;
  if (v.starts_with("-")) {
    digits = v.slice_snap(1, v.len());
  }
  if (!digits.is_ascii_digits()) {
    return None;
  }
  match (i32.parse_i32(v)) {
    Ok(n) => {
      return Some(n);
    },
    Err(_) => {
      if (v.starts_with("-")) {
        return Some(0 - 1);
      }
      return Some(MAX_AGE_CAP);
    }
  }
  return None;
}

function __min(a: i64, b: i64): i64 {
  if (a < b) {
    return a;
  }
  return b;
}
```

### `20 pub function parse_date(s: string): Option[i64]`

```fern
<<20 pub function parse_date(s: string): Option[i64]>>=
pub function parse_date(s: string): Option[i64] {
  let found: i32[] = [0 - 1, 0 - 1, 0 - 1, 0 - 1, 0 - 1, 0 - 1];
  for token in __date_tokens(s) {
    found = __date_token(found, token);
  }
  let year: i32 = __year_of(found[5]);
  if (!__date_in_range(found, year)) {
    return None;
  }
  let d: Date = time.date_make(year, found[4], found[3]);
  if (!d.is_valid()) {
    return None;
  }
  let days: i32 = d.days_since(time.date_make(1970, 1, 1));
  return Some(days as i64 * (86400 as i64) + (found[0] * 3600 + found[1] * 60 + found[2]) as i64);
}
```

### `21 function __year_of(year: i32): i32`

```fern
<<21 function __year_of(year: i32): i32>>=
function __year_of(year: i32): i32 {
  if (year >= 70 && year <= 99) {
    return year + 1900;
  }
  if (year >= 0 && year <= 69) {
    return year + 2000;
  }
  return year;
}

function __date_in_range(found: i32[], year: i32): boolean {
  if (found[0] < 0 || found[3] < 0 || found[4] < 0 || found[5] < 0 || year < 1601) {
    return false;
  }
  return found[3] <= 31 && found[0] <= 23 && found[1] <= 59 && found[2] <= 59;
}

function __date_tokens(s: string): string[] {
  let out: string[] = [];
  let cur: string = "";
  let i: i32 = 0;
  while (i < s.len()) {
    if (__is_date_delimiter(s[i] as i32)) {
      if (cur.len() > 0) {
        out = out.append(cur);
      }
      cur = "";
    } else {
      cur = cur + s.slice_snap(i, i + 1);
    }
    i = i + 1;
  }
  if (cur.len() > 0) {
    out = out.append(cur);
  }
  return out;
}

function __is_date_delimiter(b: i32): boolean {
  return b == 9 || b >= 32 && b <= 47 || b >= 59 && b <= 64 || b >= 91 && b <= 96 || b >= 123 && b <= 126;
}

function __date_token(found: i32[], token: string): i32[] {
  if (found[0] < 0) {
    match (__time_of(token)) {
      Some(hms) => {
        return [hms.0, hms.1, hms.2, found[3], found[4], found[5]];
      },
      None => {}
    }
  }
  return __date_number(found, token);
}

function __date_number(found: i32[], token: string): i32[] {
  let digits: i32 = __leading_digits(token);
  if (found[3] < 0 && digits >= 1 && digits <= 2) {
    return [found[0], found[1], found[2], __digits_value(token, digits), found[4], found[5]];
  }
  if (found[4] < 0 && __month_of(token) > 0) {
    return [found[0], found[1], found[2], found[3], __month_of(token), found[5]];
  }
  if (found[5] < 0 && digits >= 2 && digits <= 4) {
    return [found[0], found[1], found[2], found[3], found[4], __digits_value(token, digits)];
  }
  return found;
}

function __time_of(token: string): Option[(i32, i32, i32)] {
  let parts: string[] = token.split(":");
  if (parts.len() < 3) {
    return None;
  }
  let h: i32 = __two_digits(parts[0], true);
  let m: i32 = __two_digits(parts[1], true);
  let s: i32 = __two_digits(parts[2], false);
  if (h < 0 || m < 0 || s < 0) {
    return None;
  }
  return Some((h, m, s));
}

function __two_digits(s: string, whole: boolean): i32 {
  let n: i32 = __leading_digits(s);
  if (n < 1 || n > 2 || whole && n != s.len()) {
    return 0 - 1;
  }
  return __digits_value(s, n);
}

function __leading_digits(token: string): i32 {
  let n: i32 = 0;
  while (n < token.len() && token[n] >= 48 && token[n] <= 57) {
    n = n + 1;
  }
  return n;
}

function __digits_value(token: string, n: i32): i32 {
  let v: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    v = v * 10 + (token[i] as i32 - 48);
    i = i + 1;
  }
  return v;
}

function __month_of(token: string): i32 {
  if (token.len() < 3) {
    return 0;
  }
  let months: string[] = ["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"];
  let head: string = token.slice_snap(0, 3).to_lower();
  let i: i32 = 0;
  while (i < 12) {
    if (months[i] == head) {
      return i + 1;
    }
    i = i + 1;
  }
  return 0;
}
```

### `22 function __place(j: Jar, u: Url, p: __Parsed, now: i64): Result[Cookie, Ignored]`

```fern
<<22 function __place(j: Jar, u: Url, p: __Parsed, now: i64): Result[Cookie, Ignored]>>=
function __place(j: Jar, u: Url, p: __Parsed, now: i64): Result[Cookie, Ignored] {
  let host: string = u.host.to_lower();
  let domain: string = host;
  let host_only: boolean = true;
  if (p.domain.len() > 0 && j.suffix(p.domain) && p.domain != host) {
    return Err(PublicSuffixDomain);
  }
  if (p.domain.len() > 0 && !j.suffix(p.domain)) {
    if (!domain_matches(host, p.domain)) {
      return Err(ForeignDomain);
    }
    domain = p.domain;
    host_only = false;
  }
  let path: string = p.path;
  if (!p.has_path) {
    path = __default_path(u);
  }
  return Ok(Cookie { name: p.name, value: p.value, domain: domain, path: path, host_only: host_only, secure: p.secure, http_only: p.http_only, expires: p.expires, created: now, accessed: now, partitioned: p.partitioned });
}

function __secure_shadowed(all: Cookie[], c: Cookie): boolean {
  for old in all {
    if (old.secure && old.name == c.name && (domain_matches(c.domain, old.domain) || domain_matches(old.domain, c.domain)) && path_matches(c.path, old.path)) {
      return true;
    }
  }
  return false;
}

function __prefixes_ok(c: Cookie, has_path: boolean): Result[boolean, Ignored] {
  let name: string = c.name.to_lower();
  if (name.starts_with("__secure-") && !c.secure) {
    return Err(PrefixBroken);
  }
  if (name.starts_with("__host-") && (!c.secure || !c.host_only || !has_path || c.path != "/")) {
    return Err(PrefixBroken);
  }
  if (c.partitioned && !c.secure) {
    return Err(UnsecuredPartition);
  }
  return Ok(true);
}

function __replaced(all: Cookie[], c: Cookie): Cookie[] {
  let out: Cookie[] = [];
  let fresh: Cookie = c;
  for old in all {
    if (old.name == c.name && old.domain == c.domain && old.path == c.path) {
      fresh = Cookie { ...c, created: old.created };
    } else {
      out = out.append(old);
    }
  }
  if (c.expires < 0 as i64 || c.expires > c.created) {
    out = out.append(fresh);
  }
  return out;
}

function __evicted(all: Cookie[], j: Jar, domain: string): Cookie[] {
  let out: Cookie[] = all;
  while (__count_of(out, domain) > j.per_domain) {
    out = __without_oldest(out, domain);
  }
  while (out.len() > j.total) {
    out = __without_oldest(out, "");
  }
  return out;
}

function __count_of(all: Cookie[], domain: string): i32 {
  let n: i32 = 0;
  for c in all {
    if (c.domain == domain) {
      n = n + 1;
    }
  }
  return n;
}

function __without_oldest(all: Cookie[], domain: string): Cookie[] {
  let pick: i32 = 0 - 1;
  let i: i32 = 0;
  while (i < all.len()) {
    if ((domain.len() == 0 || all[i].domain == domain) && (pick < 0 || all[i].accessed < all[pick].accessed)) {
      pick = i;
    }
    i = i + 1;
  }
  if (pick < 0) {
    return all;
  }
  return array.slice(all, 0, pick).concat(array.slice(all, pick + 1, all.len()));
}

function __reaches(c: Cookie, host: string, path: string, secure: boolean): boolean {
  if (c.host_only && host != c.domain) {
    return false;
  }
  if (!c.host_only && !domain_matches(host, c.domain)) {
    return false;
  }
  return path_matches(path, c.path) && (secure || !c.secure);
}

function __ordered(picked: Cookie[], c: Cookie): Cookie[] {
  let k: i32 = 0;
  while (k < picked.len() && (picked[k].path.len() > c.path.len() || picked[k].path.len() == c.path.len() && picked[k].created <= c.created)) {
    k = k + 1;
  }
  return array.slice(picked, 0, k).append(c).concat(array.slice(picked, k, picked.len()));
}

function __pairs(picked: Cookie[]): string {
  let out: string = "";
  for c in picked {
    if (out.len() > 0) {
      out = out + "; ";
    }
    if (c.name.len() > 0) {
      out = out + c.name + "=";
    }
    out = out + c.value;
  }
  return out;
}
```

### `23 pub function domain_matches(host: string, domain: string): boolean`

```fern
<<23 pub function domain_matches(host: string, domain: string): boolean>>=
pub function domain_matches(host: string, domain: string): boolean {
  let h: string = host.to_lower();
  let d: string = domain.to_lower();
  if (h == d) {
    return true;
  }
  return h.ends_with("." + d) && !__is_ip(h);
}

pub function path_matches(request_path: string, cookie_path: string): boolean {
  if (request_path == cookie_path) {
    return true;
  }
  if (!request_path.starts_with(cookie_path)) {
    return false;
  }
  return cookie_path.ends_with("/") || request_path[cookie_path.len()] == 47;
}
```

### `24 function __is_ip(host: string): boolean`

```fern
<<24 function __is_ip(host: string): boolean>>=
function __is_ip(host: string): boolean {
  if (host.contains(":")) {
    return true;
  }
  let labels: string[] = host.split(".");
  return labels[labels.len() - 1].is_ascii_digits();
}

function __default_path(u: Url): string {
  let p: string = u.path;
  if (!p.starts_with("/")) {
    return "/";
  }
  let Some(last) = p.last_index_of("/") else {
    return "/";
  };
  if (last == 0) {
    return "/";
  }
  return p.slice_snap(0, last);
}

function __request_path(u: Url): string {
  if (!u.path.starts_with("/")) {
    return "/";
  }
  return u.path;
}

function __sep(): string {
  return string_from_bytes_unchecked([1 as u8]);
}

function __joined(all: Cookie[]): string {
  let out: string = "";
  for c in all {
    out = out + __row(c) + "\n";
  }
  return out;
}

function __row(c: Cookie): string {
  let flags: string = "";
  if (c.host_only) {
    flags = flags + "h";
  }
  if (c.secure) {
    flags = flags + "s";
  }
  if (c.http_only) {
    flags = flags + "o";
  }
  if (c.partitioned) {
    flags = flags + "p";
  }
  let fields: string[] = [c.name, c.value, c.domain, c.path, flags, __i64_string(c.expires), __i64_string(c.created), __i64_string(c.accessed)];
  return fields.join(__sep());
}

function __restored(row: string): Cookie {
  let f: string[] = row.split(__sep());
  return Cookie { name: f[0], value: f[1], domain: f[2], path: f[3], host_only: f[4].contains("h"), secure: f[4].contains("s"), http_only: f[4].contains("o"), partitioned: f[4].contains("p"), expires: __i64_of(f[5]), created: __i64_of(f[6]), accessed: __i64_of(f[7]) };
}

function __i64_string(n: i64): string {
  if (n < 0 as i64) {
    return "-" + __i64_digits(0 as i64 - n);
  }
  return __i64_digits(n);
}

function __i64_digits(n: i64): string {
  if (n < 10 as i64) {
    return string_from_bytes_unchecked([(48 as i64 + n) as u8]);
  }
  return __i64_digits(n / (10 as i64)) + string_from_bytes_unchecked([(48 as i64 + n - n / (10 as i64) * (10 as i64)) as u8]);
}

function __i64_of(s: string): i64 {
  let v: i64 = 0 as i64;
  let neg: boolean = s.starts_with("-");
  let i: i32 = 0;
  if (neg) {
    i = 1;
  }
  while (i < s.len()) {
    v = v * (10 as i64) + (s[i] as i64 - 48 as i64);
    i = i + 1;
  }
  if (neg) {
    return 0 as i64 - v;
  }
  return v;
}
```
