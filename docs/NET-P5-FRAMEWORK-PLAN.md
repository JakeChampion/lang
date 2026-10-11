# Net P5: the framework layer — the plan

How #9859 is built, in the order it lands. One PR per slice. Each slice
names its gate; a module runs its suite through the interpreter and the
self-host compiler on x86-64, arm64 and wasm, and what touches the serve
loop runs against a real server from Go.

## Module layout

`std/serve` is the accept loop and its configuration, near the 4,000-line
cap, and `std/http` is the message layer. What P5 adds goes beside them,
each piece a module a program imports when it wants it: `std/http/router`
(patterns, parameters, 405), `std/http/form` and `std/http/multipart`
(bodies), `std/http/cookie` (the jar and the stricter `SetCookie`),
`std/http/sse` and `std/http/websocket`, `std/http/static`,
`std/http/compress`, `std/http/forwarded` (PROXY protocol v2 and
`Forwarded`), `std/http/cache` (RFC 9111's response-side helpers),
`std/http/trace` (W3C `traceparent`, the OTel conventions) and
`std/metrics`. Every module type-checks standalone
(`TestStdlibModulesImportStandalone`), and none reaches into the serve
loop except through the handler signature it already has, so a wasi-http
program uses the same router and middleware as a native one.

## Two rules the layer keeps

**A value a stage produces travels as the next stage's parameter, not in a
bag.** The epic (#9851 §4) sets this for middleware: one that authenticates
hands the user to the handler it calls, as an argument. Route parameters
are the same kind of value, produced by the router, so a route handler is
`(req, params, plat)` and `HttpRequest` stays what came off the wire. That
also keeps `HttpRequest` out of the change: it is a layout both compilers
inject (`internal/check/checker`, `compiler/parser.fern`) with byte offsets
the wasi-http wrapper hardcodes, and a field added for the router would be
a change to both for a value only routed requests have.

**Middleware is a plain function whose last parameter is the next
handler**, so `use` composes a chain in a straight line:

```fern
function timed(req: HttpRequest, plat: platform.Host, next: (HttpRequest) => HttpResponse): HttpResponse {
  let started: i64 = plat.elapsed_ns();
  let resp: HttpResponse = next(req);
  plat.log(req.method + " " + req.path + " " + (plat.elapsed_ns() - started).to_string() + "ns");
  return resp;
}

function users_show(req: HttpRequest, params: router.Params, plat: platform.Host): HttpResponse {
  use req <- timed(req, plat);
  return http.respond(show(params));
}
```

A middleware that stops the chain returns without calling `next`; one that
produces a value gives it to `next` as a second argument, and the handler
behind it takes it as a parameter. There is no middleware type to implement
and nothing to register: the router dispatches to handlers, and a handler
decides what wraps it.

## Slices

1. **`std/http/router`.** Go 1.22's patterns: `"GET /users/{id}"`,
   `{name...}` for the rest of the path, `{$}` for the end, no method for
   any, a trailing `/` for every path below it; the most specific pattern
   takes precedence; 405 with `Allow` when the path is known and the method
   is not, 404 otherwise; a GET route answers HEAD. Two patterns that could
   both match a request with neither more specific are refused when the
   second is registered, as a `RouteError` the registering call returns, so
   a program learns at startup, never at the request. `Params` carries the
   bound values percent-decoded, with `param` and `int` answering a 500 for
   a name the pattern does not bind and a 400 for a value that is not an
   integer, so a handler returning `Result[HttpResponse, http.HttpError]`
   writes `params.int("id")?` and `http.respond` turns the failure into
   problem+json. `Router[P]` is generic over the platform, so one router
   serves `serve.run` (`platform.Host`) and a wasi-http `handle`
   (`platform.Platform`); `.handler()` is the closure `serve.run` takes.
   Dispatch is a scan of the registered routes, which is right for the
   tables programs write by hand; a tree is a later change behind the same
   surface if a table ever asks for one. Gate: `tests/stdlib/http_router_test.fern`
   (patterns refused, precedence, 404 and 405, `{$}` and the trailing slash,
   parameters decoded and typed, a `use` chain over a route) in the stdtest
   differential; `TestRouterServeInterp` and `TestRouterServeX86_64`
   against the real loop.
2. **Bodies and uploads.** `std/http/form` reads a urlencoded body as its
   fields over `std/url`'s query decoder. `std/http/multipart` is an RFC
   7578 reader over the body stream: `next` gives a part's head, `read`
   hands its content over a piece at a time and `content` the rest of it,
   so the parser never holds a file part whole, and a handler writing one
   to disk never does either. Caps on the part count, the bytes of one
   part, the bytes of the body and the bytes of a part's headers come from
   `http.http_limits()` by default; a handler names its own, since the
   serve loop's `Config` does not reach it. The builder makes such a body
   for a client, and `examples/upload.fern` drives a handler with one.
   Gate: both suites in the stdtest differential; `TestUploadServeInterp`
   and `TestUploadServeX86_64`, a form with a file as Go's `mime/multipart`
   sends it, read piece by piece under the loop with bodies whole and
   streamed, the caps and the refusals answered over HTTP.
3. **Cookies.** `SetCookie` gains `domain`, `expires`, `partitioned` and a
   `SameSite` enum; serialisation enforces rfc6265bis (`__Host-` and
   `__Secure-` invariants, the 4096-byte pair, the 400-day cap, control and
   non-ASCII bytes refused); the client's opt-in per-worker jar reached
   through `plat`, with public-suffix `Domain` rejection.
4. **SSE and WebSocket.** SSE over `Body::Chunks` with the keep-alive
   comment; WebSocket (RFC 6455) with cork, the `Sent` / `Backpressure` /
   `Dropped` send result and drain event from P1, and the shared compressor
   mode. Gate: the Autobahn suite on the `net-nightly` lane.
5. **Static files, compression, proxies, caching.** ETag, `Range` and
   precompressed variants; response compression (gzip and zstd; brotli
   decode only) off by default as every 2026 server has it; PROXY protocol
   v2 and `Forwarded`; the response-side RFC 9111 helpers (validators,
   `Cache-Control`, `Vary`).
6. **Host seams as `Platform` methods.** The request deadline and cancel
   signal, background work that outlives the response (`wait_until`,
   bounded like Workers' 30 s), outbound HTTP gated by an allow-list.
7. **Observability.** The OTel HTTP semantic conventions, `traceparent`
   propagation through `plat.http`, a JSON-lines access log with the OTel
   log data model's fields, health and readiness helpers; `std/log`'s
   `Logger` gains a sink over `plat.log`; metrics as per-worker counters and
   fixed-bucket histograms in a static registry, snapshotted to the
   supervisor over its pipe once a second and served at `/metrics` in
   Prometheus text format so a scrape never touches a worker.
8. **The developer loop.** In-memory serving through the full router and
   middleware stack on the simulated `Platform`; recording and replay of
   upstream traffic; a rate-driven load tool in `tools/` with latency
   correction; httplint's response checks on the `net-nightly` lane.
9. **The exit criteria.** The tutorial's routed JSON API with logging
   middleware and graceful shutdown under 40 lines, its deterministic
   simulation with fault injection under `examples/tests/` under 20, both
   compiled in the examples lane; Autobahn and httplint green nightly;
   `internal/e2eselfhost` twins for the router, middleware, multipart and
   WebSocket tests.
