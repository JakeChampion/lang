# Net P8: QUIC and HTTP/3 — the plan

Status: [plan] in progress. Slices 1 to 7 and the h3spec half of 8 landed. Open: 4b, the rest of 8 (more interop peers), 9 (the client), the rest of 10 (speed) and the h3spec case of 0-RTT.

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
  Connection IDs beyond the first and path validation landed in slice 4b.
  Issuing a Retry (`retry`, `validated`, `accept_validated`,
  `invalid_token`) landed with #12513: the token is HMAC-SHA256 over the
  client's address, the Retry's connection ID, the issue time and the ID
  the client's first Initial named, under a key the caller holds, so the
  server keeps no state until the token comes back.
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
   a field section's size are capped. A field section's size counts each
   field as it is decoded. An encoder-stream literal too long for the table
   is refused at its length. A stream that waits on QPACK stays unread, so
   its window holds its later bytes.
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
   connection IDs and keys mid-stream. Landed (#12512).

   Connection IDs: each side derives its IDs and their reset tokens from a
   32-byte secret in its entropy (HMAC-SHA256 over the sequence number), so
   neither needs a random source after the handshake. Once connected, a
   side issues IDs until the peer holds its `active_connection_id_limit`,
   and issues another whenever the peer retires one. The peer's IDs are
   stored up to this side's limit (CONNECTION_ID_LIMIT_ERROR past it),
   `retire_prior_to` retires the ones below it and moves the ID in use when
   it is among them, a RETIRE_CONNECTION_ID for the ID the packet arrived
   on or for a sequence number never issued is PROTOCOL_VIOLATION, and
   `Conn.rotate_dcid()` moves to the next ID the peer issued. A stateless
   reset matches any of the peer's tokens. A datagram is this connection's
   when it names any ID this side issued (`Conn.local_cids()`, re-read by
   a router when `Conn.cids_version()` moves).

   Paths: `Conn.receive_from(datagram, path, now)` takes the sender's
   address in the caller's encoding. A server whose client sends from a new
   address moves to it once a packet from there is authenticated: it sends
   a PATH_CHALLENGE there, expanded to 1200 bytes when the path's
   amplification budget allows, limits what it sends on the unvalidated
   path to three times what it received there, and addresses the client by
   a spare connection ID when one was issued (RFC 9000 sections 9.3 and
   9.5). A PATH_RESPONSE with the challenge's data validates the path; no
   answer within three PTOs returns to the previous path. A client drops
   datagrams from an address that is not its server's. `Conn.path()` is
   where datagrams go now; `std/serve` keeps each connection's address and
   routing IDs in step with it. Congestion control is not reset on
   migration: NAT rebinding, the case the slice is for, keeps the route.
   Gates: `tests/stdlib/quic_conn_test.fern` (IDs issued to the limit, a
   rotation mid-stream, a rebinding followed and validated, a path that does
   not answer abandoned, a client ignoring an unknown server address,
   retire_prior_to, the refused frames), `TestServeH3RebindX86_64` (a
   client's second request from a second socket, answered there on a new
   ID), and aioquic's client changing connection ID between its requests
   in `scripts/net-quic-interop`.

   The key-update portion is implemented independently of connection-ID
   rotation and path validation. `Conn.update_keys(now)`
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

   Under load (#12513): once `Config.h3_retry_above` connections are still
   in their handshake, a new client's Initial is answered with a Retry and
   no state is kept. The default is 32. A value of 0 asks every client, and
   a negative value never asks. The Initial that echoes the token is
   accepted with the client's first connection ID as
   `original_destination_connection_id` and the Retry's as
   `retry_source_connection_id`. A token the server did not issue, or one
   older than ten seconds, is answered with a CONNECTION_CLOSE that carries
   INVALID_TOKEN (section 8.1.3), sealed with the Initial keys of the ID it
   named. One `serve.run` picks the token key, so every worker it forks
   accepts the tokens any of them issued. Gate: `TestServeH3RetryX86_64`
   sends a spoofed Initial that gets a Retry alone, a forged token that is
   closed with INVALID_TOKEN, and a client that is served through the
   Retry. `scripts/net-quic-interop` has aioquic's HTTP/3 client answer
   the Fern server's Retry.
8. **Interop nightly.** h3spec on `scripts/net-h3spec`, every case
   against `std/serve` with HTTP/3 on, landed (#12511). More HTTP/3 peers
   beside aioquic (`scripts/net-quic-interop` has carried HTTP/3 both ways
   since 7a) are open. Gate: the nightly jobs.
9. **Client (landed, #12510).** `std/fetch` speaks HTTP/3 to an origin
   that offers it. An Alt-Svc field on a TLS response from the origin
   itself names the UDP port (`fetch.alt_svc` reads the value, the pool's
   `note_alt_svc` ages it, `offer_h3` seeds it). The next request to that
   origin dials QUIC through two datagram functions on `fetch.Transport`,
   drives `std/http/h3q` as it drives `std/http/h2`, and keeps the
   connection in the pool (`h3q.Parked`, the connection without its
   events). A QUIC connection that cannot be made forgets the alternative
   and the request goes over TCP (RFC 9114 section 3.1). `std/sim_fetch`
   hosts an HTTP/3 endpoint in memory (`serve_h3`) and carries the datagrams
   with a delay and a loss schedule. Gate: `tests/stdlib/sim_fetch_h3_test.fern`
   on the simulated network, `TestFetchH3X86_64` and `TestFetchH3Interp`
   against `std/serve` with HTTP/3 on, discovered by Alt-Svc, and
   `scripts/net-quic-interop` against aioquic's server on the nightly.
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
      The send side landed with #12516: `udp_sendmmsg(sock, addrs, data,
      lens)` sends many messages in one call, a message whose segment size
      is set carrying `UDP_SEGMENT`, under the `mmsg` capability that
      `hosted-linux` and `hosted-android` grant. `net.send_many` groups a
      flight into those messages (a run of datagrams of one size, a shorter
      one as its last segment, at most 64 segments and 65,535 bytes per
      message) and sends each datagram on its own elsewhere. `std/serve`
      gathers every flight of one wake of its loop and sends them in one
      call before it waits again. With 64 connections that is 57 times fewer
      sends and 41 percent less server CPU per request
      (`docs/benchmarks/net-sendmmsg-2026-11-08.md`). Gates:
      `TestSelfHostUDPSendmmsg` and `TestNetSendManyX86_64`. The receive
      side landed next: `udp_recvmmsg(sock, buf, froms, lens)` brings many
      messages in one call, each with its sender, byte count, `UDP_GRO`
      segment size and TOS byte, under the same `mmsg` capability; socket
      ops 10, 11 and 12 set `UDP_GRO`, `IP_RECVTOS` and `IPV6_RECVTCLASS`.
      `net.Inbox` receives into slots and walks a coalesced message into
      its datagrams, and `std/serve` reads every datagram of a wake through
      an inbox of 16 slots with `UDP_GRO` on. With 64 connections that is
      15 times fewer receives and 51 percent less server CPU per request
      (`docs/benchmarks/net-recvmmsg-2026-11-08.md`). Gates:
      `TestSelfHostUDPRecvmmsg` and `TestNetInboxX86_64`. ECN landed last
      (RFC 9000 section 13.4, RFC 9002 appendix B.7). A connection
      configured `with_ecn` marks its packets ECT(0), and `udp_sendmmsg`
      puts each message's TOS byte in `IP_TOS` or `IPV6_TCLASS`. The
      receiver counts the marks of each packet number space and reports
      them in ACK_ECN frames. The sender validates the counts on each ACK
      that raises the largest acknowledged packet, and it stops marking
      when the counts are missing, fall, or rise by less than the marked
      packets acknowledged. A rising ECN-CE count is a congestion event.
      `std/serve` turns ECN on where it can send and read the TOS byte.
      A hello request allocates no more than before. Gates:
      `tests/stdlib/quic_conn_test.fern`, `tests/stdlib/quic_recovery_test.fern`
      and `TestServeH3EcnX86_64`.

0-RTT (#12514) starts with session resumption over QUIC. A server
configured `with_tickets` issues a NewSessionTicket in 1-RTT once the
handshake is done. The ticket has the sealed format of `std/tls/server`
(`issue_ticket`, `lookup_ticket`). A TCP and a QUIC server with one secret
can take each other's tickets, which no test checks yet. A client keeps each
ticket (`Conn.tickets`) with the server's transport parameters, and
offers it with `with_ticket`. With `with_early_data` on both ends, the
ticket allows early data. The client then writes its first streams in
0-RTT under the limits it remembered. The server reads them before its own
handshake completes, and it can answer in 1-RTT at once. A server that
refuses the early data drops the 0-RTT packets, and the client sends their
frames again in 1-RTT.

The HTTP/3 half uses this. `std/serve` issues tickets sealed with the key
of its Retry tokens, and takes 0-RTT. A connection records each stream that
carried data in 0-RTT (`Conn.early_stream`), because an attacker can replay
that data. `h3q` answers a request on such a stream with 425 Too Early when
its method is not safe (RFC 8470 section 5.2). It gives a safe request to
the handler with the field `Early-Data: 1`.

`std/fetch` keeps the last ticket of each origin in its pool and uses each
ticket one time. On a resumed connection, it sends a safe method in 0-RTT
and any other method after the handshake. It sends a request again after the
handshake when the server answers its 0-RTT copy with 425.

Two items stay open. h3spec's 0-RTT case has not run against the server
yet. No test makes a TCP and a QUIC server take each other's tickets.
