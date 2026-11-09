# QUIC connection state

This module implements QUIC connections without I/O.

## Model

A QUIC connection coordinates packets, TLS, transport parameters, streams, and recovery. Input datagrams and time progress drive the protocol state.

## Contracts

The caller performs I/O and sends produced datagrams. Configuration separates client and server identity requirements. Deadlines determine when timer progress is due.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 const HANDSHAKING: i32>>

<<4 pub function config(alpn: string[]): Config>>

<<5 pub struct Close>>

<<6 function __space(): Space>>

<<7 pub function client_entropy(c: Config): i32>>

<<8 function __tls_client_config(c: Config, scid: [u8], params_bytes: u8[]): handshake.ClientConfig>>

<<9 pub function client(c: Config, entropy: [u8], now: Instant, wall: time.Timestamp): Result[Conn, string]>>

<<10 function (c: Conn) __initial_keys(dcid: [u8]): Conn>>

<<11 pub function (c: Conn) connected(): boolean>>

<<12 function __earliest(a: i64, b: i64): i64>>

<<13 pub function (c: Conn) events_taken(): Conn>>

<<15 pub function (c: Conn) open_stream(uni: boolean): Result[(Conn, i64), string]>>

<<16 function (c: Conn) __checked(): Result[Conn, string]>>

<<17 pub function (c: Conn) written(id: i64, data: u8[], fin: boolean): Conn>>

<<18 function (c: Conn) __fail(code: i64, frame_type: i64, reason: string, now: i64): Conn>>

<<19 pub function (c: Conn) tick(now: Instant): Conn>>

<<20 function (c: Conn) __tick(now: i64): Conn>>

<<21 pub function (c: Conn) update_keys(now: Instant): Result[Conn, string]>>

<<22 function (c: Conn) __promote_keys(send: packet.Keys, recv: packet.Keys, pn: i64, now: i64, remote: boolean)...>>

<<23 pub function (c: Conn) receive(datagram: [u8], now: Instant): Conn>>

<<24 function (c: Conn) __datagram(datagram: [u8], now: i64): Conn>>

<<25 function (c: Conn) __retry_held(now: i64): Conn>>

<<26 function (c: Conn) __frame(f: frames.Frame, space: i32, now: i64): Conn>>

<<27 function (c: Conn) __discard(space: i32): Conn>>

<<28 function (c: Conn) __tls_question(e: handshake.Event, now: i64): Conn>>

<<29 pub function (c: Conn) transmit(now: Instant): Conn>>

<<30 function (c: Conn) __datagram_sent(now: i64): Conn>>

<<31 struct Plan>>

<<32 function __no_plan(space: i32): Plan>>

<<33 function (c: Conn) __crypto_out(space: i32, room: i32): Conn>>

<<34 function (c: Conn) __seal(planned: i32, now: i64): Conn>>

<<35 function __add_range(ranges: frames.AckRange[], pn: i64): frames.AckRange[]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module implements QUIC connections without I/O.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/quic/frames";
import "std/quic/packet";
import "std/quic/params";
import "std/quic/recovery";
import "std/quic/streams";
import "std/tls/client" as tlsclient;
import "std/tls/handshake";
import "std/tls/keyschedule";
import "std/tls/identity";
import "std/tls/message";
import "std/tls/server" as tlsserver;
import "std/tls/verify";
import "std/array";
import "std/option";
import "std/i64";
import "std/time";
import "std/crypto";
```

### `3 const HANDSHAKING: i32`

```fern
<<3 const HANDSHAKING: i32>>=
const HANDSHAKING: i32 = 0;

const ESTABLISHED: i32 = 1;

const CLOSING: i32 = 2;

const DRAINING: i32 = 3;

const CLOSED: i32 = 4;

const MAX_ACK_RANGES: i32 = 32;

const MAX_CRYPTO_HELD: i64 = 65536;

const MAX_UNDECRYPTED: i32 = 8;

pub struct Config {
  alpn: string[],
  params: params.Params,
  cid_len: i32,
  max_datagram: i32,
  server_name: string,
  roots: verify.Roots,
  identity: Option[identity.Identity],
  ecn: boolean,
  // ticket_key seals the session tickets a server issues; empty, it issues
  // none and resumes none. resume is the ticket a client offers.
  ticket_key: u8[],
  ticket_lifetime: Duration,
  resume: Option[Ticket],
  // early_data has a client send 0-RTT with a ticket that allows it, and a
  // server take 0-RTT and issue tickets that allow it.
  early_data: boolean,
}

// Ticket is a session ticket with the transport parameters of the server
// that issued it, which bound what a client sends in 0-RTT (RFC 9000
// section 7.4.1).
pub struct Ticket { tls: tlsclient.Ticket, params: params.Params }
```

### `4 pub function config(alpn: string[]): Config`

```fern
<<4 pub function config(alpn: string[]): Config>>=
pub function config(alpn: string[]): Config {
  let p = params.Params { ...params.defaults(), max_idle_timeout: 30000 as i64, max_udp_payload_size: 1472 as i64, disable_active_migration: true };
  return Config { alpn: alpn, params: p, cid_len: 8, max_datagram: 1200, server_name: "", roots: verify.no_roots(), identity: None, ecn: false, ticket_key: [], ticket_lifetime: time.duration_seconds(7200 as i64), resume: None, early_data: false };
}

// with_ecn says the transport carries ECN marks both ways: it sends each
// flight with ecn_codepoint and passes each datagram's mark to receive_from.
// The connection then marks its packets ECT(0) until the peer's ACKs fail
// validation (RFC 9000 section 13.4).
pub function (c: Config) with_ecn(): Config {
  return Config { ...c, ecn: true };
}

pub function (c: Config) client_side(server_name: string, roots: verify.Roots): Config {
  return Config { ...c, server_name: server_name, roots: roots };
}

pub function (c: Config) with_streams(bidi: i64, uni: i64, stream_window: i64, connection_window: i64): Config {
  let p = params.Params { ...c.params, initial_max_streams_bidi: bidi, initial_max_streams_uni: uni, initial_max_stream_data_bidi_local: stream_window, initial_max_stream_data_bidi_remote: stream_window, initial_max_stream_data_uni: stream_window, initial_max_data: connection_window };
  return Config { ...c, params: p };
}

pub function (c: Config) server_side(id: identity.Identity): Config {
  return Config { ...c, identity: Some(id) };
}

// with_tickets has a server issue a session ticket on each connection,
// sealed under a key derived from `secret`, and resume the tickets that any
// server with the same secret issued.
pub function (c: Config) with_tickets(secret: [u8]): Config {
  return Config { ...c, ticket_key: tlsserver.ticket_key_of(secret) };
}

// with_ticket has a client offer the ticket `t` to resume its session.
pub function (c: Config) with_ticket(t: Ticket): Config {
  return Config { ...c, resume: Some(t) };
}

// with_early_data turns 0-RTT on: a client sends its first streams before
// the handshake completes when its ticket allows it, and a server takes
// them and issues tickets that allow it. 0-RTT data can be replayed, so a
// server must take only what is safe to repeat (RFC 9001 section 9.2).
pub function (c: Config) with_early_data(): Config {
  return Config { ...c, early_data: true };
}
```

### `5 pub struct Close`

```fern
<<5 pub struct Close>>=
pub struct Close { code: i64, frame_type: i64, reason: string, app: boolean, remote: boolean }

pub enum Event { Connected, Closed(Close) }

struct Space {
  send: Option[packet.Keys],
  recv: Option[packet.Keys],
  next_pn: i64,
  largest_recv: i64,
  largest_recv_at: i64,
  ranges: frames.AckRange[],
  ack_wanted: boolean,
  crypto_out: u8[],
  crypto_sent: i64,
  crypto_lost: i64[],
  crypto_in: streams.Reassembly,
  discarded: boolean,
  // ecn counts the marks of the packets received in the space, and
  // peer_ecn holds the largest counts the peer's ACKs reported.
  ecn: frames.Ecn,
  peer_ecn: frames.Ecn,
}

enum Tls { TlsClient(handshake.Client), TlsServer(handshake.Server), TlsNone }

pub struct Conn {
  config: Config,
  server: boolean,
  tls: Tls,
  spaces: Space[],
  recovery: recovery.Recovery,
  scid: u8[],
  dcid: u8[],
  odcid: u8[],
  retry_scid: Option[u8[]],
  token: u8[],
  peer_seen: boolean,
  peer_params: Option[params.Params],
  reset_token: u8[],
  state: i32,
  close: Option[Close],
  close_pending: boolean,
  close_until: i64,
  idle_until: i64,
  validated: boolean,
  plans: Plan[],
  received_bytes: i64,
  sent_bytes: i64,
  handshake_done_pending: boolean,
  probes: i32[],
  pending: frames.Frame[],
  reader: frames.Reader,
  held: u8[][],
  streams: streams.Streams,
  events: Event[],
  out: u8[][],
  key_phase: boolean,
  key_next_send: Option[packet.Keys],
  key_next_recv: Option[packet.Keys],
  key_previous_recv: Option[packet.Keys],
  key_first_recv: i64,
  key_previous_max: i64,
  key_first_sent: i64,
  key_unacked: boolean,
  key_awaiting_peer: boolean,
  key_generate_at: i64,
  key_discard_at: i64,
  key_sent: i64,
  failed_auth: i64,
  wall_offset: i64,
  cid_secret: u8[],
  local_cids: Cid[],
  local_next: i64,
  remote_cids: Cid[],
  dcid_seq: i64,
  retire_prior: i64,
  cids_version: i64,
  on_dcid: u8[],
  accepted: i64,
  path: u8[],
  probe: Option[Probe],
  // ecn_in is the mark of the datagram being processed, and held_ecn the
  // mark of each held packet. ecn_failed stops marking on this path.
  ecn_in: i32,
  held_ecn: i32[],
  ecn_failed: boolean,
  // ticket_entropy is the randomness of the ticket a server issues, and
  // tickets the tickets a client received.
  ticket_entropy: u8[],
  tickets: Ticket[],
  // zero_rtt says a client sends application packets as 0-RTT, and
  // early_recv holds the keys a server opens 0-RTT packets with.
  // early_streams are the streams that carried data in 0-RTT.
  zero_rtt: boolean,
  early_recv: Option[packet.Keys],
  early_streams: i64[],
}

// Cid is one connection ID with its sequence number and the stateless reset
// token that goes with it.
pub struct Cid { seq: i64, id: u8[], token: u8[] }

// Probe is a peer path under validation: its address, the PATH_CHALLENGE
// data, when validation gives up, the bytes exchanged on the path before
// it is validated, and the path to return to when validation fails.
struct Probe { path: u8[], data: u8[], until: i64, received: i64, sent: i64, previous: u8[] }
```

### `6 function __space(): Space`

```fern
<<6 function __space(): Space>>=
function __space(): Space {
  return Space { send: None, recv: None, next_pn: 0 as i64, largest_recv: 0 - 1 as i64, largest_recv_at: 0 as i64, ranges: [], ack_wanted: false, crypto_out: [], crypto_sent: 0 as i64, crypto_lost: [], crypto_in: streams.reassembly(), discarded: false, ecn: __no_marks(), peer_ecn: __no_marks() };
}

function __no_marks(): frames.Ecn {
  return frames.Ecn { ect0: 0 as i64, ect1: 0 as i64, ce: 0 as i64 };
}
```

### `7 pub function client_entropy(c: Config): i32`

```fern
<<7 pub function client_entropy(c: Config): i32>>=
pub function client_entropy(c: Config): i32 {
  return c.cid_len + 8 + CID_SECRET + handshake.client_entropy(__tls_client_config(c, [], []));
}

// CID_SECRET is the size of the secret new connection IDs and their reset
// tokens are derived from.
const CID_SECRET: i32 = 32;

pub function server_entropy(c: Config): i32 {
  let sc = __tls_server_config(c, [], [], []);
  return c.cid_len + 16 + CID_SECRET + handshake.server_entropy(sc) + __ticket_entropy(c);
}

function __ticket_entropy(c: Config): i32 {
  if (c.ticket_key.len() == 0) {
    return 0;
  }
  return tlsserver.TICKET_ENTROPY;
}
```

### `8 function __tls_client_config(c: Config, scid: [u8], params_bytes: u8[]): handshake.ClientConfig`

```fern
<<8 function __tls_client_config(c: Config, scid: [u8], params_bytes: u8[]): handshake.ClientConfig>>=
function __tls_client_config(c: Config, scid: [u8], params_bytes: u8[]): handshake.ClientConfig {
  let ext = message.Extension { kind: message.EXT_QUIC_TRANSPORT_PARAMETERS, data: params_bytes };
  return handshake.ClientConfig { ...handshake.client_config(c.server_name), alpn: c.alpn, shares: 2, quic: true, extensions: [ext] };
}

function __tls_server_config(c: Config, chain: u8[][], schemes: i32[], params_bytes: u8[]): handshake.ServerConfig {
  let ext = message.Extension { kind: message.EXT_QUIC_TRANSPORT_PARAMETERS, data: params_bytes };
  return handshake.ServerConfig { ...handshake.server_config(chain, schemes), alpn: c.alpn, quic: true, extensions: [ext], early_data: c.early_data };
}

function __base(c: Config, server: boolean, scid: u8[], dcid: u8[], odcid: u8[], now: i64): Conn {
  let r = recovery.new(c.max_datagram, server).with_max_ack_delay(time.duration_millis(25));
  return Conn { config: c, server: server, tls: TlsNone, spaces: [__space(), __space(), __space()], recovery: r, scid: scid, dcid: dcid, odcid: odcid, retry_scid: None, token: [], peer_seen: false, peer_params: None, reset_token: [], state: HANDSHAKING, close: None, close_pending: false, close_until: 0 as i64, idle_until: now + c.params.max_idle_timeout * 1000000, validated: !server, plans: [__no_plan(0), __no_plan(1), __no_plan(2)], reader: frames.reader(), received_bytes: 0 as i64, sent_bytes: 0 as i64, handshake_done_pending: false, probes: [], pending: [], held: [], streams: streams.table(server, c.params), events: [], out: [], key_phase: false, key_next_send: None, key_next_recv: None, key_previous_recv: None, key_first_recv: 0 as i64, key_previous_max: -1i64, key_first_sent: 0 as i64, key_unacked: false, key_awaiting_peer: false, key_generate_at: 0 as i64, key_discard_at: 0 as i64, key_sent: 0 as i64, failed_auth: 0 as i64, wall_offset: 0 as i64, cid_secret: [], local_cids: [Cid { seq: 0 as i64, id: scid, token: [] }], local_next: 1 as i64, remote_cids: [], dcid_seq: 0 as i64, retire_prior: 0 as i64, cids_version: 0 as i64, on_dcid: [], accepted: 0 as i64, path: [], probe: None, ecn_in: 0, held_ecn: [], ecn_failed: false, ticket_entropy: [], tickets: [], zero_rtt: false, early_recv: None, early_streams: [] };
}
```

### `9 pub function client(c: Config, entropy: [u8], now: Instant, wall: time.Timestamp): Result[Conn, string]`

```fern
<<9 pub function client(c: Config, entropy: [u8], now: Instant, wall: time.Timestamp): Result[Conn, string]>>=
// `now` reads the monotonic clock the connection's timers run on; `wall` is the time certificates are checked against.
pub function client(c: Config, entropy: [u8], now: Instant, wall: time.Timestamp): Result[Conn, string] {
  return __client(c, entropy, time.instant_nanos(now), wall.unix_millis());
}

function __offered(c: Config, unix_ms: i64): Option[handshake.Resumption] {
  let Some(t) = c.resume else {
    return None;
  };
  return tlsclient.offered(t.tls, c.server_name, time.timestamp_from_unix_millis(unix_ms));
}

function __early_wanted(c: Config): boolean {
  let Some(t) = c.resume else {
    return false;
  };
  return c.early_data && t.tls.max_early > 0 as u32;
}

function __client(c: Config, entropy: [u8], now: i64, unix_ms: i64): Result[Conn, string] {
  let scid = __bytes_range(entropy, 0, c.cid_len);
  let dcid = __bytes_range(entropy, c.cid_len, c.cid_len + 8);
  let p = params.Params { ...c.params, initial_source_connection_id: Some(scid) };
  let cc = handshake.ClientConfig { ...__tls_client_config(c, scid, params.encode(p, false)), resume: __offered(c, unix_ms), early_data: __early_wanted(c) };
  let secret = __bytes_range(entropy, c.cid_len + 8, c.cid_len + 8 + CID_SECRET);
  match (handshake.client_start(cc, entropy[c.cid_len + 8 + CID_SECRET:entropy.len()])) {
    Ok(step) => {
      let conn = __base(c, false, scid, dcid, dcid, now).__initial_keys(dcid);
      conn = Conn { ...conn, tls: TlsClient(step.client), wall_offset: unix_ms * 1000000 - now, cid_secret: secret };
      return Ok(conn.__tls_events(step.events, now));
    },
    Err(e) => {
      return Err(e.message());
    }
  }
}

pub function accept(c: Config, entropy: [u8], first: packet.Header, now: Instant, wall: time.Timestamp): Result[Conn, string] {
  return __accept(c, entropy, first, first.dcid, None, time.instant_nanos(now), wall.unix_millis());
}

// accept_validated accepts `first`, an Initial whose token `validated`
// returned `odcid` for: the client's first Initial named `odcid`, and the
// Retry that answered it named `first.dcid` (RFC 9000 section 7.3).
pub function accept_validated(c: Config, entropy: [u8], first: packet.Header, odcid: [u8], now: Instant, wall: time.Timestamp): Result[Conn, string] {
  return __accept(c, entropy, first, odcid, Some(first.dcid), time.instant_nanos(now), wall.unix_millis());
}

function __accept(c: Config, entropy: [u8], first: packet.Header, odcid: [u8], retried: Option[u8[]], now: i64, unix_ms: i64): Result[Conn, string] {
  let Some(id) = c.identity else {
    return Err("quic: a server needs an identity");
  };
  let scid = __bytes_range(entropy, 0, c.cid_len);
  let reset = __bytes_range(entropy, c.cid_len, c.cid_len + 16);
  let p = params.Params { ...c.params, original_destination_connection_id: Some(__copy(odcid)), initial_source_connection_id: Some(scid), stateless_reset_token: Some(reset), retry_source_connection_id: retried };
  let sc = __tls_server_config(c, id.chain, [id.key.scheme()], params.encode(p, true));
  let secret = __bytes_range(entropy, c.cid_len + 16, c.cid_len + 16 + CID_SECRET);
  let tls_end = entropy.len() - __ticket_entropy(c);
  match (handshake.server_new(sc, entropy[c.cid_len + 16 + CID_SECRET:tls_end])) {
    Ok(s) => {
      let conn = __base(c, true, scid, first.scid, __copy(odcid), now).__initial_keys(first.dcid);
      return Ok(Conn { ...conn, tls: TlsServer(s), retry_scid: retried, reset_token: reset, cid_secret: secret, local_cids: [Cid { seq: 0 as i64, id: scid, token: reset }], wall_offset: unix_ms * 1000000 - now, ticket_entropy: __bytes_range(entropy, tls_end, entropy.len()) });
    },
    Err(e) => {
      return Err(e.message());
    }
  }
}

// retry_entropy is the number of random bytes `retry` needs.
pub function retry_entropy(c: Config): i32 {
  return c.cid_len;
}

// TOKEN_LIFETIME_MS is how long a server accepts a token its Retry issued.
// A client answers a Retry within one round trip, so a token older than this
// was replayed or kept.
const TOKEN_LIFETIME_MS: i64 = 10000;

// A Retry token: this label, the issue time in Unix milliseconds, the length
// and bytes of the connection ID the client's first Initial named, then
// HMAC-SHA256 over the client's address, the Retry's connection ID and
// those bytes (RFC 9000 section 8.1.4).
const TOKEN_RETRY: u8 = 1u8;

const TOKEN_MAC: i32 = 32;

// retry answers `first`, an Initial from `path` without a token, with a
// Retry packet (RFC 9000 section 8.1.2): a connection ID from `entropy`
// and a token authenticated with `key` that binds the client's address and
// first connection ID to that ID. The server keeps no state for the client
// until the token comes back.
pub function retry(c: Config, key: [u8], entropy: [u8], first: packet.Header, path: [u8], now: time.Timestamp): u8[] {
  let scid = __bytes_range(entropy, 0, c.cid_len);
  if (__same(scid, first.dcid)) {
    scid = scid.with(0, (scid[0] as i32 ^ 1) as u8);
  }
  let body: u8[] = [TOKEN_RETRY];
  body = __be64_appended(body, now.unix_millis());
  body = body.append(first.dcid.len() as u8).concat(first.dcid);
  let token = body.concat(crypto.hmac_sha256(key, __token_message(path, scid, body)));
  return packet.retry(first.scid, scid, first.dcid, token);
}

function __token_message(path: [u8], scid: [u8], body: [u8]): u8[] {
  let out: u8[] = [path.len() as u8];
  return out.concat(path).append(scid.len() as u8).concat(scid).concat(body);
}

function __be64_appended(own out: u8[], v: i64): u8[] {
  let shift = 56;
  while (shift >= 0) {
    out = out.append((v >> shift & 255) as u8);
    shift -= 8;
  }
  return out;
}

function __be64_at(b: [u8], at: i32): i64 {
  let v: i64 = 0;
  let i = 0;
  while (i < 8) {
    v = v << 8 | b[at + i] as i64;
    i += 1;
  }
  return v;
}

// validated checks the token on `first`, an Initial from `path`, against
// `key`: `retry` issued it for this address and this connection ID within
// TOKEN_LIFETIME_MS of `now`. Some is the connection ID the client's first
// Initial named; None is a token this server did not issue or no longer
// accepts, which `invalid_token` answers.
pub function validated(key: [u8], first: packet.Header, path: [u8], now: time.Timestamp): Option[u8[]] {
  let t = first.token;
  if (t.len() < 10 + TOKEN_MAC || t[0] != TOKEN_RETRY) {
    return None;
  }
  let n = t[9] as i32;
  if (t.len() != 10 + n + TOKEN_MAC) {
    return None;
  }
  if (!crypto.hmac_sha256_verify(key, __token_message(path, first.dcid, t[0:10 + n]), t[10 + n:t.len()])) {
    return None;
  }
  let issued = __be64_at(t, 1);
  let ms = now.unix_millis();
  if (issued > ms || ms - issued > TOKEN_LIFETIME_MS) {
    return None;
  }
  return Some(__bytes_range(t, 10, 10 + n));
}

// invalid_token is the CONNECTION_CLOSE a server answers `first` with when
// `validated` refused its token (RFC 9000 section 8.1.3), sealed with the
// Initial keys of the connection ID it names, so the server keeps no state.
pub function invalid_token(first: packet.Header): u8[] {
  let why = frames.ConnectionClose { code: frames.INVALID_TOKEN, frame_type: 0 as i64, reason: "the token is not one this server issued", app: false };
  return packet.seal_long(packet.Long { kind: packet.Initial, dcid: first.scid, scid: first.dcid, token: [], pn: 0 as i64, pn_len: 1 }, frames.encode(frames.ConnectionCloseFrame(why)), packet.initial_keys(first.dcid, false));
}
```

### `10 function (c: Conn) __initial_keys(dcid: [u8]): Conn`

```fern
<<10 function (c: Conn) __initial_keys(dcid: [u8]): Conn>>=
function (c: Conn) __initial_keys(dcid: [u8]): Conn {
  let s = c.spaces[recovery.INITIAL];
  s = Space { ...s, send: Some(packet.initial_keys(dcid, !c.server)), recv: Some(packet.initial_keys(dcid, c.server)) };
  return Conn { ...c, spaces: c.spaces.with(recovery.INITIAL, s) };
}
```

### `11 pub function (c: Conn) connected(): boolean`

```fern
<<11 pub function (c: Conn) connected(): boolean>>=
pub function (c: Conn) connected(): boolean {
  return c.state == ESTABLISHED;
}

// handshaking is whether the connection has neither completed its handshake
// nor begun to close.
pub function (c: Conn) handshaking(): boolean {
  return c.state == HANDSHAKING;
}

// retried is whether the connection went through a Retry: the server asked
// the client to prove its address before it kept any state.
// tickets are the session tickets a client received, to resume with
// Config.with_ticket.
pub function (c: Conn) tickets(): Ticket[] {
  return c.tickets;
}

// tickets_taken is the connection without the tickets it received, once
// the caller keeps them.
pub function (c: Conn) tickets_taken(): Conn {
  return Conn { ...c, tickets: [] };
}

// sending_early says a client sends application data in 0-RTT, before the
// handshake ends.
pub function (c: Conn) sending_early(): boolean {
  return c.zero_rtt;
}

// resumed says the handshake resumed a session with a ticket.
pub function (c: Conn) resumed(): boolean {
  match (c.tls) {
    TlsClient(t) => {
      return t.resumed();
    },
    TlsServer(t) => {
      return t.resumed();
    },
    TlsNone => {
      return false;
    }
  }
  return false;
}

// early_stream says stream id carried data in 0-RTT, which an attacker
// can replay (RFC 9001 section 9.2).
pub function (c: Conn) early_stream(id: i64): boolean {
  for s in c.early_streams {
    if (s == id) {
      return true;
    }
  }
  return false;
}

pub function (c: Conn) retried(): boolean {
  return c.retry_scid.is_some();
}

pub function (c: Conn) confirmed(): boolean {
  return c.state == ESTABLISHED && c.spaces[recovery.HANDSHAKE].discarded;
}

pub function (c: Conn) done(): boolean {
  return c.state == CLOSED;
}

pub function (c: Conn) alpn(): string {
  match (c.tls) {
    TlsClient(t) => {
      return t.alpn();
    },
    TlsServer(t) => {
      return t.alpn;
    },
    TlsNone => {
      return "";
    }
  }
}

pub function (c: Conn) scid(): u8[] {
  return c.scid;
}

// local_cids is every connection ID this side currently answers to: the
// IDs a datagram may name for this connection.
pub function (c: Conn) local_cids(): u8[][] {
  let out: u8[][] = [];
  for k in c.local_cids {
    out = out.append(k.id);
  }
  return out;
}

// cids_version changes whenever local_cids changes, so a caller that routes
// datagrams by connection ID knows when to read them again.
pub function (c: Conn) cids_version(): i64 {
  return c.cids_version;
}

// path is the peer address, as the caller gave it to receive_from, that
// datagrams are sent to now. Empty until the first receive_from.
pub function (c: Conn) path(): u8[] {
  return c.path;
}

// rotate_dcid addresses the peer by the next connection ID it issued and
// retires the one in use. Without a spare ID the connection is unchanged.
pub function (c: Conn) rotate_dcid(): Conn {
  let Some(next) = __cid_after(c.remote_cids, c.dcid_seq) else {
    return c;
  };
  return Conn { ...c, pending: c.pending.append(frames.RetireConnectionId(c.dcid_seq)), remote_cids: __cids_minus(c.remote_cids, c.dcid_seq), dcid: next.id, dcid_seq: next.seq };
}

pub function (c: Conn) peer_params(): Option[params.Params] {
  return c.peer_params;
}

pub function (c: Conn) closed_with(): Option[Close] {
  return c.close;
}

// When the connection next needs a tick, if ever.
pub function (c: Conn) deadline(): Option[Instant] {
  let at = c.__deadline();
  if (at == 0) {
    return None;
  }
  return Some(time.instant_from_nanos(at));
}

function (c: Conn) __deadline(): i64 {
  if (c.state == CLOSED) {
    return 0 as i64;
  }
  if (c.state == CLOSING || c.state == DRAINING) {
    return c.close_until;
  }
  let probe_until = 0 as i64;
  if let Some(pr) = c.probe {
    probe_until = pr.until;
  }
  return __earliest(__earliest(__earliest(c.recovery.timer_ns(), c.idle_until), __earliest(c.key_generate_at, c.key_discard_at)), probe_until);
}
```

### `12 function __earliest(a: i64, b: i64): i64`

```fern
<<12 function __earliest(a: i64, b: i64): i64>>=
function __earliest(a: i64, b: i64): i64 {
  if (a == 0 || b != 0 && b < a) {
    return b;
  }
  return a;
}
```

### `13 pub function (c: Conn) events_taken(): Conn`

```fern
<<13 pub function (c: Conn) events_taken(): Conn>>=
pub function (c: Conn) events_taken(): Conn {
  return Conn { ...c, events: [], streams: c.streams.events_taken() };
}

// stream_events lists the streams' events since events_taken. A step's
// connection events come before its stream events.
pub function (c: Conn) stream_events(): streams.Event[] {
  return c.streams.events;
}

pub function (c: Conn) datagrams(): u8[][] {
  return c.out;
}

pub function (c: Conn) sent(): Conn {
  return Conn { ...c, out: [] };
}
```

### `15 pub function (c: Conn) open_stream(uni: boolean): Result[(Conn, i64), string]`

```fern
<<15 pub function (c: Conn) open_stream(uni: boolean): Result[(Conn, i64), string]>>=
pub function (c: Conn) open_stream(uni: boolean): Result[(Conn, i64), string] {
  if (!c.__app_ready()) {
    return Err("quic: the connection is not established");
  }
  let cur = Conn { ...c, streams: c.streams.open(uni) };
  if (cur.streams.refused != "") {
    return Err("quic: " + cur.streams.refused);
  }
  let id = cur.streams.last_opened;
  return Ok((cur, id));
}

pub function (c: Conn) can_open(uni: boolean): boolean {
  return c.__app_ready() && c.streams.can_open(uni);
}

// __app_ready says streams can carry data: once established, while a
// client sends 0-RTT, and on a server that took 0-RTT once it can write
// 1-RTT.
function (c: Conn) __app_ready(): boolean {
  if (c.state == ESTABLISHED || c.zero_rtt) {
    return true;
  }
  return c.server && c.state == HANDSHAKING && c.early_recv.is_some() && c.spaces[recovery.APPLICATION].send.is_some();
}

pub function (c: Conn) blocked_open(uni: boolean): Conn {
  return Conn { ...c, streams: c.streams.blocked_open(uni) };
}

pub function (c: Conn) write(id: i64, data: u8[], fin: boolean): Result[Conn, string] {
  return c.written(id, data, fin).__checked();
}

pub function (c: Conn) read(id: i64): Result[(Conn, u8[], boolean), string] {
  let cur = c.read_into(id);
  if (cur.refused() != "") {
    return Err("quic: " + cur.refused());
  }
  let data = cur.read_data();
  let fin = cur.read_fin();
  return Ok((cur.read_taken(), data, fin));
}

pub function (c: Conn) reset_stream(id: i64, code: i64): Result[Conn, string] {
  return c.stream_reset(id, code).__checked();
}

pub function (c: Conn) stop_sending(id: i64, code: i64): Result[Conn, string] {
  return c.sending_stopped(id, code).__checked();
}
```

### `16 function (c: Conn) __checked(): Result[Conn, string]`

```fern
<<16 function (c: Conn) __checked(): Result[Conn, string]>>=
function (c: Conn) __checked(): Result[Conn, string] {
  if (c.refused() != "") {
    return Err("quic: " + c.refused());
  }
  return Ok(c);
}
```

### `17 pub function (c: Conn) written(id: i64, data: u8[], fin: boolean): Conn`

```fern
<<17 pub function (c: Conn) written(id: i64, data: u8[], fin: boolean): Conn>>=
pub function (c: Conn) written(id: i64, data: u8[], fin: boolean): Conn {
  return Conn { ...c, streams: c.streams.write(id, data, fin) };
}

pub function (c: Conn) read_into(id: i64): Conn {
  return Conn { ...c, streams: c.streams.read(id) };
}

pub function (c: Conn) stream_reset(id: i64, code: i64): Conn {
  return Conn { ...c, streams: c.streams.reset(id, code) };
}

pub function (c: Conn) sending_stopped(id: i64, code: i64): Conn {
  return Conn { ...c, streams: c.streams.stop(id, code) };
}

pub function (c: Conn) refused(): string {
  return c.streams.refused;
}

pub function (c: Conn) read_data(): u8[] {
  return c.streams.read_data;
}

pub function (c: Conn) read_fin(): boolean {
  return c.streams.read_fin;
}

pub function (c: Conn) read_taken(): Conn {
  return Conn { ...c, streams: streams.Streams { ...c.streams, read_data: [] } };
}

pub function (c: Conn) buffered(id: i64): i64 {
  return c.streams.buffered(id);
}

pub function (c: Conn) close(code: i64, reason: string, app: boolean, now: Instant): Conn {
  return c.__close(code, reason, app, time.instant_nanos(now));
}

function (c: Conn) __close(code: i64, reason: string, app: boolean, now: i64): Conn {
  if (c.state >= CLOSING) {
    return c;
  }
  let why = Close { code: code, frame_type: 0 as i64, reason: reason, app: app, remote: false };
  return Conn { ...c, state: CLOSING, close: Some(why), close_pending: true, close_until: now + 3 * c.recovery.pto_ns(recovery.APPLICATION), events: c.events.append(Closed(why)) };
}
```

### `18 function (c: Conn) __fail(code: i64, frame_type: i64, reason: string, now: i64): Conn`

```fern
<<18 function (c: Conn) __fail(code: i64, frame_type: i64, reason: string, now: i64): Conn>>=
function (c: Conn) __fail(code: i64, frame_type: i64, reason: string, now: i64): Conn {
  if (c.state >= CLOSING) {
    return c;
  }
  let why = Close { code: code, frame_type: frame_type, reason: reason, app: false, remote: false };
  return Conn { ...c, state: CLOSING, close: Some(why), close_pending: true, close_until: now + 3 * c.recovery.pto_ns(recovery.APPLICATION), events: c.events.append(Closed(why)) };
}

function (c: Conn) __drain(why: Close, now: i64): Conn {
  if (c.state >= DRAINING) {
    return c;
  }
  let events = c.events;
  if (c.state < CLOSING) {
    events = events.append(Closed(why));
  }
  return Conn { ...c, state: DRAINING, close: Some(why), close_pending: false, close_until: now + 3 * c.recovery.pto_ns(recovery.APPLICATION), events: events };
}
```

### `19 pub function (c: Conn) tick(now: Instant): Conn`

```fern
<<19 pub function (c: Conn) tick(now: Instant): Conn>>=
pub function (c: Conn) tick(now: Instant): Conn {
  let ns = time.instant_nanos(now);
  return c.__key_timers(ns).__tick(ns);
}
```

### `20 function (c: Conn) __tick(now: i64): Conn`

```fern
<<20 function (c: Conn) __tick(now: i64): Conn>>=
function (c: Conn) __tick(now: i64): Conn {
  if (c.state == CLOSING || c.state == DRAINING) {
    if (now >= c.close_until) {
      return Conn { ...c, state: CLOSED };
    }
    return c;
  }
  if (c.state == CLOSED) {
    return c;
  }
  if (c.idle_until > 0 && now >= c.idle_until) {
    let why = Close { code: frames.NO_ERROR, frame_type: 0 as i64, reason: "idle timeout", app: false, remote: false };
    return Conn { ...c, state: CLOSED, close: Some(why), events: c.events.append(Closed(why)) };
  }
  let cur = c.__probe_timed_out(now);
  let t = cur.recovery.timer_ns();
  if (t == 0 || now < t) {
    return cur;
  }
  cur = Conn { ...cur, recovery: cur.recovery.on_timeout(time.instant_from_nanos(now)) };
  let lost = cur.recovery.lost;
  let probe = cur.recovery.probe;
  let after = Conn { ...cur, recovery: cur.recovery.outcome_taken() }.__requeue(lost);
  if (probe >= 0) {
    after = Conn { ...after, probes: after.probes.append(probe) };
  }
  return after;
}

// __probe_timed_out abandons a path whose validation got no answer in time
// and returns to the previous path (RFC 9000 section 9.3.2).
function (c: Conn) __probe_timed_out(now: i64): Conn {
  let Some(pr) = c.probe else {
    return c;
  };
  if (now < pr.until) {
    return c;
  }
  return Conn { ...c, probe: None, path: pr.previous };
}
```

### `21 pub function (c: Conn) update_keys(now: Instant): Result[Conn, string]`

```fern
<<21 pub function (c: Conn) update_keys(now: Instant): Result[Conn, string]>>=
pub function (c: Conn) update_keys(now: Instant): Result[Conn, string] {
  return c.__update_keys(time.instant_nanos(now));
}

function (c: Conn) __update_keys(now: i64): Result[Conn, string] {
  if (c.state != ESTABLISHED || !c.recovery.handshake_confirmed) {
    return Err("quic: key update before handshake confirmation");
  }
  if (c.key_awaiting_peer || c.key_unacked || c.recovery.spaces[recovery.APPLICATION].largest_acked < c.key_first_sent) {
    return Err("quic: current key phase is not acknowledged");
  }
  let Some(send) = c.key_next_send else {
    return Err("quic: next keys are not prepared");
  };
  let Some(recv) = c.key_next_recv else {
    return Err("quic: next keys are not prepared");
  };
  return Ok(c.__promote_keys(send, recv, -1i64, now, false));
}
```

### `22 function (c: Conn) __promote_keys(send: packet.Keys, recv: packet.Keys, pn: i64, now: i64, remote: boolean)...`

```fern
<<22 function (c: Conn) __promote_keys(send: packet.Keys, recv: packet.Keys, pn: i64, now: i64, remote: boolean)...>>=
function (c: Conn) __promote_keys(send: packet.Keys, recv: packet.Keys, pn: i64, now: i64, remote: boolean): Conn {
  let s = c.spaces[recovery.APPLICATION];
  let pto = c.recovery.pto_ns(recovery.APPLICATION);
  let discard = 0;
  if (remote) {
    discard = now + 3 * pto;
  }
  return Conn { ...c, spaces: c.spaces.with(recovery.APPLICATION, Space { ...s, send: Some(send), recv: Some(recv) }), key_phase: !c.key_phase, key_previous_recv: s.recv, key_previous_max: s.largest_recv, key_first_recv: pn, key_first_sent: s.next_pn, key_next_send: None, key_next_recv: None, key_unacked: remote, key_awaiting_peer: !remote, key_generate_at: now + pto, key_discard_at: discard, key_sent: 0 as i64 };
}

function (c: Conn) __key_timers(now: i64): Conn {
  let cur = c;
  if (cur.key_discard_at > 0 && now >= cur.key_discard_at) {
    cur = Conn { ...cur, key_previous_recv: None, key_discard_at: 0 as i64 };
  }
  if (cur.key_generate_at > 0 && now >= cur.key_generate_at) {
    cur = Conn { ...cur, key_generate_at: 0 as i64 };
    if (cur.state == ESTABLISHED) {
      if let Some(send) = cur.spaces[recovery.APPLICATION].send {
        if let Some(recv) = cur.spaces[recovery.APPLICATION].recv {
          cur = Conn { ...cur, key_next_send: Some(send.updated()), key_next_recv: Some(recv.updated()) };
        }
      }
    }
  }
  return cur;
}

function (c: Conn) __receive_key(u: packet.Unprotected, current: packet.Keys, now: i64): (packet.Keys, i32) {
  if (u.key_phase == c.key_phase) {
    return (current, 0);
  }
  if let Some(old) = c.key_previous_recv {
    if ((c.key_discard_at == 0 || now < c.key_discard_at) && (c.key_awaiting_peer || u.pn < c.key_first_recv)) {
      return (old, -1);
    }
  }
  if let Some(next) = c.key_next_recv {
    return (next, 1);
  }
  if let Some(old) = c.key_previous_recv {
    if (c.key_discard_at == 0 || now < c.key_discard_at) {
      return (old, 2);
    }
  }
  return (current, 2);
}

function (c: Conn) __key_authenticated(o: packet.Opened, generation: i32, now: i64): Conn {
  if (generation == 2) {
    return c.__fail(frames.KEY_UPDATE_ERROR, 0 as i64, "key update before next keys are available", now);
  }
  if (generation == 1) {
    if (!c.recovery.handshake_confirmed || c.key_unacked || o.pn <= c.spaces[recovery.APPLICATION].largest_recv) {
      return c.__fail(frames.KEY_UPDATE_ERROR, 0 as i64, "premature or nonmonotonic key update", now);
    }
    let Some(send) = c.key_next_send else {
      return c.__fail(frames.KEY_UPDATE_ERROR, 0 as i64, "send keys unavailable", now);
    };
    let Some(recv) = c.key_next_recv else {
      return c.__fail(frames.KEY_UPDATE_ERROR, 0 as i64, "receive keys unavailable", now);
    };
    return c.__promote_keys(send, recv, o.pn, now, true);
  }
  if (generation < 0) {
    return Conn { ...c, key_previous_max: __max(c.key_previous_max, o.pn) };
  }
  return c.__key_current(o.pn, now);
}

function (c: Conn) __key_current(pn: i64, now: i64): Conn {
  if (c.key_awaiting_peer || pn < c.key_first_recv) {
    if (pn <= c.key_previous_max) {
      return c.__fail(frames.KEY_UPDATE_ERROR, 0 as i64, "new keys used below an old-phase packet number", now);
    }
    let discard = c.key_discard_at;
    if (c.key_awaiting_peer) {
      discard = now + 3 * c.recovery.pto_ns(recovery.APPLICATION);
    }
    return Conn { ...c, key_first_recv: pn, key_awaiting_peer: false, key_discard_at: discard };
  }
  return c;
}
```

### `23 pub function (c: Conn) receive(datagram: [u8], now: Instant): Conn`

```fern
<<23 pub function (c: Conn) receive(datagram: [u8], now: Instant): Conn>>=
pub function (c: Conn) receive(datagram: [u8], now: Instant): Conn {
  if (c.ecn_in == 0) {
    return c.__receive(datagram, time.instant_nanos(now));
  }
  return Conn { ...c, ecn_in: 0 }.__receive(datagram, time.instant_nanos(now));
}

// receive_from is receive with the address the datagram came from, in any
// encoding the caller keeps to, and the ECN codepoint of its IP header (the
// low two bits of the TOS byte, 0 when the transport does not read it). A
// server moves to a client's new address once a packet from it is
// authenticated, validating the path; a client drops datagrams from an
// address that is not its server's (RFC 9000 section 9).
pub function (c: Conn) receive_from(datagram: [u8], path: [u8], ecn: i32, now: Instant): Conn {
  let ns = time.instant_nanos(now);
  let marked = c;
  if (c.ecn_in != (ecn & 3)) {
    marked = Conn { ...c, ecn_in: ecn & 3 };
  }
  if (c.path.len() == 0) {
    return Conn { ...marked, path: __bytes_range(path, 0, path.len()) }.__receive(datagram, ns);
  }
  if (__same(path, c.path)) {
    return marked.__receive(datagram, ns);
  }
  if (!c.server || c.state != ESTABLISHED) {
    return c;
  }
  let cur = marked.__receive(datagram, ns);
  if (cur.accepted == c.accepted || cur.state >= CLOSING) {
    return cur;
  }
  return cur.__migrating(path, datagram.len() as i64, ns);
}

function (c: Conn) __receive(datagram: [u8], now: i64): Conn {
  if (c.state == CLOSED) {
    return c;
  }
  let cur = Conn { ...c, received_bytes: c.received_bytes + datagram.len() as i64 };
  if (cur.state == CLOSING) {
    return Conn { ...cur, close_pending: true };
  }
  cur = cur.__datagram(datagram, now);
  return cur.__retry_held(now);
}
```

### `24 function (c: Conn) __datagram(datagram: [u8], now: i64): Conn`

```fern
<<24 function (c: Conn) __datagram(datagram: [u8], now: i64): Conn>>=
function (c: Conn) __datagram(datagram: [u8], now: i64): Conn {
  let cur = c;
  let at = 0;
  while (at < datagram.len() && cur.state < CLOSING) {
    match (packet.parse(datagram, at, cur.config.cid_len)) {
      Ok(h) => {
        cur = cur.__packet(datagram, h, now);
        at = h.end;
      },
      Err(_) => {
        return cur;
      }
    }
  }
  return cur;
}

function (c: Conn) __packet(datagram: [u8], h: packet.Header, now: i64): Conn {
  match (h.kind) {
    packet.VersionNegotiation => {
      return c.__version_negotiation(h, now);
    },
    packet.Retry => {
      return c.__retry(datagram, h, now);
    },
    packet.Initial => {
      return c.__protected(datagram, h, recovery.INITIAL, frames.IN_INITIAL, now);
    },
    packet.Handshake => {
      return c.__protected(datagram, h, recovery.HANDSHAKE, frames.IN_HANDSHAKE, now);
    },
    packet.OneRtt => {
      return c.__protected(datagram, h, recovery.APPLICATION, frames.IN_ONE_RTT, now);
    },
    packet.ZeroRtt => {
      return c.__zero_rtt(datagram, h, now);
    },
    _ => {
      return c;
    }
  }
}

// __zero_rtt opens a 0-RTT packet on a server that took the client's early
// data; any other server drops it.
function (c: Conn) __zero_rtt(datagram: [u8], h: packet.Header, now: i64): Conn {
  let Some(keys) = c.early_recv else {
    return c;
  };
  if (!c.server || !c.__ours(h)) {
    return c;
  }
  match (packet.open(datagram, h, keys, c.spaces[recovery.APPLICATION].largest_recv)) {
    Ok(o) => {
      return c.__opened(h, o, recovery.APPLICATION, frames.IN_ZERO_RTT, now);
    },
    Err(_) => {
      return c;
    }
  }
  return c;
}

function (c: Conn) __version_negotiation(h: packet.Header, now: i64): Conn {
  if (c.server || c.peer_seen || h.versions.contains(packet.VERSION_1)) {
    return c;
  }
  let why = Close { code: frames.NO_ERROR, frame_type: 0 as i64, reason: "no version in common", app: false, remote: true };
  return Conn { ...c, state: CLOSED, close: Some(why), events: c.events.append(Closed(why)) };
}

function (c: Conn) __retry(datagram: [u8], h: packet.Header, now: i64): Conn {
  if (c.server || c.peer_seen || c.retry_scid.is_some() || __same(h.scid, c.dcid) || !packet.retry_valid(datagram[h.at:h.end], c.odcid)) {
    return c;
  }
  let fresh = Conn { ...c, dcid: h.scid, retry_scid: Some(h.scid), token: h.token, recovery: c.recovery.discard(recovery.INITIAL) }.__initial_keys(h.scid);
  let s = fresh.spaces[recovery.INITIAL];
  return Conn { ...fresh, spaces: fresh.spaces.with(recovery.INITIAL, Space { ...s, crypto_sent: 0 as i64, crypto_lost: [] }) };
}

// __ours is whether a datagram names this connection: one of the IDs this
// side issued, or, on a server still in its handshake, the ID the client
// addressed it by first: the Retry's, or the one the client chose.
function (c: Conn) __ours(h: packet.Header): boolean {
  for k in c.local_cids {
    if (__same(h.dcid, k.id)) {
      return true;
    }
  }
  if (!c.server) {
    return false;
  }
  if let Some(r) = c.retry_scid {
    return __same(h.dcid, r);
  }
  return __same(h.dcid, c.odcid);
}

function (c: Conn) __protected(datagram: [u8], h: packet.Header, space: i32, kind: i32, now: i64): Conn {
  let s = c.spaces[space];
  if (s.discarded || !c.__ours(h)) {
    return c;
  }
  let Some(keys) = s.recv else {
    return c.__hold(datagram[h.at:h.end]);
  };
  if (space == recovery.APPLICATION) {
    return c.__protected_application(datagram, h, keys, now);
  }
  match (packet.open(datagram, h, keys, s.largest_recv)) {
    Ok(o) => {
      return c.__opened(h, o, space, kind, now);
    },
    Err(why) => {
      return c.__opening_failed(datagram, h, keys, why, now);
    }
  }
}

function (c: Conn) __protected_application(datagram: [u8], h: packet.Header, keys: packet.Keys, now: i64): Conn {
  let Ok(u) = packet.unprotect(datagram, h, keys, c.spaces[recovery.APPLICATION].largest_recv) else {
    return c.__maybe_reset(datagram, h, now);
  };
  let (selected, generation) = c.__receive_key(u, keys, now);
  match (packet.open_unprotected(datagram, h, u, selected)) {
    Ok(o) => {
      if (!o.reserved_ok) {
        return c.__opened(h, o, recovery.APPLICATION, frames.IN_ONE_RTT, now);
      }
      let cur = c.__key_authenticated(o, generation, now);
      if (cur.state >= CLOSING) {
        return cur;
      }
      return cur.__opened(h, o, recovery.APPLICATION, frames.IN_ONE_RTT, now);
    },
    Err(why) => {
      return c.__opening_failed(datagram, h, selected, why, now);
    }
  }
}

function (c: Conn) __opening_failed(datagram: [u8], h: packet.Header, keys: packet.Keys, why: packet.PacketError, now: i64): Conn {
  match (why) {
    packet.PacketUndecryptable => {},
    _ => {
      return c.__maybe_reset(datagram, h, now);
    }
  }
  let limit = 4503599627370496;
  match (keys.suite.aead()) {
    keyschedule.ChaCha20Poly1305 => {
      limit = 68719476736;
    },
    _ => {}
  }
  let failed = c.failed_auth;
  if (failed < limit) {
    failed += 1;
  }
  let cur = Conn { ...c, failed_auth: failed };
  if (failed >= limit) {
    return cur.__fail(frames.AEAD_LIMIT_REACHED, 0 as i64, "packet authentication limit reached", now);
  }
  return cur.__maybe_reset(datagram, h, now);
}

function (c: Conn) __hold(bytes: [u8]): Conn {
  if (c.held.len() >= MAX_UNDECRYPTED) {
    return c;
  }
  return Conn { ...c, held: c.held.append(__copy(bytes)), held_ecn: c.held_ecn.append(c.ecn_in) };
}
```

### `25 function (c: Conn) __retry_held(now: i64): Conn`

```fern
<<25 function (c: Conn) __retry_held(now: i64): Conn>>=
function (c: Conn) __retry_held(now: i64): Conn {
  if (c.held.len() == 0) {
    return c;
  }
  let held = c.held;
  let marks = c.held_ecn;
  let cur = Conn { ...c, held: [], held_ecn: [] };
  for i in 0..held.len() {
    if (cur.state < CLOSING) {
      cur = Conn { ...cur, ecn_in: marks[i] }.__datagram(held[i], now);
    }
  }
  if (cur.held.len() == held.len()) {
    return Conn { ...cur, held: held, held_ecn: marks, ecn_in: c.ecn_in };
  }
  return Conn { ...cur, ecn_in: c.ecn_in };
}

function (c: Conn) __maybe_reset(datagram: [u8], h: packet.Header, now: i64): Conn {
  let n = datagram.len();
  if (c.server || n < 21) {
    return c;
  }
  for k in c.remote_cids {
    if (k.token.len() == 16 && __same(datagram[n - 16:n], k.token)) {
      return c.__drain(Close { code: frames.NO_ERROR, frame_type: 0 as i64, reason: "stateless reset", app: false, remote: true }, now);
    }
  }
  return c;
}

function (c: Conn) __opened(h: packet.Header, o: packet.Opened, space: i32, kind: i32, now: i64): Conn {
  if (!o.reserved_ok) {
    return c.__fail(frames.PROTOCOL_VIOLATION, 0 as i64, "reserved bits set", now);
  }
  if (__has(c.spaces[space].ranges, o.pn)) {
    return c;
  }
  let cur = c.__peer_cid(h);
  if (cur.server && space == recovery.HANDSHAKE && !cur.spaces[recovery.INITIAL].discarded) {
    cur = Conn { ...cur, validated: true }.__discard(recovery.INITIAL);
  }
  if (o.payload.len() == 0) {
    return cur.__fail(frames.PROTOCOL_VIOLATION, 0 as i64, "a packet with no frames", now);
  }
  cur = Conn { ...cur, reader: cur.reader.started(o.payload), on_dcid: h.dcid }.__frames(o.payload, space, kind, now);
  let eliciting = cur.reader.elicited;
  cur = Conn { ...cur, reader: cur.reader.done() };
  if (cur.state < CLOSING) {
    cur = Conn { ...cur, idle_until: __idle(cur, now) }.__received(space, o.pn, eliciting, now);
  }
  return cur;
}

function (c: Conn) __peer_cid(h: packet.Header): Conn {
  if (c.server || c.peer_seen) {
    return c;
  }
  match (h.kind) {
    packet.OneRtt => {
      return c;
    },
    _ => {
      return Conn { ...c, dcid: h.scid, peer_seen: true };
    }
  }
}

function __idle(c: Conn, now: i64): i64 {
  let ms = c.config.params.max_idle_timeout;
  if let Some(p) = c.peer_params {
    if (p.max_idle_timeout > 0 && (ms == 0 || p.max_idle_timeout < ms)) {
      ms = p.max_idle_timeout;
    }
  }
  if (ms == 0) {
    return 0 as i64;
  }
  let floor = 3 * c.recovery.pto_ns(recovery.APPLICATION);
  let d = ms * 1000000;
  if (d < floor) {
    d = floor;
  }
  return now + d;
}

function (c: Conn) __received(space: i32, pn: i64, eliciting: boolean, now: i64): Conn {
  let s = c.spaces[space].__received(pn, eliciting, now);
  if (c.ecn_in != 0) {
    s = Space { ...s, ecn: __counted(s.ecn, c.ecn_in) };
  }
  return Conn { ...c, spaces: c.spaces.with(space, s), accepted: c.accepted + 1 };
}

// __counted adds one packet's ECN codepoint to the counts: 2 is ECT(0), 1
// is ECT(1) and 3 is ECN-CE.
function __counted(e: frames.Ecn, mark: i32): frames.Ecn {
  if (mark == 2) {
    return frames.Ecn { ...e, ect0: e.ect0 + 1 };
  }
  if (mark == 1) {
    return frames.Ecn { ...e, ect1: e.ect1 + 1 };
  }
  if (mark == 3) {
    return frames.Ecn { ...e, ce: e.ce + 1 };
  }
  return e;
}

function (s: Space) __received(pn: i64, eliciting: boolean, now: i64): Space {
  let largest = s.largest_recv;
  let at = s.largest_recv_at;
  if (pn > largest) {
    largest = pn;
    at = now;
  }
  return Space { ...s, ranges: __add_range(s.ranges, pn), largest_recv: largest, largest_recv_at: at, ack_wanted: s.ack_wanted || eliciting };
}

// __frames acts on each frame of the payload as the reader reads it.
function (c: Conn) __frames(payload: u8[], space: i32, kind: i32, now: i64): Conn {
  let cur = c;
  while (cur.state < CLOSING && cur.reader.more()) {
    cur = Conn { ...cur, reader: cur.reader.next() };
    if let Some(e) = cur.reader.failed {
      return cur.__fail(e.code, e.frame_type, e.reason, now);
    }
    if (!cur.reader.permitted(kind)) {
      return cur.__fail(frames.PROTOCOL_VIOLATION, 0 as i64, "a frame not allowed in this packet type", now);
    }
    if (kind == frames.IN_ZERO_RTT && cur.reader.is_stream() && !cur.early_stream(cur.reader.stream)) {
      cur = Conn { ...cur, early_streams: cur.early_streams.append(cur.reader.stream) };
    }
    cur = cur.__frame_read(payload, space, now);
  }
  return cur;
}

function (c: Conn) __frame_read(payload: u8[], space: i32, now: i64): Conn {
  if (c.reader.is_stream()) {
    return Conn { ...c, streams: c.streams.on_stream(c.reader.stream, c.reader.offset, payload[c.reader.from:c.reader.to], c.reader.fin) }.__streams_checked(now);
  }
  if (c.reader.is_ack()) {
    return c.__ack(c.reader.ack, space, now);
  }
  return c.__frame(c.reader.frame, space, now);
}

function (c: Conn) __streams_checked(now: i64): Conn {
  if let Some(e) = c.streams.failed {
    return c.__fail(e.code, e.frame_type, e.reason, now);
  }
  return c;
}
```

### `26 function (c: Conn) __frame(f: frames.Frame, space: i32, now: i64): Conn`

```fern
<<26 function (c: Conn) __frame(f: frames.Frame, space: i32, now: i64): Conn>>=
function (c: Conn) __frame(f: frames.Frame, space: i32, now: i64): Conn {
  match (f) {
    frames.Crypto(offset, data) => {
      return c.__crypto(space, offset, data, now);
    },
    frames.HandshakeDone => {
      return c.__handshake_done(now);
    },
    frames.ConnectionCloseFrame(cc) => {
      return c.__drain(Close { code: cc.code, frame_type: cc.frame_type, reason: cc.reason, app: cc.app, remote: true }, now);
    },
    frames.PathChallenge(data) => {
      return Conn { ...c, pending: c.pending.append(frames.PathResponse(data)) };
    },
    frames.PathResponse(data) => {
      return c.__path_answered(data);
    },
    _ => {
      return c.__frame_rest(f, now);
    }
  }
}

function (c: Conn) __frame_rest(f: frames.Frame, now: i64): Conn {
  if (streams.owns(f)) {
    return Conn { ...c, streams: c.streams.on_frame(f) }.__streams_checked(now);
  }
  match (f) {
    frames.NewToken(_) => {
      if (c.server) {
        return c.__fail(frames.PROTOCOL_VIOLATION, frames.NEW_TOKEN, "a client sent NEW_TOKEN", now);
      }
      return c;
    },
    frames.RetireConnectionId(seq) => {
      return c.__retired_by_peer(seq, now);
    },
    frames.NewConnectionIdFrame(n) => {
      return c.__cid_offered(n, now);
    },
    _ => {
      return c;
    }
  }
}

// __retired_by_peer drops a connection ID this side issued once the peer
// retires it, and issues another in its place.
function (c: Conn) __retired_by_peer(seq: i64, now: i64): Conn {
  if (seq >= c.local_next) {
    return c.__fail(frames.PROTOCOL_VIOLATION, frames.RETIRE_CONNECTION_ID, "RETIRE_CONNECTION_ID for a sequence number never issued", now);
  }
  let k = __cid_index(c.local_cids, seq);
  if (k < 0) {
    return c;
  }
  if (__same(c.local_cids[k].id, c.on_dcid)) {
    return c.__fail(frames.PROTOCOL_VIOLATION, frames.RETIRE_CONNECTION_ID, "RETIRE_CONNECTION_ID names the connection ID it arrived on", now);
  }
  return Conn { ...c, local_cids: __cids_minus(c.local_cids, seq), cids_version: c.cids_version + 1 }.__cids_offered();
}

// __cids_offered issues connection IDs until the peer holds as many as its
// active_connection_id_limit allows (RFC 9000 section 5.1.1).
function (c: Conn) __cids_offered(): Conn {
  let Some(p) = c.peer_params else {
    return c;
  };
  if (c.config.cid_len == 0 || c.state != ESTABLISHED) {
    return c;
  }
  let cur = c;
  while (cur.local_cids.len() as i64 < p.active_connection_id_limit) {
    let next = Cid { seq: cur.local_next, id: __derived(cur, 105u8, cur.local_next, cur.config.cid_len), token: __derived(cur, 116u8, cur.local_next, 16) };
    let f = frames.NewConnectionIdFrame(frames.NewConnectionId { sequence: next.seq, retire_prior_to: 0 as i64, id: next.id, reset_token: next.token });
    cur = Conn { ...cur, local_cids: cur.local_cids.append(next), local_next: cur.local_next + 1, cids_version: cur.cids_version + 1, pending: cur.pending.append(f) };
  }
  return cur;
}

// __derived is `n` bytes keyed by the connection's secret, distinct for each
// label and sequence number: a connection ID or a reset token nobody off the
// path can predict.
function __derived(c: Conn, label: u8, seq: i64, n: i32): u8[] {
  let msg: u8[] = [label];
  let shift = 56;
  while (shift >= 0) {
    msg = msg.append((seq >> shift & 255) as u8);
    shift -= 8;
  }
  return __bytes_range(crypto.hmac_sha256(c.cid_secret, msg), 0, n);
}

// __cid_offered stores a connection ID the peer issued, retires those it
// asks to retire, and refuses more than this side's limit (RFC 9000
// section 19.15).
function (c: Conn) __cid_offered(n: frames.NewConnectionId, now: i64): Conn {
  if (c.dcid.len() == 0) {
    return c.__fail(frames.PROTOCOL_VIOLATION, frames.NEW_CONNECTION_ID, "NEW_CONNECTION_ID from a peer with a zero-length connection ID", now);
  }
  let k = __cid_index(c.remote_cids, n.sequence);
  if (k >= 0) {
    if (__same(c.remote_cids[k].id, n.id) && __same(c.remote_cids[k].token, n.reset_token)) {
      return c.__retire_prior(n.retire_prior_to);
    }
    return c.__fail(frames.PROTOCOL_VIOLATION, frames.NEW_CONNECTION_ID, "NEW_CONNECTION_ID repeats a sequence number with another connection ID", now);
  }
  for known in c.remote_cids {
    if (__same(known.id, n.id)) {
      return c.__fail(frames.PROTOCOL_VIOLATION, frames.NEW_CONNECTION_ID, "NEW_CONNECTION_ID repeats a connection ID under another sequence number", now);
    }
  }
  if (n.sequence < c.retire_prior) {
    return Conn { ...c, pending: c.pending.append(frames.RetireConnectionId(n.sequence)) };
  }
  let cur = Conn { ...c, remote_cids: c.remote_cids.append(Cid { seq: n.sequence, id: n.id, token: n.reset_token }) }.__retire_prior(n.retire_prior_to);
  if (cur.remote_cids.len() as i64 > cur.config.params.active_connection_id_limit) {
    return cur.__fail(frames.CONNECTION_ID_LIMIT_ERROR, frames.NEW_CONNECTION_ID, "more connection IDs than active_connection_id_limit allows", now);
  }
  return cur;
}

// __retire_prior retires every connection ID the peer issued below `prior`,
// moving to a newer one when the ID in use is among them.
function (c: Conn) __retire_prior(prior: i64): Conn {
  if (prior <= c.retire_prior) {
    return c;
  }
  let kept: Cid[] = [];
  let pending = c.pending;
  for k in c.remote_cids {
    if (k.seq < prior) {
      pending = pending.append(frames.RetireConnectionId(k.seq));
    } else {
      kept = kept.append(k);
    }
  }
  let cur = Conn { ...c, remote_cids: kept, pending: pending, retire_prior: prior };
  if (cur.dcid_seq >= prior) {
    return cur;
  }
  let Some(next) = __cid_after(cur.remote_cids, -1i64) else {
    return cur;
  };
  return Conn { ...cur, dcid: next.id, dcid_seq: next.seq };
}

// __path_answered ends the validation of the probed path when the response
// carries the challenge's data; the path then stays in use.
function (c: Conn) __path_answered(data: u8[]): Conn {
  let Some(pr) = c.probe else {
    return c;
  };
  if (!__same(data, pr.data)) {
    return c;
  }
  return Conn { ...c, probe: None };
}

// __migrating moves to the path a client's datagram arrived on, validates it
// with a PATH_CHALLENGE and, when the peer issued a spare connection ID,
// addresses it by that one (RFC 9000 sections 9.3 and 9.5).
function (c: Conn) __migrating(path: [u8], received: i64, now: i64): Conn {
  let to = __bytes_range(path, 0, path.len());
  let data = __derived(c, 112u8, now, 8);
  let pr = Probe { path: to, data: data, until: now + 3 * c.recovery.pto_ns(recovery.APPLICATION), received: received, sent: 0 as i64, previous: c.path };
  return Conn { ...c, path: to, probe: Some(pr), pending: c.pending.append(frames.PathChallenge(data)), ecn_failed: false }.rotate_dcid();
}

function __cid_index(ids: Cid[], seq: i64): i32 {
  for i in 0..ids.len() {
    if (ids[i].seq == seq) {
      return i;
    }
  }
  return -1;
}

// __cid_after is the connection ID with the lowest sequence number above
// `seq`.
function __cid_after(ids: Cid[], seq: i64): Option[Cid] {
  let best: Option[Cid] = None;
  for k in ids {
    if (k.seq > seq) {
      match (best) {
        Some(b) => {
          if (k.seq < b.seq) {
            best = Some(k);
          }
        },
        None => {
          best = Some(k);
        }
      }
    }
  }
  return best;
}

function __cids_minus(ids: Cid[], seq: i64): Cid[] {
  let out: Cid[] = [];
  for k in ids {
    if (k.seq != seq) {
      out = out.append(k);
    }
  }
  return out;
}

function (c: Conn) __ack(a: frames.Ack, space: i32, now: i64): Conn {
  let exponent = 3;
  if let Some(p) = c.peer_params {
    exponent = p.ack_delay_exponent;
  }
  let delay = (a.delay << exponent) * 1000;
  let before = c.recovery.largest_acked(space);
  let cur = Conn { ...c, recovery: c.recovery.on_ack(space, a, time.duration_nanos(delay), time.instant_from_nanos(now), c.spaces[space].next_pn - 1) };
  let refused = cur.recovery.refused;
  if (refused != 0) {
    return Conn { ...cur, recovery: cur.recovery.outcome_taken() }.__fail(refused, frames.ACK, "an ACK of a packet never sent", now);
  }
  let acked = cur.recovery.acked;
  if (a.ranges[0].high > before) {
    cur = cur.__ecn_validated(space, a, acked);
  }
  let lost = cur.recovery.lost;
  cur = Conn { ...cur, recovery: cur.recovery.outcome_taken() };
  if (!cur.server && space == recovery.HANDSHAKE) {
    cur = Conn { ...cur, recovery: cur.recovery.validated() };
  }
  return cur.__delivered(acked).__requeue(lost);
}

// __ecn_validated checks the peer's ECN counts on an ACK that raises the
// largest acknowledged (RFC 9000 section 13.4.2.1). Marking stops when the
// ACK acknowledges marked packets without counts, when a count falls, or
// when the counts rose by less than the marked packets it acknowledges.
function (c: Conn) __ecn_validated(space: i32, a: frames.Ack, acked: recovery.Sent[]): Conn {
  let marked = 0 as i64;
  for p in acked {
    if (p.ect) {
      marked += 1;
    }
  }
  let s = c.spaces[space];
  let Some(e) = a.ecn else {
    if (marked > 0) {
      return Conn { ...c, ecn_failed: true };
    }
    return c;
  };
  let was = s.peer_ecn;
  let fell = e.ect0 < was.ect0 || e.ect1 < was.ect1 || e.ce < was.ce;
  if (fell) {
    return Conn { ...c, ecn_failed: true };
  }
  let failed = c.ecn_failed || e.ect0 - was.ect0 + e.ce - was.ce < marked;
  return Conn { ...c, spaces: c.spaces.with(space, Space { ...s, peer_ecn: e }), ecn_failed: failed };
}

// __marking is whether the next packet leaves marked ECT(0).
function (c: Conn) __marking(): boolean {
  return c.config.ecn && !c.ecn_failed;
}

// ecn_counts is the marks counted on the packets received in the space.
pub function (c: Conn) ecn_counts(space: i32): frames.Ecn {
  return c.spaces[space].ecn;
}

// ecn_codepoint is the ECN codepoint for the datagrams transmit built: 2
// for ECT(0), or 0 when the transport carries no marks or validation failed.
pub function (c: Conn) ecn_codepoint(): i32 {
  if (c.__marking()) {
    return 2;
  }
  return 0;
}

function (c: Conn) __delivered(acked: recovery.Sent[]): Conn {
  let t = c.streams;
  for p in acked {
    for item in p.resend {
      t = t.acked(item);
    }
  }
  return Conn { ...c, streams: t };
}

function (c: Conn) __requeue(lost: recovery.Sent[]): Conn {
  let cur = c;
  for p in lost {
    cur = cur.__resend(p.resend);
  }
  return cur;
}

function (c: Conn) __resend(items: recovery.Resend[]): Conn {
  let cur = c;
  for item in items {
    match (item) {
      recovery.CryptoRange(offset, len) => {
        cur = cur.__crypto_lost(offset, len);
      },
      recovery.StreamRange(_, _, _, _) => {
        cur = Conn { ...cur, streams: cur.streams.lost(item) };
      },
      recovery.Again(f) => {
        cur = cur.__again(f);
      }
    }
  }
  return cur;
}

function (c: Conn) __crypto_lost(offset: i64, len: i32): Conn {
  let space = __crypto_space(c, offset);
  if (space < 0) {
    return c;
  }
  let s = c.spaces[space];
  return Conn { ...c, spaces: c.spaces.with(space, Space { ...s, crypto_lost: s.crypto_lost.append(offset).append(len as i64) }) };
}

function (c: Conn) __again(f: frames.Frame): Conn {
  if (streams.owns(f)) {
    return Conn { ...c, streams: c.streams.lost(recovery.Again(f)) };
  }
  match (f) {
    frames.HandshakeDone => {
      return Conn { ...c, handshake_done_pending: true };
    },
    _ => {
      return Conn { ...c, pending: c.pending.append(f) };
    }
  }
}

function (c: Conn) __handshake_done(now: i64): Conn {
  if (c.server) {
    return c.__fail(frames.PROTOCOL_VIOLATION, frames.HANDSHAKE_DONE, "a client sent HANDSHAKE_DONE", now);
  }
  return Conn { ...c, recovery: c.recovery.confirmed() }.__discard(recovery.HANDSHAKE);
}
```

### `27 function (c: Conn) __discard(space: i32): Conn`

```fern
<<27 function (c: Conn) __discard(space: i32): Conn>>=
function (c: Conn) __discard(space: i32): Conn {
  let s = c.spaces[space];
  if (s.discarded) {
    return c;
  }
  return Conn { ...c, spaces: c.spaces.with(space, Space { ...__space(), discarded: true, next_pn: s.next_pn }), recovery: c.recovery.discard(space) };
}

function (c: Conn) __crypto(space: i32, offset: i64, data: u8[], now: i64): Conn {
  let s = c.spaces[space];
  if (offset + data.len() as i64 - s.crypto_in.delivered() > MAX_CRYPTO_HELD) {
    return c.__fail(frames.CRYPTO_BUFFER_EXCEEDED, frames.CRYPTO, "CRYPTO data past what this side holds out of order", now);
  }
  let (r, bytes) = s.crypto_in.add(offset, data).take();
  let cur = Conn { ...c, spaces: c.spaces.with(space, Space { ...s, crypto_in: r }) };
  if (bytes.len() == 0) {
    return cur;
  }
  return cur.__tls_read(space, bytes, now);
}

function (c: Conn) __tls_read(level: i32, bytes: u8[], now: i64): Conn {
  match (c.tls) {
    TlsClient(t) => {
      return c.__client_step(t.read_crypto(level, bytes), now);
    },
    TlsServer(t) => {
      return c.__server_step(t.read_crypto(level, bytes), now);
    },
    TlsNone => {
      return c;
    }
  }
}

function (c: Conn) __client_step(r: Result[handshake.ClientStep, handshake.HandshakeError], now: i64): Conn {
  match (r) {
    Ok(step) => {
      return Conn { ...c, tls: TlsClient(step.client) }.__tls_events(step.events, now);
    },
    Err(e) => {
      return c.__fail(frames.CRYPTO_ERROR + e.alert() as i64, frames.CRYPTO, e.message(), now);
    }
  }
}

function (c: Conn) __server_step(r: Result[handshake.ServerStep, handshake.HandshakeError], now: i64): Conn {
  match (r) {
    Ok(step) => {
      return Conn { ...c, tls: TlsServer(step.server) }.__tls_events(step.events, now);
    },
    Err(e) => {
      return c.__fail(frames.CRYPTO_ERROR + e.alert() as i64, frames.CRYPTO, e.message(), now);
    }
  }
}

function (c: Conn) __tls_events(events: handshake.Event[], now: i64): Conn {
  let cur = c;
  for e in events {
    if (cur.state >= CLOSING) {
      return cur;
    }
    cur = cur.__tls_event(e, now);
  }
  return cur;
}

function (c: Conn) __tls_event(e: handshake.Event, now: i64): Conn {
  match (e) {
    handshake.Crypto(f) => {
      let s = c.spaces[f.level];
      return Conn { ...c, spaces: c.spaces.with(f.level, Space { ...s, crypto_out: s.crypto_out.concat(f.bytes) }) };
    },
    handshake.Secret(t) => {
      return c.__secret(t);
    },
    handshake.Connected => {
      return c.__connected(now).__early_settled().__ticket_issued(now);
    },
    _ => {
      return c.__tls_question(e, now);
    }
  }
}

// __early_settled resends in 1-RTT what a client sent in 0-RTT when the
// server rejected its early data (RFC 9001 section 4.6.2).
function (c: Conn) __early_settled(): Conn {
  if (c.server || c.early_state() != handshake.EARLY_REJECTED) {
    return c;
  }
  let cur = c;
  for sent in c.recovery.unacked(recovery.APPLICATION) {
    cur = cur.__resend(sent.resend);
  }
  return Conn { ...cur, recovery: cur.recovery.discard(recovery.APPLICATION) };
}

// early_state is a client's handshake.EARLY_* state, and on a server
// EARLY_ACCEPTED when it took the client's early data.
pub function (c: Conn) early_state(): i32 {
  match (c.tls) {
    TlsClient(t) => {
      return t.early_state();
    },
    TlsServer(t) => {
      if (t.early_accepted()) {
        return handshake.EARLY_ACCEPTED;
      }
      return handshake.EARLY_NONE;
    },
    TlsNone => {
      return handshake.EARLY_NONE;
    }
  }
  return handshake.EARLY_NONE;
}

// __ticket_issued sends a server's session ticket once the handshake is
// done, when the config has a ticket key.
function (c: Conn) __ticket_issued(now: i64): Conn {
  if (!c.server || c.config.ticket_key.len() == 0 || c.state >= CLOSING) {
    return c;
  }
  match (c.tls) {
    TlsServer(t) => {
      return c.__server_step(t.ticket(tlsserver.issue_ticket(c.config.ticket_key, c.config.ticket_lifetime, t, c.ticket_entropy, c.__wall_ms(now), c.config.early_data)), now);
    },
    _ => {
      return c;
    }
  }
  return c;
}

function (c: Conn) __wall_ms(now: i64): i64 {
  return (now + c.wall_offset) / 1000000;
}

function (c: Conn) __secret(t: handshake.TrafficSecret): Conn {
  if (t.level == handshake.EARLY_LEVEL) {
    return c.__early_secret(t);
  }
  let k = packet.keys(__suite(c), t.secret);
  let s = c.spaces[t.level];
  if (t.write) {
    s = Space { ...s, send: Some(k) };
  } else {
    s = Space { ...s, recv: Some(k) };
  }
  let cur = Conn { ...c, spaces: c.spaces.with(t.level, s) };
  if (t.level == recovery.APPLICATION) {
    if (t.write) {
      cur = Conn { ...cur, key_next_send: Some(k.updated()), zero_rtt: false };
    } else {
      cur = Conn { ...cur, key_next_recv: Some(k.updated()) };
    }
  }
  return cur;
}

// __early_secret takes the 0-RTT keys. A client sends its application
// packets as 0-RTT under the limits its ticket remembered, until the 1-RTT
// keys replace them. A server opens 0-RTT packets, and may answer them in
// 1-RTT before the handshake completes, under the client's limits.
function (c: Conn) __early_secret(t: handshake.TrafficSecret): Conn {
  if (t.write) {
    let Some(ticket) = c.config.resume else {
      return c;
    };
    let suite = keyschedule.cipher_suite(ticket.tls.suite).unwrap_or(__suite(c));
    let s = c.spaces[recovery.APPLICATION];
    return Conn { ...c, spaces: c.spaces.with(recovery.APPLICATION, Space { ...s, send: Some(packet.keys(suite, t.secret)) }), zero_rtt: true, streams: c.streams.with_peer(ticket.params) };
  }
  match (c.__peer_transport_params()) {
    Ok(p) => {
      return Conn { ...c, early_recv: Some(packet.keys(__suite(c), t.secret)), streams: c.streams.with_peer(p) };
    },
    Err(_) => {
      return c;
    }
  }
}

function __suite(c: Conn): keyschedule.CipherSuite {
  match (c.tls) {
    TlsClient(t) => {
      return t.cipher_suite();
    },
    TlsServer(t) => {
      return t.cipher_suite();
    },
    TlsNone => {
      return keyschedule.Aes128GcmSha256;
    }
  }
}
```

### `28 function (c: Conn) __tls_question(e: handshake.Event, now: i64): Conn`

```fern
<<28 function (c: Conn) __tls_question(e: handshake.Event, now: i64): Conn>>=
function (c: Conn) __tls_question(e: handshake.Event, now: i64): Conn {
  match (c.tls) {
    TlsClient(t) => {
      match (e) {
        handshake.VerifyServer(v) => {
          return c.__client_step(t.verdict(__trusted(c.config, v, (now + c.wall_offset) / 1000000000)), now);
        },
        handshake.Ticket(nst) => {
          match (tlsclient.ticket_from(t, nst, c.config.server_name, time.timestamp_from_unix_millis(c.__wall_ms(now)))) {
            Some(kept) => {
              let Some(p) = c.peer_params else {
                return c;
              };
              return Conn { ...c, tickets: c.tickets.append(Ticket { tls: kept, params: p }) };
            },
            None => {
              return c;
            }
          }
        },
        _ => {
          return c;
        }
      }
    },
    TlsServer(t) => {
      return c.__server_question(t, e, now);
    },
    TlsNone => {
      return c;
    }
  }
}

function (c: Conn) __server_question(t: handshake.Server, e: handshake.Event, now: i64): Conn {
  match (e) {
    handshake.SignNeeded(sg) => {
      let Some(id) = c.config.identity else {
        return c.__fail(frames.INTERNAL_ERROR, frames.CRYPTO, "no identity to sign with", now);
      };
      match (id.key.sign(sg.scheme, sg.signed)) {
        Ok(sig) => {
          return c.__server_step(t.signature(sig), now);
        },
        Err(err) => {
          return c.__fail(frames.INTERNAL_ERROR, frames.CRYPTO, err.message(), now);
        }
      }
    },
    handshake.ResumeOffered(offer) => {
      let found: Option[handshake.Resumed] = None;
      if (c.config.ticket_key.len() > 0) {
        found = tlsserver.lookup_ticket(c.config.ticket_key, offer, c.__wall_ms(now));
      }
      return c.__server_step(t.resume(found), now);
    },
    handshake.VerifyClient(_) => {
      return c.__server_step(t.verdict(true), now);
    },
    _ => {
      return c;
    }
  }
}

function __trusted(cfg: Config, v: handshake.Verification, unix_sec: i64): boolean {
  match (verify.server(v.chain, cfg.roots, cfg.server_name, time.timestamp_from_unix(unix_sec))) {
    Ok(leaf) => {
      return verify.signed(leaf, v.scheme, v.signed, v.signature);
    },
    Err(_) => {
      return false;
    }
  }
}

function (c: Conn) __connected(now: i64): Conn {
  match (c.__peer_transport_params()) {
    Ok(p) => {
      let token: u8[] = [];
      if let Some(t) = p.stateless_reset_token {
        token = t;
      }
      let cur = Conn { ...c, state: ESTABLISHED, peer_params: Some(p), streams: c.streams.with_peer(p), recovery: c.recovery.with_max_ack_delay(time.duration_millis(p.max_ack_delay)), events: c.events.append(Event.Connected), remote_cids: [Cid { seq: 0 as i64, id: c.dcid, token: token }] };
      if (cur.server) {
        cur = Conn { ...cur, handshake_done_pending: true, recovery: cur.recovery.confirmed() }.__discard(recovery.HANDSHAKE);
      }
      return Conn { ...cur, idle_until: __idle(cur, now) }.__cids_offered();
    },
    Err(e) => {
      return c.__fail(e.code, 0 as i64, e.reason, now);
    }
  }
}

function (c: Conn) __peer_extensions(): message.Extension[] {
  match (c.tls) {
    TlsClient(t) => {
      return t.peer_extensions();
    },
    TlsServer(t) => {
      return t.peer_extensions();
    },
    TlsNone => {
      return [];
    }
  }
}

function (c: Conn) __peer_transport_params(): Result[params.Params, frames.FrameError] {
  let Some(raw) = message.find(c.__peer_extensions(), message.EXT_QUIC_TRANSPORT_PARAMETERS) else {
    return Err(__param_error("no transport parameters"));
  };
  let p = params.decode(raw, !c.server)?;
  if (!__is(p.initial_source_connection_id, c.dcid)) {
    return Err(__param_error("initial_source_connection_id is not the connection ID the peer used"));
  }
  if (c.server) {
    return Ok(p);
  }
  if (!__is(p.original_destination_connection_id, c.odcid)) {
    return Err(__param_error("original_destination_connection_id is not the connection ID this client chose"));
  }
  if (!__same_option(p.retry_source_connection_id, c.retry_scid)) {
    return Err(__param_error("retry_source_connection_id does not match the Retry"));
  }
  return Ok(p);
}

function __param_error(reason: string): frames.FrameError {
  return frames.FrameError { code: frames.TRANSPORT_PARAMETER_ERROR, frame_type: 0 as i64, reason: reason };
}
```

### `29 pub function (c: Conn) transmit(now: Instant): Conn`

```fern
<<29 pub function (c: Conn) transmit(now: Instant): Conn>>=
pub function (c: Conn) transmit(now: Instant): Conn {
  return c.__transmit(time.instant_nanos(now));
}

function (c: Conn) __transmit(now: i64): Conn {
  let cur = c;
  let guard = 0;
  while (guard < 64) {
    let before = cur.out.len();
    cur = cur.__datagram_sent(now);
    if (cur.out.len() == before) {
      return cur;
    }
    guard += 1;
  }
  return cur;
}
```

### `30 function (c: Conn) __datagram_sent(now: i64): Conn`

```fern
<<30 function (c: Conn) __datagram_sent(now: i64): Conn>>=
function (c: Conn) __datagram_sent(now: i64): Conn {
  if (c.validated && c.probe.is_none()) {
    return c.__datagram_out(now);
  }
  let next = c.__datagram_out(now);
  if (!c.validated && next.sent_bytes > 3 * next.received_bytes) {
    return c;
  }
  let Some(pr) = c.probe else {
    return next;
  };
  let added = next.sent_bytes - c.sent_bytes;
  if (pr.sent + added > 3 * pr.received) {
    return c;
  }
  return Conn { ...next, probe: Some(Probe { ...pr, sent: pr.sent + added }) };
}
```

### `31 struct Plan`

```fern
<<31 struct Plan>>=
// A plan is one space's part of the next datagram, built in the space's
// slot in `plans`. `sized` is whether its payload's buffer has the
// datagram's room; the slot keeps that buffer for the next datagram.
struct Plan { space: i32, payload: u8[], eliciting: boolean, resend: recovery.Resend[], sized: boolean, challenge: boolean }
```

### `32 function __no_plan(space: i32): Plan`

```fern
<<32 function __no_plan(space: i32): Plan>>=
function __no_plan(space: i32): Plan {
  return Plan { space: space, payload: [], eliciting: false, resend: [], sized: false, challenge: false };
}

// __emptied is a sealed plan made ready for the next datagram, its buffer kept.
function __emptied(p: Plan): Plan {
  unsafe {
    __arr_set_len(p.payload, 0);
    return Plan { ...p, eliciting: false, resend: [], challenge: false };
  }
}

function (c: Conn) __datagram_out(now: i64): Conn {
  return c.__aead_guard(now).__datagram_guarded(now);
}

function __seal_limit(keys: packet.Keys): i64 {
  match (keys.suite.aead()) {
    keyschedule.ChaCha20Poly1305 => {
      return 4611686018427387904;
    },
    _ => {}
  }
  return 8388608 as i64;
}

function (c: Conn) __aead_guard(now: i64): Conn {
  if (c.state >= CLOSING) {
    return c;
  }
  let cur = c;
  let space = 0;
  while (space < 3) {
    let s = cur.spaces[space];
    if let Some(keys) = s.send {
      let used = s.next_pn;
      if (space == recovery.APPLICATION) {
        used = cur.key_sent;
      }
      if (s.next_pn >= frames.MAX_VARINT || used >= __seal_limit(keys) - 1) {
        if (space == recovery.APPLICATION && s.next_pn < frames.MAX_VARINT) {
          if let Ok(updated) = cur.__update_keys(now) {
            cur = updated;
          } else {
            return cur.__fail(frames.AEAD_LIMIT_REACHED, 0 as i64, "packet encryption limit reached", now);
          }
        } else {
          return cur.__fail(frames.AEAD_LIMIT_REACHED, 0 as i64, "packet encryption limit reached", now);
        }
      }
    }
    space += 1;
  }
  return cur;
}

function (c: Conn) __datagram_guarded(now: i64): Conn {
  if (c.state == DRAINING || c.state == CLOSED) {
    return c;
  }
  if (c.state == CLOSING) {
    return c.__close_datagram();
  }
  let cur = c;
  let planned = 0;
  let room = c.config.max_datagram;
  for space in 0..3 {
    cur = cur.__plan(space, room - 64, now);
    let n = cur.plans[space].payload.len();
    if (n > 0) {
      room = room - n - 64;
      planned |= 1 << space;
    }
  }
  if (planned == 0) {
    return cur;
  }
  return cur.__seal(planned, now);
}

function (c: Conn) __plan(space: i32, room: i32, now: i64): Conn {
  let s = c.spaces[space];
  if (s.discarded || s.send.is_none() || room < 40) {
    return c;
  }
  let cur = c;
  if (s.ack_wanted) {
    cur = cur.__ack_planned(space, now);
  }
  let window = cur.recovery.window() >= cur.config.max_datagram as i64 || cur.probes.contains(space);
  if (!window) {
    return cur.__acked_out(space);
  }
  return cur.__crypto_out(space, room).__control_out(space, room).__streams_out(space, room).__probe_out(space).__acked_out(space);
}

function (c: Conn) __planned(space: i32, f: frames.Frame, again: boolean): Conn {
  let cur = c.__pushed(space, f);
  if let frames.PathChallenge(_) = f {
    cur = Conn { ...cur, plans: cur.plans.with(space, Plan { ...cur.plans[space], challenge: true }) };
  }
  if (!again) {
    return cur;
  }
  return Conn { ...cur, plans: cur.plans.with(space, cur.plans[space].__eliciting(recovery.Again(f))) };
}

function (p: Plan) __elicits(): Plan {
  return Plan { ...p, eliciting: true };
}

function (p: Plan) __eliciting(item: recovery.Resend): Plan {
  return Plan { ...p, eliciting: true, resend: p.resend.append(item) };
}

function (c: Conn) __pushed(space: i32, f: frames.Frame): Conn {
  return Conn { ...c, plans: c.plans.with(space, c.plans[space].__put(f, c.config.max_datagram)) };
}

function (p: Plan) __put(f: frames.Frame, room: i32): Plan {
  let q = p.__started(room);
  let payload = q.payload;
  q = Plan { ...q, payload: [] };
  payload = frames.put(payload, f);
  return Plan { ...q, payload: payload };
}

function (c: Conn) __ack_planned(space: i32, now: i64): Conn {
  let s = c.spaces[space];
  let delay = __ack_delay(s, space, now);
  return Conn { ...c, plans: c.plans.with(space, c.plans[space].__ack_put(delay, s.ranges, s.ecn, c.config.max_datagram)) };
}

// __ack_put writes an ACK, with the ECN counts once any packet of the
// space arrived marked.
function (p: Plan) __ack_put(delay: i64, ranges: frames.AckRange[], marks: frames.Ecn, room: i32): Plan {
  let q = p.__started(room);
  let payload = q.payload;
  q = Plan { ...q, payload: [] };
  if (marks.ect0 + marks.ect1 + marks.ce > 0) {
    payload = frames.put_ack_ecn(payload, delay, ranges, marks);
  } else {
    payload = frames.put_ack(payload, delay, ranges);
  }
  return Plan { ...q, payload: payload };
}

// __started gives a plan's first frame a buffer the whole datagram fits in.
function (p: Plan) __started(room: i32): Plan {
  unsafe {
    if (p.payload.len() > 0 || p.sized) {
      return p;
    }
    let out = __alloc_u8(room);
    __arr_set_len(out, 0);
    return Plan { ...p, payload: out, sized: true };
  }
}

function (c: Conn) __acked_out(space: i32): Conn {
  if (c.plans[space].payload.len() == 0) {
    return c;
  }
  let unacked = c.key_unacked;
  if (space == recovery.APPLICATION && c.spaces[space].ack_wanted) {
    unacked = false;
  }
  return Conn { ...c, key_unacked: unacked, spaces: c.spaces.with(space, Space { ...c.spaces[space], ack_wanted: false }) };
}

function __ack_delay(s: Space, space: i32, now: i64): i64 {
  if (space == recovery.APPLICATION) {
    return (now - s.largest_recv_at) / 1000 >> 3;
  }
  return 0 as i64;
}
```

### `33 function (c: Conn) __crypto_out(space: i32, room: i32): Conn`

```fern
<<33 function (c: Conn) __crypto_out(space: i32, room: i32): Conn>>=
function (c: Conn) __crypto_out(space: i32, room: i32): Conn {
  if (c.spaces[space].crypto_lost.len() == 0 && c.spaces[space].crypto_out.len() as i64 == c.spaces[space].crypto_sent) {
    return c;
  }
  let s = c.spaces[space];
  let cur = c;
  let lost = s.crypto_lost;
  let kept: i64[] = [];
  let i = 0;
  while (i + 1 < lost.len()) {
    let n = __crypto_fit(cur.plans[space].payload.len(), lost[i], lost[i + 1] as i32, room);
    cur = cur.__crypto_planned(space, lost[i], n);
    let rest = lost[i + 1] as i32 - n;
    if (rest > 0) {
      kept = kept.append(lost[i] + (lost[i + 1] - rest as i64)).append(rest as i64);
    }
    i += 2;
  }
  let unsent = (s.crypto_out.len() as i64 - s.crypto_sent) as i32;
  let sent = s.crypto_sent;
  if (unsent > 0) {
    let n = __crypto_fit(cur.plans[space].payload.len(), s.crypto_sent, unsent, room);
    cur = cur.__crypto_planned(space, s.crypto_sent, n);
    sent = s.crypto_sent + n as i64;
  }
  return Conn { ...cur, spaces: cur.spaces.with(space, Space { ...cur.spaces[space], crypto_lost: kept, crypto_sent: sent }) };
}

function __crypto_fit(used: i32, offset: i64, len: i32, room: i32): i32 {
  let free = room - used - 1 - frames.varint_len(offset) - 2;
  if (free <= 0) {
    return 0;
  }
  if (len > free) {
    return free;
  }
  return len;
}

function (c: Conn) __crypto_planned(space: i32, offset: i64, n: i32): Conn {
  if (n <= 0) {
    return c;
  }
  let at = offset as i32;
  let cur = c.__pushed(space, frames.Crypto(offset, __copy(c.spaces[space].crypto_out[at:at + n])));
  return Conn { ...cur, plans: cur.plans.with(space, cur.plans[space].__eliciting(recovery.CryptoRange(offset, n))) };
}

function (c: Conn) __control_out(space: i32, room: i32): Conn {
  if (space != recovery.APPLICATION) {
    return c;
  }
  let cur = c;
  if (cur.handshake_done_pending) {
    cur = Conn { ...cur, handshake_done_pending: false }.__planned(space, frames.HandshakeDone, true);
  }
  if (cur.pending.len() == 0) {
    return cur;
  }
  let pending = cur.pending;
  cur = Conn { ...cur, pending: [] };
  let kept: frames.Frame[] = [];
  for f in pending {
    if (cur.plans[space].payload.len() + frames.encode(f).len() <= room) {
      cur = cur.__planned(space, f, true);
    } else {
      kept = kept.append(f);
    }
  }
  return Conn { ...cur, pending: kept };
}

function (c: Conn) __streams_out(space: i32, room: i32): Conn {
  if (space != recovery.APPLICATION || !c.__app_ready()) {
    return c;
  }
  let before = c.plans[space].payload.len();
  let payload = c.plans[space].payload;
  let cur = Conn { ...c, plans: c.plans.with(space, c.plans[space].__unfilled()) };
  cur = Conn { ...cur, streams: cur.streams.fill(payload, room) };
  return Conn { ...cur, plans: cur.plans.with(space, cur.plans[space].__filled(cur.streams.filled, cur.streams.resend, before)), streams: cur.streams.fill_taken() };
}

function (p: Plan) __unfilled(): Plan {
  return Plan { ...p, payload: [] };
}

function (p: Plan) __filled(payload: u8[], resend: recovery.Resend[], before: i32): Plan {
  if (p.resend.len() == 0) {
    return Plan { ...p, payload: payload, eliciting: p.eliciting || payload.len() > before, resend: resend };
  }
  return Plan { ...p, payload: payload, eliciting: p.eliciting || payload.len() > before, resend: p.resend.concat(resend) };
}

function (c: Conn) __probe_out(space: i32): Conn {
  if (!c.probes.contains(space)) {
    return c;
  }
  let cur = Conn { ...c, probes: __without(c.probes, space) };
  if (cur.plans[space].eliciting) {
    return cur;
  }
  for sent in cur.recovery.unacked(space) {
    for item in sent.resend {
      if let recovery.CryptoRange(offset, len) = item {
        let n = __crypto_fit(cur.plans[space].payload.len(), offset, len, cur.config.max_datagram - 64);
        if (n > 0) {
          return cur.__crypto_planned(space, offset, n);
        }
      }
    }
  }
  cur = cur.__pushed(space, frames.Ping);
  return Conn { ...cur, plans: cur.plans.with(space, cur.plans[space].__elicits()) };
}
```

### `34 function (c: Conn) __seal(planned: i32, now: i64): Conn`

```fern
<<34 function (c: Conn) __seal(planned: i32, now: i64): Conn>>=
// __seal seals the plans of the spaces set in `planned` into one datagram.
function (c: Conn) __seal(planned: i32, now: i64): Conn {
  let pad = __planned_in(planned, recovery.INITIAL) && (!c.server || c.plans[recovery.INITIAL].eliciting) || c.__challenge_padded(planned);
  let cur = c;
  let out: u8[] = [];
  for space in 0..3 {
    if (__planned_in(planned, space)) {
      let plan = cur.plans[space];
      let extra = 0;
      let bytes = cur.__sealed(plan, 0);
      if (pad && planned >> space + 1 == 0 && out.len() + bytes.len() < packet.MIN_INITIAL_DATAGRAM) {
        extra = packet.MIN_INITIAL_DATAGRAM - out.len() - bytes.len();
        bytes = cur.__sealed(plan, extra);
      }
      if (bytes.len() > 0) {
        cur = cur.__recorded(plan, now, bytes.len(), extra > 0);
      }
      if (out.len() == 0) {
        out = bytes;
      } else {
        out = out.concat(bytes);
      }
      cur = Conn { ...cur, plans: cur.plans.with(space, __emptied(cur.plans[space])) };
    }
  }
  if (!cur.server && __planned_in(planned, recovery.HANDSHAKE)) {
    cur = cur.__discard(recovery.INITIAL);
  }
  if (out.len() == 0) {
    return cur;
  }
  return Conn { ...cur, out: cur.out.append(out), sent_bytes: cur.sent_bytes + out.len() as i64 };
}

function __planned_in(planned: i32, space: i32): boolean {
  return (planned >> space & 1) != 0;
}

// __challenge_padded says whether a datagram carrying a PATH_CHALLENGE is
// expanded to the minimum datagram size (RFC 9000 section 8.2.1): yes, when
// the probed path's amplification budget has room for it.
function (c: Conn) __challenge_padded(planned: i32): boolean {
  if (!__planned_in(planned, recovery.APPLICATION) || !c.plans[recovery.APPLICATION].challenge) {
    return false;
  }
  let Some(pr) = c.probe else {
    return true;
  };
  return 3 * pr.received - pr.sent >= packet.MIN_INITIAL_DATAGRAM as i64;
}

function (c: Conn) __sealed(p: Plan, extra: i32): u8[] {
  let s = c.spaces[p.space];
  let Some(keys) = s.send else {
    return [];
  };
  let used = s.next_pn;
  if (p.space == recovery.APPLICATION) {
    used = c.key_sent;
  }
  if (used >= __seal_limit(keys) || s.next_pn > frames.MAX_VARINT) {
    return [];
  }
  let payload = p.payload;
  if (extra > 0) {
    payload = frames.put(payload, frames.Padding(extra));
  }
  let n = packet.pn_len(s.next_pn, -1i64);
  if (p.space == recovery.APPLICATION && c.zero_rtt) {
    return packet.seal_long(packet.Long { kind: packet.ZeroRtt, dcid: c.dcid, scid: c.scid, token: [], pn: s.next_pn, pn_len: n }, payload, keys);
  }
  if (p.space == recovery.APPLICATION) {
    return packet.seal_short(c.dcid, s.next_pn, n, c.key_phase, false, payload, keys);
  }
  let kind = packet.Initial;
  if (p.space == recovery.HANDSHAKE) {
    kind = packet.Handshake;
  }
  let token: u8[] = [];
  if (p.space == recovery.INITIAL) {
    token = c.token;
  }
  return packet.seal_long(packet.Long { kind: kind, dcid: c.dcid, scid: c.scid, token: token, pn: s.next_pn, pn_len: n }, payload, keys);
}

function (c: Conn) __recorded(p: Plan, now: i64, size: i32, padded: boolean): Conn {
  let pn = c.spaces[p.space].next_pn;
  let sent = recovery.Sent { pn: pn, at: time.instant_from_nanos(now), size: size, ack_eliciting: p.eliciting, in_flight: p.eliciting || padded, ect: c.__marking(), resend: p.resend };
  let sent_count = c.key_sent;
  if (p.space == recovery.APPLICATION) {
    sent_count += 1;
  }
  let cur = Conn { ...c, key_sent: sent_count, recovery: c.recovery.on_sent(p.space, sent) };
  return Conn { ...cur, spaces: cur.spaces.with(p.space, Space { ...cur.spaces[p.space], next_pn: pn + 1 }) };
}

function (c: Conn) __close_datagram(): Conn {
  if (!c.close_pending) {
    return c;
  }
  let Some(why) = c.close else {
    return c;
  };
  let cur = Conn { ...c, close_pending: false };
  let planned = 0;
  for space in 0..3 {
    let s = c.spaces[space];
    if (!s.discarded && s.send.is_some()) {
      planned |= 1 << space;
      cur = Conn { ...cur, plans: cur.plans.with(space, Plan { space: space, payload: frames.encode(frames.ConnectionCloseFrame(__close_frame(why, space))), eliciting: false, resend: [], sized: false, challenge: false }) };
    }
  }
  return cur.__seal(planned, 0 as i64);
}

function __close_frame(why: Close, space: i32): frames.ConnectionClose {
  if (why.app && space != recovery.APPLICATION) {
    return frames.ConnectionClose { code: frames.APPLICATION_ERROR, frame_type: 0 as i64, reason: "", app: false };
  }
  return frames.ConnectionClose { code: why.code, frame_type: why.frame_type, reason: why.reason, app: why.app };
}
```

### `35 function __add_range(ranges: frames.AckRange[], pn: i64): frames.AckRange[]`

```fern
<<35 function __add_range(ranges: frames.AckRange[], pn: i64): frames.AckRange[]>>=
function __add_range(ranges: frames.AckRange[], pn: i64): frames.AckRange[] {
  if (ranges.len() > 0 && pn == ranges[0].high + 1) {
    return ranges.with(0, __raised(ranges[0], pn));
  }
  let out: frames.AckRange[] = [];
  let placed = false;
  for r in ranges {
    if (!placed && pn > r.high + 1) {
      out = __push_range(out, frames.AckRange { low: pn, high: pn });
      placed = true;
    }
    if (!placed && pn >= r.low - 1 && pn <= r.high + 1) {
      out = __push_range(out, frames.AckRange { low: __min(r.low, pn), high: __max(r.high, pn) });
      placed = true;
    } else {
      out = __push_range(out, r);
    }
  }
  if (!placed) {
    out = __push_range(out, frames.AckRange { low: pn, high: pn });
  }
  if (out.len() <= MAX_ACK_RANGES) {
    return out;
  }
  let kept: frames.AckRange[] = [];
  let i = 0;
  while (i < MAX_ACK_RANGES) {
    kept = kept.append(out[i]);
    i += 1;
  }
  return kept;
}

function __raised(r: frames.AckRange, pn: i64): frames.AckRange {
  return frames.AckRange { ...r, high: pn };
}

function __push_range(out: frames.AckRange[], r: frames.AckRange): frames.AckRange[] {
  if (out.len() > 0) {
    let last = out[out.len() - 1];
    if (r.high + 1 >= last.low) {
      return out.with(out.len() - 1, frames.AckRange { low: __min(last.low, r.low), high: __max(last.high, r.high) });
    }
  }
  return out.append(r);
}

function __has(ranges: frames.AckRange[], pn: i64): boolean {
  for r in ranges {
    if (pn >= r.low && pn <= r.high) {
      return true;
    }
  }
  return false;
}

function __crypto_space(c: Conn, offset: i64): i32 {
  let space = 0;
  while (space < 3) {
    let s = c.spaces[space];
    if (!s.discarded && s.crypto_out.len() as i64 > offset && s.crypto_sent > offset) {
      return space;
    }
    space += 1;
  }
  return -1;
}

function __without(xs: i32[], x: i32): i32[] {
  let out: i32[] = [];
  for y in xs {
    if (y != x) {
      out = out.append(y);
    }
  }
  return out;
}

function __min(a: i64, b: i64): i64 {
  if (a < b) {
    return a;
  }
  return b;
}

function __max(a: i64, b: i64): i64 {
  if (a > b) {
    return a;
  }
  return b;
}

function __is(o: Option[u8[]], b: [u8]): boolean {
  match (o) {
    Some(x) => {
      return __same(x, b);
    },
    None => {
      return false;
    }
  }
}

function __same_option(a: Option[u8[]], b: Option[u8[]]): boolean {
  match (a) {
    Some(x) => {
      return __is(b, x);
    },
    None => {
      return b.is_none();
    }
  }
}

function __same(a: [u8], b: [u8]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  let diff = 0;
  let i = 0;
  while (i < a.len()) {
    diff = diff | a[i] as i32 ^ b[i] as i32;
    i += 1;
  }
  return diff == 0;
}

function __copy(b: [u8]): u8[] {
  return __bytes_owned(b);
}
```
