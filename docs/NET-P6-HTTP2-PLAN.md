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
stream after GOAWAY's last stream id is ignored, and an incomplete
connection preface at EOF closes the connection rather than leaving it
readable at EOF for the loop to read again every pass (RFC 9113 §6.5.3's
SETTINGS timeout is optional and not enforced).

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
   from one request to the next. The allocations-per-request reading:
   `TestSelfHostServeAllocsPerRequestH2` drives 100,000 hello requests
   over one h2c connection (`e2eharness.H2CClient`, the least client the
   gate needs) and read 99.0 to 100.0 allocations per request on x86-64
   against HTTP/1.1's zero, varying from 85 to 400 request to request
   with the blocks the connection's frames, header blocks, stream slots
   and the connection record rebuilt per frame take and give back, and
   with how the client's frames coalesce into reads; the gate holds the
   mean under a ceiling as a ratchet, lowered when the mean falls eight
   under it, since the mean moves with how the client's frames coalesce
   into reads and so with the machine (the CI runner reads about three
   under this one). Taking it towards zero is slice 8's, with the
   per-stream bump budget: the first round took it from 99 to 72.4 under
   a ceiling of 74 (the table owned through a request, in the slice
   above, had taken 107 to 99), and closing an incomplete preface at EOF,
   an arm that hands the table on, took it to 60.0 under 62 with the
   loop's splices unchanged: the table through the HTTP/2 turn is no
   longer copied,
   the state machine updating its connection in place (a step's run
   taken apart before the connection is rebuilt, so the planner moves
   the record instead of copying it), the HPACK decode threaded through
   one record instead of a record per integer and string read, a
   response's fields written straight into the encoder (`begin`, `put`,
   `finish`) with no `Field[]` built, the common status texts as
   literals, and a frame's header written in one allocation; the planner
   re-reading a stream where its connection dies, so the connection, its
   stream array and a stream were no longer copied twice a request, took
   it to 55.0; and 55.0 to 41.0 under 42, one change a commit: a
   literal string read out of the block in one allocation and the HPACK
   tables handed back as they were when a block leaves them alone, a frame
   written in one allocation and a padded payload read without a record,
   the request target handed back when already its normal form (still
   read through the parser's own byte array: reading it through a view
   cost the HTTP/1.1 hello benchmark 1.8% of its instructions, a view
   conversion being about fifty instructions in today's compiler), a
   response both windows and the frame
   size take sent at once rather than owed to a stream rebuilt around it
   and flushed, a request complete at its header block handed over from
   the stream parsed, the read buffer fed to the connection without a copy
   (`read` copies out what it keeps), the connection carrying the Date its
   responses get (`with_date`) in place of a response rebuilt around a
   header map, and the handler started from its request rather than a
   framed record built around it; and 41.0 to 21.0 under 22, with the
   instructions per request under callgrind from 14,888 to 12,592 (the
   HTTP/1.1 hello runs 10,165): a header block read one field at a time
   through `hpack.Decoding` with no `Field` box, array or result (the
   stream it builds carries "malformed" as a bit of `seen`, since a
   `Result` kept live on its `Err` arm retained the stream across the
   call and each field copied it), a response that fits written in one
   buffer from the body's own bytes (`frame.headers_room`,
   `headers_data`, `hpack.Encoder.begin_in`; the buffer parameters `own`,
   as a bare array parameter stays borrowed and its first append copied
   the buffer), `int_to_string` a constant for 0 to 99, the request's
   HEADERS frame read off the inbox without the record `frame.read`
   builds (`frame.plain_headers_at`), a frame taken off and counted in
   one update, and a block's readers keeping their run whole until they
   rebuild it; and 21.0 to 20.0 under 21, with the instructions per
   request from 12,592 to 11,995 (oha's hello, seven fields of which
   four are dynamic-table hits in the steady state, 15,060 to 13,510):
   the decoder reads a block as its own state (`hpack.Decoder.start`,
   `start_in`, `more`, `next`; the `Decoding` record and `finish` are
   gone) with an indexed field's entry looked up once, the block read
   where it lies in the inbox (`start_in`'s offsets; `__frame_taken`
   leaves the inbox on the connection and `read` cuts it at its end), a
   regular field's name and value checked by the `__scan_set` kernels
   (`http.http_field_name_lower_ok`, `http_field_value_ok`), and the run
   rebuilt around its connection in one nested construction where a
   request opens, since the planner keeps a record's unchanged fields in
   place only while no other reuse token is live across the update
   (#12795). What is left, 20 a request: the two literal strings; the
   request handed over (its record, body, event and the events array,
   the stream's record and the streams array, the `host` map and its two
   arrays, and `http_request_target`'s copy of the target); the decoder,
   copied at a block's start because the loop reading the block lies
   between taking the decoder out of the connection and putting it back,
   which keeps the planner from taking it (#12795); the encoding record,
   the response buffer and the two steps; the serve
   loop's `__H2Answered` and `Http2` box; and the connection copied out
   of the loop's table at each turn, which wants a take out of an array
   slot the language does not have: reading the slot's element and
   overwriting the slot anchors the table to the element, so the table's
   rebuild copies all of it (650 instructions and a box more a request).
   Measured on this round and not kept: the encoder carrying its block in
   place of the `Encoding` record (+51 instructions with the Go client,
   the allocation unchanged, the encoder reaching `begin_in` shared), and
   the static-table lookup comparing lengths and end bytes before strings
   (+238).
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
   Streaming both ways (landed): `h2.Config.stream_bodies` hands a request
   over at its header block (`Streamed`), its body as `StreamData` and
   `StreamEnd` events with the windows replenished by `consumed` as the
   handler takes the bytes; `respond_head`, `write`, `end` and `pending`
   write a response in parts under the windows. `std/serve` under
   `stream_bodies` gives the handler a body Stream whose pull parks on the
   connection (the loop reads the frames first, then wakes the pulls
   parked on it, each once), and drives a `BodyChunks` producer per stream
   as a task that may park, asking for more only while the pending bytes
   are under 64 KiB. `TestServeH2X86_64` streams a request body in three
   parts and reads each echoed back before sending the next, with a
   trailer at the end; the interpreter, which cannot park a task
   (`async.parks()`), hands a body over whole. `read_some` on `std/stream`
   is the reader a duplex handler needs. The wire (landed): `std/protobuf`
   reads and writes the wire format with no schema, `std/http/grpc` the
   message framing, the status trailers, `is_grpc` and `grpc-timeout`, and
   the serve loop puts a request's `grpc-timeout` on its deadline, the
   sooner of it and `handler_deadline`, answering a call past it with
   `grpc-status: 4` where an HTTP request gets 503 (`TestServeH2X86_64`
   parks a timed call on the held upstream and reads the status back
   while the upstream still holds). The lane (landed): `scripts/net-grpc-interop`
   on `net-nightly.yml` builds grpc-go's interop client at a pinned release
   and runs `empty_unary`, `large_unary`, `client_streaming`,
   `server_streaming`, `ping_pong`, `timeout_on_sleeping_server`,
   `status_code_and_message`, `special_status_message`, `custom_metadata`,
   `unimplemented_method` and `unimplemented_service` over TLS against
   `scripts/net-grpc-interop.d/server.fern`, grpc.testing.TestService in
   a few hundred lines of Fern on `std/serve`, `std/http/grpc` and
   `std/protobuf`; every case green or the job fails. Writing it found
   two loop faults: a flight parked on its own HTTP/2 connection unwatched
   the connection when it woke, and a connection draining a large
   response was watched for writes alone, so the client's WINDOW_UPDATEs
   went unread; both fixed. A producer sets the trailers a call ends
   with (landed): `http.chunks_with_trailers` carries a function asked
   once the body has ended, sent after the body beside the response's own
   trailers under both HTTP/1.1 and HTTP/2, so the interop server's
   duplex call frames its status from the messages it streamed.
   `TestServeH2Interp` and `TestServeH2X86_64` read a produced body's
   trailers back over both.
7. **The client.** `std/fetch` over HTTP/2 to an origin that offers it,
   one connection per origin, so a handler's upstream calls multiplex.
   The connection (landed): the state machine opens from the client's
   side too (`h2.client`, `conn.request`, `Answered`), frames, flow
   control, settings, resets and GOAWAY the one code for both sides, and
   `tests/stdlib/http_h2_test.fern` drives a client connection against a
   server connection in process. `std/fetch` offers `h2` beside
   `http/1.1` by ALPN and speaks HTTP/2 to an origin that chooses it: the
   request on a stream of its own, the response read whole, the
   connection kept in the pool, which now holds its rows as `Kept` values
   (the TLS session and the HTTP/2 state as they are, where the text
   table it replaced had to save and restore a session per park) until
   the server sends GOAWAY. `TestTLSClientAgainstGo` and its interpreter
   twin fetch twice from Go's HTTP/2 server over one connection and read
   the protocol back. The multiplexing (landed): under the serve loop the
   handlers' requests to one origin go over one connection at once. The
   loop's pool carries a `fetch.Shared` table; a handler's request is a
   stream opened on the origin's driven connection and a `Mail` slot its
   task parks on (a wait-set pair with `fetch.MAIL_INTEREST`, which the
   loop keeps off its reactor and answers itself), the loop reads the
   driven connections' frames at its own events, puts each stream's
   answer in its slot and wakes the task waiting on it at the end of the
   pass, the first task to an origin opens the connection while the
   others wait for it, and a connection idle past the pool's bound or sent
   GOAWAY is closed. `TestServeH2X86_64` parks six handlers on one
   upstream HTTP/2 connection and reads every answer back after the
   release.
8. **Exit.** Per-stream bump bytes bounded and recorded on the perf lane;
   `internal/e2eselfhost` twins for the connection tests; #9860 closed.
   The bound (landed): `TestSelfHostHeldConnectionsHeapBoundX86_64`'s
   fourth shape parks 64 handlers, then 64 more, on the streams of one
   h2c connection (`e2eharness.H2CClient` opening streams and awaiting
   them apart) and holds the second batch's growth under 12 KiB per
   stream; it reads about 8.0 KiB, the suspended handler's 7.0 KiB without
   a connection plus about 2.5 KiB the state machine keeps per open
   stream, and `scripts/net-bench` records it nightly as
   `net/fern/held.bytes_per_stream`. The twins: the connection tests
   (`http_hpack`, `http_h2_frame`, `http_h2`, `http_grpc`, `protobuf`)
   run through the self-host compiler in `TestSelfHostStdTestE2E`, and
   every `…X86_64` serve test builds its server with `bin/fern`, which is
   the self-host compiler.
