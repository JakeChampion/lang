# HTTP requests and responses

This module parses HTTP/1.1 requests and constructs responses.

## Model

The request parser separates the message start, headers, and body rules. Response constructors provide status-specific values through one response representation.

## Contracts

Limits and frame rules determine how much input belongs to a message. Protocol errors retain a status and an explanation. A response can also come from a `ToResponse` implementation.

## Executable example

```fern test name=std-http-example
import "std/http";

function main(): i32 {
  if (!http.http_is_success(204) || http.http_is_success(404)) { return 1; }
  if (http.http_status_text(404) != "Not Found") { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub function http_status_text(code: i32): string>>

<<4 function __http_response_typed(status: i32, body: string, ct: string): HttpResponse>>

<<5 pub function json(body: string): HttpResponse>>

<<6 pub trait ToResponse>>

<<7 pub function respond[E: ToResponse](result: Result[HttpResponse, E]): HttpResponse>>

<<8 pub struct HttpError>>

<<9 pub function (e: HttpError) message(): string>>

<<10 impl error.Error for HttpError>>

<<11 pub function fail(status: i32, detail: string): HttpError>>

<<12 impl ToResponse for HttpError>>

<<13 pub function (e: BodyError) message(): string>>

<<14 impl error.Error for BodyError>>

<<15 function __ended_early_status(fault: i32): i32>>

<<16 pub function body_json[T: json.FromJson](req: HttpRequest): Result[T, BodyError]>>

<<17 function __media_type_is_json(ct: string): boolean>>

<<18 pub function http_path_segments(path: string): string[]>>

<<19 function __find_crlf(buf: u8[], from: i32, end: i32): i32>>

<<20 pub struct HttpLimits>>

<<21 pub function http_limits(): HttpLimits>>

<<22 const NOT_TCHAR: u8[]>>

<<23 function __is_token(buf: u8[], from: i32, end: i32): boolean>>

<<24 function __crlf_terminated(buf: u8[], from: i32, end: i32): boolean>>

<<25 pub function http_header_block_complete(buf: u8[], from: i32, limits: HttpLimits): boolean>>

<<26 function __find_crlf_crlf(buf: u8[], from: i32, end: i32): i32>>

<<27 enum __BodyFraming>>

<<28 function __http_body_framing(h: HeaderMap, cl: i32, te: i32): Result[__BodyFraming, i32]>>

<<29 function __chunk_size_line(buf: u8[], at: i32, budget: i32, room: i32): (i32, i32, i32)>>

<<30 function __chunked_trailers(buf: u8[], at: i32, budget: i32, spans: i32[], fields: i32): (i32, i32, i32[], ...>>

<<31 pub struct ChunkDecoder>>

<<32 pub function chunk_decoder(limits: HttpLimits): ChunkDecoder>>

<<33 function __chunk_step(d: ChunkDecoder, buf: u8[], at: i32, out: u8[]): (i32, ChunkDecoder, i32, u8[])>>

<<34 pub function http_parse_request_bytes(buf: u8[]): Option[HttpRequest]>>

<<35 pub struct HttpFramed>>

<<36 function __expectation(h: HeaderMap, at: i32): i32>>

<<37 pub function http_parse_request_framed(buf: u8[]): HttpFraming>>

<<38 function __framed_new(head: __Head, body: u8[], trailers: HeaderMap, len: i32): HttpFramed>>

<<39 pub struct HttpHead>>

<<40 pub function http_parse_request_head_from(buf: u8[], from: i32, limits: HttpLimits, prev: HttpRequest): Htt...>>

<<41 struct __Head>>

<<42 function __request_head(buf: u8[], from: i32, limits: HttpLimits, prev: HttpRequest): Result[__Head, i32]>>

<<43 pub function http_request_target(method: string, target: string): Result[string, i32]>>

<<44 function __target_path(buf: u8[], from: i32, end: i32, options: boolean, prev: string): Result[string, i32]>>

<<45 const ASTERISK_FORM: i32>>

<<46 function __target_start(buf: u8[], from: i32, end: i32, options: boolean): i32>>

<<47 function __plain_path(buf: u8[], from: i32, end: i32): boolean>>

<<48 function __version_status(buf: u8[], from: i32, end: i32): i32>>

<<49 pub function http_parse_request(buf: string): Option[HttpRequest]>>

<<50 function __http_reason(code: i32): string>>

<<51 pub function http_serialize_response(resp: HttpResponse): string>>

<<52 function __push_head(b: usize, resp: HttpResponse): void>>

<<53 pub function request(method: string, path: string): HttpRequest>>

<<54 function __query_of(path: string): string>>

<<55 pub function (r: HttpRequest) body_stream(): ByteStream>>

<<56 function __whole_body(s: ByteStream): (u8[], ByteStream)>>

<<57 pub function (r: HttpRequest) body_bytes(): u8[]>>

<<58 function __declared_length(h: HeaderMap): i32>>

<<59 pub function (r: HttpResponse) body_string(): string>>

<<60 function __chunks_joined(producer: ChunkProducer): u8[]>>

<<61 pub function (r: HttpResponse) body_len(): i32>>

<<62 function __file_ranges_read(path: string, ranges: i64[], seams: u8[][]): Option[u8[]]>>

<<63 pub function file_ranges_ok(ranges: i64[], seams: u8[][]): boolean>>

<<64 function __read_span(r: Reader, offset: i64, len: i64): Option[u8[]]>>

<<65 pub function bytes(status: i32, bs: u8[]): HttpResponse>>

<<66 function __no_trailers(): HeaderMap>>

<<67 pub function chunks_idle(status: i32, next: (i32)>>

<<68 pub struct HttpResponseFramed>>

<<69 pub function http_parse_response_framed(buf: u8[], method: string, eof: boolean, limits: HttpLimits): HttpR...>>

<<70 function __response_head_refused(buf: u8[], from: i32, first_crlf: i32, headers_end: i32, bare: boolean): i32>>

<<71 pub function http_hop_by_hop(name: string): boolean>>

<<72 function __lists_token(value: string, want: string): boolean>>

<<73 pub function (r: HttpResponse) body_text(): Option[string]>>

<<74 function __str_is_token(s: string): boolean>>

<<75 pub function http_method_idempotent(method: string): boolean>>
```


### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/i32";
import "core/int";
import "core/map";
import "std/string";
import "std/headers";
import "std/io_buffered" as io_buffered;
import "std/json";
import "std/stream";
import "std/error";
import "std/platform";
import "std/url";
import "std/utf8" as utf8;
import "core/cmp";
```

### `3 pub function http_status_text(code: i32): string`

```fern
<<3 pub function http_status_text(code: i32): string>>=
pub function http_status_text(code: i32): string {
  let codes: i32[] = [
    100, 101, 200, 201, 202, 203, 204, 205, 206, 300, 301, 302, 303, 304, 305, 307, 308,
    400, 401, 402, 403, 404, 405, 406, 407, 408, 409, 410, 411, 412, 413, 414, 415, 416, 417, 418, 421, 422, 426, 428, 429, 431,
    500, 501, 502, 503, 504, 505, 511,
  ];
  let phrases: string[] = [
    "Continue", "Switching Protocols", "OK", "Created", "Accepted", "Non-Authoritative Information", "No Content", "Reset Content", "Partial Content", "Multiple Choices", "Moved Permanently", "Found", "See Other", "Not Modified", "Use Proxy", "Temporary Redirect", "Permanent Redirect",
    "Bad Request", "Unauthorized", "Payment Required", "Forbidden", "Not Found", "Method Not Allowed", "Not Acceptable", "Proxy Authentication Required", "Request Timeout", "Conflict", "Gone", "Length Required", "Precondition Failed", "Payload Too Large", "URI Too Long", "Unsupported Media Type", "Range Not Satisfiable", "Expectation Failed", "I'm a teapot", "Misdirected Request", "Unprocessable Entity", "Upgrade Required", "Precondition Required", "Too Many Requests", "Request Header Fields Too Large",
    "Internal Server Error", "Not Implemented", "Bad Gateway", "Service Unavailable", "Gateway Timeout", "HTTP Version Not Supported", "Network Authentication Required",
  ];
  let i: i32 = 0;
  while (i < codes.len()) {
    if (codes[i] == code) {
      return phrases[i];
    }
    i = i + 1;
  }
  return "";
}

pub function is_valid_http_status(code: i32): boolean {
  return code >= 100 && code <= 599;
}

pub function http_is_informational(code: i32): boolean {
  return code >= 100 && code <= 199;
}

pub function http_is_success(code: i32): boolean {
  return code >= 200 && code <= 299;
}

pub function http_is_redirect(code: i32): boolean {
  return code >= 300 && code <= 399;
}

pub function http_is_client_error(code: i32): boolean {
  return code >= 400 && code <= 499;
}

pub function http_is_server_error(code: i32): boolean {
  return code >= 500 && code <= 599;
}

pub function http_is_error(code: i32): boolean {
  return code >= 400 && code <= 599;
}

pub function ok(body: string): HttpResponse {
  return HttpResponse { status: 200, body: BodyText(body), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
}

pub function text(status: i32, body: string): HttpResponse {
  return HttpResponse { status: status, body: BodyText(body), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
}

pub function not_found(): HttpResponse {
  return HttpResponse { status: 404, body: BodyText("Not Found"), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
}

pub function bad_request(body: string): HttpResponse {
  return HttpResponse { status: 400, body: BodyText(body), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
}

pub function internal_error(body: string): HttpResponse {
  return HttpResponse { status: 500, body: BodyText(body), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
}

pub function redirect(location: string): HttpResponse {
  let h: HeaderMap = headers.header_map_new();
  h = h.set("Location", location);
  return HttpResponse { status: 302, body: BodyText(location), headers: h, trailers: headers.header_map_new(), background: None };
}

pub function no_content(): HttpResponse {
  return HttpResponse { status: 204, body: BodyText(""), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
}

pub function created(body: string): HttpResponse {
  return HttpResponse { status: 201, body: BodyText(body), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
}
```

### `4 function __http_response_typed(status: i32, body: string, ct: string): HttpResponse`

```fern
<<4 function __http_response_typed(status: i32, body: string, ct: string): HttpResponse>>=
function __http_response_typed(status: i32, body: string, ct: string): HttpResponse {
  let h: HeaderMap = headers.header_map_new();
  h = h.set("Content-Type", ct);
  return HttpResponse { status: status, body: BodyText(body), headers: h, trailers: headers.header_map_new(), background: None };
}
```

### `5 pub function json(body: string): HttpResponse`

```fern
<<5 pub function json(body: string): HttpResponse>>=
pub function json(body: string): HttpResponse {
  return __http_response_typed(200, body, "application/json");
}

pub function json_status(status: i32, body: string): HttpResponse {
  return __http_response_typed(status, body, "application/json");
}

pub function json_of[T: json.Json](v: T): HttpResponse {
  return __http_response_typed(200, v.to_json(), "application/json");
}

pub function json_of_status[T: json.Json](status: i32, v: T): HttpResponse {
  return __http_response_typed(status, v.to_json(), "application/json");
}

pub function html(body: string): HttpResponse {
  return __http_response_typed(200, body, "text/html; charset=utf-8");
}

pub function plain(body: string): HttpResponse {
  return __http_response_typed(200, body, "text/plain; charset=utf-8");
}
```

### `6 pub trait ToResponse`

```fern
<<6 pub trait ToResponse>>=
pub trait ToResponse {
  function to_response(self: Self): HttpResponse;
}
```

### `7 pub function respond[E: ToResponse](result: Result[HttpResponse, E]): HttpResponse`

```fern
<<7 pub function respond[E: ToResponse](result: Result[HttpResponse, E]): HttpResponse>>=
pub function respond[E: ToResponse](result: Result[HttpResponse, E]): HttpResponse {
  match (result) {
    Ok(r) => {
      return r;
    },
    Err(e) => {
      return e.to_response();
    }
  }
  return problem(500, "", "");
}

pub function respond_with[S, E: ToResponse](pair: (S, Result[HttpResponse, E])): (S, HttpResponse) {
  return (pair.0, respond(pair.1));
}

pub function respond_error(result: Result[HttpResponse, dyn error.Error], plat: platform.Platform): HttpResponse {
  match (result) {
    Ok(r) => {
      return r;
    },
    Err(e) => {
      plat.log("handler error: " + e.message());
      return problem(500, "", "");
    }
  }
  return problem(500, "", "");
}

pub function respond_error_with[S](pair: (S, Result[HttpResponse, dyn error.Error]), plat: platform.Platform): (S, HttpResponse) {
  return (pair.0, respond_error(pair.1, plat));
}

pub function problem(status: i32, title: string, detail: string): HttpResponse {
  let t: string = title;
  if (t.len() == 0) {
    t = http_status_text(status);
  }
  let body: string = "{\"type\":\"about:blank\",\"title\":\"" + json.json_escape(t) + "\",\"status\":" + int.int_to_string(status);
  if (detail.len() > 0) {
    body = body + ",\"detail\":\"" + json.json_escape(detail) + "\"";
  }
  return __http_response_typed(status, body + "}", "application/problem+json");
}
```

### `8 pub struct HttpError`

```fern
<<8 pub struct HttpError>>=
pub struct HttpError { status: i32, title: string, detail: string }
```

### `9 pub function (e: HttpError) message(): string`

```fern
<<9 pub function (e: HttpError) message(): string>>=
pub function (e: HttpError) message(): string {
  let title: string = e.title;
  if (title.len() == 0) {
    title = http_status_text(e.status);
  }
  if (e.detail.len() > 0) {
    return title + ": " + e.detail;
  }
  return title;
}

pub function (e: HttpError) to_string(): string {
  return e.message();
}
```

### `10 impl error.Error for HttpError`

```fern
<<10 impl error.Error for HttpError>>=
impl error.Error for HttpError {}

impl cmp.Display for HttpError {}
```

### `11 pub function fail(status: i32, detail: string): HttpError`

```fern
<<11 pub function fail(status: i32, detail: string): HttpError>>=
pub function fail(status: i32, detail: string): HttpError {
  return HttpError { status: status, title: http_status_text(status), detail: detail };
}
```

### `12 impl ToResponse for HttpError`

```fern
<<12 impl ToResponse for HttpError>>=
impl ToResponse for HttpError {
  function to_response(self: HttpError): HttpResponse {
    return problem(self.status, self.title, self.detail);
  }
}

impl ToResponse for json.JsonError {
  function to_response(self: json.JsonError): HttpResponse {
    return problem(400, "Malformed JSON", self.message + " at line " + int.int_to_string(self.line) + ", column " + int.int_to_string(self.col));
  }
}

impl ToResponse for string {
  function to_response(self: string): HttpResponse {
    return problem(500, "", "");
  }
}

pub enum BodyError {
  UnsupportedMediaType(string),
  MalformedJson(json.JsonError),
  WrongShape(string),
  NotUtf8,
  EndedEarly(i32),
}
```

### `13 pub function (e: BodyError) message(): string`

```fern
<<13 pub function (e: BodyError) message(): string>>=
pub function (e: BodyError) message(): string {
  match (e) {
    UnsupportedMediaType(ct) => {
      if (ct.len() == 0) {
        return "expected a Content-Type of application/json";
      }
      return "expected application/json, got " + ct;
    },
    MalformedJson(why) => {
      return why.to_string();
    },
    WrongShape(why) => {
      return why;
    },
    NotUtf8 => {
      return "body is not valid UTF-8";
    },
    EndedEarly(fault) => {
      return __ended_early_detail(fault);
    }
  }
  return "";
}

pub function (e: BodyError) to_string(): string {
  return e.message();
}
```

### `14 impl error.Error for BodyError`

```fern
<<14 impl error.Error for BodyError>>=
impl error.Error for BodyError {}

impl cmp.Display for BodyError {}

impl ToResponse for BodyError {
  function to_response(self: BodyError): HttpResponse {
    match (self) {
      NotUtf8 => {
        return problem(400, "", "body is not valid UTF-8");
      },
      EndedEarly(fault) => {
        return problem(__ended_early_status(fault), "", __ended_early_detail(fault));
      },
      UnsupportedMediaType(ct) => {
        if (ct.len() == 0) {
          return problem(415, "", "expected a Content-Type of application/json");
        }
        return problem(415, "", "expected application/json, got " + ct);
      },
      MalformedJson(e) => {
        return problem(400, "Malformed JSON", e.to_string());
      },
      WrongShape(why) => {
        return problem(422, "", why);
      }
    }
    return problem(500, "", "");
  }
}
```

### `15 function __ended_early_status(fault: i32): i32`

```fern
<<15 function __ended_early_status(fault: i32): i32>>=
function __ended_early_status(fault: i32): i32 {
  if (fault >= 400 && fault < 600) {
    return fault;
  }
  return 500;
}

function __ended_early_detail(fault: i32): string {
  if (fault == 413) {
    return "the body is past the limit";
  }
  if (fault == 408) {
    return "the body arrived too slowly";
  }
  if (fault == 400) {
    return "the body was malformed or cut short";
  }
  if (fault == 431) {
    return "the body's framing is past the limit";
  }
  return "the body did not arrive whole";
}
```

### `16 pub function body_json[T: json.FromJson](req: HttpRequest): Result[T, BodyError]`

```fern
<<16 pub function body_json[T: json.FromJson](req: HttpRequest): Result[T, BodyError]>>=
pub function body_json[T: json.FromJson](req: HttpRequest): Result[T, BodyError] {
  match (req.headers.get("content-type")) {
    Some(ct) => {
      if (!__media_type_is_json(ct)) {
        return Err(UnsupportedMediaType(ct));
      }
    },
    None => {
      return Err(UnsupportedMediaType(""));
    }
  }
  let text: string = req.body_string()?;
  match (json.parse(text)) {
    Ok(v) => {
      match (T.from_json_value(v)) {
        Ok(t) => {
          return Ok(t);
        },
        Err(why) => {
          return Err(WrongShape(why.to_string()));
        }
      }
    },
    Err(e) => {
      return Err(MalformedJson(e));
    }
  }
  return Err(UnsupportedMediaType(""));
}
```

### `17 function __media_type_is_json(ct: string): boolean`

```fern
<<17 function __media_type_is_json(ct: string): boolean>>=
function __media_type_is_json(ct: string): boolean {
  let mt: string = ct;
  if let Some(semi) = ct.index_of(";") {
    mt = slice_unchecked(ct, 0, semi) + "";
  }
  mt = mt.trim();
  if (mt.len() == 16 && mt.starts_with_ci("application/json")) {
    return true;
  }
  return mt.ends_with_ci("+json");
}
```

### `18 pub function http_path_segments(path: string): string[]`

```fern
<<18 pub function http_path_segments(path: string): string[]>>=
pub function http_path_segments(path: string): string[] {
  let n: i32 = path.len();
  if let Some(q) = path.index_of("?") {
    n = q;
  }
  let out: string[] = [];
  let i: i32 = 0;
  while (i < n) {
    while (i < n && path[i] == 47) {
      i = i + 1;
    }
    if (i >= n) {
      return out;
    }
    let start: i32 = i;
    while (i < n && path[i] != 47) {
      i = i + 1;
    }
    out = out.append(slice_unchecked(path, start, i) + "");
  }
  return out;
}

pub function http_url_path_only(path: string): string {
  let Some(q) = path.index_of("?") else {
    return path;
  };
  return slice_unchecked(path, 0, q) + "";
}

pub function http_user_agent_is_bot(ua: string): boolean {
  if (ua.contains_ci("bot")) {
    return true;
  }
  if (ua.contains_ci("crawler")) {
    return true;
  }
  if (ua.contains_ci("spider")) {
    return true;
  }
  if (ua.contains_ci("slurp")) {
    return true;
  }
  return false;
}

pub function http_header_value(block: string, key: string): Option[string] {
  let bs: u8[] = block.bytes();
  match (__parse_header_bytes(bs, 0, bs.len(), http_limits().header_fields, headers.header_map_new())) {
    Ok(h) => {
      return h.get(key);
    },
    Err(_) => {
      return None;
    }
  }
  return None;
}
```

### `19 function __find_crlf(buf: u8[], from: i32, end: i32): i32`

```fern
<<19 function __find_crlf(buf: u8[], from: i32, end: i32): i32>>=
function __find_crlf(buf: u8[], from: i32, end: i32): i32 {
  let i: i32 = __memchr_bytes(buf, 13, from);
  while (i >= 0 && i + 1 < end) {
    if (buf[i + 1] == 10 as u8) {
      return i;
    }
    i = __memchr_bytes(buf, 13, i + 1);
  }
  return -1;
}

function __find_byte(buf: u8[], from: i32, end: i32, b: u8): i32 {
  let i: i32 = __memchr_bytes(buf, b as i32, from);
  if (i < end) {
    return i;
  }
  return -1;
}

function __line_end(buf: u8[], from: i32, end: i32): i32 {
  let cr: i32 = __memchr_bytes(buf, 13, from);
  if (cr < 0 || cr >= end) {
    return end;
  }
  if (cr + 1 >= end || buf[cr + 1] != 10 as u8) {
    return -1;
  }
  return cr;
}

function __bytes_string(buf: u8[], from: i32, end: i32): string {
  return string_from_bytes_range_unchecked(buf, from, end);
}

function __shared_string(buf: u8[], from: i32, end: i32, prev: string): string {
  if (prev.len() == end - from && __bytes_are(buf, from, prev)) {
    return prev;
  }
  return __bytes_string(buf, from, end);
}
```

### `20 pub struct HttpLimits`

```fern
<<20 pub struct HttpLimits>>=
pub struct HttpLimits { request_line: i32, header_bytes: i32, header_fields: i32, body: i32 }
```

### `21 pub function http_limits(): HttpLimits`

```fern
<<21 pub function http_limits(): HttpLimits>>=
pub function http_limits(): HttpLimits {
  return HttpLimits { request_line: 8192, header_bytes: 32768, header_fields: 100, body: 1048576 };
}

pub function http_request_bytes_cap(limits: HttpLimits): i32 {
  return limits.header_bytes + 4 + limits.body + limits.header_bytes;
}
```

### `22 const NOT_TCHAR: u8[]`

```fern
<<22 const NOT_TCHAR: u8[]>>=
const NOT_TCHAR: u8[] = [
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 0u8, 1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 1u8, 0u8, 0u8, 1u8, 0u8, 0u8, 1u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 1u8, 1u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 0u8, 1u8, 0u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
];

const NOT_LOWER_TCHAR: u8[] = [
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 0u8, 1u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 1u8, 0u8, 0u8, 1u8, 0u8, 0u8, 1u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 0u8, 1u8, 0u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
];

const FIELD_CTL: u8[] = [
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 0u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8, 1u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8,
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8,
];

const CR_LF: u8[] = [
  0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 1u8, 0u8, 0u8, 1u8,
];

const HTTP_SLASH: u8[] = [72u8, 84u8, 84u8, 80u8, 47u8];
```

### `23 function __is_token(buf: u8[], from: i32, end: i32): boolean`

```fern
<<23 function __is_token(buf: u8[], from: i32, end: i32): boolean>>=
function __is_token(buf: u8[], from: i32, end: i32): boolean {
  return end > from && __token_end(buf, from, end) == end;
}

function __token_is(s: string, want: string): boolean {
  return __token_at(s, 0, s.len(), want);
}

function __token_at(s: string, from: i32, end: i32, want: string): boolean {
  while (from < end && __is_ows(s[from])) {
    from = from + 1;
  }
  while (end > from && __is_ows(s[end - 1])) {
    end = end - 1;
  }
  if (end - from != want.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < want.len()) {
    if (__ascii_lower(s[from + i]) != __ascii_lower(want[i])) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __is_ows(b: u8): boolean {
  return b == 32 as u8 || b == 9 as u8;
}

function __ascii_lower(b: u8): i32 {
  let v: i32 = b as i32;
  if (v >= 65 && v <= 90) {
    return v + 32;
  }
  return v;
}

function __field_value(buf: u8[], from: i32, end: i32, prev: HeaderMap, at: i32): Option[string] {
  while (from < end && __is_ows(buf[from])) {
    from = from + 1;
  }
  while (end > from && __is_ows(buf[end - 1])) {
    end = end - 1;
  }
  if (at >= 0 && prev.values[at].len() == end - from && __bytes_are(buf, from, prev.values[at])) {
    return Some(prev.values[at]);
  }
  if (__scan_set_bytes(buf, from, FIELD_CTL) < end) {
    return None;
  }
  let v: string = __bytes_string(buf, from, end);
  if (!utf8.is_valid(v)) {
    return None;
  }
  return Some(v);
}

function __parse_header_bytes(buf: u8[], from: i32, end: i32, fields: i32, prev: HeaderMap): Result[HeaderMap, i32] {
  let h: HeaderMap = headers.header_map_new();
  let same: boolean = true;
  let count: i32 = 0;
  let at: i32 = from;
  while (at < end) {
    let split: (i32, i32) = __field_line(buf, at, end);
    let col: i32 = split.0;
    let e: i32 = split.1;
    if (col < 0) {
      return Err(400);
    }
    count = count + 1;
    if (count > fields) {
      return Err(431);
    }
    let i: i32 = count - 1;
    if (same && i >= prev.names.len()) {
      if (i > 0) {
        h = __leading_fields(prev, i);
      }
      same = false;
    }
    let at_prev: i32 = 0 - 1;
    if (same) {
      at_prev = i;
    }
    match (__field_value(buf, col + 1, e, prev, at_prev)) {
      Some(v) => {
        if (same) {
          let name: string = __shared_string(buf, at, col, prev.names[i]);
          same = name == prev.names[i] && v == prev.values[i];
          if (!same) {
            h = __leading_fields(prev, i).append(name, v);
          }
        } else {
          h = h.append(__bytes_string(buf, at, col), v);
        }
      },
      None => {
        return Err(400);
      }
    }
    at = e + 2;
  }
  if (same && count == prev.names.len()) {
    return Ok(prev);
  }
  if (same) {
    return Ok(__leading_fields(prev, count));
  }
  return Ok(h);
}

function __field_line(buf: u8[], at: i32, end: i32): (i32, i32) {
  let col: i32 = __token_end(buf, at, end);
  if (col == at || col >= end || buf[col] != 58 as u8) {
    return (-1, end);
  }
  let e: i32 = __line_end(buf, col + 1, end);
  if (e < 0) {
    return (-1, end);
  }
  return (col, e);
}

function __leading_fields(h: HeaderMap, n: i32): HeaderMap {
  let out: HeaderMap = headers.header_map_new();
  let i: i32 = 0;
  while (i < n) {
    out = out.append(h.names[i], h.values[i]);
    i = i + 1;
  }
  return out;
}
```

### `24 function __crlf_terminated(buf: u8[], from: i32, end: i32): boolean`

```fern
<<24 function __crlf_terminated(buf: u8[], from: i32, end: i32): boolean>>=
function __crlf_terminated(buf: u8[], from: i32, end: i32): boolean {
  let i: i32 = from;
  while (true) {
    let cr: i32 = __memchr_bytes(buf, 13, i);
    let lf: i32 = __memchr_bytes(buf, 10, i);
    if (cr < 0 || cr >= end) {
      return lf < 0 || lf >= end;
    }
    if (lf >= 0 && lf < cr) {
      return false;
    }
    if (cr + 1 < buf.len() && buf[cr + 1] != 10 as u8) {
      return false;
    }
    i = cr + 2;
  }
  return true;
}

function __head_shape(buf: u8[], from: i32, limits: HttpLimits): (i32, i32, boolean) {
  let n: i32 = buf.len();
  let line_limit: i32 = from + limits.request_line + 2;
  let block_limit: i32 = from + limits.header_bytes + 4;
  let end: i32 = n;
  if (block_limit < end) {
    end = block_limit;
  }
  let first_crlf: i32 = -1;
  let headers_end: i32 = -1;
  let bare: boolean = false;
  let line: i32 = from;
  let after_crlf: boolean = false;
  let i: i32 = from;
  let p: i32 = __scan_set_bytes(buf, i, CR_LF);
  let kind: i32 = __line_break(buf, p, end);
  while (kind != 2) {
    if (kind == 1) {
      bare = true;
      after_crlf = false;
      i = p + 1;
      line = i;
    } else {
      if (first_crlf == -1) {
        first_crlf = __within(p, p + 1, line_limit);
      }
      if (p == line && after_crlf) {
        headers_end = __within(p - 2, p + 1, block_limit);
        break;
      }
      after_crlf = true;
      i = p + 2;
      line = i;
    }
    p = __scan_set_bytes(buf, i, CR_LF);
    kind = __line_break(buf, p, end);
  }
  if (first_crlf == -1) {
    first_crlf = __line_end_past(buf, i, end, line_limit);
  }
  if (headers_end == -1 && n >= block_limit) {
    headers_end = -2;
  }
  return (first_crlf, headers_end, bare);
}

function __line_break(buf: u8[], p: i32, end: i32): i32 {
  if (p >= end) {
    return 2;
  }
  if (buf[p] == 10 as u8 || p + 1 < buf.len() && buf[p + 1] != 10 as u8) {
    return 1;
  }
  if (p + 1 >= buf.len()) {
    return 2;
  }
  return 0;
}

function __within(at: i32, last: i32, limit: i32): i32 {
  if (last >= limit) {
    return -2;
  }
  return at;
}

function __line_end_past(buf: u8[], i: i32, end: i32, line_limit: i32): i32 {
  let n: i32 = buf.len();
  if (line_limit > end && end < n) {
    let to: i32 = n;
    if (line_limit < to) {
      to = line_limit;
    }
    let at: i32 = __find_crlf(buf, i, to);
    if (at >= 0) {
      return at;
    }
  }
  if (n >= line_limit) {
    return -2;
  }
  return -1;
}
```

### `25 pub function http_header_block_complete(buf: u8[], from: i32, limits: HttpLimits): boolean`

```fern
<<25 pub function http_header_block_complete(buf: u8[], from: i32, limits: HttpLimits): boolean>>=
pub function http_header_block_complete(buf: u8[], from: i32, limits: HttpLimits): boolean {
  from = __past_empty_line(buf, from);
  let end: i32 = buf.len();
  if (from + limits.header_bytes + 4 < end) {
    end = from + limits.header_bytes + 4;
  }
  return __find_crlf_crlf(buf, from, end) >= 0;
}
```

### `26 function __find_crlf_crlf(buf: u8[], from: i32, end: i32): i32`

```fern
<<26 function __find_crlf_crlf(buf: u8[], from: i32, end: i32): i32>>=
function __find_crlf_crlf(buf: u8[], from: i32, end: i32): i32 {
  let i: i32 = __memchr_bytes(buf, 13, from);
  while (i >= 0 && i + 3 < end) {
    if (buf[i + 1] == 10 as u8 && buf[i + 2] == 13 as u8 && buf[i + 3] == 10 as u8) {
      return i;
    }
    i = __memchr_bytes(buf, 13, i + 1);
  }
  return -1;
}

function __field_at(h: HeaderMap, key: string, from: i32): i32 {
  let i: i32 = from;
  while (i < h.names.len()) {
    if (h.names[i].len() == key.len() && h.names[i].eq_ignore_ascii_case(key)) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}

function __fields_of(h: HeaderMap): (i32, i32, i32, i32, i32) {
  let cl: i32 = -1;
  let te: i32 = -1;
  let host: i32 = -1;
  let expect: i32 = -1;
  let conn: i32 = -1;
  let i: i32 = 0;
  while (i < h.names.len()) {
    let kind: i32 = __field_kind(h.names[i]);
    if (kind == 1) {
      cl = __once(cl, i);
    }
    if (kind == 2) {
      te = __once(te, i);
    }
    if (kind == 3) {
      host = __once(host, i);
    }
    if (kind == 4 && expect < 0) {
      expect = i;
    }
    if (kind == 5 && conn < 0) {
      conn = i;
    }
    i = i + 1;
  }
  return (cl, te, host, expect, conn);
}

function __once(at: i32, i: i32): i32 {
  if (at == -1) {
    return i;
  }
  return -2;
}

function __field_kind(name: string): i32 {
  let n: i32 = name.len();
  if (n == 4) {
    return __kind_if(name, "host", 3);
  }
  if (n == 6) {
    return __kind_if(name, "expect", 4);
  }
  if (n == 10) {
    return __kind_if(name, "connection", 5);
  }
  if (n == 14) {
    return __kind_if(name, "content-length", 1);
  }
  if (n == 17) {
    return __kind_if(name, "transfer-encoding", 2);
  }
  return 0;
}

function __kind_if(name: string, want: string, kind: i32): i32 {
  if (name.eq_ignore_ascii_case(want)) {
    return kind;
  }
  return 0;
}

function __http_content_length(h: HeaderMap, at: i32): Option[i32] {
  if (at == -1) {
    return Some(0);
  }
  if (at == -2) {
    return None;
  }
  let value: string = h.values[at];
  if (value.len() == 0) {
    return None;
  }
  let acc: i32 = 0;
  let i: i32 = 0;
  while (i < value.len()) {
    let b: i32 = value[i] as i32;
    if (b < 48 || b > 57) {
      return None;
    }
    let digit: i32 = b - 48;
    if (acc > 214748364 || acc == 214748364 && digit > 7) {
      return None;
    }
    acc = acc * 10 + digit;
    i = i + 1;
  }
  return Some(acc);
}
```

### `27 enum __BodyFraming`

```fern
<<27 enum __BodyFraming>>=
enum __BodyFraming { Length(i32), Chunked }
```

### `28 function __http_body_framing(h: HeaderMap, cl: i32, te: i32): Result[__BodyFraming, i32]`

```fern
<<28 function __http_body_framing(h: HeaderMap, cl: i32, te: i32): Result[__BodyFraming, i32]>>=
function __http_body_framing(h: HeaderMap, cl: i32, te: i32): Result[__BodyFraming, i32] {
  if (te == -1) {
    match (__http_content_length(h, cl)) {
      Some(n) => {
        return Ok(Length(n));
      },
      None => {
        return Err(400);
      }
    }
    return Err(400);
  }
  if (cl != -1 || te == -2) {
    return Err(400);
  }
  let codings: string[] = h.values[te].split(",");
  if (!__token_is(codings[codings.len() - 1], "chunked")) {
    return Err(400);
  }
  let i: i32 = 0;
  while (i + 1 < codings.len()) {
    if (__token_is(codings[i], "chunked")) {
      return Err(400);
    }
    i = i + 1;
  }
  if (codings.len() > 1) {
    return Err(501);
  }
  return Ok(Chunked);
}

function __host_ok(h: HeaderMap, at: i32, version: string): boolean {
  if (at == -1) {
    return version != "HTTP/1.1";
  }
  if (at == -2) {
    return false;
  }
  let host: string = h.values[at];
  let i: i32 = 0;
  while (i < host.len()) {
    let b: i32 = host[i] as i32;
    if (b < 33 || b > 126) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __hex_digit(b: u8): i32 {
  if (b >= 48 as u8 && b <= 57 as u8) {
    return b as i32 - 48;
  }
  if (b >= 97 as u8 && b <= 102 as u8) {
    return b as i32 - 87;
  }
  if (b >= 65 as u8 && b <= 70 as u8) {
    return b as i32 - 55;
  }
  return -1;
}

function __chunked_walk(buf: u8[], from: i32, limits: HttpLimits): (i32, i32, i32[], HeaderMap) {
  let n: i32 = buf.len();
  let at: i32 = from;
  let budget: i32 = limits.header_bytes;
  let room: i32 = limits.body;
  let spans: i32[] = [];
  let none: HeaderMap = headers.header_map_new();
  while (true) {
    let line: (i32, i32, i32) = __chunk_size_line(buf, at, budget, room);
    if (line.0 != 2) {
      return (line.0, 0, spans, none);
    }
    let data: i32 = line.1 + 2;
    let size: i32 = line.2;
    budget = budget - (data - at) - 2;
    if (budget < 0) {
      return (431, 0, spans, none);
    }
    if (size == 0) {
      return __chunked_trailers(buf, data, budget + 2, spans, limits.header_fields);
    }
    let crlf: i32 = __crlf_at(buf, data + size);
    if (crlf == 0) {
      return (400, 0, spans, none);
    }
    if (crlf == 1) {
      return (1, 0, spans, none);
    }
    spans = spans.append(data).append(size);
    room = room - size;
    at = data + size + 2;
  }
  return (400, 0, spans, none);
}

function __crlf_at(buf: u8[], at: i32): i32 {
  let n: i32 = buf.len();
  if (at < n && buf[at] != 13 as u8) {
    return 0;
  }
  if (at + 1 < n && buf[at + 1] != 10 as u8) {
    return 0;
  }
  if (at + 2 > n) {
    return 1;
  }
  return 2;
}
```

### `29 function __chunk_size_line(buf: u8[], at: i32, budget: i32, room: i32): (i32, i32, i32)`

```fern
<<29 function __chunk_size_line(buf: u8[], at: i32, budget: i32, room: i32): (i32, i32, i32)>>=
function __chunk_size_line(buf: u8[], at: i32, budget: i32, room: i32): (i32, i32, i32) {
  let n: i32 = buf.len();
  let limit: i32 = at + budget;
  if (limit > n) {
    limit = n;
  }
  let crlf: i32 = __find_crlf(buf, at, limit);
  let scanned: i32 = limit;
  if (crlf >= 0) {
    scanned = crlf;
  }
  if (!__crlf_terminated(buf, at, scanned)) {
    return (400, 0, 0);
  }
  if (crlf < 0) {
    if (n >= at + budget) {
      return (431, 0, 0);
    }
    return (1, 0, 0);
  }
  let size: (i32, i32, i32) = __chunk_size(buf, at, crlf, room);
  if (size.0 != 0) {
    return (size.0, 0, 0);
  }
  if (!__chunk_exts_ok(buf, size.2, crlf)) {
    return (400, 0, 0);
  }
  return (2, crlf, size.1);
}

function __chunk_size(buf: u8[], at: i32, end: i32, room: i32): (i32, i32, i32) {
  let size: i32 = 0;
  let i: i32 = at;
  while (i < end && __hex_digit(buf[i]) >= 0) {
    let d: i32 = __hex_digit(buf[i]);
    if (size * 16 + d > room) {
      return (413, 0, i);
    }
    size = size * 16 + d;
    i = i + 1;
  }
  if (i == at) {
    return (400, 0, i);
  }
  return (0, size, i);
}

function __chunk_exts_ok(buf: u8[], from: i32, end: i32): boolean {
  let i: i32 = from;
  while (i < end) {
    let semi: i32 = __bws_end(buf, i, end);
    if (semi >= end || buf[semi] != 59 as u8) {
      return false;
    }
    i = __bws_end(buf, semi + 1, end);
    let name_end: i32 = __token_end(buf, i, end);
    if (name_end == i) {
      return false;
    }
    i = name_end;
    let eq: i32 = __bws_end(buf, name_end, end);
    if (eq < end && buf[eq] == 61 as u8) {
      i = __chunk_ext_val_end(buf, __bws_end(buf, eq + 1, end), end);
      if (i < 0) {
        return false;
      }
    }
  }
  return true;
}

function __bws_end(buf: u8[], from: i32, end: i32): i32 {
  let i: i32 = from;
  while (i < end && __is_ows(buf[i])) {
    i = i + 1;
  }
  return i;
}

function __token_end(buf: u8[], from: i32, end: i32): i32 {
  let i: i32 = __scan_set_bytes(buf, from, NOT_TCHAR);
  if (i > end) {
    return end;
  }
  return i;
}

function __chunk_ext_val_end(buf: u8[], from: i32, end: i32): i32 {
  if (from >= end) {
    return -1;
  }
  if (buf[from] != 34 as u8) {
    let tok: i32 = __token_end(buf, from, end);
    if (tok == from) {
      return -1;
    }
    return tok;
  }
  let i: i32 = from + 1;
  while (i < end) {
    let b: u8 = buf[i];
    if (b == 34 as u8) {
      return i + 1;
    }
    if (b == 92 as u8) {
      i = i + 1;
    }
    if (i >= end || !__is_quoted_byte(buf[i])) {
      return -1;
    }
    i = i + 1;
  }
  return -1;
}

function __is_quoted_byte(b: u8): boolean {
  return b == 9 as u8 || b >= 32 as u8 && b != 127 as u8;
}
```

### `30 function __chunked_trailers(buf: u8[], at: i32, budget: i32, spans: i32[], fields: i32): (i32, i32, i32[], ...`

```fern
<<30 function __chunked_trailers(buf: u8[], at: i32, budget: i32, spans: i32[], fields: i32): (i32, i32, i32[], ...>>=
function __chunked_trailers(buf: u8[], at: i32, budget: i32, spans: i32[], fields: i32): (i32, i32, i32[], HeaderMap) {
  let n: i32 = buf.len();
  let trailers: HeaderMap = headers.header_map_new();
  let limit: i32 = at + budget;
  if (limit > n) {
    limit = n;
  }
  let sep: i32 = __find_crlf_crlf(buf, at - 2, limit);
  let scanned: i32 = limit;
  if (sep >= 0) {
    scanned = sep;
  }
  if (scanned > at && !__crlf_terminated(buf, at, scanned)) {
    return (400, 0, spans, trailers);
  }
  if (sep < 0) {
    if (n >= at + budget) {
      return (431, 0, spans, trailers);
    }
    return (1, 0, spans, trailers);
  }
  if (sep > at) {
    match (__parse_header_bytes(buf, at, sep, fields, headers.header_map_new())) {
      Ok(h) => {
        trailers = h;
      },
      Err(status) => {
        return (status, 0, spans, trailers);
      }
    }
  }
  return (2, sep + 4, spans, trailers);
}

function __chunked_body(buf: u8[], spans: i32[]): u8[] {
  let total: i32 = 0;
  let k: i32 = 1;
  while (k < spans.len()) {
    total = total + spans[k];
    k = k + 2;
  }
  let out: u8[] = __alloc_u8(total);
  let o: i32 = 0;
  k = 0;
  while (k + 1 < spans.len()) {
    out = __bytes_into(out, o, buf, spans[k], spans[k + 1]);
    o = o + spans[k + 1];
    k = k + 2;
  }
  return out;
}

function __framed_body(buf: u8[], at: i32, framing: __BodyFraming, version: string, limits: HttpLimits): (i32, u8[], i32, HeaderMap) {
  let empty: u8[] = [];
  let none: HeaderMap = headers.header_map_new();
  match (framing) {
    Length(content_length) => {
      if (content_length > limits.body) {
        return (413, empty, 0, none);
      }
      if (at + content_length > buf.len()) {
        return (1, empty, 0, none);
      }
      return (2, __bytes_range(buf, at, at + content_length), at + content_length, none);
    },
    Chunked => {
      if (version != "HTTP/1.1") {
        return (400, empty, 0, none);
      }
      let walk: (i32, i32, i32[], HeaderMap) = __chunked_walk(buf, at, limits);
      if (walk.0 != 2) {
        return (walk.0, empty, 0, none);
      }
      return (2, __chunked_body(buf, walk.2), walk.1, walk.3);
    }
  }
  return (400, empty, 0, none);
}
```

### `31 pub struct ChunkDecoder`

```fern
<<31 pub struct ChunkDecoder>>=
pub struct ChunkDecoder { state: i32, held: u8[], left: i32, budget: i32, room: i32, fields: i32, trailers: HeaderMap }

pub enum ChunkStep { ChunkData(ChunkDecoder, u8[]), ChunkEnd(u8[], HeaderMap, u8[]), ChunkRefused(i32) }
```

### `32 pub function chunk_decoder(limits: HttpLimits): ChunkDecoder`

```fern
<<32 pub function chunk_decoder(limits: HttpLimits): ChunkDecoder>>=
pub function chunk_decoder(limits: HttpLimits): ChunkDecoder {
  let none: u8[] = [];
  return ChunkDecoder { state: 0, held: none, left: 0, budget: limits.header_bytes, room: limits.body, fields: limits.header_fields, trailers: headers.header_map_new() };
}

pub function (d: ChunkDecoder) feed(bytes: u8[]): ChunkStep {
  let buf: u8[] = bytes;
  if (d.held.len() > 0) {
    buf = d.held.concat(bytes);
  }
  let none: u8[] = [];
  let cur: ChunkDecoder = ChunkDecoder { ...d, held: none };
  let out: u8[] = [];
  let at: i32 = 0;
  while (true) {
    let step: (i32, ChunkDecoder, i32, u8[]) = __chunk_step(cur, buf, at, out);
    cur = step.1;
    at = step.2;
    out = step.3;
    if (step.0 == 1) {
      return ChunkData(ChunkDecoder { ...cur, held: __bytes_range(buf, at, buf.len()) }, out);
    }
    if (step.0 == 2) {
      return ChunkEnd(out, cur.trailers, __bytes_range(buf, at, buf.len()));
    }
    if (step.0 >= 400) {
      return ChunkRefused(step.0);
    }
  }
  return ChunkRefused(400);
}
```

### `33 function __chunk_step(d: ChunkDecoder, buf: u8[], at: i32, out: u8[]): (i32, ChunkDecoder, i32, u8[])`

```fern
<<33 function __chunk_step(d: ChunkDecoder, buf: u8[], at: i32, out: u8[]): (i32, ChunkDecoder, i32, u8[])>>=
function __chunk_step(d: ChunkDecoder, buf: u8[], at: i32, out: u8[]): (i32, ChunkDecoder, i32, u8[]) {
  if (d.state == 1) {
    return __chunk_data_step(d, buf, at, out);
  }
  if (d.state == 2) {
    let crlf: i32 = __crlf_at(buf, at);
    if (crlf == 0) {
      return (400, d, at, out);
    }
    if (crlf == 1) {
      return (1, d, at, out);
    }
    return (0, ChunkDecoder { ...d, state: 0 }, at + 2, out);
  }
  if (d.state == 0) {
    return __chunk_size_step(d, buf, at, out);
  }
  if (d.state == 3) {
    return __chunk_trailers_step(d, buf, at, out);
  }
  return (2, d, at, out);
}

function __chunk_data_step(d: ChunkDecoder, buf: u8[], at: i32, out: u8[]): (i32, ChunkDecoder, i32, u8[]) {
  let take: i32 = d.left;
  if (take > buf.len() - at) {
    take = buf.len() - at;
  }
  let passed: u8[] = out.concat(__bytes_range(buf, at, at + take));
  let next: ChunkDecoder = ChunkDecoder { ...d, left: d.left - take };
  if (next.left > 0) {
    return (1, next, at + take, passed);
  }
  return (0, ChunkDecoder { ...next, state: 2 }, at + take, passed);
}

function __chunk_size_step(d: ChunkDecoder, buf: u8[], at: i32, out: u8[]): (i32, ChunkDecoder, i32, u8[]) {
  let line: (i32, i32, i32) = __chunk_size_line(buf, at, d.budget, d.room);
  if (line.0 != 2) {
    return (line.0, d, at, out);
  }
  let data: i32 = line.1 + 2;
  let size: i32 = line.2;
  let budget: i32 = d.budget - (data - at) - 2;
  if (budget < 0) {
    return (431, d, at, out);
  }
  if (size == 0) {
    return (0, ChunkDecoder { ...d, state: 3, budget: budget }, data - 2, out);
  }
  return (0, ChunkDecoder { ...d, state: 1, left: size, budget: budget, room: d.room - size }, data, out);
}

function __chunk_trailers_step(d: ChunkDecoder, buf: u8[], at: i32, out: u8[]): (i32, ChunkDecoder, i32, u8[]) {
  let none: i32[] = [];
  let t: (i32, i32, i32[], HeaderMap) = __chunked_trailers(buf, at + 2, d.budget + 2, none, d.fields);
  if (t.0 != 2) {
    return (t.0, d, at, out);
  }
  return (2, ChunkDecoder { ...d, state: 4, trailers: t.3 }, t.1, out);
}
```

### `34 pub function http_parse_request_bytes(buf: u8[]): Option[HttpRequest]`

```fern
<<34 pub function http_parse_request_bytes(buf: u8[]): Option[HttpRequest]>>=
pub function http_parse_request_bytes(buf: u8[]): Option[HttpRequest] {
  match (http_parse_request_framed(buf)) {
    Framed(f) => {
      return Some(f.request);
    },
    Incomplete => {
      return None;
    },
    Continue => {
      return None;
    },
    Malformed(_) => {
      return None;
    }
  }
  return None;
}
```

### `35 pub struct HttpFramed`

```fern
<<35 pub struct HttpFramed>>=
pub struct HttpFramed { request: HttpRequest, len: i32, keep_alive: boolean, version: string }

pub enum HttpFraming { Framed(HttpFramed), Incomplete, Continue, Malformed(i32) }
```

### `36 function __expectation(h: HeaderMap, at: i32): i32`

```fern
<<36 function __expectation(h: HeaderMap, at: i32): i32>>=
function __expectation(h: HeaderMap, at: i32): i32 {
  if (at < 0) {
    return 0;
  }
  while (at >= 0) {
    if (!__token_is(h.values[at], "100-continue")) {
      return 417;
    }
    at = __field_at(h, "expect", at + 1);
  }
  return 1;
}

function __header_rules(h: HeaderMap, host: i32, expectation: i32, version: string): i32 {
  if (!__host_ok(h, host, version)) {
    return 400;
  }
  if (expectation == 417) {
    return 417;
  }
  return 0;
}

function __unframed(kind: i32, head: __Head): HttpFraming {
  if (kind >= 400) {
    return HttpFraming.Malformed(kind);
  }
  if (head.version != "HTTP/1.0" && head.expectation == 1) {
    return Continue;
  }
  return Incomplete;
}

function __repeats(prev: HttpRequest, head: __Head, body: u8[], trailers: HeaderMap): boolean {
  if (body.len() != 0 || trailers.names.len() != 0 || prev.trailers.names.len() != 0 || !__empty_body(prev.body)) {
    return false;
  }
  return head.method == prev.method && head.path == prev.path && __same_fields(head.headers, prev.headers);
}

function __empty_body(s: ByteStream): boolean {
  match (s.source) {
    Some(_) => {
      return false;
    },
    None => {
      return s.data.len() == 0 && s.pos == 0;
    }
  }
  return false;
}

function __same_fields(a: HeaderMap, b: HeaderMap): boolean {
  if (a.names.len() != b.names.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < a.names.len()) {
    if (a.names[i] != b.names[i] || a.values[i] != b.values[i]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __request_line(buf: u8[], from: i32, end: i32, prev_path: string): (i32, i32, i32, string) {
  let sp1: i32 = __token_end(buf, from, end);
  if (sp1 == from || sp1 >= end || buf[sp1] != 32 as u8) {
    return (400, -1, -1, "");
  }
  let sp2: i32 = __find_byte(buf, sp1 + 1, end, 32 as u8);
  if (sp2 < 0 || sp2 == sp1 + 1) {
    return (400, -1, -1, "");
  }
  let version_status: i32 = __version_status(buf, sp2 + 1, end);
  if (version_status != 0) {
    return (version_status, -1, -1, "");
  }
  match (__target_path(buf, sp1 + 1, sp2, sp1 - from == 7 && __bytes_are(buf, from, "OPTIONS"), prev_path)) {
    Ok(p) => {
      return (0, sp1, sp2, p);
    },
    Err(status) => {
      return (status, -1, -1, "");
    }
  }
  return (400, -1, -1, "");
}
```

### `37 pub function http_parse_request_framed(buf: u8[]): HttpFraming`

```fern
<<37 pub function http_parse_request_framed(buf: u8[]): HttpFraming>>=
pub function http_parse_request_framed(buf: u8[]): HttpFraming {
  let limits: HttpLimits = http_limits();
  match (__request_head(buf, 0, limits, http_framed_none().request)) {
    Ok(head) => {
      let body: (i32, u8[], i32, HeaderMap) = __framed_body(buf, head.len, head.framing, head.version, limits);
      if (body.0 != 2) {
        return __unframed(body.0, head);
      }
      return Framed(__framed_new(head, body.1, body.3, body.2));
    },
    Err(status) => {
      return __head_refused(status);
    }
  }
  return Incomplete;
}

pub function http_framed_none(): HttpFramed {
  return HttpFramed { request: HttpRequest { method: "", path: "", body: ByteStream { data: [], pos: 0, source: None }, headers: headers.header_map_new(), trailers: headers.header_map_new() }, len: 0, keep_alive: false, version: "" };
}

pub function http_framed_kept(f: HttpFramed): HttpFramed {
  if (__empty_body(f.request.body) && f.request.trailers.names.len() == 0) {
    return f;
  }
  let empty: u8[] = [];
  return HttpFramed { ...f, request: HttpRequest { ...f.request, body: stream.stream_from_bytes(empty), trailers: headers.header_map_new() } };
}

pub function http_parse_request_framed_from(buf: u8[], from: i32, limits: HttpLimits, prev: HttpFramed): HttpFraming {
  match (__request_head(buf, from, limits, prev.request)) {
    Ok(head) => {
      let body: (i32, u8[], i32, HeaderMap) = __framed_body(buf, from + head.len, head.framing, head.version, limits);
      if (body.0 != 2) {
        return __unframed(body.0, head);
      }
      let len: i32 = body.2 - from;
      if (len == prev.len && head.keep_alive == prev.keep_alive && head.version == prev.version && __repeats(prev.request, head, body.1, body.3)) {
        return Framed(prev);
      }
      return Framed(__framed_new(head, body.1, body.3, len));
    },
    Err(status) => {
      return __head_refused(status);
    }
  }
  return Incomplete;
}
```

### `38 function __framed_new(head: __Head, body: u8[], trailers: HeaderMap, len: i32): HttpFramed`

```fern
<<38 function __framed_new(head: __Head, body: u8[], trailers: HeaderMap, len: i32): HttpFramed>>=
function __framed_new(head: __Head, body: u8[], trailers: HeaderMap, len: i32): HttpFramed {
  let req: HttpRequest = HttpRequest { method: head.method, path: head.path, body: stream.stream_from_bytes(body), headers: head.headers, trailers: trailers };
  return HttpFramed { request: req, len: len, keep_alive: head.keep_alive, version: head.version };
}

function __head_refused(status: i32): HttpFraming {
  if (status == 1) {
    return Incomplete;
  }
  return HttpFraming.Malformed(status);
}
```

### `39 pub struct HttpHead`

```fern
<<39 pub struct HttpHead>>=
pub struct HttpHead { request: HttpRequest, len: i32, keep_alive: boolean, version: string, length: i32, expects_continue: boolean }

pub enum HttpHeading { Headed(HttpHead), HeadIncomplete, HeadMalformed(i32) }
```

### `40 pub function http_parse_request_head_from(buf: u8[], from: i32, limits: HttpLimits, prev: HttpRequest): Htt...`

```fern
<<40 pub function http_parse_request_head_from(buf: u8[], from: i32, limits: HttpLimits, prev: HttpRequest): Htt...>>=
pub function http_parse_request_head_from(buf: u8[], from: i32, limits: HttpLimits, prev: HttpRequest): HttpHeading {
  match (__request_head(buf, from, limits, prev)) {
    Ok(head) => {
      let length: i32 = __declared(head.framing);
      if (length > limits.body) {
        return HeadMalformed(413);
      }
      if (length < 0 && head.version != "HTTP/1.1") {
        return HeadMalformed(400);
      }
      let empty: u8[] = [];
      let req: HttpRequest = HttpRequest { method: head.method, path: head.path, body: stream.stream_from_bytes(empty), headers: head.headers, trailers: headers.header_map_new() };
      let expects: boolean = head.version != "HTTP/1.0" && head.expectation == 1;
      return Headed(HttpHead { request: req, len: head.len, keep_alive: head.keep_alive, version: head.version, length: length, expects_continue: expects });
    },
    Err(status) => {
      if (status == 1) {
        return HeadIncomplete;
      }
      return HeadMalformed(status);
    }
  }
  return HeadIncomplete;
}
```

### `41 struct __Head`

```fern
<<41 struct __Head>>=
struct __Head { method: string, path: string, headers: HeaderMap, version: string, len: i32, keep_alive: boolean, framing: __BodyFraming, expectation: i32 }
```

### `42 function __request_head(buf: u8[], from: i32, limits: HttpLimits, prev: HttpRequest): Result[__Head, i32]`

```fern
<<42 function __request_head(buf: u8[], from: i32, limits: HttpLimits, prev: HttpRequest): Result[__Head, i32]>>=
function __request_head(buf: u8[], from: i32, limits: HttpLimits, prev: HttpRequest): Result[__Head, i32] {
  let start: i32 = from;
  from = __past_empty_line(buf, from);
  if (from >= buf.len()) {
    return Err(1);
  }
  let shape: (i32, i32, boolean) = __head_shape(buf, from, limits);
  let first_crlf: i32 = shape.0;
  let headers_end: i32 = shape.1;
  let bounded: i32 = __head_bounded(first_crlf, headers_end, shape.2);
  if (bounded != 0) {
    return Err(bounded);
  }
  let line: (i32, i32, i32, string) = (0, -1, -1, "");
  if (first_crlf >= 0) {
    line = __request_line(buf, from, first_crlf, prev.path);
    if (line.0 != 0) {
      return Err(line.0);
    }
  }
  let sp1: i32 = line.1;
  let sp2: i32 = line.2;
  let path: string = line.3;
  if (first_crlf < 0 || headers_end < 0) {
    return Err(1);
  }
  let headers_len: i32 = headers_end + 4 - from;
  let hdr_start: i32 = first_crlf + 2;
  if (hdr_start > headers_end) {
    hdr_start = headers_end;
  }
  let req_headers: HeaderMap = headers.header_map_new();
  match (__parse_header_bytes(buf, hdr_start, headers_end, limits.header_fields, prev.headers)) {
    Ok(h) => {
      req_headers = h;
    },
    Err(status) => {
      return Err(status);
    }
  }
  let version: string = __version_of(buf, sp2 + 1);
  let fields: (i32, i32, i32, i32, i32) = __fields_of(req_headers);
  let expectation: i32 = __expectation(req_headers, fields.3);
  let refused: i32 = __header_rules(req_headers, fields.2, expectation, version);
  if (refused > 0) {
    return Err(refused);
  }
  let framing: __BodyFraming = Length(0);
  match (__http_body_framing(req_headers, fields.0, fields.1)) {
    Ok(f) => {
      framing = f;
    },
    Err(status) => {
      return Err(status);
    }
  }
  return Ok(__Head { method: __shared_string(buf, from, sp1, prev.method), path: path, headers: req_headers, version: version, len: from + headers_len - start, keep_alive: __persists(version, req_headers, fields.4), framing: framing, expectation: expectation });
}

function __head_bounded(first_crlf: i32, headers_end: i32, bare: boolean): i32 {
  if (first_crlf == -2) {
    return 414;
  }
  if (headers_end == -2) {
    return 431;
  }
  if (bare) {
    return 400;
  }
  return 0;
}

function __declared(f: __BodyFraming): i32 {
  match (f) {
    Length(n) => {
      return n;
    },
    Chunked => {
      return 0 - 1;
    }
  }
  return 0 - 1;
}
```

### `43 pub function http_request_target(method: string, target: string): Result[string, i32]`

```fern
<<43 pub function http_request_target(method: string, target: string): Result[string, i32]>>=
pub function http_request_target(method: string, target: string): Result[string, i32] {
  let buf: u8[] = target.bytes();
  let end: i32 = target.len();
  let path_from: i32 = __target_start(buf, 0, end, method == "OPTIONS");
  if (path_from == BAD_TARGET) {
    return Err(400);
  }
  if (path_from == ASTERISK_FORM) {
    return Ok("*");
  }
  let path_end: i32 = __path_end(buf, path_from, end);
  if (path_end < 0) {
    return Err(400);
  }
  if (path_from < path_end && __plain_path(buf, path_from, path_end)) {
    if (path_from == 0) {
      return Ok(target);
    }
    return Ok(slice_unchecked(target, path_from, end).to_owned());
  }
  return __decoded_path(buf, path_from, path_end, end);
}
```

### `44 function __target_path(buf: u8[], from: i32, end: i32, options: boolean, prev: string): Result[string, i32]`

```fern
<<44 function __target_path(buf: u8[], from: i32, end: i32, options: boolean, prev: string): Result[string, i32]>>=
function __target_path(buf: u8[], from: i32, end: i32, options: boolean, prev: string): Result[string, i32] {
  let path_from: i32 = __target_start(buf, from, end, options);
  if (path_from == BAD_TARGET) {
    return Err(400);
  }
  if (path_from == ASTERISK_FORM) {
    return Ok("*");
  }
  let path_end: i32 = __path_end(buf, path_from, end);
  if (path_end < 0) {
    return Err(400);
  }
  if (path_from < path_end && __plain_path(buf, path_from, path_end)) {
    return Ok(__shared_string(buf, path_from, end, prev));
  }
  return __decoded_path(buf, path_from, path_end, end);
}
```

### `45 const ASTERISK_FORM: i32`

```fern
<<45 const ASTERISK_FORM: i32>>=
const ASTERISK_FORM: i32 = 0 - 1;

const BAD_TARGET: i32 = 0 - 2;
```

### `46 function __target_start(buf: u8[], from: i32, end: i32, options: boolean): i32`

```fern
<<46 function __target_start(buf: u8[], from: i32, end: i32, options: boolean): i32>>=
function __target_start(buf: u8[], from: i32, end: i32, options: boolean): i32 {
  if (end == from + 1 && buf[from] == 42 as u8) {
    if (options) {
      return ASTERISK_FORM;
    }
    return BAD_TARGET;
  }
  if (end > from && buf[from] == 47 as u8) {
    return from;
  }
  let path_from: i32 = __absolute_form_path(buf, from, end);
  if (path_from < 0) {
    return BAD_TARGET;
  }
  return path_from;
}

function __path_end(buf: u8[], from: i32, end: i32): i32 {
  let q: i32 = __find_char(buf, from, end, 63 as u8);
  if (q < 0) {
    return end;
  }
  if (!__query_ok(buf, q + 1, end)) {
    return 0 - 1;
  }
  return q;
}

function __absolute_form_path(target: [u8], from: i32, end: i32): i32 {
  let at: i32 = -1;
  if (__prefix_ci(target, from, end, "http://")) {
    at = from + 7;
  }
  if (__prefix_ci(target, from, end, "https://")) {
    at = from + 8;
  }
  if (at < 0) {
    return -1;
  }
  let authority_end: i32 = at;
  while (authority_end < end && target[authority_end] != 47 as u8 && target[authority_end] != 63 as u8) {
    if (!__is_pchar(target[authority_end])) {
      return -1;
    }
    authority_end = authority_end + 1;
  }
  if (authority_end == at) {
    return -1;
  }
  return authority_end;
}

function __prefix_ci(s: [u8], from: i32, end: i32, want: string): boolean {
  if (end - from < want.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < want.len()) {
    if (__ascii_lower(s[from + i]) != want[i] as i32) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __find_char(s: [u8], from: i32, end: i32, c: u8): i32 {
  let i: i32 = from;
  while (i < end) {
    if (s[i] == c) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}

function __is_pchar(b: u8): boolean {
  let v: i32 = b as i32;
  return __is_unreserved(v) || __is_sub_delim(v) || v == 58 || v == 64 || v == 37;
}

function __is_unreserved(b: i32): boolean {
  return b >= 97 && b <= 122 || b >= 65 && b <= 90 || b >= 48 && b <= 57
    || b == 45 || b == 46 || b == 95 || b == 126;
}

function __is_sub_delim(b: i32): boolean {
  let delims: string = "!$&'()*+,;=";
  let i: i32 = 0;
  while (i < delims.len()) {
    if (delims[i] as i32 == b) {
      return true;
    }
    i = i + 1;
  }
  return false;
}

function __query_ok(s: [u8], from: i32, end: i32): boolean {
  let i: i32 = from;
  while (i < end) {
    let b: u8 = s[i];
    if (b == 37 as u8) {
      if (__pct_byte(s, i, end) < 0) {
        return false;
      }
      i = i + 3;
    } else {
      if (!__is_pchar(b) && b != 47 as u8 && b != 63 as u8) {
        return false;
      }
      i = i + 1;
    }
  }
  return true;
}

function __pct_byte(s: [u8], at: i32, end: i32): i32 {
  if (at + 2 >= end) {
    return -1;
  }
  let hi: i32 = __hex_digit(s[at + 1]);
  let lo: i32 = __hex_digit(s[at + 2]);
  if (hi < 0 || lo < 0) {
    return -1;
  }
  return hi * 16 + lo;
}
```

### `47 function __plain_path(buf: u8[], from: i32, end: i32): boolean`

```fern
<<47 function __plain_path(buf: u8[], from: i32, end: i32): boolean>>=
function __plain_path(buf: u8[], from: i32, end: i32): boolean {
  let seg: i32 = from + 1;
  let i: i32 = from;
  while (i < end) {
    let b: u8 = buf[i];
    if (b == 47 as u8) {
      if (i > from && __dot_segment(buf, seg, i) > 0) {
        return false;
      }
      seg = i + 1;
    } else if (b == 37 as u8 || !__is_pchar(b)) {
      return false;
    }
    i = i + 1;
  }
  return __dot_segment(buf, seg, end) == 0;
}

function __dot_segment(s: u8[], from: i32, end: i32): i32 {
  let n: i32 = end - from;
  if (n < 1 || n > 2 || s[from] != 46 as u8 || s[end - 1] != 46 as u8) {
    return 0;
  }
  return n;
}

function __decoded_path(buf: u8[], from: i32, path_end: i32, end: i32): Result[string, i32] {
  let out: u8[] = [];
  let w: i32 = 0;
  let trailing: boolean = false;
  let escaped_high: boolean = false;
  let at: i32 = from + 1;
  while (at <= path_end) {
    let seg: i32 = w;
    out = __put_byte(out, w, 47 as u8);
    w = w + 1;
    while (at < path_end && buf[at] != 47 as u8) {
      let b: i32 = __path_byte(buf, at, path_end);
      if (b < 0) {
        return Err(400);
      }
      escaped_high = escaped_high || b >= 128;
      out = __put_byte(out, w, b as u8);
      w = w + 1;
      at = at + __path_byte_len(buf[at]);
    }
    trailing = at == path_end;
    let dots: i32 = __dot_segment(out, seg + 1, w);
    if (dots == 0) {
      trailing = false;
    } else {
      w = __dropped_to(out, seg, dots, escaped_high);
      if (w < 0) {
        return Err(400);
      }
    }
    at = at + 1;
  }
  return __finished_path(out, w, trailing, escaped_high, buf, path_end, end);
}

function __path_byte(buf: u8[], at: i32, end: i32): i32 {
  let b: i32 = buf[at] as i32;
  if (b == 37) {
    b = __pct_byte(buf, at, end);
    if (b <= 0 || b == 47) {
      return -1;
    }
    return b;
  }
  if (!__is_pchar(b as u8)) {
    return -1;
  }
  return b;
}

function __path_byte_len(b: u8): i32 {
  if (b == 37 as u8) {
    return 3;
  }
  return 1;
}

function __dropped_to(out: u8[], seg: i32, dots: i32, escaped_high: boolean): i32 {
  if (dots == 1) {
    return seg;
  }
  if (seg == 0) {
    return -1;
  }
  let prev: i32 = seg - 1;
  while (out[prev] != 47 as u8) {
    prev = prev - 1;
  }
  if (escaped_high && !utf8.is_valid(__bytes_string(out, prev, seg))) {
    return -1;
  }
  return prev;
}

function __finished_path(out: u8[], w: i32, trailing: boolean, escaped_high: boolean, buf: u8[], from: i32, end: i32): Result[string, i32] {
  if (w == 0 || trailing) {
    out = __put_byte(out, w, 47 as u8);
    w = w + 1;
  }
  let i: i32 = from;
  while (i < end) {
    out = __put_byte(out, w, buf[i]);
    w = w + 1;
    i = i + 1;
  }
  let path: string = __bytes_string(out, 0, w);
  if (escaped_high && !utf8.is_valid(path)) {
    return Err(400);
  }
  return Ok(path);
}

function __put_byte(out: u8[], at: i32, b: u8): u8[] {
  if (at < out.len()) {
    return out.with(at, b);
  }
  return out.append(b);
}

function __past_empty_line(buf: u8[], from: i32): i32 {
  if (from + 1 < buf.len() && buf[from] == 13 as u8 && buf[from + 1] == 10 as u8) {
    return from + 2;
  }
  return from;
}
```

### `48 function __version_status(buf: u8[], from: i32, end: i32): i32`

```fern
<<48 function __version_status(buf: u8[], from: i32, end: i32): i32>>=
function __version_status(buf: u8[], from: i32, end: i32): i32 {
  if (end - from != 8 || __mismatch_bytes(buf, from, HTTP_SLASH, 0, 5) != 5) {
    return 400;
  }
  if (!__is_digit(buf[from + 5]) || buf[from + 6] != 46 as u8 || !__is_digit(buf[from + 7])) {
    return 400;
  }
  if (buf[from + 5] != 49 as u8) {
    return 505;
  }
  return 0;
}

function __version_of(buf: u8[], from: i32): string {
  if (buf[from + 7] == 48 as u8) {
    return "HTTP/1.0";
  }
  return "HTTP/1.1";
}

function __is_digit(b: u8): boolean {
  return b >= 48 as u8 && b <= 57 as u8;
}

function __bytes_are(buf: u8[], from: i32, want: string): boolean {
  return __mismatch_bytes_str(buf, from, want, 0, want.len()) == want.len();
}

function __persists(version: string, h: HeaderMap, at: i32): boolean {
  let close: boolean = false;
  let keep: boolean = false;
  while (at >= 0) {
    if (__lists_token(h.values[at], "close")) {
      close = true;
    }
    if (__lists_token(h.values[at], "keep-alive")) {
      keep = true;
    }
    at = __field_at(h, "connection", at + 1);
  }
  if (close) {
    return false;
  }
  if (version == "HTTP/1.0") {
    return keep;
  }
  return true;
}
```

### `49 pub function http_parse_request(buf: string): Option[HttpRequest]`

```fern
<<49 pub function http_parse_request(buf: string): Option[HttpRequest]>>=
pub function http_parse_request(buf: string): Option[HttpRequest] {
  return http_parse_request_bytes(buf.bytes());
}
```

### `50 function __http_reason(code: i32): string`

```fern
<<50 function __http_reason(code: i32): string>>=
function __http_reason(code: i32): string {
  let text: string = http_status_text(code);
  if (text.len() == 0) {
    return "Status";
  }
  return text;
}
```

### `51 pub function http_serialize_response(resp: HttpResponse): string`

```fern
<<51 pub function http_serialize_response(resp: HttpResponse): string>>=
pub function http_serialize_response(resp: HttpResponse): string {
  return http_serialize_response_conn(resp, false);
}

pub function http_serialize_response_conn(resp: HttpResponse, keep_alive: boolean): string {
  return http_serialize_response_to("GET", "HTTP/1.1", resp, keep_alive);
}

pub function http_serialize_response_to(method: string, version: string, resp: HttpResponse, keep_alive: boolean): string {
  let bodiless: boolean = http_bodiless(resp.status);
  let framing: string = "";
  let body: string = "";
  if (!bodiless) {
    body = resp.body_string();
    framing = "Content-Length: " + int.int_to_string(body.len()) + "\r\n";
  }
  let b: usize = buf_new(256);
  http_serialize_response_head_into(b, version, resp, keep_alive, framing);
  let head: string = buf_take(b);
  buf_free(b);
  if (bodiless || method == "HEAD") {
    return head;
  }
  return head + body;
}

pub function http_serialize_response_bytes(resp: HttpResponse): u8[] {
  return http_serialize_response_conn_bytes(resp, false);
}

pub function http_serialize_response_conn_bytes(resp: HttpResponse, keep_alive: boolean): u8[] {
  return http_serialize_response_to_bytes("GET", "HTTP/1.1", resp, keep_alive);
}

pub function http_serialize_response_to_bytes(method: string, version: string, resp: HttpResponse, keep_alive: boolean): u8[] {
  let cap: i32 = 256;
  match (resp.body) {
    BodyText(t) => {
      cap = t.len();
    },
    BodyBytes(bs) => {
      cap = bs.len();
    },
    _ => {}
  }
  if (cap <= 2147483647 - 256) {
    cap = cap + 256;
  }
  let b: usize = buf_new(cap);
  http_serialize_response_into(b, method, version, resp, keep_alive, "");
  return __take_wire(b);
}

pub function http_serialize_response_into(b: usize, method: string, version: string, resp: HttpResponse, keep_alive: boolean, fields: string): void {
  if (http_bodiless(resp.status)) {
    http_serialize_response_head_into(b, version, resp, keep_alive, fields);
    return;
  }
  if (method == "HEAD") {
    http_serialize_response_head_sized_into(b, version, resp, keep_alive, fields, resp.body_len());
    return;
  }
  let text: string = "";
  let body: u8[] = [];
  match (resp.body) {
    BodyText(s) => {
      text = s;
    },
    _ => {
      body = resp.body_bytes();
    }
  }
  let len: i32 = text.len() + body.len();
  http_serialize_response_head_sized_into(b, version, resp, keep_alive, fields, len);
  assert(len <= 2147483647 - buf_len(b));
  buf_push(b, text);
  buf_push_bytes_range(b, body, 0, body.len());
}

pub function http_bodiless(status: i32): boolean {
  return status < 200 || status == 204 || status == 304;
}

pub function http_serialize_fields(fields: HeaderMap): string {
  let out: string = "";
  let i: i32 = 0;
  while (i < fields.names.len()) {
    out = out + fields.names[i] + ": " + fields.values[i] + "\r\n";
    i = i + 1;
  }
  return out;
}

pub function http_serialize_response_head_sized_into(b: usize, version: string, resp: HttpResponse, keep_alive: boolean, fields: string, len: i32): void {
  __push_head(b, resp);
  buf_push(b, fields);
  __push_length(b, len);
  __push_close(b, version, keep_alive);
}

pub function http_serialize_response_head_into(b: usize, version: string, resp: HttpResponse, keep_alive: boolean, fields: string): void {
  __push_head(b, resp);
  buf_push(b, fields);
  __push_conn(b, version, resp, keep_alive);
}
```

### `52 function __push_head(b: usize, resp: HttpResponse): void`

```fern
<<52 function __push_head(b: usize, resp: HttpResponse): void>>=
function __push_head(b: usize, resp: HttpResponse): void {
  buf_push(b, "HTTP/1.1 ");
  io_buffered.buf_push_i64(b, resp.status as i64);
  buf_push(b, " ");
  buf_push(b, __http_reason(resp.status));
  buf_push(b, "\r\n");
  let i: i32 = 0;
  while (i < resp.headers.names.len()) {
    let hname: string = resp.headers.names[i];
    if (!hname.eq_ignore_ascii_case("content-length") && !hname.eq_ignore_ascii_case("connection") && !hname.eq_ignore_ascii_case("transfer-encoding")) {
      buf_push(b, hname);
      buf_push(b, ": ");
      buf_push(b, resp.headers.values[i]);
      buf_push(b, "\r\n");
    }
    i = i + 1;
  }
}

function __push_length(b: usize, len: i32): void {
  buf_push(b, "Content-Length: ");
  io_buffered.buf_push_i64(b, len as i64);
  buf_push(b, "\r\n");
}

function __push_conn(b: usize, version: string, resp: HttpResponse, keep_alive: boolean): void {
  if (resp.status == 101) {
    buf_push(b, "Connection: Upgrade\r\n\r\n");
    return;
  }
  __push_close(b, version, keep_alive);
}

function __push_close(b: usize, version: string, keep_alive: boolean): void {
  if (!keep_alive) {
    buf_push(b, "Connection: close\r\n\r\n");
    return;
  }
  if (__http10(version)) {
    buf_push(b, "Connection: keep-alive\r\n\r\n");
    return;
  }
  buf_push(b, "\r\n");
}

function __http10(version: string): boolean {
  return version.len() == 8 && version[7] == b'0';
}

function __take_wire(b: usize): u8[] {
  let wire: u8[] = buf_take_bytes(b);
  buf_free(b);
  return wire;
}
```

### `53 pub function request(method: string, path: string): HttpRequest`

```fern
<<53 pub function request(method: string, path: string): HttpRequest>>=
pub function request(method: string, path: string): HttpRequest {
  let empty: u8[] = [];
  return HttpRequest { method: method, path: path, body: stream.stream_from_bytes(empty), headers: headers.header_map_new(), trailers: headers.header_map_new() };
}

pub function (r: HttpRequest) with_header(name: string, value: string): HttpRequest {
  return HttpRequest { ...r, headers: r.headers.set(name, value) };
}

pub function (r: HttpRequest) with_body(body: string): HttpRequest {
  return HttpRequest { ...r, body: stream.stream_from_bytes(body.bytes()), headers: r.headers.set("Content-Length", int.int_to_string(body.len())) };
}

pub function (r: HttpRequest) with_json(body: string): HttpRequest {
  return r.with_body(body).with_header("Content-Type", "application/json");
}

pub function (r: HttpRequest) query(name: string): Option[string] {
  return url.query_get(__query_of(r.path), name);
}

pub function (r: HttpRequest) query_all(name: string): string[] {
  return url.query_get_all(__query_of(r.path), name);
}
```

### `54 function __query_of(path: string): string`

```fern
<<54 function __query_of(path: string): string>>=
function __query_of(path: string): string {
  let Some(q) = path.index_of("?") else {
    return "";
  };
  return slice_unchecked(path, q + 1, path.len()) + "";
}
```

### `55 pub function (r: HttpRequest) body_stream(): ByteStream`

```fern
<<55 pub function (r: HttpRequest) body_stream(): ByteStream>>=
pub function (r: HttpRequest) body_stream(): ByteStream {
  return r.body;
}

pub function (r: HttpRequest) body_string(): Result[string, BodyError] {
  let (bytes, rest) = __whole_body(r.body);
  if (rest.fault() != 0) {
    return Err(EndedEarly(rest.fault()));
  }
  match (utf8.from_bytes(bytes)) {
    Some(s) => {
      return Ok(s);
    },
    None => {
      return Err(NotUtf8);
    }
  }
  return Err(NotUtf8);
}
```

### `56 function __whole_body(s: ByteStream): (u8[], ByteStream)`

```fern
<<56 function __whole_body(s: ByteStream): (u8[], ByteStream)>>=
function __whole_body(s: ByteStream): (u8[], ByteStream) {
  let from_start: ByteStream = ByteStream { ...s, pos: 0 };
  return from_start.read_all();
}
```

### `57 pub function (r: HttpRequest) body_bytes(): u8[]`

```fern
<<57 pub function (r: HttpRequest) body_bytes(): u8[]>>=
pub function (r: HttpRequest) body_bytes(): u8[] {
  let (bytes, rest) = __whole_body(r.body);
  return bytes;
}

pub function (r: HttpRequest) body_len(): i32 {
  match (r.body.source) {
    Some(src) => {
      return __declared_length(r.headers);
    },
    None => {
      return r.body.data.len();
    }
  }
  return r.body.data.len();
}
```

### `58 function __declared_length(h: HeaderMap): i32`

```fern
<<58 function __declared_length(h: HeaderMap): i32>>=
function __declared_length(h: HeaderMap): i32 {
  match (h.get("content-length")) {
    Some(v) => {
      match (i32.parse_i32_radix(v.trim(), 10)) {
        Ok(n) => {
          return n;
        },
        Err(_) => {
          return 0 - 1;
        }
      }
    },
    None => {
      return 0 - 1;
    }
  }
  return 0 - 1;
}
```

### `59 pub function (r: HttpResponse) body_string(): string`

```fern
<<59 pub function (r: HttpResponse) body_string(): string>>=
pub function (r: HttpResponse) body_string(): string {
  match (r.body) {
    BodyText(s) => {
      return s;
    },
    BodyBytes(bs) => {
      return utf8.from_bytes_lossy(bs);
    },
    BodyStream(st) => {
      let rest: (u8[], ByteStream) = st.read_all();
      return utf8.from_bytes_lossy(rest.0);
    },
    BodyFile(path) => {
      return "";
    },
    BodyFileRanges(path, ranges, seams) => {
      return "";
    },
    BodyChunks(producer) => {
      let joined: u8[] = __chunks_joined(producer);
      return utf8.from_bytes_lossy(joined);
    }
  }
  return "";
}

pub function (r: HttpResponse) body_bytes(): u8[] {
  return r.body.bytes();
}

pub function (b: Body) bytes(): u8[] {
  match (b) {
    BodyText(s) => {
      return s.bytes();
    },
    BodyBytes(bs) => {
      return bs;
    },
    BodyStream(st) => {
      let rest: (u8[], ByteStream) = st.read_all();
      return rest.0;
    },
    BodyFile(path) => {
      let none: u8[] = [];
      return none;
    },
    BodyFileRanges(path, ranges, seams) => {
      let none: u8[] = [];
      return none;
    },
    BodyChunks(producer) => {
      return __chunks_joined(producer);
    }
  }
  let empty: u8[] = [];
  return empty;
}
```

### `60 function __chunks_joined(producer: ChunkProducer): u8[]`

```fern
<<60 function __chunks_joined(producer: ChunkProducer): u8[]>>=
function __chunks_joined(producer: ChunkProducer): u8[] {
  let next: (i32) => Option[u8[]] = producer.next;
  let out: u8[] = [];
  let i: i32 = 0;
  while (true) {
    match (next(i)) {
      Some(chunk) => {
        out = out.concat(chunk);
      },
      None => {
        return out;
      }
    }
    i = i + 1;
  }
  return out;
}
```

### `61 pub function (r: HttpResponse) body_len(): i32`

```fern
<<61 pub function (r: HttpResponse) body_len(): i32>>=
pub function (r: HttpResponse) body_len(): i32 {
  match (r.body) {
    BodyText(s) => {
      return s.len();
    },
    BodyBytes(bs) => {
      return bs.len();
    },
    BodyStream(st) => {
      return st.remaining();
    },
    BodyFile(path) => {
      return 0;
    },
    BodyFileRanges(path, ranges, seams) => {
      return file_ranges_len(ranges, seams) as i32;
    },
    BodyChunks(producer) => {
      return __chunks_joined(producer).len();
    }
  }
  return 0;
}

pub function http_materialize(r: HttpResponse): HttpResponse {
  match (r.body) {
    BodyFile(path) => {
      match (read_file_bytes(path)) {
        Ok(bs) => {
          return HttpResponse { ...r, body: BodyBytes(bs) };
        },
        Err(e) => {
          return not_found();
        }
      }
      return not_found();
    },
    BodyFileRanges(path, ranges, seams) => {
      match (__file_ranges_read(path, ranges, seams)) {
        Some(bs) => {
          return HttpResponse { ...r, body: BodyBytes(bs) };
        },
        None => {
          return not_found();
        }
      }
      return not_found();
    },
    _ => {
      return r;
    }
  }
  return r;
}
```

### `62 function __file_ranges_read(path: string, ranges: i64[], seams: u8[][]): Option[u8[]]`

```fern
<<62 function __file_ranges_read(path: string, ranges: i64[], seams: u8[][]): Option[u8[]]>>=
function __file_ranges_read(path: string, ranges: i64[], seams: u8[][]): Option[u8[]] {
  match (open_reader(path)) {
    Ok(r) => {
      let got: Option[u8[]] = file_ranges_bytes(ranges, seams, (offset: i64, len: i64): Option[u8[]] => __read_span(r, offset, len));
      r.close();
      return got;
    },
    Err(e) => {
      return None;
    }
  }
  return None;
}
```

### `63 pub function file_ranges_ok(ranges: i64[], seams: u8[][]): boolean`

```fern
<<63 pub function file_ranges_ok(ranges: i64[], seams: u8[][]): boolean>>=
pub function file_ranges_ok(ranges: i64[], seams: u8[][]): boolean {
  if (ranges.len() % 2 != 0 || seams.len() != ranges.len() / 2 + 1) {
    return false;
  }
  for n in ranges {
    if (n < 0 as i64) {
      return false;
    }
  }
  return true;
}

pub function file_ranges_len(ranges: i64[], seams: u8[][]): i64 {
  let n: i64 = 0 as i64;
  let i: i32 = 1;
  while (i < ranges.len()) {
    n = n + ranges[i];
    i = i + 2;
  }
  for s in seams {
    n = n + s.len() as i64;
  }
  return n;
}

pub function file_ranges_bytes(ranges: i64[], seams: u8[][], read: (i64, i64) => Option[u8[]]): Option[u8[]] {
  if (!file_ranges_ok(ranges, seams)) {
    return None;
  }
  let out: u8[] = seams[0];
  let i: i32 = 0;
  while (i < ranges.len()) {
    match (read(ranges[i], ranges[i + 1])) {
      Some(bs) => {
        out = out.concat(bs).concat(seams[i / 2 + 1]);
      },
      None => {
        return None;
      }
    }
    i = i + 2;
  }
  return Some(out);
}
```

### `64 function __read_span(r: Reader, offset: i64, len: i64): Option[u8[]]`

```fern
<<64 function __read_span(r: Reader, offset: i64, len: i64): Option[u8[]]>>=
function __read_span(r: Reader, offset: i64, len: i64): Option[u8[]] {
  match (r.seek(offset, 0)) {
    Err(e) => {
      return None;
    },
    Ok(at) => {}
  }
  let out: u8[] = [];
  while (out.len() as i64 < len) {
    let want: i64 = len - out.len() as i64;
    if (want > 65536 as i64) {
      want = 65536 as i64;
    }
    match (r.read_chunk_bytes(want as i32)) {
      Ok(piece) => {
        if (piece.len() == 0) {
          return None;
        }
        out = out.concat(piece);
      },
      Err(e) => {
        return None;
      }
    }
  }
  return Some(out);
}
```

### `65 pub function bytes(status: i32, bs: u8[]): HttpResponse`

```fern
<<65 pub function bytes(status: i32, bs: u8[]): HttpResponse>>=
pub function bytes(status: i32, bs: u8[]): HttpResponse {
  return HttpResponse { status: status, body: BodyBytes(bs), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
}

pub function stream(status: i32, s: ByteStream): HttpResponse {
  return HttpResponse { status: status, body: BodyStream(s), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
}

pub function file(path: string): HttpResponse {
  return HttpResponse { status: 200, body: BodyFile(path), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
}

pub function chunks(status: i32, next: (i32) => Option[u8[]]): HttpResponse {
  let none: u8[] = [];
  return HttpResponse { status: status, body: BodyChunks(ChunkProducer { next: next, idle_ms: 0, filler: none, trailers: __no_trailers }), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
}

pub function chunks_with_trailers(status: i32, next: (i32) => Option[u8[]], trailers: () => HeaderMap): HttpResponse {
  let none: u8[] = [];
  return HttpResponse { status: status, body: BodyChunks(ChunkProducer { next: next, idle_ms: 0, filler: none, trailers: trailers }), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
}
```

### `66 function __no_trailers(): HeaderMap`

```fern
<<66 function __no_trailers(): HeaderMap>>=
function __no_trailers(): HeaderMap {
  return headers.header_map_new();
}
```

### `67 pub function chunks_idle(status: i32, next: (i32)`

```fern
<<67 pub function chunks_idle(status: i32, next: (i32)>>=
pub function chunks_idle(status: i32, next: (i32) => Option[u8[]], idle: Duration, filler: u8[]): HttpResponse {
  let ms: i64 = idle.as_millis();
  if (ms > 2147483647 as i64) {
    ms = 2147483647 as i64;
  }
  return HttpResponse { status: status, body: BodyChunks(ChunkProducer { next: next, idle_ms: ms as i32, filler: filler, trailers: __no_trailers }), headers: headers.header_map_new(), trailers: headers.header_map_new(), background: None };
}

pub function (r: HttpResponse) with_header(name: string, value: string): HttpResponse {
  return HttpResponse { ...r, headers: r.headers.set(name, value) };
}

pub function (r: HttpResponse) with_trailer(name: string, value: string): HttpResponse {
  return HttpResponse { ...r, trailers: r.trailers.set(name, value) };
}

pub function (r: HttpResponse) with_background(work: () => void): HttpResponse {
  match (r.background) {
    Some(first) => {
      return HttpResponse { ...r, background: Some(() => {
        first();
        work();
      }) };
    },
    None => {
      return HttpResponse { ...r, background: Some(work) };
    }
  }
  return r;
}

pub function (r: HttpResponse) with_appended_header(name: string, value: string): HttpResponse {
  return HttpResponse { ...r, headers: r.headers.append(name, value) };
}

pub function (r: HttpResponse) with_content_type(ct: string): HttpResponse {
  return HttpResponse { ...r, headers: r.headers.set("Content-Type", ct) };
}
```

### `68 pub struct HttpResponseFramed`

```fern
<<68 pub struct HttpResponseFramed>>=
pub struct HttpResponseFramed { response: HttpResponse, len: i32, version: string, keep_alive: boolean }

pub enum HttpResponseFraming { Complete(HttpResponseFramed), Unfinished, Rejected(i32) }
```

### `69 pub function http_parse_response_framed(buf: u8[], method: string, eof: boolean, limits: HttpLimits): HttpR...`

```fern
<<69 pub function http_parse_response_framed(buf: u8[], method: string, eof: boolean, limits: HttpLimits): HttpR...>>=
pub function http_parse_response_framed(buf: u8[], method: string, eof: boolean, limits: HttpLimits): HttpResponseFraming {
  return http_parse_response_framed_from(buf, 0, method, eof, limits);
}

pub function http_parse_response_framed_from(buf: u8[], from: i32, method: string, eof: boolean, limits: HttpLimits): HttpResponseFraming {
  let shape: (i32, i32, boolean) = __head_shape(buf, from, limits);
  let first_crlf: i32 = shape.0;
  let headers_end: i32 = shape.1;
  let refused: i32 = __response_head_refused(buf, from, first_crlf, headers_end, shape.2);
  if (refused != 0) {
    return Rejected(refused);
  }
  if (first_crlf < 0 || headers_end < 0) {
    return Unfinished;
  }
  return __response_body(buf, from, first_crlf, headers_end, __status_code(buf, from), method, eof, limits);
}
```

### `70 function __response_head_refused(buf: u8[], from: i32, first_crlf: i32, headers_end: i32, bare: boolean): i32`

```fern
<<70 function __response_head_refused(buf: u8[], from: i32, first_crlf: i32, headers_end: i32, bare: boolean): i32>>=
function __response_head_refused(buf: u8[], from: i32, first_crlf: i32, headers_end: i32, bare: boolean): i32 {
  if (first_crlf == -2) {
    return 400;
  }
  if (headers_end == -2) {
    return 431;
  }
  if (bare) {
    return 400;
  }
  if (first_crlf >= 0) {
    return __status_line_refused(buf, from, first_crlf);
  }
  return 0;
}

function __status_line_refused(buf: u8[], from: i32, end: i32): i32 {
  if (end - from < 8) {
    return 400;
  }
  let version: i32 = __version_status(buf, from, from + 8);
  if (version != 0) {
    return version;
  }
  if (end < from + 12 || buf[from + 8] != 32 as u8 || !__three_digits(buf, from + 9)) {
    return 400;
  }
  if (end > from + 12 && buf[from + 12] != 32 as u8) {
    return 400;
  }
  if (!__reason_ok(buf, from + 13, end)) {
    return 400;
  }
  return 0;
}

function __three_digits(buf: u8[], at: i32): boolean {
  return __is_digit(buf[at]) && __is_digit(buf[at + 1]) && __is_digit(buf[at + 2]);
}

function __reason_ok(buf: u8[], from: i32, end: i32): boolean {
  let i: i32 = from;
  while (i < end) {
    if (buf[i] < 32 as u8 && buf[i] != 9 as u8) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __status_code(buf: u8[], from: i32): i32 {
  return (buf[from + 9] as i32 - 48) * 100 + (buf[from + 10] as i32 - 48) * 10 + (buf[from + 11] as i32 - 48);
}

function __response_body(buf: u8[], from: i32, first_crlf: i32, headers_end: i32, status: i32, method: string, eof: boolean, limits: HttpLimits): HttpResponseFraming {
  let hdr_start: i32 = first_crlf + 2;
  if (hdr_start > headers_end) {
    hdr_start = headers_end;
  }
  let h: HeaderMap = headers.header_map_new();
  match (__parse_header_bytes(buf, hdr_start, headers_end, limits.header_fields, h)) {
    Ok(parsed) => {
      h = parsed;
    },
    Err(code) => {
      return Rejected(code);
    }
  }
  let version: string = __version_of(buf, from);
  let at: i32 = headers_end + 4;
  let fields: (i32, i32, i32, i32, i32) = __fields_of(h);
  let body: (i32, u8[], i32, HeaderMap) = __response_payload(buf, at, h, fields, status, method, eof, version, limits);
  if (body.0 == 1) {
    return Unfinished;
  }
  if (body.0 != 2) {
    return Rejected(body.0);
  }
  let resp: HttpResponse = HttpResponse { status: status, body: BodyBytes(body.1), headers: h, trailers: body.3, background: None };
  return Complete(HttpResponseFramed { response: resp, len: body.2 - from, version: version, keep_alive: __persists(version, h, fields.4) });
}

function __response_payload(buf: u8[], at: i32, h: HeaderMap, fields: (i32, i32, i32, i32, i32), status: i32, method: string, eof: boolean, version: string, limits: HttpLimits): (i32, u8[], i32, HeaderMap) {
  let empty: u8[] = [];
  let none: HeaderMap = headers.header_map_new();
  if (method == "HEAD" || http_bodiless(status)) {
    return (2, empty, at, none);
  }
  if (fields.1 == -1 && fields.0 == -1) {
    if (!eof) {
      return (1, empty, 0, none);
    }
    if (buf.len() - at > limits.body) {
      return (413, empty, 0, none);
    }
    return (2, __bytes_range(buf, at, buf.len()), buf.len(), none);
  }
  match (__http_body_framing(h, fields.0, fields.1)) {
    Ok(framing) => {
      return __framed_body(buf, at, framing, version, limits);
    },
    Err(code) => {
      return (code, empty, 0, none);
    }
  }
  return (400, empty, 0, none);
}
```

### `71 pub function http_hop_by_hop(name: string): boolean`

```fern
<<71 pub function http_hop_by_hop(name: string): boolean>>=
pub function http_hop_by_hop(name: string): boolean {
  let n: i32 = name.len();
  if (n == 10) {
    return name.eq_ignore_ascii_case("connection") || name.eq_ignore_ascii_case("keep-alive");
  }
  if (n == 17) {
    return name.eq_ignore_ascii_case("transfer-encoding");
  }
  if (n == 16) {
    return name.eq_ignore_ascii_case("proxy-connection");
  }
  if (n == 7) {
    return name.eq_ignore_ascii_case("trailer") || name.eq_ignore_ascii_case("upgrade");
  }
  return n == 2 && name.eq_ignore_ascii_case("te");
}

pub function http_end_to_end(h: HeaderMap): HeaderMap {
  let i: i32 = 0;
  while (i < h.names.len() && !http_hop_by_hop(h.names[i])) {
    i = i + 1;
  }
  if (i == h.names.len()) {
    return h;
  }
  let out: HeaderMap = headers.header_map_new();
  i = 0;
  while (i < h.names.len()) {
    if (!http_hop_by_hop(h.names[i]) && !http_connection_names(h, h.names[i])) {
      out = out.append(h.names[i], h.values[i]);
    }
    i = i + 1;
  }
  return out;
}

pub function http_connection_names(h: HeaderMap, name: string): boolean {
  let at: i32 = __field_at(h, "connection", 0);
  while (at >= 0) {
    if (__lists_token(h.values[at], name)) {
      return true;
    }
    at = __field_at(h, "connection", at + 1);
  }
  return false;
}
```

### `72 function __lists_token(value: string, want: string): boolean`

```fern
<<72 function __lists_token(value: string, want: string): boolean>>=
function __lists_token(value: string, want: string): boolean {
  let from: i32 = 0;
  let n: i32 = value.len();
  while (from <= n) {
    let end: i32 = from;
    while (end < n && value[end] != 44 as u8) {
      end = end + 1;
    }
    if (__token_at(value, from, end, want)) {
      return true;
    }
    from = end + 1;
  }
  return false;
}
```

### `73 pub function (r: HttpResponse) body_text(): Option[string]`

```fern
<<73 pub function (r: HttpResponse) body_text(): Option[string]>>=
pub function (r: HttpResponse) body_text(): Option[string] {
  return utf8.from_bytes(r.body_bytes());
}

pub function http_method_ok(method: string): boolean {
  return __str_is_token(method);
}
```

### `74 function __str_is_token(s: string): boolean`

```fern
<<74 function __str_is_token(s: string): boolean>>=
function __str_is_token(s: string): boolean {
  return s.len() > 0 && __scan_set(s, 0, NOT_TCHAR) == s.len();
}
```

### `75 pub function http_method_idempotent(method: string): boolean`

```fern
<<75 pub function http_method_idempotent(method: string): boolean>>=
pub function http_method_idempotent(method: string): boolean {
  return method == "GET" || method == "HEAD" || method == "PUT" || method == "DELETE" || method == "OPTIONS" || method == "TRACE";
}

pub function http_target_ok(target: string): boolean {
  if (target.len() == 0 || target[0] != 47 as u8) {
    return false;
  }
  let bs: [u8] = target.as_bytes();
  let q: i32 = __find_char(bs, 0, bs.len(), 63 as u8);
  if (q < 0) {
    return __query_ok(bs, 0, bs.len());
  }
  return __query_ok(bs, 0, q) && __query_ok(bs, q + 1, bs.len());
}

pub function http_field_name_ok(name: string): boolean {
  return __str_is_token(name);
}

pub function http_field_name_lower_ok(name: string): boolean {
  return name.len() > 0 && __scan_set(name, 0, NOT_LOWER_TCHAR) == name.len();
}

pub function http_fields_ok(fields: HeaderMap): boolean {
  let i: i32 = 0;
  while (i < fields.names.len()) {
    if (!http_field_name_ok(fields.names[i]) || !http_field_value_ok(fields.values[i])) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

pub function http_field_value_ok(value: string): boolean {
  return __scan_set(value, 0, FIELD_CTL) == value.len();
}
```

### Module introduction

```fern
<<module introduction>>=
// HTTP/1.1 request parsing and response construction.
```
