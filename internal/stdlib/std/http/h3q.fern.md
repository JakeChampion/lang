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
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/quic/conn";
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

pub struct Conn { quic: conn.Conn, session: h3.Session, started: boolean, stopping: boolean, max_body: i64, partials: Partial[], next: i64, parked: i64[], events: Event[] }
```

### `4 pub function transport(c: conn.Config): conn.Config`

```fern
<<4 pub function transport(c: conn.Config): conn.Config>>=
pub function transport(c: conn.Config): conn.Config {
  return conn.Config { ...c, alpn: ["h3"] }.with_streams(100 as i64, 8 as i64, 1048576 as i64, 16777216 as i64);
}

pub function server(q: conn.Conn, c: h3.Config): Conn {
  return Conn { quic: q, session: h3.server(c), started: false, stopping: false, max_body: 1048576 as i64, partials: [], next: 0 as i64, parked: [], events: [] };
}

pub function client(q: conn.Conn, c: h3.Config): Conn {
  return Conn { quic: q, session: h3.client(c), started: false, stopping: false, max_body: 1048576 as i64, partials: [], next: 0 as i64, parked: [], events: [] };
}

pub function (c: Conn) with_max_body(n: i64): Conn {
  return Conn { ...c, max_body: n };
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
  if (evs.len() == 0) {
    return c.__applied(now);
  }
  let cur: Conn = Conn { ...c, quic: c.quic.events_taken() };
  for e in evs {
    cur = cur.__quic_event(e, now);
  }
  return cur.__applied(now);
}

function (c: Conn) __quic_event(e: conn.Event, now: i64): Conn {
  match (e) {
    conn.Connected => {
      return c.__started();
    },
    conn.StreamReadable(id) => {
      return c.__readable(id);
    },
    conn.StreamReset(id, code) => {
      return Conn { ...c, session: c.session.on_reset(id, code) };
    },
    conn.StreamStopped(id, code) => {
      return Conn { ...c, session: c.session.on_reset(id, code) };
    },
    conn.Closed(why) => {
      return Conn { ...c, events: c.events.append(Ended(why)) };
    },
    _ => {
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
    for e in evs {
      cur = cur.__session_event(e);
    }
  }
  return cur;
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
  let next: i64 = c.next;
  if (c.session.is_server()) {
    if (c.stopping && id >= next) {
      return Conn { ...c, session: c.session.abort(id, h3.H3_REQUEST_REJECTED) };
    }
    if (id + 4 > next) {
      next = id + 4;
    }
  }
  let kept: Partial[] = __without(c.partials, id);
  return Conn { ...c, partials: kept.append(Partial { id: id, fields: fields, body: [], trailers: [] }), next: next };
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
    return Conn { ...c, partials: rest, events: c.events.append(Requested(id, __request(p))) };
  }
  return Conn { ...c, partials: rest, events: c.events.append(Responded(id, __response(p))) };
}

function __request(p: Partial): HttpRequest {
  let hs: HeaderMap = headers.header_map_new();
  let authority: string = "";
  let method: string = "";
  let path: string = "";
  for f in p.fields {
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
  return HttpRequest { method: method, path: path, body: Stream { data: p.body, pos: 0, source: None }, headers: hs, trailers: __map(p.trailers) };
}

function __response(p: Partial): HttpResponse {
  let status: i32 = 0;
  let hs: HeaderMap = headers.header_map_new();
  for f in p.fields {
    if (f.name == ":status") {
      status = f.value.parse_int_or(0);
    } else if (!f.name.starts_with(":")) {
      hs = hs.append(f.name, f.value);
    }
  }
  return HttpResponse { status: status, body: BodyBytes(p.body), headers: hs, trailers: __map(p.trailers), background: None };
}

function __map(fields: qpack.Field[]): HeaderMap {
  let hs: HeaderMap = headers.header_map_new();
  for f in fields {
    hs = hs.append(f.name, f.value);
  }
  return hs;
}

function __response_fields(resp: HttpResponse, length: i64): qpack.Field[] {
  let fs: qpack.Field[] = [qpack.field(":status", int.int_to_string(resp.status))];
  let has_length: boolean = false;
  let i: i32 = 0;
  while (i < resp.headers.names.len()) {
    let name: string = resp.headers.names[i].to_lower();
    has_length = has_length || name == "content-length";
    if (!__connection_specific(name)) {
      fs = fs.append(qpack.field(name, resp.headers.values[i]));
    }
    i = i + 1;
  }
  if (!has_length && length >= 0 && !http.http_bodiless(resp.status)) {
    fs = fs.append(qpack.field("content-length", length.to_string()));
  }
  return fs;
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
  let body: u8[] = resp.body_bytes();
  if (http.http_bodiless(resp.status)) {
    body = [];
  }
  let head: Conn = c.respond_head(id, resp, body.len() as i64);
  let w: Conn = head.write(id, body);
  return w.end(id, resp.trailers);
}

pub function (c: Conn) respond_head(id: i64, resp: HttpResponse, length: i64): Conn {
  return Conn { ...c, session: c.session.send_headers(id, __response_fields(resp, length), false) }.__applied(0 as i64);
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
