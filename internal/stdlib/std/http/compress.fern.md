# HTTP response compression

This module compresses HTTP response bodies under the request policy.

## Model

Compression policy combines accepted content codings, a minimum size, and content-type eligibility. A response helper applies that policy to a body.

## Contracts

Negotiation must agree with the selected representation. Bodies that are too small or unsuitable can remain uncompressed. The configured content-code list controls the available choices.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct Options>>

<<4 pub function options(): Options>>

<<5 function __written(codings: string[]): string[]>>

<<6 pub function compressible(content_type: string): boolean>>

<<7 function __media_type(content_type: string): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module compresses HTTP response bodies under the request policy.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/deflate";
import "std/zstd";
import "std/headers";
import "std/http";
import "std/http/cache";
```

### `3 pub struct Options`

```fern
<<3 pub struct Options>>=
pub struct Options { min_size: i32, codings: string[] }
```

### `4 pub function options(): Options`

```fern
<<4 pub function options(): Options>>=
pub function options(): Options {
  return Options { min_size: 1024, codings: ["zstd", "gzip"] };
}

pub function compressed(req: HttpRequest, next: (HttpRequest) => Result[HttpResponse, dyn http.ToResponse]): Result[HttpResponse, dyn http.ToResponse] {
  return Ok(response(req, http.respond(next(req)), options()));
}

pub function compressed_with(req: HttpRequest, o: Options, next: (HttpRequest) => Result[HttpResponse, dyn http.ToResponse]): Result[HttpResponse, dyn http.ToResponse] {
  return Ok(response(req, http.respond(next(req)), o));
}

pub function response(req: HttpRequest, resp: HttpResponse, o: Options): HttpResponse {
  if (!__eligible(resp, o)) {
    return resp;
  }
  let varied: HttpResponse = resp.with_vary("Accept-Encoding");
  match (req.headers.preferred_coding(__written(o.codings))) {
    Some(c) => {
      return __coded(varied, c, resp.body_bytes());
    },
    None => {
      return varied;
    }
  }
  return varied;
}

pub function encode(coding: string, body: u8[]): Option[u8[]] {
  if (coding == "gzip") {
    return Some(deflate.gzip(body));
  }
  if (coding == "zstd") {
    return Some(zstd.compress(body));
  }
  return None;
}
```

### `5 function __written(codings: string[]): string[]`

```fern
<<5 function __written(codings: string[]): string[]>>=
function __written(codings: string[]): string[] {
  let out: string[] = [];
  for c in codings {
    if (c == "gzip" || c == "zstd") {
      out = out.append(c);
    }
  }
  return out;
}
```

### `6 pub function compressible(content_type: string): boolean`

```fern
<<6 pub function compressible(content_type: string): boolean>>=
pub function compressible(content_type: string): boolean {
  let t: string = __media_type(content_type);
  if (t.starts_with("text/") || t.ends_with("+json") || t.ends_with("+xml")) {
    return true;
  }
  let listed: string[] = ["application/json", "application/javascript", "application/ecmascript", "application/xml", "application/wasm", "application/x-ndjson", "application/graphql-response+json", "image/x-icon", "image/vnd.microsoft.icon", "font/ttf", "font/otf", "application/vnd.ms-fontobject"];
  for l in listed {
    if (t == l) {
      return true;
    }
  }
  return false;
}
```

### `7 function __media_type(content_type: string): string`

```fern
<<7 function __media_type(content_type: string): string>>=
function __media_type(content_type: string): string {
  if let Some(semi) = content_type.index_of(";") {
    return content_type.slice_snap(0, semi).trim().to_lower();
  }
  return content_type.trim().to_lower();
}

function __eligible(resp: HttpResponse, o: Options): boolean {
  if (resp.status == 206 || http.http_bodiless(resp.status) || resp.headers.has("Content-Encoding")) {
    return false;
  }
  if (cache.parse_cache_control(resp.headers).no_transform || resp.body_len() < o.min_size) {
    return false;
  }
  match (resp.headers.get("Content-Type")) {
    Some(ct) => {
      return __whole(resp.body) && compressible(ct);
    },
    None => {
      return false;
    }
  }
  return false;
}

function __whole(b: Body): boolean {
  match (b) {
    BodyText(_) => {
      return true;
    },
    BodyBytes(_) => {
      return true;
    },
    _ => {
      return false;
    }
  }
  return false;
}

function __coded(r: HttpResponse, coding: string, body: u8[]): HttpResponse {
  match (encode(coding, body)) {
    Some(z) => {
      if (z.len() >= body.len()) {
        return r;
      }
      let h: HeaderMap = r.headers.set("Content-Encoding", coding).remove("Accept-Ranges");
      return HttpResponse { ...r, body: BodyBytes(z), headers: __weak_etag(h) };
    },
    None => {
      return r;
    }
  }
  return r;
}

function __weak_etag(h: HeaderMap): HeaderMap {
  match (h.get("ETag")) {
    Some(e) => {
      if (e.starts_with("\"")) {
        return h.set("ETag", "W/" + e);
      }
      return h;
    },
    None => {
      return h;
    }
  }
  return h;
}
```
