---
title: Routing and middleware
description: Method-and-path routing with parameters, 404 and 405; middleware as plain functions composed with `use`; access logs, compression, static files and HTTP caching.
sidebar:
  order: 3
---

## A router

`std/http/router` matches a method and a path pattern, in the shape Go
1.22 uses. A route handler takes the request, the route's parameters and
the platform, and answers a `Result`: `Ok` with the response, or `Err`
with any error that answers for itself, so a handler fails with `?` and
the router turns the failure into the reply.

```fern
import "std/http";
import "std/http/router";
import "std/serve";
import "std/option";
import "std/platform";

function routes[P: platform.Platform](): router.Router[P] {
    return router.new[P]()
        .get("/{$}", home)   // "/" alone would match every path
        .get("/users/{id}", show_user)
        .delete("/users/{id}", delete_user)
        .get("/files/{path...}", show_file);
}

function home[P: platform.Platform](req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] {
    return Ok(http.plain("home\n"));
}

function show_user[P: platform.Platform](req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] {
    let id: i32 = params.int("id")?;
    return Ok(http.plain("user " + id.to_string() + "\n"));
}

function delete_user[P: platform.Platform](req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] {
    return Ok(http.no_content());
}

function show_file[P: platform.Platform](req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] {
    return Ok(http.plain("file " + params.get("path").unwrap_or("") + "\n"));
}

function main(): i32 {
    return serve.supervise(8080, serve.config(), routes[platform.Host]().handler());
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

A malformed pattern (`/a//b`, a `{rest...}` that is not last, a parameter
bound twice) is a `BadPattern`, and two patterns that could both match
some request with neither more specific than the other — `/a/{x}` and
`/{y}/b` both take `/a/b` — are a `PatternConflict`. The router keeps the first refusal through every registration after it, so
the table is built in one chain, and `handler()` reports the refusal on
stderr and exits the program with status 2 before anything is served. A
program that would rather handle it reads `r.check()`, which is `Ok(r)`
or `Err(RouteError)`; until the pattern is fixed, `r.handle` answers
every request `500` naming it. Either way the mistake surfaces at
start-up, never as a surprising match later.

### Errors that answer for themselves

A handler's error type is `dyn http.ToResponse`: any value whose type
implements `ToResponse` fits, and `?` boxes it. `http.HttpError` answers
as problem details, `http.BodyError` as the 400, 413 or 415 the body
earned, `form.FormError`, `cookie.CookieError` and `multipart.MultipartError`
as their own statuses, and `fetch.FetchError` as a 502 or 504. One
handler mixes them freely:

```fern
import "std/http";
import "std/http/router";
import "std/json";
import "std/platform";

@derive(json.Json, json.FromJson)
struct Note { id: i32, text: string }

function create[P: platform.Platform](req: HttpRequest, params: router.Params, plat: P): Result[HttpResponse, dyn http.ToResponse] {
    let n: Note = http.body_json[Note](req)?;        // 415, 400 or 413 when the body is wrong
    if (n.id < 0) {
        return Err(http.fail(422, "a note's id is not negative"));
    }
    return Ok(http.json_of_status(201, n));           // the struct, serialised
}
```

### Parameters

| Call | Gives |
|---|---|
| `params.get(name)` | `Option[string]` |
| `params.param(name)` | `Result[string, http.HttpError]` — a 500 if the pattern binds no such name |
| `params.int(name)` | `Result[i32, http.HttpError]` — a 400 if the value is not an integer |

The `Result` forms are meant for `?` inside the handler, as `show_user`
does above.

## Middleware

There is no middleware type to implement. **A middleware is a function
whose last parameter is the handler it wraps**, and it answers the same
`Result` a route handler does:

```fern
function timed(req: HttpRequest, plat: platform.Host, next: (HttpRequest) => Result[HttpResponse, dyn http.ToResponse]): Result[HttpResponse, dyn http.ToResponse] {
    let started: i64 = plat.elapsed_ns();
    let resp: HttpResponse = http.respond(next(req));
    plat.log(req.path + " took " + (plat.elapsed_ns() - started).to_string() + "ns");
    return Ok(resp);
}
```

It can change the request before calling `next`, change the response
after, or not call `next` at all. `http.respond` turns a failure behind
it into the response it will become, so a middleware that reads the
status sees the one the client gets; one that does not care passes the
`Result` straight through. A handler composes the middleware it wants
with `use`, which turns the rest of the block into the `next` callback,
so a chain reads top to bottom:

```fern
import "std/http";
import "std/http/router";
import "std/serve";
import "std/platform";

function stamped(req: HttpRequest, plat: platform.Host, next: (HttpRequest) => Result[HttpResponse, dyn http.ToResponse]): Result[HttpResponse, dyn http.ToResponse] {
    let id: string = plat.random_i32().to_string();
    let resp: HttpResponse = http.respond(next(req.with_header("x-request-id", id)));
    return Ok(resp.with_header("x-request-id", id));
}

// A middleware that produces a value hands it to the rest of the handler.
function authed(req: HttpRequest, plat: platform.Host, next: (string) => Result[HttpResponse, dyn http.ToResponse]): Result[HttpResponse, dyn http.ToResponse] {
    match (req.headers.get("authorization")) {
        Some(token) => {
            if (token == "Bearer s3cret") {
                return next("alice");
            }
        },
        None => {}
    }
    return Ok(http.problem(401, "", "a bearer token is required")
        .with_header("www-authenticate", "Bearer"));
}

function whoami(req: HttpRequest, params: router.Params, plat: platform.Host): Result[HttpResponse, dyn http.ToResponse] {
    use req <- stamped(req, plat);
    use user <- authed(req, plat);
    return Ok(http.plain("you are " + user + "\n"));
}

function main(): i32 {
    return serve.supervise(8080, serve.config(), router.new[platform.Host]().get("/me", whoami).handler());
}
```

The authenticated user arrives as a parameter, not as a field smuggled
into the request or a context bag, so the type checker sees it. A
request with no token gets the 401, and still carries `x-request-id`,
because `stamped` wraps `authed`.

Because middleware is ordinary code, what wraps a route is decided per
handler: the health check does not pay for authentication, and the
admin routes can add a check the rest do not.

### Wrapping the whole router

A middleware in the shape above, `(req, plat, next)`, also goes around
every route at once through `router.wrap`, where it runs before the
router picks a route. That is where a middleware belongs when it must
see requests no route admits: a CORS preflight is an `OPTIONS` the
routes do not register, so a `cors.cors` inside a handler would never
run on one and the router would answer `405`. The first `wrap` is the
outermost: it sees the request first and the response last, and the
`404` and `405` the router answers pass through it like any response.

```fern
function cross_origin(req: HttpRequest, plat: platform.Host, next: (HttpRequest) => Result[HttpResponse, dyn http.ToResponse]): Result[HttpResponse, dyn http.ToResponse] {
    return cors.cors(req, cors.policy(["https://app.example.com"]).with_methods(["GET", "POST", "DELETE"]).with_headers(["Content-Type"]).with_credentials(), next);
}

function routes(): router.Router[platform.Host] {
    return router.new[platform.Host]().wrap(cross_origin).get("/users", users).delete("/users/{id}", users_delete);
}
```

## Stock middleware

Three middleware ship with the standard library, each a function in the
shape above.

**CORS** (`std/http/cors`): a policy names the origins it admits, exactly
(`https://app.example.com`), by subdomain (`https://*.example.com`) or
`*`, plus the methods and request headers a preflight may ask for, the
response headers a script may read, credentials and the preflight cache
age. A preflight is answered without reaching the routes, bare when it asks
for something the policy does not admit; an actual response from an
admitted origin gains the allow headers. It goes around the router,
through `wrap` as `cross_origin` does above, since a preflight is an
`OPTIONS` no route registers.

**Authentication** (`std/http/auth`): `bearer` and `basic` read
`Authorization`, hand the credential to the program's check, and give
the rest of the handler the principal it answered; anything else is a
401 with its `WWW-Authenticate` challenge, as problem details. A static
secret is compared with `auth.token_eq`, whose time does not depend on
where the two differ.

```fern
function by_token(token: string): Option[string] {
    if (auth.token_eq(token, "s3cret")) {
        return Some("alice");
    }
    return None;
}

function me(req: HttpRequest, params: router.Params, plat: platform.Host): Result[HttpResponse, dyn http.ToResponse] {
    use user <- auth.bearer(req, by_token);
    return Ok(http.plain("you are " + user + "\n"));
}
```

**Request ids** (`std/http/requestid`): `stamped` keeps a well-formed
`X-Request-Id` the request arrived with and mints one otherwise, hands
the handler the request carrying it and puts the same id on the
response; `requestid.of(req)` reads it for a log line or an upstream
call.

```fern
function users(req: HttpRequest, params: router.Params, plat: platform.Host): Result[HttpResponse, dyn http.ToResponse] {
    use req <- requestid.stamped(req, plat);
    use req <- accesslog.logged(req, plat);
    return Ok(http.json_of(list()));
}
```

## Access logs

`std/http/accesslog` writes one JSON line per request through
`plat.log`, in the OpenTelemetry log model: method, path, status,
duration, client address, user agent and response size, with the trace
and span ids when [tracing](../production/#tracing) is on. A 5xx is
logged at `ERROR`, the rest at `INFO`.

```fern
function show(req: HttpRequest, params: router.Params, plat: platform.Host): Result[HttpResponse, dyn http.ToResponse] {
    use req <- accesslog.logged(req, plat);
    return Ok(http.plain("ok\n"));
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
function page(req: HttpRequest, params: router.Params, plat: platform.Host): Result[HttpResponse, dyn http.ToResponse] {
    use req <- compress.compressed(req);
    return Ok(http.html(render()));
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

function assets(req: HttpRequest, params: router.Params, plat: platform.Host): Result[HttpResponse, dyn http.ToResponse] {
    let cc: cache.CacheControl = cache.CacheControl { ...cache.cache_control(), public: true, max_age: Some(3600 as i64) };
    let d: static.Dir = static.Dir {
        ...static.dir("public"),
        options: static.Options { ...static.options(), cache_control: Some(cc) },
    };
    let file: string = params.param("file")?;
    return Ok(static.serve_path(req, d, file, plat));
}

function main(): i32 {
    return serve.supervise(8080, serve.config(), router.new[platform.Host]().get("/assets/{file...}", assets).handler());
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
