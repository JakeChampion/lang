# Cross-origin request policy

This module applies CORS policies to HTTP requests and responses.

## Model

A policy names allowed origins, methods, request headers, and exposed response headers. Credential use and preflight lifetime are separate fields.

## Contracts

The wrapper applies this policy to the request and handler result. Wildcard origin policy and credential policy must remain consistent.

## Executable example

```fern test name=std-http-cors-example
import "std/http/cors";

function main(): i32 {
  let policy = cors.policy(["https://example.com"]);
  if (!policy.allows_origin("https://example.com") || policy.allows_origin("https://other.example")) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct Policy>>

<<4 pub function policy(origins: string[]): Policy>>

<<5 function __admits(p: Policy, origin: string, method: string, asked: string[]): boolean>>

<<6 pub function allowed(resp: HttpResponse, origin: string, p: Policy): HttpResponse>>

<<7 function __origin_headers(resp: HttpResponse, origin: string, p: Policy): HttpResponse>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module applies CORS policies to HTTP requests and responses.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/http";
import "std/http/cache";
import "std/headers";
import "std/string";
import "std/i64";
```

### `3 pub struct Policy`

```fern
<<3 pub struct Policy>>=
pub struct Policy { origins: string[], methods: string[], headers: string[], exposed: string[], credentials: boolean, max_age: i64 }
```

### `4 pub function policy(origins: string[]): Policy`

```fern
<<4 pub function policy(origins: string[]): Policy>>=
pub function policy(origins: string[]): Policy {
  return Policy { origins: origins, methods: ["GET", "HEAD", "POST"], headers: [], exposed: [], credentials: false, max_age: 0 - 1 as i64 };
}

pub function any_origin(): Policy {
  return policy(["*"]);
}

pub function (p: Policy) with_methods(methods: string[]): Policy {
  return Policy { ...p, methods: methods };
}

pub function (p: Policy) with_headers(names: string[]): Policy {
  return Policy { ...p, headers: names };
}

pub function (p: Policy) with_exposed(names: string[]): Policy {
  return Policy { ...p, exposed: names };
}

pub function (p: Policy) with_credentials(): Policy {
  return Policy { ...p, credentials: true };
}

pub function (p: Policy) with_max_age(seconds: i64): Policy {
  return Policy { ...p, max_age: seconds };
}

pub function cors(req: HttpRequest, p: Policy, next: (HttpRequest) => Result[HttpResponse, dyn http.ToResponse]): Result[HttpResponse, dyn http.ToResponse] {
  match (req.headers.get("origin")) {
    Some(origin) => {
      match (preflight(req, origin, p)) {
        Some(answer) => {
          return Ok(answer);
        },
        None => {
          return Ok(allowed(http.respond(next(req)), origin, p));
        }
      }
    },
    None => {
      return next(req);
    }
  }
  return next(req);
}

pub function (p: Policy) allows_origin(origin: string): boolean {
  for o in p.origins {
    if (o == "*" || o == origin || __wildcard_origin(o, origin)) {
      return true;
    }
  }
  return false;
}

pub function preflight(req: HttpRequest, origin: string, p: Policy): Option[HttpResponse] {
  if (req.method != "OPTIONS") {
    return None;
  }
  let method: string = "";
  match (req.headers.get("access-control-request-method")) {
    Some(m) => {
      method = m.trim();
    },
    None => {
      return None;
    }
  }
  let bare: HttpResponse = http.no_content().with_vary("Origin").with_vary("Access-Control-Request-Method").with_vary("Access-Control-Request-Headers");
  let asked: string[] = __asked_headers(req);
  if (!__admits(p, origin, method, asked)) {
    return Some(bare);
  }
  let out: HttpResponse = __origin_headers(bare, origin, p).with_header("Access-Control-Allow-Methods", method);
  if (asked.len() > 0) {
    out = out.with_header("Access-Control-Allow-Headers", asked.join(", "));
  }
  if (p.max_age >= 0 as i64) {
    out = out.with_header("Access-Control-Max-Age", p.max_age.to_string());
  }
  return Some(out);
}
```

### `5 function __admits(p: Policy, origin: string, method: string, asked: string[]): boolean`

```fern
<<5 function __admits(p: Policy, origin: string, method: string, asked: string[]): boolean>>=
function __admits(p: Policy, origin: string, method: string, asked: string[]): boolean {
  if (!p.allows_origin(origin) || !__has_ci(p.methods, method)) {
    return false;
  }
  if (__has_str(p.headers, "*")) {
    return true;
  }
  for h in asked {
    if (!__has_ci(p.headers, h)) {
      return false;
    }
  }
  return true;
}
```

### `6 pub function allowed(resp: HttpResponse, origin: string, p: Policy): HttpResponse`

```fern
<<6 pub function allowed(resp: HttpResponse, origin: string, p: Policy): HttpResponse>>=
pub function allowed(resp: HttpResponse, origin: string, p: Policy): HttpResponse {
  let out: HttpResponse = resp.with_vary("Origin");
  if (!p.allows_origin(origin)) {
    return out;
  }
  out = __origin_headers(out, origin, p);
  if (p.exposed.len() > 0) {
    out = out.with_header("Access-Control-Expose-Headers", p.exposed.join(", "));
  }
  return out;
}
```

### `7 function __origin_headers(resp: HttpResponse, origin: string, p: Policy): HttpResponse`

```fern
<<7 function __origin_headers(resp: HttpResponse, origin: string, p: Policy): HttpResponse>>=
function __origin_headers(resp: HttpResponse, origin: string, p: Policy): HttpResponse {
  let value: string = origin;
  if (!p.credentials && __has_str(p.origins, "*")) {
    value = "*";
  }
  let out: HttpResponse = resp.with_header("Access-Control-Allow-Origin", value);
  if (p.credentials) {
    out = out.with_header("Access-Control-Allow-Credentials", "true");
  }
  return out;
}

function __asked_headers(req: HttpRequest): string[] {
  let out: string[] = [];
  for raw in req.headers.get_all("access-control-request-headers") {
    for name in raw.split(",") {
      let t: string = name.trim();
      if (t.len() > 0) {
        out = out.append(t);
      }
    }
  }
  return out;
}

function __wildcard_origin(pattern: string, origin: string): boolean {
  let Some(star) = pattern.index_of("://*.") else {
    return false;
  };
  let scheme: string = pattern.slice_snap(0, star + 3);
  let domain: string = pattern.slice_snap(star + 4, pattern.len());
  if (!origin.starts_with(scheme) || !origin.ends_with(domain)) {
    return false;
  }
  let host: string = origin.slice_snap(scheme.len(), origin.len() - domain.len());
  return host.len() > 0 && !host.contains("/") && !host.contains(":");
}

function __has_str(xs: string[], x: string): boolean {
  for y in xs {
    if (y == x) {
      return true;
    }
  }
  return false;
}

function __has_ci(xs: string[], x: string): boolean {
  for y in xs {
    if (y.eq_ignore_case(x)) {
      return true;
    }
  }
  return false;
}
```
