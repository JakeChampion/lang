# QUIC stream flow control

This module controls QUIC streams and flow limits.

## Model

Reassembly combines stream fragments by byte offset and exposes contiguous data. Stream state also tracks open limits, reset state, and flow-control credit.

## Contracts

Delivered bytes and retained fragments have separate counts. Stream and connection limits both constrain progress. A reset does not imply ordinary end of input.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct Reassembly>>

<<4 pub function reassembly(): Reassembly>>

<<5 function (r: Reassembly) __discard(): Reassembly>>

<<6 struct Send>>

<<7 pub function table(server: boolean, local: params.Params): Streams>>

<<8 function (s: Streams) __local_type(uni: boolean): i32>>

<<9 pub function (s: Streams) can_open(uni: boolean): boolean>>

<<10 function (s: Streams) __refuse(why: string): Streams>>

<<11 pub function (s: Streams) open(uni: boolean): Streams>>

<<12 function __reset_send(st: Stream, code: i64): Stream>>

<<13 pub function owns(f: frames.Frame): boolean>>

<<14 function (s: Streams) __fail(e: frames.FrameError): Streams>>

<<15 function (s: Streams) __on_stop(id: i64, code: i64): Streams>>

<<16 pub function (s: Streams) fill(out: u8[], room: i32): Streams>>

<<17 function (s: Streams) __add(room: i32, f: frames.Frame): Streams>>

<<18 function __resend_lost(id: i64, snd: Send, out: u8[], room: i32, resend: recovery.Resend[]): (Send, u8[], r...>>

<<19 pub function (s: Streams) blocked_open(uni: boolean): Streams>>

<<20 function __acked_range(snd: Send, offset: i64, len: i32, fin: boolean): Send>>

<<21 pub function (s: Streams) lost(item: recovery.Resend): Streams>>

<<22 function (s: Streams) __lost_frame(f: frames.Frame): Streams>>

<<23 function __rest_i64(xs: i64[]): i64[]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module controls QUIC streams and flow limits.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/quic/frames";
import "std/quic/params";
import "std/quic/recovery";
import "std/option";
import "std/i64";
```

### `3 pub struct Reassembly`

```fern
<<3 pub struct Reassembly>>=
pub struct Reassembly { taken: i64, ready: u8[], held_at: i64[], held: u8[][], held_bytes: i64 }
```

### `4 pub function reassembly(): Reassembly`

```fern
<<4 pub function reassembly(): Reassembly>>=
pub function reassembly(): Reassembly {
  return Reassembly { taken: 0 as i64, ready: [], held_at: [], held: [], held_bytes: 0 as i64 };
}

pub function (r: Reassembly) delivered(): i64 {
  return r.taken + r.ready.len() as i64;
}

pub function (r: Reassembly) add(offset: i64, data: u8[]): Reassembly {
  let front: i64 = r.delivered();
  let end: i64 = offset + data.len() as i64;
  if (end <= front) {
    return r;
  }
  if (offset > front) {
    return __hold(r, offset, data);
  }
  let skip: i32 = (front - offset) as i32;
  if (skip == 0 && r.ready.len() == 0) {
    return __drain(Reassembly { ...r, ready: data });
  }
  return __drain(Reassembly { ...r, ready: __append_range(r.ready, data, skip, data.len()) });
}

pub function (r: Reassembly) take(): (Reassembly, u8[]) {
  return (Reassembly { ...r, taken: r.delivered(), ready: [] }, r.ready);
}
```

### `5 function (r: Reassembly) __discard(): Reassembly`

```fern
<<5 function (r: Reassembly) __discard(): Reassembly>>=
function (r: Reassembly) __discard(): Reassembly {
  return Reassembly { ...r, ready: [], held_at: [], held: [], held_bytes: 0 as i64 };
}

function __hold(r: Reassembly, offset: i64, data: u8[]): Reassembly {
  let end: i64 = offset + data.len() as i64;
  let cur: i64 = offset;
  let at: i64[] = [];
  let held: u8[][] = [];
  let i: i32 = 0;
  while (i < r.held.len()) {
    let s: i64 = r.held_at[i];
    let e: i64 = s + r.held[i].len() as i64;
    if (cur < end && s > cur) {
      let piece: i64 = __min(end, s);
      at = at.append(cur);
      held = held.append(__slice_range(data, (cur - offset) as i32, (piece - offset) as i32));
      cur = piece;
    }
    if (s <= cur && e > cur) {
      cur = e;
    }
    at = at.append(s);
    held = held.append(r.held[i]);
    i = i + 1;
  }
  if (cur < end) {
    at = at.append(cur);
    held = held.append(__slice_range(data, (cur - offset) as i32, data.len()));
  }
  return Reassembly { ...r, held_at: at, held: held, held_bytes: __total(held) };
}

function __drain(r: Reassembly): Reassembly {
  let cur: Reassembly = r;
  while (cur.held.len() > 0 && cur.held_at[0] <= cur.delivered()) {
    let front: i64 = cur.delivered();
    let seg: u8[] = cur.held[0];
    let ready: u8[] = cur.ready;
    let skip: i64 = front - cur.held_at[0];
    if (skip < seg.len() as i64) {
      ready = __append_range(ready, seg, skip as i32, seg.len());
    }
    let held: u8[][] = __rest(cur.held);
    cur = Reassembly { ...cur, ready: ready, held_at: __rest_i64(cur.held_at), held: held, held_bytes: __total(held) };
  }
  return cur;
}
```

### `6 struct Send`

```fern
<<6 struct Send>>=
struct Send {
  buf: u8[],
  base: i64,
  sent: i64,
  acked: i64[],
  lost: i64[],
  max: i64,
  fin: boolean,
  fin_sent: boolean,
  fin_acked: boolean,
  reset: i64,
  reset_state: i32,
  blocked_at: i64,
}

struct Recv {
  data: Reassembly,
  highest: i64,
  final_size: i64,
  max: i64,
  window: i64,
  max_dirty: boolean,
  reset: i64,
  stop: i64,
  stop_state: i32,
  fin_read: boolean,
}

const NONE: i32 = 0;

const TO_SEND: i32 = 1;

const SENT: i32 = 2;

const ACKED: i32 = 3;

struct Stream { id: i64, has_send: boolean, send: Send, has_recv: boolean, recv: Recv, closed: boolean }

pub enum Event { Opened(i64), Readable(i64), ResetByPeer(i64, i64), StopRequested(i64, i64) }

pub struct Streams {
  server: boolean,
  list: Stream[],
  local: params.Params,
  peer: params.Params,
  limits: i64[],
  opened: i64[],
  limits_dirty: boolean[],
  open_blocked: i64[],
  recv_max: i64,
  recv_window: i64,
  recv_highest: i64,
  recv_taken: i64,
  recv_max_dirty: boolean,
  send_max: i64,
  send_sent: i64,
  blocked_at: i64,
  next: i32,
  events: Event[],
  refused: string,
  failed: Option[frames.FrameError],
  last_opened: i64,
  read_data: u8[],
  read_fin: boolean,
  filled: u8[],
  resend: recovery.Resend[],
  fit: boolean,
  retired: i32,
  index: i32[],
  base: i64,
}
```

### `7 pub function table(server: boolean, local: params.Params): Streams`

```fern
<<7 pub function table(server: boolean, local: params.Params): Streams>>=
pub function table(server: boolean, local: params.Params): Streams {
  let none: params.Params = params.defaults();
  let s: Streams = Streams { server: server, list: [], local: local, peer: none, limits: [0 as i64, 0 as i64, 0 as i64, 0 as i64], opened: [0 as i64, 0 as i64, 0 as i64, 0 as i64], limits_dirty: [false, false, false, false], open_blocked: [0 - 1 as i64, 0 - 1 as i64, 0 - 1 as i64, 0 - 1 as i64], recv_max: local.initial_max_data, recv_window: local.initial_max_data, recv_highest: 0 as i64, recv_taken: 0 as i64, recv_max_dirty: false, send_max: 0 as i64, send_sent: 0 as i64, blocked_at: 0 - 1 as i64, next: 0, events: [], refused: "", failed: None, last_opened: 0 - 1 as i64, read_data: [], read_fin: false, filled: [], resend: [], fit: false, retired: 0, index: [], base: 0 as i64 };
  let bidi: i32 = s.__peer_type(false);
  return Streams { ...s, limits: s.limits.with(bidi, local.initial_max_streams_bidi).with(bidi + 2, local.initial_max_streams_uni) };
}

pub function (s: Streams) with_peer(p: params.Params): Streams {
  let mine: i32 = s.__local_type(false);
  let cur: Streams = Streams { ...s, peer: p, send_max: __max(s.send_max, p.initial_max_data), limits: s.limits.with(mine, __max(s.limits[mine], p.initial_max_streams_bidi)).with(mine + 2, __max(s.limits[mine + 2], p.initial_max_streams_uni)) };
  let list: Stream[] = [];
  for st in cur.list {
    let next: Stream = st;
    if (st.has_send) {
      next = Stream { ...st, send: Send { ...st.send, max: __max(st.send.max, cur.__send_window(st.id)) } };
    }
    list = list.append(next);
  }
  return Streams { ...cur, list: list };
}
```

### `8 function (s: Streams) __local_type(uni: boolean): i32`

```fern
<<8 function (s: Streams) __local_type(uni: boolean): i32>>=
function (s: Streams) __local_type(uni: boolean): i32 {
  let t: i32 = 0;
  if (s.server) {
    t = 1;
  }
  if (uni) {
    t = t + 2;
  }
  return t;
}

function (s: Streams) __peer_type(uni: boolean): i32 {
  return s.__local_type(uni) ^ 1;
}

function (s: Streams) __is_local(id: i64): boolean {
  return (id & 1) == s.__local_type(false) as i64;
}

function __is_uni(id: i64): boolean {
  return (id & 2) != 0;
}

function (s: Streams) __recv_window(id: i64): i64 {
  if (__is_uni(id)) {
    return s.local.initial_max_stream_data_uni;
  }
  if (s.__is_local(id)) {
    return s.local.initial_max_stream_data_bidi_local;
  }
  return s.local.initial_max_stream_data_bidi_remote;
}

function (s: Streams) __send_window(id: i64): i64 {
  if (__is_uni(id)) {
    return s.peer.initial_max_stream_data_uni;
  }
  if (s.__is_local(id)) {
    return s.peer.initial_max_stream_data_bidi_remote;
  }
  return s.peer.initial_max_stream_data_bidi_local;
}

function (s: Streams) __new_stream(id: i64): Stream {
  let window: i64 = s.__recv_window(id);
  let send: Send = Send { buf: [], base: 0 as i64, sent: 0 as i64, acked: [], lost: [], max: s.__send_window(id), fin: false, fin_sent: false, fin_acked: false, reset: 0 - 1 as i64, reset_state: NONE, blocked_at: 0 - 1 as i64 };
  let recv: Recv = Recv { data: reassembly(), highest: 0 as i64, final_size: 0 - 1 as i64, max: window, window: window, max_dirty: false, reset: 0 - 1 as i64, stop: 0 - 1 as i64, stop_state: NONE, fin_read: false };
  let local: boolean = s.__is_local(id);
  return Stream { id: id, has_send: local || !__is_uni(id), send: send, has_recv: !local || !__is_uni(id), recv: recv, closed: false };
}

function (s: Streams) __find(id: i64): i32 {
  if ((id & 2) != 0) {
    let i: i32 = s.list.len() - 1;
    while (i >= 0) {
      if (s.list[i].id == id) {
        if (s.list[i].closed) {
          return 0 - 1;
        }
        return i;
      }
      i = i - 1;
    }
    return 0 - 1;
  }
  let k: i64 = __bidi_key(id) - s.base;
  if (k < 0) {
    return s.__scan(id);
  }
  if (k >= s.index.len() as i64) {
    return 0 - 1;
  }
  let i: i32 = s.index[k as i32] - 1;
  if (i < 0 || s.list[i].closed) {
    return 0 - 1;
  }
  return i;
}

function (s: Streams) __scan(id: i64): i32 {
  let i: i32 = s.list.len() - 1;
  while (i >= 0) {
    if (s.list[i].id == id) {
      if (s.list[i].closed) {
        return 0 - 1;
      }
      return i;
    }
    i = i - 1;
  }
  return 0 - 1;
}

function __bidi_key(id: i64): i64 {
  return (id >> 2) * 2 + (id & 1);
}

function (s: Streams) __indexed(id: i64, pos: i32): Streams {
  if ((id & 2) != 0) {
    return s;
  }
  let k: i64 = __bidi_key(id) - s.base;
  if (k < 0) {
    return s;
  }
  if (k >= s.index.len() as i64) {
    let grown: Streams = Streams { ...s, index: __grown(s.index, k as i32 + 1) };
    return Streams { ...grown, index: grown.index.with(k as i32, pos + 1) };
  }
  return Streams { ...s, index: s.index.with(k as i32, pos + 1) };
}

function __grown(index: i32[], n: i32): i32[] {
  let size: i32 = n * 2;
  if (size < 16) {
    size = 16;
  }
  let out: i32[] = __alloc_i32(size);
  let i: i32 = 0;
  while (i < index.len()) {
    out = out.with(i, index[i]);
    i = i + 1;
  }
  return out;
}

function (s: Streams) __put(i: i32, st: Stream): Streams {
  return Streams { ...s, list: s.list.with(i, st) };
}
```

### `9 pub function (s: Streams) can_open(uni: boolean): boolean`

```fern
<<9 pub function (s: Streams) can_open(uni: boolean): boolean>>=
pub function (s: Streams) can_open(uni: boolean): boolean {
  let t: i32 = s.__local_type(uni);
  return s.opened[t] < s.limits[t];
}
```

### `10 function (s: Streams) __refuse(why: string): Streams`

```fern
<<10 function (s: Streams) __refuse(why: string): Streams>>=
function (s: Streams) __refuse(why: string): Streams {
  return Streams { ...s, refused: why };
}
```

### `11 pub function (s: Streams) open(uni: boolean): Streams`

```fern
<<11 pub function (s: Streams) open(uni: boolean): Streams>>=
pub function (s: Streams) open(uni: boolean): Streams {
  let t: i32 = s.__local_type(uni);
  if (s.opened[t] >= s.limits[t]) {
    return Streams { ...s, refused: "the peer's limit on streams is reached", last_opened: 0 - 1 as i64 };
  }
  let id: i64 = s.opened[t] * 4 + t as i64;
  let cur: Streams = Streams { ...s, list: s.list.append(s.__new_stream(id)), opened: s.opened.with(t, s.opened[t] + 1), refused: "", last_opened: id };
  return cur.__indexed(id, cur.list.len() - 1);
}

pub function (s: Streams) write(id: i64, data: [u8], fin: boolean): Streams {
  let i: i32 = s.__find(id);
  if (i < 0 || !s.list[i].has_send) {
    return s.__refuse("no stream to write to");
  }
  let st: Stream = s.list[i];
  if (st.send.fin || st.send.reset >= 0) {
    return s.__refuse("the stream's sending half is closed");
  }
  let buf: u8[] = __copy(data);
  if (st.send.buf.len() > 0) {
    buf = st.send.buf.concat(buf);
  }
  return s.__refuse("").__put(i, Stream { ...st, send: Send { ...st.send, buf: buf, fin: fin } });
}

pub function (s: Streams) read(id: i64): Streams {
  let i: i32 = s.__find(id);
  if (i < 0 || !s.list[i].has_recv) {
    return s.__refuse("no stream to read from");
  }
  let st: Stream = s.list[i];
  if (st.recv.reset >= 0) {
    return s.__refuse("the peer reset the stream");
  }
  let (data, bytes) = st.recv.data.take();
  let fin: boolean = st.recv.final_size >= 0 && data.taken == st.recv.final_size;
  let cur: Streams = Streams { ...s, recv_taken: s.recv_taken + bytes.len() as i64, refused: "", read_data: bytes, read_fin: fin }.__put(i, Stream { ...st, recv: Recv { ...st.recv, data: data, fin_read: fin } });
  return cur.__retire_done(i);
}

pub function (s: Streams) reset(id: i64, code: i64): Streams {
  let i: i32 = s.__find(id);
  if (i < 0 || !s.list[i].has_send) {
    return s.__refuse("no stream to reset");
  }
  return s.__refuse("").__put(i, __reset_send(s.list[i], code));
}

pub function (s: Streams) stop(id: i64, code: i64): Streams {
  let i: i32 = s.__find(id);
  if (i < 0 || !s.list[i].has_recv) {
    return s.__refuse("no stream to stop");
  }
  let st: Stream = s.list[i];
  if (st.recv.stop >= 0 || st.recv.reset >= 0 || st.recv.fin_read) {
    return s.__refuse("");
  }
  return s.__refuse("").__put(i, Stream { ...st, recv: Recv { ...st.recv, stop: code, stop_state: TO_SEND, data: st.recv.data.__discard() } });
}

pub function (s: Streams) buffered(id: i64): i64 {
  let i: i32 = s.__find(id);
  if (i < 0) {
    return 0 as i64;
  }
  return s.list[i].send.buf.len() as i64;
}

pub function (s: Streams) events_taken(): Streams {
  return Streams { ...s, events: [] };
}
```

### `12 function __reset_send(st: Stream, code: i64): Stream`

```fern
<<12 function __reset_send(st: Stream, code: i64): Stream>>=
function __reset_send(st: Stream, code: i64): Stream {
  if (st.send.reset >= 0 || st.send.fin_acked && st.send.buf.len() == 0) {
    return st;
  }
  return Stream { ...st, send: Send { ...st.send, buf: [], lost: [], acked: [], base: st.send.sent, reset: code, reset_state: TO_SEND } };
}
```

### `13 pub function owns(f: frames.Frame): boolean`

```fern
<<13 pub function owns(f: frames.Frame): boolean>>=
pub function owns(f: frames.Frame): boolean {
  match (f) {
    frames.StreamFrame(_) | frames.ResetStreamFrame(_) | frames.StopSending(_, _) | frames.MaxData(_) | frames.MaxStreamData(_, _) | frames.MaxStreams(_, _) | frames.DataBlocked(_) | frames.StreamDataBlocked(_, _) | frames.StreamsBlocked(_, _) => {
      return true;
    },
    _ => {
      return false;
    }
  }
}

pub function (s: Streams) on_frame(f: frames.Frame): Streams {
  let cur: Streams = Streams { ...s, failed: None };
  match (f) {
    frames.StreamFrame(d) => {
      return cur.__on_stream(d);
    },
    frames.ResetStreamFrame(r) => {
      return cur.__on_reset(r);
    },
    frames.StopSending(id, code) => {
      return cur.__on_stop(id, code);
    },
    frames.MaxStreamData(id, v) => {
      return cur.__on_max_stream_data(id, v);
    },
    frames.StreamDataBlocked(id, _) => {
      let (next, _) = cur.__stream_for(id, true, frames.STREAM_DATA_BLOCKED);
      return next;
    },
    _ => {
      return cur.__on_connection_frame(f);
    }
  }
}
```

### `14 function (s: Streams) __fail(e: frames.FrameError): Streams`

```fern
<<14 function (s: Streams) __fail(e: frames.FrameError): Streams>>=
function (s: Streams) __fail(e: frames.FrameError): Streams {
  return Streams { ...s, failed: Some(e) };
}

function (s: Streams) __on_connection_frame(f: frames.Frame): Streams {
  match (f) {
    frames.MaxData(v) => {
      return Streams { ...s, send_max: __max(s.send_max, v) };
    },
    frames.MaxStreams(bidi, v) => {
      let t: i32 = s.__local_type(!bidi);
      return Streams { ...s, limits: s.limits.with(t, __max(s.limits[t], v)) };
    },
    _ => {
      return s;
    }
  }
}

function (s: Streams) __stream_for(id: i64, recv: boolean, kind: i64): (Streams, i32) {
  let local: boolean = s.__is_local(id);
  if (__is_uni(id) && local == recv) {
    return (s.__fail(__error(frames.STREAM_STATE_ERROR, kind, "a frame for the half of a unidirectional stream that does not exist")), 0 - 1);
  }
  let t: i32 = (id & 3) as i32;
  let n: i64 = id / 4;
  if (local && n >= s.opened[t]) {
    return (s.__fail(__error(frames.STREAM_STATE_ERROR, kind, "a frame for a stream this side has not opened")), 0 - 1);
  }
  if (!local && n >= s.limits[t]) {
    return (s.__fail(__error(frames.STREAM_LIMIT_ERROR, kind, "a stream past the limit advertised")), 0 - 1);
  }
  let cur: Streams = s;
  if (!local && n >= s.opened[t]) {
    cur = s.__peer_opens(t, n);
  }
  let i: i32 = cur.__find(id);
  return (cur, i);
}

function (s: Streams) __peer_opens(t: i32, n: i64): Streams {
  let cur: Streams = s;
  let k: i64 = s.opened[t];
  while (k <= n) {
    let id: i64 = k * 4 + t as i64;
    cur = Streams { ...cur, list: cur.list.append(cur.__new_stream(id)), events: cur.events.append(Opened(id)) };
    cur = cur.__indexed(id, cur.list.len() - 1);
    k = k + 1;
  }
  return Streams { ...cur, opened: cur.opened.with(t, n + 1) };
}

function (s: Streams) __on_stream(d: frames.StreamData): Streams {
  let (cur, i) = s.__stream_for(d.stream, true, frames.STREAM);
  if (i < 0) {
    return cur;
  }
  let st: Stream = cur.list[i];
  let end: i64 = d.offset + d.data.len() as i64;
  if let Some(e) = __final_error(st.recv, end, d.fin, frames.STREAM) {
    return cur.__fail(e);
  }
  if let Some(e) = cur.__credit_error(st.recv, end, frames.STREAM) {
    return cur.__fail(e);
  }
  let r: Recv = __with_final(st.recv, end, d.fin);
  let more: i64 = __max(0 as i64, end - r.highest);
  let next: Streams = Streams { ...cur, recv_highest: cur.recv_highest + more };
  if (r.reset >= 0 || r.stop >= 0) {
    return next.__put(i, Stream { ...st, recv: Recv { ...r, highest: r.highest + more } });
  }
  let before: i64 = r.data.delivered();
  let data: Reassembly = r.data.add(d.offset, d.data);
  let readable: boolean = data.delivered() > before || d.fin && !r.fin_read && data.delivered() == r.final_size;
  let events: Event[] = next.events;
  if (readable) {
    events = events.append(Readable(d.stream));
  }
  return Streams { ...next, events: events }.__put(i, Stream { ...st, recv: Recv { ...r, data: data, highest: r.highest + more } });
}

function __final_error(r: Recv, end: i64, fin: boolean, kind: i64): Option[frames.FrameError] {
  if (r.final_size >= 0 && (end > r.final_size || fin && end != r.final_size)) {
    return Some(__error(frames.FINAL_SIZE_ERROR, kind, "data past the stream's final size, or a different final size"));
  }
  if (fin && end < r.highest) {
    return Some(__error(frames.FINAL_SIZE_ERROR, kind, "a final size below data already received"));
  }
  return None;
}

function __with_final(r: Recv, end: i64, fin: boolean): Recv {
  if (fin) {
    return Recv { ...r, final_size: end };
  }
  return r;
}

function (s: Streams) __credit_error(r: Recv, end: i64, kind: i64): Option[frames.FrameError] {
  if (end > r.max) {
    return Some(__error(frames.FLOW_CONTROL_ERROR, kind, "stream data past the stream's window"));
  }
  let more: i64 = __max(0 as i64, end - r.highest);
  if (s.recv_highest + more > s.recv_max) {
    return Some(__error(frames.FLOW_CONTROL_ERROR, kind, "stream data past the connection's window"));
  }
  return None;
}

function (s: Streams) __on_reset(rs: frames.ResetStream): Streams {
  let (cur, i) = s.__stream_for(rs.stream, true, frames.RESET_STREAM);
  if (i < 0) {
    return cur;
  }
  let st: Stream = cur.list[i];
  if (st.recv.reset >= 0) {
    return cur;
  }
  if let Some(e) = __final_error(st.recv, rs.final_size, true, frames.RESET_STREAM) {
    return cur.__fail(e);
  }
  if let Some(e) = cur.__credit_error(st.recv, rs.final_size, frames.RESET_STREAM) {
    return cur.__fail(e);
  }
  let r: Recv = __with_final(st.recv, rs.final_size, true);
  let more: i64 = __max(0 as i64, rs.final_size - r.highest);
  let unread: i64 = rs.final_size - r.data.taken;
  let next: Streams = Streams { ...cur, recv_highest: cur.recv_highest + more, recv_taken: cur.recv_taken + unread, events: cur.events.append(ResetByPeer(rs.stream, rs.code)) };
  return next.__put(i, Stream { ...st, recv: Recv { ...r, highest: r.highest + more, reset: rs.code, data: reassembly() } }).__retire_done(i);
}
```

### `15 function (s: Streams) __on_stop(id: i64, code: i64): Streams`

```fern
<<15 function (s: Streams) __on_stop(id: i64, code: i64): Streams>>=
function (s: Streams) __on_stop(id: i64, code: i64): Streams {
  let (cur, i) = s.__stream_for(id, false, frames.STOP_SENDING);
  if (i < 0) {
    return cur;
  }
  let st: Stream = cur.list[i];
  if (st.send.reset >= 0) {
    return cur;
  }
  return Streams { ...cur, events: cur.events.append(StopRequested(id, code)) }.__put(i, __reset_send(st, code));
}

function (s: Streams) __on_max_stream_data(id: i64, v: i64): Streams {
  let (cur, i) = s.__stream_for(id, false, frames.MAX_STREAM_DATA);
  if (i < 0) {
    return cur;
  }
  let st: Stream = cur.list[i];
  return cur.__put(i, Stream { ...st, send: Send { ...st.send, max: __max(st.send.max, v) } });
}

function __error(code: i64, kind: i64, reason: string): frames.FrameError {
  return frames.FrameError { code: code, frame_type: kind, reason: reason };
}
```

### `16 pub function (s: Streams) fill(out: u8[], room: i32): Streams`

```fern
<<16 pub function (s: Streams) fill(out: u8[], room: i32): Streams>>=
pub function (s: Streams) fill(out: u8[], room: i32): Streams {
  return Streams { ...s, filled: out, resend: [], fit: false }.__signals(room).__window_updates(room).__data(room);
}

pub function (s: Streams) fill_taken(): Streams {
  return Streams { ...s, filled: [], resend: [] };
}
```

### `17 function (s: Streams) __add(room: i32, f: frames.Frame): Streams`

```fern
<<17 function (s: Streams) __add(room: i32, f: frames.Frame): Streams>>=
function (s: Streams) __add(room: i32, f: frames.Frame): Streams {
  let bytes: u8[] = frames.encode(f);
  if (s.filled.len() + bytes.len() > room) {
    return Streams { ...s, fit: false };
  }
  return Streams { ...s, filled: s.filled.concat(bytes), resend: s.resend.append(recovery.Again(f)), fit: true };
}

function (s: Streams) __signals(room: i32): Streams {
  let cur: Streams = s;
  let i: i32 = 0;
  while (i < cur.list.len()) {
    let st: Stream = cur.list[i];
    if (st.send.reset_state == TO_SEND) {
      cur = cur.__add(room, frames.ResetStreamFrame(frames.ResetStream { stream: st.id, code: st.send.reset, final_size: st.send.sent }));
      if (cur.fit) {
        cur = cur.__put(i, Stream { ...st, send: Send { ...st.send, reset_state: SENT } });
        st = cur.list[i];
      }
    }
    if (st.recv.stop_state == TO_SEND) {
      cur = cur.__add(room, frames.StopSending(st.id, st.recv.stop));
      if (cur.fit) {
        cur = cur.__put(i, Stream { ...st, recv: Recv { ...st.recv, stop_state: SENT } });
      }
    }
    i = i + 1;
  }
  return cur;
}

function (s: Streams) __window_updates(room: i32): Streams {
  let cur: Streams = s.__opened_windows();
  if (cur.recv_max_dirty) {
    cur = cur.__add(room, frames.MaxData(cur.recv_max));
    cur = Streams { ...cur, recv_max_dirty: !cur.fit };
  }
  let t: i32 = 0;
  while (t < 4) {
    if (cur.limits_dirty[t]) {
      cur = cur.__add(room, frames.MaxStreams((t & 2) == 0, cur.limits[t]));
      cur = Streams { ...cur, limits_dirty: cur.limits_dirty.with(t, !cur.fit) };
    }
    t = t + 1;
  }
  return cur.__stream_windows(room);
}

function (s: Streams) __opened_windows(): Streams {
  let cur: Streams = s;
  let want: i64 = s.recv_taken + s.recv_window;
  if (want - s.recv_max >= s.recv_window / 2 && s.recv_window > 0) {
    cur = Streams { ...cur, recv_max: want, recv_max_dirty: true };
  }
  let i: i32 = 0;
  while (i < cur.list.len()) {
    let st: Stream = cur.list[i];
    let r: Recv = st.recv;
    let open: i64 = r.data.taken + r.window;
    if (st.has_recv && r.final_size < 0 && r.reset < 0 && r.stop < 0 && r.window > 0 && open - r.max >= r.window / 2) {
      cur = cur.__put(i, Stream { ...st, recv: Recv { ...r, max: open, max_dirty: true } });
    }
    i = i + 1;
  }
  return cur;
}

function (s: Streams) __stream_windows(room: i32): Streams {
  let cur: Streams = s;
  let i: i32 = 0;
  while (i < cur.list.len()) {
    let st: Stream = cur.list[i];
    if (st.recv.max_dirty) {
      cur = cur.__add(room, frames.MaxStreamData(st.id, st.recv.max));
      if (cur.fit) {
        cur = cur.__put(i, Stream { ...st, recv: Recv { ...st.recv, max_dirty: false } });
      }
    }
    i = i + 1;
  }
  return cur;
}

function (s: Streams) __data(room: i32): Streams {
  let n: i32 = s.list.len();
  if (n == 0) {
    return s.__blocked(room);
  }
  let cur: Streams = s;
  let start: i32 = s.next % n;
  let k: i32 = 0;
  while (k < n && cur.filled.len() + 8 < room) {
    let i: i32 = (start + k) % n;
    cur = cur.__stream_data(i, room);
    k = k + 1;
  }
  return Streams { ...cur, next: start + 1 }.__blocked(room);
}

function (s: Streams) __stream_data(i: i32, room: i32): Streams {
  let st: Stream = s.list[i];
  if (!st.has_send || st.send.reset >= 0 || !__sendable(st.send)) {
    return s;
  }
  let out: u8[] = s.filled;
  let resend: recovery.Resend[] = s.resend;
  let taken: Streams = Streams { ...s, filled: [], resend: [] };
  let (snd, o1, r1) = __resend_lost(st.id, st.send, out, room, resend);
  let credit: i64 = __min(snd.max, snd.sent + taken.send_max - taken.send_sent);
  let (snd2, o2, r2) = __send_new(st.id, snd, credit, o1, room, r1);
  let cur: Streams = Streams { ...taken, send_sent: taken.send_sent + (snd2.sent - snd.sent), filled: o2, resend: r2 };
  return cur.__put(i, Stream { ...st, send: snd2 });
}

function __written(snd: Send): i64 {
  return snd.base + snd.buf.len() as i64;
}

function __sendable(snd: Send): boolean {
  return snd.lost.len() > 0 || __written(snd) > snd.sent || snd.fin && !snd.fin_sent;
}
```

### `18 function __resend_lost(id: i64, snd: Send, out: u8[], room: i32, resend: recovery.Resend[]): (Send, u8[], r...`

```fern
<<18 function __resend_lost(id: i64, snd: Send, out: u8[], room: i32, resend: recovery.Resend[]): (Send, u8[], r...>>=
function __resend_lost(id: i64, snd: Send, out: u8[], room: i32, resend: recovery.Resend[]): (Send, u8[], recovery.Resend[]) {
  if (snd.lost.len() == 0) {
    return (snd, out, resend);
  }
  let o: u8[] = out;
  let rs: recovery.Resend[] = resend;
  let kept: i64[] = [];
  let i: i32 = 0;
  while (i + 1 < snd.lost.len()) {
    let from: i64 = __max(snd.lost[i], snd.base);
    let end: i64 = snd.lost[i] + snd.lost[i + 1];
    if (end > from) {
      let fit: i32 = room - o.len() - frames.stream_overhead(id, from, (end - from) as i32);
      let n: i32 = __min(end - from, __max(0 as i64, fit as i64)) as i32;
      if (n > 0) {
        let at: i32 = (from - snd.base) as i32;
        o = __append_range(frames.put_stream_head(o, id, from, n, false), snd.buf, at, at + n);
        rs = rs.append(recovery.StreamRange(id, from, n, false));
      }
      if (from + n as i64 < end) {
        kept = kept.append(from + n as i64).append(end - from - n as i64);
      }
    }
    i = i + 2;
  }
  return (Send { ...snd, lost: kept }, o, rs);
}

function __send_new(id: i64, snd: Send, credit: i64, own out: u8[], room: i32, resend: recovery.Resend[]): (Send, u8[], recovery.Resend[]) {
  let avail: i64 = __min(__written(snd), credit) - snd.sent;
  let fit: i64 = (room - out.len() - frames.stream_overhead(id, snd.sent, avail as i32)) as i64;
  let n: i64 = __max(0 as i64, __min(avail, fit));
  let fin: boolean = snd.fin && !snd.fin_sent && snd.sent + n == __written(snd);
  if (n == 0 && !fin) {
    return (snd, out, resend);
  }
  if (n == 0 && fit < 0) {
    return (snd, out, resend);
  }
  let at: i32 = (snd.sent - snd.base) as i32;
  let o: u8[] = __append_range(frames.put_stream_head(out, id, snd.sent, n as i32, fin), snd.buf, at, at + n as i32);
  let next: Send = Send { ...snd, sent: snd.sent + n, fin_sent: snd.fin_sent || fin };
  return (next, o, resend.append(recovery.StreamRange(id, snd.sent, n as i32, fin)));
}

function (s: Streams) __blocked(room: i32): Streams {
  return s.__stream_data_blocked(room).__data_blocked(room).__streams_blocked(room);
}

function __held(st: Stream): boolean {
  return st.has_send && st.send.reset < 0 && __written(st.send) > st.send.sent;
}

function (s: Streams) __stream_data_blocked(room: i32): Streams {
  let cur: Streams = s;
  let i: i32 = 0;
  while (i < cur.list.len()) {
    let st: Stream = cur.list[i];
    let snd: Send = st.send;
    if (__held(st) && snd.sent >= snd.max && snd.blocked_at != snd.max) {
      cur = cur.__add(room, frames.StreamDataBlocked(st.id, snd.max));
      if (cur.fit) {
        cur = cur.__put(i, Stream { ...st, send: Send { ...snd, blocked_at: snd.max } });
      }
    }
    i = i + 1;
  }
  return cur;
}

function (s: Streams) __data_blocked(room: i32): Streams {
  if (s.send_sent < s.send_max || s.blocked_at == s.send_max || !s.list.any(__held)) {
    return s;
  }
  let cur: Streams = s.__add(room, frames.DataBlocked(s.send_max));
  if (cur.fit) {
    return Streams { ...cur, blocked_at: cur.send_max };
  }
  return cur;
}

function (s: Streams) __streams_blocked(room: i32): Streams {
  let cur: Streams = s;
  let t: i32 = 0;
  while (t < 4) {
    if (cur.open_blocked[t] >= 0 && cur.open_blocked[t] == cur.limits[t]) {
      cur = cur.__add(room, frames.StreamsBlocked((t & 2) == 0, cur.limits[t]));
      if (cur.fit) {
        cur = Streams { ...cur, open_blocked: cur.open_blocked.with(t, 0 - 1 as i64) };
      }
    }
    t = t + 1;
  }
  return cur;
}
```

### `19 pub function (s: Streams) blocked_open(uni: boolean): Streams`

```fern
<<19 pub function (s: Streams) blocked_open(uni: boolean): Streams>>=
pub function (s: Streams) blocked_open(uni: boolean): Streams {
  let t: i32 = s.__local_type(uni);
  return Streams { ...s, open_blocked: s.open_blocked.with(t, s.limits[t]) };
}

pub function (s: Streams) acked(item: recovery.Resend): Streams {
  match (item) {
    recovery.StreamRange(id, offset, len, fin) => {
      let i: i32 = s.__find(id);
      if (i < 0) {
        return s;
      }
      let st: Stream = s.list[i];
      return s.__put(i, Stream { ...st, send: __acked_range(st.send, offset, len, fin) }).__retire_done(i);
    },
    recovery.Again(frames.ResetStreamFrame(r)) => {
      let i: i32 = s.__find(r.stream);
      if (i < 0) {
        return s;
      }
      let st: Stream = s.list[i];
      return s.__put(i, Stream { ...st, send: Send { ...st.send, reset_state: ACKED } }).__retire_done(i);
    },
    _ => {
      return s;
    }
  }
}
```

### `20 function __acked_range(snd: Send, offset: i64, len: i32, fin: boolean): Send`

```fern
<<20 function __acked_range(snd: Send, offset: i64, len: i32, fin: boolean): Send>>=
function __acked_range(snd: Send, offset: i64, len: i32, fin: boolean): Send {
  if (snd.reset >= 0) {
    return snd;
  }
  let acked: i64[] = __merge(snd.acked, offset, offset + len as i64);
  let base: i64 = snd.base;
  while (acked.len() >= 2 && acked[0] <= base) {
    base = __max(base, acked[1]);
    acked = __rest_i64(__rest_i64(acked));
  }
  let drop: i32 = (base - snd.base) as i32;
  return Send { ...snd, acked: acked, base: base, buf: __slice_range(snd.buf, drop, snd.buf.len()), fin_acked: snd.fin_acked || fin };
}

function __merge(ranges: i64[], lo: i64, hi: i64): i64[] {
  let out: i64[] = [];
  let a: i64 = lo;
  let b: i64 = hi;
  let placed: boolean = false;
  let i: i32 = 0;
  while (i + 1 < ranges.len()) {
    let l: i64 = ranges[i];
    let h: i64 = ranges[i + 1];
    if (h < a) {
      out = out.append(l).append(h);
    } else if (l > b) {
      if (!placed) {
        out = out.append(a).append(b);
        placed = true;
      }
      out = out.append(l).append(h);
    } else {
      a = __min(a, l);
      b = __max(b, h);
    }
    i = i + 2;
  }
  if (!placed) {
    out = out.append(a).append(b);
  }
  return out;
}
```

### `21 pub function (s: Streams) lost(item: recovery.Resend): Streams`

```fern
<<21 pub function (s: Streams) lost(item: recovery.Resend): Streams>>=
pub function (s: Streams) lost(item: recovery.Resend): Streams {
  match (item) {
    recovery.StreamRange(id, offset, len, fin) => {
      let i: i32 = s.__find(id);
      if (i < 0) {
        return s;
      }
      let st: Stream = s.list[i];
      let snd: Send = Send { ...st.send, lost: st.send.lost.append(offset).append(len as i64), fin_sent: st.send.fin_sent && !fin };
      return s.__put(i, Stream { ...st, send: snd });
    },
    recovery.Again(f) => {
      return s.__lost_frame(f);
    },
    _ => {
      return s;
    }
  }
}
```

### `22 function (s: Streams) __lost_frame(f: frames.Frame): Streams`

```fern
<<22 function (s: Streams) __lost_frame(f: frames.Frame): Streams>>=
function (s: Streams) __lost_frame(f: frames.Frame): Streams {
  match (f) {
    frames.MaxData(_) => {
      return Streams { ...s, recv_max_dirty: true };
    },
    frames.MaxStreams(bidi, _) => {
      let t: i32 = s.__peer_type(!bidi);
      return Streams { ...s, limits_dirty: s.limits_dirty.with(t, true) };
    },
    _ => {
      return s.__lost_stream_frame(f);
    }
  }
}

function (s: Streams) __lost_stream_frame(f: frames.Frame): Streams {
  match (f) {
    frames.ResetStreamFrame(r) => {
      return s.__restate(r.stream, true);
    },
    frames.StopSending(id, _) => {
      return s.__restate(id, false);
    },
    frames.MaxStreamData(id, _) => {
      let i: i32 = s.__find(id);
      if (i < 0 || s.list[i].recv.final_size >= 0) {
        return s;
      }
      let st: Stream = s.list[i];
      return s.__put(i, Stream { ...st, recv: Recv { ...st.recv, max_dirty: true } });
    },
    _ => {
      return s;
    }
  }
}

function (s: Streams) __restate(id: i64, send: boolean): Streams {
  let i: i32 = s.__find(id);
  if (i < 0) {
    return s;
  }
  let st: Stream = s.list[i];
  if (send && st.send.reset_state == SENT) {
    return s.__put(i, Stream { ...st, send: Send { ...st.send, reset_state: TO_SEND } });
  }
  if (!send && st.recv.stop_state == SENT && st.recv.reset < 0 && st.recv.final_size < 0) {
    return s.__put(i, Stream { ...st, recv: Recv { ...st.recv, stop_state: TO_SEND } });
  }
  return s;
}

function (s: Streams) __retire_done(i: i32): Streams {
  let st: Stream = s.list[i];
  if (st.closed || !__done(st)) {
    return s;
  }
  let cur: Streams = Streams { ...s, retired: s.retired + 1 };
  if (!cur.__is_local(st.id)) {
    let t: i32 = (st.id & 3) as i32;
    cur = Streams { ...cur, limits: cur.limits.with(t, cur.limits[t] + 1), limits_dirty: cur.limits_dirty.with(t, true) };
  }
  cur = cur.__put(i, Stream { ...st, closed: true });
  if (cur.retired >= 32 && cur.retired * 2 >= cur.list.len()) {
    return cur.__sweep();
  }
  return cur;
}

function (s: Streams) __sweep(): Streams {
  let base: i64 = s.opened[0] * 2;
  let top: i64 = base;
  let list: Stream[] = [];
  for st in s.list {
    if (!st.closed) {
      list = list.append(st);
      if ((st.id & 2) == 0) {
        let key: i64 = __bidi_key(st.id);
        if (key < base) {
          base = key;
        }
        if (key > top) {
          top = key;
        }
      }
    }
  }
  let cur: Streams = Streams { ...s, list: list, index: __alloc_i32((top - base) as i32 + 16), base: base, retired: 0 };
  let i: i32 = 0;
  while (i < list.len()) {
    cur = cur.__indexed(list[i].id, i);
    i = i + 1;
  }
  return cur;
}

function __done(st: Stream): boolean {
  return __send_done(st) && __recv_done(st);
}

function __send_done(st: Stream): boolean {
  if (!st.has_send) {
    return true;
  }
  if (st.send.reset >= 0) {
    return st.send.reset_state == ACKED;
  }
  return st.send.fin_acked && st.send.buf.len() == 0;
}

function __recv_done(st: Stream): boolean {
  return !st.has_recv || st.recv.fin_read || st.recv.reset >= 0;
}

function __total(held: u8[][]): i64 {
  let n: i64 = 0;
  for h in held {
    n = n + h.len() as i64;
  }
  return n;
}

function __rest(xs: u8[][]): u8[][] {
  let out: u8[][] = [];
  let i: i32 = 1;
  while (i < xs.len()) {
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}
```

### `23 function __rest_i64(xs: i64[]): i64[]`

```fern
<<23 function __rest_i64(xs: i64[]): i64[]>>=
function __rest_i64(xs: i64[]): i64[] {
  let out: i64[] = [];
  let i: i32 = 1;
  while (i < xs.len()) {
    out = out.append(xs[i]);
    i = i + 1;
  }
  return out;
}

function __append_range(out: u8[], src: u8[], from: i32, to: i32): u8[] {
  if (from == to) {
    return out;
  }
  if (out.len() == 0) {
    return __slice_range(src, from, to);
  }
  return out.concat(__slice_range(src, from, to));
}

function __slice_range(src: u8[], from: i32, to: i32): u8[] {
  return __bytes_range(src, from, to);
}

function __copy(b: [u8]): u8[] {
  return __bytes_owned(b);
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
```
