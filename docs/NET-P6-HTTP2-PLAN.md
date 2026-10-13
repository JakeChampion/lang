# Net P6: HTTP/2 — the plan

How #9860 is built, in the order it lands. One PR per slice. Each slice
names its gate; a module runs its suite through the interpreter and the
self-host compiler on x86-64, arm64 and wasm, and what touches the serve
loop runs against a real server from Go and, nightly, against h2spec and
the gRPC interop suite.

## Where it sits

HTTP/2 is a second framing of the same messages. `HttpRequest`,
`HttpResponse`, `Body`, the trailers and the handler signature do not
change: a handler written for HTTP/1.1 serves HTTP/2 connections, and
`std/http/router` and every middleware run unchanged over it. What P6 adds
is the connection: a state machine per connection that multiplexes
streams onto one socket, with HPACK for the header blocks, flow control
for the bodies, and the limits that keep one peer from spending the
server's memory.

Modules, each importable standalone and each under the 4,000-line cap:

- `std/http/hpack` — RFC 7541 (slice 1, landed).
- `std/http/h2/frame` — the nine frame types of RFC 9113 §6 read and
  written over bytes, with SETTINGS_MAX_FRAME_SIZE enforced on both
  sides, unknown types skipped, and the per-connection header budget that
  closes a CONTINUATION flood.
- `std/http/h2` — the connection: the preface, settings, streams and
  their states (§5.1), flow-control windows at both levels (§5.2), PING,
  GOAWAY, RST_STREAM, the request assembled from a stream's HEADERS and
  DATA and the response written back, priorities read and ignored (§5.3
  as RFC 9113 leaves it). Sans-IO: bytes in, bytes out, no socket, so it
  runs under `std/sim` like the TLS handshake does.
- `std/serve` — a connection whose first bytes are the client preface
  (prior-knowledge h2c), or whose TLS handshake chose `h2` by ALPN, is
  handed to the HTTP/2 state machine; everything else stays HTTP/1.1. The
  `Upgrade: h2c` path is not implemented: RFC 9113 deprecates it.
- `std/fetch` — the client side later: one connection per origin carrying
  many requests, so `plat.http` from a handler multiplexes its upstream
  calls.

## The four attack classes, designed in

Each is a bound the connection holds from its first PR, with a test that
sends the attack and reads the connection refusing it:

1. **Rapid Reset (CVE-2023-44487).** A handler runs for a stream only
   while the stream counts against SETTINGS_MAX_CONCURRENT_STREAMS, and a
   stream the client resets stops counting only once its handler has
   stopped: resets do not free the slot early. New streams past the limit
   are refused with REFUSED_STREAM, and a connection that opens streams
   faster than they can be refused is closed with ENHANCE_YOUR_CALM.
2. **CONTINUATION flood (CVE-2023-45288).** Header bytes per stream are
   capped by SETTINGS_MAX_HEADER_LIST_SIZE as they arrive, Huffman-decoded
   size included, and a connection sends at most a fixed excess of header
   frames past a block it is going to refuse before the connection, not
   the stream, is closed.
3. **MadeYouReset (CVE-2025-8671).** The reset budget counts every reset,
   the server's own included: a client whose frames make the server send
   RST_STREAM again and again is closed at the same budget as one that
   sends RST_STREAM itself.
4. **HTTP/2 Bomb (CVE-2026-49975).** HPACK's decoder counts each field as
   it decodes and refuses more than a cap of fields, so a block that
   would expand past the list size stops before the allocation; the
   dynamic table is held to the size the local settings announce.

And the small ones: an empty DATA frame counts against a per-connection
budget, SETTINGS_MAX_FRAME_SIZE under 16,384 or over 16,777,215 is a
protocol error, an unknown frame type is skipped whole, a frame on a
stream after GOAWAY's last stream id is ignored, and a SETTINGS frame
without ACK within the timeout closes the connection.

## Slices

1. **HPACK (landed).** `std/http/hpack`: a `Decoder` and an `Encoder`
   as values, the static table, a dynamic table with eviction, integers
   with any prefix, Huffman coding from the canonical table (decoding by
   the first code of each length, so a symbol is an offset), size updates
   taken at a block's start and only up to the settings, and the list,
   field and table bounds. `tests/stdlib/http_hpack_test.fern` runs
   RFC 7541's Appendix C both ways, byte for byte, with the table sizes
   and evictions it shows, and each refusal; the interpreter and the
   self-host compiler both run it.
2. **Frames.** `std/http/h2/frame`: each frame type as a value read from
   and written to bytes, padding, flags, the frame size settings, and
   the header budget. Gate: the suite, with every frame of RFC 9113's
   examples and every malformed shape §6 names.
3. **The connection.** `std/http/h2`: the state machine over frames and
   HPACK, sans-IO, with streams, both flow-control windows, settings
   exchange, PING, GOAWAY and the four bounds above. Gate: the suite
   drives whole connections from bytes, including each attack.
4. **Serving.** `std/serve` hands a prior-knowledge h2c connection and an
   ALPN `h2` TLS connection to the state machine; handlers run per stream
   under the concurrency limit, suspended handlers (P3) park per stream.
   Gate: Go e2e tests with a real HTTP/2 client (Go's net/http) against
   the Fern server, h2c and over TLS; the handler census and the
   allocations-per-request gate for the HTTP/2 path.
5. **h2spec nightly.** `scripts/net-h2spec` on `net-nightly.yml`: h2spec
   strict against the server, every case green or the job fails.
6. **gRPC interop.** Trailers both ways, `grpc-timeout` onto the request
   deadline, and `scripts/net-grpc-interop` running the interop suite's
   `empty_unary`, `large_unary`, `client_streaming`, `server_streaming`,
   `ping_pong` and `timeout_on_sleeping_server` against a Fern server with
   the suite's certificates.
7. **The client.** `std/fetch` over HTTP/2 to an origin that offers it,
   one connection per origin, so a handler's upstream calls multiplex.
8. **Exit.** Per-stream bump bytes bounded and recorded on the perf lane;
   `internal/e2eselfhost` twins for the connection tests; #9860 closed.
