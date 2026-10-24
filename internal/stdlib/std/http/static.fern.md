# Static HTTP file responses

This module serves static files with HTTP cache validation, byte ranges, and compression.

## Model

A directory combines a filesystem root with file-response policy. Responses can apply cache validators, byte ranges, and precompressed representations.

## Contracts

Directory options control index names, dotfiles, and range count. The range parser distinguishes a whole response, selected parts, and an unsatisfiable request.

## Executable example

```fern test name=std-http-static-example
import "std/http/static";

function main(): i32 {
  if (static.content_type("index.html") != "text/html; charset=utf-8") { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct Options>>

<<4 pub function options(): Options>>

<<5 pub struct Dir>>

<<6 pub function dir(root: string): Dir>>

<<7 function __serve(req: HttpRequest, d: Dir, segs: string[], wants_dir: boolean, plat: platform.Platform): Ht...>>

<<8 struct Rep>>

<<9 function __file(req: HttpRequest, d: Dir, path: string, st: FileStat, plat: platform.Platform): HttpResponse>>

<<10 struct Ranging>>

<<11 function __ranged(req: HttpRequest, full: HttpResponse, rep: Rep, g: Ranging, plat: platform.Platform): Htt...>>

<<12 pub enum Range>>

<<13 pub function byte_range(value: string, size: i64, max: i32): Range>>

<<14 function __range_set(found: i64[], size: i64, max: i32): Range>>

<<15 pub function content_type(path: string): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module serves static files with HTTP cache validation, byte ranges, and compression.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/http";
import "std/http/cache";
import "std/headers";
import "std/hex";
import "std/platform";
import "std/time";
import "std/url";
```

### `3 pub struct Options`

```fern
<<3 pub struct Options>>=
pub struct Options { index: string, dotfiles: boolean, precompressed: string[], cache_control: Option[cache.CacheControl], max_ranges: i32 }
```

### `4 pub function options(): Options`

```fern
<<4 pub function options(): Options>>=
pub function options(): Options {
  return Options { index: "index.html", dotfiles: false, precompressed: ["br", "zstd", "gzip"], cache_control: None, max_ranges: 200 };
}
```

### `5 pub struct Dir`

```fern
<<5 pub struct Dir>>=
pub struct Dir { root: string, options: Options }
```

### `6 pub function dir(root: string): Dir`

```fern
<<6 pub function dir(root: string): Dir>>=
pub function dir(root: string): Dir {
  return Dir { root: root, options: options() };
}

pub function serve(req: HttpRequest, d: Dir, plat: platform.Platform): HttpResponse {
  let path: string = http.http_url_path_only(req.path);
  return __serve(req, d, path.split("/"), path.ends_with("/"), plat);
}

pub function serve_path(req: HttpRequest, d: Dir, rel: string, plat: platform.Platform): HttpResponse {
  return __serve(req, d, rel.split("/"), rel.len() == 0 || rel.ends_with("/"), plat);
}
```

### `7 function __serve(req: HttpRequest, d: Dir, segs: string[], wants_dir: boolean, plat: platform.Platform): Ht...`

```fern
<<7 function __serve(req: HttpRequest, d: Dir, segs: string[], wants_dir: boolean, plat: platform.Platform): Ht...>>=
function __serve(req: HttpRequest, d: Dir, segs: string[], wants_dir: boolean, plat: platform.Platform): HttpResponse {
  if (req.method != "GET" && req.method != "HEAD") {
    return http.text(405, "").with_header("Allow", "GET, HEAD");
  }
  match (__clean(segs, d.options.dotfiles)) {
    Some(rel) => {
      return __entry(req, d, d.root + rel, wants_dir, plat);
    },
    None => {
      return http.not_found();
    }
  }
}

function __clean(segs: string[], dotfiles: boolean): Option[string] {
  let out: string = "";
  for s in segs {
    if (s.len() == 0 || s == ".") {
      continue;
    }
    if (s == ".." || s.contains("\x00") || s.starts_with(".") && !dotfiles) {
      return None;
    }
    out = out + "/" + s;
  }
  return Some(out);
}

function __entry(req: HttpRequest, d: Dir, path: string, wants_dir: boolean, plat: platform.Platform): HttpResponse {
  match (plat.stat(path)) {
    Ok(st) => {
      if (st.is_dir && !wants_dir) {
        return __add_slash(req.path);
      }
      if (st.is_dir) {
        return __index(req, d, path, plat);
      }
      if (wants_dir) {
        return http.not_found();
      }
      return __file(req, d, path, st, plat);
    },
    Err(e) => {
      return __stat_failed(e);
    }
  }
}

function __index(req: HttpRequest, d: Dir, path: string, plat: platform.Platform): HttpResponse {
  if (d.options.index.len() == 0) {
    return http.not_found();
  }
  let index: string = path + "/" + d.options.index;
  match (plat.stat(index)) {
    Ok(st) => {
      if (st.is_dir) {
        return http.not_found();
      }
      return __file(req, d, index, st, plat);
    },
    Err(e) => {
      return __stat_failed(e);
    }
  }
}

function __add_slash(target: string): HttpResponse {
  let path: string = http.http_url_path_only(target);
  let query: string = target.slice_snap(path.len(), target.len());
  let base: string = __last_segment(path);
  return HttpResponse { ...http.redirect(url.url_encode(base) + "/" + query), status: 301 };
}

function __last_segment(path: string): string {
  let Some(slash) = path.last_index_of("/") else {
    return path;
  };
  return path.slice_snap(slash + 1, path.len());
}

function __stat_failed(e: IoError): HttpResponse {
  match (e) {
    PermissionDenied(p) => {
      return http.text(403, "Forbidden");
    },
    Other(p, msg, errno) => {
      if (errno != 20 && errno != 36 && errno != 40) {
        return http.internal_error("");
      }
      return http.not_found();
    },
    _ => {
      return http.not_found();
    }
  }
}
```

### `8 struct Rep`

```fern
<<8 struct Rep>>=
struct Rep { path: string, st: FileStat, coding: string }
```

### `9 function __file(req: HttpRequest, d: Dir, path: string, st: FileStat, plat: platform.Platform): HttpResponse`

```fern
<<9 function __file(req: HttpRequest, d: Dir, path: string, st: FileStat, plat: platform.Platform): HttpResponse>>=
function __file(req: HttpRequest, d: Dir, path: string, st: FileStat, plat: platform.Platform): HttpResponse {
  let rep: Rep = __representation(req, d.options.precompressed, Rep { path: path, st: st, coding: "" }, plat);
  let now: Instant = time.instant_from_unix(plat.now_ms() / (1000 as i64));
  let v: cache.Validators = cache.Validators { etag: Some(__etag(rep)), last_modified: Some(time.instant_from_unix(rep.st.mtime)) };
  let full: HttpResponse = __headed(rep, d.options, content_type(path)).with_validators(v, now);
  match (cache.evaluate(req, v, now)) {
    Proceed => {
      return __ranged(req, full, rep, Ranging { v: v, now: now, max: d.options.max_ranges }, plat);
    },
    NotModified => {
      return cache.not_modified(full);
    },
    PreconditionFailed => {
      return cache.precondition_failed();
    }
  }
}

function __representation(req: HttpRequest, codings: string[], plain: Rep, plat: platform.Platform): Rep {
  if (codings.len() == 0 || !req.headers.has("accept-encoding")) {
    return plain;
  }
  let found: Rep[] = [];
  let offers: string[] = [];
  for c in codings {
    let sibling: string = plain.path + __suffix(c);
    match (plat.stat(sibling)) {
      Ok(st) => {
        if (st.is_file && __suffix(c).len() > 0) {
          found = found.append(Rep { path: sibling, st: st, coding: c });
          offers = offers.append(c);
        }
      },
      Err(e) => {}
    }
  }
  match (req.headers.preferred_coding(offers)) {
    Some(c) => {
      return found[__position(offers, c)];
    },
    None => {
      return plain;
    }
  }
}

function __position(xs: string[], x: string): i32 {
  let i: i32 = 0;
  while (i < xs.len() && xs[i] != x) {
    i = i + 1;
  }
  return i;
}

function __suffix(coding: string): string {
  if (coding == "br") {
    return ".br";
  }
  if (coding == "zstd") {
    return ".zst";
  }
  if (coding == "gzip") {
    return ".gz";
  }
  return "";
}

function __etag(rep: Rep): cache.ETag {
  let tag: string = rep.st.mtime.to_string() + "." + rep.st.mtime_nsec.to_string() + "-" + rep.st.size.to_string();
  if (rep.coding.len() > 0) {
    tag = tag + "-" + rep.coding;
  }
  return cache.strong(tag);
}

function __headed(rep: Rep, o: Options, ctype: string): HttpResponse {
  let h: HeaderMap = headers.header_map_new().append("Content-Type", ctype).append("Accept-Ranges", "bytes");
  if (rep.coding.len() > 0) {
    h = h.append("Content-Encoding", rep.coding);
  }
  if (o.precompressed.len() > 0) {
    h = h.append("Vary", "Accept-Encoding");
  }
  let resp: HttpResponse = HttpResponse { status: 200, body: __file_body(rep.path, 0 as i64, rep.st.size), headers: h, trailers: headers.header_map_new(), background: None };
  match (o.cache_control) {
    Some(cc) => {
      return resp.with_cache_control(cc);
    },
    None => {
      return resp;
    }
  }
}

function __file_body(path: string, start: i64, len: i64): Body {
  let none: u8[] = [];
  return BodyFileRanges(path, [start, len], [none, none]);
}
```

### `10 struct Ranging`

```fern
<<10 struct Ranging>>=
struct Ranging { v: cache.Validators, now: Instant, max: i32 }
```

### `11 function __ranged(req: HttpRequest, full: HttpResponse, rep: Rep, g: Ranging, plat: platform.Platform): Htt...`

```fern
<<11 function __ranged(req: HttpRequest, full: HttpResponse, rep: Rep, g: Ranging, plat: platform.Platform): Htt...>>=
function __ranged(req: HttpRequest, full: HttpResponse, rep: Rep, g: Ranging, plat: platform.Platform): HttpResponse {
  let fields: string[] = req.headers.get_all("range");
  if (req.method != "GET" || fields.len() != 1 || !cache.if_range(req, g.v, g.now)) {
    return full;
  }
  let size: i64 = rep.st.size;
  match (byte_range(fields[0], size, g.max)) {
    Part(start, len) => {
      return HttpResponse { ...full, status: 206, body: __file_body(rep.path, start, len) }.with_header("Content-Range", __content_range(start, len, size));
    },
    Parts(spans) => {
      return __byteranges(full, rep, spans, __boundary(plat));
    },
    Unsatisfiable => {
      return http.text(416, "").with_header("Content-Range", "bytes */" + size.to_string());
    },
    Whole => {
      return full;
    }
  }
}

function __content_range(start: i64, len: i64, size: i64): string {
  return "bytes " + start.to_string() + "-" + (start + len - 1 as i64).to_string() + "/" + size.to_string();
}

function __byteranges(full: HttpResponse, rep: Rep, spans: i64[], boundary: string): HttpResponse {
  let ctype: string = "application/octet-stream";
  match (full.headers.get("content-type")) {
    Some(t) => {
      ctype = t;
    },
    None => {}
  }
  let seams: u8[][] = [];
  let lead: string = "";
  let i: i32 = 0;
  while (i + 1 < spans.len()) {
    let head: string = lead + "--" + boundary + "\r\nContent-Type: " + ctype + "\r\nContent-Range: " + __content_range(spans[i], spans[i + 1], rep.st.size) + "\r\n\r\n";
    seams = seams.append(head.bytes());
    lead = "\r\n";
    i = i + 2;
  }
  seams = seams.append(("\r\n--" + boundary + "--\r\n").bytes());
  let h: HeaderMap = full.headers.set("Content-Type", "multipart/byteranges; boundary=" + boundary);
  return HttpResponse { ...full, status: 206, body: BodyFileRanges(rep.path, spans, seams), headers: h };
}

function __boundary(plat: platform.Platform): string {
  let bytes: u8[] = [];
  for k in [0, 1, 2, 3] {
    let x: i32 = plat.random_i32();
    for shift in [0, 8, 16, 24] {
      bytes = bytes.append((x >> shift & 255) as u8);
    }
  }
  return hex.encode(bytes);
}
```

### `12 pub enum Range`

```fern
<<12 pub enum Range>>=
pub enum Range { Whole, Part(i64, i64), Parts(i64[]), Unsatisfiable }
```

### `13 pub function byte_range(value: string, size: i64, max: i32): Range`

```fern
<<13 pub function byte_range(value: string, size: i64, max: i32): Range>>=
pub function byte_range(value: string, size: i64, max: i32): Range {
  let Some(eq) = value.index_of("=") else {
    return Whole;
  };
  if (!value.slice_snap(0, eq).trim().eq_ignore_ascii_case("bytes")) {
    return Whole;
  }
  let specs: string[] = headers.split_list(value.slice_snap(eq + 1, value.len()));
  if (specs.len() == 0) {
    return Whole;
  }
  let found: i64[] = [];
  for spec in specs {
    match (__range_spec(spec, size)) {
      Part(start, len) => {
        found = found.append(start).append(len);
      },
      Whole => {
        return Whole;
      },
      _ => {}
    }
  }
  return __range_set(found, size, max);
}
```

### `14 function __range_set(found: i64[], size: i64, max: i32): Range`

```fern
<<14 function __range_set(found: i64[], size: i64, max: i32): Range>>=
function __range_set(found: i64[], size: i64, max: i32): Range {
  if (found.len() == 0) {
    return Unsatisfiable;
  }
  if (found.len() == 2) {
    return Part(found[0], found[1]);
  }
  let total: i64 = 0 as i64;
  let i: i32 = 1;
  while (i < found.len()) {
    total = total + found[i];
    i = i + 2;
  }
  if (found.len() / 2 > max || total > size) {
    return Whole;
  }
  return Parts(found);
}

function __range_spec(spec: string, size: i64): Range {
  let Some(dash) = spec.index_of("-") else {
    return Whole;
  };
  let last_text: string = spec.slice_snap(dash + 1, spec.len());
  if (dash == 0) {
    return __suffix_range(__digits(last_text), size);
  }
  let first: i64 = __digits(spec.slice_snap(0, dash));
  let last: i64 = size - 1 as i64;
  if (last_text.len() > 0) {
    last = __digits(last_text);
  }
  if (first < 0 as i64 || last_text.len() > 0 && last < first) {
    return Whole;
  }
  if (first >= size) {
    return Unsatisfiable;
  }
  if (last >= size) {
    last = size - 1 as i64;
  }
  return Part(first, last - first + 1 as i64);
}

function __suffix_range(n: i64, size: i64): Range {
  if (n < 0 as i64) {
    return Whole;
  }
  if (n == 0 as i64 || size == 0 as i64) {
    return Unsatisfiable;
  }
  if (n > size) {
    return Part(0 as i64, size);
  }
  return Part(size - n, n);
}

function __digits(s: string): i64 {
  if (s.len() == 0) {
    return 0 - 1 as i64;
  }
  let cap: i64 = 4611686018427387904;
  let n: i64 = 0 as i64;
  for b in s.bytes() {
    if (b < 48 as u8 || b > 57 as u8) {
      return 0 - 1 as i64;
    }
    if (n < cap) {
      n = n * (10 as i64) + (b - 48 as u8) as i64;
    }
  }
  return n;
}
```

### `15 pub function content_type(path: string): string`

```fern
<<15 pub function content_type(path: string): string>>=
pub function content_type(path: string): string {
  let name: string = __last_segment(path);
  let Some(dot) = name.last_index_of(".") else {
    return "application/octet-stream";
  };
  let ext: string = name.slice_snap(dot + 1, name.len()).to_lower();
  let exts: string[] = ["html", "htm", "css", "js", "mjs", "json", "map", "txt", "md", "csv", "xml", "svg", "wasm", "png", "jpg", "jpeg", "gif", "webp", "avif", "ico", "pdf", "woff", "woff2", "ttf", "otf", "mp4", "webm", "mp3", "ogg", "wav", "zip", "webmanifest"];
  let types: string[] = ["text/html; charset=utf-8", "text/html; charset=utf-8", "text/css; charset=utf-8", "text/javascript; charset=utf-8", "text/javascript; charset=utf-8", "application/json", "application/json", "text/plain; charset=utf-8", "text/markdown; charset=utf-8", "text/csv; charset=utf-8", "application/xml", "image/svg+xml", "application/wasm", "image/png", "image/jpeg", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/vnd.microsoft.icon", "application/pdf", "font/woff", "font/woff2", "font/ttf", "font/otf", "video/mp4", "video/webm", "audio/mpeg", "audio/ogg", "audio/wav", "application/zip", "application/manifest+json"];
  let i: i32 = __position(exts, ext);
  if (i < exts.len()) {
    return types[i];
  }
  return "application/octet-stream";
}
```
