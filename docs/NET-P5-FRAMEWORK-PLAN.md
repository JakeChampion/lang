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
3. **Cookies.** `std/http/cookie` takes the cookie family over from
   `std/http`: `SetCookie` gains `domain`, `expires`, `partitioned` and a
   `SameSite` enum, and `serialize` (and `resp.with_cookie(c)`) answers a
   `CookieError` for what no browser would keep (a name that is not a
   token, a value byte outside the cookie-octets, the 4096-byte pair, a
   1024-byte attribute, a Max-Age past 400 days, the `__Host-` and
   `__Secure-` invariants, `Partitioned` or `SameSite=None` without
   `Secure`), a 500 through `http.respond` since it is the program's
   mistake. The jar stores what a response set under rfc6265bis §5.7 (the
   Domain and path rules, Secure over plain http, the prefixes, the 400-day
   cap, eviction by last use past 50 per domain and 3000) and writes the
   `Cookie` header in §5.8.3's order; a Domain that is a public suffix is
   refused through `std/publicsuffix`, publicsuffix.org's ICANN section
   generated by `cmd/pslgen`, which the jar carries as a closure so a
   program with no jar pays nothing for the list. The jar is reached
   through the platform (`Host.with_jar`, `serve.Config { cookie_jar }` for
   one per worker, `SimPlatform.with_jar`) and `fetch.Policy.jar`, so every
   hop of a redirect carries and stores cookies with the transport's clock.
   Gate: `tests/stdlib/http_cookie_test.fern` in the stdtest differential,
   its last case the client's route over the scripted network;
   `conformance/cases/http_cookies`.
4. **SSE and WebSocket.** `std/http/sse` (landed): `Event` and its
   encoding, `respond` over `Body::Chunks`, `respond_with` writing the
   keep-alive comment while the producer waits, and the client's
   `Decoder`. For a producer to wait at all, the serve loop runs a chunk
   producer as a task: one that parks becomes a flight on its connection
   (`ParkedTail`, a `Producer` job beside the handler's), woken by what it
   waits on, so an event stream costs the worker nothing between events;
   the keep-alive comment is the loop's too (`http.chunks_idle`, a
   filler the producer carries and the loop writes while it is parked
   past the interval), since a handler reaches no clock and no wait
   around its platform (E080). The wait a producer makes between events
   is the platform's: `Platform.sleep(d)`, which parks under the loop,
   naps in a plain program, and spends the simulation's clock in the sim.
   Gate:
   `tests/stdlib/http_sse_test.fern` in the stdtest differential;
   `TestSelfHostServeSSE` (x86-64, arm64) streaming while another
   connection is answered, `TestServeSSESequentialInterp` its twin.
   `std/http/websocket` (landed): the handshake and the session as
   callbacks (`open`, `message`, `drain`, `close`), what a callback sends
   corked into one write when it returns, with `Sent` / `Backpressure` /
   `Dropped` from the cork's size against `Options` and `drain` once a
   cork past the soft bound has gone out. The serve loop's part: an
   upgrade request's body is streamed as the connection's bytes after
   the head (the lent pull of `stream_bodies`, raw framing, bounded by
   `keep_alive_idle`), `BodySource` gained `send` as the write side of
   that connection, and a handler answering 101 with a producer keeps
   the connection for the session, the producer running it as a parked
   flight that carries the lent body (TLS writes go through the lent
   session). Gate: `tests/stdlib/http_websocket_test.fern` in the stdtest
   differential; `TestSelfHostServeWebSocket` (x86-64, arm64) and
   `TestServeWebSocketSequentialInterp`. permessage-deflate (RFC 7692)
   in its shared-compressor mode: `Options.compression` takes the
   client's offer with no context takeover on either side, so no
   connection holds a window; `std/deflate` gained the encoder it needs
   (`deflate`, `deflate_flushed`: fixed Huffman codes over LZ77, the
   sync flush a continuing stream ends on), a compressed message is
   inflated whole under `max_message`, and the echo's replies go out
   compressed. The Autobahn suite's fuzzing client runs the conformance
   sections (1-8, 10) against `scripts/net-autobahn.d/server.fern` on the
   `net-nightly` lane (`scripts/net-autobahn`, the image pinned by
   digest), each case passing or the job failing; these gate framing,
   fragmentation, UTF-8 and close, not compression — the client offers
   permessage-deflate only in the compression sections (12 and 13), so the
   compressor is exercised only when one of those is run on demand. The
   performance and compression-volume sections (9,
   12, 13) are left out of the default set: each case passes in seconds
   in isolation, but any high-frame-count case late in the full suite
   stalls once a few hundred connections precede it, an accumulation
   tracked in issue #12136; the wstest run is bounded by a timeout and
   `net-autobahn - "13.*"` runs an excluded section by hand.
5. **Static files, compression, proxies, caching.** Four PRs, in the
   order each builds on the last.
   `std/http/cache` (landed): the response-side RFC 9111 helpers and the
   validators static files answer with — `ETag` written, read and
   compared both ways, `Validators`, `evaluate` taking RFC 9110 §13.2.2's
   preconditions in order to `Proceed`, `NotModified` or
   `PreconditionFailed`, `if_range`, `respond` producing the 304 with the
   fields §15.4.5 has it repeat, `CacheControl` read and written, and
   `with_vary`. The current time is the caller's, from `plat.now_ms()`:
   `std/time`'s `http_date_parse_at` reads all three HTTP-date forms a
   recipient must accept, RFC 850's two-digit year against it, and
   `Last-Modified` is never sent later than it. `std/headers` gained
   `get_list`, `split_list` and `unquote`, the quote-aware list parsing
   the later modules share. Gate: `tests/stdlib/http_cache_test.fern` in
   the stdtest differential.
   `std/http/static`: a directory served with `ETag` and `Last-Modified`
   through `std/http/cache`, `Range` (206, 416, `If-Range`), and
   precompressed `.br`, `.zst` and `.gz` siblings chosen by
   `Accept-Encoding` under `Vary`.
   `std/http/forwarded`: `Forwarded` (RFC 7239) and the PROXY protocol v2
   header the serve loop reads before the request.
   Compression: a gzip encoder over `std/deflate`, zstd both ways, brotli
   decode only, and `std/http/compress` negotiating `Accept-Encoding` for
   a response, off by default as every 2026 server has it.
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
