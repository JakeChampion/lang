# TLS 1.3 handshake state

This module implements TLS 1.3 handshake state without I/O.

## Model

Handshake state coordinates message transcripts, key shares, authentication, and traffic secrets. Crypto flights identify the protocol level for output bytes.

## Contracts

The state machine performs no I/O. Its caller delivers flights and consumes events. Error values preserve the TLS alert associated with a failed step.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum HandshakeError>>

<<4 pub function (e: HandshakeError) to_string(): string>>

<<5 impl error.Error for HandshakeError>>

<<6 function __record_alert(e: record.RecordError): i32>>

<<7 pub function (e: HandshakeError) alert(): i32>>

<<8 pub enum Event>>

<<9 pub function certificate_verify_content(server: boolean, transcript_hash: [u8]): u8[]>>

<<10 function __cat(own a: u8[], b: [u8]): u8[]>>

<<11 struct Conn>>

<<12 function __conn(): Conn>>

<<13 struct Flushed>>

<<14 function __plain_out(c: Conn, version: i32, bytes: u8[]): Wrote>>

<<15 struct Out>>

<<16 function __out(): Out>>

<<17 struct Wrote>>

<<18 function __write(c: Conn, t: record.ContentType, content: [u8]): Result[Wrote, HandshakeError]>>

<<19 struct Absorbed>>

<<20 function __absorb(c: Conn, r: record.Record, ccs: boolean, app: boolean, out: Out): Result[Absorbed, Handsh...>>

<<21 struct Next>>

<<22 function __next(c: Conn): Result[Next, HandshakeError]>>

<<23 pub struct KernelKeys>>

<<24 function __offload(c: Conn): Option[Offloaded]>>

<<25 struct Offloaded>>

<<26 pub function rekeyed(suite: keyschedule.CipherSuite, events: Event[]): KernelKeys>>

<<27 function __alert_record(c: Conn, e: HandshakeError): u8[]>>

<<28 pub struct ClientConfig>>

<<29 pub function client_config(server_name: string): ClientConfig>>

<<30 const C_WAIT_SERVER_HELLO: i32>>

<<31 function __client_step(c: Client, out: Out): ClientStep>>

<<32 pub function client_entropy(config: ClientConfig): i32>>

<<33 function __shares(config: ClientConfig): i32>>

<<34 pub function client_start(config: ClientConfig, entropy: [u8]): Result[ClientStep, HandshakeError]>>

<<35 function __offering(hello: message.ClientHello, resume: Option[Resumption]): Result[message.ClientHello, Ha...>>

<<36 struct ClientRun>>

<<37 function __server_hello(c: Client, raw: message.Raw, out: Out): Result[ClientRun, HandshakeError]>>

<<38 function __retry_group(c: Client, h: message.ServerHello): Result[i32, HandshakeError]>>

<<39 function __encrypted_extensions(c: Client, exts: message.Extension[]): Result[Client, HandshakeError]>>

<<40 function __client_message(c: Client, raw: message.Raw, out: Out): Result[ClientRun, HandshakeError]>>

<<41 function __client_run(c: Client, out: Out): Result[ClientStep, HandshakeError]>>

<<42 pub function (c: Client) read(input: [u8]): Result[ClientStep, HandshakeError]>>

<<43 pub struct ClientOffload>>

<<44 pub function (c: Client) offload(): Option[ClientOffload]>>

<<45 function __u32_to(own out: u8[], v: i32): u8[]>>

<<46 pub struct ServerConfig>>

<<47 pub function server_config(chain: u8[][], schemes: i32[]): ServerConfig>>

<<48 const VERIFY_SCHEMES: i32[]>>

<<49 pub function server_entropy(config: ServerConfig): i32>>

<<50 pub struct Server>>

<<51 function __server_step(s: Server, out: Out): ServerStep>>

<<52 pub function server_new(config: ServerConfig, entropy: [u8]): Result[Server, HandshakeError]>>

<<53 struct ServerRun>>

<<54 function __ext(exts: message.Extension[], kind: i32, what: string): Result[u8[], HandshakeError]>>

<<55 struct Choice>>

<<56 function __client_shares(h: message.ClientHello): Result[message.KeyShareEntry[], HandshakeError]>>

<<57 function __pick_scheme(s: Server, h: message.ClientHello): Result[i32, HandshakeError]>>

<<58 struct Negotiated>>

<<59 function __negotiate(s: Server, h: message.ClientHello): Result[Negotiated, HandshakeError]>>

<<60 function __retry_request(s: Server, raw: message.Raw, h: message.ClientHello, n: Negotiated, out: Out): Res...>>

<<61 function __server_run(s: Server, out: Out): Result[ServerStep, HandshakeError]>>

<<62 pub function (s: Server) read(input: [u8]): Result[ServerStep, HandshakeError]>>

<<63 pub struct ServerOffload>>

<<64 pub function (s: Server) offload(): Option[ServerOffload]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module implements TLS 1.3 handshake state without I/O.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/int";
import "std/crypto";
import "std/tls/keyschedule";
import "std/tls/keyshare";
import "std/tls/message";
import "std/tls/record";
import "std/utf8";
import "std/error";
import "core/cmp";
```

### `3 pub enum HandshakeError`

```fern
<<3 pub enum HandshakeError>>=
pub enum HandshakeError {
  Record(record.RecordError),
  Message(message.MsgError),
  KeyShare(keyshare.KeyShareError),
  OutOfOrder(i32),
  UnexpectedRecord(i32),
  Level(i32),
  Illegal(string),
  MissingExtension(string),
  UnsupportedExtension(i32),
  NotTls13,
  HandshakeFailure(string),
  NoApplicationProtocol,
  BadCertificate,
  CertificateRequired,
  DecryptError,
  PeerAlert(i32),
  Entropy(i32, i32),
  BadConfig(string),
  Misuse(string),
}
```

### `4 pub function (e: HandshakeError) to_string(): string`

```fern
<<4 pub function (e: HandshakeError) to_string(): string>>=
pub function (e: HandshakeError) to_string(): string {
  return e.message();
}
```

### `5 impl error.Error for HandshakeError`

```fern
<<5 impl error.Error for HandshakeError>>=
impl error.Error for HandshakeError {}

impl cmp.Display for HandshakeError {}
```

### `6 function __record_alert(e: record.RecordError): i32`

```fern
<<6 function __record_alert(e: record.RecordError): i32>>=
function __record_alert(e: record.RecordError): i32 {
  match (e) {
    record.RecordOverflow(n) => {
      return 22;
    },
    record.BadRecordMac => {
      return 20;
    },
    record.SequenceExhausted => {
      return 80;
    },
    record.BadKeys(k, iv) => {
      return 80;
    },
    _ => {
      return 10;
    }
  }
  return 10;
}

function __message_alert(e: message.MsgError): i32 {
  match (e) {
    message.DecodeError(w) => {
      return 50;
    },
    message.IllegalParameter(w) => {
      return 47;
    },
    message.ProtocolVersion(v) => {
      return 70;
    },
    _ => {
      return 10;
    }
  }
  return 10;
}
```

### `7 pub function (e: HandshakeError) alert(): i32`

```fern
<<7 pub function (e: HandshakeError) alert(): i32>>=
// fern-lint: allow cyclomatic-complexity
pub function (e: HandshakeError) alert(): i32 {
  match (e) {
    Record(r) => {
      return __record_alert(r);
    },
    Message(m) => {
      return __message_alert(m);
    },
    KeyShare(k) => {
      return 47;
    },
    Level(l) => {
      return 10;
    },
    OutOfOrder(t) => {
      return 10;
    },
    UnexpectedRecord(t) => {
      return 10;
    },
    Illegal(w) => {
      return 47;
    },
    MissingExtension(w) => {
      return 109;
    },
    UnsupportedExtension(k) => {
      return 110;
    },
    NotTls13 => {
      return 70;
    },
    HandshakeFailure(w) => {
      return 40;
    },
    NoApplicationProtocol => {
      return 120;
    },
    BadCertificate => {
      return 42;
    },
    CertificateRequired => {
      return 116;
    },
    DecryptError => {
      return 51;
    },
    PeerAlert(d) => {
      return 0;
    },
    Entropy(n, want) => {
      return 80;
    },
    BadConfig(w) => {
      return 80;
    },
    Misuse(w) => {
      return 80;
    }
  }
  return 80;
}

// fern-lint: allow cyclomatic-complexity
pub function (e: HandshakeError) message(): string {
  match (e) {
    Record(r) => {
      return r.message();
    },
    Message(m) => {
      return m.message();
    },
    KeyShare(k) => {
      return k.message();
    },
    Level(l) => {
      return "tls handshake: handshake bytes at encryption level " + int.int_to_string(l) + " out of turn";
    },
    OutOfOrder(t) => {
      return "tls handshake: unexpected handshake message " + int.int_to_string(t);
    },
    UnexpectedRecord(t) => {
      return "tls handshake: unexpected record of type " + int.int_to_string(t);
    },
    Illegal(w) => {
      return "tls handshake: illegal " + w;
    },
    MissingExtension(w) => {
      return "tls handshake: missing " + w;
    },
    UnsupportedExtension(k) => {
      return "tls handshake: extension " + int.int_to_string(k) + " was not offered";
    },
    NotTls13 => {
      return "tls handshake: the peer does not speak TLS 1.3";
    },
    HandshakeFailure(w) => {
      return "tls handshake: " + w;
    },
    NoApplicationProtocol => {
      return "tls handshake: no application protocol in common";
    },
    BadCertificate => {
      return "tls handshake: the peer's certificate was refused";
    },
    CertificateRequired => {
      return "tls handshake: a client certificate is required";
    },
    DecryptError => {
      return "tls handshake: the peer's Finished does not verify";
    },
    PeerAlert(d) => {
      return "tls handshake: the peer sent alert " + int.int_to_string(d);
    },
    Entropy(n, want) => {
      return "tls handshake: " + int.int_to_string(n) + " bytes of entropy, want " + int.int_to_string(want);
    },
    BadConfig(w) => {
      return "tls handshake: " + w;
    },
    Misuse(w) => {
      return "tls handshake: " + w + " called out of turn";
    }
  }
  return "";
}
```

### `8 pub enum Event`

```fern
<<8 pub enum Event>>=
pub enum Event {
  VerifyServer(Verification),
  SignNeeded(Signing),
  VerifyClient(Verification),
  Connected,
  PeerClosed,
  Ticket(message.NewSessionTicket),
  ResumeOffered(Offer),
  Crypto(CryptoFlight),
  Secret(TrafficSecret),
}

pub struct CryptoFlight { level: i32, bytes: u8[] }

pub struct TrafficSecret { level: i32, write: boolean, secret: u8[] }

pub struct Offer { identities: message.PskIdentity[], suite: keyschedule.CipherSuite, server_name: string }

pub struct Resumed { index: i32, psk: u8[] }

pub struct Resumption { identity: u8[], psk: u8[], suite: keyschedule.CipherSuite, obfuscated_age: u32 }

pub struct Verification { chain: u8[][], scheme: i32, signed: u8[], signature: u8[] }

pub struct Signing { scheme: i32, signed: u8[] }
```

### `9 pub function certificate_verify_content(server: boolean, transcript_hash: [u8]): u8[]`

```fern
<<9 pub function certificate_verify_content(server: boolean, transcript_hash: [u8]): u8[]>>=
pub function certificate_verify_content(server: boolean, transcript_hash: [u8]): u8[] {
  let context: string = "TLS 1.3, client CertificateVerify";
  if (server) {
    context = "TLS 1.3, server CertificateVerify";
  }
  let out: u8[] = [];
  let i: i32 = 0;
  while (i < 64) {
    out = out.append(0x20 as u8);
    i = i + 1;
  }
  out = __cat(out, context.bytes());
  out = out.append(0 as u8);
  return __cat(out, transcript_hash);
}
```

### `10 function __cat(own a: u8[], b: [u8]): u8[]`

```fern
<<10 function __cat(own a: u8[], b: [u8]): u8[]>>=
function __cat(own a: u8[], b: [u8]): u8[] {
  if (b.len() == 0) {
    return a;
  }
  if (a.len() == 0) {
    return __bytes_owned(b);
  }
  return a.concat(__bytes_owned(b));
}

function __copy(b: [u8]): u8[] {
  return __bytes_owned(b);
}

function __join(a: [u8], b: [u8]): u8[] {
  return __cat(__copy(a), b);
}

function __same(a: [u8], b: [u8]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < a.len()) {
    if (a[i] != b[i]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __same_hash(a: keyschedule.Hash, b: keyschedule.Hash): boolean {
  return a.size() == b.size();
}

function __zeros(n: i32): u8[] {
  let out: u8[] = [];
  while (out.len() < n) {
    out = out.append(0 as u8);
  }
  return out;
}

function __binder(h: keyschedule.Hash, psk: [u8], before: [u8], truncated: [u8]): u8[] {
  let th: u8[] = h.digest(__cat(__copy(before), truncated));
  return keyschedule.finished_verify_data(h, keyschedule.early_secret(h, psk).binder_key(), th);
}

function __without(exts: message.Extension[], kind: i32): message.Extension[] {
  let out: message.Extension[] = [];
  for e in exts {
    if (e.kind != kind) {
      out = out.append(e);
    }
  }
  return out;
}
```

### `11 struct Conn`

```fern
<<11 struct Conn>>=
struct Conn {
  held: record.Record[],
  inbox: u8[],
  pending: u8[],
  transcript: u8[],
  suite: keyschedule.CipherSuite,
  read: record.Protection,
  write: record.Protection,
  reading: i32,
  writing: i32,
  read_secret: u8[],
  write_secret: u8[],
  quic: boolean,
  kernel: boolean,
  crypto: CryptoFlight[],
  secrets: TrafficSecret[],
}
```

### `12 function __conn(): Conn`

```fern
<<12 function __conn(): Conn>>=
function __conn(): Conn {
  let none: record.Protection = record.Protection { suite: keyschedule.Aes128GcmSha256, key: [], iv: [], seq: 0 as u64, gcm: None, chacha: None };
  return Conn { held: [], inbox: [], pending: [], transcript: [], suite: keyschedule.Aes128GcmSha256, read: none, write: none, reading: 0, writing: 0, read_secret: [], write_secret: [], quic: false, kernel: false, crypto: [], secrets: [] };
}

function __conn_for(quic: boolean): Conn {
  return Conn { ...__conn(), quic: quic };
}

function __hash(c: Conn): u8[] {
  return c.suite.hash().digest(c.transcript);
}

function __with_read(c: Conn, secret: u8[], level: i32): Conn {
  let next: Conn = Conn { ...c, read: record.protection(c.suite, secret), reading: level, read_secret: secret };
  if (c.quic || c.kernel) {
    return Conn { ...next, secrets: next.secrets.append(TrafficSecret { level: level, write: false, secret: secret }) };
  }
  return next;
}

function __with_write(c: Conn, secret: u8[], level: i32): Conn {
  let next: Conn = Conn { ...c, write: record.protection(c.suite, secret), writing: level, write_secret: secret };
  if (c.quic || c.kernel) {
    return Conn { ...next, secrets: next.secrets.append(TrafficSecret { level: level, write: true, secret: secret }) };
  }
  return next;
}

function __flushed(c: Conn, out: Out): Flushed {
  if (!c.quic && !c.kernel) {
    return Flushed { conn: c, out: out };
  }
  let events: Event[] = out.events;
  for t in c.secrets {
    events = events.append(Secret(t));
  }
  for k in c.crypto {
    events = events.append(Crypto(k));
  }
  return Flushed { conn: Conn { ...c, crypto: [], secrets: [] }, out: Out { ...out, events: events } };
}
```

### `13 struct Flushed`

```fern
<<13 struct Flushed>>=
struct Flushed { conn: Conn, out: Out }
```

### `14 function __plain_out(c: Conn, version: i32, bytes: u8[]): Wrote`

```fern
<<14 function __plain_out(c: Conn, version: i32, bytes: u8[]): Wrote>>=
function __plain_out(c: Conn, version: i32, bytes: u8[]): Wrote {
  if (c.quic) {
    return Wrote { conn: Conn { ...c, crypto: c.crypto.append(CryptoFlight { level: c.writing, bytes: bytes }) }, bytes: [] };
  }
  return Wrote { conn: c, bytes: record.frame(record.Handshake, version, bytes) };
}
```

### `15 struct Out`

```fern
<<15 struct Out>>=
struct Out { output: u8[], data: u8[], events: Event[], closed: boolean }
```

### `16 function __out(): Out`

```fern
<<16 function __out(): Out>>=
function __out(): Out {
  return Out { output: [], data: [], events: [], closed: false };
}
```

### `17 struct Wrote`

```fern
<<17 struct Wrote>>=
struct Wrote { conn: Conn, bytes: u8[] }
```

### `18 function __write(c: Conn, t: record.ContentType, content: [u8]): Result[Wrote, HandshakeError]`

```fern
<<18 function __write(c: Conn, t: record.ContentType, content: [u8]): Result[Wrote, HandshakeError]>>=
function __write(c: Conn, t: record.ContentType, content: [u8]): Result[Wrote, HandshakeError] {
  if (c.quic) {
    return __write_quic(c, t, content);
  }
  if (c.writing == 0 || c.kernel) {
    return Ok(Wrote { conn: c, bytes: record.frame(t, 0x0303, content) });
  }
  let out: u8[] = [];
  let p: record.Protection = c.write;
  let from: i32 = 0;
  while (from < content.len() || from == 0) {
    let to: i32 = content.len();
    if (to - from > 16384) {
      to = from + 16384;
    }
    match (p.seal(t, content[from:to], 0)) {
      Ok(s) => {
        out = __cat(out, s.record);
        p = s.next;
      },
      Err(e) => {
        return Err(Record(e));
      }
    }
    from = to;
    if (to == 0) {
      break;
    }
  }
  return Ok(Wrote { conn: Conn { ...c, write: p }, bytes: out });
}

function __write_quic(c: Conn, t: record.ContentType, content: [u8]): Result[Wrote, HandshakeError] {
  if (t.code() == 22) {
    return Ok(Wrote { conn: Conn { ...c, crypto: c.crypto.append(CryptoFlight { level: c.writing, bytes: __copy(content) }) }, bytes: [] });
  }
  if (t.code() == 21) {
    return Ok(Wrote { conn: c, bytes: [] });
  }
  return Err(Misuse("application data under QUIC"));
}
```

### `19 struct Absorbed`

```fern
<<19 struct Absorbed>>=
struct Absorbed { conn: Conn, out: Out }
```

### `20 function __absorb(c: Conn, r: record.Record, ccs: boolean, app: boolean, out: Out): Result[Absorbed, Handsh...`

```fern
<<20 function __absorb(c: Conn, r: record.Record, ccs: boolean, app: boolean, out: Out): Result[Absorbed, Handsh...>>=
function __absorb(c: Conn, r: record.Record, ccs: boolean, app: boolean, out: Out): Result[Absorbed, HandshakeError] {
  if (r.content_type.code() == 20) {
    if (ccs && r.fragment.len() == 1 && r.fragment[0] == 1) {
      return Ok(Absorbed { conn: c, out: out });
    }
    return Err(UnexpectedRecord(20));
  }
  if (c.reading == 0 || c.kernel) {
    return __content(c, r.content_type.code(), r.fragment, app, out);
  }
  if (r.content_type.code() != 23) {
    return Err(UnexpectedRecord(r.content_type.code()));
  }
  match (c.read.open(r)) {
    Ok(o) => {
      return __opened(Conn { ...c, read: o.next }, o, app, out);
    },
    Err(e) => {
      return Err(Record(e));
    }
  }
  return Err(UnexpectedRecord(23));
}

function __opened(c: Conn, o: record.Opened, app: boolean, out: Out): Result[Absorbed, HandshakeError] {
  if (o.content_type.code() == 23 && app && c.reading == 2) {
    if (out.data.len() == 0) {
      return Ok(Absorbed { conn: c, out: Out { ...out, data: o.content } });
    }
    return Ok(Absorbed { conn: c, out: Out { ...out, data: __join(out.data, o.content) } });
  }
  return __content(c, o.content_type.code(), o.content, app, out);
}

function __content(c: Conn, code: i32, content: u8[], app: boolean, out: Out): Result[Absorbed, HandshakeError] {
  if (code == 22) {
    return Ok(Absorbed { conn: Conn { ...c, pending: __join(c.pending, content) }, out: out });
  }
  if (code == 21) {
    return __peer_alert(c, content, out);
  }
  if (code == 23 && app && c.reading == 2) {
    return Ok(Absorbed { conn: c, out: Out { ...out, data: __join(out.data, content) } });
  }
  return Err(UnexpectedRecord(code));
}

function __peer_alert(c: Conn, content: u8[], out: Out): Result[Absorbed, HandshakeError] {
  if (content.len() != 2) {
    return Err(Message(message.DecodeError("alert")));
  }
  if (content[1] == 0) {
    return Ok(Absorbed { conn: c, out: Out { ...out, events: out.events.append(PeerClosed), closed: true } });
  }
  if (content[1] == 90) {
    return Ok(Absorbed { conn: c, out: out });
  }
  return Err(PeerAlert(content[1] as i32));
}
```

### `21 struct Next`

```fern
<<21 struct Next>>=
struct Next { conn: Conn, raw: message.Raw, has: boolean }
```

### `22 function __next(c: Conn): Result[Next, HandshakeError]`

```fern
<<22 function __next(c: Conn): Result[Next, HandshakeError]>>=
function __next(c: Conn): Result[Next, HandshakeError] {
  match (message.split(c.pending)) {
    Ok(s) => {
      if (s.messages.len() == 0) {
        return Ok(Next { conn: c, raw: message.Raw { kind: 0, bytes: [] }, has: false });
      }
      let raw: message.Raw = s.messages[0];
      return Ok(Next { conn: Conn { ...c, pending: __copy(c.pending[raw.bytes.len():c.pending.len()]) }, raw: raw, has: true });
    },
    Err(e) => {
      return Err(Message(e));
    }
  }
  return Err(Misuse("split"));
}

function __decode(raw: message.Raw): Result[message.Message, HandshakeError] {
  match (message.decode(raw)) {
    Ok(m) => {
      return Ok(m);
    },
    Err(e) => {
      return Err(Message(e));
    }
  }
  return Err(OutOfOrder(raw.kind));
}

function __at_key_change(c: Conn): Result[boolean, HandshakeError] {
  if (c.pending.len() != 0) {
    return Err(OutOfOrder(c.pending[0] as i32));
  }
  return Ok(true);
}

function __key_update(c: Conn, request: boolean, out: Out): Result[Absorbed, HandshakeError] {
  if (c.quic) {
    return Err(Illegal("KeyUpdate under QUIC"));
  }
  __at_key_change(c)?;
  let h: keyschedule.Hash = c.suite.hash();
  let conn: Conn = __with_read(c, keyschedule.next_traffic_secret(h, c.read_secret), 2);
  if (!request) {
    return Ok(Absorbed { conn: conn, out: out });
  }
  let w: Wrote = __update(conn, false)?;
  return Ok(Absorbed { conn: w.conn, out: Out { ...out, output: __join(out.output, w.bytes) } });
}

function __update(c: Conn, request: boolean): Result[Wrote, HandshakeError] {
  if (c.quic) {
    return Err(Illegal("KeyUpdate under QUIC"));
  }
  let w: Wrote = __write(c, record.Handshake, message.encode(message.KeyUpdate(request)))?;
  let h: keyschedule.Hash = c.suite.hash();
  return Ok(Wrote { conn: __with_write(w.conn, keyschedule.next_traffic_secret(h, w.conn.write_secret), 2), bytes: w.bytes });
}
```

### `23 pub struct KernelKeys`

```fern
<<23 pub struct KernelKeys>>=
pub struct KernelKeys { transmit: u8[], receive: u8[] }
```

### `24 function __offload(c: Conn): Option[Offloaded]`

```fern
<<24 function __offload(c: Conn): Option[Offloaded]>>=
function __offload(c: Conn): Option[Offloaded] {
  if (c.quic || c.kernel || c.reading != 2 || c.writing != 2 || c.held.len() != 0 || c.inbox.len() != 0 || c.pending.len() != 0) {
    return None;
  }
  return Some(Offloaded { conn: Conn { ...c, kernel: true }, keys: KernelKeys { transmit: c.write.kernel_info(), receive: c.read.kernel_info() } });
}
```

### `25 struct Offloaded`

```fern
<<25 struct Offloaded>>=
struct Offloaded { conn: Conn, keys: KernelKeys }
```

### `26 pub function rekeyed(suite: keyschedule.CipherSuite, events: Event[]): KernelKeys`

```fern
<<26 pub function rekeyed(suite: keyschedule.CipherSuite, events: Event[]): KernelKeys>>=
pub function rekeyed(suite: keyschedule.CipherSuite, events: Event[]): KernelKeys {
  let keys: KernelKeys = KernelKeys { transmit: [], receive: [] };
  for e in events {
    if let Secret(t) = e {
      let info: u8[] = record.protection(suite, t.secret).kernel_info();
      if (t.write) {
        keys = KernelKeys { ...keys, transmit: info };
      } else {
        keys = KernelKeys { ...keys, receive: info };
      }
    }
  }
  return keys;
}

pub function (earlier: KernelKeys) then(later: KernelKeys): KernelKeys {
  let keys: KernelKeys = earlier;
  if (later.transmit.len() > 0) {
    keys = KernelKeys { ...keys, transmit: later.transmit };
  }
  if (later.receive.len() > 0) {
    keys = KernelKeys { ...keys, receive: later.receive };
  }
  return keys;
}
```

### `27 function __alert_record(c: Conn, e: HandshakeError): u8[]`

```fern
<<27 function __alert_record(c: Conn, e: HandshakeError): u8[]>>=
function __alert_record(c: Conn, e: HandshakeError): u8[] {
  if (e.alert() == 0) {
    return [];
  }
  match (__write(c, record.Alert, [2 as u8, e.alert() as u8])) {
    Ok(w) => {
      return w.bytes;
    },
    Err(err) => {
      return [];
    }
  }
  return [];
}

function __has(v: i32[], x: i32): boolean {
  let i: i32 = 0;
  while (i < v.len()) {
    if (v[i] == x) {
      return true;
    }
    i = i + 1;
  }
  return false;
}

function __has_string(v: string[], x: string): boolean {
  let i: i32 = 0;
  while (i < v.len()) {
    if (v[i] == x) {
      return true;
    }
    i = i + 1;
  }
  return false;
}
```

### `28 pub struct ClientConfig`

```fern
<<28 pub struct ClientConfig>>=
pub struct ClientConfig { server_name: string, alpn: string[], suites: keyschedule.CipherSuite[], groups: keyshare.Group[], shares: i32, schemes: i32[], resume: Option[Resumption], chain: u8[][], sign_schemes: i32[], quic: boolean, extensions: message.Extension[] }
```

### `29 pub function client_config(server_name: string): ClientConfig`

```fern
<<29 pub function client_config(server_name: string): ClientConfig>>=
pub function client_config(server_name: string): ClientConfig {
  return ClientConfig { server_name: server_name, alpn: [], suites: [keyschedule.Aes128GcmSha256, keyschedule.Aes256GcmSha384, keyschedule.ChaCha20Poly1305Sha256], groups: [keyshare.X25519MlKem768, keyshare.X25519, keyshare.Secp256r1, keyshare.Secp256r1MlKem768], shares: 1, schemes: [0x0403, 0x0503, 0x0807, 0x0804, 0x0805, 0x0806, 0x0401, 0x0501, 0x0601], resume: None, chain: [], sign_schemes: [], quic: false, extensions: [] };
}
```

### `30 const C_WAIT_SERVER_HELLO: i32`

```fern
<<30 const C_WAIT_SERVER_HELLO: i32>>=
const C_WAIT_SERVER_HELLO: i32 = 0;

const C_WAIT_ENCRYPTED_EXTENSIONS: i32 = 1;

const C_WAIT_CERTIFICATE_OR_REQUEST: i32 = 2;

const C_WAIT_CERTIFICATE: i32 = 3;

const C_WAIT_CERTIFICATE_VERIFY: i32 = 4;

const C_WAIT_VERDICT: i32 = 5;

const C_WAIT_FINISHED: i32 = 6;

const C_CONNECTED: i32 = 7;

const C_CLOSED: i32 = 8;

const C_WAIT_SIGNATURE: i32 = 9;

pub struct Client {
  conn: Conn,
  state: i32,
  hello: message.ClientHello,
  alpn_offered: string[],
  share: keyshare.ClientShare,
  more_shares: keyshare.ClientShare[],
  spare: u8[],
  retry_suite: i32,
  hs: keyschedule.HandshakeSecret,
  client_hs: u8[],
  server_hs: u8[],
  chain: u8[][],
  alpn: string,
  requested: boolean,
  request_context: u8[],
  resume: Option[Resumption],
  resumed: boolean,
  res_master: u8[],
  own_chain: u8[][],
  sign_schemes: i32[],
  request_schemes: i32[],
  own_scheme: i32,
  flight: u8[],
  client_ap: u8[],
  server_ap: u8[],
  peer_extensions: message.Extension[],
}

pub struct ClientStep { client: Client, output: u8[], data: u8[], events: Event[] }
```

### `31 function __client_step(c: Client, out: Out): ClientStep`

```fern
<<31 function __client_step(c: Client, out: Out): ClientStep>>=
function __client_step(c: Client, out: Out): ClientStep {
  let f: Flushed = __flushed(c.conn, out);
  return ClientStep { client: Client { ...c, conn: f.conn }, output: f.out.output, data: f.out.data, events: f.out.events };
}

function __offers(config: ClientConfig): i32[] {
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < config.suites.len()) {
    out = out.append(config.suites[i].code());
    i = i + 1;
  }
  return out;
}

function __group_codes(config: ClientConfig): i32[] {
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < config.groups.len()) {
    out = out.append(config.groups[i].code());
    i = i + 1;
  }
  return out;
}
```

### `32 pub function client_entropy(config: ClientConfig): i32`

```fern
<<32 pub function client_entropy(config: ClientConfig): i32>>=
pub function client_entropy(config: ClientConfig): i32 {
  let n: i32 = __shares(config);
  let sent: i32 = 0;
  let spare: i32 = 0;
  let i: i32 = 0;
  while (i < config.groups.len()) {
    let e: i32 = config.groups[i].client_entropy();
    if (i < n) {
      sent = sent + e;
    } else if (e > spare) {
      spare = e;
    }
    i = i + 1;
  }
  return 32 + sent + spare;
}
```

### `33 function __shares(config: ClientConfig): i32`

```fern
<<33 function __shares(config: ClientConfig): i32>>=
function __shares(config: ClientConfig): i32 {
  if (config.shares < 1) {
    return 1;
  }
  if (config.shares > config.groups.len()) {
    return config.groups.len();
  }
  return config.shares;
}

function __hello_extensions(config: ClientConfig, shares: keyshare.ClientShare[]): message.Extension[] {
  let entries: message.KeyShareEntry[] = [];
  for sh in shares {
    entries = entries.append(message.KeyShareEntry { group: sh.group.code(), key: sh.public });
  }
  let exts: message.Extension[] = [];
  if (config.server_name.len() > 0) {
    exts = exts.append(message.ext_server_name(config.server_name));
  }
  exts = exts.append(message.ext_supported_versions([message.TLS13])).append(message.ext_supported_groups(__group_codes(config))).append(message.ext_key_shares(entries)).append(message.ext_signature_algorithms(config.schemes));
  if (config.alpn.len() > 0) {
    exts = exts.append(message.ext_alpn(config.alpn));
  }
  exts = exts.append(message.ext_psk_modes([1]));
  for extra in config.extensions {
    exts = exts.append(extra);
  }
  return exts;
}

function __made_shares(config: ClientConfig, entropy: [u8]): Result[(keyshare.ClientShare[], u8[]), HandshakeError] {
  let shares: keyshare.ClientShare[] = [];
  let at: i32 = 32;
  let i: i32 = 0;
  while (i < __shares(config)) {
    let g: keyshare.Group = config.groups[i];
    let end: i32 = at + g.client_entropy();
    match (keyshare.client_share(g, entropy[at:end])) {
      Ok(share) => {
        shares = shares.append(share);
      },
      Err(e) => {
        return Err(KeyShare(e));
      }
    }
    at = end;
    i = i + 1;
  }
  return Ok((shares, __copy(entropy[at:entropy.len()])));
}
```

### `34 pub function client_start(config: ClientConfig, entropy: [u8]): Result[ClientStep, HandshakeError]`

```fern
<<34 pub function client_start(config: ClientConfig, entropy: [u8]): Result[ClientStep, HandshakeError]>>=
pub function client_start(config: ClientConfig, entropy: [u8]): Result[ClientStep, HandshakeError] {
  if (config.groups.len() == 0 || config.suites.len() == 0) {
    return Err(BadConfig("a client needs a group and a suite"));
  }
  if (entropy.len() != client_entropy(config)) {
    return Err(Entropy(entropy.len(), client_entropy(config)));
  }
  let (shares, spare) = __made_shares(config, entropy)?;
  let hello: message.ClientHello = message.ClientHello { random: __copy(entropy[0:32]), session_id: [], cipher_suites: __offers(config), extensions: __hello_extensions(config, shares) };
  let more: keyshare.ClientShare[] = [];
  let i: i32 = 1;
  while (i < shares.len()) {
    more = more.append(shares[i]);
    i = i + 1;
  }
  let step: ClientStep = __client_from_hello(__offering(hello, config.resume)?, shares[0], spare, config)?;
  return Ok(ClientStep { ...step, client: Client { ...step.client, more_shares: more } });
}

pub function client_from_hello(hello: message.ClientHello, share: keyshare.ClientShare, spare: [u8]): Result[ClientStep, HandshakeError] {
  return __client_from_hello(hello, share, spare, client_config(""));
}
```

### `35 function __offering(hello: message.ClientHello, resume: Option[Resumption]): Result[message.ClientHello, Ha...`

```fern
<<35 function __offering(hello: message.ClientHello, resume: Option[Resumption]): Result[message.ClientHello, Ha...>>=
function __offering(hello: message.ClientHello, resume: Option[Resumption]): Result[message.ClientHello, HandshakeError] {
  match (resume) {
    Some(r) => {
      if (!__has(hello.cipher_suites, r.suite.code())) {
        return Err(BadConfig("the ticket's suite is not offered"));
      }
      return Ok(__bound(hello, r, []));
    },
    None => {
      return Ok(hello);
    }
  }
  return Ok(hello);
}

function __bound(hello: message.ClientHello, r: Resumption, before: [u8]): message.ClientHello {
  let h: keyschedule.Hash = r.suite.hash();
  let id: message.PskIdentity = message.PskIdentity { identity: r.identity, obfuscated_age: r.obfuscated_age };
  let blank: u8[][] = [__zeros(h.size())];
  let draft: message.ClientHello = message.ClientHello { ...hello, extensions: __without(hello.extensions, message.EXT_PRE_SHARED_KEY).append(message.ext_pre_shared_key(message.OfferedPsks { identities: [id], binders: blank })) };
  let bytes: u8[] = message.encode(message.ClientHelloMsg(draft));
  let binder: u8[] = __binder(h, r.psk, before, bytes[0:bytes.len() - message.binders_length(blank)]);
  return message.ClientHello { ...draft, extensions: __without(draft.extensions, message.EXT_PRE_SHARED_KEY).append(message.ext_pre_shared_key(message.OfferedPsks { identities: [id], binders: [binder] })) };
}

function __client_from_hello(hello: message.ClientHello, share: keyshare.ClientShare, spare: [u8], config: ClientConfig): Result[ClientStep, HandshakeError] {
  let alpn: string[] = [];
  match (message.find(hello.extensions, message.EXT_ALPN)) {
    Some(d) => {
      match (message.alpn(d)) {
        Ok(p) => {
          alpn = p;
        },
        Err(e) => {
          return Err(Message(e));
        }
      }
    },
    None => {
      alpn = [];
    }
  }
  let bytes: u8[] = message.encode(message.ClientHelloMsg(hello));
  let w: Wrote = __plain_out(Conn { ...__conn_for(config.quic), transcript: bytes }, 0x0301, bytes);
  let hs: keyschedule.HandshakeSecret = keyschedule.HandshakeSecret { hash: keyschedule.Sha256, secret: [] };
  let c: Client = Client { conn: w.conn, state: C_WAIT_SERVER_HELLO, hello: hello, alpn_offered: alpn, share: share, more_shares: [], spare: __copy(spare), retry_suite: 0, hs: hs, client_hs: [], server_hs: [], chain: [], alpn: "", requested: false, request_context: [], resume: config.resume, resumed: false, res_master: [], own_chain: config.chain, sign_schemes: config.sign_schemes, request_schemes: [], own_scheme: 0, flight: [], client_ap: [], server_ap: [], peer_extensions: [] };
  return Ok(__client_step(c, Out { ...__out(), output: w.bytes }));
}
```

### `36 struct ClientRun`

```fern
<<36 struct ClientRun>>=
struct ClientRun { client: Client, out: Out }
```

### `37 function __server_hello(c: Client, raw: message.Raw, out: Out): Result[ClientRun, HandshakeError]`

```fern
<<37 function __server_hello(c: Client, raw: message.Raw, out: Out): Result[ClientRun, HandshakeError]>>=
function __server_hello(c: Client, raw: message.Raw, out: Out): Result[ClientRun, HandshakeError] {
  let h: message.ServerHello = message.ServerHello { random: [], session_id: [], cipher_suite: 0, extensions: [] };
  match (__decode(raw)?) {
    message.ServerHelloMsg(sh) => {
      h = sh;
    },
    _ => {
      return Err(OutOfOrder(raw.kind));
    }
  }
  if (h.is_retry()) {
    return __hello_retry(c, raw, h, out);
  }
  let suite: keyschedule.CipherSuite = __check_server_hello(c, h)?;
  if (c.retry_suite != 0 && h.cipher_suite != c.retry_suite) {
    return Err(Illegal("cipher suite after a HelloRetryRequest"));
  }
  let shared: u8[] = __agree(c, h)?;
  let psk: u8[] = __selected_psk(c, h, suite)?;
  let conn: Conn = Conn { ...c.conn, suite: suite, transcript: __join(c.conn.transcript, raw.bytes) };
  __at_key_change(conn)?;
  let hs: keyschedule.HandshakeSecret = keyschedule.early_secret(suite.hash(), psk).handshake_secret(shared);
  let th: u8[] = __hash(conn);
  let client_hs: u8[] = hs.client_traffic(th);
  let server_hs: u8[] = hs.server_traffic(th);
  conn = __with_write(__with_read(conn, server_hs, 1), client_hs, 1);
  return Ok(ClientRun { client: Client { ...c, conn: conn, state: C_WAIT_ENCRYPTED_EXTENSIONS, hs: hs, client_hs: client_hs, server_hs: server_hs, resumed: psk.len() > 0 }, out: out });
}

function __selected_psk(c: Client, h: message.ServerHello, suite: keyschedule.CipherSuite): Result[u8[], HandshakeError] {
  match (message.find(h.extensions, message.EXT_PRE_SHARED_KEY)) {
    Some(d) => {
      match (c.resume) {
        Some(r) => {
          if (__code(message.selected_psk(d))? != 0 || !__same_hash(r.suite.hash(), suite.hash())) {
            return Err(Illegal("pre_shared_key"));
          }
          return Ok(r.psk);
        },
        None => {
          return Err(UnsupportedExtension(message.EXT_PRE_SHARED_KEY));
        }
      }
    },
    None => {
      return Ok([]);
    }
  }
  return Ok([]);
}

function __code(r: Result[i32, message.MsgError]): Result[i32, HandshakeError] {
  match (r) {
    Ok(v) => {
      return Ok(v);
    },
    Err(e) => {
      return Err(Message(e));
    }
  }
  return Ok(0);
}

function __check_server_hello(c: Client, h: message.ServerHello): Result[keyschedule.CipherSuite, HandshakeError] {
  match (message.find(h.extensions, message.EXT_SUPPORTED_VERSIONS)) {
    Some(d) => {
      if (__version(d)? != message.TLS13) {
        return Err(Illegal("selected version"));
      }
    },
    None => {
      return Err(NotTls13);
    }
  }
  if (!__same(h.session_id, c.hello.session_id)) {
    return Err(Illegal("legacy_session_id_echo"));
  }
  __hello_extensions_only(c, h)?;
  return __offered_suite(c, h.cipher_suite);
}

function __hello_retry(c: Client, raw: message.Raw, h: message.ServerHello, out: Out): Result[ClientRun, HandshakeError] {
  if (c.retry_suite != 0) {
    return Err(OutOfOrder(raw.kind));
  }
  let suite: keyschedule.CipherSuite = __check_server_hello(c, h)?;
  let share: keyshare.ClientShare = __retry_share(c, h)?;
  let hello: message.ClientHello = message.ClientHello { ...c.hello, extensions: __without(__retry_extensions(c.hello.extensions, h.extensions, share), message.EXT_PRE_SHARED_KEY) };
  let digest: u8[] = suite.hash().digest(c.conn.transcript);
  let before: u8[] = __cat(__cat([254 as u8, 0 as u8, 0 as u8, digest.len() as u8], digest), raw.bytes);
  let resume: Option[Resumption] = None;
  match (c.resume) {
    Some(r) => {
      if (__same_hash(r.suite.hash(), suite.hash())) {
        hello = __bound(hello, r, before);
        resume = Some(r);
      }
    },
    None => {}
  }
  let ch2: u8[] = message.encode(message.ClientHelloMsg(hello));
  let transcript: u8[] = __cat(before, ch2);
  __at_key_change(c.conn)?;
  let w: Wrote = __plain_out(Conn { ...c.conn, transcript: transcript }, 0x0303, ch2);
  let next: Client = Client { ...c, conn: w.conn, hello: hello, share: share, more_shares: [], spare: [], retry_suite: h.cipher_suite, resume: resume };
  return Ok(ClientRun { client: next, out: Out { ...out, output: __join(out.output, w.bytes) } });
}

function __retry_share(c: Client, h: message.ServerHello): Result[keyshare.ClientShare, HandshakeError] {
  let code: i32 = __retry_group(c, h)?;
  match (keyshare.group(code)) {
    Some(g) => {
      if (c.spare.len() < g.client_entropy()) {
        return Err(Entropy(c.spare.len(), g.client_entropy()));
      }
      match (keyshare.client_share(g, c.spare[0:g.client_entropy()])) {
        Ok(share) => {
          return Ok(share);
        },
        Err(e) => {
          return Err(KeyShare(e));
        }
      }
    },
    None => {
      return Err(HandshakeFailure("the server asks for a group this client cannot make"));
    }
  }
  return Err(Illegal("HelloRetryRequest group"));
}
```

### `38 function __retry_group(c: Client, h: message.ServerHello): Result[i32, HandshakeError]`

```fern
<<38 function __retry_group(c: Client, h: message.ServerHello): Result[i32, HandshakeError]>>=
function __retry_group(c: Client, h: message.ServerHello): Result[i32, HandshakeError] {
  let offered: i32[] = __codes(message.supported_groups(__ext(c.hello.extensions, message.EXT_SUPPORTED_GROUPS, "supported_groups")?))?;
  match (message.retry_group(__ext(h.extensions, message.EXT_KEY_SHARE, "key_share")?)) {
    Ok(code) => {
      if (__sends_share(c, code) || !__has(offered, code)) {
        return Err(Illegal("HelloRetryRequest group"));
      }
      return Ok(code);
    },
    Err(e) => {
      return Err(Message(e));
    }
  }
  return Err(Illegal("HelloRetryRequest group"));
}

function __retry_extensions(first: message.Extension[], retry: message.Extension[], share: keyshare.ClientShare): message.Extension[] {
  let out: message.Extension[] = [];
  for e in first {
    if (e.kind == message.EXT_KEY_SHARE) {
      out = out.append(message.ext_key_shares([message.KeyShareEntry { group: share.group.code(), key: share.public }]));
    } else {
      out = out.append(e);
    }
  }
  if let Some(cookie) = message.find(retry, message.EXT_COOKIE) {
    out = out.append(message.Extension { kind: message.EXT_COOKIE, data: cookie });
  }
  return out;
}

function __offered_suite(c: Client, code: i32): Result[keyschedule.CipherSuite, HandshakeError] {
  match (keyschedule.cipher_suite(code)) {
    Some(s) => {
      if (__has(c.hello.cipher_suites, code)) {
        return Ok(s);
      }
    },
    None => {
      return Err(Illegal("cipher suite"));
    }
  }
  return Err(Illegal("cipher suite"));
}

function __hello_extensions_only(c: Client, h: message.ServerHello): Result[boolean, HandshakeError] {
  let psk: boolean = false;
  match (c.resume) {
    Some(r) => {
      psk = !h.is_retry();
    },
    None => {}
  }
  let i: i32 = 0;
  while (i < h.extensions.len()) {
    let k: i32 = h.extensions[i].kind;
    if (k != message.EXT_SUPPORTED_VERSIONS && k != message.EXT_KEY_SHARE && (k != message.EXT_COOKIE || !h.is_retry()) && (k != message.EXT_PRE_SHARED_KEY || !psk)) {
      return Err(UnsupportedExtension(k));
    }
    i = i + 1;
  }
  return Ok(true);
}

function __version(d: u8[]): Result[i32, HandshakeError] {
  match (message.selected_version(d)) {
    Ok(v) => {
      return Ok(v);
    },
    Err(e) => {
      return Err(Message(e));
    }
  }
  return Err(NotTls13);
}

function __sent_share(c: Client, code: i32): Option[keyshare.ClientShare] {
  if (c.share.group.code() == code) {
    return Some(c.share);
  }
  for sh in c.more_shares {
    if (sh.group.code() == code) {
      return Some(sh);
    }
  }
  return None;
}

function __sends_share(c: Client, code: i32): boolean {
  match (__sent_share(c, code)) {
    Some(_) => {
      return true;
    },
    None => {
      return false;
    }
  }
}

function __agree(c: Client, h: message.ServerHello): Result[u8[], HandshakeError] {
  match (message.key_share(__ext(h.extensions, message.EXT_KEY_SHARE, "key_share")?)) {
    Ok(e) => {
      let Some(share) = __sent_share(c, e.group) else {
        return Err(Illegal("key share group"));
      };
      match (share.shared_secret(e.key)) {
        Ok(s) => {
          return Ok(s);
        },
        Err(err) => {
          return Err(KeyShare(err));
        }
      }
    },
    Err(e) => {
      return Err(Message(e));
    }
  }
  return Err(MissingExtension("key_share"));
}

function __hello_only(k: i32): boolean {
  return k == message.EXT_KEY_SHARE || k == message.EXT_SUPPORTED_VERSIONS || k == message.EXT_PRE_SHARED_KEY || k == message.EXT_SIGNATURE_ALGORITHMS;
}

function __offered_kinds(c: Client): i32[] {
  let offered: i32[] = [];
  for e in c.hello.extensions {
    offered = offered.append(e.kind);
  }
  return offered;
}
```

### `39 function __encrypted_extensions(c: Client, exts: message.Extension[]): Result[Client, HandshakeError]`

```fern
<<39 function __encrypted_extensions(c: Client, exts: message.Extension[]): Result[Client, HandshakeError]>>=
function __encrypted_extensions(c: Client, exts: message.Extension[]): Result[Client, HandshakeError] {
  let offered: i32[] = __offered_kinds(c);
  let alpn: string = "";
  let i: i32 = 0;
  while (i < exts.len()) {
    let k: i32 = exts[i].kind;
    if (__hello_only(k)) {
      return Err(Illegal("extension in EncryptedExtensions"));
    }
    if (!__has(offered, k)) {
      return Err(UnsupportedExtension(k));
    }
    if (k == message.EXT_ALPN) {
      alpn = __chosen_alpn(c, exts[i].data)?;
    }
    i = i + 1;
  }
  if (c.conn.quic) {
    __quic_terms(exts, alpn)?;
  }
  if (c.resumed) {
    return Ok(Client { ...c, alpn: alpn, peer_extensions: exts, state: C_WAIT_FINISHED });
  }
  return Ok(Client { ...c, alpn: alpn, peer_extensions: exts, state: C_WAIT_CERTIFICATE_OR_REQUEST });
}

function __quic_terms(exts: message.Extension[], alpn: string): Result[boolean, HandshakeError] {
  match (message.find(exts, message.EXT_QUIC_TRANSPORT_PARAMETERS)) {
    Some(d) => {},
    None => {
      return Err(MissingExtension("quic_transport_parameters"));
    }
  }
  if (alpn.len() == 0) {
    return Err(NoApplicationProtocol);
  }
  return Ok(true);
}

function __chosen_alpn(c: Client, d: u8[]): Result[string, HandshakeError] {
  match (message.alpn(d)) {
    Ok(p) => {
      if (p.len() != 1 || !__has_string(c.alpn_offered, p[0])) {
        return Err(Illegal("application protocol"));
      }
      return Ok(p[0]);
    },
    Err(e) => {
      return Err(Message(e));
    }
  }
  return Err(Illegal("application protocol"));
}

function __offered_schemes(c: Client): i32[] {
  match (message.find(c.hello.extensions, message.EXT_SIGNATURE_ALGORITHMS)) {
    Some(d) => {
      match (message.signature_algorithms(d)) {
        Ok(s) => {
          return s;
        },
        Err(e) => {
          return [];
        }
      }
    },
    None => {
      return [];
    }
  }
  return [];
}

function __server_finished(c: Client, raw: message.Raw, verify_data: u8[], out: Out): Result[ClientRun, HandshakeError] {
  let h: keyschedule.Hash = c.conn.suite.hash();
  if (!keyschedule.verify_finished(h, c.server_hs, __hash(c.conn), verify_data)) {
    return Err(DecryptError);
  }
  let conn: Conn = Conn { ...c.conn, transcript: __join(c.conn.transcript, raw.bytes) };
  __at_key_change(conn)?;
  let master: keyschedule.MasterSecret = c.hs.master_secret();
  let th: u8[] = __hash(conn);
  let keyed: Client = Client { ...c, server_ap: master.server_traffic(th), client_ap: master.client_traffic(th) };
  if (!c.requested) {
    return __client_finish(keyed, conn, [], out);
  }
  let scheme: i32 = __own_scheme(c);
  let entries: message.CertificateEntry[] = [];
  if (scheme != 0) {
    for d in c.own_chain {
      entries = entries.append(message.CertificateEntry { data: d, extensions: [] });
    }
  }
  let cert: u8[] = message.encode(message.CertificateMsg(message.Certificate { context: c.request_context, entries: entries }));
  conn = Conn { ...conn, transcript: __join(conn.transcript, cert) };
  if (entries.len() == 0) {
    return __client_finish(keyed, conn, cert, out);
  }
  let ev: Event = SignNeeded(Signing { scheme: scheme, signed: certificate_verify_content(false, __hash(conn)) });
  return Ok(ClientRun { client: Client { ...keyed, conn: conn, state: C_WAIT_SIGNATURE, own_scheme: scheme, flight: cert }, out: Out { ...out, events: out.events.append(ev) } });
}

function __own_scheme(c: Client): i32 {
  if (c.own_chain.len() == 0) {
    return 0;
  }
  for scheme in c.sign_schemes {
    if (__has(c.request_schemes, scheme)) {
      return scheme;
    }
  }
  return 0;
}

function __client_finish(c: Client, conn: Conn, flight: u8[], out: Out): Result[ClientRun, HandshakeError] {
  let h: keyschedule.Hash = conn.suite.hash();
  let fin: u8[] = message.encode(message.Finished(keyschedule.finished_verify_data(h, c.client_hs, __hash(conn))));
  let next: Conn = Conn { ...conn, transcript: __join(conn.transcript, fin) };
  let res_master: u8[] = c.hs.master_secret().resumption(__hash(next));
  let w: Wrote = __write(next, record.Handshake, __cat(__copy(flight), fin))?;
  next = __with_read(__with_write(w.conn, c.client_ap, 2), c.server_ap, 2);
  let done: Out = Out { ...out, output: __join(out.output, w.bytes), events: out.events.append(Connected) };
  return Ok(ClientRun { client: Client { ...c, conn: next, state: C_CONNECTED, res_master: res_master, flight: [], client_ap: [], server_ap: [] }, out: done });
}
```

### `40 function __client_message(c: Client, raw: message.Raw, out: Out): Result[ClientRun, HandshakeError]`

```fern
<<40 function __client_message(c: Client, raw: message.Raw, out: Out): Result[ClientRun, HandshakeError]>>=
function __client_message(c: Client, raw: message.Raw, out: Out): Result[ClientRun, HandshakeError] {
  if (c.state == C_WAIT_SERVER_HELLO) {
    return __server_hello(c, raw, out);
  }
  let m: message.Message = __decode(raw)?;
  if (c.state == C_CONNECTED) {
    return __client_after(c, raw.kind, m, out);
  }
  let next: Client = Client { ...c, conn: Conn { ...c.conn, transcript: __join(c.conn.transcript, raw.bytes) } };
  if (c.state == C_WAIT_ENCRYPTED_EXTENSIONS) {
    return __on_encrypted_extensions(next, raw.kind, m, out);
  }
  if (c.state == C_WAIT_CERTIFICATE_OR_REQUEST || c.state == C_WAIT_CERTIFICATE) {
    return __on_certificate(next, raw.kind, m, out);
  }
  if (c.state == C_WAIT_CERTIFICATE_VERIFY) {
    return __on_certificate_verify(c, next, raw.kind, m, out);
  }
  if (c.state == C_WAIT_FINISHED) {
    match (m) {
      message.Finished(verify_data) => {
        return __server_finished(c, raw, verify_data, out);
      },
      _ => {
        return Err(OutOfOrder(raw.kind));
      }
    }
  }
  return Err(OutOfOrder(raw.kind));
}

function __on_encrypted_extensions(c: Client, kind: i32, m: message.Message, out: Out): Result[ClientRun, HandshakeError] {
  match (m) {
    message.EncryptedExtensions(exts) => {
      return Ok(ClientRun { client: __encrypted_extensions(c, exts)?, out: out });
    },
    _ => {
      return Err(OutOfOrder(kind));
    }
  }
  return Err(OutOfOrder(kind));
}

function __on_certificate(c: Client, kind: i32, m: message.Message, out: Out): Result[ClientRun, HandshakeError] {
  match (m) {
    message.CertificateRequestMsg(r) => {
      if (c.state == C_WAIT_CERTIFICATE_OR_REQUEST) {
        let schemes: i32[] = __codes(message.signature_algorithms(__ext(r.extensions, message.EXT_SIGNATURE_ALGORITHMS, "signature_algorithms")?))?;
        return Ok(ClientRun { client: Client { ...c, requested: true, request_context: r.context, request_schemes: schemes, state: C_WAIT_CERTIFICATE }, out: out });
      }
    },
    message.CertificateMsg(cert) => {
      return Ok(ClientRun { client: Client { ...c, chain: __chain(cert)?, state: C_WAIT_CERTIFICATE_VERIFY }, out: out });
    },
    _ => {
      return Err(OutOfOrder(kind));
    }
  }
  return Err(OutOfOrder(kind));
}

function __chain(cert: message.Certificate): Result[u8[][], HandshakeError] {
  if (cert.context.len() != 0) {
    return Err(Illegal("certificate_request_context"));
  }
  if (cert.entries.len() == 0) {
    return Err(Message(message.DecodeError("certificate_list")));
  }
  let chain: u8[][] = [];
  let i: i32 = 0;
  while (i < cert.entries.len()) {
    chain = chain.append(cert.entries[i].data);
    i = i + 1;
  }
  return Ok(chain);
}

function __on_certificate_verify(c: Client, next: Client, kind: i32, m: message.Message, out: Out): Result[ClientRun, HandshakeError] {
  match (m) {
    message.CertificateVerifyMsg(v) => {
      if (!__has(__offered_schemes(c), v.scheme)) {
        return Err(Illegal("signature scheme"));
      }
      let signed: u8[] = certificate_verify_content(true, __hash(c.conn));
      let ev: Event = VerifyServer(Verification { chain: c.chain, scheme: v.scheme, signed: signed, signature: v.signature });
      return Ok(ClientRun { client: Client { ...next, state: C_WAIT_VERDICT }, out: Out { ...out, events: out.events.append(ev) } });
    },
    _ => {
      return Err(OutOfOrder(kind));
    }
  }
  return Err(OutOfOrder(kind));
}

function __client_after(c: Client, kind: i32, m: message.Message, out: Out): Result[ClientRun, HandshakeError] {
  match (m) {
    message.NewSessionTicketMsg(t) => {
      return Ok(ClientRun { client: c, out: Out { ...out, events: out.events.append(Ticket(t)) } });
    },
    message.KeyUpdate(request) => {
      let a: Absorbed = __key_update(c.conn, request, out)?;
      return Ok(ClientRun { client: Client { ...c, conn: a.conn }, out: a.out });
    },
    _ => {
      return Err(OutOfOrder(kind));
    }
  }
  return Err(OutOfOrder(kind));
}

function __client_parked(state: i32): boolean {
  return state == C_WAIT_VERDICT || state == C_WAIT_SIGNATURE || state == C_CLOSED;
}

function __client_drain(c: Client, out: Out): Result[ClientRun, HandshakeError] {
  let client: Client = c;
  while (!__client_parked(client.state)) {
    let n: Next = __next(client.conn)?;
    if (!n.has) {
      break;
    }
    let r: ClientRun = __client_message(Client { ...client, conn: n.conn }, n.raw, out)?;
    client = r.client;
    out = r.out;
  }
  return Ok(ClientRun { client: client, out: out });
}
```

### `41 function __client_run(c: Client, out: Out): Result[ClientStep, HandshakeError]`

```fern
<<41 function __client_run(c: Client, out: Out): Result[ClientStep, HandshakeError]>>=
function __client_run(c: Client, out: Out): Result[ClientStep, HandshakeError] {
  let records: record.Record[] = c.conn.held;
  let r: ClientRun = __client_drain(c, out)?;
  let i: i32 = 0;
  while (!__client_parked(r.client.state) && i < records.len()) {
    let a: Absorbed = __absorb(r.client.conn, records[i], r.client.state < C_CONNECTED, r.client.state == C_CONNECTED, r.out)?;
    let state: i32 = r.client.state;
    if (a.out.closed) {
      state = C_CLOSED;
    }
    r = __client_drain(Client { ...r.client, conn: a.conn, state: state }, a.out)?;
    i = i + 1;
  }
  let client: Client = Client { ...r.client, conn: Conn { ...r.client.conn, held: __rest(records, i) } };
  return Ok(__client_step(client, r.out));
}

function __rest(records: record.Record[], from: i32): record.Record[] {
  let out: record.Record[] = [];
  let i: i32 = from;
  while (i < records.len()) {
    out = out.append(records[i]);
    i = i + 1;
  }
  return out;
}

function __deframe(c: Conn, input: [u8]): Result[Conn, HandshakeError] {
  match (__records(c.inbox, input)) {
    Ok(d) => {
      let held: record.Record[] = c.held;
      let i: i32 = 0;
      while (i < d.records.len()) {
        held = held.append(d.records[i]);
        i = i + 1;
      }
      return Ok(Conn { ...c, held: held, inbox: d.rest });
    },
    Err(e) => {
      return Err(Record(e));
    }
  }
  return Err(Misuse("deframe"));
}

function __records(inbox: u8[], input: [u8]): Result[record.Deframed, record.RecordError] {
  if (inbox.len() == 0) {
    return record.deframe(input);
  }
  return record.deframe(__cat(__copy(inbox), input));
}
```

### `42 pub function (c: Client) read(input: [u8]): Result[ClientStep, HandshakeError]`

```fern
<<42 pub function (c: Client) read(input: [u8]): Result[ClientStep, HandshakeError]>>=
pub function (c: Client) read(input: [u8]): Result[ClientStep, HandshakeError] {
  if (c.state == C_CLOSED) {
    return Err(Misuse("read after close"));
  }
  if (c.conn.quic) {
    return Err(Misuse("read under QUIC"));
  }
  return __client_run(Client { ...c, conn: __deframe(c.conn, input)? }, __out());
}

pub function (c: Client) read_crypto(level: i32, bytes: [u8]): Result[ClientStep, HandshakeError] {
  if (c.state == C_CLOSED) {
    return Err(Misuse("read_crypto after close"));
  }
  if (!c.conn.quic) {
    return Err(Misuse("read_crypto"));
  }
  if (level != c.conn.reading) {
    return Err(Level(level));
  }
  return __client_run(Client { ...c, conn: Conn { ...c.conn, pending: __join(c.conn.pending, bytes) } }, __out());
}

pub function (c: Client) peer_extensions(): message.Extension[] {
  return c.peer_extensions;
}

pub function (c: Client) verdict(ok: boolean): Result[ClientStep, HandshakeError] {
  if (c.state != C_WAIT_VERDICT) {
    return Err(Misuse("verdict"));
  }
  if (!ok) {
    return Err(BadCertificate);
  }
  return __client_run(Client { ...c, state: C_WAIT_FINISHED }, __out());
}

pub function (c: Client) signature(sig: [u8]): Result[ClientStep, HandshakeError] {
  if (c.state != C_WAIT_SIGNATURE) {
    return Err(Misuse("signature"));
  }
  let cv: u8[] = message.encode(message.CertificateVerifyMsg(message.CertificateVerify { scheme: c.own_scheme, signature: __copy(sig) }));
  let conn: Conn = Conn { ...c.conn, transcript: __join(c.conn.transcript, cv) };
  let r: ClientRun = __client_finish(c, conn, __cat(__copy(c.flight), cv), __out())?;
  return __client_run(r.client, r.out);
}

pub function (c: Client) write(data: [u8]): Result[ClientStep, HandshakeError] {
  if (c.state != C_CONNECTED) {
    return Err(Misuse("write"));
  }
  let w: Wrote = __write(c.conn, record.ApplicationData, data)?;
  return Ok(__client_step(Client { ...c, conn: w.conn }, Out { ...__out(), output: w.bytes }));
}

pub function (c: Client) close(): Result[ClientStep, HandshakeError] {
  let w: Wrote = __write(c.conn, record.Alert, [1 as u8, 0 as u8])?;
  return Ok(__client_step(Client { ...c, conn: w.conn, state: C_CLOSED }, Out { ...__out(), output: w.bytes }));
}

pub function (c: Client) alert(e: HandshakeError): u8[] {
  return __alert_record(c.conn, e);
}

pub function (c: Client) connected(): boolean {
  return c.state == C_CONNECTED;
}

pub function (c: Client) update_keys(request: boolean): Result[ClientStep, HandshakeError] {
  if (c.state != C_CONNECTED) {
    return Err(Misuse("update_keys"));
  }
  let w: Wrote = __update(c.conn, request)?;
  return Ok(__client_step(Client { ...c, conn: w.conn }, Out { ...__out(), output: w.bytes }));
}

pub function (c: Client) offloaded(): boolean {
  return c.conn.kernel;
}
```

### `43 pub struct ClientOffload`

```fern
<<43 pub struct ClientOffload>>=
pub struct ClientOffload { client: Client, keys: KernelKeys }
```

### `44 pub function (c: Client) offload(): Option[ClientOffload]`

```fern
<<44 pub function (c: Client) offload(): Option[ClientOffload]>>=
pub function (c: Client) offload(): Option[ClientOffload] {
  if (c.state != C_CONNECTED) {
    return None;
  }
  match (__offload(c.conn)) {
    Some(o) => {
      return Some(ClientOffload { client: Client { ...c, conn: o.conn }, keys: o.keys });
    },
    None => {}
  }
  return None;
}

pub function (c: Client) alpn(): string {
  return c.alpn;
}

pub function (c: Client) resumed(): boolean {
  return c.resumed;
}

pub function (c: Client) cipher_suite(): keyschedule.CipherSuite {
  return c.conn.suite;
}

pub function (c: Client) resumption_psk(nonce: [u8]): Result[u8[], HandshakeError] {
  if (c.res_master.len() == 0) {
    return Err(Misuse("resumption_psk"));
  }
  return Ok(keyschedule.resumption_psk(c.conn.suite.hash(), c.res_master, nonce));
}

pub function (c: Client) save(): Result[u8[], HandshakeError] {
  if (c.conn.quic) {
    return Err(Misuse("save under QUIC"));
  }
  if (c.conn.kernel) {
    return Err(Misuse("save once offloaded"));
  }
  if (c.state != C_CONNECTED || c.conn.held.len() > 0) {
    return Err(Misuse("save"));
  }
  let out: u8[] = __u32_to([1 as u8], c.conn.suite.code());
  out = __saved_direction(out, c.conn.read_secret, c.conn.read.seq);
  out = __saved_direction(out, c.conn.write_secret, c.conn.write.seq);
  out = __saved_field(out, c.conn.inbox);
  out = __saved_field(out, c.conn.pending);
  return Ok(__saved_field(out, c.alpn.bytes()));
}

pub function client_restore(saved: [u8]): Result[Client, HandshakeError] {
  if (saved.len() < 5 || saved[0] != 1 as u8) {
    return Err(Misuse("client_restore"));
  }
  let suite: keyschedule.CipherSuite = keyschedule.Aes128GcmSha256;
  match (keyschedule.cipher_suite(__u32_at(saved, 1))) {
    Some(s) => {
      suite = s;
    },
    None => {
      return Err(Misuse("client_restore"));
    }
  }
  let f: u8[][] = __saved_fields(saved, 5)?;
  let conn: Conn = Conn { ...__conn(), suite: suite, inbox: f[4], pending: f[5] };
  conn = __with_read(conn, f[0], 2);
  conn = __with_write(conn, f[2], 2);
  conn = Conn { ...conn, read: record.Protection { ...conn.read, seq: __u64_of(f[1]) }, write: record.Protection { ...conn.write, seq: __u64_of(f[3]) } };
  let none: message.ClientHello = message.ClientHello { random: [], session_id: [], cipher_suites: [], extensions: [] };
  let hs: keyschedule.HandshakeSecret = keyschedule.HandshakeSecret { hash: suite.hash(), secret: [] };
  let share: keyshare.ClientShare = keyshare.ClientShare { group: keyshare.X25519, public: [], scalar: [], decapsulation_key: [] };
  let alpn: string = "";
  match (utf8.from_bytes(f[6])) {
    Some(p) => {
      alpn = p;
    },
    None => {
      return Err(Misuse("client_restore"));
    }
  }
  return Ok(Client { conn: conn, state: C_CONNECTED, hello: none, alpn_offered: [], share: share, more_shares: [], spare: [], retry_suite: 0, hs: hs, client_hs: [], server_hs: [], chain: [], alpn: alpn, requested: false, request_context: [], resume: None, resumed: false, res_master: [], own_chain: [], sign_schemes: [], request_schemes: [], own_scheme: 0, flight: [], client_ap: [], server_ap: [], peer_extensions: [] });
}
```

### `45 function __u32_to(own out: u8[], v: i32): u8[]`

```fern
<<45 function __u32_to(own out: u8[], v: i32): u8[]>>=
function __u32_to(own out: u8[], v: i32): u8[] {
  return out.append((v >> 24 & 255) as u8).append((v >> 16 & 255) as u8).append((v >> 8 & 255) as u8).append((v & 255) as u8);
}

function __u32_at(b: [u8], at: i32): i32 {
  return b[at] as i32 << 24 | b[at + 1] as i32 << 16 | b[at + 2] as i32 << 8 | b[at + 3] as i32;
}

function __saved_field(own out: u8[], b: [u8]): u8[] {
  return __cat(__u32_to(out, b.len()), b);
}

function __saved_direction(own out: u8[], secret: [u8], seq: u64): u8[] {
  let s: u8[] = [];
  let i: i32 = 7;
  while (i >= 0) {
    s = s.append((seq >> (8 * i) as u64 & 255 as u64) as u8);
    i = i - 1;
  }
  return __saved_field(__saved_field(out, secret), s);
}

function __u64_of(b: [u8]): u64 {
  let v: u64 = 0 as u64;
  for x in b {
    v = v << 8 as u64 | x as u64;
  }
  return v;
}

function __saved_fields(saved: [u8], from: i32): Result[u8[][], HandshakeError] {
  let out: u8[][] = [];
  let at: i32 = from;
  while (at < saved.len()) {
    if (saved.len() - at < 4) {
      return Err(Misuse("client_restore"));
    }
    let n: i32 = __u32_at(saved, at);
    if (n < 0 || n > saved.len() - at - 4) {
      return Err(Misuse("client_restore"));
    }
    out = out.append(__copy(saved[at + 4:at + 4 + n]));
    at = at + 4 + n;
  }
  if (out.len() != 7 || out[1].len() != 8 || out[3].len() != 8) {
    return Err(Misuse("client_restore"));
  }
  return Ok(out);
}
```

### `46 pub struct ServerConfig`

```fern
<<46 pub struct ServerConfig>>=
pub struct ServerConfig { chain: u8[][], schemes: i32[], suites: keyschedule.CipherSuite[], groups: keyshare.Group[], alpn: string[], request_client_certificate: boolean, quic: boolean, extensions: message.Extension[] }
```

### `47 pub function server_config(chain: u8[][], schemes: i32[]): ServerConfig`

```fern
<<47 pub function server_config(chain: u8[][], schemes: i32[]): ServerConfig>>=
pub function server_config(chain: u8[][], schemes: i32[]): ServerConfig {
  return ServerConfig { chain: chain, schemes: schemes, suites: [keyschedule.Aes128GcmSha256, keyschedule.Aes256GcmSha384, keyschedule.ChaCha20Poly1305Sha256], groups: [keyshare.X25519MlKem768, keyshare.X25519, keyshare.Secp256r1, keyshare.Secp256r1MlKem768], alpn: [], request_client_certificate: false, quic: false, extensions: [] };
}
```

### `48 const VERIFY_SCHEMES: i32[]`

```fern
<<48 const VERIFY_SCHEMES: i32[]>>=
const VERIFY_SCHEMES: i32[] = [0x0403, 0x0503, 0x0807, 0x0804, 0x0805, 0x0806];

const S_WAIT_CLIENT_HELLO: i32 = 0;

const S_WAIT_SIGNATURE: i32 = 1;

const S_WAIT_FINISHED: i32 = 2;

const S_CONNECTED: i32 = 3;

const S_CLOSED: i32 = 4;

const S_WAIT_RESUME: i32 = 5;

const S_WAIT_CLIENT_CERTIFICATE: i32 = 6;

const S_WAIT_CLIENT_VERIFY: i32 = 7;

const S_WAIT_CLIENT_VERDICT: i32 = 8;
```

### `49 pub function server_entropy(config: ServerConfig): i32`

```fern
<<49 pub function server_entropy(config: ServerConfig): i32>>=
pub function server_entropy(config: ServerConfig): i32 {
  let most: i32 = 0;
  for g in config.groups {
    if (g.server_entropy() > most) {
      most = g.server_entropy();
    }
  }
  return 32 + most;
}
```

### `50 pub struct Server`

```fern
<<50 pub struct Server>>=
pub struct Server {
  conn: Conn,
  state: i32,
  config: ServerConfig,
  entropy: u8[],
  retry_group: i32,
  retry_suite: i32,
  scheme: i32,
  hs: keyschedule.HandshakeSecret,
  client_hs: u8[],
  server_hs: u8[],
  flight: u8[],
  server_name: string,
  alpn: string,
  parked: message.Raw,
  resumed: boolean,
  res_master: u8[],
  finished_hash: u8[],
  client_chain: u8[][],
  peer_extensions: message.Extension[],
}

pub struct ServerStep { server: Server, output: u8[], data: u8[], events: Event[] }
```

### `51 function __server_step(s: Server, out: Out): ServerStep`

```fern
<<51 function __server_step(s: Server, out: Out): ServerStep>>=
function __server_step(s: Server, out: Out): ServerStep {
  let f: Flushed = __flushed(s.conn, out);
  return ServerStep { server: Server { ...s, conn: f.conn }, output: f.out.output, data: f.out.data, events: f.out.events };
}
```

### `52 pub function server_new(config: ServerConfig, entropy: [u8]): Result[Server, HandshakeError]`

```fern
<<52 pub function server_new(config: ServerConfig, entropy: [u8]): Result[Server, HandshakeError]>>=
pub function server_new(config: ServerConfig, entropy: [u8]): Result[Server, HandshakeError] {
  if (config.chain.len() == 0 || config.schemes.len() == 0 || config.suites.len() == 0 || config.groups.len() == 0) {
    return Err(BadConfig("a server needs a certificate, a signature scheme, a suite and a group"));
  }
  if (entropy.len() != server_entropy(config)) {
    return Err(Entropy(entropy.len(), server_entropy(config)));
  }
  let hs: keyschedule.HandshakeSecret = keyschedule.HandshakeSecret { hash: keyschedule.Sha256, secret: [] };
  return Ok(Server { conn: __conn_for(config.quic), state: S_WAIT_CLIENT_HELLO, config: config, entropy: __copy(entropy), retry_group: 0, retry_suite: 0, scheme: 0, hs: hs, client_hs: [], server_hs: [], flight: [], server_name: "", alpn: "", parked: message.Raw { kind: 0, bytes: [] }, resumed: false, res_master: [], finished_hash: [], client_chain: [], peer_extensions: [] });
}
```

### `53 struct ServerRun`

```fern
<<53 struct ServerRun>>=
struct ServerRun { server: Server, out: Out }
```

### `54 function __ext(exts: message.Extension[], kind: i32, what: string): Result[u8[], HandshakeError]`

```fern
<<54 function __ext(exts: message.Extension[], kind: i32, what: string): Result[u8[], HandshakeError]>>=
function __ext(exts: message.Extension[], kind: i32, what: string): Result[u8[], HandshakeError] {
  match (message.find(exts, kind)) {
    Some(d) => {
      return Ok(d);
    },
    None => {
      return Err(MissingExtension(what));
    }
  }
  return Err(MissingExtension(what));
}

function __codes(r: Result[i32[], message.MsgError]): Result[i32[], HandshakeError] {
  match (r) {
    Ok(v) => {
      return Ok(v);
    },
    Err(e) => {
      return Err(Message(e));
    }
  }
  return Ok([]);
}
```

### `55 struct Choice`

```fern
<<55 struct Choice>>=
struct Choice { group: keyshare.Group, key: u8[], retry: boolean }
```

### `56 function __client_shares(h: message.ClientHello): Result[message.KeyShareEntry[], HandshakeError]`

```fern
<<56 function __client_shares(h: message.ClientHello): Result[message.KeyShareEntry[], HandshakeError]>>=
function __client_shares(h: message.ClientHello): Result[message.KeyShareEntry[], HandshakeError] {
  match (message.key_shares(__ext(h.extensions, message.EXT_KEY_SHARE, "key_share")?)) {
    Ok(v) => {
      return Ok(v);
    },
    Err(e) => {
      return Err(Message(e));
    }
  }
  return Err(MissingExtension("key_share"));
}

function __choose_share(s: Server, h: message.ClientHello): Result[Choice, HandshakeError] {
  let groups: i32[] = __codes(message.supported_groups(__ext(h.extensions, message.EXT_SUPPORTED_GROUPS, "supported_groups")?))?;
  let shares: message.KeyShareEntry[] = __client_shares(h)?;
  let i: i32 = 0;
  while (i < shares.len()) {
    if (!__has(groups, shares[i].group)) {
      return Err(Illegal("key share for a group not in supported_groups"));
    }
    i = i + 1;
  }
  let g: i32 = 0;
  while (g < s.config.groups.len()) {
    let j: i32 = 0;
    while (j < shares.len()) {
      if (shares[j].group == s.config.groups[g].code()) {
        return Ok(Choice { group: s.config.groups[g], key: shares[j].key, retry: false });
      }
      j = j + 1;
    }
    g = g + 1;
  }
  return __retry_choice(s, groups);
}

function __retry_choice(s: Server, groups: i32[]): Result[Choice, HandshakeError] {
  for want in s.config.groups {
    if (__has(groups, want.code())) {
      return Ok(Choice { group: want, key: [], retry: true });
    }
  }
  return Err(HandshakeFailure("no group in common"));
}

function __choose_alpn(s: Server, h: message.ClientHello): Result[string, HandshakeError] {
  match (message.find(h.extensions, message.EXT_ALPN)) {
    Some(d) => {
      match (message.alpn(d)) {
        Ok(offered) => {
          if (s.config.alpn.len() == 0) {
            return Ok("");
          }
          let i: i32 = 0;
          while (i < s.config.alpn.len()) {
            if (__has_string(offered, s.config.alpn[i])) {
              return Ok(s.config.alpn[i]);
            }
            i = i + 1;
          }
          return Err(NoApplicationProtocol);
        },
        Err(e) => {
          return Err(Message(e));
        }
      }
    },
    None => {
      return Ok("");
    }
  }
  return Ok("");
}

function __server_name(h: message.ClientHello): Result[string, HandshakeError] {
  match (message.find(h.extensions, message.EXT_SERVER_NAME)) {
    Some(d) => {
      match (message.server_name(d)) {
        Ok(n) => {
          return Ok(n);
        },
        Err(e) => {
          return Err(Message(e));
        }
      }
    },
    None => {
      return Ok("");
    }
  }
  return Ok("");
}

function __as_client_hello(raw: message.Raw): Result[message.ClientHello, HandshakeError] {
  match (__decode(raw)?) {
    message.ClientHelloMsg(h) => {
      return Ok(h);
    },
    _ => {
      return Err(OutOfOrder(raw.kind));
    }
  }
  return Err(OutOfOrder(raw.kind));
}

function __offers_tls13(h: message.ClientHello): Result[boolean, HandshakeError] {
  match (message.find(h.extensions, message.EXT_SUPPORTED_VERSIONS)) {
    Some(d) => {
      if (__has(__codes(message.supported_versions(d))?, message.TLS13)) {
        return Ok(true);
      }
    },
    None => {
      return Err(NotTls13);
    }
  }
  return Err(NotTls13);
}

function __pick_suite(s: Server, h: message.ClientHello): Result[keyschedule.CipherSuite, HandshakeError] {
  let i: i32 = 0;
  while (i < s.config.suites.len()) {
    if (__has(h.cipher_suites, s.config.suites[i].code())) {
      return Ok(s.config.suites[i]);
    }
    i = i + 1;
  }
  return Err(HandshakeFailure("no cipher suite in common"));
}
```

### `57 function __pick_scheme(s: Server, h: message.ClientHello): Result[i32, HandshakeError]`

```fern
<<57 function __pick_scheme(s: Server, h: message.ClientHello): Result[i32, HandshakeError]>>=
function __pick_scheme(s: Server, h: message.ClientHello): Result[i32, HandshakeError] {
  let schemes: i32[] = __codes(message.signature_algorithms(__ext(h.extensions, message.EXT_SIGNATURE_ALGORITHMS, "signature_algorithms")?))?;
  let i: i32 = 0;
  while (i < s.config.schemes.len()) {
    if (__has(schemes, s.config.schemes[i])) {
      return Ok(s.config.schemes[i]);
    }
    i = i + 1;
  }
  return Err(HandshakeFailure("no signature scheme in common"));
}

function __answer_share(s: Server, choice: Choice): Result[keyshare.ServerShare, HandshakeError] {
  match (keyshare.server_share(choice.group, choice.key, s.entropy[32:32 + choice.group.server_entropy()])) {
    Ok(ss) => {
      return Ok(ss);
    },
    Err(e) => {
      return Err(KeyShare(e));
    }
  }
  return Err(HandshakeFailure("key share"));
}

function __encrypted_extensions_msg(s: Server, alpn: string): u8[] {
  let ee: message.Extension[] = [];
  if (alpn.len() > 0) {
    ee = ee.append(message.ext_alpn([alpn]));
  }
  for extra in s.config.extensions {
    ee = ee.append(extra);
  }
  return message.encode(message.EncryptedExtensions(ee));
}

function __server_flight(s: Server, alpn: string): u8[] {
  let out: u8[] = __encrypted_extensions_msg(s, alpn);
  if (s.config.request_client_certificate) {
    out = __cat(out, message.encode(message.CertificateRequestMsg(message.CertificateRequest { context: [], extensions: [message.ext_signature_algorithms(VERIFY_SCHEMES)] })));
  }
  let entries: message.CertificateEntry[] = [];
  let i: i32 = 0;
  while (i < s.config.chain.len()) {
    entries = entries.append(message.CertificateEntry { data: s.config.chain[i], extensions: [] });
    i = i + 1;
  }
  return __cat(out, message.encode(message.CertificateMsg(message.Certificate { context: [], entries: entries })));
}
```

### `58 struct Negotiated`

```fern
<<58 struct Negotiated>>=
struct Negotiated { suite: keyschedule.CipherSuite, scheme: i32, choice: Choice, alpn: string, name: string }
```

### `59 function __negotiate(s: Server, h: message.ClientHello): Result[Negotiated, HandshakeError]`

```fern
<<59 function __negotiate(s: Server, h: message.ClientHello): Result[Negotiated, HandshakeError]>>=
function __negotiate(s: Server, h: message.ClientHello): Result[Negotiated, HandshakeError] {
  __offers_tls13(h)?;
  return Ok(Negotiated { suite: __pick_suite(s, h)?, scheme: __pick_scheme(s, h)?, choice: __choose_share(s, h)?, alpn: __choose_alpn(s, h)?, name: __server_name(h)? });
}

function __negotiated_terms(s: Server, h: message.ClientHello): Result[Negotiated, HandshakeError] {
  let n: Negotiated = __negotiate(s, h)?;
  if (s.conn.quic) {
    __quic_terms(h.extensions, n.alpn)?;
  }
  return Ok(n);
}

function __client_hello(first: Server, raw: message.Raw, out: Out): Result[ServerRun, HandshakeError] {
  let h: message.ClientHello = __as_client_hello(raw)?;
  let s: Server = Server { ...first, peer_extensions: h.extensions };
  let n: Negotiated = __negotiated_terms(s, h)?;
  if (s.retry_group != 0) {
    __check_second_hello(s, n)?;
  } else if (n.choice.retry) {
    return __retry_request(s, raw, h, n, out);
  }
  match (__offered_psks(h)?) {
    Some(offer) => {
      let ev: Event = ResumeOffered(Offer { identities: offer.identities, suite: n.suite, server_name: n.name });
      return Ok(ServerRun { server: Server { ...s, state: S_WAIT_RESUME, parked: raw }, out: Out { ...out, events: out.events.append(ev) } });
    },
    None => {}
  }
  return __serve_hello(s, raw, h, n, [], 0, out);
}

function __offered_psks(h: message.ClientHello): Result[Option[message.OfferedPsks], HandshakeError] {
  match (message.find(h.extensions, message.EXT_PRE_SHARED_KEY)) {
    Some(d) => {
      if (h.extensions[h.extensions.len() - 1].kind != message.EXT_PRE_SHARED_KEY) {
        return Err(Illegal("pre_shared_key is not the last extension"));
      }
      let modes: i32[] = __codes(message.psk_modes(__ext(h.extensions, message.EXT_PSK_KEY_EXCHANGE_MODES, "psk_key_exchange_modes")?))?;
      if (!__has(modes, 1)) {
        return Err(HandshakeFailure("no psk_dhe_ke"));
      }
      match (message.pre_shared_key(d)) {
        Ok(o) => {
          return Ok(Some(o));
        },
        Err(e) => {
          return Err(Message(e));
        }
      }
    },
    None => {
      return Ok(None);
    }
  }
  return Ok(None);
}

function __serve_hello(s: Server, raw: message.Raw, h: message.ClientHello, n: Negotiated, psk: [u8], index: i32, out: Out): Result[ServerRun, HandshakeError] {
  let suite: keyschedule.CipherSuite = n.suite;
  let share: keyshare.ServerShare = __answer_share(s, n.choice)?;
  let exts: message.Extension[] = [message.ext_key_share(message.KeyShareEntry { group: n.choice.group.code(), key: share.public }), message.ext_selected_version(message.TLS13)];
  if (psk.len() > 0) {
    exts = exts.append(message.ext_selected_psk(index));
  }
  let sh: message.ServerHello = message.ServerHello { random: __copy(s.entropy[0:32]), session_id: h.session_id, cipher_suite: suite.code(), extensions: exts };
  let sh_bytes: u8[] = message.encode(message.ServerHelloMsg(sh));
  let conn: Conn = Conn { ...s.conn, suite: suite, transcript: __cat(__cat(__copy(s.conn.transcript), raw.bytes), sh_bytes) };
  __at_key_change(conn)?;
  let plain: Wrote = __plain_out(conn, 0x0303, sh_bytes);
  conn = plain.conn;
  let output: u8[] = __join(out.output, plain.bytes);
  if (s.retry_group == 0 && !conn.quic) {
    output = __compat_ccs(output, h);
  }
  let hs: keyschedule.HandshakeSecret = keyschedule.early_secret(suite.hash(), psk).handshake_secret(share.shared_secret);
  let th: u8[] = __hash(conn);
  let client_hs: u8[] = hs.client_traffic(th);
  let server_hs: u8[] = hs.server_traffic(th);
  conn = __with_write(__with_read(conn, client_hs, 1), server_hs, 1);
  let next: Server = Server { ...s, conn: conn, scheme: n.scheme, hs: hs, client_hs: client_hs, server_hs: server_hs, server_name: n.name, alpn: n.alpn, resumed: psk.len() > 0 };
  let sent: Out = Out { ...out, output: output };
  if (psk.len() > 0) {
    return __send_finished(next, [], __encrypted_extensions_msg(next, n.alpn), sent);
  }
  let flight: u8[] = __server_flight(s, n.alpn);
  conn = Conn { ...conn, transcript: __join(conn.transcript, flight) };
  let ev: Event = SignNeeded(Signing { scheme: n.scheme, signed: certificate_verify_content(true, __hash(conn)) });
  return Ok(ServerRun { server: Server { ...next, conn: conn, state: S_WAIT_SIGNATURE, flight: flight }, out: Out { ...sent, events: sent.events.append(ev) } });
}

function __send_finished(s: Server, before: [u8], tail: [u8], out: Out): Result[ServerRun, HandshakeError] {
  let h: keyschedule.Hash = s.conn.suite.hash();
  let conn: Conn = Conn { ...s.conn, transcript: __join(s.conn.transcript, tail) };
  let fin: u8[] = message.encode(message.Finished(keyschedule.finished_verify_data(h, s.server_hs, __hash(conn))));
  conn = Conn { ...conn, transcript: __join(conn.transcript, fin) };
  let w: Wrote = __write(conn, record.Handshake, __cat(__cat(__copy(before), tail), fin))?;
  let master: keyschedule.MasterSecret = s.hs.master_secret();
  let finished_hash: u8[] = __hash(w.conn);
  let next: Conn = __with_write(w.conn, master.server_traffic(finished_hash), 2);
  let state: i32 = S_WAIT_FINISHED;
  if (s.config.request_client_certificate && !s.resumed) {
    state = S_WAIT_CLIENT_CERTIFICATE;
  }
  return Ok(ServerRun { server: Server { ...s, conn: next, state: state, flight: [], finished_hash: finished_hash }, out: Out { ...out, output: __join(out.output, w.bytes) } });
}

function __check_binder(s: Server, raw: message.Raw, h: message.ClientHello, suite: keyschedule.CipherSuite, c: Resumed): Result[boolean, HandshakeError] {
  let offer: message.OfferedPsks = message.OfferedPsks { identities: [], binders: [] };
  match (__offered_psks(h)?) {
    Some(o) => {
      offer = o;
    },
    None => {
      return Err(Misuse("resume"));
    }
  }
  if (c.index < 0 || c.index >= offer.identities.len()) {
    return Err(Misuse("resume"));
  }
  let truncated: [u8] = raw.bytes[0:raw.bytes.len() - message.binders_length(offer.binders)];
  if (!crypto.consteq(__binder(suite.hash(), c.psk, s.conn.transcript, truncated), offer.binders[c.index])) {
    return Err(DecryptError);
  }
  return Ok(true);
}

function __compat_ccs(own output: u8[], h: message.ClientHello): u8[] {
  if (h.session_id.len() > 0) {
    return __cat(output, [20 as u8, 3 as u8, 3 as u8, 0 as u8, 1 as u8, 1 as u8]);
  }
  return output;
}
```

### `60 function __retry_request(s: Server, raw: message.Raw, h: message.ClientHello, n: Negotiated, out: Out): Res...`

```fern
<<60 function __retry_request(s: Server, raw: message.Raw, h: message.ClientHello, n: Negotiated, out: Out): Res...>>=
function __retry_request(s: Server, raw: message.Raw, h: message.ClientHello, n: Negotiated, out: Out): Result[ServerRun, HandshakeError] {
  let hrr: message.ServerHello = message.ServerHello { random: message.retry_random(), session_id: h.session_id, cipher_suite: n.suite.code(), extensions: [message.ext_selected_version(message.TLS13), message.ext_retry_group(n.choice.group.code())] };
  let hrr_bytes: u8[] = message.encode(message.ServerHelloMsg(hrr));
  let digest: u8[] = n.suite.hash().digest(raw.bytes);
  let conn: Conn = Conn { ...s.conn, transcript: __cat(__cat([254 as u8, 0 as u8, 0 as u8, digest.len() as u8], digest), hrr_bytes) };
  __at_key_change(conn)?;
  let plain: Wrote = __plain_out(conn, 0x0303, hrr_bytes);
  let output: u8[] = __join(out.output, plain.bytes);
  if (!conn.quic) {
    output = __compat_ccs(output, h);
  }
  return Ok(ServerRun { server: Server { ...s, conn: plain.conn, retry_group: n.choice.group.code(), retry_suite: n.suite.code() }, out: Out { ...out, output: output } });
}

function __check_second_hello(s: Server, n: Negotiated): Result[boolean, HandshakeError] {
  if (n.choice.retry || n.choice.group.code() != s.retry_group) {
    return Err(Illegal("second ClientHello without the share asked for"));
  }
  if (n.suite.code() != s.retry_suite) {
    return Err(Illegal("second ClientHello changes the cipher suite"));
  }
  return Ok(true);
}

function __client_finished(s: Server, raw: message.Raw, verify_data: u8[], out: Out): Result[ServerRun, HandshakeError] {
  let h: keyschedule.Hash = s.conn.suite.hash();
  if (!keyschedule.verify_finished(h, s.client_hs, __hash(s.conn), verify_data)) {
    return Err(DecryptError);
  }
  let conn: Conn = Conn { ...s.conn, transcript: __join(s.conn.transcript, raw.bytes) };
  __at_key_change(conn)?;
  let master: keyschedule.MasterSecret = s.hs.master_secret();
  conn = __with_read(conn, master.client_traffic(s.finished_hash), 2);
  return Ok(ServerRun { server: Server { ...s, conn: conn, state: S_CONNECTED, res_master: master.resumption(__hash(conn)) }, out: Out { ...out, events: out.events.append(Connected) } });
}

function __client_certificate(s: Server, raw: message.Raw, cert: message.Certificate, out: Out): Result[ServerRun, HandshakeError] {
  if (cert.context.len() != 0) {
    return Err(Illegal("certificate_request_context"));
  }
  let conn: Conn = Conn { ...s.conn, transcript: __join(s.conn.transcript, raw.bytes) };
  if (cert.entries.len() == 0) {
    let ev: Event = VerifyClient(Verification { chain: [], scheme: 0, signed: [], signature: [] });
    return Ok(ServerRun { server: Server { ...s, conn: conn, state: S_WAIT_CLIENT_VERDICT }, out: Out { ...out, events: out.events.append(ev) } });
  }
  let chain: u8[][] = [];
  for e in cert.entries {
    chain = chain.append(e.data);
  }
  return Ok(ServerRun { server: Server { ...s, conn: conn, state: S_WAIT_CLIENT_VERIFY, client_chain: chain }, out: out });
}

function __client_verify(s: Server, raw: message.Raw, v: message.CertificateVerify, out: Out): Result[ServerRun, HandshakeError] {
  if (!__has(VERIFY_SCHEMES, v.scheme)) {
    return Err(Illegal("signature scheme"));
  }
  let signed: u8[] = certificate_verify_content(false, __hash(s.conn));
  let conn: Conn = Conn { ...s.conn, transcript: __join(s.conn.transcript, raw.bytes) };
  let ev: Event = VerifyClient(Verification { chain: s.client_chain, scheme: v.scheme, signed: signed, signature: v.signature });
  return Ok(ServerRun { server: Server { ...s, conn: conn, state: S_WAIT_CLIENT_VERDICT }, out: Out { ...out, events: out.events.append(ev) } });
}

function __server_message(s: Server, raw: message.Raw, out: Out): Result[ServerRun, HandshakeError] {
  if (s.state == S_WAIT_CLIENT_HELLO) {
    return __client_hello(s, raw, out);
  }
  let m: message.Message = __decode(raw)?;
  if (s.state == S_WAIT_CLIENT_CERTIFICATE || s.state == S_WAIT_CLIENT_VERIFY) {
    return __client_auth_message(s, raw, m, out);
  }
  match (m) {
    message.Finished(verify_data) => {
      if (s.state == S_WAIT_FINISHED) {
        return __client_finished(s, raw, verify_data, out);
      }
    },
    message.KeyUpdate(request) => {
      if (s.state == S_CONNECTED) {
        let a: Absorbed = __key_update(s.conn, request, out)?;
        return Ok(ServerRun { server: Server { ...s, conn: a.conn }, out: a.out });
      }
    },
    _ => {
      return Err(OutOfOrder(raw.kind));
    }
  }
  return Err(OutOfOrder(raw.kind));
}

function __client_auth_message(s: Server, raw: message.Raw, m: message.Message, out: Out): Result[ServerRun, HandshakeError] {
  match (m) {
    message.CertificateMsg(cert) => {
      if (s.state == S_WAIT_CLIENT_CERTIFICATE) {
        return __client_certificate(s, raw, cert, out);
      }
    },
    message.CertificateVerifyMsg(v) => {
      if (s.state == S_WAIT_CLIENT_VERIFY) {
        return __client_verify(s, raw, v, out);
      }
    },
    _ => {}
  }
  return Err(OutOfOrder(raw.kind));
}

function __server_parked(state: i32): boolean {
  return state == S_WAIT_SIGNATURE || state == S_WAIT_RESUME || state == S_WAIT_CLIENT_VERDICT || state == S_CLOSED;
}

function __client_flight_due(state: i32): boolean {
  return state == S_WAIT_FINISHED || state == S_WAIT_CLIENT_CERTIFICATE || state == S_WAIT_CLIENT_VERIFY;
}

function __server_drain(s: Server, out: Out): Result[ServerRun, HandshakeError] {
  let server: Server = s;
  while (!__server_parked(server.state)) {
    let n: Next = __next(server.conn)?;
    if (!n.has) {
      break;
    }
    let r: ServerRun = __server_message(Server { ...server, conn: n.conn }, n.raw, out)?;
    server = r.server;
    out = r.out;
  }
  return Ok(ServerRun { server: server, out: out });
}
```

### `61 function __server_run(s: Server, out: Out): Result[ServerStep, HandshakeError]`

```fern
<<61 function __server_run(s: Server, out: Out): Result[ServerStep, HandshakeError]>>=
function __server_run(s: Server, out: Out): Result[ServerStep, HandshakeError] {
  let records: record.Record[] = s.conn.held;
  let r: ServerRun = __server_drain(s, out)?;
  let i: i32 = 0;
  while (!__server_parked(r.server.state) && i < records.len()) {
    let ccs: boolean = __client_flight_due(r.server.state) || r.server.state == S_WAIT_CLIENT_HELLO && r.server.retry_group != 0;
    let a: Absorbed = __absorb(r.server.conn, records[i], ccs, r.server.state == S_CONNECTED, r.out)?;
    let state: i32 = r.server.state;
    if (a.out.closed) {
      state = S_CLOSED;
    }
    r = __server_drain(Server { ...r.server, conn: a.conn, state: state }, a.out)?;
    i = i + 1;
  }
  let server: Server = Server { ...r.server, conn: Conn { ...r.server.conn, held: __rest(records, i) } };
  return Ok(__server_step(server, r.out));
}
```

### `62 pub function (s: Server) read(input: [u8]): Result[ServerStep, HandshakeError]`

```fern
<<62 pub function (s: Server) read(input: [u8]): Result[ServerStep, HandshakeError]>>=
pub function (s: Server) read(input: [u8]): Result[ServerStep, HandshakeError] {
  if (s.state == S_CLOSED) {
    return Err(Misuse("read after close"));
  }
  if (s.conn.quic) {
    return Err(Misuse("read under QUIC"));
  }
  return __server_run(Server { ...s, conn: __deframe(s.conn, input)? }, __out());
}

pub function (s: Server) read_crypto(level: i32, bytes: [u8]): Result[ServerStep, HandshakeError] {
  if (s.state == S_CLOSED) {
    return Err(Misuse("read_crypto after close"));
  }
  if (!s.conn.quic) {
    return Err(Misuse("read_crypto"));
  }
  if (level != s.conn.reading) {
    return Err(Level(level));
  }
  return __server_run(Server { ...s, conn: Conn { ...s.conn, pending: __join(s.conn.pending, bytes) } }, __out());
}

pub function (s: Server) peer_extensions(): message.Extension[] {
  return s.peer_extensions;
}

pub function (s: Server) signature(sig: [u8]): Result[ServerStep, HandshakeError] {
  if (s.state != S_WAIT_SIGNATURE) {
    return Err(Misuse("signature"));
  }
  let cv: u8[] = message.encode(message.CertificateVerifyMsg(message.CertificateVerify { scheme: s.scheme, signature: __copy(sig) }));
  let r: ServerRun = __send_finished(s, s.flight, cv, __out())?;
  return __server_run(r.server, r.out);
}

pub function (s: Server) resume(choice: Option[Resumed]): Result[ServerStep, HandshakeError] {
  if (s.state != S_WAIT_RESUME) {
    return Err(Misuse("resume"));
  }
  let raw: message.Raw = s.parked;
  let h: message.ClientHello = __as_client_hello(raw)?;
  let n: Negotiated = __negotiate(s, h)?;
  let server: Server = Server { ...s, parked: message.Raw { kind: 0, bytes: [] } };
  match (choice) {
    Some(c) => {
      __check_binder(s, raw, h, n.suite, c)?;
      let r: ServerRun = __serve_hello(server, raw, h, n, c.psk, c.index, __out())?;
      return __server_run(r.server, r.out);
    },
    None => {
      let r: ServerRun = __serve_hello(server, raw, h, n, [], 0, __out())?;
      return __server_run(r.server, r.out);
    }
  }
  return Err(Misuse("resume"));
}

pub function (s: Server) verdict(ok: boolean): Result[ServerStep, HandshakeError] {
  if (s.state != S_WAIT_CLIENT_VERDICT) {
    return Err(Misuse("verdict"));
  }
  if (!ok) {
    return Err(BadCertificate);
  }
  return __server_run(Server { ...s, state: S_WAIT_FINISHED }, __out());
}

pub function (s: Server) client_chain(): u8[][] {
  return s.client_chain;
}

pub function (s: Server) ticket(t: message.NewSessionTicket): Result[ServerStep, HandshakeError] {
  if (s.state != S_CONNECTED) {
    return Err(Misuse("ticket"));
  }
  let w: Wrote = __write(s.conn, record.Handshake, message.encode(message.NewSessionTicketMsg(t)))?;
  return Ok(__server_step(Server { ...s, conn: w.conn }, Out { ...__out(), output: w.bytes }));
}

pub function (s: Server) resumed(): boolean {
  return s.resumed;
}

pub function (s: Server) cipher_suite(): keyschedule.CipherSuite {
  return s.conn.suite;
}

pub function (s: Server) resumption_psk(nonce: [u8]): Result[u8[], HandshakeError] {
  if (s.res_master.len() == 0) {
    return Err(Misuse("resumption_psk"));
  }
  return Ok(keyschedule.resumption_psk(s.conn.suite.hash(), s.res_master, nonce));
}

pub function (s: Server) write(data: [u8]): Result[ServerStep, HandshakeError] {
  if (s.state != S_CONNECTED) {
    return Err(Misuse("write"));
  }
  let w: Wrote = __write(s.conn, record.ApplicationData, data)?;
  return Ok(__server_step(Server { ...s, conn: w.conn }, Out { ...__out(), output: w.bytes }));
}

pub function (s: Server) close(): Result[ServerStep, HandshakeError] {
  let w: Wrote = __write(s.conn, record.Alert, [1 as u8, 0 as u8])?;
  return Ok(__server_step(Server { ...s, conn: w.conn, state: S_CLOSED }, Out { ...__out(), output: w.bytes }));
}

pub function (s: Server) alert(e: HandshakeError): u8[] {
  return __alert_record(s.conn, e);
}

pub function (s: Server) connected(): boolean {
  return s.state == S_CONNECTED;
}

pub function (s: Server) update_keys(request: boolean): Result[ServerStep, HandshakeError] {
  if (s.state != S_CONNECTED) {
    return Err(Misuse("update_keys"));
  }
  let w: Wrote = __update(s.conn, request)?;
  return Ok(__server_step(Server { ...s, conn: w.conn }, Out { ...__out(), output: w.bytes }));
}

pub function (s: Server) offloaded(): boolean {
  return s.conn.kernel;
}
```

### `63 pub struct ServerOffload`

```fern
<<63 pub struct ServerOffload>>=
pub struct ServerOffload { server: Server, keys: KernelKeys }
```

### `64 pub function (s: Server) offload(): Option[ServerOffload]`

```fern
<<64 pub function (s: Server) offload(): Option[ServerOffload]>>=
pub function (s: Server) offload(): Option[ServerOffload] {
  if (s.state != S_CONNECTED) {
    return None;
  }
  match (__offload(s.conn)) {
    Some(o) => {
      return Some(ServerOffload { server: Server { ...s, conn: o.conn }, keys: o.keys });
    },
    None => {}
  }
  return None;
}
```
