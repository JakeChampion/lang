---
title: Routing and middleware
description: Method-and-path routing with parameters, 404 and 405; middleware as plain functions composed with `use`; access logs, compression, static files and HTTP caching.
sidebar:
  order: 3
---

## A router

`std/http/router` matches a method and a path pattern, in the shape Go
1.22 uses. A route handler takes the request, the route's parameters and
the platform:

```fern
import "std/http";
import "std/http/router";
import "std/serve";
import "std/option";
import "std/platform";

function routes[P: platform.Platform](): Result[router.Router[P], router.RouteError] {
    let r: router.Router[P] = router.new();
    r = r.get("/{$}", home)?;   // "/" alone would match every path
    r = r.get("/users/{id}", show_user)?;
    r = r.delete("/users/{id}", delete_user)?;
    r = r.get("/files/{path...}", show_file)?;
    return Ok(r);
}

function home[P: platform.Platform](req: HttpRequest, params: router.Params, plat: P): HttpResponse {
    return http.plain("home\n");
}

function show_user[P: platform.Platform](req: HttpRequest, params: router.Params, plat: P): HttpResponse {
    return http.respond(user_page(params));
}

function user_page(params: router.Params): Result[HttpResponse, http.HttpError] {
    let id: i32 = params.int("id")?;
    return Ok(http.plain("user " + id.to_string() + "\n"));
}

function delete_user[P: platform.Platform](req: HttpRequest, params: router.Params, plat: P): HttpResponse {
    return http.no_content();
}

function show_file[P: platform.Platform](req: HttpRequest, params: router.Params, plat: P): HttpResponse {
    return http.plain("file " + params.get("path").unwrap_or("") + "\n");
}

function main(): i32 {
    match (routes[platform.Host]()) {
        Ok(r) => { return serve.supervise(8080, serve.config(), r.handler()); },
        Err(e) => {
            eprint(e.message());
            return 1;
        }
    }
}
```

```bash
curl localhost:8080/users/42          # → user 42
curl localhost:8080/users/abc         # → 400: path parameter id is not an integer: abc
curl -X PUT localhost:8080/users/42   # → 405, Allow: GET, HEAD, DELETE
curl localhost:8080/nope              # → 404
```

`routes` is generic over the platform, so the same table runs under the
real server and under a test's mock or simulated platform.

### Patterns

| Pattern | Matches |
|---|---|
| `"/users/{id}"` with `r.get` | `GET` (and `HEAD`) of `/users/42` |
| `"POST /users"` with `r.route` | A method written into the pattern |
| `"/users"` with `r.route` | Any method |
| `{name}` | One non-empty path segment, percent-decoded |
| `{name...}` | The rest of the path; must be last |
| `{$}` | The end of the path: `/static/{$}` matches `/static/` only |
| a trailing `/` | Everything below it: `/static/` matches `/static/a/b` |

`r.get`, `r.post`, `r.put`, `r.patch` and `r.delete` add the method for
you. The query string is ignored when matching, and the most specific
pattern wins: `/a/b` beats `/a/{x}`.

A `GET` route answers `HEAD` too. When a path is known under other
methods, the answer is `405` with an `Allow` header; otherwise it is a
`404`.

### Route errors happen at start-up

Adding a route returns a `Result`. A malformed pattern (`/a//b`, a
`{rest...}` that is not last, a parameter bound twice) is a
`BadPattern`, and two patterns that would match exactly the same
requests — `/a/{x}` and `/{y}/b` — are a `PatternConflict`. Both surface
when the router is built, before the first request, never as a
surprising match later.

### Parameters

| Call | Gives |
|---|---|
| `params.get(name)` | `Option[string]` |
| `params.param(name)` | `Result[string, http.HttpError]` — a 500 if the pattern binds no such name |
| `params.int(name)` | `Result[i32, http.HttpError]` — a 400 if the value is not an integer |

The `Result` forms are meant for `?` inside a function returning
`Result[HttpResponse, http.HttpError]`, as `user_page` does above.

## Middleware

There is no middleware type and nothing to register. **A middleware is a
function whose last parameter is the handler it wraps**:

```fern
function timed(req: HttpRequest, plat: platform.Host, next: (HttpRequest) => HttpResponse): HttpResponse {
    let started: i64 = plat.elapsed_ns();
    let resp: HttpResponse = next(req);
    plat.log(req.path + " took " + (plat.elapsed_ns() - started).to_string() + "ns");
    return resp;
}
```

It can change the request before calling `next`, change the response
after, or not call `next` at all. A handler composes the middleware it
wants with `use`, which turns the rest of the block into the `next`
callback, so a chain reads top to bottom:

```fern
import "std/http";
import "std/http/router";
import "std/serve";
import "std/platform";

function stamped(req: HttpRequest, plat: platform.Host, next: (HttpRequest) => HttpResponse): HttpResponse {
    let id: string = plat.random_i32().to_string();
    let resp: HttpResponse = next(req.with_header("x-request-id", id));
    return resp.with_header("x-request-id", id);
}

// A middleware that produces a value hands it to the rest of the handler.
function authed(req: HttpRequest, plat: platform.Host, next: (string) => HttpResponse): HttpResponse {
    match (req.headers.get("authorization")) {
        Some(token) => {
            if (token == "Bearer s3cret") {
                return next("alice");
            }
        },
        None => {}
    }
    return http.problem(401, "", "a bearer token is required")
        .with_header("www-authenticate", "Bearer");
}

function whoami(req: HttpRequest, params: router.Params, plat: platform.Host): HttpResponse {
    use req <- stamped(req, plat);
    use user <- authed(req, plat);
    return http.plain("you are " + user + "\n");
}

function main(): i32 {
    match (router.new[platform.Host]().get("/me", whoami)) {
        Ok(r) => { return serve.supervise(8080, serve.config(), r.handler()); },
        Err(e) => {
            eprint(e.message());
            return 1;
        }
    }
}
```

The authenticated user arrives as a parameter, not as a field smuggled
into the request or a context bag, so the type checker sees it. A
request with no token gets the 401, and still carries `x-request-id`,
because `stamped` wraps `authed`.

Because middleware is ordinary code, what wraps a route is decided per
handler: the health check does not pay for authentication, and the
admin routes can add a check the rest do not.

## Access logs

`std/http/accesslog` writes one JSON line per request through
`plat.log`, in the OpenTelemetry log model: method, path, status,
duration, client address, user agent and response size, with the trace
and span ids when [tracing](../production/#tracing) is on. A 5xx is
logged at `ERROR`, the rest at `INFO`.

```fern
function show(req: HttpRequest, params: router.Params, plat: platform.Host): HttpResponse {
    use req <- accesslog.logged(req, plat);
    return http.plain("ok\n");
}
```

## Compression

`std/http/compress` compresses a response when the client asks for it
and it is worth it. It negotiates `zstd` or `gzip` from
`Accept-Encoding`, always adds `Vary: Accept-Encoding`, and leaves the
response alone when it is already encoded, marked `no-transform`, a
206, under 1 KiB, of a media type that does not compress, or would not
get smaller. A compressed response's `ETag` becomes weak.

```fern
function page(req: HttpRequest, params: router.Params, plat: platform.Host): HttpResponse {
    use req <- compress.compressed(req);
    return http.html(render());
}
```

`compress.compressed_with(req, options, next)` changes the threshold or
the codings offered.

## Static files

`std/http/static` serves a directory the way a production file server
should: `ETag` and `Last-Modified` from the file, conditional requests,
`Range` and `If-Range` (including multi-range), and precompressed
`.br`, `.zst` and `.gz` siblings chosen by `Accept-Encoding`. It refuses
`..`, NUL bytes and dotfiles with a 404, lists no directories, and only
answers `GET` and `HEAD`. The handler never reads the file: it returns a
file body that the server sends with `sendfile(2)`.

```fern
import "std/http";
import "std/http/router";
import "std/http/static";
import "std/http/cache";
import "std/serve";
import "std/platform";

function assets(req: HttpRequest, params: router.Params, plat: platform.Host): HttpResponse {
    let cc: cache.CacheControl = cache.CacheControl { ...cache.cache_control(), public: true, max_age: Some(3600 as i64) };
    let d: static.Dir = static.Dir {
        ...static.dir("public"),
        options: static.Options { ...static.options(), cache_control: Some(cc) },
    };
    match (params.get("file")) {
        Some(f) => { return static.serve_path(req, d, f, plat); },
        None => { return http.not_found(); }
    }
}

function main(): i32 {
    match (router.new[platform.Host]().get("/assets/{file...}", assets)) {
        Ok(r) => { return serve.supervise(8080, serve.config(), r.handler()); },
        Err(e) => {
            eprint(e.message());
            return 1;
        }
    }
}
```

`static.serve(req, dir, plat)` serves the request's whole path instead.
A path ending in `/` serves the directory's `index.html`.

## Caching and conditional requests

`std/http/cache` handles the validator half of HTTP caching for
responses you build yourself. Give it an `ETag` and a modification time;
it answers `304 Not Modified` or `412 Precondition Failed` when the
request's `If-None-Match`, `If-Modified-Since`, `If-Match` or
`If-Unmodified-Since` call for it, and otherwise adds the validators to
your response:

```fern
import "std/http";
import "std/http/cache";
import "std/serve";
import "std/time";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let now: Instant = time.instant_from_unix(plat.now_ms() / 1000 as i64);
    let v: cache.Validators = cache.Validators {
        etag: Some(cache.strong("v42")),
        last_modified: Some(time.instant_from_unix(1760000000 as i64)),
    };
    let cc: cache.CacheControl = cache.CacheControl { ...cache.cache_control(), max_age: Some(60 as i64) };
    return cache.respond(req, v, now, http.html("<p>the page</p>")).with_cache_control(cc);
}
```

```bash
curl -i localhost:8080/                              # 200, ETag: "v42", Cache-Control: max-age=60
curl -i -H 'If-None-Match: "v42"' localhost:8080/    # 304 Not Modified
```

`cache.CacheControl` has every RFC 9111 directive as a field —
`no_store`, `private`, `immutable`, `stale_while_revalidate` and the
rest — and `cache.parse_cache_control(headers)` reads one back.
`with_vary(field)` adds to `Vary`.

Next: [State and the server →](../server/)
