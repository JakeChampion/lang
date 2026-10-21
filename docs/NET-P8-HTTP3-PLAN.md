# Net P8: QUIC and HTTP/3 — the plan

Status: [plan] in progress; slices 1 to 7 have landed, and 4b, 8 (interop nightly), 9 (the client), the rest of 10 (speed) and 0-RTT are open.

How QUIC (RFC 9000, 9001, 9002) and HTTP/3 (RFC 9114, with QPACK, RFC 9204)
are built into the standard library, in the order they land. One PR per
slice. Each slice names its gate: a module's suite runs through the
interpreter and the self-host compiler on x86-64 and arm64, and what touches
the serve loop runs against a real server and, nightly, against other
implementations.

HTTP/3 lives in the standard library rather than in a package because the
server and the client already do (`std/serve`, `std/fetch`), and a handler
should not change when the transport does. Nothing here is closed to a
package: the QUIC connection is a value with no IO, so another HTTP/3, a
WebTransport or a DNS-over-QUIC can drive the same `std/quic/conn`.

## Where it sits

The handler, `HttpRequest` and `HttpResponse` do not change. A request that
arrives over HTTP/3 is the same value as one over HTTP/1.1 or HTTP/2, and a
response is sent the same way, streamed bodies and trailers included.

`std/tls/handshake` already runs under QUIC (`docs/NET-P4-TLS-PLAN.md`):
handshake bytes by encryption level in and out, each traffic secret reported
as it is set, transport parameters carried as an opaque extension. Packet
protection is AES-GCM through the `__aes_*` kernels and ChaCha20-Poly1305;
header protection is one AES block or one ChaCha20 block from the same
primitives.

The modules, each importable on its own and sans-IO, so each runs under
`std/sim`:

- `std/quic/frames` — variable-length integers, every frame of §19, the
  transport error codes, which frames each packet type may carry. Slice 1,
  landed.
- `std/quic/packet` — long and short headers, packet number encoding,
  Initial keys, packet and header protection, Version Negotiation, Retry
  and its integrity tag, key update. Slice 1, landed.
- `std/quic/params` — transport parameters, with §18.2's checks. Slice 1,
  landed.
- `std/quic/recovery` — RTT estimation, loss detection, PTO, NewReno
  congestion control and persistent congestion (RFC 9002). Slices 2–3,
  landed together. Pacing is slice 10's.
- `std/quic/conn` — the connection: the handshake, three packet number
  spaces and their ACKs, CRYPTO reassembly, idle timeout, closing and
  draining, the anti-amplification limit, following a Retry and Version
  Negotiation, and recognising a stateless reset landed in slices 2–3.
  Key updates use cached current, next and previous receive generations,
  with handshake/acknowledgement gates and bounded old-key retention.
  Connection IDs beyond the first and path validation remain slice 4b;
  issuing a Retry is slice 7's.
- `std/quic/streams` — streams: each one's send and receive halves,
  reassembly (CRYPTO's too), the limits on how many each side opens, flow
  control per stream and for the connection, RESET_STREAM and
  STOP_SENDING. Slice 4, landed.
- `std/http/qpack` — the static and dynamic tables, the encoder and
  decoder streams, Huffman shared with `std/http/hpack` through
  `std/http/huffman`. The encoder uses the static table and literals only,
  so a peer never blocks on it. Slice 5.
- `std/http/h3` — frames, the control and QPACK streams, SETTINGS and
  GOAWAY, request and response streams with interim responses and
  trailers, and §4.1.2's message checks. It runs over stream bytes, not
  over `std/quic/conn`: the caller hands it each stream's bytes and resets
  and takes back writes, resets and events, so it is tested in memory and
  any QUIC implementation can carry it. Slice 6.
- `std/http/h3q` — HTTP/3 over `std/quic/conn`: one connection and its
  session, speaking `HttpRequest` and `HttpResponse` as `std/http/h2`'s
  `Conn` does, with `transport` giving QUIC the settings HTTP/3 needs.
  Slice 7a.

## The attacks, designed in

Each is a bound with a test that crosses it.

1. **Amplification.** Before an address is validated a server sends at most
   three times what it received (§8.1). An Initial under 1200 bytes is
   dropped. Under load, Retry with an address-validation token (§8.1.2)
   costs the attacker a round trip.
2. **State exhaustion.** CRYPTO data out of order is buffered only up to a
   cap (CRYPTO_BUFFER_EXCEEDED); stream data only up to the flow-control
   window it was granted; streams only up to the limits advertised; an ACK
   frame's ranges are capped; QPACK's dynamic table, its blocked streams and
   a field section's size are capped.
3. **Floods of cheap frames.** PING, empty STREAM, NEW_CONNECTION_ID and
   PATH_CHALLENGE count against a per-connection budget, as HTTP/2's reset
   and empty-DATA budgets do.
4. **Optimistic ACKs.** A sender skips packet numbers now and then; an ACK
   for one it never sent is a PROTOCOL_VIOLATION (§21.4).
5. **Stateless reset and connection IDs.** Reset tokens come from a static
   key, compared in constant time; a peer is held to the
   active_connection_id_limit it was given (CONNECTION_ID_LIMIT_ERROR).

## Slices

1. **The wire.** `std/quic/frames`, `std/quic/packet`, `std/quic/params`.
   Gate: `tests/stdlib/quic_frames_test.fern`,
   `tests/stdlib/quic_packet_test.fern` (RFC 9001 Appendix A's packets, byte
   for byte), `tests/stdlib/quic_params_test.fern`.
2. **The handshake.** `std/quic/conn` takes a client and a server through
   Initial, Handshake and 1-RTT: CRYPTO frames in and out of
   `std/tls/handshake`, ACKs per space, Initial and Handshake keys
   discarded, HANDSHAKE_DONE, transport parameters authenticated (§7.3).
   Gate: a client and a server `Conn` handshaking in memory
   (`tests/stdlib/quic_conn_test.fern`), and `scripts/net-quic-interop`
   nightly: the Fern client against aioquic's server plainly, through a
   Retry and on ChaCha20-Poly1305 alone, and aioquic's client against the
   Fern server. Landed with slice 3.
3. **Recovery.** `std/quic/recovery` and its use in `std/quic/conn`.
   Gate: RFC 9002's algorithms driven in virtual time
   (`tests/stdlib/quic_recovery_test.fern`) — loss by packet and by time
   threshold, PTO and its backoff, the congestion window across slow
   start, recovery and persistent congestion — and a lost datagram and a
   lost flight recovered between two `Conn`s. Landed with slice 2.

   aioquic offers no X25519MLKEM768 and does not answer a HelloRetryRequest
   on the server side, so the QUIC client sends key shares for the first
   two groups it supports (`ClientConfig.shares` in `std/tls/handshake`);
   a TCP client still sends one.
4. **Streams.** Bidirectional and unidirectional streams, flow control,
   RESET_STREAM and STOP_SENDING, stream limits. Gate:
   `tests/stdlib/quic_streams_test.fern`, each rule frame by frame, and two
   `Conn`s echoing a stream through small windows, then through scripted
   loss and reordering (`tests/stdlib/quic_conn_test.fern`); nightly,
   `scripts/net-quic-interop` echoes 300 KB through aioquic each way.
   Landed.

   4b. **Connection IDs, key update, path validation.** NEW_CONNECTION_ID
   and RETIRE_CONNECTION_ID with active_connection_id_limit, a stateless
   reset token per connection ID, the peer's key update answered and one
   started, PATH_CHALLENGE on a new path. Gate: two `Conn`s rotating
   connection IDs and keys mid-stream.

   The key-update portion is implemented independently of connection-ID
   rotation and path validation, which remain follow-ups. `Conn.update_keys(now)`
   requires handshake confirmation and acknowledgement in the current phase.
   A peer's authenticated update advances sending keys before its ACK; old
   receive keys allow reordering for at most three PTOs after the first new-phase
   packet. `deadline()` includes key-generation and expiry timers, so sans-IO
   callers must continue to call `tick(now)` when due. Keys are prepared on
   installation or timer expiry, not derived in response to forged phase bits.
   Encryption limits trigger a safe rotation or `AEAD_LIMIT_REACHED`; failed
   authentications are counted across generations, with the RFC 9001 limits
   for AES-GCM and ChaCha20-Poly1305. The connection suite covers these guards,
   repeated rotations, reordered/expired packets, and unchanged old aliases.
5. **QPACK.** Gate: `tests/stdlib/http_qpack_test.fern`, with RFC 9204
   Appendix B's examples.
6. **HTTP/3.** `std/http/h3`. Gate: `tests/stdlib/http_h3_test.fern`, a
   client and a server session joined in memory, bytes delivered whole or
   one at a time.
7a. **HTTP/3 over QUIC.** `std/http/h3q`. Gate:
   `tests/stdlib/http_h3q_test.fern` in memory under loss and reordering,
   and `scripts/net-quic-interop` against aioquic's HTTP/3 each way.
7. **Serving.** `serve.Config.with_h3()`: a UDP socket beside the TCP
   listener, connections routed by connection ID, QUIC's timers in the
   loop's wait, handlers parked per stream as for HTTP/2 (keyed by the
   connection's serial, since one socket carries every connection), chunked
   bodies produced as tasks under a cap on what the client has not
   acknowledged, GOAWAY on shutdown, and Alt-Svc on the TCP responses.
   `run_with`'s stateful loop serves HTTP/3 too, its state threaded through
   each request in turn. Gate: `TestServeH3Interp` and `TestServeH3X86_64`,
   a std/http/h3q client against the served program. Each worker binds its
   own socket with `udp_bind_with` (#12480), SO_REUSEPORT and native
   dual-stack, including when the TCP listener is inherited. Gate:
   `TestServeH3SocketX86_64`, two requests on each connection over IPv4
   and IPv6, and independent connections reaching both supervised workers.
   `TestServeH3PeerLimitX86_64` pins per-IP admission independently of the
   peer's UDP port. File bodies use HTTP/2's existing part producer from
   #12484: 64 KiB reads with file-identity checks, under the same HTTP/3
   unacknowledged-byte cap as other chunk producers. Gate:
   `TestServeH3FileInterp`, `TestServeH3FileX86_64`, and
   `TestServeH3FileArm64`: whole binary files, ranges and seams, HEAD,
   missing/invalid/empty files, bodyless statuses and trailers. The x86-64
   transfer exceeds three times the cap and compares every byte by SHA-256.
   ARM64 under QEMU is correctness coverage, not native performance.
8. **Interop nightly.** h3spec, and more HTTP/3 peers beside aioquic
   (`scripts/net-quic-interop` has carried HTTP/3 both ways since 7a).
   Gate: the nightly job.
9. **Client.** `std/fetch` speaks HTTP/3 to an origin that offers it by
   Alt-Svc, and `std/sim_fetch` scripts datagrams. Gate: fetch against
   aioquic's server, and the simulated network.
10. **Speed.** Measured first (`docs/benchmarks/net-h3-2026-10-16.md`):
    632 allocations per hello request in the serve loop against HTTP/1.1's
    zero and HTTP/2's 60, and one datagram sent per response. The gate is
    `TestSelfHostServeAllocsPerRequestH3`, a ratchet on the mean like
    HTTP/2's; throughput is recorded, not gated. In the order the
    allocation sites rank:

    - **The stream table (landed).** `std/quic/streams` rebuilt every
      stream's sending half and the whole list on every packet. A
      transmit now leaves a stream with nothing to send untouched and
      writes back only the streams it changed; the loop flushes a
      connection once per burst of datagrams and once after their
      requests are answered, so acknowledgements and responses share
      datagrams. 632 to 421 allocations per request; with 32 requests in
      flight on one connection, 34,698 to 114,057 responses per second on
      one worker.
    - **The wire (landed).** `std/quic/frames` read each varint through a
      cursor record and a result, and `std/quic/packet` each header field
      through a reader record. A varint is now a scalar read
      (`frames.varint_at`, `varint_end`), a payload is read through one
      record updated in place, and a header's fields are read by offset.
      421 to 385 allocations per request.
    - **Packet protection (first part landed).** `std/crypto/aes_gcm`
      expanded the key and derived the GHASH key on every seal and open.
      `aes_gcm.key` prepares both once and `packet.Keys` and
      `std/tls/record`'s `Protection` keep it, two allocations fewer per
      AEAD operation. What remains per operation is the counter, lengths
      and tag blocks and the three GHASH results, which a fused kernel
      would fold into one, and the zero block, which #12499 (a constant
      array allocates per use) would make free.
    - **Records handed back bare (landed).** A record returned inside a
      Result or a tuple is copied on the way out (#12501). The stream
      table, the connection's steps (`receive`, `tick`, `transmit`), the
      datagram planner, QPACK's decoder and the HTTP/3 session now hand
      themselves back with their outcome in fields, and the serve loop
      steps each connection inside the construction that stores it back.
      Huffman decoding goes four bits at a time through a state table.
      383 to 308 allocations per request; 118,646 to 145,400 responses
      per second.
    - **The QUIC connection stepped in place (landed).** `h3q` applied
      the session's writes through `conn.write`'s `Result`, copying the
      connection and its stream table per action; it now uses the bare
      steps (`written`, `read_into`). A fifth fewer instructions per
      request with 64 requests in flight per connection
      (`docs/benchmarks/net-h3-2026-10-17.md`).
    - **What remains per request**, by the x86-64 trace: packet receive
      and AEAD open, the HTTP/3 request path (each request record bound
      before it is rebuilt, a field section's partial copied), the AEAD
      blocks a fused kernel would fold, and `concat`, which builds its
      result an element at a time; a runtime kernel sizing it once is a
      compiler change that every program gains from.
    - **The socket.** `sendmmsg`, UDP GSO and GRO, ECN: new builtins, so
      four classifications each, reported Unsupported on Darwin and WASI.

0-RTT waits on early data in `std/tls/handshake`, which does not derive
an early traffic secret yet.
