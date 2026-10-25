# TLS 1.3 client sessions

This module provides TLS 1.3 client sessions and connections.

## Model

Client configuration supplies a server name, trust roots, ALPN protocols, and optional resumption or client identity. A session advances the TLS handshake and application records.

## Contracts

A session draws no randomness of its own: `start` takes exactly `entropy(config)` bytes from the caller, so a session given the same bytes and clock writes the same records. `connect` and `over` draw those bytes from the platform's random source.

A session step returns state, output, application data, and ticket updates. The caller must preserve the returned state and apply the configured verification policy.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct Config>>

<<4 pub function config(server_name: string, roots: verify.Roots): Config>>

<<5 pub struct Session>>

<<6 pub function start(config: Config, entropy: [u8], now: i64): Result[Step, tls.TlsError]>>

<<7 function __offer(config: Config, now: i64): Option[handshake.Resumption]>>

<<8 pub function (s: Session) read(input: [u8], now: i64): Result[Step, tls.Failure]>>

<<9 function __record(s: Session, record: [u8], now: i64): Result[Step, tls.Failure]>>

<<10 pub function (s: Session) write(data: [u8]): Result[Step, tls.TlsError]>>

<<11 pub struct Offloaded>>

<<12 pub function (s: Session) offload(): Option[Offloaded]>>

<<13 function __step(r: Result[handshake.ClientStep, handshake.HandshakeError]): Result[handshake.ClientStep, tl...>>

<<14 function __trusted(s: Session, v: handshake.Verification, now: i64): Result[void, tls.TlsError]>>

<<15 pub struct Connection>>

<<16 pub function connect(host: string, port: i32, config: Config, timeout: Duration): Result[Connection, tls.Tl...>>

<<17 function __handshake_by(fd: i32, config: Config, deadline: i64): Result[Connection, tls.TlsError]>>

<<18 pub function (c: Connection) send(data: [u8]): Result[Connection, tls.TlsError]>>

<<19 function __received(c: Connection, s: Session, data: u8[], ended: boolean): Received>>

<<20 struct Kerneled>>

<<21 function __kernel_next(c: Connection, read: ktls.Read): Result[Kerneled, tls.TlsError]>>

<<22 pub function (c: Connection) close(): void>>

<<23 function __send_fd(fd: i32, data: [u8]): Result[void, tls.TlsError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides TLS 1.3 client sessions and connections.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/int";
import "std/dns";
import "std/i32";
import "std/net";
import "std/time";
import "std/tls";
import "std/tls/handshake";
import "std/tls/identity";
import "std/tls/keyschedule";
import "std/tls/ktls";
import "std/tls/message";
import "std/tls/record";
import "std/tls/verify";
import "std/tls/x509";
```

### `3 pub struct Config`

```fern
<<3 pub struct Config>>=
pub struct Config { server_name: string, alpn: string[], roots: verify.Roots, resume: Option[Ticket], identity: Option[identity.Identity] }

pub struct Ticket { identity: u8[], psk: u8[], suite: i32, lifetime: u32, age_add: u32, received_ms: i64, server_name: string }
```

### `4 pub function config(server_name: string, roots: verify.Roots): Config`

```fern
<<4 pub function config(server_name: string, roots: verify.Roots): Config>>=
pub function config(server_name: string, roots: verify.Roots): Config {
  return Config { server_name: server_name, alpn: [], roots: roots, resume: None, identity: None };
}

pub function (c: Config) with_alpn(protocols: string[]): Config {
  return Config { ...c, alpn: protocols };
}

pub function (c: Config) with_ticket(t: Ticket): Config {
  return Config { ...c, resume: Some(t) };
}

pub function (c: Config) with_identity(id: identity.Identity): Config {
  return Config { ...c, identity: Some(id) };
}
```

### `5 pub struct Session`

```fern
<<5 pub struct Session>>=
pub struct Session { client: handshake.Client, name: string, roots: verify.Roots, identity: Option[identity.Identity], held: u8[] }

pub struct Step { session: Session, output: u8[], data: u8[], closed: boolean, tickets: Ticket[], rekeyed: handshake.KernelKeys }
```

### `6 pub function start(config: Config, entropy: [u8], now: i64): Result[Step, tls.TlsError]`

```fern
<<6 pub function start(config: Config, entropy: [u8], now: i64): Result[Step, tls.TlsError]>>=
pub function start(config: Config, entropy: [u8], now: i64): Result[Step, tls.TlsError] {
  let hc: handshake.ClientConfig = handshake.ClientConfig { ...__hello(config), resume: __offer(config, now) };
  match (handshake.client_start(hc, entropy)) {
    Ok(step) => {
      let s: Session = Session { client: step.client, name: config.server_name, roots: config.roots, identity: config.identity, held: [] };
      return Ok(Step { session: s, output: step.output, data: [], closed: false, tickets: [], rekeyed: __no_keys() });
    },
    Err(e) => {
      return Err(tls.HandshakeFailed(e));
    }
  }
  return Err(tls.ClosedEarly);
}

pub function entropy(config: Config): i32 {
  return handshake.client_entropy(__hello(config));
}

function __hello(config: Config): handshake.ClientConfig {
  let sni: string = config.server_name;
  if (__is_address(sni)) {
    sni = "";
  }
  let hc: handshake.ClientConfig = handshake.ClientConfig { ...handshake.client_config(sni), alpn: config.alpn };
  match (config.identity) {
    Some(id) => {
      return handshake.ClientConfig { ...hc, chain: id.chain, sign_schemes: [id.key.scheme()] };
    },
    None => {}
  }
  return hc;
}
```

### `7 function __offer(config: Config, now: i64): Option[handshake.Resumption]`

```fern
<<7 function __offer(config: Config, now: i64): Option[handshake.Resumption]>>=
function __offer(config: Config, now: i64): Option[handshake.Resumption] {
  match (config.resume) {
    Some(t) => {
      let age: i64 = now - t.received_ms;
      if (t.server_name != config.server_name || age < 0 as i64 || age >= t.lifetime as i64 * (1000 as i64)) {
        return None;
      }
      match (keyschedule.cipher_suite(t.suite)) {
        Some(suite) => {
          let obfuscated: i64 = age + t.age_add as i64 & 0xffffffff as i64;
          return Some(handshake.Resumption { identity: t.identity, psk: t.psk, suite: suite, obfuscated_age: obfuscated as u32 });
        },
        None => {
          return None;
        }
      }
    },
    None => {
      return None;
    }
  }
  return None;
}

function __is_address(name: string): boolean {
  match (net.ip_parse(name)) {
    Some(_) => {
      return true;
    },
    None => {
      return false;
    }
  }
  return false;
}
```

### `8 pub function (s: Session) read(input: [u8], now: i64): Result[Step, tls.Failure]`

```fern
<<8 pub function (s: Session) read(input: [u8], now: i64): Result[Step, tls.Failure]>>=
pub function (s: Session) read(input: [u8], now: i64): Result[Step, tls.Failure] {
  let buf: u8[] = __copy(s.held);
  buf = __cat(buf, input);
  let session: Session = Session { ...s, held: [] };
  let output: u8[] = [];
  let data: u8[] = [];
  let closed: boolean = false;
  let tickets: Ticket[] = [];
  let keys: handshake.KernelKeys = __no_keys();
  let at: i32 = 0;
  while (!closed && buf.len() - at >= 5) {
    let end: i32 = at + 5 + (buf[at + 3] as i32 << 8 | buf[at + 4] as i32);
    if (end > buf.len()) {
      break;
    }
    let next: Step = __record(session, buf[at:end], now)?;
    session = next.session;
    output = __cat(output, next.output);
    data = __cat(data, next.data);
    closed = next.closed;
    for t in next.tickets {
      tickets = tickets.append(t);
    }
    keys = keys.then(next.rekeyed);
    at = end;
  }
  return Ok(Step { session: Session { ...session, held: __copy(buf[at:buf.len()]) }, output: output, data: data, closed: closed, tickets: tickets, rekeyed: keys });
}
```

### `9 function __record(s: Session, record: [u8], now: i64): Result[Step, tls.Failure]`

```fern
<<9 function __record(s: Session, record: [u8], now: i64): Result[Step, tls.Failure]>>=
function __record(s: Session, record: [u8], now: i64): Result[Step, tls.Failure] {
  match (s.client.read(record)) {
    Ok(step) => {
      return __judged(s, step, now);
    },
    Err(e) => {
      return Err(tls.Failure { error: tls.HandshakeFailed(e), alert: s.client.alert(e) });
    }
  }
  return Err(tls.Failure { error: tls.ClosedEarly, alert: [] });
}
```

### `10 pub function (s: Session) write(data: [u8]): Result[Step, tls.TlsError]`

```fern
<<10 pub function (s: Session) write(data: [u8]): Result[Step, tls.TlsError]>>=
pub function (s: Session) write(data: [u8]): Result[Step, tls.TlsError] {
  let step: handshake.ClientStep = __step(s.client.write(data))?;
  return Ok(Step { session: Session { ...s, client: step.client }, output: step.output, data: [], closed: false, tickets: [], rekeyed: __no_keys() });
}

pub function (s: Session) close(): Result[Step, tls.TlsError] {
  let step: handshake.ClientStep = __step(s.client.close())?;
  return Ok(Step { session: Session { ...s, client: step.client }, output: step.output, data: [], closed: false, tickets: [], rekeyed: __no_keys() });
}

pub function (s: Session) update_keys(request: boolean): Result[Step, tls.TlsError] {
  let step: handshake.ClientStep = __step(s.client.update_keys(request))?;
  return Ok(Step { session: Session { ...s, client: step.client }, output: step.output, data: [], closed: false, tickets: [], rekeyed: handshake.rekeyed(step.client.cipher_suite(), step.events) });
}
```

### `11 pub struct Offloaded`

```fern
<<11 pub struct Offloaded>>=
pub struct Offloaded { session: Session, keys: handshake.KernelKeys }
```

### `12 pub function (s: Session) offload(): Option[Offloaded]`

```fern
<<12 pub function (s: Session) offload(): Option[Offloaded]>>=
pub function (s: Session) offload(): Option[Offloaded] {
  if (s.held.len() != 0) {
    return None;
  }
  match (s.client.offload()) {
    Some(o) => {
      return Some(Offloaded { session: Session { ...s, client: o.client }, keys: o.keys });
    },
    None => {}
  }
  return None;
}

pub function (s: Session) connected(): boolean {
  return s.client.connected();
}

pub function (s: Session) offloaded(): boolean {
  return s.client.offloaded();
}

pub function (s: Session) alpn(): string {
  return s.client.alpn();
}

pub function (s: Session) resumed(): boolean {
  return s.client.resumed();
}

pub function (s: Session) save(): Result[u8[], tls.TlsError] {
  let c: u8[] = __handshake_ok(s.client.save())?;
  let n: i32 = c.len();
  let out: u8[] = [(n >> 24 & 255) as u8, (n >> 16 & 255) as u8, (n >> 8 & 255) as u8, (n & 255) as u8];
  return Ok(__cat(__cat(out, c), s.held));
}

pub function restore(saved: [u8]): Result[Session, tls.TlsError] {
  if (saved.len() < 4) {
    return Err(tls.HandshakeFailed(handshake.Misuse("restore")));
  }
  let n: i32 = saved[0] as i32 << 24 | saved[1] as i32 << 16 | saved[2] as i32 << 8 | saved[3] as i32;
  if (n < 0 || n > saved.len() - 4) {
    return Err(tls.HandshakeFailed(handshake.Misuse("restore")));
  }
  match (handshake.client_restore(saved[4:4 + n])) {
    Ok(c) => {
      return Ok(Session { client: c, name: "", roots: verify.no_roots(), identity: None, held: __copy(saved[4 + n:saved.len()]) });
    },
    Err(e) => {
      return Err(tls.HandshakeFailed(e));
    }
  }
  return Err(tls.ClosedEarly);
}
```

### `13 function __step(r: Result[handshake.ClientStep, handshake.HandshakeError]): Result[handshake.ClientStep, tl...`

```fern
<<13 function __step(r: Result[handshake.ClientStep, handshake.HandshakeError]): Result[handshake.ClientStep, tl...>>=
function __step(r: Result[handshake.ClientStep, handshake.HandshakeError]): Result[handshake.ClientStep, tls.TlsError] {
  match (r) {
    Ok(step) => {
      return Ok(step);
    },
    Err(e) => {
      return Err(tls.HandshakeFailed(e));
    }
  }
  return Err(tls.ClosedEarly);
}

function __verdict(c: handshake.Client): Result[handshake.ClientStep, tls.Failure] {
  match (c.verdict(true)) {
    Ok(step) => {
      return Ok(step);
    },
    Err(e) => {
      return Err(tls.Failure { error: tls.HandshakeFailed(e), alert: c.alert(e) });
    }
  }
  return Err(tls.Failure { error: tls.ClosedEarly, alert: [] });
}

function __handshake_ok(r: Result[u8[], handshake.HandshakeError]): Result[u8[], tls.TlsError] {
  match (r) {
    Ok(b) => {
      return Ok(b);
    },
    Err(e) => {
      return Err(tls.HandshakeFailed(e));
    }
  }
  return Err(tls.ClosedEarly);
}

function __judged(s: Session, first: handshake.ClientStep, now: i64): Result[Step, tls.Failure] {
  let c: handshake.Client = first.client;
  let output: u8[] = first.output;
  let data: u8[] = first.data;
  let events: handshake.Event[] = first.events;
  let closed: boolean = false;
  let tickets: Ticket[] = [];
  let i: i32 = 0;
  while (i < events.len()) {
    match (events[i]) {
      handshake.Ticket(t) => {
        tickets = __kept(tickets, s, c, t, now);
      },
      handshake.PeerClosed => {
        closed = true;
      },
      _ => {
        let next: handshake.ClientStep = __answered(s, c, events[i], now)?;
        c = next.client;
        output = __cat(output, next.output);
        data = __cat(data, next.data);
        events = __more(events, next.events);
      }
    }
    i = i + 1;
  }
  return Ok(Step { session: Session { ...s, client: c }, output: output, data: data, closed: closed, tickets: tickets, rekeyed: handshake.rekeyed(c.cipher_suite(), events) });
}

function __no_keys(): handshake.KernelKeys {
  return handshake.KernelKeys { transmit: [], receive: [] };
}

function __kept(own tickets: Ticket[], s: Session, c: handshake.Client, t: message.NewSessionTicket, now: i64): Ticket[] {
  match (c.resumption_psk(t.nonce)) {
    Ok(psk) => {
      return tickets.append(Ticket { identity: t.ticket, psk: psk, suite: c.cipher_suite().code(), lifetime: t.lifetime, age_add: t.age_add, received_ms: now, server_name: s.name });
    },
    Err(_) => {
      return tickets;
    }
  }
  return tickets;
}

function __answered(s: Session, c: handshake.Client, e: handshake.Event, now: i64): Result[handshake.ClientStep, tls.Failure] {
  match (e) {
    handshake.VerifyServer(v) => {
      match (__trusted(s, v, now)) {
        Ok(_) => {
          return __verdict(c);
        },
        Err(err) => {
          return Err(tls.Failure { error: err, alert: c.alert(handshake.BadCertificate) });
        }
      }
    },
    handshake.SignNeeded(sg) => {
      return __signature(s, c, sg);
    },
    _ => {}
  }
  return Ok(handshake.ClientStep { client: c, output: [], data: [], events: [] });
}

function __signature(s: Session, c: handshake.Client, sg: handshake.Signing): Result[handshake.ClientStep, tls.Failure] {
  let sig: u8[] = [];
  match (s.identity) {
    Some(id) => {
      match (id.key.sign(sg.scheme, sg.signed)) {
        Ok(b) => {
          sig = b;
        },
        Err(_) => {
          return Err(tls.Failure { error: tls.SigningFailed, alert: c.alert(handshake.HandshakeFailure("signing")) });
        }
      }
    },
    None => {
      return Err(tls.Failure { error: tls.SigningFailed, alert: c.alert(handshake.HandshakeFailure("signing")) });
    }
  }
  match (c.signature(sig)) {
    Ok(step) => {
      return Ok(step);
    },
    Err(e) => {
      return Err(tls.Failure { error: tls.HandshakeFailed(e), alert: c.alert(e) });
    }
  }
  return Err(tls.Failure { error: tls.ClosedEarly, alert: [] });
}
```

### `14 function __trusted(s: Session, v: handshake.Verification, now: i64): Result[void, tls.TlsError]`

```fern
<<14 function __trusted(s: Session, v: handshake.Verification, now: i64): Result[void, tls.TlsError]>>=
function __trusted(s: Session, v: handshake.Verification, now: i64): Result[void, tls.TlsError] {
  match (verify.verify_server(v.chain, s.roots, s.name, now / 1000)) {
    Ok(leaf) => {
      if (!verify.verify_signed(leaf, v.scheme, v.signed, v.signature)) {
        return Err(tls.SignatureInvalid);
      }
      return Ok(());
    },
    Err(e) => {
      return Err(tls.Untrusted(e));
    }
  }
  return Err(tls.SignatureInvalid);
}

function __cat(own a: u8[], b: [u8]): u8[] {
  let i: i32 = 0;
  while (i < b.len()) {
    a = a.append(b[i]);
    i = i + 1;
  }
  return a;
}

function __copy(b: [u8]): u8[] {
  return __cat([], b);
}

function __more(own a: handshake.Event[], b: handshake.Event[]): handshake.Event[] {
  for e in b {
    a = a.append(e);
  }
  return a;
}
```

### `15 pub struct Connection`

```fern
<<15 pub struct Connection>>=
pub struct Connection { fd: i32, session: Session, tickets: Ticket[], open: Cell[boolean] }

pub struct Received { conn: Connection, data: u8[], ended: boolean }
```

### `16 pub function connect(host: string, port: i32, config: Config, timeout: Duration): Result[Connection, tls.Tl...`

```fern
<<16 pub function connect(host: string, port: i32, config: Config, timeout: Duration): Result[Connection, tls.Tl...>>=
pub function connect(host: string, port: i32, config: Config, timeout: Duration): Result[Connection, tls.TlsError] {
  let deadline: i64 = monotonic_ns() + timeout.as_nanos();
  let addrs: net.IpAddr[] = [];
  match (dns.resolve(host)) {
    Ok(a) => {
      addrs = a;
    },
    Err(e) => {
      return Err(tls.Unresolved(e));
    }
  }
  match (dns.connect_race(addrs, port, dns.DialOptions { ...dns.dial_options(), timeout: timeout })) {
    Ok(fd) => {
      return __handshake_by(fd, config, deadline);
    },
    Err(e) => {
      return Err(tls.NetFailed(e));
    }
  }
  return Err(tls.ClosedEarly);
}

pub function over(fd: i32, config: Config, timeout: Duration): Result[Connection, tls.TlsError] {
  return __handshake_by(fd, config, monotonic_ns() + timeout.as_nanos());
}
```

### `17 function __handshake_by(fd: i32, config: Config, deadline: i64): Result[Connection, tls.TlsError]`

```fern
<<17 function __handshake_by(fd: i32, config: Config, deadline: i64): Result[Connection, tls.TlsError]>>=
function __handshake_by(fd: i32, config: Config, deadline: i64): Result[Connection, tls.TlsError] {
  match (__handshake_on(fd, config, deadline)) {
    Ok(st) => {
      return Ok(st);
    },
    Err(e) => {
      tcp_close(fd);
      return Err(e);
    }
  }
  return Err(tls.ClosedEarly);
}

function __handshake_on(fd: i32, config: Config, deadline: i64): Result[Connection, tls.TlsError] {
  let step: Step = start(config, random_bytes(entropy(config)), now_unix_ms())?;
  __send_fd(fd, step.output)?;
  let s: Session = step.session;
  let tickets: Ticket[] = [];
  while (!s.connected()) {
    let input: u8[] = [];
    match (__await(fd, deadline)?) {
      Some(b) => {
        input = b;
      },
      None => {
        return Err(tls.HandshakeTimeout);
      }
    }
    if (input.len() == 0) {
      return Err(tls.ClosedEarly);
    }
    let step: Step = __exchange_fd(fd, s, input)?;
    s = step.session;
    tickets = __more_tickets(tickets, step.tickets);
  }
  return Ok(Connection { fd: fd, session: s, tickets: tickets, open: cell_new(true) });
}

function __more_tickets(own a: Ticket[], b: Ticket[]): Ticket[] {
  for t in b {
    a = a.append(t);
  }
  return a;
}
```

### `18 pub function (c: Connection) send(data: [u8]): Result[Connection, tls.TlsError]`

```fern
<<18 pub function (c: Connection) send(data: [u8]): Result[Connection, tls.TlsError]>>=
pub function (c: Connection) send(data: [u8]): Result[Connection, tls.TlsError] {
  if (c.session.offloaded()) {
    __send_fd(c.fd, data)?;
    return Ok(c);
  }
  let step: Step = c.session.write(data)?;
  __send_fd(c.fd, step.output)?;
  return Ok(Connection { ...c, session: step.session });
}

pub function (c: Connection) offload(): Result[Connection, tls.TlsError] {
  if let Some(o) = c.session.offload() {
    match (ktls.attach(c.fd, o.keys)) {
      ktls.Kernel => {
        return Ok(Connection { ...c, session: o.session });
      },
      ktls.Unchanged => {},
      ktls.Broken(e) => {
        tcp_close(c.fd);
        c.open.set(false);
        return Err(tls.NetFailed(net.error_from_host(e)));
      }
    }
  }
  return Ok(c);
}

pub function (c: Connection) offloaded(): boolean {
  return c.session.offloaded();
}

pub function (c: Connection) update_keys(request: boolean): Result[Connection, tls.TlsError] {
  let step: Step = c.session.update_keys(request)?;
  __send_out(c.fd, c.session, step.output)?;
  let r: i32 = ktls.install(c.fd, step.rekeyed);
  if (r < 0) {
    tcp_close(c.fd);
    c.open.set(false);
    return Err(tls.KeysRefused(0 - r));
  }
  return Ok(Connection { ...c, session: step.session });
}

pub function (c: Connection) recv(wait: Duration): Result[Received, tls.TlsError] {
  let deadline: i64 = monotonic_ns() + wait.as_nanos();
  if (c.session.offloaded()) {
    return __recv_kernel(c, deadline);
  }
  let conn: Connection = c;
  let s: Session = c.session;
  while (true) {
    let input: u8[] = [];
    match (__await(c.fd, deadline)?) {
      Some(b) => {
        input = b;
      },
      None => {
        return Ok(__received(conn, s, [], false));
      }
    }
    if (input.len() == 0) {
      return __stream_ended(conn, s);
    }
    let step: Step = __exchange(c, s, input)?;
    s = step.session;
    conn = Connection { ...conn, tickets: __more_tickets(conn.tickets, step.tickets) };
    if (step.data.len() > 0 || step.closed) {
      return Ok(__received(conn, s, step.data, step.closed));
    }
  }
  return Ok(__received(conn, c.session, [], true));
}
```

### `19 function __received(c: Connection, s: Session, data: u8[], ended: boolean): Received`

```fern
<<19 function __received(c: Connection, s: Session, data: u8[], ended: boolean): Received>>=
function __received(c: Connection, s: Session, data: u8[], ended: boolean): Received {
  return Received { conn: Connection { ...c, session: s }, data: data, ended: ended };
}

function __recv_kernel(c: Connection, deadline: i64): Result[Received, tls.TlsError] {
  let conn: Connection = c;
  while (true) {
    let left: i64 = deadline - monotonic_ns();
    if (left <= 0) {
      return Ok(__received(conn, conn.session, [], false));
    }
    let next: Kerneled = __kernel_next(conn, ktls.recv(c.fd, time.duration_nanos(left)))?;
    conn = Connection { ...conn, tickets: __more_tickets(conn.tickets, next.tickets) };
    if (next.done) {
      return Ok(__received(conn, next.session, next.data, next.ended));
    }
    conn = Connection { ...conn, session: next.session };
  }
  return Ok(__received(conn, conn.session, [], true));
}
```

### `20 struct Kerneled`

```fern
<<20 struct Kerneled>>=
struct Kerneled { session: Session, data: u8[], ended: boolean, tickets: Ticket[], done: boolean }
```

### `21 function __kernel_next(c: Connection, read: ktls.Read): Result[Kerneled, tls.TlsError]`

```fern
<<21 function __kernel_next(c: Connection, read: ktls.Read): Result[Kerneled, tls.TlsError]>>=
function __kernel_next(c: Connection, read: ktls.Read): Result[Kerneled, tls.TlsError] {
  let s: Session = c.session;
  match (read) {
    ktls.Opened(t, content) => {
      if (t == 23) {
        return Ok(Kerneled { session: s, data: content, ended: false, tickets: [], done: true });
      }
      let step: Step = __exchange(c, s, ktls.framed(t, content))?;
      return Ok(Kerneled { session: step.session, data: step.data, ended: step.closed, tickets: step.tickets, done: step.data.len() > 0 || step.closed });
    },
    ktls.StreamEnded => {
      return Ok(Kerneled { session: s, data: [], ended: true, tickets: [], done: true });
    },
    ktls.NothingYet => {
      return Ok(Kerneled { session: s, data: [], ended: false, tickets: [], done: true });
    },
    ktls.WaitCancelled => {
      return Err(tls.NetFailed(net.Interrupted));
    },
    ktls.ReadFailed(e) => {
      return Err(__kernel_failed(c, e));
    }
  }
  return Err(tls.ClosedEarly);
}

function __kernel_failed(c: Connection, errno: i32): tls.TlsError {
  let err: tls.TlsError = tls.NetFailed(net.error_from_host(errno));
  if (errno == 74) {
    ktls.alert(c.fd, 20);
    err = tls.HandshakeFailed(handshake.Record(record.BadRecordMac));
  } else if (errno == 90) {
    ktls.alert(c.fd, 22);
    err = tls.HandshakeFailed(handshake.Record(record.RecordOverflow(0)));
  }
  tcp_close(c.fd);
  c.open.set(false);
  return err;
}

function __stream_ended(c: Connection, s: Session): Result[Received, tls.TlsError] {
  if (s.held.len() > 0) {
    return Err(tls.RecordCut);
  }
  return Ok(__received(c, s, [], true));
}

function __exchange(c: Connection, s: Session, input: [u8]): Result[Step, tls.TlsError] {
  match (__exchange_fd(c.fd, s, input)) {
    Ok(step) => {
      return Ok(step);
    },
    Err(e) => {
      tcp_close(c.fd);
      c.open.set(false);
      return Err(e);
    }
  }
  return Err(tls.ClosedEarly);
}

function __exchange_fd(fd: i32, s: Session, input: [u8]): Result[Step, tls.TlsError] {
  match (s.read(input, now_unix_ms())) {
    Ok(step) => {
      __send_out(fd, s, step.output)?;
      let r: i32 = ktls.install(fd, step.rekeyed);
      if (r < 0) {
        return Err(tls.KeysRefused(0 - r));
      }
      return Ok(step);
    },
    Err(f) => {
      __send_out(fd, s, f.alert);
      return Err(f.error);
    }
  }
  return Err(tls.ClosedEarly);
}

function __send_out(fd: i32, s: Session, out: [u8]): Result[void, tls.TlsError] {
  if (!s.offloaded()) {
    return __send_fd(fd, out);
  }
  let r: i32 = ktls.send_records(fd, out);
  if (r < 0) {
    return Err(tls.NetFailed(net.error_from_host(0 - r)));
  }
  return Ok(());
}
```

### `22 pub function (c: Connection) close(): void`

```fern
<<22 pub function (c: Connection) close(): void>>=
pub function (c: Connection) close(): void {
  if (!c.open.get()) {
    return;
  }
  c.open.set(false);
  if let Ok(step) = c.session.close() {
    __send_out(c.fd, c.session, step.output);
  }
  tcp_close(c.fd);
}
```

### `23 function __send_fd(fd: i32, data: [u8]): Result[void, tls.TlsError]`

```fern
<<23 function __send_fd(fd: i32, data: [u8]): Result[void, tls.TlsError]>>=
function __send_fd(fd: i32, data: [u8]): Result[void, tls.TlsError] {
  let at: i32 = 0;
  while (at < data.len()) {
    let n: i32 = tcp_send_bytes(fd, __cat([], data[at:data.len()]));
    if (n < 0) {
      return Err(tls.NetFailed(net.error_from_host(0 - n)));
    }
    if (n == 0) {
      return Err(tls.NetFailed(net.ConnectionReset));
    }
    at = at + n;
  }
  return Ok(());
}

function __await(fd: i32, deadline: i64): Result[Option[u8[]], tls.TlsError] {
  let left: i64 = deadline - monotonic_ns();
  if (left <= 0) {
    return Ok(None);
  }
  return __recv_fd(fd, time.duration_nanos(left));
}

function __recv_fd(fd: i32, wait: Duration): Result[Option[u8[]], tls.TlsError] {
  match (net.read_deadline(fd, 65536, wait)) {
    Ok(net.Chunk(b)) => {
      return Ok(Some(b));
    },
    Ok(net.Elapsed()) => {
      return Ok(None);
    },
    Ok(net.Abandoned()) => {
      return Err(tls.NetFailed(net.Interrupted));
    },
    Err(e) => {
      return Err(tls.NetFailed(e));
    }
  }
  return Ok(None);
}
```
