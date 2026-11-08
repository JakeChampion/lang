# A TCP connection

This module is the state machine of one TCP connection (RFC 9293), with no device and no clock of its own. It works on `Segment` values and on a time in milliseconds that the caller supplies. `std/ip` moves the segments to and from the wire, and `std/tcp` binds connections to an interface. The split keeps this module in the core partition, so a hosted test drives it with synthetic segments.

A connection opens passively: `accept` answers a SYN with a SYN-ACK. The connection takes data in order and acknowledges every segment that carries data or a FIN. It drops a segment ahead of the next expected byte, and the duplicate acknowledgement asks the peer to send it again.

The connection keeps the bytes it sent in flight until the peer acknowledges them. When the retransmission timer expires, it sends them again and doubles the timeout. `close` queues a FIN behind the data.

The connection is `Closed` when both sides closed, after a reset, or after too many retransmissions. Every operation gives back the new connection value and the segments to send. The caller sends them, in order.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<pub struct Segment>>

<<pub enum State>>

<<pub struct Connection>>

<<the sequence space>>

<<pub function accept>>

<<pub function (c: Connection) receive>>

<<pub function (c: Connection) transmit>>

<<the application side>>

<<pub function reset>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module is the state machine of one TCP connection, over segments and a
// clock that the caller supplies.
```

### `dependencies`

```fern
<<dependencies>>=
import "std/option";
```

### `pub struct Segment`

A segment is the TCP header without its options, the two addresses of the IPv4 header that the checksum covers, and the payload. `mss` is the maximum segment size option of a SYN, or 0 when the segment carries none.

```fern
<<pub struct Segment>>=
pub struct Segment { source: u8[], source_port: i32, destination: u8[], port: i32, seq: u32, ack: u32, flags: i32, window: i32, mss: i32, payload: u8[] }

pub const FIN: i32 = 1;

pub const SYN: i32 = 2;

pub const RST: i32 = 4;

pub const PSH: i32 = 8;

pub const ACK: i32 = 16;

// The receive window the connection advertises when nothing is buffered.
pub const WINDOW: i32 = 16384;

// The maximum segment size the connection offers and uses when the peer
// offers none: an Ethernet frame less the two headers, and the RFC 9293 default.
pub const MSS: i32 = 1460;

const DEFAULT_MSS: i32 = 536;

// The first retransmission timeout, the limit the doubling stops at, and the
// retransmissions after which the connection gives up.
pub const RTO_MS: i64 = 200;

const RTO_MAX_MS: i64 = 6400;

const RETRIES: i32 = 8;

// How long a connection stays in TimeWait.
pub const TIME_WAIT_MS: i64 = 1000;

pub function (s: Segment) has(flag: i32): boolean {
  return (s.flags & flag) != 0;
}

// The sequence numbers the segment takes: its payload, plus one for a SYN and
// one for a FIN.
pub function (s: Segment) length(): i32 {
  let n = s.payload.len();
  if (s.has(SYN)) {
    n += 1;
  }
  if (s.has(FIN)) {
    n += 1;
  }
  return n;
}
```

### `pub enum State`

The states of RFC 9293 that a passive open visits. There is no `Listen` and no `SynSent`: the listener in `std/tcp` holds the port, and the connection exists from the SYN on.

```fern
<<pub enum State>>=
pub enum State { SynReceived, Established, FinWait1, FinWait2, Closing, TimeWait, CloseWait, LastAck, Closed }

pub function (s: State) to_string(): string {
  match (s) {
    SynReceived => {
      return "syn-received";
    },
    Established => {
      return "established";
    },
    FinWait1 => {
      return "fin-wait-1";
    },
    FinWait2 => {
      return "fin-wait-2";
    },
    Closing => {
      return "closing";
    },
    TimeWait => {
      return "time-wait";
    },
    CloseWait => {
      return "close-wait";
    },
    LastAck => {
      return "last-ack";
    },
    Closed => {
      return "closed";
    }
  }
}

pub function (s: State) eq(other: State): boolean {
  return s.to_string() == other.to_string();
}
```

### `pub struct Connection`

`received` holds the bytes the application has not read. `unacked` holds the bytes from `snd_una` that the peer has not acknowledged, and `unsent` the bytes that wait for window. `timer` is the time the retransmission timer expires, or 0 when nothing is in flight. In `TimeWait`, it is the time the connection closes.

```fern
<<pub struct Connection>>=
pub struct Connection {
  local: u8[],
  port: i32,
  remote: u8[],
  remote_port: i32,
  state: State,
  rcv_nxt: u32,
  snd_una: u32,
  snd_nxt: u32,
  snd_wnd: i32,
  mss: i32,
  received: u8[],
  unacked: u8[],
  unsent: u8[],
  fin_queued: boolean,
  fin_sent: boolean,
  timer: i64,
  rto: i64,
  retries: i32,
}

// Whether the segment is from the connection's peer, to the connection's port.
pub function (c: Connection) matches(s: Segment): boolean {
  return s.port == c.port && s.source_port == c.remote_port && same(s.source, c.remote) && same(s.destination, c.local);
}

pub function (c: Connection) is_closed(): boolean {
  return c.state.eq(Closed);
}

// A closed connection with no peer: the value of a free slot.
pub function none(): Connection {
  return Connection { local: [], port: 0, remote: [], remote_port: 0, state: Closed, rcv_nxt: 0 as u32, snd_una: 0 as u32, snd_nxt: 0 as u32, snd_wnd: 0, mss: DEFAULT_MSS, received: [], unacked: [], unsent: [], fin_queued: false, fin_sent: false, timer: 0i64, rto: RTO_MS, retries: 0 };
}

// The window the connection advertises: the room left in its receive buffer.
pub function (c: Connection) window(): i32 {
  let room = WINDOW - c.received.len();
  if (room < 0) {
    return 0;
  }
  return room;
}

function same(a: u8[], b: u8[]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  for i in 0..a.len() {
    if (a[i] != b[i]) {
      return false;
    }
  }
  return true;
}
```

### `the sequence space`

Sequence numbers wrap, so the comparisons work on the difference as a signed number.

```fern
<<the sequence space>>=
// Whether a is after b in the sequence space.
function after(a: u32, b: u32): boolean {
  return (a - b) as i32 > 0;
}

function at_or_after(a: u32, b: u32): boolean {
  return (a - b) as i32 >= 0;
}

function (c: Connection) segment(flags: i32, seq: u32, payload: u8[]): Segment {
  return Segment { source: c.local, source_port: c.port, destination: c.remote, port: c.remote_port, seq: seq, ack: c.rcv_nxt, flags: flags, window: c.window(), mss: 0, payload: payload };
}

// An acknowledgement of everything received so far.
function (c: Connection) ack(): Segment {
  return c.segment(ACK, c.snd_nxt, []);
}

function (c: Connection) syn_ack(): Segment {
  return Segment { ...c.segment(SYN | ACK, c.snd_una, []), mss: MSS };
}

function (c: Connection) armed(now: i64): Connection {
  if (c.timer != 0i64) {
    return c;
  }
  return Connection { ...c, timer: now + c.rto };
}

function slice(data: u8[], start: i32, end: i32): u8[] {
  let out: u8[] = [];
  for i in start..end {
    out = out.append(data[i]);
  }
  return out;
}

function concat(a: u8[], b: u8[]): u8[] {
  for x in b {
    a = a.append(x);
  }
  return a;
}
```

### `pub function accept`

`accept` takes a SYN for a port that listens, and the initial sequence number of the connection. It gives the connection in `SynReceived` and the SYN-ACK to send. It is `None` for a segment that is not a plain SYN.

```fern
<<pub function accept>>=
pub function accept(s: Segment, iss: u32, now: i64): Option[(Connection, Segment)] {
  if (!s.has(SYN) || s.has(ACK) || s.has(RST) || s.has(FIN)) {
    return None;
  }
  let mss = s.mss;
  if (mss <= 0) {
    mss = DEFAULT_MSS;
  }
  if (mss > MSS) {
    mss = MSS;
  }
  let c = Connection {
    local: s.destination,
    port: s.port,
    remote: s.source,
    remote_port: s.source_port,
    state: SynReceived,
    rcv_nxt: s.seq + 1 as u32,
    snd_una: iss,
    snd_nxt: iss + 1 as u32,
    snd_wnd: s.window,
    mss: mss,
    received: [],
    unacked: [],
    unsent: [],
    fin_queued: false,
    fin_sent: false,
    timer: 0i64,
    rto: RTO_MS,
    retries: 0,
  };
  return Some((c.armed(now), c.syn_ack()));
}
```

### `pub function (c: Connection) receive`

`receive` takes a segment that `matches` the connection. A reset closes the connection. An acknowledgement frees the bytes it covers and moves the states that wait for one. Data in order goes to `received`, and a FIN in order moves the state. Every segment with data or a FIN gets an acknowledgement. A segment out of order gets one too, so the peer sends the missing bytes again.

```fern
<<pub function (c: Connection) receive>>=
pub function (c: Connection) receive(s: Segment, now: i64): (Connection, Segment[]) {
  let none: Segment[] = [];
  if (c.is_closed()) {
    return (c, none);
  }
  if (s.has(RST)) {
    return (c.closed(), none);
  }
  if (c.state.eq(SynReceived) && s.has(SYN) && !s.has(ACK)) {
    return (c, [c.syn_ack()]);
  }
  if (!s.has(ACK)) {
    return (c, none);
  }
  let (acked, ok) = c.take_ack(s, now);
  if (!ok) {
    return (acked, [acked.ack()]);
  }
  if (s.length() == 0) {
    return (acked, none);
  }
  return acked.take_data(s, now);
}

// Takes the data and the FIN of a segment, when it is in order and the
// connection still takes data, and acknowledges it either way.
function (c: Connection) take_data(s: Segment, now: i64): (Connection, Segment[]) {
  if (s.seq != c.rcv_nxt || !c.takes_data()) {
    return (c, [c.ack()]);
  }
  if (s.payload.len() > 0) {
    c = Connection { ...c, received: concat(c.received, s.payload), rcv_nxt: c.rcv_nxt + s.payload.len() as u32 };
  }
  if (s.has(FIN)) {
    c = c.take_fin(now);
  }
  return (c, [c.ack()]);
}

// Whether the connection still takes data: the handshake is complete and
// the peer has not sent its FIN.
function (c: Connection) takes_data(): boolean {
  return c.state.eq(Established) || c.state.eq(FinWait1) || c.state.eq(FinWait2);
}

function (c: Connection) take_fin(now: i64): Connection {
  c = Connection { ...c, rcv_nxt: c.rcv_nxt + 1 as u32 };
  if (c.state.eq(Established)) {
    return Connection { ...c, state: CloseWait };
  }
  if (c.state.eq(FinWait1)) {
    return Connection { ...c, state: Closing };
  }
  return c.time_wait(now);
}

// Applies the acknowledgement of a segment. False when the acknowledgement
// is for bytes the connection has not sent, which only gets an ACK back.
function (c: Connection) take_ack(s: Segment, now: i64): (Connection, boolean) {
  if (after(s.ack, c.snd_nxt)) {
    return (c, false);
  }
  if (!after(s.ack, c.snd_una)) {
    return (Connection { ...c, snd_wnd: s.window }, true);
  }
  let data = c.acked_bytes(s.ack);
  c = Connection { ...c, snd_una: s.ack, snd_wnd: s.window, unacked: slice(c.unacked, data, c.unacked.len()), retries: 0, rto: RTO_MS, timer: 0i64 };
  if (c.snd_una != c.snd_nxt) {
    c = c.armed(now);
  }
  return (c.after_ack(now), true);
}

// The bytes of `unacked` that an acknowledgement up to `ack` covers: the
// sequence numbers it covers, less the SYN and the FIN.
function (c: Connection) acked_bytes(ack: u32): i32 {
  let data = (ack - c.snd_una) as i32;
  if (c.state.eq(SynReceived)) {
    data -= 1;
  }
  if (c.fin_sent && ack == c.snd_nxt) {
    data -= 1;
  }
  if (data > c.unacked.len()) {
    return c.unacked.len();
  }
  return data;
}

// The state after an acknowledgement moved `snd_una`: the handshake
// completes, and an acknowledged FIN moves the close on.
function (c: Connection) after_ack(now: i64): Connection {
  if (c.state.eq(SynReceived)) {
    return Connection { ...c, state: Established };
  }
  if (!c.fin_sent || c.snd_una != c.snd_nxt) {
    return c;
  }
  if (c.state.eq(FinWait1)) {
    return Connection { ...c, state: FinWait2 };
  }
  if (c.state.eq(Closing)) {
    return c.time_wait(now);
  }
  if (c.state.eq(LastAck)) {
    return c.closed();
  }
  return c;
}

function (c: Connection) time_wait(now: i64): Connection {
  return Connection { ...c, state: TimeWait, timer: now + TIME_WAIT_MS };
}

function (c: Connection) closed(): Connection {
  return Connection { ...c, state: Closed, timer: 0i64, unsent: [], unacked: [] };
}
```

### `pub function (c: Connection) transmit`

`transmit` sends what the window allows from `unsent`, then the FIN that `close` queued once nothing is left to send. When the retransmission timer expired, it sends the oldest segment in flight again: the oldest bytes, the SYN-ACK or the FIN. It doubles the timeout, and it closes the connection after `RETRIES` attempts. A connection in `TimeWait` closes when its timer expires.

```fern
<<pub function (c: Connection) transmit>>=
pub function (c: Connection) transmit(now: i64): (Connection, Segment[]) {
  let out: Segment[] = [];
  if (c.is_closed()) {
    return (c, out);
  }
  if (c.state.eq(TimeWait)) {
    if (now >= c.timer) {
      return (c.closed(), out);
    }
    return (c, out);
  }
  let (timed, again) = c.retransmit(now, out);
  if (timed.is_closed() || !(timed.state.eq(Established) || timed.state.eq(CloseWait))) {
    return (timed, again);
  }
  let (sent, more) = timed.send_unsent(now, again);
  return sent.send_fin(now, more);
}

// Sends the oldest segment in flight again when the timer expired, with the
// timeout doubled, and closes the connection after RETRIES attempts.
function (c: Connection) retransmit(now: i64, out: Segment[]): (Connection, Segment[]) {
  if (c.timer == 0i64 || now < c.timer) {
    return (c, out);
  }
  if (c.retries >= RETRIES) {
    return (c.closed(), out);
  }
  let rto = c.rto * 2i64;
  if (rto > RTO_MAX_MS) {
    rto = RTO_MAX_MS;
  }
  c = Connection { ...c, retries: c.retries + 1, rto: rto, timer: now + rto };
  return (c, out.append(c.oldest()));
}

// Sends what the peer's window allows from `unsent`, in segments of at most
// `mss` bytes, with PSH on the last one.
function (c: Connection) send_unsent(now: i64, out: Segment[]): (Connection, Segment[]) {
  while (c.unsent.len() > 0 && c.unacked.len() < c.snd_wnd) {
    let n = c.snd_wnd - c.unacked.len();
    if (n > c.mss) {
      n = c.mss;
    }
    if (n > c.unsent.len()) {
      n = c.unsent.len();
    }
    let payload = slice(c.unsent, 0, n);
    let flags = ACK;
    if (n == c.unsent.len()) {
      flags = ACK | PSH;
    }
    out = out.append(c.segment(flags, c.snd_nxt, payload));
    c = Connection { ...c, unsent: slice(c.unsent, n, c.unsent.len()), unacked: concat(c.unacked, payload), snd_nxt: c.snd_nxt + n as u32 };
    c = c.armed(now);
  }
  return (c, out);
}

// Sends the FIN that `close` queued, once nothing is left to send.
function (c: Connection) send_fin(now: i64, out: Segment[]): (Connection, Segment[]) {
  if (!c.fin_queued || c.fin_sent || c.unsent.len() > 0) {
    return (c, out);
  }
  out = out.append(c.segment(FIN | ACK, c.snd_nxt, []));
  let state = FinWait1;
  if (c.state.eq(CloseWait)) {
    state = LastAck;
  }
  c = Connection { ...c, fin_sent: true, snd_nxt: c.snd_nxt + 1 as u32, state: state };
  return (c.armed(now), out);
}

// The oldest segment in flight, for a retransmission.
function (c: Connection) oldest(): Segment {
  if (c.state.eq(SynReceived)) {
    return c.syn_ack();
  }
  if (c.unacked.len() == 0) {
    return c.segment(FIN | ACK, c.snd_nxt - 1 as u32, []);
  }
  let n = c.unacked.len();
  if (n > c.mss) {
    n = c.mss;
  }
  return c.segment(ACK | PSH, c.snd_una, slice(c.unacked, 0, n));
}
```

### `the application side`

`read` takes the bytes received so far. `write` queues bytes, and `close` queues a FIN behind them. `transmit` sends both. `finished` is true once the peer closed its side, so no more data comes.

```fern
<<the application side>>=
pub function (c: Connection) read(): (Connection, u8[]) {
  return (Connection { ...c, received: [] }, c.received);
}

pub function (c: Connection) write(data: u8[]): Connection {
  if (c.is_closed() || c.fin_queued) {
    return c;
  }
  return Connection { ...c, unsent: concat(c.unsent, data) };
}

pub function (c: Connection) close(): Connection {
  return Connection { ...c, fin_queued: true };
}

pub function (c: Connection) finished(): boolean {
  return c.state.eq(CloseWait) || c.state.eq(LastAck) || c.state.eq(Closing) || c.state.eq(TimeWait) || c.state.eq(Closed);
}
```

### `pub function reset`

The reset for a segment that reaches no connection: it takes the acknowledgement number of a segment that has one, and acknowledges the segment otherwise. A reset itself gets no reply.

```fern
<<pub function reset>>=
pub function reset(s: Segment): Option[Segment] {
  if (s.has(RST)) {
    return None;
  }
  let reply = Segment { source: s.destination, source_port: s.port, destination: s.source, port: s.source_port, seq: 0 as u32, ack: s.seq + s.length() as u32, flags: RST | ACK, window: 0, mss: 0, payload: [] };
  if (s.has(ACK)) {
    reply = Segment { ...reply, seq: s.ack, ack: 0 as u32, flags: RST };
  }
  return Some(reply);
}
```
