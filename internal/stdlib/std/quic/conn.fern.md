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

<<9 pub function client(c: Config, entropy: [u8], now: i64, unix_ms: i64): Result[Conn, string]>>

<<10 function (c: Conn) __initial_keys(dcid: [u8]): Conn>>

<<11 pub function (c: Conn) connected(): boolean>>

<<12 function __earliest(a: i64, b: i64): i64>>

<<13 pub function (c: Conn) events_taken(): Conn>>

<<14 function __stream_event(n: streams.Event): Event>>

<<15 pub function (c: Conn) open_stream(uni: boolean): Result[(Conn, i64), string]>>

<<16 function (c: Conn) __checked(): Result[Conn, string]>>

<<17 pub function (c: Conn) written(id: i64, data: [u8], fin: boolean): Conn>>

<<18 function (c: Conn) __fail(code: i64, frame_type: i64, reason: string, now: i64): Conn>>

<<19 pub function (c: Conn) tick(now: i64): Conn>>

<<20 function (c: Conn) __tick(now: i64): Conn>>

<<21 pub function (c: Conn) update_keys(now: i64): Result[Conn, string]>>

<<22 function (c: Conn) __promote_keys(send: packet.Keys, recv: packet.Keys, pn: i64, now: i64, remote: boolean)...>>

<<23 pub function (c: Conn) receive(datagram: [u8], now: i64): Conn>>

<<24 function (c: Conn) __datagram(datagram: [u8], now: i64): Conn>>

<<25 function (c: Conn) __retry_held(now: i64): Conn>>

<<26 function (c: Conn) __frame(f: frames.Frame, space: i32, now: i64): Conn>>

<<27 function (c: Conn) __discard(space: i32): Conn>>

<<28 function (c: Conn) __tls_question(e: handshake.Event, now: i64): Conn>>

<<29 pub function (c: Conn) transmit(now: i64): Conn>>

<<30 function (c: Conn) __datagram_sent(now: i64): Conn>>

<<31 struct Plan>>

<<32 function __no_plan(space: i32): Plan>>

<<33 function (c: Conn) __crypto_out(space: i32, room: i32): Conn>>

<<34 function (c: Conn) __seal(plans: Plan[], now: i64): Conn>>

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
import "std/tls/handshake";
import "std/tls/keyschedule";
import "std/tls/identity";
import "std/tls/message";
import "std/tls/verify";
import "std/array";
import "std/option";
import "std/i64";
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
}
```

### `4 pub function config(alpn: string[]): Config`

```fern
<<4 pub function config(alpn: string[]): Config>>=
pub function config(alpn: string[]): Config {
  let p: params.Params = params.Params { ...params.defaults(), max_idle_timeout: 30000 as i64, max_udp_payload_size: 1472 as i64, disable_active_migration: true };
  return Config { alpn: alpn, params: p, cid_len: 8, max_datagram: 1200, server_name: "", roots: verify.no_roots(), identity: None };
}

pub function (c: Config) client_side(server_name: string, roots: verify.Roots): Config {
  return Config { ...c, server_name: server_name, roots: roots };
}

pub function (c: Config) with_streams(bidi: i64, uni: i64, stream_window: i64, connection_window: i64): Config {
  let p: params.Params = params.Params { ...c.params, initial_max_streams_bidi: bidi, initial_max_streams_uni: uni, initial_max_stream_data_bidi_local: stream_window, initial_max_stream_data_bidi_remote: stream_window, initial_max_stream_data_uni: stream_window, initial_max_data: connection_window };
  return Config { ...c, params: p };
}

pub function (c: Config) server_side(id: identity.Identity): Config {
  return Config { ...c, identity: Some(id) };
}
```

### `5 pub struct Close`

```fern
<<5 pub struct Close>>=
pub struct Close { code: i64, frame_type: i64, reason: string, app: boolean, remote: boolean }

pub enum Event { Connected, Closed(Close), StreamOpened(i64), StreamReadable(i64), StreamReset(i64, i64), StreamStopped(i64, i64) }

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
  plan: Plan,
  received_bytes: i64,
  sent_bytes: i64,
  handshake_done_pending: boolean,
  probes: i32[],
  pending: frames.Frame[],
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
}
```

### `6 function __space(): Space`

```fern
<<6 function __space(): Space>>=
function __space(): Space {
  return Space { send: None, recv: None, next_pn: 0 as i64, largest_recv: 0 - 1 as i64, largest_recv_at: 0 as i64, ranges: [], ack_wanted: false, crypto_out: [], crypto_sent: 0 as i64, crypto_lost: [], crypto_in: streams.reassembly(), discarded: false };
}
```

### `7 pub function client_entropy(c: Config): i32`

```fern
<<7 pub function client_entropy(c: Config): i32>>=
pub function client_entropy(c: Config): i32 {
  return c.cid_len + 8 + handshake.client_entropy(__tls_client_config(c, [], []));
}

pub function server_entropy(c: Config): i32 {
  let sc: handshake.ServerConfig = __tls_server_config(c, [], [], []);
  return c.cid_len + 16 + handshake.server_entropy(sc);
}
```

### `8 function __tls_client_config(c: Config, scid: [u8], params_bytes: u8[]): handshake.ClientConfig`

```fern
<<8 function __tls_client_config(c: Config, scid: [u8], params_bytes: u8[]): handshake.ClientConfig>>=
function __tls_client_config(c: Config, scid: [u8], params_bytes: u8[]): handshake.ClientConfig {
  let ext: message.Extension = message.Extension { kind: message.EXT_QUIC_TRANSPORT_PARAMETERS, data: params_bytes };
  return handshake.ClientConfig { ...handshake.client_config(c.server_name), alpn: c.alpn, shares: 2, quic: true, extensions: [ext] };
}

function __tls_server_config(c: Config, chain: u8[][], schemes: i32[], params_bytes: u8[]): handshake.ServerConfig {
  let ext: message.Extension = message.Extension { kind: message.EXT_QUIC_TRANSPORT_PARAMETERS, data: params_bytes };
  return handshake.ServerConfig { ...handshake.server_config(chain, schemes), alpn: c.alpn, quic: true, extensions: [ext] };
}

function __base(c: Config, server: boolean, scid: u8[], dcid: u8[], odcid: u8[], now: i64): Conn {
  let r: recovery.Recovery = recovery.new(c.max_datagram, server).with_max_ack_delay(25000000 as i64);
  return Conn { config: c, server: server, tls: TlsNone, spaces: [__space(), __space(), __space()], recovery: r, scid: scid, dcid: dcid, odcid: odcid, retry_scid: None, token: [], peer_seen: false, peer_params: None, reset_token: [], state: HANDSHAKING, close: None, close_pending: false, close_until: 0 as i64, idle_until: now + c.params.max_idle_timeout * 1000000, validated: !server, plan: __no_plan(0), received_bytes: 0 as i64, sent_bytes: 0 as i64, handshake_done_pending: false, probes: [], pending: [], held: [], streams: streams.table(server, c.params), events: [], out: [], key_phase: false, key_next_send: None, key_next_recv: None, key_previous_recv: None, key_first_recv: 0 as i64, key_previous_max: 0 - 1 as i64, key_first_sent: 0 as i64, key_unacked: false, key_awaiting_peer: false, key_generate_at: 0 as i64, key_discard_at: 0 as i64, key_sent: 0 as i64, failed_auth: 0 as i64, wall_offset: 0 as i64 };
}
```

### `9 pub function client(c: Config, entropy: [u8], now: i64, unix_ms: i64): Result[Conn, string]`

```fern
<<9 pub function client(c: Config, entropy: [u8], now: i64, unix_ms: i64): Result[Conn, string]>>=
pub function client(c: Config, entropy: [u8], now: i64, unix_ms: i64): Result[Conn, string] {
  let scid: u8[] = __bytes_range(entropy, 0, c.cid_len);
  let dcid: u8[] = __bytes_range(entropy, c.cid_len, c.cid_len + 8);
  let p: params.Params = params.Params { ...c.params, initial_source_connection_id: Some(scid) };
  let cc: handshake.ClientConfig = __tls_client_config(c, scid, params.encode(p, false));
  match (handshake.client_start(cc, entropy[c.cid_len + 8:entropy.len()])) {
    Ok(step) => {
      let conn: Conn = __base(c, false, scid, dcid, dcid, now).__initial_keys(dcid);
      conn = Conn { ...conn, tls: TlsClient(step.client), wall_offset: unix_ms * 1000000 - now };
      return Ok(conn.__tls_events(step.events, now));
    },
    Err(e) => {
      return Err(e.message());
    }
  }
}

pub function accept(c: Config, entropy: [u8], first: packet.Header, now: i64): Result[Conn, string] {
  let Some(id) = c.identity else {
    return Err("quic: a server needs an identity");
  };
  let scid: u8[] = __bytes_range(entropy, 0, c.cid_len);
  let reset: u8[] = __bytes_range(entropy, c.cid_len, c.cid_len + 16);
  let p: params.Params = params.Params { ...c.params, original_destination_connection_id: Some(first.dcid), initial_source_connection_id: Some(scid), stateless_reset_token: Some(reset) };
  let sc: handshake.ServerConfig = __tls_server_config(c, id.chain, [id.key.scheme()], params.encode(p, true));
  match (handshake.server_new(sc, entropy[c.cid_len + 16:entropy.len()])) {
    Ok(s) => {
      let conn: Conn = __base(c, true, scid, first.scid, first.dcid, now).__initial_keys(first.dcid);
      return Ok(Conn { ...conn, tls: TlsServer(s), reset_token: reset });
    },
    Err(e) => {
      return Err(e.message());
    }
  }
}
```

### `10 function (c: Conn) __initial_keys(dcid: [u8]): Conn`

```fern
<<10 function (c: Conn) __initial_keys(dcid: [u8]): Conn>>=
function (c: Conn) __initial_keys(dcid: [u8]): Conn {
  let s: Space = c.spaces[recovery.INITIAL];
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

pub function (c: Conn) peer_params(): Option[params.Params] {
  return c.peer_params;
}

pub function (c: Conn) closed_with(): Option[Close] {
  return c.close;
}

pub function (c: Conn) deadline(): i64 {
  if (c.state == CLOSED) {
    return 0 as i64;
  }
  if (c.state == CLOSING || c.state == DRAINING) {
    return c.close_until;
  }
  return __earliest(__earliest(c.recovery.timer(), c.idle_until), __earliest(c.key_generate_at, c.key_discard_at));
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

function (c: Conn) __stepped(): Conn {
  let notes: streams.Event[] = c.streams.events;
  if (notes.len() == 0) {
    return c;
  }
  let events: Event[] = c.events;
  for n in notes {
    events = events.append(__stream_event(n));
  }
  return Conn { ...c, streams: c.streams.events_taken(), events: events };
}
```

### `13 pub function (c: Conn) events_taken(): Conn`

```fern
<<13 pub function (c: Conn) events_taken(): Conn>>=
pub function (c: Conn) events_taken(): Conn {
  return Conn { ...c, events: [] };
}

pub function (c: Conn) datagrams(): u8[][] {
  return c.out;
}

pub function (c: Conn) sent(): Conn {
  return Conn { ...c, out: [] };
}
```

### `14 function __stream_event(n: streams.Event): Event`

```fern
<<14 function __stream_event(n: streams.Event): Event>>=
function __stream_event(n: streams.Event): Event {
  match (n) {
    streams.Opened(id) => {
      return StreamOpened(id);
    },
    streams.Readable(id) => {
      return StreamReadable(id);
    },
    streams.ResetByPeer(id, code) => {
      return StreamReset(id, code);
    },
    streams.StopRequested(id, code) => {
      return StreamStopped(id, code);
    }
  }
}
```

### `15 pub function (c: Conn) open_stream(uni: boolean): Result[(Conn, i64), string]`

```fern
<<15 pub function (c: Conn) open_stream(uni: boolean): Result[(Conn, i64), string]>>=
pub function (c: Conn) open_stream(uni: boolean): Result[(Conn, i64), string] {
  if (c.state != ESTABLISHED) {
    return Err("quic: the connection is not established");
  }
  let cur: Conn = Conn { ...c, streams: c.streams.open(uni) };
  if (cur.streams.refused != "") {
    return Err("quic: " + cur.streams.refused);
  }
  let id: i64 = cur.streams.last_opened;
  return Ok((cur, id));
}

pub function (c: Conn) can_open(uni: boolean): boolean {
  return c.state == ESTABLISHED && c.streams.can_open(uni);
}

pub function (c: Conn) blocked_open(uni: boolean): Conn {
  return Conn { ...c, streams: c.streams.blocked_open(uni) };
}

pub function (c: Conn) write(id: i64, data: [u8], fin: boolean): Result[Conn, string] {
  return c.written(id, data, fin).__checked();
}

pub function (c: Conn) read(id: i64): Result[(Conn, u8[], boolean), string] {
  let cur: Conn = c.read_into(id);
  if (cur.refused() != "") {
    return Err("quic: " + cur.refused());
  }
  let data: u8[] = cur.read_data();
  let fin: boolean = cur.read_fin();
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

### `17 pub function (c: Conn) written(id: i64, data: [u8], fin: boolean): Conn`

```fern
<<17 pub function (c: Conn) written(id: i64, data: [u8], fin: boolean): Conn>>=
pub function (c: Conn) written(id: i64, data: [u8], fin: boolean): Conn {
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

pub function (c: Conn) close(code: i64, reason: string, app: boolean, now: i64): Conn {
  if (c.state >= CLOSING) {
    return c;
  }
  let why: Close = Close { code: code, frame_type: 0 as i64, reason: reason, app: app, remote: false };
  return Conn { ...c, state: CLOSING, close: Some(why), close_pending: true, close_until: now + 3 * c.recovery.pto(recovery.APPLICATION), events: c.events.append(Closed(why)) };
}
```

### `18 function (c: Conn) __fail(code: i64, frame_type: i64, reason: string, now: i64): Conn`

```fern
<<18 function (c: Conn) __fail(code: i64, frame_type: i64, reason: string, now: i64): Conn>>=
function (c: Conn) __fail(code: i64, frame_type: i64, reason: string, now: i64): Conn {
  if (c.state >= CLOSING) {
    return c;
  }
  let why: Close = Close { code: code, frame_type: frame_type, reason: reason, app: false, remote: false };
  return Conn { ...c, state: CLOSING, close: Some(why), close_pending: true, close_until: now + 3 * c.recovery.pto(recovery.APPLICATION), events: c.events.append(Closed(why)) };
}

function (c: Conn) __drain(why: Close, now: i64): Conn {
  if (c.state >= DRAINING) {
    return c;
  }
  let events: Event[] = c.events;
  if (c.state < CLOSING) {
    events = events.append(Closed(why));
  }
  return Conn { ...c, state: DRAINING, close: Some(why), close_pending: false, close_until: now + 3 * c.recovery.pto(recovery.APPLICATION), events: events };
}
```

### `19 pub function (c: Conn) tick(now: i64): Conn`

```fern
<<19 pub function (c: Conn) tick(now: i64): Conn>>=
pub function (c: Conn) tick(now: i64): Conn {
  return c.__key_timers(now).__tick(now);
}
```

### `20 function (c: Conn) __tick(now: i64): Conn`

```fern
<<20 function (c: Conn) __tick(now: i64): Conn>>=
function (c: Conn) __tick(now: i64): Conn {
  if (c.state == CLOSING || c.state == DRAINING) {
    if (now >= c.close_until) {
      return Conn { ...c, state: CLOSED }.__stepped();
    }
    return c.__stepped();
  }
  if (c.state == CLOSED) {
    return c.__stepped();
  }
  if (c.idle_until > 0 && now >= c.idle_until) {
    let why: Close = Close { code: frames.NO_ERROR, frame_type: 0 as i64, reason: "idle timeout", app: false, remote: false };
    return Conn { ...c, state: CLOSED, close: Some(why), events: c.events.append(Closed(why)) }.__stepped();
  }
  let t: i64 = c.recovery.timer();
  if (t == 0 || now < t) {
    return c.__stepped();
  }
  let cur: Conn = Conn { ...c, recovery: c.recovery.on_timeout(now) };
  let lost: recovery.Sent[] = cur.recovery.lost;
  let probe: i32 = cur.recovery.probe;
  let after: Conn = Conn { ...cur, recovery: cur.recovery.outcome_taken() }.__requeue(lost);
  if (probe >= 0) {
    after = Conn { ...after, probes: after.probes.append(probe) };
  }
  return after.__stepped();
}
```

### `21 pub function (c: Conn) update_keys(now: i64): Result[Conn, string]`

```fern
<<21 pub function (c: Conn) update_keys(now: i64): Result[Conn, string]>>=
pub function (c: Conn) update_keys(now: i64): Result[Conn, string] {
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
  return Ok(c.__promote_keys(send, recv, 0 - 1 as i64, now, false));
}
```

### `22 function (c: Conn) __promote_keys(send: packet.Keys, recv: packet.Keys, pn: i64, now: i64, remote: boolean)...`

```fern
<<22 function (c: Conn) __promote_keys(send: packet.Keys, recv: packet.Keys, pn: i64, now: i64, remote: boolean)...>>=
function (c: Conn) __promote_keys(send: packet.Keys, recv: packet.Keys, pn: i64, now: i64, remote: boolean): Conn {
  let s: Space = c.spaces[recovery.APPLICATION];
  let pto: i64 = c.recovery.pto(recovery.APPLICATION);
  let discard: i64 = 0;
  if (remote) {
    discard = now + 3 * pto;
  }
  return Conn { ...c, spaces: c.spaces.with(recovery.APPLICATION, Space { ...s, send: Some(send), recv: Some(recv) }), key_phase: !c.key_phase, key_previous_recv: s.recv, key_previous_max: s.largest_recv, key_first_recv: pn, key_first_sent: s.next_pn, key_next_send: None, key_next_recv: None, key_unacked: remote, key_awaiting_peer: !remote, key_generate_at: now + pto, key_discard_at: discard, key_sent: 0 as i64 };
}

function (c: Conn) __key_timers(now: i64): Conn {
  let cur: Conn = c;
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
      return (old, 0 - 1);
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
    let discard: i64 = c.key_discard_at;
    if (c.key_awaiting_peer) {
      discard = now + 3 * c.recovery.pto(recovery.APPLICATION);
    }
    return Conn { ...c, key_first_recv: pn, key_awaiting_peer: false, key_discard_at: discard };
  }
  return c;
}
```

### `23 pub function (c: Conn) receive(datagram: [u8], now: i64): Conn`

```fern
<<23 pub function (c: Conn) receive(datagram: [u8], now: i64): Conn>>=
pub function (c: Conn) receive(datagram: [u8], now: i64): Conn {
  if (c.state == CLOSED) {
    return c.__stepped();
  }
  let cur: Conn = Conn { ...c, received_bytes: c.received_bytes + datagram.len() as i64 };
  if (cur.state == CLOSING) {
    return Conn { ...cur, close_pending: true }.__stepped();
  }
  cur = cur.__datagram(datagram, now);
  return cur.__retry_held(now).__stepped();
}
```

### `24 function (c: Conn) __datagram(datagram: [u8], now: i64): Conn`

```fern
<<24 function (c: Conn) __datagram(datagram: [u8], now: i64): Conn>>=
function (c: Conn) __datagram(datagram: [u8], now: i64): Conn {
  let cur: Conn = c;
  let at: i32 = 0;
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
    _ => {
      return c;
    }
  }
}

function (c: Conn) __version_negotiation(h: packet.Header, now: i64): Conn {
  if (c.server || c.peer_seen || h.versions.contains(packet.VERSION_1)) {
    return c;
  }
  let why: Close = Close { code: frames.NO_ERROR, frame_type: 0 as i64, reason: "no version in common", app: false, remote: true };
  return Conn { ...c, state: CLOSED, close: Some(why), events: c.events.append(Closed(why)) };
}

function (c: Conn) __retry(datagram: [u8], h: packet.Header, now: i64): Conn {
  if (c.server || c.peer_seen || c.retry_scid.is_some() || !packet.retry_valid(datagram[h.at:h.end], c.odcid)) {
    return c;
  }
  let fresh: Conn = Conn { ...c, dcid: h.scid, retry_scid: Some(h.scid), token: h.token, recovery: c.recovery.discard(recovery.INITIAL) }.__initial_keys(h.scid);
  let s: Space = fresh.spaces[recovery.INITIAL];
  return Conn { ...fresh, spaces: fresh.spaces.with(recovery.INITIAL, Space { ...s, crypto_sent: 0 as i64, crypto_lost: [] }) };
}

function (c: Conn) __ours(h: packet.Header): boolean {
  if (__same(h.dcid, c.scid)) {
    return true;
  }
  return c.server && __same(h.dcid, c.odcid);
}

function (c: Conn) __protected(datagram: [u8], h: packet.Header, space: i32, kind: i32, now: i64): Conn {
  let s: Space = c.spaces[space];
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
      let cur: Conn = c.__key_authenticated(o, generation, now);
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

function (c: Conn) __opening_failed(datagram: [u8], h: packet.Header, keys: packet.Keys, why: string, now: i64): Conn {
  let cur: Conn = c;
  if (why == "packet does not decrypt") {
    let limit: i64 = 4503599627370496;
    match (keys.suite.aead()) {
      keyschedule.ChaCha20Poly1305 => {
        limit = 68719476736;
      },
      _ => {}
    }
    let failed: i64 = c.failed_auth;
    if (failed < limit) {
      failed = failed + 1;
    }
    cur = Conn { ...c, failed_auth: failed };
    if (failed >= limit) {
      return cur.__fail(frames.AEAD_LIMIT_REACHED, 0 as i64, "packet authentication limit reached", now);
    }
  }
  return cur.__maybe_reset(datagram, h, now);
}

function (c: Conn) __hold(bytes: [u8]): Conn {
  if (c.held.len() >= MAX_UNDECRYPTED) {
    return c;
  }
  return Conn { ...c, held: c.held.append(__copy(bytes)) };
}
```

### `25 function (c: Conn) __retry_held(now: i64): Conn`

```fern
<<25 function (c: Conn) __retry_held(now: i64): Conn>>=
function (c: Conn) __retry_held(now: i64): Conn {
  if (c.held.len() == 0) {
    return c;
  }
  let held: u8[][] = c.held;
  let cur: Conn = Conn { ...c, held: [] };
  for p in held {
    if (cur.state < CLOSING) {
      cur = cur.__datagram(p, now);
    }
  }
  if (cur.held.len() == held.len()) {
    return Conn { ...cur, held: held };
  }
  return cur;
}

function (c: Conn) __maybe_reset(datagram: [u8], h: packet.Header, now: i64): Conn {
  let Some(p) = c.peer_params else {
    return c;
  };
  let Some(token) = p.stateless_reset_token else {
    return c;
  };
  let n: i32 = datagram.len();
  if (c.server || n < 21 || !__same(datagram[n - 16:n], token)) {
    return c;
  }
  return c.__drain(Close { code: frames.NO_ERROR, frame_type: 0 as i64, reason: "stateless reset", app: false, remote: true }, now);
}

function (c: Conn) __opened(h: packet.Header, o: packet.Opened, space: i32, kind: i32, now: i64): Conn {
  if (!o.reserved_ok) {
    return c.__fail(frames.PROTOCOL_VIOLATION, 0 as i64, "reserved bits set", now);
  }
  if (__has(c.spaces[space].ranges, o.pn)) {
    return c;
  }
  let cur: Conn = c.__peer_cid(h);
  if (cur.server && space == recovery.HANDSHAKE && !cur.spaces[recovery.INITIAL].discarded) {
    cur = Conn { ...cur, validated: true }.__discard(recovery.INITIAL);
  }
  match (frames.read_all(o.payload)) {
    Ok(fs) => {
      cur = cur.__frames(fs, space, kind, now);
      if (cur.state < CLOSING) {
        cur = Conn { ...cur, idle_until: __idle(cur, now) }.__received(space, o.pn, __eliciting(fs), now);
      }
      return cur;
    },
    Err(e) => {
      return cur.__fail(e.code, e.frame_type, e.reason, now);
    }
  }
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
  let ms: i64 = c.config.params.max_idle_timeout;
  if let Some(p) = c.peer_params {
    if (p.max_idle_timeout > 0 && (ms == 0 || p.max_idle_timeout < ms)) {
      ms = p.max_idle_timeout;
    }
  }
  if (ms == 0) {
    return 0 as i64;
  }
  let floor: i64 = 3 * c.recovery.pto(recovery.APPLICATION);
  let d: i64 = ms * 1000000;
  if (d < floor) {
    d = floor;
  }
  return now + d;
}

function __eliciting(fs: frames.Frame[]): boolean {
  for f in fs {
    if (f.ack_eliciting()) {
      return true;
    }
  }
  return false;
}

function (c: Conn) __received(space: i32, pn: i64, eliciting: boolean, now: i64): Conn {
  let s: Space = c.spaces[space];
  let largest: i64 = s.largest_recv;
  let at: i64 = s.largest_recv_at;
  if (pn > largest) {
    largest = pn;
    at = now;
  }
  s = Space { ...s, ranges: __add_range(s.ranges, pn), largest_recv: largest, largest_recv_at: at, ack_wanted: s.ack_wanted || eliciting };
  return Conn { ...c, spaces: c.spaces.with(space, s) };
}

function (c: Conn) __frames(fs: frames.Frame[], space: i32, kind: i32, now: i64): Conn {
  let cur: Conn = c;
  for f in fs {
    if (cur.state >= CLOSING) {
      return cur;
    }
    if (!f.permitted(kind)) {
      return cur.__fail(frames.PROTOCOL_VIOLATION, 0 as i64, "a frame not allowed in this packet type", now);
    }
    cur = cur.__frame(f, space, now);
  }
  return cur;
}
```

### `26 function (c: Conn) __frame(f: frames.Frame, space: i32, now: i64): Conn`

```fern
<<26 function (c: Conn) __frame(f: frames.Frame, space: i32, now: i64): Conn>>=
function (c: Conn) __frame(f: frames.Frame, space: i32, now: i64): Conn {
  match (f) {
    frames.AckFrame(a) => {
      return c.__ack(a, space, now);
    },
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
    _ => {
      return c.__frame_rest(f, now);
    }
  }
}

function (c: Conn) __frame_rest(f: frames.Frame, now: i64): Conn {
  if (streams.owns(f)) {
    let cur: Conn = Conn { ...c, streams: c.streams.on_frame(f) };
    if let Some(e) = cur.streams.failed {
      return cur.__fail(e.code, e.frame_type, e.reason, now);
    }
    return cur;
  }
  match (f) {
    frames.NewToken(_) => {
      if (c.server) {
        return c.__fail(frames.PROTOCOL_VIOLATION, frames.NEW_TOKEN, "a client sent NEW_TOKEN", now);
      }
      return c;
    },
    _ => {
      return c;
    }
  }
}

function (c: Conn) __ack(a: frames.Ack, space: i32, now: i64): Conn {
  let exponent: i64 = 3;
  if let Some(p) = c.peer_params {
    exponent = p.ack_delay_exponent;
  }
  let delay: i64 = (a.delay << exponent) * 1000;
  let cur: Conn = Conn { ...c, recovery: c.recovery.on_ack(space, a, delay, now, c.spaces[space].next_pn - 1) };
  let refused: i64 = cur.recovery.refused;
  if (refused != 0) {
    return Conn { ...cur, recovery: cur.recovery.outcome_taken() }.__fail(refused, frames.ACK, "an ACK of a packet never sent", now);
  }
  let acked: recovery.Sent[] = cur.recovery.acked;
  let lost: recovery.Sent[] = cur.recovery.lost;
  cur = Conn { ...cur, recovery: cur.recovery.outcome_taken() };
  if (!cur.server && space == recovery.HANDSHAKE) {
    cur = Conn { ...cur, recovery: cur.recovery.validated() };
  }
  return cur.__delivered(acked).__requeue(lost);
}

function (c: Conn) __delivered(acked: recovery.Sent[]): Conn {
  let t: streams.Streams = c.streams;
  for p in acked {
    for item in p.resend {
      t = t.acked(item);
    }
  }
  return Conn { ...c, streams: t };
}

function (c: Conn) __requeue(lost: recovery.Sent[]): Conn {
  let cur: Conn = c;
  for p in lost {
    cur = cur.__resend(p.resend);
  }
  return cur;
}

function (c: Conn) __resend(items: recovery.Resend[]): Conn {
  let cur: Conn = c;
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
  let space: i32 = __crypto_space(c, offset);
  if (space < 0) {
    return c;
  }
  let s: Space = c.spaces[space];
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
  let s: Space = c.spaces[space];
  if (s.discarded) {
    return c;
  }
  return Conn { ...c, spaces: c.spaces.with(space, Space { ...__space(), discarded: true, next_pn: s.next_pn }), recovery: c.recovery.discard(space) };
}

function (c: Conn) __crypto(space: i32, offset: i64, data: u8[], now: i64): Conn {
  let s: Space = c.spaces[space];
  let (r, bytes) = s.crypto_in.add(offset, data).take();
  if (r.held_bytes > MAX_CRYPTO_HELD) {
    return c.__fail(frames.CRYPTO_BUFFER_EXCEEDED, frames.CRYPTO, "too much CRYPTO data held out of order", now);
  }
  let cur: Conn = Conn { ...c, spaces: c.spaces.with(space, Space { ...s, crypto_in: r }) };
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
  let cur: Conn = c;
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
      let s: Space = c.spaces[f.level];
      return Conn { ...c, spaces: c.spaces.with(f.level, Space { ...s, crypto_out: s.crypto_out.concat(f.bytes) }) };
    },
    handshake.Secret(t) => {
      return c.__secret(t);
    },
    handshake.Connected => {
      return c.__connected(now);
    },
    _ => {
      return c.__tls_question(e, now);
    }
  }
}

function (c: Conn) __secret(t: handshake.TrafficSecret): Conn {
  let k: packet.Keys = packet.keys(__suite(c), t.secret);
  let s: Space = c.spaces[t.level];
  if (t.write) {
    s = Space { ...s, send: Some(k) };
  } else {
    s = Space { ...s, recv: Some(k) };
  }
  let cur: Conn = Conn { ...c, spaces: c.spaces.with(t.level, s) };
  if (t.level == recovery.APPLICATION) {
    if (t.write) {
      cur = Conn { ...cur, key_next_send: Some(k.updated()) };
    } else {
      cur = Conn { ...cur, key_next_recv: Some(k.updated()) };
    }
  }
  return cur;
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
    handshake.ResumeOffered(_) => {
      return c.__server_step(t.resume(None), now);
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
  match (verify.verify_server(v.chain, cfg.roots, cfg.server_name, unix_sec)) {
    Ok(leaf) => {
      return verify.verify_signed(leaf, v.scheme, v.signed, v.signature);
    },
    Err(_) => {
      return false;
    }
  }
}

function (c: Conn) __connected(now: i64): Conn {
  match (c.__peer_transport_params()) {
    Ok(p) => {
      let cur: Conn = Conn { ...c, state: ESTABLISHED, peer_params: Some(p), streams: c.streams.with_peer(p), recovery: c.recovery.with_max_ack_delay(p.max_ack_delay * 1000000), events: c.events.append(Event.Connected) };
      if (cur.server) {
        cur = Conn { ...cur, handshake_done_pending: true, recovery: cur.recovery.confirmed() }.__discard(recovery.HANDSHAKE);
      }
      return Conn { ...cur, idle_until: __idle(cur, now) };
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
  let p: params.Params = params.decode(raw, !c.server)?;
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

### `29 pub function (c: Conn) transmit(now: i64): Conn`

```fern
<<29 pub function (c: Conn) transmit(now: i64): Conn>>=
pub function (c: Conn) transmit(now: i64): Conn {
  let cur: Conn = c;
  let guard: i32 = 0;
  while (guard < 64) {
    let before: i32 = cur.out.len();
    cur = cur.__datagram_sent(now);
    if (cur.out.len() == before) {
      return cur;
    }
    guard = guard + 1;
  }
  return cur;
}
```

### `30 function (c: Conn) __datagram_sent(now: i64): Conn`

```fern
<<30 function (c: Conn) __datagram_sent(now: i64): Conn>>=
function (c: Conn) __datagram_sent(now: i64): Conn {
  if (c.validated) {
    return c.__datagram_out(now);
  }
  let before: Conn = c;
  let next: Conn = c.__datagram_out(now);
  if (next.sent_bytes > 3 * next.received_bytes) {
    return before;
  }
  return next;
}
```

### `31 struct Plan`

```fern
<<31 struct Plan>>=
struct Plan { space: i32, payload: u8[], eliciting: boolean, resend: recovery.Resend[] }
```

### `32 function __no_plan(space: i32): Plan`

```fern
<<32 function __no_plan(space: i32): Plan>>=
function __no_plan(space: i32): Plan {
  return Plan { space: space, payload: [], eliciting: false, resend: [] };
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
  let cur: Conn = c;
  let space: i32 = 0;
  while (space < 3) {
    let s: Space = cur.spaces[space];
    if let Some(keys) = s.send {
      let used: i64 = s.next_pn;
      if (space == recovery.APPLICATION) {
        used = cur.key_sent;
      }
      if (s.next_pn >= frames.MAX_VARINT || used >= __seal_limit(keys) - 1) {
        if (space == recovery.APPLICATION && s.next_pn < frames.MAX_VARINT) {
          if let Ok(updated) = cur.update_keys(now) {
            cur = updated;
          } else {
            return cur.__fail(frames.AEAD_LIMIT_REACHED, 0 as i64, "packet encryption limit reached", now);
          }
        } else {
          return cur.__fail(frames.AEAD_LIMIT_REACHED, 0 as i64, "packet encryption limit reached", now);
        }
      }
    }
    space = space + 1;
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
  let cur: Conn = c;
  let plans: Plan[] = [];
  let room: i32 = c.config.max_datagram;
  let space: i32 = 0;
  while (space < 3) {
    cur = cur.__plan(space, room - 64, now);
    if (cur.plan.payload.len() > 0) {
      plans = plans.append(cur.plan);
      room = room - cur.plan.payload.len() - 64;
      cur = Conn { ...cur, plan: __no_plan(space) };
    }
    space = space + 1;
  }
  if (plans.len() == 0) {
    return cur;
  }
  return cur.__seal(plans, now);
}

function (c: Conn) __plan(space: i32, room: i32, now: i64): Conn {
  let s: Space = c.spaces[space];
  if (s.discarded || s.send.is_none() || room < 40) {
    return c;
  }
  let cur: Conn = Conn { ...c, plan: Plan { ...c.plan, space: space } };
  if (s.ack_wanted) {
    cur = cur.__ack_planned(space, now);
  }
  let window: boolean = cur.recovery.window() >= cur.config.max_datagram as i64 || cur.probes.contains(space);
  if (!window) {
    return cur.__acked_out(space);
  }
  return cur.__crypto_out(space, room).__control_out(space, room).__streams_out(space, room).__probe_out(space).__acked_out(space);
}

function (c: Conn) __planned(f: frames.Frame, again: boolean): Conn {
  let cur: Conn = c.__pushed(f);
  if (!again) {
    return cur;
  }
  return Conn { ...cur, plan: Plan { ...cur.plan, eliciting: true, resend: cur.plan.resend.append(recovery.Again(f)) } };
}

function (c: Conn) __pushed(f: frames.Frame): Conn {
  let payload: u8[] = c.plan.payload;
  let cur: Conn = Conn { ...c, plan: Plan { ...c.plan, payload: [] } };
  payload = frames.put(__started(payload, cur.config.max_datagram), f);
  return Conn { ...cur, plan: Plan { ...cur.plan, payload: payload } };
}

function (c: Conn) __ack_planned(space: i32, now: i64): Conn {
  let delay: i64 = __ack_delay(c.spaces[space], space, now);
  let ranges: frames.AckRange[] = c.spaces[space].ranges;
  let room: i32 = c.config.max_datagram;
  let payload: u8[] = c.plan.payload;
  let cur: Conn = Conn { ...c, plan: Plan { ...c.plan, payload: [] } };
  payload = frames.put_ack(__started(payload, room), delay, ranges);
  return Conn { ...cur, plan: Plan { ...cur.plan, payload: payload } };
}

// __started gives a plan's first frame a buffer the whole datagram fits in.
function __started(own payload: u8[], room: i32): u8[] {
  if (payload.len() > 0) {
    return payload;
  }
  let out: u8[] = __alloc_u8(room);
  __arr_set_len(out, 0);
  return out;
}

function (c: Conn) __acked_out(space: i32): Conn {
  if (c.plan.payload.len() == 0) {
    return c;
  }
  let unacked: boolean = c.key_unacked;
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
  let s: Space = c.spaces[space];
  let cur: Conn = c;
  let lost: i64[] = s.crypto_lost;
  let kept: i64[] = [];
  let i: i32 = 0;
  while (i + 1 < lost.len()) {
    let n: i32 = __crypto_fit(cur.plan.payload.len(), lost[i], lost[i + 1] as i32, room);
    cur = cur.__crypto_planned(space, lost[i], n);
    let rest: i32 = lost[i + 1] as i32 - n;
    if (rest > 0) {
      kept = kept.append(lost[i] + (lost[i + 1] - rest as i64)).append(rest as i64);
    }
    i = i + 2;
  }
  let unsent: i32 = (s.crypto_out.len() as i64 - s.crypto_sent) as i32;
  let sent: i64 = s.crypto_sent;
  if (unsent > 0) {
    let n: i32 = __crypto_fit(cur.plan.payload.len(), s.crypto_sent, unsent, room);
    cur = cur.__crypto_planned(space, s.crypto_sent, n);
    sent = s.crypto_sent + n as i64;
  }
  return Conn { ...cur, spaces: cur.spaces.with(space, Space { ...cur.spaces[space], crypto_lost: kept, crypto_sent: sent }) };
}

function __crypto_fit(used: i32, offset: i64, len: i32, room: i32): i32 {
  let free: i32 = room - used - 1 - frames.varint_len(offset) - 2;
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
  let at: i32 = offset as i32;
  let cur: Conn = c.__pushed(frames.Crypto(offset, __copy(c.spaces[space].crypto_out[at:at + n])));
  return Conn { ...cur, plan: Plan { ...cur.plan, eliciting: true, resend: cur.plan.resend.append(recovery.CryptoRange(offset, n)) } };
}

function (c: Conn) __control_out(space: i32, room: i32): Conn {
  if (space != recovery.APPLICATION) {
    return c;
  }
  let cur: Conn = c;
  if (cur.handshake_done_pending) {
    cur = Conn { ...cur, handshake_done_pending: false }.__planned(frames.HandshakeDone, true);
  }
  if (cur.pending.len() == 0) {
    return cur;
  }
  let pending: frames.Frame[] = cur.pending;
  cur = Conn { ...cur, pending: [] };
  let kept: frames.Frame[] = [];
  for f in pending {
    if (cur.plan.payload.len() + frames.encode(f).len() <= room) {
      cur = cur.__planned(f, true);
    } else {
      kept = kept.append(f);
    }
  }
  return Conn { ...cur, pending: kept };
}

function (c: Conn) __streams_out(space: i32, room: i32): Conn {
  if (space != recovery.APPLICATION || c.state != ESTABLISHED) {
    return c;
  }
  let before: i32 = c.plan.payload.len();
  let payload: u8[] = c.plan.payload;
  let cur: Conn = Conn { ...c, plan: Plan { ...c.plan, payload: [] } };
  cur = Conn { ...cur, streams: cur.streams.fill(payload, room) };
  return Conn { ...cur, plan: cur.plan.__filled(cur.streams.filled, cur.streams.resend, before), streams: cur.streams.fill_taken() };
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
  let cur: Conn = Conn { ...c, probes: __without(c.probes, space) };
  if (cur.plan.eliciting) {
    return cur;
  }
  for sent in cur.recovery.unacked(space) {
    for item in sent.resend {
      if let recovery.CryptoRange(offset, len) = item {
        let n: i32 = __crypto_fit(cur.plan.payload.len(), offset, len, cur.config.max_datagram - 64);
        if (n > 0) {
          return cur.__crypto_planned(space, offset, n);
        }
      }
    }
  }
  cur = cur.__pushed(frames.Ping);
  return Conn { ...cur, plan: Plan { ...cur.plan, eliciting: true } };
}
```

### `34 function (c: Conn) __seal(plans: Plan[], now: i64): Conn`

```fern
<<34 function (c: Conn) __seal(plans: Plan[], now: i64): Conn>>=
function (c: Conn) __seal(plans: Plan[], now: i64): Conn {
  let pad: boolean = __needs_padding(plans, c.server);
  let cur: Conn = c;
  let out: u8[] = [];
  let i: i32 = 0;
  while (i < plans.len()) {
    let plan: Plan = plans[i];
    let extra: i32 = 0;
    let bytes: u8[] = cur.__sealed(plan, 0);
    if (pad && i == plans.len() - 1 && out.len() + bytes.len() < packet.MIN_INITIAL_DATAGRAM) {
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
    i = i + 1;
  }
  if (!cur.server && __has_space(plans, recovery.HANDSHAKE)) {
    cur = cur.__discard(recovery.INITIAL);
  }
  if (out.len() == 0) {
    return cur;
  }
  return Conn { ...cur, out: cur.out.append(out), sent_bytes: cur.sent_bytes + out.len() as i64 };
}

function __needs_padding(plans: Plan[], server: boolean): boolean {
  for p in plans {
    if (p.space == recovery.INITIAL && (!server || p.eliciting)) {
      return true;
    }
  }
  return false;
}

function __has_space(plans: Plan[], space: i32): boolean {
  for p in plans {
    if (p.space == space) {
      return true;
    }
  }
  return false;
}

function (c: Conn) __sealed(p: Plan, extra: i32): u8[] {
  let s: Space = c.spaces[p.space];
  let Some(keys) = s.send else {
    return [];
  };
  let used: i64 = s.next_pn;
  if (p.space == recovery.APPLICATION) {
    used = c.key_sent;
  }
  if (used >= __seal_limit(keys) || s.next_pn > frames.MAX_VARINT) {
    return [];
  }
  let payload: u8[] = p.payload;
  if (extra > 0) {
    payload = frames.put(payload, frames.Padding(extra));
  }
  let n: i32 = packet.pn_len(s.next_pn, 0 - 1 as i64);
  if (p.space == recovery.APPLICATION) {
    return packet.seal_short(c.dcid, s.next_pn, n, c.key_phase, false, payload, keys);
  }
  let kind: packet.Kind = packet.Initial;
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
  let pn: i64 = c.spaces[p.space].next_pn;
  let sent: recovery.Sent = recovery.Sent { pn: pn, at: now, size: size, ack_eliciting: p.eliciting, in_flight: p.eliciting || padded, resend: p.resend };
  let sent_count: i64 = c.key_sent;
  if (p.space == recovery.APPLICATION) {
    sent_count = sent_count + 1;
  }
  let cur: Conn = Conn { ...c, key_sent: sent_count, recovery: c.recovery.on_sent(p.space, sent) };
  return Conn { ...cur, spaces: cur.spaces.with(p.space, Space { ...cur.spaces[p.space], next_pn: pn + 1 }) };
}

function (c: Conn) __close_datagram(): Conn {
  if (!c.close_pending) {
    return c;
  }
  let Some(why) = c.close else {
    return c;
  };
  let plans: Plan[] = [];
  let space: i32 = 0;
  while (space < 3) {
    let s: Space = c.spaces[space];
    if (!s.discarded && s.send.is_some()) {
      plans = plans.append(Plan { space: space, payload: frames.encode(frames.ConnectionCloseFrame(__close_frame(why, space))), eliciting: false, resend: [] });
    }
    space = space + 1;
  }
  return Conn { ...c, close_pending: false }.__seal(plans, 0 as i64);
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
  let out: frames.AckRange[] = [];
  let placed: boolean = false;
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
  let i: i32 = 0;
  while (i < MAX_ACK_RANGES) {
    kept = kept.append(out[i]);
    i = i + 1;
  }
  return kept;
}

function __push_range(out: frames.AckRange[], r: frames.AckRange): frames.AckRange[] {
  if (out.len() > 0) {
    let last: frames.AckRange = out[out.len() - 1];
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
  let space: i32 = 0;
  while (space < 3) {
    let s: Space = c.spaces[space];
    if (!s.discarded && s.crypto_out.len() as i64 > offset && s.crypto_sent > offset) {
      return space;
    }
    space = space + 1;
  }
  return 0 - 1;
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
  let diff: i32 = 0;
  let i: i32 = 0;
  while (i < a.len()) {
    diff = diff | a[i] as i32 ^ b[i] as i32;
    i = i + 1;
  }
  return diff == 0;
}

function __copy(b: [u8]): u8[] {
  return __bytes_owned(b);
}
```
