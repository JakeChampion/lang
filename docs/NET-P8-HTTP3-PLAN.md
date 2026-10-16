# Net P8: QUIC and HTTP/3 — the plan

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
  Streams and flow control, connection IDs beyond the first, path
  validation and key update are slice 4; issuing a Retry is slice 7's.
- `std/http/qpack` — the static and dynamic tables, the encoder and
  decoder streams, Huffman shared with `std/http/hpack`. Slice 5.
- `std/http/h3` — frames, the control and QPACK streams, SETTINGS and
  GOAWAY, request streams as `HttpRequest` events, responses and trailers.
  Slice 6.

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
   RESET_STREAM and STOP_SENDING, stream limits, connection IDs, closing,
   stateless reset, key update, path validation. Gate: two `Conn`s
   exchanging streams under scripted loss and reordering.
5. **QPACK.** Gate: `tests/stdlib/http_qpack_test.fern`, with RFC 9204
   Appendix B's examples.
6. **HTTP/3.** `std/http/h3` over a `std/quic/conn`. Gate:
   `tests/stdlib/http_h3_test.fern`, a client and a server in memory.
7. **Serving.** `serve.Config.with_h3()`: a UDP socket per worker beside
   the TCP listener, connections routed by connection ID, QUIC's timers in
   the loop's wait, handlers parked per stream as for HTTP/2, and Alt-Svc on
   the TCP responses. The runtime gains what UDP serving needs:
   `SO_REUSEPORT` and dual-stack on UDP sockets, and the datagram's local
   address. Gate: `TestServeH3Interp` and `TestServeH3X86_64` against an
   aioquic client.
8. **Interop nightly.** `scripts/net-quic-interop` grows HTTP/3 requests
   both ways, and h3spec. Gate: the nightly job.
9. **Client.** `std/fetch` speaks HTTP/3 to an origin that offers it by
   Alt-Svc, and `std/sim_fetch` scripts datagrams. Gate: fetch against
   aioquic's server, and the simulated network.
10. **Speed.** Packet protection with the AES round keys kept, `sendmmsg`
    and UDP GSO/GRO, ECN. Gate: the alloc and throughput ratchets.

0-RTT waits on early data in `std/tls/handshake`, which does not derive
an early traffic secret yet.
