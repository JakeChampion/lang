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
`std/http/compress`, `std/http/forwarded` (the PROXY protocol and
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
   with no context takeover on the server's side: `Options.compression`
   takes the client's offer, the server compressing each message alone
   and the client keeping its window, which the reader decodes against
   (`deflate.inflate_after`, the last 32 KiB of the client's messages);
   `std/deflate` gained the encoder it needs
   (`deflate`, `deflate_flushed`: fixed Huffman codes over LZ77, the
   sync flush a continuing stream ends on), a compressed message is
   inflated whole under `max_message`, and the echo's replies go out
   compressed. The Autobahn suite's fuzzing client runs against
   `scripts/net-autobahn.d/server.fern` on the
   `net-nightly` lane (`scripts/net-autobahn`, the image pinned by
   digest), each case passing or the job failing: every section, the
   performance (9) and compression (12, 13) ones included, each group of
   them in a client process of its own (the suite's PyPy client keeps the
   native memory of every zlib object it makes, and run whole it grew past
   10 GB and slowed to what read as a stall, #12136).
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
   `std/http/static` (landed): a directory served with `ETag` and
   `Last-Modified` through `std/http/cache`, `Range` (206, 416,
   `If-Range`), and precompressed `.br`, `.zst` and `.gz` siblings chosen
   by `Accept-Encoding` under `Vary` (`HeaderMap.preferred_coding`, which
   compression shares). A handler still reads no file: the platform
   gained `stat`, and the body is a `BodyFileRanges`, ranges of a file with
   the seams between them, which the serve loop sends by `sendfile(2)`
   after a seek, so a range, or a `multipart/byteranges` of several, costs
   what a whole file does.
   The mock and the sim answer `stat` from files a test sets and read a
   file body back with `materialize`. Gate:
   `tests/stdlib/http_static_test.fern` in the stdtest differential;
   `TestStaticServeInterp` and `TestStaticServeX86_64` against the real
   loop; `TestStaticPreconditionsMatchGo`, every combination of the
   precondition fields, `Range` and `If-Range` against Go's
   `http.ServeContent`.
   `std/http/forwarded` (landed): `Forwarded` (RFC 7239) read and written,
   and `client`, the walk from the connection's peer past the proxies a
   deployment trusts (`net.Prefix` blocks), `X-Forwarded-*` on request. The
   peer reaches the handler as `plat.peer()`: std/serve reads each
   connection's peer at accept and hands that connection's handlers the host
   carrying it, which is the per-request platform slice 6's deadline and
   cancel signal ride on too. Gate: `tests/stdlib/http_forwarded_test.fern`
   in the stdtest differential; `TestForwardedServeInterp` and
   `TestForwardedServeX86_64` against the real loop.
   PROXY protocol v1 and v2 (landed): the header a load balancer writes
   before the request, `forwarded.proxy_header`, read by the serve loop
   when `Config.proxy_protocol` asks for it, before TLS, its source address
   becoming the connection's peer. Gate: the parser cases in the same
   suite; `TestProxyProtocolServeInterp` and `TestProxyProtocolServeX86_64`
   send both versions, a split header, LOCAL, none, and TLS behind it.
   Compression, in three PRs. The first (landed): `std/deflate`'s encoder
   writes dynamic-Huffman blocks (stored or fixed when shorter) with
   lazy matching, `gzip` frames it, and `std/http/compress` negotiates
   `Accept-Encoding` for a response held whole, off unless a handler asks,
   as every 2026 server has it. Gate: `tests/stdlib/http_compress_test.fern`
   and the deflate suite in the stdtest differential;
   `TestCompressServeInterp` and `TestCompressServeX86_64` read the loop's
   gzip with Go's decoder. The second (landed): `std/zstd` both ways,
   offered ahead of gzip. Gate: `tests/stdlib/zstd_test.fern` decoding
   the reference encoder's frames in the stdtest differential, and
   `TestZstdEncodeAgainstGo`, where Go's own zstd decoder (the one
   `debug/elf` uses for compressed sections) reads the encoder's frames.
   The third (landed): `std/brotli` decodes, with the static dictionary
   generated by `cmd/brotlidictgen`, and std/fetch asks for and undoes
   `gzip`, `zstd` and `br`. Gate: `tests/stdlib/brotli_test.fern` decoding
   the reference encoder's streams and a hand-built one in the stdtest
   differential, and the fetch suites' zstd and brotli answers.
6. **Host seams as `Platform` methods.** The request deadline and cancel
   signal, background work that outlives the response (`wait_until`,
   bounded like Workers' 30 s), outbound HTTP gated by an allow-list.
   The allow-list (landed): `fetch.allow` names hosts, `host:port`s,
   `*.domain`s and bracketed IPv6 addresses, refusing an entry that names
   none when it is built, and `Policy.allow` refuses any other host as
   `Blocked` on every redirect hop before resolving it, wasi-http's route
   included. A platform carries one (`Host.with_allow`,
   `SimPlatform.with_allow`, `serve.Config { http_allow }`). Gate: the
   allow-list cases in `tests/stdlib/sim_fetch_test.fern` and
   `tests/stdlib/sim_platform_test.fern`; `TestHTTPAllowServeInterp` and
   `TestHTTPAllowServeX86_64` through the real loop.
   The deadline and cancel signal (landed): `Config.handler_deadline`
   bounds a handler from its start, the loop cancelling one parked when it
   passes and answering 503 in place of any later response, as Go's
   `http.TimeoutHandler` does. The handler reads it as `plat.deadline()`
   and asks `plat.cancelled()`, true once the client reset the connection
   (the task's cancel mark, `async.task_cancelled()`, over a new
   `__task_cancelled` primitive) or the deadline passed, so work between
   waits can stop. `SimPlatform.with_deadline` puts it on the sim's
   clock; the mock cans both. Gate: the deadline cases in
   `tests/stdlib/sim_platform_test.fern` and
   `tests/stdlib/mock_platform_canned_test.fern`;
   `TestHandlerDeadlineServeInterp` and `TestHandlerDeadlineServeX86_64`
   through the real loop, the latter with a client that resets.
   Background work (landed), on the response rather than the platform: a
   platform has nowhere to hold a closure for the loop, since a `Cell` may
   not hold one (E057, the cycle rule), so the work rides out of the
   handler in the value it returns, as a chunk producer does.
   `HttpResponse.background` holds it and `with_background` adds to it in
   order. The loop starts it as a task once the response is written,
   holding one that parks as a flight on no connection until
   `Config.background_deadline` (30 s) cancels it, and a shutdown's drain
   waits for it. wasi-http runs it once the body is finished. Gate: the
   ordering case in `tests/stdlib/http_respond_test.fern`;
   `TestBackgroundServeInterp` and `TestBackgroundServeX86_64` through the
   real loop. On the way: the typed lowering refused a lambda capturing a
   binding matched out of a built-in record's field (`BodyChunks(p)` from
   `resp.body`), since `module_env` left the built-in structs out when
   lowering annotated a module before injecting them; the
   `closure_captures_builtin_record_binding` conformance case pins it.
7. **Observability.** The OTel HTTP semantic conventions, `traceparent`
   propagation through `plat.http`, a JSON-lines access log with the OTel
   log data model's fields, health and readiness helpers; `std/log`'s
   `Logger` gains a sink over `plat.log`; metrics as per-worker counters and
   fixed-bucket histograms in a static registry, snapshotted to the
   supervisor over its pipe once a second and served at `/metrics` in
   Prometheus text format so a scrape never touches a worker.
   Trace Context (landed): `std/http/trace` reads, writes and continues
   `traceparent` and `tracestate` by W3C Trace Context (Level 2's random
   flag included); `serve.Config { trace }` opens a span per request,
   read as `plat.trace()` and carried upstream by `plat.http` as a child.
   Opt-in, as Go's `net/http` and axum ship no tracing by default. Gate:
   `tests/stdlib/http_trace_test.fern` and the propagation case in
   `tests/stdlib/sim_platform_test.fern`; `TestTraceServeInterp` and
   `TestTraceServeX86_64` through the real loop.
   Access log (landed): `std/http/accesslog`'s `logged` middleware writes
   one JSON line per request through `plat.log` in the OTel log data
   model's OTLP/JSON spelling, its attributes under the HTTP semantic
   conventions' names and its span from `plat.trace()`. A middleware rather
   than a loop hook, so a wasi-http handler logs the same way. Gate:
   `tests/stdlib/http_accesslog_test.fern`; `TestAccessLogServeInterp` and
   `TestAccessLogServeX86_64`.
   The logger's sink (landed): `std/log`'s `Logger` carries the function
   each record goes through, stderr unless `logger.to(sink)` names
   another, so a handler builds one over `plat.log` and a test over a
   collector; `emit` hands the sink the line without its newline, as
   `plat.log` and `eprint` take one. A handler that calls `log_info` and
   its siblings is already told to use `plat.log`: the ambient rule
   reports the `eprint` under them with the chain through `log_info`. And
   `eprint`, so `plat.log` on the host platform, writes a line and its
   newline in one `writev(2)` on the native targets, so workers sharing a
   stderr never land a write inside each other's line of up to `PIPE_BUF`
   bytes. On wasm the two stay separate writes: wasmtime's preview1
   `fd_write` writes only the first non-empty iovec, and a wasm program
   has no fork and no shared pipe for the guarantee to matter.
   Gate: the sink cases in `tests/stdlib/log_test.fern` in the stdtest
   differential; `TestX86_64EprintWholeLines`, `TestArm64EprintWholeLines`
   and `TestArm64DarwinEprintWholeLines`, four forked workers logging a
   thousand lines each to one pipe, every line read back whole.
   Metrics (landed): `std/metrics` has counters, gauges and fixed-bucket
   histograms whose numbers live in cells, so a handler's copy and the
   registry's count the same thing, their Prometheus text exposition
   (format 0.0.4), `merge` summing several expositions, and an `Exporter`
   answering scrapes on a port of its own from the caller's reactor. The
   serve loop records `http_server_request_duration_seconds` by method
   and status (OpenTelemetry's buckets, the request timed from its header
   block's arrival, the clock the body deadline already keeps),
   connections accepted and open, into a
   `ServerMetrics` kept on its connection table, and renders it with the
   program's `Config.metrics` registry. Under `supervise` each worker
   ships that text, ended by the `# EOF` line, down a non-blocking pipe
   to the supervisor once a second (a snapshot the pipe has no room for
   is dropped, never a stall), and the supervisor sums the workers'
   latest snapshots and answers `/metrics`, `/healthz` and `/readyz` on
   `Config.metrics_port`, so a scrape never touches a worker; `run`, and
   `supervise` where there is no fork, answer them from the loop. There
   is no per-worker state to share, so a dead worker's counts leave with
   it: the reset a scraper's rate functions expect of a restart. Gate:
   `tests/stdlib/metrics_test.fern` in the stdtest differential;
   `TestMetricsServeInterp` (one process) and
   `TestMetricsSupervisedServeX86_64` (two workers through the
   supervisor) read the counts back off the metrics port.
8. **The developer loop.** In-memory serving through the full router and
   middleware stack on the simulated `Platform`; recording and replay of
   upstream traffic; a rate-driven load tool in `tools/` with latency
   correction; httplint's response checks on the `net-nightly` lane.
   In-memory serving and record/replay (landed): `sim_fetch.Net.serve(addr,
   port, handler)` answers the client's requests from a handler with the
   serve loop's parsing and framing and no socket, so a router with its
   middleware runs under `plat.http` on a `SimPlatform`
   (`tests/stdlib/sim_platform_test.fern`, "serves a router in memory");
   `sim_fetch.taped` / `send_taped` record a client's exchanges as JSON
   lines and `Net.replay(cassette)` answers them in order, `https`
   replayed as plain `http` since the simulated network carries no TLS
   (`tests/stdlib/sim_fetch_test.fern`, the cassette cases). httplint
   (landed): `scripts/net-httplint` on `net-nightly.yml` fetches fourteen
   responses from a router program (`scripts/net-httplint.d/server.fern`:
   text, JSON, HEAD, 405, 302, problem, upload, compressed, static file,
   304, 206, `Cache-Control`) and fails on a note httplint rates BAD; the
   first run found the status line of a 206 and a 412 going out as
   "Status", so `http_status_text` now names every RFC 9110 code. The
   load tool (landed): `tools/fernload/fernload.fern` sends at a fixed
   rate over kept connections, open-loop, and measures each request's
   latency from the moment the rate said it was due, so a stalled server
   shows the queueing a real client would see (wrk2's correction for
   coordinated omission); the raw latency from the send is reported beside
   it, as `metric<TAB>value` rows `scripts/perf-history` can record.
   `TestFernloadX86_64` and `TestFernloadInterp` run it against a
   `std/serve` hello and check every scheduled request was sent and
   answered. Writing it found the interpreter's `connect_start` answering a
   blocking socket where the native one is non-blocking; it is
   non-blocking on both now.
9. **The exit criteria.** The tutorial's routed JSON API with logging
   middleware and graceful shutdown under 40 lines, its deterministic
   simulation with fault injection under `examples/tests/` under 20, both
   compiled in the examples lane; Autobahn and httplint green nightly;
   `internal/e2eselfhost` twins for the router, middleware, multipart and
   WebSocket tests.
   The two programs (landed): `examples/api.fern`, 38 lines, a router
   generic over the platform with the access log as middleware, a lookup
   at an upstream directory through `plat.http`, and
   `serve.supervise_shutdown` reporting why it stopped; and
   `examples/tests/api_sim.fern`, 19 lines, the same routes on a
   `SimPlatform` whose directory resets twice, so the first lookup is a 502
   past the client's one retry and the next a 200. The examples lanes
   (`examples.yml`, `macos.yml`, `make examples`) compile
   `examples/tests/*.fern` beside `examples/*.fern`; a `norun` row in
   `.github/examples-expected-exits.txt` marks the server as built and not
   run. The tutorial (`site/src/content/docs/tutorial/http-server.mdx`)
   carries both. Writing them found the self-host refusing a generic named
   as a value inside another generic (#12313, fixed on main first) and
   not instantiating a generic call from a method argument's expected type
   (#12314, open). The router, middleware, multipart and WebSocket gates
   already run through the self-host compiler: every `…X86_64` test in
   `internal/testing/e2e` builds its server with `bin/fern -target
   x86-64-linux`, which is the self-host compiler, and
   `TestSelfHostServeWebSocket` sits in `internal/testing/e2ecompiler`.
