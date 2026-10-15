---
title: Requests and responses
description: Headers, query strings, typed JSON bodies, forms and cookies — and handlers that fail with `?`, where each error knows the response it answers with.
sidebar:
  order: 2
---

## Headers

Headers live in a `HeaderMap`: names compare without regard to case,
order is kept, and the map is a value, so every change returns a new one.

```fern
import "std/http";
import "std/serve";
import "std/headers";
import "std/option";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let agent: string = req.headers.get("user-agent").unwrap_or("unknown");
    // Comma-separated list members, across repeated lines (RFC 9110 §5.6.1).
    let accepts: string[] = req.headers.get_list("accept");
    // Content negotiation: the best of what we offer, by the client's q-values.
    let coding: string = req.headers.preferred_coding(["zstd", "gzip"]).unwrap_or("identity");
    return http.plain(agent + "\n" + accepts.len().to_string() + " accepted types\n" + coding + "\n");
}
```

`set`, `append`, `remove`, `has`, `get_all` and `len` are the rest of the
`HeaderMap` surface (`std/headers`).

## Query strings

`req.path` carries the query. `std/url` splits and decodes it; a
repeated key keeps every value.

```fern
import "std/http";
import "std/serve";
import "std/url";
import "std/option";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let parts: string[] = req.path.split("?");
    let query: string = "";
    if (parts.len() > 1) {
        query = parts[1];
    }
    let q: string = url.query_get(query, "q").unwrap_or("");
    let tags: string[] = url.query_get_all(query, "tag");
    let verbose: boolean = url.query_has(query, "verbose");
    return http.plain("q=" + q + " tags=" + tags.len().to_string() + " verbose=" + verbose.to_string() + "\n");
}
```

`url.query_parse(query)` gives the whole thing as a
`Map[string, string[]]`, and `url.query_encode(pairs)` goes the other
way. For a full URL, `url.url_parse` returns its parts.

## JSON in, JSON out

Derive `json.FromJson` on a struct and `http.body_json[T](req)` decodes
the body into it; derive `json.Json` and `value.to_json()` encodes it.

```fern
import "std/http";
import "std/serve";
import "std/json";
import "std/platform";

@derive(json.Json, json.FromJson)
struct Item { name: string, qty: i32, tags: string[] }

function handle(req: HttpRequest, plat: platform.Platform): Result[HttpResponse, http.BodyError] {
    let item: Item = http.body_json[Item](req)?;
    let saved: Item = Item { ...item, qty: item.qty + 1 };
    return Ok(http.json_status(201, saved.to_json()));
}
```

```bash
curl -H 'content-type: application/json' -d '{"name":"axe","qty":2,"tags":[]}' localhost:8080/
# → {"name":"axe","qty":3,"tags":[]}
curl -d '{"name":"axe"}' localhost:8080/
# → 415, application/problem+json: the media type must be JSON
```

Each way the decode can fail answers with its own status, as an RFC 9457
problem-details body:

| `http.BodyError` | Status | When |
|---|---|---|
| `UnsupportedMediaType` | 415 | `Content-Type` is not `application/json` or a `+json` type |
| `NotUtf8` | 400 | The body is not UTF-8 |
| `MalformedJson` | 400 | Not JSON; the detail names line and column |
| `WrongShape` | 422 | JSON, but not an `Item` |
| `EndedEarly` | 413 / 408 / 400 | A streamed body was cut off (too large, too slow, malformed) |

An `Option` field is `None` when the key is missing or `null`. For
untyped JSON, `json.json_parse` gives a `json.JsonValue` to walk.

## Handlers that fail with `?`

A handler can return `Result[HttpResponse, E]`. Any error type that
implements `http.ToResponse` works, and the compiler wraps the handler so
an `Err` becomes its response. `http.fail(status, detail)` is the
general-purpose error:

```fern
import "std/http";
import "std/serve";
import "std/option";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): Result[HttpResponse, http.HttpError] {
    let name: string = req.headers.get("x-name")
        .ok_or(http.fail(400, "send an x-name header"))?;
    if (name.len() > 64) {
        return Err(http.fail(413, "that name is too long"));
    }
    return Ok(http.plain("hello, " + name + "\n"));
}
```

```bash
curl localhost:8080/
# → {"type":"about:blank","title":"Bad Request","status":400,"detail":"send an x-name header"}
```

The errors the standard library raises already implement `ToResponse`:
`http.BodyError`, `json.JsonError` (400), `fetch.FetchError` (504 on a
timeout, 502 otherwise, never echoing the upstream's name), the router's
parameter errors, `form.FormError`, `multipart.MultipartError`,
`websocket.UpgradeError` and `cookie.CookieError`. Your own error type
joins in with an `impl`:

```fern
import "std/http";
import "std/serve";
import "std/platform";

enum ShopError { OutOfStock(string), Closed }

impl http.ToResponse for ShopError {
    function to_response(self: ShopError): HttpResponse {
        match (self) {
            OutOfStock(item) => { return http.problem(409, "Out of stock", item + " is sold out"); },
            Closed => { return http.problem(503, "", "the shop is closed").with_header("retry-after", "3600"); }
        }
    }
}

function buy(item: string): Result[string, ShopError] {
    if (item == "axe") {
        return Err(OutOfStock(item));
    }
    return Ok(item);
}

function handle(req: HttpRequest, plat: platform.Platform): Result[HttpResponse, ShopError] {
    let bought: string = buy(req.path.drop(1))?;
    return Ok(http.plain("bought " + bought + "\n"));
}
```

When a handler composes errors of several types, `map_err` converts one
into the handler's error type at the `?`. A handler may also return
`Result[HttpResponse, dyn error.Error]`: any error converts into it, and
an `Err` is logged through `plat.log` and answered with a bare 500, so
internal messages never reach the client.

Outside a top-level `handle` — in a route handler, say — call
`http.respond(result)` to turn a `Result` into a response yourself.

## Forms

`std/http/form` reads `application/x-www-form-urlencoded`:

```fern
import "std/http";
import "std/serve";
import "std/http/form";
import "std/option";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): Result[HttpResponse, form.FormError] {
    let f: form.Form = form.parse(req)?;
    let who: string = f.get("name").unwrap_or("stranger");
    let picked: string[] = f.get_all("topping");
    return Ok(http.plain(who + " picked " + picked.len().to_string() + " toppings\n"));
}
```

A body of another media type is a 415. File uploads
(`multipart/form-data`) are read as a stream, part by part — see
[Streaming](../streaming/#uploads).

## Cookies

`std/http/cookie` reads the request's `Cookie` header and builds
`Set-Cookie` values. `cookie.new` starts from safe defaults — `Path=/`,
`HttpOnly`, `SameSite=Lax` — and `serialize` refuses a cookie that breaks
the rules (a bad name or value byte, a `__Host-` prefix without `Secure`,
`SameSite=None` without `Secure`, a lifetime past 400 days).

```fern
import "std/http";
import "std/serve";
import "std/http/cookie";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): Result[HttpResponse, cookie.CookieError] {
    match (req.cookie("sid")) {
        Some(sid) => { return Ok(http.plain("welcome back, " + sid + "\n")); },
        None => {}
    }
    let c: cookie.SetCookie = cookie.SetCookie {
        ...cookie.new("sid", "abc123"),
        secure: true,
        max_age: 3600,
        same_site: cookie.Strict,
    };
    return http.plain("hello\n").with_cookie(c);
}
```

That sets `sid=abc123; Path=/; Max-Age=3600; Secure; HttpOnly;
SameSite=Strict`. `cookie.delete(name)` builds the `Max-Age=0` cookie
that removes one. On the client side a cookie jar is available too — see
[Calling other services](../client/#cookies).

## Redirects and status helpers

`http.redirect(location)` is a 302. For another code, set the header
yourself: `http.text(308, "").with_header("location", "/new")`.
`http.http_status_text(code)` gives the reason phrase, and
`http.http_is_success`, `http_is_client_error` and friends classify a
code.

Next: [Routing and middleware →](../routing-middleware/)
