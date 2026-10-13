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
- `std/http/h2/frame` — the frame types of RFC 9113 §6 read and written
  over bytes (slice 2, landed), with the frame size enforced on reading,
  unknown types kept for skipping, and every malformed shape §6 names an
  error with its code and stream.
- `std/http/h2` — the connection (slice 3, landed): the preface,
  settings, streams and their states (§5.1), flow-control windows at both
  levels (§5.2), PING, GOAWAY, RST_STREAM, the request assembled from a
  stream's HEADERS, CONTINUATION and DATA and the response written back,
  priorities read and ignored (§5.3 as RFC 9113 leaves it). Sans-IO:
  bytes in, bytes out, no socket, so it runs under `std/sim` like the TLS
  handshake does.
- `std/serve` — with `Config.with_h2()` (slice 4, landed), a connection
  whose first bytes are the client preface (prior-knowledge h2c), or whose
  TLS handshake chose `h2` by ALPN, is handed to the HTTP/2 state machine;
  everything else stays HTTP/1.1. The `Upgrade: h2c` path is not
  implemented: RFC 9113 deprecates it.
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
2. **Frames (landed).** `std/http/h2/frame`: each frame type as a value
   read from and written to bytes, padding and priority taken off, the
   flags, each setting checked, and a frame read in pieces as it
   arrives. `tests/stdlib/http_h2_frame_test.fern` writes and reads back
   every type byte for byte and answers every malformed shape §6 names;
   the per-connection header budget against a CONTINUATION flood needs
   the connection's state and is slice 3's.
3. **The connection (landed).** `std/http/h2`: the state machine over
   frames and HPACK, sans-IO, with streams, both flow-control windows,
   settings exchange, PING, GOAWAY and the four bounds above. A request
   is handed over whole at its END_STREAM, with its trailers; a response
   is split by the peer's frame size and both windows, the rest held for
   WINDOW_UPDATE, and its trailers go last. `tests/stdlib/http_h2_test.fern`
   drives whole connections from bytes as a client does: each attack class
   refused, each malformed request reset and each malformed connection
   closed, flow control at both levels, and the control frames.
4. **Serving (landed, most of it).** `std/serve` under `with_h2()` hands a
   prior-knowledge h2c connection and an ALPN `h2` TLS connection to the
   state machine: the loop's read and write paths carry the connection's
   bytes, pending writes and all, and each stream's handler starts as an
   HTTP/1.1 request's does and is counted in the metrics. A handler that
   suspends (P3) parks as a flight on the connection and its stream: the
   loop keeps reading the connection and answers its other streams, and
   the flight's answer goes out on its stream when it wakes, counted from
   the clock the flight kept. The HTTP/2 connection lives in the table's
   framing column beside HTTP/1.1's pulled body, so a held connection
   stays at 992 bytes under the 1 KiB bound. `TestServeH2Interp` and
   `TestServeH2X86_64` drive the server with curl over h2c (two GETs on
   one connection, a POST echoed), with Go's client over TLS (h2 by ALPN,
   eight requests at once, a 50 KB upload echoed), and with four handlers
   parked on an upstream while a fifth request on the same connection is
   answered, the four answering once the upstream does. `run_with`'s
   stateful loop serves HTTP/2 the same way, each stream's request run
   with the state in order and nothing parked, since the state threads
   from one request to the next. Still open here: the
   allocations-per-request reading for the HTTP/2 path.
5. **h2spec nightly (landed).** `scripts/net-h2spec` on `net-nightly.yml`:
   h2spec 2.6.0 strict over h2c against `scripts/net-h2spec.d/server.fern`,
   146 cases, every one green or the job fails, the counts recorded under
   `refs/notes/perf`. The one case not run is §3.5's invalid connection
   preface: a port that serves HTTP/1.1 and h2c together answers bytes that
   are not the preface as HTTP/1.1, where h2spec expects GOAWAY; under TLS
   the ALPN choice decides instead, so a bad preface there is GOAWAY. The
   run is h2c because h2spec's TLS client does not offer TLS 1.3. The
   cases found two gaps in slice 3, fixed with their tests: RST_STREAM and
   WINDOW_UPDATE on a stream never opened are a connection error, and a
   stream that depends on itself in HEADERS or PRIORITY is reset.
6. **gRPC interop.** Trailers both ways, `grpc-timeout` onto the request
   deadline, and `scripts/net-grpc-interop` running the interop suite's
   `empty_unary`, `large_unary`, `client_streaming`, `server_streaming`,
   `ping_pong` and `timeout_on_sleeping_server` against a Fern server with
   the suite's certificates.
7. **The client.** `std/fetch` over HTTP/2 to an origin that offers it,
   one connection per origin, so a handler's upstream calls multiplex.
8. **Exit.** Per-stream bump bytes bounded and recorded on the perf lane;
   `internal/e2eselfhost` twins for the connection tests; #9860 closed.
