# HTTP access log records

This module records each HTTP request as a JSON log entry.

## Model

An access record combines request fields, response fields, and elapsed time into JSON. A platform supplies the clock and log destination.

## Contracts

The wrapper calls the next handler and records its outcome. The record builder returns text so callers can choose a different log destination.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub function logged[P: platform.Platform](req: HttpRequest, plat: P, next: (HttpRequest)>>

<<4 function __severity(status: i32): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module records each HTTP request as a JSON log entry.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/http";
import "std/json";
import "std/platform";
import "std/net";
import "std/headers";
import "std/i32";
import "std/i64";
```

### `3 pub function logged[P: platform.Platform](req: HttpRequest, plat: P, next: (HttpRequest)`

```fern
<<3 pub function logged[P: platform.Platform](req: HttpRequest, plat: P, next: (HttpRequest)>>=
pub function logged[P: platform.Platform](req: HttpRequest, plat: P, next: (HttpRequest) => Result[HttpResponse, dyn http.ToResponse]): Result[HttpResponse, dyn http.ToResponse] {
  let started: i64 = plat.elapsed_ns();
  let resp: HttpResponse = http.respond(next(req));
  plat.log(record(req, resp, plat, plat.elapsed_ns() - started));
  return Ok(resp);
}

pub function record[P: platform.Platform](req: HttpRequest, resp: HttpResponse, plat: P, took_ns: i64): string {
  let at: i64 = plat.now_ms() * (1000000 as i64);
  let out: string = "{\"timeUnixNano\":\"" + at.to_string() + "\"," + __severity(resp.status) + ",\"body\":" + __quoted(req.method + " " + req.path + " " + resp.status.to_string());
  match (plat.trace()) {
    Some(span) => {
      out = out + ",\"traceId\":\"" + span.trace_id + "\",\"spanId\":\"" + span.span_id + "\"";
    },
    None => {}
  }
  return out + ",\"attributes\":{" + __attributes(req, resp, plat, took_ns) + "}}";
}
```

### `4 function __severity(status: i32): string`

```fern
<<4 function __severity(status: i32): string>>=
function __severity(status: i32): string {
  if (status >= 500) {
    return "\"severityNumber\":17,\"severityText\":\"ERROR\"";
  }
  return "\"severityNumber\":9,\"severityText\":\"INFO\"";
}

function __attributes[P: platform.Platform](req: HttpRequest, resp: HttpResponse, plat: P, took_ns: i64): string {
  let out: string = "\"http.request.method\":" + __quoted(req.method) + ",\"url.path\":" + __quoted(req.path) + ",\"http.response.status_code\":" + resp.status.to_string() + ",\"http.server.request.duration\":" + __seconds(took_ns);
  match (plat.peer()) {
    Some(addr) => {
      out = out + ",\"client.address\":" + __quoted(addr.ip.to_string());
    },
    None => {}
  }
  match (req.headers.get("user-agent")) {
    Some(ua) => {
      out = out + ",\"user_agent.original\":" + __quoted(ua);
    },
    None => {}
  }
  let size: i64 = __body_size(resp.body);
  if (size >= 0 as i64) {
    out = out + ",\"http.response.body.size\":" + size.to_string();
  }
  return out;
}

function __body_size(body: Body): i64 {
  match (body) {
    BodyText(s) => {
      return s.len() as i64;
    },
    BodyBytes(bs) => {
      return bs.len() as i64;
    },
    BodyFileRanges(path, ranges, seams) => {
      return http.file_ranges_len(ranges, seams);
    },
    _ => {
      return 0 - 1 as i64;
    }
  }
  return 0 - 1 as i64;
}

function __seconds(ns: i64): string {
  let us: i64 = ns / (1000 as i64);
  let frac: string = (us % (1000000 as i64)).to_string();
  while (frac.len() < 6) {
    frac = "0" + frac;
  }
  return (us / (1000000 as i64)).to_string() + "." + frac;
}

function __quoted(s: string): string {
  return "\"" + json.json_escape(s) + "\"";
}
```
