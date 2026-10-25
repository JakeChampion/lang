# QUIC frames and variable-length integers

This module reads and writes QUIC frames and variable-length integers.

## Model

Wire helpers encode and decode QUIC frame fields. QUIC variable-length integers use one, two, four, or eight bytes with a two-bit size prefix.

## Contracts

A QUIC variable-length integer is at most 62 bits. Frame type determines the valid fields and whether a frame can appear in a packet context.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub const MAX_VARINT: i64>>

<<4 pub function varint_len(v: i64): i32>>

<<5 pub struct AckRange>>

<<6 pub function (e: FrameError) to_string(): string>>

<<7 impl error.Error for FrameError>>

<<8 pub function (e: FrameError) message(): string>>

<<9 function __bad(kind: i64, reason: string): FrameError>>

<<10 struct Reading>>

<<11 function __reading(payload: u8[], at: i32): Reading>>

<<12 pub function read(payload: u8[], at: i32): Result[(Frame, i32), FrameError]>>

<<13 function __frame(own r: Reading): Reading>>

<<14 function __stream_control(own r: Reading): Reading>>

<<15 function __close(own r: Reading): Reading>>

<<16 pub function encode(f: Frame): u8[]>>

<<17 function __put_rest(own out: u8[], f: Frame): u8[]>>

<<18 pub function put_stream_head(own out: u8[], stream: i64, offset: i64, len: i32, fin: boolean): u8[]>>

<<19 function __put_ack(own out: u8[], a: Ack): u8[]>>

<<20 pub function (f: Frame) ack_eliciting(): boolean>>

<<21 function __zero_rtt_ok(f: Frame): boolean>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads and writes QUIC frames and variable-length integers.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/int";
import "std/i64";
import "std/array";
import "std/option";
import "std/utf8";
import "std/error";
import "core/cmp";
```

### `3 pub const MAX_VARINT: i64`

```fern
<<3 pub const MAX_VARINT: i64>>=
pub const MAX_VARINT: i64 = 4611686018427387903;

pub const PADDING: i64 = 0;

pub const PING: i64 = 1;

pub const ACK: i64 = 2;

pub const ACK_ECN: i64 = 3;

pub const RESET_STREAM: i64 = 4;

pub const STOP_SENDING: i64 = 5;

pub const CRYPTO: i64 = 6;

pub const NEW_TOKEN: i64 = 7;

pub const STREAM: i64 = 8;

pub const MAX_DATA: i64 = 16;

pub const MAX_STREAM_DATA: i64 = 17;

pub const MAX_STREAMS_BIDI: i64 = 18;

pub const MAX_STREAMS_UNI: i64 = 19;

pub const DATA_BLOCKED: i64 = 20;

pub const STREAM_DATA_BLOCKED: i64 = 21;

pub const STREAMS_BLOCKED_BIDI: i64 = 22;

pub const STREAMS_BLOCKED_UNI: i64 = 23;

pub const NEW_CONNECTION_ID: i64 = 24;

pub const RETIRE_CONNECTION_ID: i64 = 25;

pub const PATH_CHALLENGE: i64 = 26;

pub const PATH_RESPONSE: i64 = 27;

pub const CONNECTION_CLOSE: i64 = 28;

pub const CONNECTION_CLOSE_APP: i64 = 29;

pub const HANDSHAKE_DONE: i64 = 30;

pub const NO_ERROR: i64 = 0;

pub const INTERNAL_ERROR: i64 = 1;

pub const CONNECTION_REFUSED: i64 = 2;

pub const FLOW_CONTROL_ERROR: i64 = 3;

pub const STREAM_LIMIT_ERROR: i64 = 4;

pub const STREAM_STATE_ERROR: i64 = 5;

pub const FINAL_SIZE_ERROR: i64 = 6;

pub const FRAME_ENCODING_ERROR: i64 = 7;

pub const TRANSPORT_PARAMETER_ERROR: i64 = 8;

pub const CONNECTION_ID_LIMIT_ERROR: i64 = 9;

pub const PROTOCOL_VIOLATION: i64 = 10;

pub const INVALID_TOKEN: i64 = 11;

pub const APPLICATION_ERROR: i64 = 12;

pub const CRYPTO_BUFFER_EXCEEDED: i64 = 13;

pub const KEY_UPDATE_ERROR: i64 = 14;

pub const AEAD_LIMIT_REACHED: i64 = 15;

pub const NO_VIABLE_PATH: i64 = 16;

pub const CRYPTO_ERROR: i64 = 256;

pub const IN_INITIAL: i32 = 0;

pub const IN_ZERO_RTT: i32 = 1;

pub const IN_HANDSHAKE: i32 = 2;

pub const IN_ONE_RTT: i32 = 3;
```

### `4 pub function varint_len(v: i64): i32`

```fern
<<4 pub function varint_len(v: i64): i32>>=
pub function varint_len(v: i64): i32 {
  if (v < 64) {
    return 1;
  }
  if (v < 16384) {
    return 2;
  }
  if (v < 1073741824) {
    return 4;
  }
  return 8;
}

pub function put_varint(own out: u8[], v: i64): u8[] {
  let n: i32 = varint_len(v);
  if (n == 1) {
    return out.append(v as u8);
  }
  if (n == 2) {
    return out.append((v >> 8 | 64) as u8).append((v & 255) as u8);
  }
  if (n == 4) {
    return out.append((v >> 24 | 128) as u8).append((v >> 16 & 255) as u8).append((v >> 8 & 255) as u8).append((v & 255) as u8);
  }
  let i: i32 = 7;
  out = out.append((v >> 56 | 192) as u8);
  while (i > 1) {
    i = i - 1;
    out = out.append((v >> (i * 8) as i64 & 255) as u8);
  }
  return out.append((v & 255) as u8);
}

pub function put_varint_n(own out: u8[], v: i64, n: i32): u8[] {
  let prefix: i64 = 0;
  if (n == 2) {
    prefix = 64;
  } else if (n == 4) {
    prefix = 128;
  } else if (n == 8) {
    prefix = 192;
  }
  let i: i32 = n;
  while (i > 0) {
    i = i - 1;
    let b: i64 = v >> (i * 8) as i64 & 255;
    if (i == n - 1) {
      b = b | prefix;
    }
    out = out.append(b as u8);
  }
  return out;
}

pub function varint_at(buf: [u8], at: i32): i64 {
  if (at >= buf.len()) {
    return 0 - 1 as i64;
  }
  let first: i32 = buf[at] as i32;
  let n: i32 = 1 << (first >> 6);
  if (at + n > buf.len()) {
    return 0 - 1 as i64;
  }
  let v: i64 = (first & 63) as i64;
  let i: i32 = 1;
  while (i < n) {
    v = v << 8 | buf[at + i] as i64;
    i = i + 1;
  }
  return v;
}

pub function varint_end(buf: [u8], at: i32): i32 {
  return at + (1 << (buf[at] as i32 >> 6));
}
```

### `5 pub struct AckRange`

```fern
<<5 pub struct AckRange>>=
pub struct AckRange { low: i64, high: i64 }

pub struct Ecn { ect0: i64, ect1: i64, ce: i64 }

pub struct Ack { delay: i64, ranges: AckRange[], ecn: Option[Ecn] }

pub struct ResetStream { stream: i64, code: i64, final_size: i64 }

pub struct StreamData { stream: i64, offset: i64, data: u8[], fin: boolean }

pub struct NewConnectionId { sequence: i64, retire_prior_to: i64, id: u8[], reset_token: u8[] }

pub struct ConnectionClose { code: i64, frame_type: i64, reason: string, app: boolean }

pub enum Frame {
  Padding(i32),
  Ping,
  AckFrame(Ack),
  ResetStreamFrame(ResetStream),
  StopSending(i64, i64),
  Crypto(i64, u8[]),
  NewToken(u8[]),
  StreamFrame(StreamData),
  MaxData(i64),
  MaxStreamData(i64, i64),
  MaxStreams(boolean, i64),
  DataBlocked(i64),
  StreamDataBlocked(i64, i64),
  StreamsBlocked(boolean, i64),
  NewConnectionIdFrame(NewConnectionId),
  RetireConnectionId(i64),
  PathChallenge(u8[]),
  PathResponse(u8[]),
  ConnectionCloseFrame(ConnectionClose),
  HandshakeDone,
}

pub struct FrameError { code: i64, frame_type: i64, reason: string }
```

### `6 pub function (e: FrameError) to_string(): string`

```fern
<<6 pub function (e: FrameError) to_string(): string>>=
pub function (e: FrameError) to_string(): string {
  return e.message();
}
```

### `7 impl error.Error for FrameError`

```fern
<<7 impl error.Error for FrameError>>=
impl error.Error for FrameError {}

impl cmp.Display for FrameError {}
```

### `8 pub function (e: FrameError) message(): string`

```fern
<<8 pub function (e: FrameError) message(): string>>=
pub function (e: FrameError) message(): string {
  return "quic frame: error " + e.code.to_string() + " in frame type " + e.frame_type.to_string() + ": " + e.reason;
}
```

### `9 function __bad(kind: i64, reason: string): FrameError`

```fern
<<9 function __bad(kind: i64, reason: string): FrameError>>=
function __bad(kind: i64, reason: string): FrameError {
  return FrameError { code: FRAME_ENCODING_ERROR, frame_type: kind, reason: reason };
}
```

### `10 struct Reading`

```fern
<<10 struct Reading>>=
struct Reading { buf: u8[], at: i32, kind: i64, n: i64, bytes: u8[], frames: Frame[], ok: boolean, failed: Option[FrameError] }
```

### `11 function __reading(payload: u8[], at: i32): Reading`

```fern
<<11 function __reading(payload: u8[], at: i32): Reading>>=
function __reading(payload: u8[], at: i32): Reading {
  return Reading { buf: payload, at: at, kind: 0, n: 0, bytes: [], frames: [], ok: true, failed: None };
}

function __fail(own r: Reading, reason: string): Reading {
  return Reading { ...r, ok: false, failed: Some(__bad(r.kind, reason)) };
}

function __int(own r: Reading, what: string): Reading {
  if (!r.ok) {
    return r;
  }
  let v: i64 = varint_at(r.buf, r.at);
  if (v < 0) {
    return __fail(r, what + " cut short");
  }
  return Reading { ...r, at: varint_end(r.buf, r.at), n: v };
}

function __take(own r: Reading, n: i64, what: string): Reading {
  if (!r.ok) {
    return r;
  }
  if (n < 0 || n > (r.buf.len() - r.at) as i64) {
    return __fail(r, what + " of " + n.to_string() + " bytes past the end of the packet");
  }
  let k: i32 = n as i32;
  return Reading { ...r, at: r.at + k, bytes: __slice(r.buf, r.at, r.at + k) };
}

function __done(own r: Reading, f: Frame): Reading {
  return Reading { ...r, frames: r.frames.append(f) };
}
```

### `12 pub function read(payload: u8[], at: i32): Result[(Frame, i32), FrameError]`

```fern
<<12 pub function read(payload: u8[], at: i32): Result[(Frame, i32), FrameError]>>=
pub function read(payload: u8[], at: i32): Result[(Frame, i32), FrameError] {
  let r: Reading = __frame(__reading(payload, at));
  match (r.failed) {
    Some(e) => {
      return Err(e);
    },
    None => {}
  }
  return Ok((r.frames[0], r.at));
}

pub function read_all(payload: u8[]): Result[Frame[], FrameError] {
  if (payload.len() == 0) {
    return Err(FrameError { code: PROTOCOL_VIOLATION, frame_type: 0, reason: "a packet with no frames" });
  }
  let r: Reading = __reading(payload, 0);
  while (r.ok && r.at < r.buf.len()) {
    r = __frame(r);
  }
  match (r.failed) {
    Some(e) => {
      return Err(e);
    },
    None => {}
  }
  return Ok(r.frames);
}
```

### `13 function __frame(own r: Reading): Reading`

```fern
<<13 function __frame(own r: Reading): Reading>>=
function __frame(own r: Reading): Reading {
  let start: i32 = r.at;
  r = __int(Reading { ...r, kind: 0 }, "frame type");
  if (!r.ok) {
    return r;
  }
  r = Reading { ...r, kind: r.n };
  if (r.kind == PADDING) {
    return __padding(r, start);
  }
  if (r.kind == PING) {
    return __done(r, Ping);
  }
  if (r.kind == HANDSHAKE_DONE) {
    return __done(r, HandshakeDone);
  }
  if (r.kind == ACK || r.kind == ACK_ECN) {
    return __ack(r);
  }
  return __carrying(r);
}

function __padding(own r: Reading, start: i32): Reading {
  let end: i32 = r.at;
  while (end < r.buf.len() && r.buf[end] == 0) {
    end = end + 1;
  }
  return __done(Reading { ...r, at: end }, Padding(end - start));
}

function __carrying(own r: Reading): Reading {
  if (r.kind >= STREAM && r.kind <= 15) {
    return __stream(r);
  }
  if (r.kind <= STOP_SENDING) {
    return __stream_control(r);
  }
  if (r.kind <= NEW_TOKEN) {
    return __handshake_data(r);
  }
  if (r.kind <= STREAMS_BLOCKED_UNI) {
    return __limits(r);
  }
  return __rest(r);
}

function __ack(own r: Reading): Reading {
  r = __int(r, "largest acknowledged");
  let largest: i64 = r.n;
  r = __int(r, "ack delay");
  let delay: i64 = r.n;
  r = __int(r, "ack range count");
  let count: i64 = r.n;
  r = __int(r, "first ack range");
  let first: i64 = r.n;
  if (!r.ok) {
    return r;
  }
  if (first > largest) {
    return __fail(r, "first ack range below packet 0");
  }
  let (r1, ranges) = __ack_ranges(r, count, [AckRange { low: largest - first, high: largest }]);
  let (r2, ecn) = __ecn(r1);
  if (!r2.ok) {
    return r2;
  }
  return __done(r2, AckFrame(Ack { delay: delay, ranges: ranges, ecn: ecn }));
}

function __ecn(own r: Reading): (Reading, Option[Ecn]) {
  if (!r.ok || r.kind == ACK) {
    return (r, None);
  }
  r = __int(r, "ECT(0) count");
  let e0: i64 = r.n;
  r = __int(r, "ECT(1) count");
  let e1: i64 = r.n;
  r = __int(r, "ECN-CE count");
  if (!r.ok) {
    return (r, None);
  }
  let ce: i64 = r.n;
  return (r, Some(Ecn { ect0: e0, ect1: e1, ce: ce }));
}

function __ack_ranges(own r: Reading, count: i64, own ranges: AckRange[]): (Reading, AckRange[]) {
  let i: i64 = 0;
  while (i < count) {
    r = __int(r, "ack gap");
    let gap: i64 = r.n;
    r = __int(r, "ack range length");
    let len: i64 = r.n;
    if (!r.ok) {
      return (r, ranges);
    }
    let high: i64 = ranges[ranges.len() - 1].low - gap - 2;
    if (high < 0 || len > high) {
      return (__fail(r, "ack range below packet 0"), ranges);
    }
    ranges = ranges.append(AckRange { low: high - len, high: high });
    i = i + 1;
  }
  return (r, ranges);
}

function __stream(own r: Reading): Reading {
  let kind: i64 = r.kind;
  r = __int(r, "stream id");
  let id: i64 = r.n;
  let offset: i64 = 0;
  if ((kind & 4) != 0) {
    r = __int(r, "stream offset");
    offset = r.n;
  }
  let len: i64 = (r.buf.len() - r.at) as i64;
  if ((kind & 2) != 0) {
    r = __int(r, "stream length");
    len = r.n;
  }
  if (!r.ok) {
    return r;
  }
  if (offset + len > MAX_VARINT) {
    return __fail(r, "stream data past 2^62-1");
  }
  r = __take(r, len, "stream data");
  if (!r.ok) {
    return r;
  }
  let data: u8[] = r.bytes;
  return __done(r, StreamFrame(StreamData { stream: id, offset: offset, data: data, fin: (kind & 1) != 0 }));
}
```

### `14 function __stream_control(own r: Reading): Reading`

```fern
<<14 function __stream_control(own r: Reading): Reading>>=
function __stream_control(own r: Reading): Reading {
  r = __int(r, "stream id");
  let id: i64 = r.n;
  r = __int(r, "error code");
  let code: i64 = r.n;
  if (r.kind == STOP_SENDING) {
    if (!r.ok) {
      return r;
    }
    return __done(r, StopSending(id, code));
  }
  r = __int(r, "final size");
  if (!r.ok) {
    return r;
  }
  let size: i64 = r.n;
  return __done(r, ResetStreamFrame(ResetStream { stream: id, code: code, final_size: size }));
}

function __handshake_data(own r: Reading): Reading {
  if (r.kind == CRYPTO) {
    r = __int(r, "crypto offset");
    let offset: i64 = r.n;
    r = __int(r, "crypto length");
    let len: i64 = r.n;
    if (!r.ok) {
      return r;
    }
    if (offset + len > MAX_VARINT) {
      return __fail(r, "crypto data past 2^62-1");
    }
    r = __take(r, len, "crypto data");
    if (!r.ok) {
      return r;
    }
    let data: u8[] = r.bytes;
    return __done(r, Crypto(offset, data));
  }
  r = __int(r, "token length");
  if (!r.ok) {
    return r;
  }
  if (r.n == 0) {
    return __fail(r, "an empty NEW_TOKEN");
  }
  let n: i64 = r.n;
  r = __take(r, n, "token");
  if (!r.ok) {
    return r;
  }
  let token: u8[] = r.bytes;
  return __done(r, NewToken(token));
}

function __limits(own r: Reading): Reading {
  r = __int(r, "value");
  if (!r.ok) {
    return r;
  }
  let v: i64 = r.n;
  if (r.kind == MAX_DATA) {
    return __done(r, MaxData(v));
  }
  if (r.kind == DATA_BLOCKED) {
    return __done(r, DataBlocked(v));
  }
  if (r.kind == MAX_STREAM_DATA || r.kind == STREAM_DATA_BLOCKED) {
    return __stream_limit(r, v);
  }
  return __stream_count(r, v);
}

function __stream_limit(own r: Reading, id: i64): Reading {
  r = __int(r, "value");
  if (!r.ok) {
    return r;
  }
  let w: i64 = r.n;
  if (r.kind == MAX_STREAM_DATA) {
    return __done(r, MaxStreamData(id, w));
  }
  return __done(r, StreamDataBlocked(id, w));
}

function __stream_count(own r: Reading, v: i64): Reading {
  if (v > 1152921504606846976) {
    return __fail(r, "a stream count over 2^60");
  }
  let bidi: boolean = r.kind == MAX_STREAMS_BIDI || r.kind == STREAMS_BLOCKED_BIDI;
  if (r.kind == MAX_STREAMS_BIDI || r.kind == MAX_STREAMS_UNI) {
    return __done(r, MaxStreams(bidi, v));
  }
  return __done(r, StreamsBlocked(bidi, v));
}

function __rest(own r: Reading): Reading {
  if (r.kind == NEW_CONNECTION_ID) {
    return __new_connection_id(r);
  }
  if (r.kind == RETIRE_CONNECTION_ID) {
    r = __int(r, "sequence number");
    if (!r.ok) {
      return r;
    }
    let seq: i64 = r.n;
    return __done(r, RetireConnectionId(seq));
  }
  if (r.kind == PATH_CHALLENGE || r.kind == PATH_RESPONSE) {
    return __path(r);
  }
  if (r.kind == CONNECTION_CLOSE || r.kind == CONNECTION_CLOSE_APP) {
    return __close(r);
  }
  return __fail(r, "unknown frame type");
}

function __path(own r: Reading): Reading {
  r = __take(r, 8 as i64, "path data");
  if (!r.ok) {
    return r;
  }
  let data: u8[] = r.bytes;
  if (r.kind == PATH_CHALLENGE) {
    return __done(r, PathChallenge(data));
  }
  return __done(r, PathResponse(data));
}
```

### `15 function __close(own r: Reading): Reading`

```fern
<<15 function __close(own r: Reading): Reading>>=
function __close(own r: Reading): Reading {
  r = __int(r, "error code");
  let code: i64 = r.n;
  let frame_type: i64 = 0;
  if (r.kind == CONNECTION_CLOSE) {
    r = __int(r, "frame type");
    frame_type = r.n;
  }
  r = __int(r, "reason length");
  if (!r.ok) {
    return r;
  }
  let len: i64 = r.n;
  r = __take(r, len, "reason phrase");
  if (!r.ok) {
    return r;
  }
  let reason: string = utf8.from_bytes_lossy(r.bytes);
  let app: boolean = r.kind == CONNECTION_CLOSE_APP;
  return __done(r, ConnectionCloseFrame(ConnectionClose { code: code, frame_type: frame_type, reason: reason, app: app }));
}

function __new_connection_id(own r: Reading): Reading {
  r = __int(r, "sequence number");
  let seq: i64 = r.n;
  r = __int(r, "retire prior to");
  let prior: i64 = r.n;
  if (!r.ok) {
    return r;
  }
  if (prior > seq) {
    return __fail(r, "retire prior to " + prior.to_string() + " past sequence number " + seq.to_string());
  }
  if (r.at >= r.buf.len()) {
    return __fail(r, "connection id length cut short");
  }
  let n: i64 = r.buf[r.at] as i64;
  if (n < 1 || n > 20) {
    return __fail(r, "a connection id of " + n.to_string() + " bytes");
  }
  r = __take(Reading { ...r, at: r.at + 1 }, n, "connection id");
  let id: u8[] = r.bytes;
  r = __take(r, 16 as i64, "stateless reset token");
  if (!r.ok) {
    return r;
  }
  let token: u8[] = r.bytes;
  return __done(r, NewConnectionIdFrame(NewConnectionId { sequence: seq, retire_prior_to: prior, id: id, reset_token: token }));
}
```

### `16 pub function encode(f: Frame): u8[]`

```fern
<<16 pub function encode(f: Frame): u8[]>>=
pub function encode(f: Frame): u8[] {
  return put(__alloc_empty(), f);
}

pub function put(own out: u8[], f: Frame): u8[] {
  match (f) {
    Padding(n) => {
      let i: i32 = 0;
      while (i < n) {
        out = out.append(0 as u8);
        i = i + 1;
      }
      return out;
    },
    Ping => {
      return out.append(1 as u8);
    },
    HandshakeDone => {
      return out.append(30 as u8);
    },
    AckFrame(a) => {
      return __put_ack(out, a);
    },
    StreamFrame(s) => {
      return put_stream_head(out, s.stream, s.offset, s.data.len(), s.fin).concat(s.data);
    },
    Crypto(offset, data) => {
      return put_varint(put_varint(put_varint(out, CRYPTO), offset), data.len() as i64).concat(data);
    },
    _ => {
      return __put_rest(out, f);
    }
  }
  return out;
}
```

### `17 function __put_rest(own out: u8[], f: Frame): u8[]`

```fern
<<17 function __put_rest(own out: u8[], f: Frame): u8[]>>=
function __put_rest(own out: u8[], f: Frame): u8[] {
  match (f) {
    ResetStreamFrame(r) => {
      return put_varint(put_varint(put_varint(put_varint(out, RESET_STREAM), r.stream), r.code), r.final_size);
    },
    StopSending(id, code) => {
      return put_varint(put_varint(put_varint(out, STOP_SENDING), id), code);
    },
    NewToken(token) => {
      return put_varint(put_varint(out, NEW_TOKEN), token.len() as i64).concat(token);
    },
    MaxData(v) => {
      return put_varint(put_varint(out, MAX_DATA), v);
    },
    MaxStreamData(id, v) => {
      return put_varint(put_varint(put_varint(out, MAX_STREAM_DATA), id), v);
    },
    MaxStreams(bidi, v) => {
      return put_varint(put_varint(out, __pick(bidi, MAX_STREAMS_BIDI, MAX_STREAMS_UNI)), v);
    },
    DataBlocked(v) => {
      return put_varint(put_varint(out, DATA_BLOCKED), v);
    },
    StreamDataBlocked(id, v) => {
      return put_varint(put_varint(put_varint(out, STREAM_DATA_BLOCKED), id), v);
    },
    StreamsBlocked(bidi, v) => {
      return put_varint(put_varint(out, __pick(bidi, STREAMS_BLOCKED_BIDI, STREAMS_BLOCKED_UNI)), v);
    },
    _ => {
      return __put_ids(out, f);
    }
  }
  return out;
}

function __put_ids(own out: u8[], f: Frame): u8[] {
  match (f) {
    NewConnectionIdFrame(n) => {
      out = put_varint(put_varint(put_varint(out, NEW_CONNECTION_ID), n.sequence), n.retire_prior_to);
      return out.append(n.id.len() as u8).concat(n.id).concat(n.reset_token);
    },
    RetireConnectionId(seq) => {
      return put_varint(put_varint(out, RETIRE_CONNECTION_ID), seq);
    },
    PathChallenge(data) => {
      return put_varint(out, PATH_CHALLENGE).concat(data);
    },
    PathResponse(data) => {
      return put_varint(out, PATH_RESPONSE).concat(data);
    },
    ConnectionCloseFrame(cc) => {
      let reason: u8[] = cc.reason.bytes();
      if (cc.app) {
        return put_varint(put_varint(put_varint(out, CONNECTION_CLOSE_APP), cc.code), reason.len() as i64).concat(reason);
      }
      return put_varint(put_varint(put_varint(put_varint(out, CONNECTION_CLOSE), cc.code), cc.frame_type), reason.len() as i64).concat(reason);
    },
    _ => {
      return out;
    }
  }
  return out;
}
```

### `18 pub function put_stream_head(own out: u8[], stream: i64, offset: i64, len: i32, fin: boolean): u8[]`

```fern
<<18 pub function put_stream_head(own out: u8[], stream: i64, offset: i64, len: i32, fin: boolean): u8[]>>=
pub function put_stream_head(own out: u8[], stream: i64, offset: i64, len: i32, fin: boolean): u8[] {
  let kind: i64 = STREAM | 2;
  if (offset > 0) {
    kind = kind | 4;
  }
  if (fin) {
    kind = kind | 1;
  }
  out = put_varint(put_varint(out, kind), stream);
  if (offset > 0) {
    out = put_varint(out, offset);
  }
  return put_varint(out, len as i64);
}

pub function stream_overhead(stream: i64, offset: i64, len: i32): i32 {
  let n: i32 = 1 + varint_len(stream) + varint_len(len as i64);
  if (offset > 0) {
    n = n + varint_len(offset);
  }
  return n;
}
```

### `19 function __put_ack(own out: u8[], a: Ack): u8[]`

```fern
<<19 function __put_ack(own out: u8[], a: Ack): u8[]>>=
function __put_ack(own out: u8[], a: Ack): u8[] {
  let kind: i64 = ACK;
  if (a.ecn.is_some()) {
    kind = ACK_ECN;
  }
  out = __put_ack_ranges(out, kind, a.delay, a.ranges);
  match (a.ecn) {
    Some(e) => {
      return put_varint(put_varint(put_varint(out, e.ect0), e.ect1), e.ce);
    },
    None => {
      return out;
    }
  }
  return out;
}
```

### `20 pub function (f: Frame) ack_eliciting(): boolean`

```fern
<<20 pub function (f: Frame) ack_eliciting(): boolean>>=
// put_ack writes an ACK frame without ECN counts, as put would write
// AckFrame(Ack { delay: delay, ranges: ranges, ecn: None }).
pub function put_ack(own out: u8[], delay: i64, ranges: AckRange[]): u8[] {
  return __put_ack_ranges(out, ACK, delay, ranges);
}

function __put_ack_ranges(own out: u8[], kind: i64, delay: i64, ranges: AckRange[]): u8[] {
  let top: AckRange = ranges[0];
  out = put_varint(put_varint(put_varint(put_varint(put_varint(out, kind), top.high), delay), (ranges.len() - 1) as i64), top.high - top.low);
  let i: i32 = 1;
  while (i < ranges.len()) {
    out = put_varint(put_varint(out, ranges[i - 1].low - ranges[i].high - 2), ranges[i].high - ranges[i].low);
    i = i + 1;
  }
  return out;
}

pub function (f: Frame) ack_eliciting(): boolean {
  match (f) {
    AckFrame(_) => {
      return false;
    },
    Padding(_) => {
      return false;
    },
    ConnectionCloseFrame(_) => {
      return false;
    },
    _ => {
      return true;
    }
  }
  return true;
}

pub function (f: Frame) permitted(space: i32): boolean {
  if (space == IN_ONE_RTT) {
    return true;
  }
  match (f) {
    Padding(_) => {
      return true;
    },
    Ping => {
      return true;
    },
    ConnectionCloseFrame(cc) => {
      return space == IN_ZERO_RTT || !cc.app;
    },
    AckFrame(_) => {
      return space != IN_ZERO_RTT;
    },
    Crypto(_, _) => {
      return space != IN_ZERO_RTT;
    },
    _ => {
      return space == IN_ZERO_RTT && __zero_rtt_ok(f);
    }
  }
  return false;
}
```

### `21 function __zero_rtt_ok(f: Frame): boolean`

```fern
<<21 function __zero_rtt_ok(f: Frame): boolean>>=
function __zero_rtt_ok(f: Frame): boolean {
  match (f) {
    HandshakeDone => {
      return false;
    },
    NewToken(_) => {
      return false;
    },
    PathResponse(_) => {
      return false;
    },
    RetireConnectionId(_) => {
      return false;
    },
    _ => {
      return true;
    }
  }
  return true;
}

function __pick(cond: boolean, a: i64, b: i64): i64 {
  if (cond) {
    return a;
  }
  return b;
}

function __alloc_empty(): u8[] {
  let out: u8[] = __alloc_u8(64);
  __arr_set_len(out, 0);
  return out;
}

function __slice(buf: u8[], from: i32, to: i32): u8[] {
  return __bytes_range(buf, from, to);
}
```
