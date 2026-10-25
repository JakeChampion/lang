# HTTP/3 over QUIC

This module connects HTTP/3 protocol state to a QUIC connection.

## Model

The adapter owns a QUIC connection and an HTTP/3 session. It translates their progress into request, response, stream-drop, and connection-end events.

## Contracts

The caller supplies datagrams and time progress and sends the returned datagrams. Body limits bound collected request or response data.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum Event>>

<<4 pub function transport(c: conn.Config): conn.Config>>

<<5 function (c: Conn) __stepped(now: i64): Conn>>

<<6 function (c: Conn) __session_event(e: h3.Event): Conn>>

<<7 function __request_fields(req: HttpRequest, scheme: string): qpack.Field[]>>

<<8 pub function (c: Conn) respond(id: i64, resp: HttpResponse): Conn>>

<<9 function __find(ps: Partial[], id: i64): i32>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module connects HTTP/3 protocol state to a QUIC connection.

import "std/i32";
import "std/unicode";
// This module connects HTTP/3 protocol state to a QUIC connection.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/quic/conn";
import "std/quic/streams";
import "std/http/h3";
import "std/http/qpack";
import "std/headers";
import "std/http";
import "std/string";
import "std/option";
import "std/result";
import "core/int";
```

### `3 pub enum Event`

```fern
<<3 pub enum Event>>=
pub enum Event { Requested(i64, HttpRequest), Responded(i64, HttpResponse), StreamDropped(i64, i64), Ended(conn.Close) }

struct Partial { id: i64, fields: qpack.Field[], body: u8[], trailers: qpack.Field[] }

pub struct Conn { quic: conn.Conn, session: h3.Session, started: boolean, stopping: boolean, max_body: i64, partials: Partial[], next: i64, parked: i64[], events: Event[], date: string }
```

### `4 pub function transport(c: conn.Config): conn.Config`

```fern
<<4 pub function transport(c: conn.Config): conn.Config>>=
pub function transport(c: conn.Config): conn.Config {
  return conn.Config { ...c, alpn: ["h3"] }.with_streams(100 as i64, 8 as i64, 1048576 as i64, 16777216 as i64);
}

pub function server(q: conn.Conn, c: h3.Config): Conn {
  return Conn { quic: q, session: h3.server(c), started: false, stopping: false, max_body: 1048576 as i64, partials: [], next: 0 as i64, parked: [], events: [], date: "" };
}

pub function client(q: conn.Conn, c: h3.Config): Conn {
  return Conn { quic: q, session: h3.client(c), started: false, stopping: false, max_body: 1048576 as i64, partials: [], next: 0 as i64, parked: [], events: [], date: "" };
}

pub function (c: Conn) with_max_body(n: i64): Conn {
  return Conn { ...c, max_body: n };
}

// with_date sets the date field a response is sent with when it has none.
pub function (c: Conn) with_date(date: string): Conn {
  return Conn { ...c, date: date };
}

pub function (c: Conn) quic(): conn.Conn {
  return c.quic;
}

pub function (c: Conn) deadline(): i64 {
  return c.quic.deadline();
}

pub function (c: Conn) done(): boolean {
  return c.quic.done();
}

pub function (c: Conn) ready(): boolean {
  return c.started && c.quic.connected() && c.session.goaway_received() < 0;
}

pub function (c: Conn) events_taken(): Conn {
  return Conn { ...c, events: [] };
}

pub function (c: Conn) datagrams(): u8[][] {
  return c.quic.out;
}

pub function (c: Conn) sent(): Conn {
  return Conn { ...c, quic: c.quic.sent() };
}

pub function (c: Conn) receive(datagram: [u8], now: i64): Conn {
  return Conn { ...c, quic: c.quic.receive(datagram, now) }.__stepped(now);
}

pub function (c: Conn) tick(now: i64): Conn {
  return Conn { ...c, quic: c.quic.tick(now) }.__stepped(now);
}

pub function (c: Conn) transmit(now: i64): Conn {
  return Conn { ...c, quic: c.quic.transmit(now) };
}

pub function (c: Conn) shutdown(): Conn {
  if (!c.started || c.stopping) {
    return c;
  }
  return Conn { ...c, session: c.session.send_goaway(c.next), stopping: true }.__applied(0 as i64);
}

pub function (c: Conn) close(now: i64): Conn {
  return Conn { ...c, quic: c.quic.close(h3.H3_NO_ERROR, "", true, now) };
}
```

### `5 function (c: Conn) __stepped(now: i64): Conn`

```fern
<<5 function (c: Conn) __stepped(now: i64): Conn>>=
function (c: Conn) __stepped(now: i64): Conn {
  let evs: conn.Event[] = c.quic.events;
  let notes: streams.Event[] = c.quic.stream_events();
  if (evs.len() == 0 && notes.len() == 0) {
    return c.__applied(now);
  }
  let cur: Conn = Conn { ...c, quic: c.quic.events_taken() };
  for e in evs {
    cur = cur.__quic_event(e);
  }
  for n in notes {
    cur = cur.__stream_event(n);
  }
  return cur.__applied(now);
}

function (c: Conn) __quic_event(e: conn.Event): Conn {
  match (e) {
    conn.Connected => {
      return c.__started();
    },
    conn.Closed(why) => {
      return Conn { ...c, events: c.events.append(Ended(why)) };
    }
  }
}

function (c: Conn) __stream_event(n: streams.Event): Conn {
  match (n) {
    streams.Readable(id) => {
      return c.__readable(id);
    },
    streams.ResetByPeer(id, code) => {
      return Conn { ...c, session: c.session.on_reset(id, code) };
    },
    streams.StopRequested(id, code) => {
      return Conn { ...c, session: c.session.on_reset(id, code) };
    },
    streams.Opened(_) => {
      return c;
    }
  }
}

function (c: Conn) __started(): Conn {
  let Ok(a) = c.quic.open_stream(true) else {
    return c.__fatal("the peer allows too few unidirectional streams");
  };
  let Ok(b) = a.0.open_stream(true) else {
    return c.__fatal("the peer allows too few unidirectional streams");
  };
  let Ok(d) = b.0.open_stream(true) else {
    return c.__fatal("the peer allows too few unidirectional streams");
  };
  return Conn { ...c, quic: d.0, session: c.session.start(a.1, b.1, d.1), started: true };
}

function (c: Conn) __fatal(why: string): Conn {
  return Conn { ...c, quic: c.quic.close(h3.H3_GENERAL_PROTOCOL_ERROR, why, true, 0 as i64) };
}

function (c: Conn) __readable(id: i64): Conn {
  if (c.session.waiting(id)) {
    if (c.parked.contains(id)) {
      return c;
    }
    return Conn { ...c, parked: c.parked.append(id) };
  }
  let cur: Conn = Conn { ...c, quic: c.quic.read_into(id) };
  if (cur.quic.refused() != "") {
    return cur;
  }
  let data: u8[] = cur.quic.read_data();
  let fin: boolean = cur.quic.read_fin();
  return Conn { ...cur, quic: cur.quic.read_taken(), session: cur.session.on_stream(id, data, fin) }.__unparked();
}

function (c: Conn) __unparked(): Conn {
  for id in c.parked {
    if (!c.session.waiting(id)) {
      return Conn { ...c, parked: __without_id(c.parked, id) }.__readable(id);
    }
  }
  return c;
}

function (c: Conn) __applied(now: i64): Conn {
  let cur: Conn = c;
  while (true) {
    let acts: h3.Action[] = cur.session.actions;
    let evs: h3.Event[] = cur.session.events;
    if (acts.len() == 0 && evs.len() == 0) {
      return cur;
    }
    cur = Conn { ...cur, session: cur.session.taken(), quic: __acts(cur.quic, acts, now) };
    let k: i32 = 0;
    while (k < evs.len()) {
      let id: i64 = __ended_by(evs, k);
      if (id >= 0) {
        cur = cur.__whole(id, evs[k]);
        k = k + 2;
      } else {
        cur = cur.__session_event(evs[k]);
        k = k + 1;
      }
    }
  }
  return cur;
}

// __ended_by is the stream whose header section at evs[k] the next event
// ends, or -1: a whole message with no body, taken without a Partial to
// gather one.
function __ended_by(evs: h3.Event[], k: i32): i64 {
  if (k + 1 >= evs.len()) {
    return 0 - 1 as i64;
  }
  let Some(id) = __headers_of(evs[k]) else {
    return 0 - 1 as i64;
  };
  match (evs[k + 1]) {
    h3.StreamEnded(end) => {
      if (end == id) {
        return id;
      }
    },
    _ => {}
  }
  return 0 - 1 as i64;
}

function __headers_of(e: h3.Event): Option[i64] {
  match (e) {
    h3.HeadersReceived(id, _) => {
      return Some(id);
    },
    _ => {}
  }
  return None;
}

function __acts(q: conn.Conn, acts: h3.Action[], now: i64): conn.Conn {
  let cur: conn.Conn = q;
  for a in acts {
    cur = __act(cur, a, now);
  }
  return cur;
}

function __act(q: conn.Conn, a: h3.Action, now: i64): conn.Conn {
  match (a) {
    h3.Write(id, bytes, fin) => {
      return q.written(id, bytes, fin);
    },
    h3.Abort(id, code) => {
      return q.stream_reset(id, code);
    },
    h3.Stop(id, code) => {
      return q.sending_stopped(id, code);
    },
    h3.CloseConnection(code, why) => {
      return q.close(code, why, true, now);
    }
  }
}
```

### `6 function (c: Conn) __session_event(e: h3.Event): Conn`

```fern
<<6 function (c: Conn) __session_event(e: h3.Event): Conn>>=
function (c: Conn) __session_event(e: h3.Event): Conn {
  match (e) {
    h3.HeadersReceived(id, fields) => {
      return c.__headers(id, fields);
    },
    h3.DataReceived(id, data) => {
      return c.__data(id, data);
    },
    h3.TrailersReceived(id, fields) => {
      return c.__with_partial(id, (p: Partial): Partial => Partial { ...p, trailers: fields });
    },
    h3.StreamEnded(id) => {
      return c.__ended(id);
    },
    h3.StreamAborted(id, code) => {
      return Conn { ...c, partials: __without(c.partials, id), events: c.events.append(StreamDropped(id, code)) };
    },
    h3.GoawayReceived(_) => {
      return c;
    }
  }
}

function (c: Conn) __headers(id: i64, fields: qpack.Field[]): Conn {
  if (c.__rejects(id)) {
    return Conn { ...c, session: c.session.abort(id, h3.H3_REQUEST_REJECTED) };
  }
  let kept: Partial[] = __without(c.partials, id);
  return Conn { ...c, partials: kept.append(Partial { id: id, fields: fields, body: [], trailers: [] }), next: c.__next_for(id) };
}

// __whole takes a message whose header section, the event e, ends its
// stream.
function (c: Conn) __whole(id: i64, e: h3.Event): Conn {
  if (c.__rejects(id)) {
    return Conn { ...c, session: c.session.abort(id, h3.H3_REQUEST_REJECTED) };
  }
  let none: qpack.Field[] = [];
  let next: i64 = c.__next_for(id);
  let rest: Partial[] = c.partials;
  if (__find(rest, id) >= 0) {
    rest = __without(rest, id);
  }
  match (e) {
    h3.HeadersReceived(_, fields) => {
      if (c.session.is_server()) {
        return Conn { ...c, partials: rest, next: next, events: c.events.append(Requested(id, __request(fields, [], none))) };
      }
      return Conn { ...c, partials: rest, next: next, events: c.events.append(Responded(id, __response(fields, [], none))) };
    },
    _ => {}
  }
  return Conn { ...c, partials: rest, next: next };
}

// __rejects says a server that is shutting down refuses request stream id.
function (c: Conn) __rejects(id: i64): boolean {
  return c.session.is_server() && c.stopping && id >= c.next;
}

// __next_for is the request stream a server's GOAWAY names once id opens.
function (c: Conn) __next_for(id: i64): i64 {
  if (c.session.is_server() && id + 4 > c.next) {
    return id + 4;
  }
  return c.next;
}

function (c: Conn) __data(id: i64, data: u8[]): Conn {
  let i: i32 = __find(c.partials, id);
  if (i < 0) {
    return c;
  }
  let p: Partial = c.partials[i];
  if ((p.body.len() + data.len()) as i64 > c.max_body) {
    let s: h3.Session = c.session.abort(id, h3.H3_REQUEST_REJECTED);
    return Conn { ...c, session: s, partials: __without(c.partials, id), events: c.events.append(StreamDropped(id, h3.H3_REQUEST_REJECTED)) };
  }
  return Conn { ...c, partials: c.partials.with(i, Partial { ...p, body: p.body.concat(data) }) };
}

function (c: Conn) __with_partial(id: i64, f: (Partial) => Partial): Conn {
  let i: i32 = __find(c.partials, id);
  if (i < 0) {
    return c;
  }
  return Conn { ...c, partials: c.partials.with(i, f(c.partials[i])) };
}

function (c: Conn) __ended(id: i64): Conn {
  let i: i32 = __find(c.partials, id);
  if (i < 0) {
    return c;
  }
  let p: Partial = c.partials[i];
  let rest: Partial[] = __without(c.partials, id);
  if (c.session.is_server()) {
    return Conn { ...c, partials: rest, events: c.events.append(Requested(id, __request(p.fields, p.body, p.trailers))) };
  }
  return Conn { ...c, partials: rest, events: c.events.append(Responded(id, __response(p.fields, p.body, p.trailers))) };
}

function __request(fields: qpack.Field[], body: u8[], trailers: qpack.Field[]): HttpRequest {
  let hs: HeaderMap = headers.header_map_new();
  let authority: string = "";
  let method: string = "";
  let path: string = "";
  for f in fields {
    if (f.name == ":method") {
      method = f.value;
    } else if (f.name == ":path") {
      path = f.value;
    } else if (f.name == ":authority") {
      authority = f.value;
    } else if (!f.name.starts_with(":")) {
      hs = hs.append(f.name, f.value);
    }
  }
  if (authority.len() > 0 && !hs.has("host")) {
    hs = hs.append("host", authority);
  }
  return HttpRequest { method: method, path: path, body: ByteStream { data: body, pos: 0, source: None }, headers: hs, trailers: __map(trailers) };
}

function __response(fields: qpack.Field[], body: u8[], trailers: qpack.Field[]): HttpResponse {
  let status: i32 = 0;
  let hs: HeaderMap = headers.header_map_new();
  for f in fields {
    if (f.name == ":status") {
      status = i32.parse_i32(f.value).unwrap_or(0);
    } else if (!f.name.starts_with(":")) {
      hs = hs.append(f.name, f.value);
    }
  }
  return HttpResponse { status: status, body: BodyBytes(body), headers: hs, trailers: __map(trailers), background: None };
}

function __map(fields: qpack.Field[]): HeaderMap {
  let hs: HeaderMap = headers.header_map_new();
  for f in fields {
    hs = hs.append(f.name, f.value);
  }
  return hs;
}

// __response_section encodes a response's header section, adding date
// and content-length when the response has none, without building its
// field list.
function __response_section(enc: qpack.Encoder, resp: HttpResponse, length: i64, date: string): u8[] {
  let names: string[] = resp.headers.names;
  let room: i32 = date.len() + 32;
  for i in 0..names.len() {
    room = room + names[i].len() + resp.headers.values[i].len() + 4;
  }
  let out: u8[] = enc.put_status(enc.section(room), resp.status);
  let has_length: boolean = false;
  let has_date: boolean = false;
  for i in 0..names.len() {
    let name: string = names[i].to_lower();
    has_length = has_length || name == "content-length";
    has_date = has_date || name == "date";
    if (!__connection_specific(name)) {
      out = enc.put(out, name, resp.headers.values[i]);
    }
  }
  if (!has_date && date.len() > 0) {
    out = enc.put(out, "date", date);
  }
  if (!has_length && length >= 0 && !http.http_bodiless(resp.status)) {
    out = enc.put_length(out, length);
  }
  return out;
}

function __connection_specific(name: string): boolean {
  return name == "connection" || name == "keep-alive" || name == "proxy-connection" || name == "transfer-encoding" || name == "upgrade";
}
```

### `7 function __request_fields(req: HttpRequest, scheme: string): qpack.Field[]`

```fern
<<7 function __request_fields(req: HttpRequest, scheme: string): qpack.Field[]>>=
function __request_fields(req: HttpRequest, scheme: string): qpack.Field[] {
  let authority: string = req.headers.get("host").unwrap_or("");
  let fs: qpack.Field[] = [qpack.field(":method", req.method), qpack.field(":scheme", scheme), qpack.field(":authority", authority), qpack.field(":path", req.path)];
  let i: i32 = 0;
  while (i < req.headers.names.len()) {
    let name: string = req.headers.names[i].to_lower();
    if (name != "host" && !__connection_specific(name)) {
      fs = fs.append(qpack.field(name, req.headers.values[i]));
    }
    i = i + 1;
  }
  return fs;
}

function __trailer_fields(t: HeaderMap): qpack.Field[] {
  let fs: qpack.Field[] = [];
  let i: i32 = 0;
  while (i < t.names.len()) {
    fs = fs.append(qpack.field(t.names[i].to_lower(), t.values[i]));
    i = i + 1;
  }
  return fs;
}
```

### `8 pub function (c: Conn) respond(id: i64, resp: HttpResponse): Conn`

```fern
<<8 pub function (c: Conn) respond(id: i64, resp: HttpResponse): Conn>>=
pub function (c: Conn) respond(id: i64, resp: HttpResponse): Conn {
  match (resp.body) {
    BodyText(s) => {
      return c.__respond_with(id, resp, s.as_bytes());
    },
    _ => {
      return c.__respond_with(id, resp, resp.body_bytes());
    }
  }
}

function (c: Conn) __respond_with(id: i64, resp: HttpResponse, bytes: [u8]): Conn {
  let n: i32 = bytes.len();
  if (http.http_bodiless(resp.status)) {
    n = 0;
  }
  let whole: boolean = resp.trailers.names.len() == 0;
  let sent: Conn = Conn { ...c, session: c.session.send_section(id, __response_section(c.session.enc, resp, n as i64, c.date), bytes[0:n], whole) }.__applied(0 as i64);
  if (whole) {
    return sent;
  }
  return sent.end(id, resp.trailers);
}

pub function (c: Conn) respond_head(id: i64, resp: HttpResponse, length: i64): Conn {
  return Conn { ...c, session: c.session.send_section(id, __response_section(c.session.enc, resp, length, c.date), [], false) }.__applied(0 as i64);
}

pub function (c: Conn) write(id: i64, bytes: [u8]): Conn {
  if (bytes.len() == 0) {
    return c;
  }
  return Conn { ...c, session: c.session.send_data(id, bytes, false) }.__applied(0 as i64);
}

pub function (c: Conn) end(id: i64, trailers: HeaderMap): Conn {
  if (trailers.names.len() > 0) {
    return Conn { ...c, session: c.session.send_trailers(id, __trailer_fields(trailers)) }.__applied(0 as i64);
  }
  return Conn { ...c, session: c.session.send_data(id, [], true) }.__applied(0 as i64);
}

pub function (c: Conn) abort(id: i64, code: i64): Conn {
  return Conn { ...c, session: c.session.abort(id, code), partials: __without(c.partials, id) }.__applied(0 as i64);
}

pub function (c: Conn) request(req: HttpRequest, scheme: string): Result[(Conn, i64), string] {
  if (!c.ready()) {
    return Err("h3: the connection takes no requests");
  }
  let (q, id) = c.quic.open_stream(false)?;
  let body: u8[] = req.body.data;
  let s: h3.Session = c.session.send_headers(id, __request_fields(req, scheme), body.len() == 0 && req.trailers.names.len() == 0);
  let cur: Conn = Conn { ...c, quic: q, session: s };
  if (body.len() > 0 || req.trailers.names.len() > 0) {
    cur = cur.write(id, body).end(id, req.trailers);
  }
  return Ok((cur.__applied(0 as i64), id));
}
```

### `9 function __find(ps: Partial[], id: i64): i32`

```fern
<<9 function __find(ps: Partial[], id: i64): i32>>=
function __find(ps: Partial[], id: i64): i32 {
  let i: i32 = 0;
  while (i < ps.len()) {
    if (ps[i].id == id) {
      return i;
    }
    i = i + 1;
  }
  return 0 - 1;
}

function __without_id(ids: i64[], id: i64): i64[] {
  let out: i64[] = [];
  for k in ids {
    if (k != id) {
      out = out.append(k);
    }
  }
  return out;
}

function __without(ps: Partial[], id: i64): Partial[] {
  let out: Partial[] = [];
  for p in ps {
    if (p.id != id) {
      out = out.append(p);
    }
  }
  return out;
}
```
