# Scripted HTTP network responses

This module provides a scripted HTTP network with virtual time.

## Model

An answer combines response bytes with first-byte delay, chunk delay, chunk size, and close policy. A simulated network delivers these answers under virtual time.

Hosts are names or address strings. A name resolves to the addresses that `host` gives it. Each latency is a `Duration` of virtual time.

## Contracts

Tests can model resets, silence, redirects, and partial responses. Scripted time values replace wall-clock delays and make transport outcomes repeatable.
`flaky` injects a fault into a share of the requests. The simulation's seeded generator selects the requests, so one seed always selects the same requests.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 pub struct Answer>>

<<3 pub function reply(status: i32, body: string): Answer>>

<<4 const CLOSING: string>>

<<5 pub function raw(text: string): Answer>>

<<6 pub struct Name>>

<<7 pub function new(drv: sim.Sim): Net>>

<<8 impl fetch.Transport for Net>>

<<9 pub function taped(req: fetch.Request, resp: HttpResponse): string>>

<<10 function __wire_only(name: string): boolean>>

<<11 pub function send_taped[T: fetch.Transport](tr: T, tape: Cell[string], req: fetch.Request, policy: fetch.Po...>>

<<12 struct __Exchange>>

<<13 function __exchange(line: string): Option[__Exchange]>>

<<14 function __replayed(n: Net, e: __Exchange): Net>>

<<15 function __target(data: u8[]): string>>

<<16 function __set_conn(n: Net, id: i32, row: string): i32>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module provides a scripted HTTP network with virtual time.
import "std/i32";
import "std/sim";
import "std/fetch";
import "std/http";
import "std/net";
import "std/dns";
import "std/i64";
import "std/hex";
import "std/utf8";
import "std/url";
import "std/json";
import "std/headers";
import "std/time";
import "std/quic/conn" as quic;
import "std/quic/packet";
import "std/http/h3";
import "std/http/h3q";
import "std/tls/identity";
```

### `2 pub struct Answer`

```fern
<<2 pub struct Answer>>=
// `delay` passes before the first byte; `every` passes between chunks of `chunk` bytes.
pub struct Answer { bytes: u8[], delay: Duration, every: Duration, chunk: i32, close: boolean }
```

### `3 pub function reply(status: i32, body: string): Answer`

```fern
<<3 pub function reply(status: i32, body: string): Answer>>=
pub function reply(status: i32, body: string): Answer {
  return raw("HTTP/1.1 " + status.to_string() + " Status\r\nContent-Length: " + body.len().to_string() + CLOSING + body);
}

pub function redirect(status: i32, location: string): Answer {
  return raw("HTTP/1.1 " + status.to_string() + " Status\r\nLocation: " + location + "\r\nContent-Length: 0" + CLOSING);
}
```

### `4 const CLOSING: string`

```fern
<<4 const CLOSING: string>>=
const CLOSING: string = "\r\nConnection: close\r\n\r\n";
```

### `5 pub function raw(text: string): Answer`

```fern
<<5 pub function raw(text: string): Answer>>=
pub function raw(text: string): Answer {
  return raw_bytes(text.bytes());
}

pub function raw_bytes(bytes: u8[]): Answer {
  return Answer { bytes: bytes, delay: time.DURATION_ZERO, every: time.DURATION_ZERO, chunk: 0, close: true };
}

pub function reset(): Answer {
  let none: u8[] = [];
  return raw_bytes(none);
}

pub function silent(): Answer {
  return Answer { ...reset(), close: false };
}

pub function (a: Answer) after(delay: Duration): Answer {
  return Answer { ...a, delay: delay };
}

pub function (a: Answer) in_chunks(size: i32, every: Duration): Answer {
  return Answer { ...a, chunk: size, every: every };
}

// Only the first `n` bytes of the answer arrive.
pub function (a: Answer) cut(n: i32): Answer {
  if (n >= a.bytes.len()) {
    return a;
  }
  let h = buf_new(n);
  buf_push_bytes_range(h, a.bytes, 0, n);
  let bytes = buf_take_bytes(h);
  buf_free(h);
  return Answer { ...a, bytes: bytes };
}

pub function (a: Answer) held(): Answer {
  return Answer { ...a, close: false };
}

pub function (a: Answer) kept(): Answer {
  let at = __find_bytes(a.bytes, CLOSING.as_bytes());
  let end = __find_bytes(a.bytes, "\r\n\r\n".as_bytes());
  if (at < 0 || end < 0 || at + CLOSING.len() != end + 4) {
    return a.held();
  }
  let h = buf_new(a.bytes.len() - CLOSING.len() + 4);
  buf_push_bytes_range(h, a.bytes, 0, at);
  buf_push(h, "\r\n\r\n");
  buf_push_bytes_range(h, a.bytes, at + CLOSING.len(), a.bytes.len());
  let bytes = buf_take_bytes(h);
  buf_free(h);
  return Answer { ...a, bytes: bytes, close: false };
}
```

### `6 pub struct Name`

```fern
<<6 pub struct Name>>=
pub struct Name { host: string, addrs: string[] }

pub struct Listener { addr: string, port: i32, connect: Duration }

pub struct Route { addr: string, port: i32, path: string, answers: Answer[] }

pub struct Server { addr: string, port: i32, handler: (HttpRequest) => HttpResponse }

pub struct Flake { addr: string, port: i32, path: string, percent: i32, fault: Answer }

// Endpoint is an HTTP/3 server at a UDP port, with the identity its TLS
// presents.
pub struct Endpoint { addr: string, port: i32, id: identity.Identity, handler: (HttpRequest) => HttpResponse }

// Peer is one datagram socket the client opened: the HTTP/3 server it
// reaches when an endpoint listens there, and the datagrams on their way
// back with the time each arrives.
pub struct Peer { id: i32, endpoint: i32, server: Option[h3q.Parked], inbox: u8[][], due: i64[], open: boolean }

pub struct Net { drv: sim.Sim, names: Name[], listeners: Listener[], routes: Route[], servers: Server[], flakes: Flake[], conns: Cell[string], hits: Cell[string], sent: Cell[string], made: Cell[string], next: Cell[i32], pool: fetch.Idle, endpoints: Endpoint[], peers: Cell[Peer[]], delay: Duration, loss: i32, carried: Cell[i32], h3_served: Cell[i32], h3_accepted: Cell[i32] }
```

### `7 pub function new(drv: sim.Sim): Net`

```fern
<<7 pub function new(drv: sim.Sim): Net>>=
pub function new(drv: sim.Sim): Net {
  let names: Name[] = [];
  let listeners: Listener[] = [];
  let routes: Route[] = [];
  let servers: Server[] = [];
  let flakes: Flake[] = [];
  let endpoints: Endpoint[] = [];
  let peers: Peer[] = [];
  return Net { drv: drv, names: names, listeners: listeners, routes: routes, servers: servers, flakes: flakes, conns: cell_new(""), hits: cell_new(""), sent: cell_new(""), made: cell_new(""), next: cell_new(1), pool: fetch.idle(), endpoints: endpoints, peers: cell_new(peers), delay: time.DURATION_ZERO, loss: 0, carried: cell_new(0), h3_served: cell_new(0), h3_accepted: cell_new(0) };
}

pub function (n: Net) pooling(pool: fetch.Idle): Net {
  return Net { ...n, pool: pool };
}

pub function (n: Net) host(name: string, addrs: string[]): Net {
  return Net { ...n, names: n.names.append(Name { host: name, addrs: addrs }) };
}

// A dial to the listener takes `connect` of virtual time.
pub function (n: Net) listen(addr: string, port: i32, connect: Duration): Net {
  return Net { ...n, listeners: n.listeners.append(Listener { addr: __canon(addr), port: port, connect: connect }) };
}

pub function (n: Net) route(addr: string, port: i32, path: string, answers: Answer[]): Net {
  return Net { ...n, routes: n.routes.append(Route { addr: __canon(addr), port: port, path: path, answers: answers }) };
}

pub function (n: Net) serve(addr: string, port: i32, handler: (HttpRequest) => HttpResponse): Net {
  let listening = n.listen(addr, port, time.DURATION_ZERO);
  return Net { ...listening, servers: listening.servers.append(Server { addr: __canon(addr), port: port, handler: handler }) };
}

// A request for `path` at `addr` and `port` gets `fault` instead of its answer, with a probability of `percent` in 100. The path "*" matches each request. A fault does not use a scripted answer of the route.
pub function (n: Net) flaky(addr: string, port: i32, path: string, percent: i32, fault: Answer): Net {
  return Net { ...n, flakes: n.flakes.append(Flake { addr: __canon(addr), port: port, path: path, percent: percent, fault: fault }) };
}

// serve_h3 answers HTTP/3 at `addr` and UDP `port` with `handler`, as the
// server `id` identifies. A TCP answer served from `addr` then carries an
// Alt-Svc field naming the port.
pub function (n: Net) serve_h3(addr: string, port: i32, id: identity.Identity, handler: (HttpRequest) => HttpResponse): Net {
  return Net { ...n, endpoints: n.endpoints.append(Endpoint { addr: __canon(addr), port: port, id: id, handler: handler }) };
}

// datagram_delay is the time a datagram takes each way. None by default.
pub function (n: Net) datagram_delay(d: Duration): Net {
  return Net { ...n, delay: d };
}

// datagram_loss drops every `every`-th datagram, in either direction. 0
// drops none.
pub function (n: Net) datagram_loss(every: i32): Net {
  return Net { ...n, loss: every };
}

// connects is how many TCP connections were made.
pub function (n: Net) connects(): i32 {
  return __rows(n.conns.get()).len();
}

// h3_requests is how many requests the HTTP/3 endpoints answered.
pub function (n: Net) h3_requests(): i32 {
  return n.h3_served.get();
}

// h3_connections is how many QUIC connections the HTTP/3 endpoints accepted.
pub function (n: Net) h3_connections(): i32 {
  return n.h3_accepted.get();
}

// datagrams is how many datagrams either side sent, lost ones included.
pub function (n: Net) datagrams(): i32 {
  return n.carried.get();
}

pub function (n: Net) open(): i32 {
  let count = 0;
  for row in __rows(n.conns.get()) {
    if (row[7] == "1") {
      count += 1;
    }
  }
  return count;
}

pub function (n: Net) sent_count(): i32 {
  return __sent_all(n).len();
}

pub function (n: Net) sent(i: i32): u8[] {
  let all = __sent_all(n);
  if (i < 0 || i >= all.len()) {
    let none: u8[] = [];
    return none;
  }
  return __journal_bytes(all[i]);
}
```

### `8 impl fetch.Transport for Net`

```fern
<<8 impl fetch.Transport for Net>>=
impl fetch.Transport for Net {
  function now(self: Net): Instant {
    return time.instant_from_nanos(self.drv.clock_ns.get());
  }
  function wall(self: Net): time.Timestamp {
    let d = time.duration_nanos(self.drv.clock_ns.get());
    return time.Timestamp { sec: d.sec, nsec: d.nsec };
  }
  function random_bytes(self: Net, n: i32): u8[] {
    let out: u8[] = [];
    while (out.len() < n) {
      out = out.append((self.drv.random_i32() & 255) as u8);
    }
    return out;
  }
  function lookup(self: Net, host: string, checked: boolean): Result[fetch.Lookup, dns.DnsError] {
    let found = __addrs_of(self, host);
    if (found.len() == 0) {
      return Err(dns.NoSuchName);
    }
    let none: net.IpAddr[] = [];
    if (!checked) {
      return Ok(fetch.Lookup { dial: found, named: none, judged: none });
    }
    return Ok(fetch.Lookup { dial: found, named: found, judged: found });
  }
  function connect(self: Net, addrs: net.IpAddr[], port: i32, timeout: Duration): Result[i32, net.NetError] {
    let timeout_ms = timeout.as_millis();
    let fallback_ms = dns.dial_options().fallback.as_millis();
    let start_ms: i64 = 0;
    let best_ms: i64 = 0 - 1;
    let best = "";
    for a in addrs {
      let key = a.to_string();
      let delay = __connect_ms(self, key, port);
      if (delay >= 0) {
        let done_ms = start_ms + delay;
        if (best_ms < 0 || done_ms < best_ms) {
          best_ms = done_ms;
          best = key;
        }
        start_ms += fallback_ms;
      }
    }
    if (timeout_ms <= 0) {
      return Err(net.TimedOut);
    }
    if (best_ms < 0) {
      return Err(net.ConnectionRefused);
    }
    if (best_ms > timeout_ms) {
      if (!self.drv.advance_to(time.instant_from_nanos(self.drv.clock_ns.get()).add(timeout))) {
        return Err(net.Interrupted);
      }
      return Err(net.TimedOut);
    }
    if (!self.drv.advance_to(time.instant_from_nanos(self.drv.clock_ns.get()).add(time.duration_millis(best_ms)))) {
      return Err(net.Interrupted);
    }
    let id = self.next.get();
    self.next.set(id + 1);
    self.conns.set(self.conns.get() + __row(id, best, port, 0 - 1, 0, 0, 0, 1));
    return Ok(id);
  }
  function write(self: Net, conn: i32, bytes: u8[]): i32 {
    let row = __conn(self, conn);
    if (row.len() == 0 || row[7] != "1") {
      return -9;
    }
    let encoded = hex.encode(bytes);
    self.sent.set(self.sent.get() + encoded.len().to_string() + ":" + encoded);
    if (__busy(self, row)) {
      return bytes.len();
    }
    let port = __field(row, 2);
    let target = __target(bytes);
    let route: i32 = -2;
    let answer = __flaked(self, row[1], port, target);
    if (answer >= 0) {
      route = __routed(self, row[1], port, target);
      if (route >= 0) {
        answer = __took(self, route);
      } else {
        route = __served(self, row[1], port, bytes);
      }
    }
    let first_ms = __answer(self, route, answer).delay.as_millis();
    __set_conn(self, conn, __row(conn, row[1], port, route, answer, 0, self.drv.clock_ns.get() + first_ms * 1000000, 1));
    return bytes.len();
  }
  function read(self: Net, conn: i32, max: i32, wait: Duration): Result[net.Recv, net.NetError] {
    let row = __conn(self, conn);
    let now = self.drv.clock_ns.get();
    let deadline = now + wait.as_nanos();
    let route = __field(row, 3);
    if (row.len() == 0 || route == 0 - 1) {
      return Ok(__elapsed(self, deadline));
    }
    let a = __answer(self, route, __field(row, 4));
    let offset = __field(row, 5);
    if (offset >= a.bytes.len()) {
      if (a.close) {
        let none: u8[] = [];
        return Ok(net.Chunk(none));
      }
      return Ok(__elapsed(self, deadline));
    }
    let ready = __field64(row, 6);
    if (ready > deadline) {
      return Ok(__elapsed(self, deadline));
    }
    if (!self.drv.advance_to(time.instant_from_nanos(ready))) {
      return Ok(net.Abandoned);
    }
    let size = a.bytes.len() - offset;
    if (a.chunk > 0 && a.chunk < size) {
      size = a.chunk;
    }
    if (max < size) {
      size = max;
    }
    let out: u8[] = [];
    let k = 0;
    while (k < size) {
      out = out.append(a.bytes[offset + k]);
      k += 1;
    }
    __set_conn(self, conn, __row(conn, row[1], __field(row, 2), route, __field(row, 4), offset + size, self.drv.clock_ns.get() + a.every.as_nanos(), 1));
    return Ok(net.Chunk(out));
  }
  function close(self: Net, conn: i32): i32 {
    let k = __peer_at(self, conn);
    if (k >= 0) {
      __peer_set(self, k, Peer { ...self.peers.get()[k], open: false });
      return 0;
    }
    let row = __conn(self, conn);
    if (row.len() > 0) {
      __set_conn(self, conn, __row(conn, row[1], __field(row, 2), __field(row, 3), __field(row, 4), __field(row, 5), __field64(row, 6), 0));
    }
    return 0;
  }
  function idle(self: Net): fetch.Idle {
    return self.pool;
  }
  function socket(self: Net, conn: i32): Option[Socket] {
    return None;
  }
  function datagram_open(self: Net, addrs: net.IpAddr[], port: i32): Result[i32, net.NetError] {
    if (addrs.len() == 0) {
      return Err(net.ConnectionRefused);
    }
    let id = self.next.get();
    self.next.set(id + 1);
    let inbox: u8[][] = [];
    let due: i64[] = [];
    self.peers.set(self.peers.get().append(Peer { id: id, endpoint: __endpoint_at(self, addrs[0].to_string(), port), server: None, inbox: inbox, due: due, open: true }));
    return Ok(id);
  }
  function datagram_send(self: Net, conn: i32, data: u8[]): i32 {
    let k = __peer_at(self, conn);
    if (k < 0) {
      return -9;
    }
    if (__lost(self)) {
      return data.len();
    }
    let at = self.drv.clock_ns.get() + self.delay.as_nanos();
    __peer_set(self, k, __h3_delivered(self, self.peers.get()[k], data, at));
    return data.len();
  }
  function datagram_recv(self: Net, conn: i32, max: i32, wait: Duration): Result[net.Recv, net.NetError] {
    let k = __peer_at(self, conn);
    if (k < 0) {
      return Err(net.Other(9));
    }
    let deadline = self.drv.clock_ns.get() + wait.as_nanos();
    while (true) {
      let p = self.peers.get()[k];
      let next = __h3_next(p);
      if (__h3_timer_first(p, next, deadline)) {
        if (!__sim_advance(self, __h3_timer(p))) {
          return Ok(net.Abandoned);
        }
        __peer_set(self, k, __h3_ticked(self, p, self.drv.clock_ns.get()));
        continue;
      }
      if (next >= 0 && p.due[next] <= deadline) {
        if (!__sim_advance(self, p.due[next])) {
          return Ok(net.Abandoned);
        }
        let d = p.inbox[next];
        __peer_set(self, k, Peer { ...p, inbox: __without_at(p.inbox, next), due: __without_at(p.due, next) });
        return Ok(net.Chunk(d));
      }
      if (!__sim_advance(self, deadline)) {
        return Ok(net.Abandoned);
      }
      return Ok(net.Elapsed);
    }
    return Ok(net.Elapsed);
  }
}

function __sim_advance(n: Net, to: i64): boolean {
  if (to <= n.drv.clock_ns.get()) {
    return true;
  }
  return n.drv.advance_to(time.instant_from_nanos(to));
}

function __peer_at(n: Net, id: i32): i32 {
  let peers = n.peers.get();
  for k in 0..peers.len() {
    if (peers[k].id == id && peers[k].open) {
      return k;
    }
  }
  return -1;
}

function __peer_set(n: Net, k: i32, p: Peer): void {
  let peers = n.peers.get();
  let none: Peer[] = [];
  n.peers.set(none);
  n.peers.set(peers.with(k, p));
}

// __lost counts a datagram and says whether the loss schedule drops it.
function __lost(n: Net): boolean {
  let count = n.carried.get() + 1;
  n.carried.set(count);
  return n.loss > 0 && count % n.loss == 0;
}

function __endpoint_at(n: Net, addr: string, port: i32): i32 {
  for k in 0..n.endpoints.len() {
    if (n.endpoints[k].addr == addr && n.endpoints[k].port == port) {
      return k;
    }
  }
  return -1;
}

function __sim_bytes(n: Net, count: i32): u8[] {
  let out: u8[] = [];
  while (out.len() < count) {
    out = out.append((n.drv.random_i32() & 255) as u8);
  }
  return out;
}

function __without_at[T](xs: T[], k: i32): T[] {
  let out: T[] = [];
  for i in 0..xs.len() {
    if (i != k) {
      out = out.append(xs[i]);
    }
  }
  return out;
}

// __h3_delivered hands a datagram the client sent to the peer's server at
// `at`, accepting the connection on its first.
function __h3_delivered(n: Net, p: Peer, data: u8[], at: i64): Peer {
  if (p.endpoint < 0) {
    return p;
  }
  let now = time.instant_from_nanos(at);
  let Some(s) = __h3_server(n, p, data, now) else {
    return p;
  };
  return __h3_sent(n, p, __h3_answering(n, p.endpoint, s.receive(data, now)), at);
}

function __h3_server(n: Net, p: Peer, data: u8[], now: Instant): Option[h3q.Conn] {
  if let Some(parked) = p.server {
    return Some(h3q.unpark(parked));
  }
  let cfg = h3q.transport(quic.config([]).server_side(n.endpoints[p.endpoint].id));
  let Ok(h) = packet.parse(data, 0, cfg.cid_len) else {
    return None;
  };
  let Ok(q) = quic.accept(cfg, __sim_bytes(n, quic.server_entropy(cfg)), h, now) else {
    return None;
  };
  n.h3_accepted.set(n.h3_accepted.get() + 1);
  return Some(h3q.server(q, h3.config()));
}

// __h3_answering answers each request the server has read with the
// endpoint's handler.
function __h3_answering(n: Net, endpoint: i32, s: h3q.Conn): h3q.Conn {
  let evs = s.events;
  let cur = s.events_taken();
  let handle = n.endpoints[endpoint].handler;
  for e in evs {
    if let h3q.Requested(id, req) = e {
      n.h3_served.set(n.h3_served.get() + 1);
      cur = cur.respond(id, handle(req));
    }
  }
  return cur;
}

// __h3_sent queues what the server sends at `at` for the client, each
// datagram due after the delay.
function __h3_sent(n: Net, p: Peer, s: h3q.Conn, at: i64): Peer {
  let flushed = s.transmit(time.instant_from_nanos(at));
  let inbox = p.inbox;
  let due = p.due;
  for d in flushed.datagrams() {
    if (!__lost(n)) {
      inbox = inbox.append(d);
      due = due.append(at + n.delay.as_nanos());
    }
  }
  return Peer { ...p, server: Some(flushed.sent().park()), inbox: inbox, due: due };
}

function __h3_ticked(n: Net, p: Peer, at: i64): Peer {
  let Some(parked) = p.server else {
    return p;
  };
  return __h3_sent(n, p, __h3_answering(n, p.endpoint, h3q.unpark(parked).tick(time.instant_from_nanos(at))), at);
}

// __h3_timer is when the peer's server next needs a tick, or -1.
function __h3_timer(p: Peer): i64 {
  let Some(parked) = p.server else {
    return -1i64;
  };
  let Some(at) = parked.quic.deadline() else {
    return -1i64;
  };
  return time.instant_nanos(at);
}

// __h3_timer_first says whether the server's timer comes before `deadline`
// and before the datagram at `next` in the inbox, if there is one.
function __h3_timer_first(p: Peer, next: i32, deadline: i64): boolean {
  let timer = __h3_timer(p);
  return timer >= 0 && timer <= deadline && (next < 0 || timer < p.due[next]);
}

// __h3_next is the index of the datagram due first in the peer's inbox, or
// -1.
function __h3_next(p: Peer): i32 {
  let best = -1;
  for k in 0..p.due.len() {
    if (best < 0 || p.due[k] < p.due[best]) {
      best = k;
    }
  }
  return best;
}

// __with_alt_svc adds the Alt-Svc field for the HTTP/3 endpoint at `addr`
// to a TCP answer, as std/serve does.
function __with_alt_svc(n: Net, addr: string, resp: HttpResponse): HttpResponse {
  if (resp.headers.has("alt-svc")) {
    return resp;
  }
  for e in n.endpoints {
    if (e.addr == addr) {
      return HttpResponse { ...resp, headers: resp.headers.append("alt-svc", "h3=\":" + e.port.to_string() + "\"; ma=86400") };
    }
  }
  return resp;
}
```

### `9 pub function taped(req: fetch.Request, resp: HttpResponse): string`

```fern
<<9 pub function taped(req: fetch.Request, resp: HttpResponse): string>>=
pub function taped(req: fetch.Request, resp: HttpResponse): string {
  let pairs: json.JsonValue[] = [];
  let i = 0;
  while (i < resp.headers.names.len()) {
    let name = resp.headers.names[i].to_lower();
    if (!__wire_only(name)) {
      pairs = pairs.append(json.JArray([json.JString(name), json.JString(resp.headers.values[i])]));
    }
    i += 1;
  }
  let keys: string[] = ["method", "url", "status", "headers"];
  let values: json.JsonValue[] = [json.JString(req.method), json.JString(req.url), json.JNumber(resp.status.to_string()), json.JArray(pairs)];
  let body = resp.body_bytes();
  match (utf8.from_bytes(body)) {
    Ok(text) => {
      keys = keys.append("body");
      values = values.append(json.JString(text));
    },
    Err(_) => {
      keys = keys.append("body_hex");
      values = values.append(json.JString(hex.encode(body)));
    }
  }
  return json.encode(json.JObject(json.object_of(keys, values))) + "\n";
}
```

### `10 function __wire_only(name: string): boolean`

```fern
<<10 function __wire_only(name: string): boolean>>=
function __wire_only(name: string): boolean {
  return name == "content-length" || name == "content-encoding" || name == "transfer-encoding" || name == "connection";
}
```

### `11 pub function send_taped[T: fetch.Transport](tr: T, tape: Cell[string], req: fetch.Request, policy: fetch.Po...`

```fern
<<11 pub function send_taped[T: fetch.Transport](tr: T, tape: Cell[string], req: fetch.Request, policy: fetch.Po...>>=
pub function send_taped[T: fetch.Transport](tr: T, tape: Cell[string], req: fetch.Request, policy: fetch.Policy): Result[HttpResponse, fetch.FetchError] {
  match (fetch.send_on(tr, req, policy)) {
    Ok(resp) => {
      tape.set(tape.get() + taped(req, resp));
      return Ok(resp);
    },
    Err(e) => {
      return Err(e);
    }
  }
}

pub function (n: Net) replay(cassette: string): Result[Net, string] {
  let out = n;
  let line_no = 0;
  for line in cassette.split("\n") {
    line_no += 1;
    if (line.len() > 0) {
      match (__exchange(line)) {
        Some(e) => {
          out = __replayed(out, e);
        },
        None => {
          return Err("sim_fetch: cassette line " + line_no.to_string() + " is not an exchange");
        }
      }
    }
  }
  return Ok(out);
}
```

### `12 struct __Exchange`

```fern
<<12 struct __Exchange>>=
struct __Exchange { host: string, port: i32, target: string, answer: Answer }
```

### `13 function __exchange(line: string): Option[__Exchange]`

```fern
<<13 function __exchange(line: string): Option[__Exchange]>>=
function __exchange(line: string): Option[__Exchange] {
  match (json.parse(line)) {
    Ok(v) => {
      match (v) {
        json.JObject(o) => {
          return __exchange_of(o);
        },
        _ => {
          return None;
        }
      }
    },
    Err(_) => {
      return None;
    }
  }
  return None;
}

function __exchange_of(o: json.JsonObject): Option[__Exchange] {
  let method = __string_member(o, "method");
  let status = __int(__string_member(o, "status"));
  let Ok(u) = url.parse(__string_member(o, "url")) else {
    return None;
  };
  if (method.len() == 0 || status <= 0 || u.host.len() == 0) {
    return None;
  }
  let port = u.port;
  if (port == 0 || u.scheme == "https") {
    port = 80;
  }
  let target = u.path;
  if (target.len() == 0) {
    target = "/";
  }
  if (u.query.len() > 0) {
    target = target + "?" + u.query;
  }
  let Some(body) = __body_member(o) else {
    return None;
  };
  let resp = HttpResponse { status: status, body: BodyBytes(body), headers: __headers_member(o), trailers: headers.new(), background: None };
  return Some(__Exchange { host: u.host, port: port, target: target, answer: raw_bytes(http.serialize_response_to_bytes(method, "HTTP/1.1", resp, false)) });
}

function __string_member(o: json.JsonObject, key: string): string {
  match (o.get(key)) {
    Some(v) => {
      match (v) {
        json.JString(s) => {
          return s;
        },
        json.JNumber(digits) => {
          return digits;
        },
        _ => {
          return "";
        }
      }
    },
    None => {
      return "";
    }
  }
  return "";
}

function __headers_member(o: json.JsonObject): HeaderMap {
  let out = headers.new();
  match (o.get("headers")) {
    Some(v) => {
      match (v) {
        json.JArray(pairs) => {
          for p in pairs {
            match (p) {
              json.JArray(kv) => {
                if (kv.len() == 2) {
                  out = out.append(__text(kv[0]), __text(kv[1]));
                }
              },
              _ => {}
            }
          }
        },
        _ => {}
      }
    },
    None => {}
  }
  return out;
}

function __text(v: json.JsonValue): string {
  match (v) {
    json.JString(s) => {
      return s;
    },
    _ => {
      return "";
    }
  }
  return "";
}

function __body_member(o: json.JsonObject): Option[u8[]] {
  if (o.contains("body_hex")) {
    match (hex.decode(__string_member(o, "body_hex"))) {
      Ok(bytes) => {
        return Some(bytes);
      },
      Err(_) => {
        return None;
      }
    }
  }
  return Some(__string_member(o, "body").bytes());
}
```

### `14 function __replayed(n: Net, e: __Exchange): Net`

```fern
<<14 function __replayed(n: Net, e: __Exchange): Net>>=
function __replayed(n: Net, e: __Exchange): Net {
  let out = n;
  let addr = __replay_addr(out, e.host);
  if (__addrs_of(out, e.host).len() == 0) {
    out = out.host(e.host, [addr]);
  }
  if (__connect_ms(out, addr, e.port) < 0) {
    out = out.listen(addr, e.port, time.DURATION_ZERO);
  }
  let i = 0;
  while (i < out.routes.len()) {
    let r = out.routes[i];
    if (r.addr == addr && r.port == e.port && r.path == e.target) {
      return Net { ...out, routes: out.routes.with(i, Route { ...r, answers: r.answers.append(e.answer) }) };
    }
    i += 1;
  }
  return out.route(addr, e.port, e.target, [e.answer]);
}

function __replay_addr(n: Net, host: string): string {
  let known = __addrs_of(n, host);
  if (known.len() > 0) {
    return known[0].to_string();
  }
  let last = 34;
  while (last < 255) {
    let candidate = "93.184.216." + last.to_string();
    if (!__addr_named(n, candidate)) {
      return candidate;
    }
    last += 1;
  }
  return "93.184.216.255";
}

function __addr_named(n: Net, addr: string): boolean {
  for name in n.names {
    for a in name.addrs {
      if (a == addr) {
        return true;
      }
    }
  }
  return false;
}

function __busy(n: Net, row: string[]): boolean {
  let route = __field(row, 3);
  if (route == -1) {
    return false;
  }
  let a = __answer(n, route, __field(row, 4));
  return a.close || __field(row, 5) < a.bytes.len();
}

function __canon(addr: string): string {
  match (net.ip_parse(addr)) {
    Ok(a) => {
      return a.to_string();
    },
    Err(_) => {
      return addr;
    }
  }
  return addr;
}

function __connect_ms(n: Net, addr: string, port: i32): i64 {
  for l in n.listeners {
    if (l.addr == addr && l.port == port) {
      return l.connect.as_millis();
    }
  }
  return -1;
}

function __addrs_of(n: Net, host: string): net.IpAddr[] {
  let out: net.IpAddr[] = [];
  match (net.ip_parse(host)) {
    Ok(a) => {
      return out.append(a);
    },
    Err(_) => {}
  }
  for name in n.names {
    if (name.host == host) {
      for s in name.addrs {
        match (net.ip_parse(s)) {
          Ok(a) => {
            out = out.append(a);
          },
          Err(_) => {}
        }
      }
    }
  }
  return out;
}

function __elapsed(n: Net, to_ns: i64): net.Recv {
  if (!n.drv.advance_to(time.instant_from_nanos(to_ns))) {
    return net.Abandoned;
  }
  return net.Elapsed;
}

function __find_bytes(data: [u8], needle: [u8]): i32 {
  let at = 0;
  while (at <= data.len() - needle.len()) {
    let j = 0;
    while (j < needle.len() && data[at + j] == needle[j]) {
      j += 1;
    }
    if (j == needle.len()) {
      return at;
    }
    at += 1;
  }
  return -1;
}
```

### `15 function __target(data: u8[]): string`

```fern
<<15 function __target(data: u8[]): string>>=
function __target(data: u8[]): string {
  let end = __find_bytes(data, "\r\n".as_bytes());
  if (end < 0) {
    end = data.len();
  }
  let start = 0;
  while (start < end && data[start] != 32 as u8) {
    start += 1;
  }
  if (start == end) {
    return "";
  }
  start += 1;
  let stop = start;
  while (stop < end && data[stop] != 32 as u8) {
    stop += 1;
  }
  let bytes = __alloc_u8(stop - start);
  let i = 0;
  while (start + i < stop) {
    bytes = bytes.with(i, data[start + i]);
    i += 1;
  }
  match (utf8.from_bytes(bytes)) {
    Ok(target) => {
      return target;
    },
    Err(_) => {
      return "";
    }
  }
}

function __routed(n: Net, addr: string, port: i32, target: string): i32 {
  let i = 0;
  while (i < n.routes.len()) {
    let r = n.routes[i];
    if (r.addr == addr && r.port == port && (r.path == target || r.path == "*")) {
      return i;
    }
    i += 1;
  }
  return -2;
}

// Answer index 0, or `-1 - k` when flake `k` matches and the seeded draw selects it.
function __flaked(n: Net, addr: string, port: i32, target: string): i32 {
  let k = 0;
  while (k < n.flakes.len()) {
    let f = n.flakes[k];
    if (f.addr == addr && f.port == port && (f.path == target || f.path == "*")) {
      if ((n.drv.random_i32() & 2147483647) % 100 < f.percent) {
        return -1 - k;
      }
    }
    k += 1;
  }
  return 0;
}

function __took(n: Net, route: i32): i32 {
  let counts = n.hits.get().split(",");
  let out = "";
  let before = 0;
  let i = 0;
  while (i < n.routes.len()) {
    let c = 0;
    if (i < counts.len()) {
      c = __int(counts[i]);
    }
    if (i == route) {
      before = c;
      c += 1;
    }
    out = out + c.to_string() + ",";
    i += 1;
  }
  n.hits.set(out);
  return before;
}

function __served(n: Net, addr: string, port: i32, bytes: u8[]): i32 {
  for s in n.servers {
    if (s.addr == addr && s.port == port) {
      let k = __journal_all(n.made.get()).len();
      let entry = __response_entry(n, s, bytes);
      n.made.set(n.made.get() + entry.len().to_string() + ":" + entry);
      return -3 - k;
    }
  }
  return -2;
}

function __response_entry(n: Net, s: Server, bytes: u8[]): string {
  match (http.parse_request_framed(bytes)) {
    http.Complete(f) => {
      let h = s.handler;
      let resp = __with_alt_svc(n, s.addr, h(f.request));
      let flag = "c";
      if (f.keep_alive) {
        flag = "k";
      }
      return flag + hex.encode(http.serialize_response_to_bytes(f.request.method, f.version, resp, f.keep_alive));
    },
    http.Incomplete => {
      return __refused_entry();
    },
    http.Continue => {
      return __refused_entry();
    },
    http.Malformed(_) => {
      return __refused_entry();
    }
  }
  return __refused_entry();
}

function __refused_entry(): string {
  return "c" + hex.encode(http.serialize_response_conn_bytes(http.text(400, "bad request"), false));
}

// A negative `i` is the fault of flake `-1 - i`.
function __answer(n: Net, route: i32, i: i32): Answer {
  if (i < 0) {
    return n.flakes[-1 - i].fault;
  }
  if (route <= -3) {
    let entry = __journal_all(n.made.get())[-3 - route];
    return Answer { bytes: __journal_bytes(slice_unchecked(entry, 1, entry.len()).to_owned()), delay: time.DURATION_ZERO, every: time.DURATION_ZERO, chunk: 0, close: entry[0] == b'c' };
  }
  if (route < 0) {
    return reply(404, "");
  }
  let answers = n.routes[route].answers;
  if (answers.len() == 0) {
    return reply(404, "");
  }
  if (i >= answers.len()) {
    return answers[answers.len() - 1];
  }
  return answers[i];
}

function __row(id: i32, addr: string, port: i32, route: i32, answer: i32, offset: i32, ready_ns: i64, open: i32): string {
  return id.to_string() + "|" + addr + "|" + port.to_string() + "|" + route.to_string() + "|" + answer.to_string() + "|" + offset.to_string() + "|" + ready_ns.to_string() + "|" + open.to_string() + ";";
}

function __rows(table: string): string[][] {
  let rows: string[][] = [];
  for entry in table.split(";") {
    if (entry.len() > 0) {
      rows = rows.append(entry.split("|"));
    }
  }
  return rows;
}

function __conn(n: Net, id: i32): string[] {
  for row in __rows(n.conns.get()) {
    if (__field(row, 0) == id) {
      return row;
    }
  }
  let none: string[] = [];
  return none;
}
```

### `16 function __set_conn(n: Net, id: i32, row: string): i32`

```fern
<<16 function __set_conn(n: Net, id: i32, row: string): i32>>=
function __set_conn(n: Net, id: i32, row: string): i32 {
  let out = "";
  for r in __rows(n.conns.get()) {
    if (__field(r, 0) == id) {
      out += row;
    } else {
      out += __row(__field(r, 0), r[1], __field(r, 2), __field(r, 3), __field(r, 4), __field(r, 5), __field64(r, 6), __field(r, 7));
    }
  }
  n.conns.set(out);
  return 0;
}

function __field(row: string[], i: i32): i32 {
  if (i >= row.len()) {
    return 0;
  }
  return __int(row[i]);
}

function __int(s: string): i32 {
  if (s.len() > 0 && s[0] == b'-') {
    return 0 - __int(s.split("-")[1]);
  }
  match (i32.parse_i32_radix(s, 10)) {
    Ok(v) => {
      return v;
    },
    Err(_) => {
      return 0;
    }
  }
  return 0;
}

function __field64(row: string[], i: i32): i64 {
  let v = 0;
  if (i >= row.len()) {
    return v;
  }
  for b in row[i].bytes() {
    v = v * 10 + (b as i64 - 48);
  }
  return v;
}

function __sent_all(n: Net): string[] {
  return __journal_all(n.sent.get());
}

function __journal_all(log: string): string[] {
  let bs = log.bytes();
  let out: string[] = [];
  let at = 0;
  while (at < bs.len()) {
    let len = 0;
    while (at < bs.len() && bs[at] != 58 as u8) {
      len = len * 10 + (bs[at] as i32 - 48);
      at += 1;
    }
    at += 1;
    out = out.append(slice_unchecked(log, at, at + len).to_owned());
    at += len;
  }
  return out;
}

// Journal entries contain only hexadecimal data from this module.
function __journal_bytes(encoded: string): u8[] {
  match (hex.decode(encoded)) {
    Ok(bytes) => {
      return bytes;
    },
    Err(problem) => {
      eprint("sim_fetch: invalid internal journal: " + problem.message());
      exit(1);
    }
  }
  return [];
}
```
