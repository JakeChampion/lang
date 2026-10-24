# URL text and query values

This module parses URLs and query strings and converts percent escapes.

## Model

URL helpers separate references, paths, query fields, and percent escapes. Form conversion also applies the form-specific rule for a plus sign.

## Contracts

Decoders that return bytes preserve arbitrary decoded bytes. Reference resolution requires a base URL and can fail for invalid input.

## Executable example

```fern test name=std-url-example
import "std/url";

function main(): i32 {
  if (url.url_encode("a b") != "a%20b" || url.form_decode("a+b") != "a b") { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 function __url_unreserved(b: i32): boolean>>

<<4 pub function url_encode(s: string): string>>

<<5 pub function form_encode(s: string): string>>

<<6 function __url_scheme_char(b: i32, first: boolean): boolean>>

<<7 pub function url_resolve(base: string, reference: string): Option[string]>>

<<8 function __resolved(b: Url, reference: string): Option[string]>>

<<9 pub function url_remove_dot_segments(path: string): string>>

<<10 function __drop_last(segments: string[]): string[]>>

<<11 pub function url_parse(s: string): Option[Url]>>

<<12 pub function query_parse(s: string): Map[string, string[]]>>

<<13 function __query_pair(m: Map[string, string[]], s: string, start: i32, end: i32): Map[string, string[]]>>

<<14 pub function query_get(query: string, key: string): Option[string]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module parses URLs and query strings and converts percent escapes.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/map";
import "std/utf8" as utf8;
```

### `3 function __url_unreserved(b: i32): boolean`

```fern
<<3 function __url_unreserved(b: i32): boolean>>=
function __url_unreserved(b: i32): boolean {
  if (b >= 65 && b <= 90) {
    return true;
  }
  if (b >= 97 && b <= 122) {
    return true;
  }
  if (b >= 48 && b <= 57) {
    return true;
  }
  if (b == 45 || b == 46 || b == 95 || b == 126) {
    return true;
  }
  return false;
}

function __url_hex_char(d: i32): i32 {
  if (d < 10) {
    return d + 48;
  }
  return d + 55;
}

function __url_hex_val(c: i32): i32 {
  if (c >= 48 && c <= 57) {
    return c - 48;
  }
  if (c >= 97 && c <= 102) {
    return c - 87;
  }
  if (c >= 65 && c <= 70) {
    return c - 55;
  }
  return -1;
}
```

### `4 pub function url_encode(s: string): string`

```fern
<<4 pub function url_encode(s: string): string>>=
pub function url_encode(s: string): string {
  let n: i32 = s.len();
  if (n == 0) {
    return "";
  }
  let out_len: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    if (__url_unreserved(s[i] as i32)) {
      out_len = out_len + 1;
    } else {
      out_len = out_len + 3;
    }
    i = i + 1;
  }
  let buf: u8[] = __alloc_u8(out_len);
  let pos: i32 = 0;
  i = 0;
  while (i < n) {
    let b: i32 = s[i] as i32;
    if (__url_unreserved(b)) {
      buf = buf.with(pos, b as u8);
      pos = pos + 1;
    } else {
      buf = buf.with(pos, 37 as u8);
      buf = buf.with(pos + 1, __url_hex_char(b >> 4 & 15) as u8);
      buf = buf.with(pos + 2, __url_hex_char(b & 15) as u8);
      pos = pos + 3;
    }
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}

pub function url_decode(s: string): string {
  return utf8.from_bytes_lossy(url_decode_bytes(s));
}

pub function url_decode_bytes(s: string): u8[] {
  let n: i32 = s.len();
  if (n == 0) {
    return [];
  }
  let out_len: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    if (s[i] == 37 && i + 2 < n
      && __url_hex_val(s[i + 1] as i32) >= 0 && __url_hex_val(s[i + 2] as i32) >= 0) {
      i = i + 3;
    } else {
      i = i + 1;
    }
    out_len = out_len + 1;
  }
  let buf: u8[] = __alloc_u8(out_len);
  let pos: i32 = 0;
  i = 0;
  while (i < n) {
    let b: i32 = s[i] as i32;
    if (b == 37 && i + 2 < n
      && __url_hex_val(s[i + 1] as i32) >= 0 && __url_hex_val(s[i + 2] as i32) >= 0) {
      buf = buf.with(pos, (__url_hex_val(s[i + 1] as i32) << 4 | __url_hex_val(s[i + 2] as i32)) as u8);
      i = i + 3;
    } else {
      buf = buf.with(pos, b as u8);
      i = i + 1;
    }
    pos = pos + 1;
  }
  return buf;
}

pub function form_decode(s: string): string {
  return utf8.from_bytes_lossy(form_decode_bytes(s));
}

pub function form_decode_bytes(s: string): u8[] {
  let n: i32 = s.len();
  if (n == 0) {
    return [];
  }
  let out_len: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    if (s[i] == 37 && i + 2 < n
      && __url_hex_val(s[i + 1] as i32) >= 0 && __url_hex_val(s[i + 2] as i32) >= 0) {
      i = i + 3;
    } else {
      i = i + 1;
    }
    out_len = out_len + 1;
  }
  let buf: u8[] = __alloc_u8(out_len);
  let pos: i32 = 0;
  i = 0;
  while (i < n) {
    let b: i32 = s[i] as i32;
    if (b == 43) {
      buf = buf.with(pos, 32 as u8);
      i = i + 1;
    } else if (b == 37 && i + 2 < n
      && __url_hex_val(s[i + 1] as i32) >= 0 && __url_hex_val(s[i + 2] as i32) >= 0) {
      buf = buf.with(pos, (__url_hex_val(s[i + 1] as i32) << 4 | __url_hex_val(s[i + 2] as i32)) as u8);
      i = i + 3;
    } else {
      buf = buf.with(pos, b as u8);
      i = i + 1;
    }
    pos = pos + 1;
  }
  return buf;
}
```

### `5 pub function form_encode(s: string): string`

```fern
<<5 pub function form_encode(s: string): string>>=
pub function form_encode(s: string): string {
  let n: i32 = s.len();
  if (n == 0) {
    return "";
  }
  let out_len: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    let b: i32 = s[i] as i32;
    if (b == 32) {
      out_len = out_len + 1;
    } else if (__url_unreserved(b)) {
      out_len = out_len + 1;
    } else {
      out_len = out_len + 3;
    }
    i = i + 1;
  }
  let buf: u8[] = __alloc_u8(out_len);
  let pos: i32 = 0;
  i = 0;
  while (i < n) {
    let b: i32 = s[i] as i32;
    if (b == 32) {
      buf = buf.with(pos, 43 as u8);
      pos = pos + 1;
    } else if (__url_unreserved(b)) {
      buf = buf.with(pos, b as u8);
      pos = pos + 1;
    } else {
      buf = buf.with(pos, 37 as u8);
      buf = buf.with(pos + 1, __url_hex_char(b >> 4 & 15) as u8);
      buf = buf.with(pos + 2, __url_hex_char(b & 15) as u8);
      pos = pos + 3;
    }
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}
```

### `6 function __url_scheme_char(b: i32, first: boolean): boolean`

```fern
<<6 function __url_scheme_char(b: i32, first: boolean): boolean>>=
function __url_scheme_char(b: i32, first: boolean): boolean {
  if (b >= 65 && b <= 90) {
    return true;
  }
  if (b >= 97 && b <= 122) {
    return true;
  }
  if (first) {
    return false;
  }
  if (b >= 48 && b <= 57) {
    return true;
  }
  return b == 43 || b == 45 || b == 46;
}

function __url_ip_literal_char(b: i32): boolean {
  if (b >= 48 && b <= 57) {
    return true;
  }
  if (b >= 97 && b <= 102) {
    return true;
  }
  if (b >= 65 && b <= 70) {
    return true;
  }
  if (b == 118 || b == 86) {
    return true;
  }
  return b == 58 || b == 46 || b == 37;
}

function __url_reg_name_char(b: i32): boolean {
  if (__url_unreserved(b)) {
    return true;
  }
  if (b == 37) {
    return true;
  }
  if (b == 33 || b == 36 || b == 38 || b == 39) {
    return true;
  }
  if (b == 40 || b == 41 || b == 42 || b == 43) {
    return true;
  }
  return b == 44 || b == 59 || b == 61;
}
```

### `7 pub function url_resolve(base: string, reference: string): Option[string]`

```fern
<<7 pub function url_resolve(base: string, reference: string): Option[string]>>=
pub function url_resolve(base: string, reference: string): Option[string] {
  match (url_parse(base)) {
    Some(b) => {
      if (b.scheme.len() == 0) {
        return None;
      }
      return __resolved(b, reference);
    },
    None => {
      return None;
    }
  }
  return None;
}
```

### `8 function __resolved(b: Url, reference: string): Option[string]`

```fern
<<8 function __resolved(b: Url, reference: string): Option[string]>>=
function __resolved(b: Url, reference: string): Option[string] {
  if (reference.len() == 0) {
    return Some(__url_compose(b.scheme, b.host, b.port, b.path, b.query, ""));
  }
  if (reference.len() >= 2 && reference[0] == 47 as u8 && reference[1] == 47 as u8) {
    return Some(b.scheme + ":" + reference);
  }
  let r: Url = Url { scheme: "", host: "", port: 0, path: "", query: "", fragment: "" };
  match (url_parse(reference)) {
    Some(parsed) => {
      r = parsed;
    },
    None => {
      return None;
    }
  }
  if (r.scheme.len() > 0) {
    return Some(reference);
  }
  let pq: (string, string) = __merged(b, r, reference);
  return Some(__url_compose(b.scheme, b.host, b.port, url_remove_dot_segments(pq.0), pq.1, r.fragment));
}

function __merged(b: Url, r: Url, reference: string): (string, string) {
  if (r.path.len() == 0) {
    if (reference.contains("?")) {
      return (b.path, r.query);
    }
    return (b.path, b.query);
  }
  if (r.path[0] != 47 as u8) {
    return (__merged_path(b.path, r.path), r.query);
  }
  return (r.path, r.query);
}

function __merged_path(base: string, rel: string): string {
  let cut: i32 = base.len();
  while (cut > 0 && base[cut - 1] != 47 as u8) {
    cut = cut - 1;
  }
  if (cut == 0) {
    return "/" + rel;
  }
  return slice_unchecked(base, 0, cut).to_owned() + rel;
}

function __url_compose(scheme: string, host: string, port: i32, path: string, query: string, fragment: string): string {
  let out: string = scheme + "://";
  if (host.contains(":")) {
    out = out + "[" + host + "]";
  } else {
    out = out + host;
  }
  if (port != 0) {
    out = out + ":" + __port_text(port);
  }
  out = out + path;
  if (query.len() > 0) {
    out = out + "?" + query;
  }
  if (fragment.len() > 0) {
    out = out + "#" + fragment;
  }
  return out;
}

function __port_text(port: i32): string {
  let digits: string = "";
  let n: i32 = port;
  while (n > 0) {
    digits = string_from_bytes_unchecked([(48 + n % 10) as u8]) + digits;
    n = n / 10;
  }
  return digits;
}
```

### `9 pub function url_remove_dot_segments(path: string): string`

```fern
<<9 pub function url_remove_dot_segments(path: string): string>>=
pub function url_remove_dot_segments(path: string): string {
  let segments: string[] = [];
  let n: i32 = path.len();
  let absolute: boolean = n > 0 && path[0] == 47 as u8;
  let at: i32 = 0;
  if (absolute) {
    at = 1;
  }
  while (at <= n) {
    let seg_end: i32 = at;
    while (seg_end < n && path[seg_end] != 47 as u8) {
      seg_end = seg_end + 1;
    }
    let seg: string = slice_unchecked(path, at, seg_end).to_owned();
    let last: boolean = seg_end == n;
    if (seg == "..") {
      segments = __drop_last(segments);
    }
    if (seg == ".." || seg == ".") {
      if (last) {
        segments = segments.append("");
      }
    } else {
      segments = segments.append(seg);
    }
    at = seg_end + 1;
  }
  return __joined_path(segments, absolute);
}
```

### `10 function __drop_last(segments: string[]): string[]`

```fern
<<10 function __drop_last(segments: string[]): string[]>>=
function __drop_last(segments: string[]): string[] {
  let out: string[] = [];
  let i: i32 = 0;
  while (i + 1 < segments.len()) {
    out = out.append(segments[i]);
    i = i + 1;
  }
  return out;
}

function __joined_path(segments: string[], absolute: boolean): string {
  let out: string = "";
  let i: i32 = 0;
  while (i < segments.len()) {
    if (i > 0 || absolute) {
      out = out + "/";
    }
    out = out + segments[i];
    i = i + 1;
  }
  return out;
}
```

### `11 pub function url_parse(s: string): Option[Url]`

```fern
<<11 pub function url_parse(s: string): Option[Url]>>=
pub function url_parse(s: string): Option[Url] {
  let n: i32 = s.len();
  if (n == 0) {
    return None;
  }

  let u: Url = Url {
    scheme: "",
    host: "",
    port: 0,
    path: "",
    query: "",
    fragment: "",
  };

  let scheme_end: i32 = -1;
  let i: i32 = 0;
  while (i < n) {
    let sb: i32 = s[i] as i32;
    if (sb == 58) {
      if (i + 2 < n && s[i + 1] == 47 && s[i + 2] == 47) {
        scheme_end = i;
      }
      i = n;
    } else if (__url_scheme_char(sb, i == 0)) {
      i = i + 1;
    } else {
      i = n;
    }
  }

  let rest_start: i32 = 0;
  if (scheme_end > 0) {
    u = Url { ...u, scheme: slice_unchecked(s, 0, scheme_end) + "" };
    rest_start = scheme_end + 3;
  }

  let frag_start: i32 = n;
  i = rest_start;
  while (i < n) {
    if (s[i] == 35) {
      frag_start = i;
      break;
    }
    i = i + 1;
  }
  if (frag_start < n) {
    u = Url { ...u, fragment: slice_unchecked(s, frag_start + 1, n) + "" };
  }

  let query_start: i32 = frag_start;
  i = rest_start;
  while (i < frag_start) {
    if (s[i] == 63) {
      query_start = i;
      break;
    }
    i = i + 1;
  }
  if (query_start < frag_start) {
    u = Url { ...u, query: slice_unchecked(s, query_start + 1, frag_start) + "" };
  }

  let authority_end: i32 = query_start;
  if (scheme_end > 0) {
    i = rest_start;
    while (i < query_start) {
      if (s[i] == 47) {
        authority_end = i;
        break;
      }
      i = i + 1;
    }
  } else {
    authority_end = rest_start;
  }

  if (scheme_end > 0) {
    let host_start: i32 = rest_start;
    i = rest_start;
    while (i < authority_end) {
      if (s[i] == 64) {
        host_start = i + 1;
      }
      i = i + 1;
    }

    let host: string = "";
    let colon: i32 = -1;
    if (host_start < authority_end && s[host_start] == 91) {
      let close: i32 = -1;
      i = host_start + 1;
      while (i < authority_end) {
        if (s[i] == 93) {
          close = i;
          i = authority_end;
        } else if (!__url_ip_literal_char(s[i] as i32)) {
          return None;
        } else {
          i = i + 1;
        }
      }
      if (close < 0) {
        return None;
      }
      if (close == host_start + 1) {
        return None;
      }
      host = slice_unchecked(s, host_start + 1, close) + "";
      if (close + 1 < authority_end) {
        if (s[close + 1] != 58) {
          return None;
        }
        colon = close + 1;
      }
    } else {
      i = host_start;
      while (i < authority_end) {
        if (s[i] == 58) {
          colon = i;
          i = authority_end;
        } else if (!__url_reg_name_char(s[i] as i32)) {
          return None;
        } else {
          i = i + 1;
        }
      }
      let host_end: i32 = authority_end;
      if (colon >= 0) {
        host_end = colon;
      }
      host = slice_unchecked(s, host_start, host_end) + "";
    }

    let port: i32 = 0;
    if (colon >= 0) {
      i = colon + 1;
      while (i < authority_end) {
        let b: i32 = s[i] as i32;
        if (b < 48 || b > 57) {
          return None;
        }
        port = port * 10 + (b - 48);
        if (port > 65535) {
          return None;
        }
        i = i + 1;
      }
    }
    u = Url { ...u, host: host, port: port };
  }

  if (authority_end < query_start) {
    u = Url { ...u, path: slice_unchecked(s, authority_end, query_start) + "" };
  }

  return Some(u);
}
```

### `12 pub function query_parse(s: string): Map[string, string[]]`

```fern
<<12 pub function query_parse(s: string): Map[string, string[]]>>=
pub function query_parse(s: string): Map[string, string[]] {
  let m: Map[string, string[]] = map_new(8);
  let n: i32 = s.len();
  if (n == 0) {
    return m;
  }

  let pair_start: i32 = 0;
  let i: i32 = 0;
  while (i <= n) {
    let sep: boolean = false;
    if (i == n) {
      sep = true;
    } else if (s[i] == 38) {
      sep = true;
    }
    if (sep) {
      let pair_len: i32 = i - pair_start;
      if (pair_len > 0) {
        m = __query_pair(m, s, pair_start, i);
      }
      pair_start = i + 1;
    }
    i = i + 1;
  }
  return m;
}
```

### `13 function __query_pair(m: Map[string, string[]], s: string, start: i32, end: i32): Map[string, string[]]`

```fern
<<13 function __query_pair(m: Map[string, string[]], s: string, start: i32, end: i32): Map[string, string[]]>>=
function __query_pair(m: Map[string, string[]], s: string, start: i32, end: i32): Map[string, string[]] {
  let eq: i32 = -1;
  let j: i32 = start;
  while (j < end) {
    if (s[j] == 61) {
      eq = j;
      break;
    }
    j = j + 1;
  }
  let key: string = "";
  let val: string = "";
  if (eq >= 0) {
    key = form_decode(slice_unchecked(s, start, eq));
    val = form_decode(slice_unchecked(s, eq + 1, end));
  } else {
    key = form_decode(slice_unchecked(s, start, end));
  }
  match (m.get(key)) {
    Some(existing) => {
      m = m.insert(key, existing.append(val));
    },
    None => {
      let arr: string[] = [val];
      m = m.insert(key, arr);
    }
  }
  return m;
}

function __query_eq_pos(s: string, start: i32, end: i32): i32 {
  let j: i32 = start;
  while (j < end) {
    if (s[j] == 61) {
      return j;
    }
    j = j + 1;
  }
  return -1;
}

function __query_key_at(s: string, start: i32, end: i32): string {
  let eq: i32 = __query_eq_pos(s, start, end);
  if (eq >= 0) {
    return form_decode(slice_unchecked(s, start, eq));
  }
  return form_decode(slice_unchecked(s, start, end));
}

function __query_val_at(s: string, start: i32, end: i32): string {
  let eq: i32 = __query_eq_pos(s, start, end);
  if (eq >= 0) {
    return form_decode(slice_unchecked(s, eq + 1, end));
  }
  return "";
}
```

### `14 pub function query_get(query: string, key: string): Option[string]`

```fern
<<14 pub function query_get(query: string, key: string): Option[string]>>=
pub function query_get(query: string, key: string): Option[string] {
  let n: i32 = query.len();
  let pair_start: i32 = 0;
  let i: i32 = 0;
  while (i <= n) {
    let sep: boolean = false;
    if (i == n) {
      sep = true;
    } else if (query[i] == 38) {
      sep = true;
    }
    if (sep) {
      if (i - pair_start > 0 && __query_key_at(query, pair_start, i) == key) {
        return Some(__query_val_at(query, pair_start, i));
      }
      pair_start = i + 1;
    }
    i = i + 1;
  }
  return None;
}

pub function query_has(query: string, key: string): boolean {
  let n: i32 = query.len();
  let pair_start: i32 = 0;
  let i: i32 = 0;
  while (i <= n) {
    let sep: boolean = false;
    if (i == n) {
      sep = true;
    } else if (query[i] == 38) {
      sep = true;
    }
    if (sep) {
      if (i - pair_start > 0 && __query_key_at(query, pair_start, i) == key) {
        return true;
      }
      pair_start = i + 1;
    }
    i = i + 1;
  }
  return false;
}

pub function query_get_all(query: string, key: string): string[] {
  let out: string[] = [];
  let n: i32 = query.len();
  let pair_start: i32 = 0;
  let i: i32 = 0;
  while (i <= n) {
    let sep: boolean = false;
    if (i == n) {
      sep = true;
    } else if (query[i] == 38) {
      sep = true;
    }
    if (sep) {
      if (i - pair_start > 0 && __query_key_at(query, pair_start, i) == key) {
        out = out.append(__query_val_at(query, pair_start, i));
      }
      pair_start = i + 1;
    }
    i = i + 1;
  }
  return out;
}

pub function query_encode(pairs: (string, string)[]): string {
  let n: i32 = pairs.len();
  if (n == 0) {
    return "";
  }
  let total: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    if (i > 0) {
      total = total + 1;
    }
    let (k, v) = pairs[i];
    total = total + form_encode(k).len() + 1 + form_encode(v).len();
    i = i + 1;
  }
  let buf: u8[] = __alloc_u8(total);
  let pos: i32 = 0;
  i = 0;
  while (i < n) {
    if (i > 0) {
      buf = buf.with(pos, 38 as u8);
      pos = pos + 1;
    }
    let (k2, v2) = pairs[i];
    let ek: string = form_encode(k2);
    let ekl: i32 = ek.len();
    let a: i32 = 0;
    while (a < ekl) {
      buf = buf.with(pos, ek[a] as u8);
      pos = pos + 1;
      a = a + 1;
    }
    buf = buf.with(pos, 61 as u8);
    pos = pos + 1;
    let ev: string = form_encode(v2);
    let evl: i32 = ev.len();
    let b: i32 = 0;
    while (b < evl) {
      buf = buf.with(pos, ev[b] as u8);
      pos = pos + 1;
      b = b + 1;
    }
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}
```
