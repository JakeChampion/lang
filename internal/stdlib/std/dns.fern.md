# DNS messages and resolution

This module resolves DNS names through host files, UDP, and TCP.

## Model

DNS records retain their resource-data kind. Packet helpers encode and decode messages, while resolver functions coordinate host files and network exchanges.

## Contracts

UDP and TCP use different message frame rules. The TCP path must send the complete length-prefixed query. Network operations return concrete DNS errors.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum DnsError>>

<<4 pub function (e: DnsError) message(): string>>

<<5 impl error.Error for DnsError>>

<<6 pub function (e: DnsError) to_string(): string>>

<<7 pub enum RData>>

<<8 pub function query(id: i32, name: string, qtype: i32): Message>>

<<9 function put16(b: u8[], v: i32): u8[]>>

<<10 pub function encode_name(name: string): Result[u8[], DnsError]>>

<<11 function encode_question(b: u8[], q: Question): Result[u8[], DnsError]>>

<<12 pub function encode(m: Message): Result[u8[], DnsError]>>

<<13 function sections_fit(m: Message): boolean>>

<<14 pub struct NameAt>>

<<15 pub function decode_name(msg: u8[], pos: i32): Result[NameAt, DnsError]>>

<<16 function first_of(next: i32, at: i32): i32>>

<<17 struct QuestionAt>>

<<18 function decode_question(msg: u8[], pos: i32): Result[QuestionAt, DnsError]>>

<<19 struct RecordAt>>

<<20 function decode_rdata(msg: u8[], rtype: i32, rclass: i32, at: i32, rdlen: i32): Result[RData, DnsError]>>

<<21 struct RecordsAt>>

<<22 function decode_records(msg: u8[], pos: i32, count: i32): Result[RecordsAt, DnsError]>>

<<23 pub function decode(msg: u8[]): Result[Message, DnsError]>>

<<24 function strip_root(n: string): string>>

<<25 pub function (m: Message) answers_question(q: Question): boolean>>

<<26 pub struct Answer>>

<<27 pub function records_for(m: Message, name: string, rtype: i32): Answer>>

<<28 struct OwnerScan>>

<<29 function scan_owner(m: Message, owner: string, rtype: i32): OwnerScan>>

<<30 pub function addresses_of(records: Record[]): net.IpAddr[]>>

<<31 pub struct HostsEntry>>

<<32 pub function parse_hosts(contents: string): HostsEntry[]>>

<<33 function entry_names(e: HostsEntry, name: string): boolean>>

<<34 pub struct ResolvConf>>

<<35 pub const PORT: i32>>

<<36 function with_nameserver(ns: net.SocketAddr[], text: string): net.SocketAddr[]>>

<<37 pub struct SearchPlan>>

<<38 pub function search_plan(name: string, rc: ResolvConf): SearchPlan>>

<<39 function zero_bytes(n: i32): u8[]>>

<<40 pub function exchange(ns: net.SocketAddr, q: Message, timeout: Duration): Result[Message, DnsError]>>

<<41 struct Slot>>

<<42 function waiting(s: Slot): boolean>>

<<43 function take_reply(sock: Socket, q: Message): Result[Option[Message], DnsError]>>

<<44 pub function exchange_tcp(ns: net.SocketAddr, q: Message, timeout: Duration): Result[Message, DnsError]>>

<<45 function send_tcp(ns: net.SocketAddr, framed: u8[]): Result[Socket, DnsError]>>

<<46 pub function ask(conf: ResolvConf, q: Message): Result[Message, DnsError]>>

<<47 struct Asked>>

<<48 function asked_result(a: Asked): Result[Message, DnsError]>>

<<49 struct Tally>>

<<50 function noted_rcode(t: Tally, rcode: i32): Tally>>

<<51 pub function answer_records(reply: Message, qname: string, rtype: i32): Result[Answer, DnsError]>>

<<52 function search_failure(lead_failed: boolean, lead_err: DnsError, servfail: boolean, last: DnsError): DnsError>>

<<53 struct Suffixes>>

<<54 function search_suffixes(suffixes: string[], asker: (string)>>

<<55 pub function lookup_records(conf: ResolvConf, name: string, rtype: i32): Result[Answer, DnsError]>>

<<56 function ask_one(conf: ResolvConf, qname: string, rtype: i32): Result[Answer, DnsError]>>

<<57 pub function lookup_a(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError]>>

<<58 function found_addresses(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError]>>

<<59 pub function combine_pair(a: Result[Answer, DnsError], aaaa: Result[Answer, DnsError]): Result[Answer, DnsE...>>

<<60 function pair_failure(ea: DnsError, e6: DnsError): DnsError>>

<<61 pub struct Policy>>

<<62 pub function policy_of(ip: net.IpAddr): Policy>>

<<63 function policy_embedded(b: u8[]): Policy>>

<<64 pub function scope_of(ip: net.IpAddr): i32>>

<<65 function scope_v4(b: u8[]): i32>>

<<66 pub function common_prefix_len(a: net.IpAddr, b: net.IpAddr): i32>>

<<67 function sort_by(dsts: net.IpAddr[], src: (net.IpAddr)>>

<<68 pub struct DialOptions>>

<<69 pub function dial_options(): DialOptions>>


<<71 pub function connect_race(addrs: net.IpAddr[], port: i32, opts: DialOptions): Result[Socket, net.NetError]>>

<<72 function wait_window(race: Race, n: i32, now: i64, deadline: i64): i32>>

<<73 struct Race>>

<<74 function writable(open: Option[Socket][]): i32[]>>

<<75 pub function dial(name: string, port: i32, opts: DialOptions): Result[Socket, DnsError]>>

<<76 pub struct Nat64Prefix>>

<<77 pub function nat64_prefix_of(aaaa: net.IpAddr): Option[Nat64Prefix]>>

<<78 function is_well_known(v4: u8[]): boolean>>

<<79 pub function synthesize(prefix: Nat64Prefix, v4: net.IpAddr): net.IpAddr>>

<<80 function carried_by(addr: net.IpAddr, prefixes: ()>>

<<81 pub function system_conf(): ResolvConf>>

<<82 function reachable(conf: ResolvConf, addrs: net.IpAddr[]): net.IpAddr[]>>

<<83 pub struct Resolver>>

<<84 pub function resolver(): Resolver>>

<<85 function (r: Resolver) fresh(): Seen>>

<<86 pub function (r: Resolver) conf(): ResolvConf>>

<<87 function (r: Resolver) prefixes(): Nat64Prefix[]>>

<<88 pub function (r: Resolver) resolve_plain(name: string): Result[net.IpAddr[], DnsError]>>

<<89 function resolve_with(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module resolves DNS names through host files, UDP, and TCP.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/net";
import "std/time";
import "std/async";
import "std/i32";
import "std/string";
import "std/array";
import "core/error";
import "std/utf8" as utf8;
import "core/cmp";
```

### `3 pub enum DnsError`

```fern
<<3 pub enum DnsError>>=
pub enum DnsError {
  NoSuchName,
  NoData,
  NoReply,
  ServerFailure,
  Refused,
  Malformed,
  NoNameserver,
  NetFailed(net.NetError),
}

impl cmp.Display for DnsError {}
```

### `4 pub function (e: DnsError) message(): string`

```fern
<<4 pub function (e: DnsError) message(): string>>=
pub function (e: DnsError) message(): string {
  match (e) {
    NoSuchName => {
      return "Name or service not known";
    },
    NoData => {
      return "No address associated with hostname";
    },
    NoReply => {
      return "Temporary failure in name resolution";
    },
    ServerFailure => {
      return "Name server failed";
    },
    Refused => {
      return "Name server refused the query";
    },
    Malformed => {
      return "Malformed DNS message";
    },
    NoNameserver => {
      return "No name server configured";
    },
    NetFailed(ne) => {
      return ne.message();
    }
  }
  return "";
}
```

### `5 impl error.Error for DnsError`

```fern
<<5 impl error.Error for DnsError>>=
impl error.Error for DnsError {}
```

### `6 pub function (e: DnsError) to_string(): string`

```fern
<<6 pub function (e: DnsError) to_string(): string>>=
pub function (e: DnsError) to_string(): string {
  return e.message();
}

pub const TYPE_A: i32 = 1;

pub const TYPE_CNAME: i32 = 5;

pub const TYPE_PTR: i32 = 12;

pub const TYPE_AAAA: i32 = 28;

pub const TYPE_OPT: i32 = 41;

pub const CLASS_IN: i32 = 1;

pub const RCODE_OK: i32 = 0;

pub const RCODE_FORMAT_ERROR: i32 = 1;

pub const RCODE_SERVER_FAILURE: i32 = 2;

pub const RCODE_NAME_ERROR: i32 = 3;

pub const RCODE_NOT_IMPLEMENTED: i32 = 4;

pub const RCODE_REFUSED: i32 = 5;
```

### `7 pub enum RData`

```fern
<<7 pub enum RData>>=
pub enum RData {
  A(net.IpAddr),
  AAAA(net.IpAddr),
  CNAME(string),
  PTR(string),
  Raw(u8[]),
}

pub struct Question { name: string, qtype: i32, qclass: i32 }

pub struct Record { name: string, rtype: i32, rclass: i32, ttl: i32, data: RData }

pub struct Message {
  id: i32,
  response: boolean,
  opcode: i32,
  authoritative: boolean,
  truncated: boolean,
  recursion_desired: boolean,
  recursion_available: boolean,
  rcode: i32,
  questions: Question[],
  answers: Record[],
  authority: Record[],
  additional: Record[],
}
```

### `8 pub function query(id: i32, name: string, qtype: i32): Message`

```fern
<<8 pub function query(id: i32, name: string, qtype: i32): Message>>=
pub function query(id: i32, name: string, qtype: i32): Message {
  let qs: Question[] = [Question { name: name, qtype: qtype, qclass: CLASS_IN }];
  return Message {
    id: id & 65535, response: false, opcode: 0, authoritative: false, truncated: false,
    recursion_desired: true, recursion_available: false, rcode: 0,
    questions: qs, answers: [], authority: [], additional: [],
  };
}

pub const UDP_PAYLOAD: i32 = 4096;

pub function with_edns(m: Message): Message {
  let none: u8[] = [];
  let opt = Record { name: "", rtype: TYPE_OPT, rclass: UDP_PAYLOAD, ttl: 0, data: Raw(none) };
  return Message { ...m, additional: m.additional.append(opt) };
}

pub function rcode_name(rcode: i32): string {
  if (rcode == 0) {
    return "NOERROR";
  }
  if (rcode == 1) {
    return "FORMERR";
  }
  if (rcode == 2) {
    return "SERVFAIL";
  }
  if (rcode == 3) {
    return "NXDOMAIN";
  }
  if (rcode == 4) {
    return "NOTIMP";
  }
  if (rcode == 5) {
    return "REFUSED";
  }
  return "RCODE" + rcode.to_string();
}
```

### `9 function put16(b: u8[], v: i32): u8[]`

```fern
<<9 function put16(b: u8[], v: i32): u8[]>>=
function put16(b: u8[], v: i32): u8[] {
  let out = b.append((v >> 8 & 255) as u8);
  return out.append((v & 255) as u8);
}

function put32(b: u8[], v: i32): u8[] {
  let out = put16(b, v >> 16 & 65535);
  return put16(out, v & 65535);
}
```

### `10 pub function encode_name(name: string): Result[u8[], DnsError]`

```fern
<<10 pub function encode_name(name: string): Result[u8[], DnsError]>>=
pub function encode_name(name: string): Result[u8[], DnsError] {
  let out: u8[] = [];
  let n = name;
  if (n.ends_with(".")) {
    n = slice_unchecked(n, 0, n.len() - 1) + "";
  }
  if (n.len() == 0) {
    return Ok(out.append(0 as u8));
  }
  let labels = n.split(".");
  let i = 0;
  while (i < labels.len()) {
    let l = labels[i];
    if (l.len() == 0 || l.len() > 63) {
      return Err(Malformed);
    }
    out = out.append(l.len() as u8);
    let j = 0;
    while (j < l.len()) {
      out = out.append(l[j]);
      j += 1;
    }
    i += 1;
  }
  out = out.append(0 as u8);
  if (out.len() > 255) {
    return Err(Malformed);
  }
  return Ok(out);
}
```

### `11 function encode_question(b: u8[], q: Question): Result[u8[], DnsError]`

```fern
<<11 function encode_question(b: u8[], q: Question): Result[u8[], DnsError]>>=
function encode_question(b: u8[], q: Question): Result[u8[], DnsError] {
  let name = encode_name(q.name)?;
  let out = b.concat(name);
  out = put16(out, q.qtype);
  return Ok(put16(out, q.qclass));
}

function encode_rdata(d: RData): Result[u8[], DnsError] {
  match (d) {
    A(ip) => {
      if (!ip.is_v4()) {
        return Err(Malformed);
      }
      return Ok(ip.bytes());
    },
    AAAA(ip) => {
      if (!ip.is_v6()) {
        return Err(Malformed);
      }
      return Ok(ip.bytes());
    },
    CNAME(n) => {
      return encode_name(n);
    },
    PTR(n) => {
      return encode_name(n);
    },
    Raw(bytes) => {
      return Ok(bytes);
    }
  }
  let none: u8[] = [];
  return Ok(none);
}

function encode_record(b: u8[], r: Record): Result[u8[], DnsError] {
  let name = encode_name(r.name)?;
  let data = encode_rdata(r.data)?;
  if (data.len() > 65535) {
    return Err(Malformed);
  }
  let out = b.concat(name);
  out = put16(out, r.rtype);
  out = put16(out, r.rclass);
  out = put32(out, r.ttl);
  out = put16(out, data.len());
  return Ok(out.concat(data));
}

function encode_records(b: u8[], rs: Record[]): Result[u8[], DnsError] {
  let out = b;
  let i = 0;
  while (i < rs.len()) {
    out = encode_record(out, rs[i])?;
    i += 1;
  }
  return Ok(out);
}
```

### `12 pub function encode(m: Message): Result[u8[], DnsError]`

```fern
<<12 pub function encode(m: Message): Result[u8[], DnsError]>>=
pub function encode(m: Message): Result[u8[], DnsError] {
  if (!sections_fit(m)) {
    return Err(Malformed);
  }
  let out: u8[] = [];
  out = put16(out, m.id & 65535);
  out = put16(out, header_flags(m));
  out = put16(out, m.questions.len());
  out = put16(out, m.answers.len());
  out = put16(out, m.authority.len());
  out = put16(out, m.additional.len());
  let i = 0;
  while (i < m.questions.len()) {
    out = encode_question(out, m.questions[i])?;
    i += 1;
  }
  out = encode_records(out, m.answers)?;
  out = encode_records(out, m.authority)?;
  return encode_records(out, m.additional);
}
```

### `13 function sections_fit(m: Message): boolean`

```fern
<<13 function sections_fit(m: Message): boolean>>=
function sections_fit(m: Message): boolean {
  return m.questions.len() <= 65535 && m.answers.len() <= 65535 && m.authority.len() <= 65535 && m.additional.len() <= 65535;
}

function header_flags(m: Message): i32 {
  let flags = (m.opcode & 15) << 11 | m.rcode & 15;
  if (m.response) {
    flags = flags | 32768;
  }
  if (m.authoritative) {
    flags = flags | 1024;
  }
  if (m.truncated) {
    flags = flags | 512;
  }
  if (m.recursion_desired) {
    flags = flags | 256;
  }
  if (m.recursion_available) {
    flags = flags | 128;
  }
  return flags;
}

function be16(m: u8[], i: i32): i32 {
  return m[i] as i32 << 8 | m[i + 1] as i32;
}

function be32(m: u8[], i: i32): i32 {
  return be16(m, i) << 16 | be16(m, i + 2);
}
```

### `14 pub struct NameAt`

```fern
<<14 pub struct NameAt>>=
pub struct NameAt { name: string, next: i32 }
```

### `15 pub function decode_name(msg: u8[], pos: i32): Result[NameAt, DnsError]`

```fern
<<15 pub function decode_name(msg: u8[], pos: i32): Result[NameAt, DnsError]>>=
pub function decode_name(msg: u8[], pos: i32): Result[NameAt, DnsError] {
  let out = "";
  let total = 0;
  let p = pos;
  let next = -1;
  let hops = 0;
  while (p < msg.len()) {
    let l = msg[p] as i32;
    if (l == 0) {
      return Ok(NameAt { name: out, next: first_of(next, p + 1) });
    }
    if ((l & 192) == 192) {
      next = first_of(next, p + 2);
      hops += 1;
      p = pointer_target(msg, p, hops)?;
      continue;
    }
    total = total + 1 + l;
    if ((l & 192) != 0 || p + 1 + l > msg.len() || total > 254) {
      return Err(Malformed);
    }
    match (join_label(out, msg, p + 1, l)) {
      Some(joined) => {
        out = joined;
      },
      None => {
        return Err(Malformed);
      }
    }
    p = p + 1 + l;
  }
  return Err(Malformed);
}
```

### `16 function first_of(next: i32, at: i32): i32`

```fern
<<16 function first_of(next: i32, at: i32): i32>>=
function first_of(next: i32, at: i32): i32 {
  if (next < 0) {
    return at;
  }
  return next;
}

function pointer_target(msg: u8[], p: i32, hops: i32): Result[i32, DnsError] {
  if (p + 1 >= msg.len()) {
    return Err(Malformed);
  }
  let target = (msg[p] as i32 & 63) << 8 | msg[p + 1] as i32;
  if (target >= p || hops > 127) {
    return Err(Malformed);
  }
  return Ok(target);
}

function join_label(out: string, msg: u8[], at: i32, len: i32): Option[string] {
  let label: u8[] = [];
  let k = 0;
  while (k < len) {
    label = label.append(msg[at + k]);
    k += 1;
  }
  match (utf8.from_bytes(label)) {
    Ok(text) => {
      if (out.len() > 0) {
        return Some(out + "." + text);
      }
      return Some(text);
    },
    Err(_) => {
      return None;
    }
  }
  return None;
}
```

### `17 struct QuestionAt`

```fern
<<17 struct QuestionAt>>=
struct QuestionAt { q: Question, next: i32 }
```

### `18 function decode_question(msg: u8[], pos: i32): Result[QuestionAt, DnsError]`

```fern
<<18 function decode_question(msg: u8[], pos: i32): Result[QuestionAt, DnsError]>>=
function decode_question(msg: u8[], pos: i32): Result[QuestionAt, DnsError] {
  let n = decode_name(msg, pos)?;
  if (n.next + 4 > msg.len()) {
    return Err(Malformed);
  }
  let q = Question { name: n.name, qtype: be16(msg, n.next), qclass: be16(msg, n.next + 2) };
  return Ok(QuestionAt { q: q, next: n.next + 4 });
}
```

### `19 struct RecordAt`

```fern
<<19 struct RecordAt>>=
struct RecordAt { r: Record, next: i32 }
```

### `20 function decode_rdata(msg: u8[], rtype: i32, rclass: i32, at: i32, rdlen: i32): Result[RData, DnsError]`

```fern
<<20 function decode_rdata(msg: u8[], rtype: i32, rclass: i32, at: i32, rdlen: i32): Result[RData, DnsError]>>=
function decode_rdata(msg: u8[], rtype: i32, rclass: i32, at: i32, rdlen: i32): Result[RData, DnsError] {
  let raw: u8[] = [];
  let k = 0;
  while (k < rdlen) {
    raw = raw.append(msg[at + k]);
    k += 1;
  }
  if (rclass != CLASS_IN) {
    return Ok(Raw(raw));
  }
  if (rtype == TYPE_CNAME || rtype == TYPE_PTR) {
    return decode_name_rdata(msg, rtype, at, rdlen);
  }
  return decode_address_rdata(rtype, raw);
}

function decode_address_rdata(rtype: i32, raw: u8[]): Result[RData, DnsError] {
  if (rtype == TYPE_A && raw.len() == 4) {
    return Ok(A(net.ipv4(raw[0] as i32, raw[1] as i32, raw[2] as i32, raw[3] as i32)));
  }
  if (rtype == TYPE_AAAA && raw.len() == 16) {
    match (net.ipv6(raw)) {
      Some(ip) => {
        return Ok(AAAA(ip));
      },
      None => {
        return Err(Malformed);
      }
    }
  }
  return Ok(Raw(raw));
}

function decode_name_rdata(msg: u8[], rtype: i32, at: i32, rdlen: i32): Result[RData, DnsError] {
  let n = decode_name(msg, at)?;
  if (n.next > at + rdlen) {
    return Err(Malformed);
  }
  if (rtype == TYPE_CNAME) {
    return Ok(CNAME(n.name));
  }
  return Ok(PTR(n.name));
}

function decode_record(msg: u8[], pos: i32): Result[RecordAt, DnsError] {
  let n = decode_name(msg, pos)?;
  let p = n.next;
  if (p + 10 > msg.len()) {
    return Err(Malformed);
  }
  let rtype = be16(msg, p);
  let rclass = be16(msg, p + 2);
  let ttl = be32(msg, p + 4);
  let rdlen = be16(msg, p + 8);
  p += 10;
  if (p + rdlen > msg.len()) {
    return Err(Malformed);
  }
  let data = decode_rdata(msg, rtype, rclass, p, rdlen)?;
  let r = Record { name: n.name, rtype: rtype, rclass: rclass, ttl: ttl, data: data };
  return Ok(RecordAt { r: r, next: p + rdlen });
}
```

### `21 struct RecordsAt`

```fern
<<21 struct RecordsAt>>=
struct RecordsAt { rs: Record[], next: i32 }
```

### `22 function decode_records(msg: u8[], pos: i32, count: i32): Result[RecordsAt, DnsError]`

```fern
<<22 function decode_records(msg: u8[], pos: i32, count: i32): Result[RecordsAt, DnsError]>>=
function decode_records(msg: u8[], pos: i32, count: i32): Result[RecordsAt, DnsError] {
  let out: Record[] = [];
  let p = pos;
  let i = 0;
  while (i < count) {
    let r = decode_record(msg, p)?;
    out = out.append(r.r);
    p = r.next;
    i += 1;
  }
  return Ok(RecordsAt { rs: out, next: p });
}
```

### `23 pub function decode(msg: u8[]): Result[Message, DnsError]`

```fern
<<23 pub function decode(msg: u8[]): Result[Message, DnsError]>>=
pub function decode(msg: u8[]): Result[Message, DnsError] {
  if (msg.len() < 12) {
    return Err(Malformed);
  }
  let flags = be16(msg, 2);
  let qd = be16(msg, 4);
  let an = be16(msg, 6);
  let ns = be16(msg, 8);
  let ar = be16(msg, 10);
  let p = 12;
  let qs: Question[] = [];
  let i = 0;
  while (i < qd) {
    let q = decode_question(msg, p)?;
    qs = qs.append(q.q);
    p = q.next;
    i += 1;
  }
  let answers = decode_records(msg, p, an)?;
  let authority = decode_records(msg, answers.next, ns)?;
  let additional = decode_records(msg, authority.next, ar)?;
  return Ok(Message {
    id: be16(msg, 0),
    response: (flags & 32768) != 0,
    opcode: flags >> 11 & 15,
    authoritative: (flags & 1024) != 0,
    truncated: (flags & 512) != 0,
    recursion_desired: (flags & 256) != 0,
    recursion_available: (flags & 128) != 0,
    rcode: flags & 15,
    questions: qs,
    answers: answers.rs,
    authority: authority.rs,
    additional: additional.rs,
  });
}

pub function names_equal(a: string, b: string): boolean {
  return strip_root(a).eq_ignore_ascii_case(strip_root(b));
}
```

### `24 function strip_root(n: string): string`

```fern
<<24 function strip_root(n: string): string>>=
function strip_root(n: string): string {
  if (n.ends_with(".") && n.len() > 1) {
    return slice_unchecked(n, 0, n.len() - 1) + "";
  }
  return n;
}
```

### `25 pub function (m: Message) answers_question(q: Question): boolean`

```fern
<<25 pub function (m: Message) answers_question(q: Question): boolean>>=
pub function (m: Message) answers_question(q: Question): boolean {
  if (m.questions.len() != 1) {
    return false;
  }
  let mq = m.questions[0];
  return mq.qtype == q.qtype && mq.qclass == q.qclass && names_equal(mq.name, q.name);
}
```

### `26 pub struct Answer`

```fern
<<26 pub struct Answer>>=
pub struct Answer { canonical: string, records: Record[] }
```

### `27 pub function records_for(m: Message, name: string, rtype: i32): Answer`

```fern
<<27 pub function records_for(m: Message, name: string, rtype: i32): Answer>>=
pub function records_for(m: Message, name: string, rtype: i32): Answer {
  let cur = name;
  let hops = 0;
  while (hops <= m.answers.len()) {
    let at = scan_owner(m, cur, rtype);
    if (at.found.len() > 0 || at.alias.len() == 0) {
      return Answer { canonical: cur, records: at.found };
    }
    cur = at.alias;
    hops += 1;
  }
  let none: Record[] = [];
  return Answer { canonical: cur, records: none };
}
```

### `28 struct OwnerScan`

```fern
<<28 struct OwnerScan>>=
struct OwnerScan { found: Record[], alias: string }
```

### `29 function scan_owner(m: Message, owner: string, rtype: i32): OwnerScan`

```fern
<<29 function scan_owner(m: Message, owner: string, rtype: i32): OwnerScan>>=
function scan_owner(m: Message, owner: string, rtype: i32): OwnerScan {
  let found: Record[] = [];
  let alias = "";
  let i = 0;
  while (i < m.answers.len()) {
    let r = m.answers[i];
    i += 1;
    if (r.rclass != CLASS_IN || !names_equal(r.name, owner)) {
      continue;
    }
    if (r.rtype == rtype) {
      found = found.append(r);
    } else if (r.rtype == TYPE_CNAME && alias.len() == 0) {
      alias = cname_target(r.data);
    }
  }
  return OwnerScan { found: found, alias: alias };
}

function cname_target(d: RData): string {
  match (d) {
    CNAME(target) => {
      return target;
    },
    _ => {
      return "";
    }
  }
  return "";
}
```

### `30 pub function addresses_of(records: Record[]): net.IpAddr[]`

```fern
<<30 pub function addresses_of(records: Record[]): net.IpAddr[]>>=
pub function addresses_of(records: Record[]): net.IpAddr[] {
  let out: net.IpAddr[] = [];
  let i = 0;
  while (i < records.len()) {
    match (records[i].data) {
      A(ip) => {
        out = out.append(ip);
      },
      AAAA(ip) => {
        out = out.append(ip);
      },
      _ => {}
    }
    i += 1;
  }
  return out;
}
```

### `31 pub struct HostsEntry`

```fern
<<31 pub struct HostsEntry>>=
pub struct HostsEntry { ip: net.IpAddr, names: string[] }
```

### `32 pub function parse_hosts(contents: string): HostsEntry[]`

```fern
<<32 pub function parse_hosts(contents: string): HostsEntry[]>>=
pub function parse_hosts(contents: string): HostsEntry[] {
  let out: HostsEntry[] = [];
  let lines = contents.lines();
  let i = 0;
  while (i < lines.len()) {
    let f = strip_comment(lines[i], "#").fields();
    i += 1;
    if (f.len() < 2) {
      continue;
    }
    match (net.ip_parse(f[0])) {
      Ok(ip) => {
        out = out.append(HostsEntry { ip: ip, names: f.drop(1) });
      },
      Err(_) => {}
    }
  }
  return out;
}

pub function hosts_lookup(entries: HostsEntry[], name: string): net.IpAddr[] {
  let out: net.IpAddr[] = [];
  let i = 0;
  while (i < entries.len()) {
    if (entry_names(entries[i], name)) {
      out = out.append(entries[i].ip);
    }
    i += 1;
  }
  return out;
}

pub function hosts_canonical(entries: HostsEntry[], name: string): Option[string] {
  let i = 0;
  while (i < entries.len()) {
    if (entry_names(entries[i], name)) {
      return Some(entries[i].names[0]);
    }
    i += 1;
  }
  return None;
}
```

### `33 function entry_names(e: HostsEntry, name: string): boolean`

```fern
<<33 function entry_names(e: HostsEntry, name: string): boolean>>=
function entry_names(e: HostsEntry, name: string): boolean {
  let j = 0;
  while (j < e.names.len()) {
    if (names_equal(e.names[j], name)) {
      return true;
    }
    j += 1;
  }
  return false;
}

function strip_comment(line: string, marker: string): string {
  let Some(h) = line.index_of(marker) else {
    return line;
  };
  return slice_unchecked(line, 0, h) + "";
}
```

### `34 pub struct ResolvConf`

```fern
<<34 pub struct ResolvConf>>=
pub struct ResolvConf {
  nameservers: net.SocketAddr[],
  search: string[],
  ndots: i32,
  timeout: Duration,
  attempts: i32,
  rotate: boolean,
}
```

### `35 pub const PORT: i32`

```fern
<<35 pub const PORT: i32>>=
pub const PORT: i32 = 53;

pub function resolv_defaults(host: string): ResolvConf {
  let ns: net.SocketAddr[] = [net.socket_addr(net.ipv4_loopback(), PORT)];
  return ResolvConf { nameservers: ns, search: host_domain(host), ndots: 1, timeout: time.duration_seconds(5 as i64), attempts: 2, rotate: false };
}

pub function parse_resolv_conf(contents: string, host: string): ResolvConf {
  let rc = resolv_defaults(host);
  let ns: net.SocketAddr[] = [];
  let have_search = false;
  let lines = contents.lines();
  let i = 0;
  while (i < lines.len()) {
    let f = strip_comment(strip_comment(lines[i], "#"), ";").fields();
    i += 1;
    if (f.len() < 2) {
      continue;
    }
    if (f[0] == "nameserver") {
      ns = with_nameserver(ns, f[1]);
    } else if (f[0] == "domain" || f[0] == "search") {
      have_search = true;
      rc = ResolvConf { ...rc, search: search_domains(f) };
    } else if (f[0] == "options") {
      rc = parse_options(f, rc);
    }
  }
  if (!have_search) {
    rc = ResolvConf { ...rc, search: host_domain(host) };
  }
  if (ns.len() > 0) {
    rc = ResolvConf { ...rc, nameservers: ns };
  }
  return rc;
}
```

### `36 function with_nameserver(ns: net.SocketAddr[], text: string): net.SocketAddr[]`

```fern
<<36 function with_nameserver(ns: net.SocketAddr[], text: string): net.SocketAddr[]>>=
function with_nameserver(ns: net.SocketAddr[], text: string): net.SocketAddr[] {
  if (ns.len() >= 3 || text.contains("%")) {
    return ns;
  }
  match (net.ip_parse(text)) {
    Ok(ip) => {
      return ns.append(net.socket_addr(ip, PORT));
    },
    Err(_) => {
      return ns;
    }
  }
  return ns;
}

function search_domains(f: string[]): string[] {
  let out: string[] = [];
  let n = f.len();
  if (f[0] == "domain") {
    n = 2;
  }
  let i = 1;
  while (i < n) {
    let d = f[i];
    if (d.ends_with(".")) {
      d = slice_unchecked(d, 0, d.len() - 1) + "";
    }
    if (d.len() > 0) {
      out = out.append(d);
    }
    i += 1;
  }
  return out;
}

function parse_options(f: string[], rc0: ResolvConf): ResolvConf {
  let rc = rc0;
  let i = 1;
  while (i < f.len()) {
    let o = f[i];
    i += 1;
    if (o == "rotate") {
      rc = ResolvConf { ...rc, rotate: true };
    } else if (o.starts_with("ndots:")) {
      rc = ResolvConf { ...rc, ndots: option_value(o, 6, 0, 15, rc.ndots) };
    } else if (o.starts_with("timeout:")) {
      rc = ResolvConf { ...rc, timeout: time.duration_seconds(option_value(o, 8, 1, 30, (rc.timeout.as_millis() / (1000 as i64)) as i32) as i64) };
    } else if (o.starts_with("attempts:")) {
      rc = ResolvConf { ...rc, attempts: option_value(o, 9, 1, 5, rc.attempts) };
    }
  }
  return rc;
}

function option_value(o: string, at: i32, lo: i32, hi: i32, current: i32): i32 {
  match (i32.parse_i32(slice_unchecked(o, at, o.len()) + "")) {
    Ok(n) => {
      if (n < lo) {
        return lo;
      }
      if (n > hi) {
        return hi;
      }
      return n;
    },
    Err(_) => {
      return current;
    }
  }
  return current;
}

function host_domain(host: string): string[] {
  let out: string[] = [];
  let Some(dot) = host.index_of(".") else {
    return out;
  };
  if (dot + 1 >= host.len()) {
    return out;
  }
  return out.append(slice_unchecked(host, dot + 1, host.len()) + "");
}
```

### `37 pub struct SearchPlan`

```fern
<<37 pub struct SearchPlan>>=
pub struct SearchPlan { as_is: string, suffixes: string[], leads: boolean }
```

### `38 pub function search_plan(name: string, rc: ResolvConf): SearchPlan`

```fern
<<38 pub function search_plan(name: string, rc: ResolvConf): SearchPlan>>=
pub function search_plan(name: string, rc: ResolvConf): SearchPlan {
  let none: string[] = [];
  if (name.ends_with(".")) {
    return SearchPlan { as_is: strip_root(name), suffixes: none, leads: true };
  }
  let dots = 0;
  let i = 0;
  while (i < name.len()) {
    if (name[i] == 46) {
      dots += 1;
    }
    i += 1;
  }
  let sfx: string[] = [];
  let j = 0;
  while (j < rc.search.len()) {
    sfx = sfx.append(name + "." + rc.search[j]);
    j += 1;
  }
  return SearchPlan { as_is: name, suffixes: sfx, leads: dots >= rc.ndots };
}

pub function search_candidates(name: string, rc: ResolvConf): string[] {
  let p = search_plan(name, rc);
  let out: string[] = [];
  if (p.leads) {
    out = out.append(p.as_is);
  }
  let j = 0;
  while (j < p.suffixes.len()) {
    out = out.append(p.suffixes[j]);
    j += 1;
  }
  if (!p.leads) {
    out = out.append(p.as_is);
  }
  return out;
}

pub function new_id(): i32 {
  return random_i32() & 65535;
}
```

### `39 function zero_bytes(n: i32): u8[]`

```fern
<<39 function zero_bytes(n: i32): u8[]>>=
function zero_bytes(n: i32): u8[] {
  let out: u8[] = [];
  let i = 0;
  while (i < n) {
    out = out.append(0 as u8);
    i += 1;
  }
  return out;
}

function now_ms(): i64 {
  return monotonic_ns() / 1000000i64;
}

// The instant `wait_ms` from now on the monotonic clock. None when `wait_ms` is negative.
function within(wait_ms: i32): Option[Instant] {
  if (wait_ms < 0) {
    return None;
  }
  return Some(time.instant_from_nanos(monotonic_ns() + wait_ms as i64 * 1000000i64));
}

function readable_within(fd: i32, wait_ms: i32): boolean {
  let set: i32[] = [fd, 1];
  if let Woke(i) = async.wait_any(set, within(wait_ms)) {
    return true;
  }
  return false;
}

function local_for(ns: net.SocketAddr): net.SocketAddr {
  if (ns.ip.is_v6()) {
    return net.socket_addr(net.ipv6_unspecified(), 0);
  }
  return net.socket_addr(net.ipv4_unspecified(), 0);
}
```

### `40 pub function exchange(ns: net.SocketAddr, q: Message, timeout: Duration): Result[Message, DnsError]`

```fern
<<40 pub function exchange(ns: net.SocketAddr, q: Message, timeout: Duration): Result[Message, DnsError]>>=
pub function exchange(ns: net.SocketAddr, q: Message, timeout: Duration): Result[Message, DnsError] {
  return exchange_many(ns, [q], timeout)[0];
}

pub function exchange_many(ns: net.SocketAddr, qs: Message[], timeout: Duration): Result[Message, DnsError][] {
  let deadline = now_ms() + timeout.as_millis();
  let slots: Slot[] = [];
  let i = 0;
  while (i < qs.len()) {
    slots = slots.append(opened_slot(ns, qs[i]));
    i += 1;
  }
  slots = await_udp_replies(slots, deadline);
  let out: Result[Message, DnsError][] = [];
  i = 0;
  while (i < slots.len()) {
    let q = slots[i].q;
    let (s, rest) = slots.swap_at(i, Slot { q: q, sock: None, reply: None, err: NoReply });
    slots = rest;
    out = out.append(slot_result(ns, closed(s), deadline));
    i += 1;
  }
  return out;
}
```

### `41 struct Slot`

```fern
<<41 struct Slot>>=
struct Slot { q: Message, sock: Option[Socket], reply: Option[Message], err: DnsError }
```

### `42 function waiting(s: Slot): boolean`

```fern
<<42 function waiting(s: Slot): boolean>>=
function waiting(s: Slot): boolean {
  if (slot_fd(s) < 0) {
    return false;
  }
  match (s.reply) {
    Some(_) => {
      return false;
    },
    None => {
      return true;
    }
  }
  return true;
}

function opened_slot(ns: net.SocketAddr, q: Message): Slot {
  let bytes: u8[] = [];
  match (encode(with_edns(q))) {
    Ok(b) => {
      bytes = b;
    },
    Err(e) => {
      return Slot { q: q, sock: None, reply: None, err: e };
    }
  }
  match (send_udp(ns, bytes)) {
    Ok(sock) => {
      return Slot { q: q, sock: Some(sock), reply: None, err: NoReply };
    },
    Err(e) => {
      return Slot { q: q, sock: None, reply: None, err: e };
    }
  }
  return Slot { q: q, sock: None, reply: None, err: NoReply };
}

function slot_result(ns: net.SocketAddr, s: Slot, deadline: i64): Result[Message, DnsError] {
  match (s.reply) {
    Some(reply) => {
      if (reply.truncated) {
        return exchange_tcp(ns, s.q, time.duration_millis(deadline - now_ms()));
      }
      return Ok(reply);
    },
    None => {
      return Err(s.err);
    }
  }
  return Err(s.err);
}

function send_udp(ns: net.SocketAddr, bytes: u8[]): Result[Socket, DnsError] {
  match (net.udp_socket(local_for(ns))) {
    Ok(sock) => {
      match (net.send_to(sock, bytes, ns)) {
        Ok(_) => {
          return Ok(sock);
        },
        Err(e) => {
          let closed = net.close(sock);
          return Err(NetFailed(e));
        }
      }
    },
    Err(e) => {
      return Err(NetFailed(e));
    }
  }
  return Err(NoReply);
}

function slot_fd(s: Slot): i32 {
  match (s.sock) {
    Some(sock) => {
      return sock.fd;
    },
    None => {}
  }
  return -1;
}

function closed(own s: Slot): Slot {
  match (s.sock) {
    Some(sock) => {
      let shut = net.close(sock);
    },
    None => {}
  }
  return Slot { q: s.q, sock: None, reply: s.reply, err: s.err };
}

function pending_slots(slots: Slot[]): i32[] {
  let out: i32[] = [];
  let i = 0;
  while (i < slots.len()) {
    if (waiting(slots[i])) {
      out = out.append(i);
    }
    i += 1;
  }
  return out;
}

function await_udp_replies(own slots: Slot[], deadline: i64): Slot[] {
  let out = slots;
  let pending = pending_slots(out);
  let left = (deadline - now_ms()) as i32;
  while (pending.len() > 0 && left > 0) {
    let ready = first_readable(out, pending, left);
    if (ready < 0) {
      return out;
    }
    out = taken(out, pending[ready]);
    pending = pending_slots(out);
    left = (deadline - now_ms()) as i32;
  }
  return out;
}

function taken(own slots: Slot[], k: i32): Slot[] {
  let q = slots[k].q;
  let (s, rest) = slots.swap_at(k, Slot { q: q, sock: None, reply: None, err: NoReply });
  let reply = s.reply;
  let err = s.err;
  match (s.sock) {
    Some(sock) => {
      match (take_reply(sock, q)) {
        Ok(found) => {
          match (found) {
            Some(got) => {
              return rest.with(k, Slot { q: q, sock: Some(sock), reply: Some(got), err: err });
            },
            None => {
              return rest.with(k, Slot { q: q, sock: Some(sock), reply: reply, err: err });
            }
          }
        },
        Err(e) => {
          let shut = net.close(sock);
          return rest.with(k, Slot { q: q, sock: None, reply: reply, err: e });
        }
      }
    },
    None => {}
  }
  return rest.with(k, Slot { q: q, sock: None, reply: reply, err: err });
}

function first_readable(slots: Slot[], pending: i32[], wait_ms: i32): i32 {
  let set: i32[] = [];
  let i = 0;
  while (i < pending.len()) {
    set = set.append(slot_fd(slots[pending[i]]));
    set = set.append(1);
    i += 1;
  }
  if let Woke(i) = async.wait_any(set, within(wait_ms)) {
    return i;
  }
  return -1;
}
```

### `43 function take_reply(sock: Socket, q: Message): Result[Option[Message], DnsError]`

```fern
<<43 function take_reply(sock: Socket, q: Message): Result[Option[Message], DnsError]>>=
function take_reply(sock: Socket, q: Message): Result[Option[Message], DnsError] {
  let buf = zero_bytes(UDP_PAYLOAD);
  let n = 0;
  match (net.recv_from(sock, buf)) {
    Ok(got) => {
      n = got.0;
    },
    Err(e) => {
      match (e) {
        WouldBlock => {
          return Ok(None);
        },
        _ => {
          return Err(NetFailed(e));
        }
      }
    }
  }
  match (decode(buf.take(n))) {
    Ok(reply) => {
      if (reply.id == q.id && reply.response && reply.answers_question(q.questions[0])) {
        return Ok(Some(reply));
      }
      return Ok(None);
    },
    Err(_) => {
      return Ok(None);
    }
  }
  return Ok(None);
}
```

### `44 pub function exchange_tcp(ns: net.SocketAddr, q: Message, timeout: Duration): Result[Message, DnsError]`

```fern
<<44 pub function exchange_tcp(ns: net.SocketAddr, q: Message, timeout: Duration): Result[Message, DnsError]>>=
pub function exchange_tcp(ns: net.SocketAddr, q: Message, timeout: Duration): Result[Message, DnsError] {
  let bytes = encode(q)?;
  let framed: u8[] = [];
  framed = put16(framed, bytes.len());
  let sock = send_tcp(ns, framed.concat(bytes))?;
  let got = read_framed(sock, now_ms() + timeout.as_millis());
  tcp_close(sock);
  let reply = decode(got?)?;
  if (reply.id != q.id || !reply.response || !reply.answers_question(q.questions[0])) {
    return Err(Malformed);
  }
  return Ok(reply);
}
```

### `45 function send_tcp(ns: net.SocketAddr, framed: u8[]): Result[Socket, DnsError]`

```fern
<<45 function send_tcp(ns: net.SocketAddr, framed: u8[]): Result[Socket, DnsError]>>=
function send_tcp(ns: net.SocketAddr, framed: u8[]): Result[Socket, DnsError] {
  match (net.connect(ns)) {
    Ok(sock) => {
      return sent_all(sock, framed);
    },
    Err(e) => {
      return Err(NetFailed(e));
    }
  }
  return Err(NoReply);
}

function sent_all(own sock: Socket, framed: u8[]): Result[Socket, DnsError] {
  let remaining = framed;
  while (remaining.len() > 0) {
    let sent = tcp_send_bytes(sock, remaining);
    if (sent < 0) {
      let failure = net.error_from_host(sent);
      if (!failure.eq(net.Interrupted)) {
        tcp_close(sock);
        return Err(NetFailed(failure));
      }
    } else {
      if (sent == 0) {
        tcp_close(sock);
        return Err(NoReply);
      }
      if (sent == remaining.len()) {
        return Ok(sock);
      }
      remaining = array.drop(remaining, sent);
    }
  }
  return Ok(sock);
}

function read_framed(sock: Socket, deadline: i64): Result[u8[], DnsError] {
  let buf: u8[] = [];
  let want = -1;
  while (want < 0 || buf.len() < want) {
    let left = (deadline - now_ms()) as i32;
    if (left <= 0 || !readable_within(sock.fd, left)) {
      return Err(NoReply);
    }
    buf = buf.concat(read_some(sock)?);
    if (want < 0 && buf.len() >= 2) {
      want = 2 + be16(buf, 0);
    }
  }
  return Ok(buf.drop(2).take(want - 2));
}

function read_some(sock: Socket): Result[u8[], DnsError] {
  match (net.read_up_to(sock, 4096)) {
    Ok(got) => {
      if (got.len() == 0) {
        return Err(Malformed);
      }
      return Ok(got);
    },
    Err(e) => {
      if (e.eq(net.WouldBlock)) {
        let none: u8[] = [];
        return Ok(none);
      }
      return Err(NetFailed(e));
    }
  }
  return Err(Malformed);
}
```

### `46 pub function ask(conf: ResolvConf, q: Message): Result[Message, DnsError]`

```fern
<<46 pub function ask(conf: ResolvConf, q: Message): Result[Message, DnsError]>>=
pub function ask(conf: ResolvConf, q: Message): Result[Message, DnsError] {
  return ask_many(conf, [q])[0];
}

pub function ask_many(conf: ResolvConf, qs: Message[]): Result[Message, DnsError][] {
  let n = conf.nameservers.len();
  let asked: Asked[] = [];
  let i = 0;
  while (i < qs.len()) {
    asked = asked.append(Asked { reply: None, tally: Tally { servfail: false, refused: false, silent: false, last: NoNameserver } });
    i += 1;
  }
  if (n > 0) {
    let start = 0;
    if (conf.rotate) {
      start = (random_i32() & 2147483647) % n;
    }
    let round = 0;
    while (round < conf.attempts) {
      let k = 0;
      while (k < n) {
        asked = ask_server(conf.nameservers[(start + k) % n], qs, asked, conf.timeout);
        k += 1;
      }
      round += 1;
    }
  }
  let out: Result[Message, DnsError][] = [];
  i = 0;
  while (i < asked.len()) {
    out = out.append(asked_result(asked[i]));
    i += 1;
  }
  return out;
}
```

### `47 struct Asked`

```fern
<<47 struct Asked>>=
struct Asked { reply: Option[Message], tally: Tally }
```

### `48 function asked_result(a: Asked): Result[Message, DnsError]`

```fern
<<48 function asked_result(a: Asked): Result[Message, DnsError]>>=
function asked_result(a: Asked): Result[Message, DnsError] {
  match (a.reply) {
    Some(reply) => {
      return Ok(reply);
    },
    None => {
      return Err(tally_error(a.tally));
    }
  }
  return Err(tally_error(a.tally));
}

function unanswered(a: Asked): boolean {
  match (a.reply) {
    Some(_) => {
      return false;
    },
    None => {
      return true;
    }
  }
  return true;
}

function ask_server(ns: net.SocketAddr, qs: Message[], asked: Asked[], timeout: Duration): Asked[] {
  let open: i32[] = [];
  let open_qs: Message[] = [];
  let i = 0;
  while (i < asked.len()) {
    if (unanswered(asked[i])) {
      open = open.append(i);
      open_qs = open_qs.append(qs[i]);
    }
    i += 1;
  }
  if (open.len() == 0) {
    return asked;
  }
  let replies = exchange_many(ns, open_qs, timeout);
  let out = asked;
  i = 0;
  while (i < open.len()) {
    out = out.with(open[i], noted(out[open[i]], replies[i]));
    i += 1;
  }
  return out;
}

function noted(a: Asked, r: Result[Message, DnsError]): Asked {
  match (r) {
    Ok(reply) => {
      if (reply.rcode != RCODE_SERVER_FAILURE && reply.rcode != RCODE_REFUSED) {
        return Asked { ...a, reply: Some(reply) };
      }
      return Asked { ...a, tally: noted_rcode(a.tally, reply.rcode) };
    },
    Err(e) => {
      return Asked { ...a, tally: noted_failure(a.tally, e) };
    }
  }
  return a;
}
```

### `49 struct Tally`

```fern
<<49 struct Tally>>=
struct Tally { servfail: boolean, refused: boolean, silent: boolean, last: DnsError }
```

### `50 function noted_rcode(t: Tally, rcode: i32): Tally`

```fern
<<50 function noted_rcode(t: Tally, rcode: i32): Tally>>=
function noted_rcode(t: Tally, rcode: i32): Tally {
  if (rcode == RCODE_SERVER_FAILURE) {
    return Tally { ...t, servfail: true };
  }
  return Tally { ...t, refused: true };
}

function noted_failure(t: Tally, e: DnsError): Tally {
  match (e) {
    NoReply => {
      return Tally { ...t, silent: true };
    },
    _ => {
      return Tally { ...t, last: e };
    }
  }
  return t;
}

function tally_error(t: Tally): DnsError {
  if (t.servfail) {
    return ServerFailure;
  }
  if (t.refused) {
    return Refused;
  }
  if (t.silent) {
    return NoReply;
  }
  return t.last;
}
```

### `51 pub function answer_records(reply: Message, qname: string, rtype: i32): Result[Answer, DnsError]`

```fern
<<51 pub function answer_records(reply: Message, qname: string, rtype: i32): Result[Answer, DnsError]>>=
pub function answer_records(reply: Message, qname: string, rtype: i32): Result[Answer, DnsError] {
  if (reply.rcode == RCODE_NAME_ERROR) {
    return Err(NoSuchName);
  }
  if (reply.rcode == RCODE_SERVER_FAILURE) {
    return Err(ServerFailure);
  }
  if (reply.rcode == RCODE_REFUSED) {
    return Err(Refused);
  }
  if (reply.rcode != RCODE_OK) {
    return Err(Malformed);
  }
  let a = records_for(reply, qname, rtype);
  if (a.records.len() == 0) {
    return Err(NoData);
  }
  return Ok(a);
}

pub function lookup_records_with(conf: ResolvConf, name: string, rtype: i32, asker: (string) => Result[Answer, DnsError]): Result[Answer, DnsError] {
  return search_with(search_plan(name, conf), asker);
}

pub function search_with(plan: SearchPlan, asker: (string) => Result[Answer, DnsError]): Result[Answer, DnsError] {
  let lead_failed = false;
  let lead_err = NoSuchName;
  if (plan.leads) {
    match (asker(plan.as_is)) {
      Ok(a) => {
        return Ok(a);
      },
      Err(e) => {
        lead_failed = true;
        lead_err = e;
      }
    }
  }
  let sfx = search_suffixes(plan.suffixes, asker);
  match (sfx.answer) {
    Some(a) => {
      return Ok(a);
    },
    None => {}
  }
  if (sfx.refused) {
    return Err(Refused);
  }
  let last = sfx.last;
  if (!plan.leads) {
    match (asker(plan.as_is)) {
      Ok(a) => {
        return Ok(a);
      },
      Err(e) => {
        last = e;
      }
    }
  }
  return Err(search_failure(lead_failed, lead_err, sfx.servfail, last));
}
```

### `52 function search_failure(lead_failed: boolean, lead_err: DnsError, servfail: boolean, last: DnsError): DnsError`

```fern
<<52 function search_failure(lead_failed: boolean, lead_err: DnsError, servfail: boolean, last: DnsError): DnsError>>=
function search_failure(lead_failed: boolean, lead_err: DnsError, servfail: boolean, last: DnsError): DnsError {
  if (lead_failed) {
    return lead_err;
  }
  if (servfail) {
    return ServerFailure;
  }
  return last;
}
```

### `53 struct Suffixes`

```fern
<<53 struct Suffixes>>=
struct Suffixes { answer: Option[Answer], refused: boolean, servfail: boolean, last: DnsError }
```

### `54 function search_suffixes(suffixes: string[], asker: (string)`

```fern
<<54 function search_suffixes(suffixes: string[], asker: (string)>>=
function search_suffixes(suffixes: string[], asker: (string) => Result[Answer, DnsError]): Suffixes {
  let out = Suffixes { answer: None, refused: false, servfail: false, last: NoSuchName };
  let i = 0;
  while (i < suffixes.len()) {
    match (asker(suffixes[i])) {
      Ok(a) => {
        return Suffixes { ...out, answer: Some(a) };
      },
      Err(e) => {
        out = suffix_failed(out, e);
        if (out.refused || !continues_search(e)) {
          return out;
        }
      }
    }
    i += 1;
  }
  return out;
}

function suffix_failed(out: Suffixes, e: DnsError): Suffixes {
  match (e) {
    Refused => {
      return Suffixes { ...out, refused: true };
    },
    ServerFailure => {
      return Suffixes { ...out, servfail: true };
    },
    _ => {
      return Suffixes { ...out, last: e };
    }
  }
  return out;
}

function continues_search(e: DnsError): boolean {
  match (e) {
    NoSuchName => {
      return true;
    },
    NoData => {
      return true;
    },
    ServerFailure => {
      return true;
    },
    _ => {
      return false;
    }
  }
  return false;
}
```

### `55 pub function lookup_records(conf: ResolvConf, name: string, rtype: i32): Result[Answer, DnsError]`

```fern
<<55 pub function lookup_records(conf: ResolvConf, name: string, rtype: i32): Result[Answer, DnsError]>>=
pub function lookup_records(conf: ResolvConf, name: string, rtype: i32): Result[Answer, DnsError] {
  return lookup_records_with(conf, name, rtype, (qname: string): Result[Answer, DnsError] => ask_one(conf, qname, rtype));
}
```

### `56 function ask_one(conf: ResolvConf, qname: string, rtype: i32): Result[Answer, DnsError]`

```fern
<<56 function ask_one(conf: ResolvConf, qname: string, rtype: i32): Result[Answer, DnsError]>>=
function ask_one(conf: ResolvConf, qname: string, rtype: i32): Result[Answer, DnsError] {
  let reply = ask(conf, query(new_id(), qname, rtype))?;
  return answer_records(reply, qname, rtype);
}
```

### `57 pub function lookup_a(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError]`

```fern
<<57 pub function lookup_a(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError]>>=
pub function lookup_a(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError] {
  let a = lookup_records(conf, name, TYPE_A)?;
  return Ok(addresses_of(a.records));
}

pub function lookup_aaaa(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError] {
  let a = lookup_records(conf, name, TYPE_AAAA)?;
  return Ok(addresses_of(a.records));
}

pub function canonical_name(conf: ResolvConf, name: string): Result[string, DnsError] {
  let a = lookup_records(conf, name, TYPE_A)?;
  return Ok(a.canonical);
}

pub function lookup_addresses(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError] {
  let found = found_addresses(conf, name)?;
  return Ok(sort_addresses(found));
}
```

### `58 function found_addresses(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError]`

```fern
<<58 function found_addresses(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError]>>=
function found_addresses(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError] {
  let a: Answer = search_with(search_plan(name, conf), (qname: string): Result[Answer, DnsError] => ask_pair(conf, qname))?;
  return Ok(addresses_of(a.records));
}

function ask_pair(conf: ResolvConf, qname: string): Result[Answer, DnsError] {
  let replies = ask_many(conf, [query(new_id(), qname, TYPE_A), query(new_id(), qname, TYPE_AAAA)]);
  return combine_pair(reply_records(replies[0], qname, TYPE_A), reply_records(replies[1], qname, TYPE_AAAA));
}

function reply_records(r: Result[Message, DnsError], qname: string, rtype: i32): Result[Answer, DnsError] {
  let reply = r?;
  return answer_records(reply, qname, rtype);
}
```

### `59 pub function combine_pair(a: Result[Answer, DnsError], aaaa: Result[Answer, DnsError]): Result[Answer, DnsE...`

```fern
<<59 pub function combine_pair(a: Result[Answer, DnsError], aaaa: Result[Answer, DnsError]): Result[Answer, DnsE...>>=
pub function combine_pair(a: Result[Answer, DnsError], aaaa: Result[Answer, DnsError]): Result[Answer, DnsError] {
  match (a) {
    Ok(ra) => {
      match (aaaa) {
        Ok(r6) => {
          return Ok(Answer { canonical: ra.canonical, records: ra.records.concat(r6.records) });
        },
        Err(_) => {
          return Ok(ra);
        }
      }
    },
    Err(ea) => {
      match (aaaa) {
        Ok(r6) => {
          return Ok(r6);
        },
        Err(e6) => {
          return Err(pair_failure(ea, e6));
        }
      }
    }
  }
  return a;
}
```

### `60 function pair_failure(ea: DnsError, e6: DnsError): DnsError`

```fern
<<60 function pair_failure(ea: DnsError, e6: DnsError): DnsError>>=
function pair_failure(ea: DnsError, e6: DnsError): DnsError {
  if (!passes_over(ea)) {
    return ea;
  }
  if (!passes_over(e6)) {
    return e6;
  }
  if (is_no_such_name(ea) && is_no_such_name(e6)) {
    return NoSuchName;
  }
  return NoData;
}

function passes_over(e: DnsError): boolean {
  match (e) {
    NoSuchName => {
      return true;
    },
    NoData => {
      return true;
    },
    _ => {
      return false;
    }
  }
  return false;
}

function is_no_such_name(e: DnsError): boolean {
  match (e) {
    NoSuchName => {
      return true;
    },
    _ => {
      return false;
    }
  }
  return false;
}
```

### `61 pub struct Policy`

```fern
<<61 pub struct Policy>>=
pub struct Policy { precedence: i32, label: i32 }
```

### `62 pub function policy_of(ip: net.IpAddr): Policy`

```fern
<<62 pub function policy_of(ip: net.IpAddr): Policy>>=
pub function policy_of(ip: net.IpAddr): Policy {
  if (ip.is_v4()) {
    return Policy { precedence: 35, label: 4 };
  }
  if (ip.is_loopback()) {
    return Policy { precedence: 50, label: 0 };
  }
  let b = ip.bytes();
  if (leading_zero_bytes(b) >= 10) {
    return policy_embedded(b);
  }
  return policy_prefixed(b);
}
```

### `63 function policy_embedded(b: u8[]): Policy`

```fern
<<63 function policy_embedded(b: u8[]): Policy>>=
function policy_embedded(b: u8[]): Policy {
  if (b[10] == 255u8 && b[11] == 255u8) {
    return Policy { precedence: 35, label: 4 };
  }
  if (b[10] == 0u8 && b[11] == 0u8) {
    return Policy { precedence: 1, label: 3 };
  }
  return Policy { precedence: 40, label: 1 };
}

function policy_prefixed(b: u8[]): Policy {
  if (b[0] == 32u8 && b[1] == 2u8) {
    return Policy { precedence: 30, label: 2 };
  }
  if (b[0] == 32u8 && b[1] == 1u8 && b[2] == 0u8 && b[3] == 0u8) {
    return Policy { precedence: 5, label: 5 };
  }
  if (b[0] == 63u8 && b[1] == 254u8) {
    return Policy { precedence: 1, label: 12 };
  }
  return policy_local(b);
}

function policy_local(b: u8[]): Policy {
  if ((b[0] & 254u8) == 252u8) {
    return Policy { precedence: 3, label: 13 };
  }
  if (b[0] == 254u8 && (b[1] & 192u8) == 192u8) {
    return Policy { precedence: 1, label: 11 };
  }
  return Policy { precedence: 40, label: 1 };
}

function leading_zero_bytes(b: u8[]): i32 {
  let n = 0;
  while (n < b.len() && b[n] == 0u8) {
    n += 1;
  }
  return n;
}
```

### `64 pub function scope_of(ip: net.IpAddr): i32`

```fern
<<64 pub function scope_of(ip: net.IpAddr): i32>>=
pub function scope_of(ip: net.IpAddr): i32 {
  if (ip.is_v4()) {
    return scope_v4(ip.bytes());
  }
  return scope_v6(ip, ip.bytes());
}
```

### `65 function scope_v4(b: u8[]): i32`

```fern
<<65 function scope_v4(b: u8[]): i32>>=
function scope_v4(b: u8[]): i32 {
  if (b[0] == 127u8 || b[0] == 169u8 && b[1] == 254u8) {
    return 2;
  }
  if (b[0] == 224u8 && b[1] == 0u8 && b[2] == 0u8) {
    return 2;
  }
  if (b[0] == 239u8) {
    return 5;
  }
  return 14;
}

function scope_v6(ip: net.IpAddr, b: u8[]): i32 {
  if (b[0] == 255u8) {
    return (b[1] & 15u8) as i32;
  }
  if (ip.is_loopback() || b[0] == 254u8 && (b[1] & 192u8) == 128u8) {
    return 2;
  }
  if (b[0] == 254u8 && (b[1] & 192u8) == 192u8) {
    return 5;
  }
  return 14;
}
```

### `66 pub function common_prefix_len(a: net.IpAddr, b: net.IpAddr): i32`

```fern
<<66 pub function common_prefix_len(a: net.IpAddr, b: net.IpAddr): i32>>=
pub function common_prefix_len(a: net.IpAddr, b: net.IpAddr): i32 {
  if (a.is_v4() || b.is_v4()) {
    return 0;
  }
  let ab = a.bytes();
  let bb = b.bytes();
  let bits = 0;
  let i = 0;
  while (i < 8) {
    let x = (ab[i] ^ bb[i]) as i32;
    if (x != 0) {
      let m = 128;
      while (m > 0 && (x & m) == 0) {
        bits += 1;
        m = m >> 1;
      }
      return bits;
    }
    bits += 8;
    i += 1;
  }
  return bits;
}

pub function source_for(dst: net.IpAddr): Option[net.IpAddr] {
  let probe = net.socket_addr(dst, 9);
  match (net.udp_socket(local_for(probe))) {
    Ok(sock) => {
      let found: Option[net.IpAddr] = None;
      match (net.set_peer(sock, probe)) {
        Ok(_) => {
          match (net.local_addr(sock)) {
            Ok(local) => {
              found = Some(local.ip);
            },
            Err(_) => {}
          }
        },
        Err(_) => {}
      }
      let closed = net.close(sock);
      return found;
    },
    Err(_) => {}
  }
  return None;
}

pub function order_addresses(dsts: net.IpAddr[], srcs: Option[net.IpAddr][]): net.IpAddr[] {
  let out: net.IpAddr[] = [];
  let taken: boolean[] = [];
  let i = 0;
  while (i < dsts.len()) {
    taken = taken.append(false);
    i += 1;
  }
  let placed = 0;
  while (placed < dsts.len()) {
    let best: i32 = -1;
    i = 0;
    while (i < dsts.len()) {
      if (!taken[i] && (best < 0 || prefers(dsts[i], srcs[i], dsts[best], srcs[best]) < 0)) {
        best = i;
      }
      i += 1;
    }
    taken = taken.with(best, true);
    out = out.append(dsts[best]);
    placed += 1;
  }
  return out;
}

pub function sort_addresses(dsts: net.IpAddr[]): net.IpAddr[] {
  return sort_by(dsts, source_for);
}
```

### `67 function sort_by(dsts: net.IpAddr[], src: (net.IpAddr)`

```fern
<<67 function sort_by(dsts: net.IpAddr[], src: (net.IpAddr)>>=
function sort_by(dsts: net.IpAddr[], src: (net.IpAddr) => Option[net.IpAddr]): net.IpAddr[] {
  if (dsts.len() < 2) {
    return dsts;
  }
  let srcs: Option[net.IpAddr][] = [];
  let i = 0;
  while (i < dsts.len()) {
    srcs = srcs.append(src(dsts[i]));
    i += 1;
  }
  return order_addresses(dsts, srcs);
}

function prefers(a: net.IpAddr, sa: Option[net.IpAddr], b: net.IpAddr, sb: Option[net.IpAddr]): i32 {
  match (sa) {
    Some(src_a) => {
      match (sb) {
        Some(src_b) => {
          return prefers_routed(a, src_a, b, src_b);
        },
        None => {
          return -1;
        }
      }
    },
    None => {
      match (sb) {
        Some(_) => {
          return 1;
        },
        None => {
          return 0;
        }
      }
    }
  }
  return 0;
}

function prefers_routed(a: net.IpAddr, sa: net.IpAddr, b: net.IpAddr, sb: net.IpAddr): i32 {
  let scope_a = scope_of(a);
  let scope_b = scope_of(b);
  let r = prefer_true(scope_a == scope_of(sa), scope_b == scope_of(sb));
  if (r != 0) {
    return r;
  }
  let pa = policy_of(a);
  let pb = policy_of(b);
  r = prefer_true(pa.label == policy_of(sa).label, pb.label == policy_of(sb).label);
  if (r != 0) {
    return r;
  }
  if (pa.precedence != pb.precedence) {
    return pb.precedence - pa.precedence;
  }
  if (scope_a != scope_b) {
    return scope_a - scope_b;
  }
  return common_prefix_len(b, sb) - common_prefix_len(a, sa);
}

function prefer_true(for_a: boolean, for_b: boolean): i32 {
  if (for_a && !for_b) {
    return -1;
  }
  if (for_b && !for_a) {
    return 1;
  }
  return 0;
}
```

### `68 pub struct DialOptions`

```fern
<<68 pub struct DialOptions>>=
pub struct DialOptions { fallback: Duration, timeout: Duration }
```

### `69 pub function dial_options(): DialOptions`

```fern
<<69 pub function dial_options(): DialOptions>>=
pub function dial_options(): DialOptions {
  return DialOptions { fallback: time.duration_millis(300 as i64), timeout: time.duration_seconds(10 as i64) };
}

pub function interleave_families(addrs: net.IpAddr[]): net.IpAddr[] {
  if (addrs.len() == 0) {
    return addrs;
  }
  let lead_v6 = addrs[0].is_v6();
  let lead: net.IpAddr[] = [];
  let other: net.IpAddr[] = [];
  let i = 0;
  while (i < addrs.len()) {
    if (addrs[i].is_v6() == lead_v6) {
      lead = lead.append(addrs[i]);
    } else {
      other = other.append(addrs[i]);
    }
    i += 1;
  }
  let out: net.IpAddr[] = [];
  i = 0;
  while (i < lead.len() || i < other.len()) {
    if (i < lead.len()) {
      out = out.append(lead[i]);
    }
    if (i < other.len()) {
      out = out.append(other[i]);
    }
    i += 1;
  }
  return out;
}
```

### `71 pub function connect_race(addrs: net.IpAddr[], port: i32, opts: DialOptions): Result[Socket, net.NetError]`

```fern
<<71 pub function connect_race(addrs: net.IpAddr[], port: i32, opts: DialOptions): Result[Socket, net.NetError]>>=
pub function connect_race(addrs: net.IpAddr[], port: i32, opts: DialOptions): Result[Socket, net.NetError] {
  let deadline = now_ms() + opts.timeout.as_millis();
  let race = Race { open: [], next: 0, due: now_ms(), last: HostUnreachable };
  while (true) {
    let now = now_ms();
    if (now >= deadline) {
      return abandoned(race, TimedOut);
    }
    if (race.next < addrs.len() && (race.open.len() == 0 || now >= race.due)) {
      let addr = net.socket_addr(addrs[race.next], port);
      race = started(race, addr, opts);
      continue;
    }
    if (race.open.len() == 0) {
      return Err(race.last);
    }
    if let async.Wake.Cancelled = async.wait_any(writable(race.open), within(wait_window(race, addrs.len(), now, deadline))) {
      return abandoned(race, net.Interrupted);
    }
    let (won, rest) = settled(race);
    race = rest;
    match (won) {
      Some(sock) => {
        return finished(race, sock);
      },
      None => {}
    }
  }
  return Err(TimedOut);
}

function abandoned(own race: Race, e: net.NetError): Result[Socket, net.NetError] {
  close_all(race.open);
  return Err(e);
}

function finished(own race: Race, own sock: Socket): Result[Socket, net.NetError] {
  close_all(race.open);
  let blocking = net.set_nonblocking(sock, false);
  return Ok(sock);
}
```

### `72 function wait_window(race: Race, n: i32, now: i64, deadline: i64): i32`

```fern
<<72 function wait_window(race: Race, n: i32, now: i64, deadline: i64): i32>>=
function wait_window(race: Race, n: i32, now: i64, deadline: i64): i32 {
  let wait_ms = (deadline - now) as i32;
  if (race.next < n && (race.due - now) as i32 < wait_ms) {
    wait_ms = (race.due - now) as i32;
  }
  if (wait_ms < 1) {
    return 1;
  }
  return wait_ms;
}
```

### `73 struct Race`

```fern
<<73 struct Race>>=
struct Race { open: Option[Socket][], next: i32, due: i64, last: net.NetError }
```

### `74 function writable(open: Option[Socket][]): i32[]`

```fern
<<74 function writable(open: Option[Socket][]): i32[]>>=
function writable(open: Option[Socket][]): i32[] {
  let set: i32[] = [];
  let i = 0;
  while (i < open.len()) {
    match (open[i]) {
      Some(sock) => {
        set = set.append(sock.fd);
        set = set.append(2);
      },
      None => {}
    }
    i += 1;
  }
  return set;
}

function started(own race: Race, addr: net.SocketAddr, opts: DialOptions): Race {
  let next = race.next + 1;
  match (net.connect_start(addr)) {
    Ok(sock) => {
      return Race { open: race.open.append(Some(sock)), next: next, due: now_ms() + opts.fallback.as_millis(), last: race.last };
    },
    Err(e) => {
      return Race { open: race.open, next: next, due: now_ms(), last: e };
    }
  }
  return Race { open: race.open, next: next, due: race.due, last: race.last };
}

function settled(own race: Race): (Option[Socket], Race) {
  let open = race.open;
  let keep: Option[Socket][] = [];
  let won: Option[Socket] = None;
  let has_won = false;
  let due = race.due;
  let last = race.last;
  let i = 0;
  while (i < open.len()) {
    let (slot, rest) = open.swap_at(i, None);
    open = rest;
    match (slot) {
      Some(sock) => {
        if (has_won) {
          keep = keep.append(Some(sock));
        } else {
          match (net.connect_result(sock)) {
            Ok(_) => {
              won = Some(sock);
              has_won = true;
            },
            Err(e) => {
              if (e.eq(net.InProgress)) {
                keep = keep.append(Some(sock));
              } else {
                let shut = net.close(sock);
                due = now_ms();
                last = e;
              }
            }
          }
        }
      },
      None => {}
    }
    i += 1;
  }
  return (won, Race { open: keep, next: race.next, due: due, last: last });
}

function close_all(own open: Option[Socket][]): void {
  let left = open;
  let i = 0;
  while (i < left.len()) {
    let (slot, rest) = left.swap_at(i, None);
    left = rest;
    match (slot) {
      Some(sock) => {
        let shut = net.close(sock);
      },
      None => {}
    }
    i += 1;
  }
}
```

### `75 pub function dial(name: string, port: i32, opts: DialOptions): Result[Socket, DnsError]`

```fern
<<75 pub function dial(name: string, port: i32, opts: DialOptions): Result[Socket, DnsError]>>=
pub function dial(name: string, port: i32, opts: DialOptions): Result[Socket, DnsError] {
  let addrs = resolve(name)?;
  match (connect_race(interleave_families(addrs), port, opts)) {
    Ok(sock) => {
      return Ok(sock);
    },
    Err(e) => {
      return Err(NetFailed(e));
    }
  }
  return Err(NoNameserver);
}
```

### `76 pub struct Nat64Prefix`

```fern
<<76 pub struct Nat64Prefix>>=
pub struct Nat64Prefix { bytes: u8[], bits: i32 }
```

### `77 pub function nat64_prefix_of(aaaa: net.IpAddr): Option[Nat64Prefix]`

```fern
<<77 pub function nat64_prefix_of(aaaa: net.IpAddr): Option[Nat64Prefix]>>=
pub function nat64_prefix_of(aaaa: net.IpAddr): Option[Nat64Prefix] {
  if (!aaaa.is_v6()) {
    return None;
  }
  let b = aaaa.bytes();
  let lengths: i32[] = [96, 64, 56, 48, 40, 32];
  let i = 0;
  while (i < lengths.len()) {
    match (aaaa.embedded_v4(lengths[i])) {
      Some(v4) => {
        if (is_well_known(v4.bytes())) {
          return Some(Nat64Prefix { bytes: b.take(lengths[i] / 8).concat(zero_bytes(16 - lengths[i] / 8)), bits: lengths[i] });
        }
      },
      None => {}
    }
    i += 1;
  }
  return None;
}
```

### `78 function is_well_known(v4: u8[]): boolean`

```fern
<<78 function is_well_known(v4: u8[]): boolean>>=
function is_well_known(v4: u8[]): boolean {
  return v4[0] == 192u8 && v4[1] == 0u8 && v4[2] == 0u8 && (v4[3] == 170u8 || v4[3] == 171u8);
}
```

### `79 pub function synthesize(prefix: Nat64Prefix, v4: net.IpAddr): net.IpAddr`

```fern
<<79 pub function synthesize(prefix: Nat64Prefix, v4: net.IpAddr): net.IpAddr>>=
pub function synthesize(prefix: Nat64Prefix, v4: net.IpAddr): net.IpAddr {
  if (!v4.is_v4()) {
    return v4;
  }
  let src = v4.bytes();
  let out = prefix.bytes.take(prefix.bits / 8);
  let k = 0;
  while (out.len() < 16) {
    if (out.len() != 8 && k < 4) {
      out = out.append(src[k]);
      k += 1;
    } else {
      out = out.append(0u8);
    }
  }
  match (net.ipv6(out)) {
    Some(ip) => {
      return ip;
    },
    None => {
      return v4;
    }
  }
  return v4;
}

pub function nat64_prefixes(conf: ResolvConf): Nat64Prefix[] {
  let out: Nat64Prefix[] = [];
  match (lookup_aaaa(conf, "ipv4only.arpa.")) {
    Ok(xs) => {
      let i = 0;
      while (i < xs.len()) {
        match (nat64_prefix_of(xs[i])) {
          Some(p) => {
            if (!has_prefix(out, p)) {
              out = out.append(p);
            }
          },
          None => {}
        }
        i += 1;
      }
    },
    Err(_) => {}
  }
  return out;
}

pub function nat64_carried(addr: net.IpAddr): Option[net.IpAddr] {
  return carried_by(addr, (): Nat64Prefix[] => nat64_prefixes(system_conf()));
}
```

### `80 function carried_by(addr: net.IpAddr, prefixes: ()`

```fern
<<80 function carried_by(addr: net.IpAddr, prefixes: ()>>=
function carried_by(addr: net.IpAddr, prefixes: () => Nat64Prefix[]): Option[net.IpAddr] {
  if (!addr.is_v6()) {
    return None;
  }
  let b = addr.bytes();
  let known = prefixes();
  for p in known {
    if (same_bytes(b.take(p.bits / 8), p.bytes.take(p.bits / 8))) {
      return addr.embedded_v4(p.bits);
    }
  }
  return None;
}

function has_prefix(ps: Nat64Prefix[], p: Nat64Prefix): boolean {
  let i = 0;
  while (i < ps.len()) {
    if (ps[i].bits == p.bits && same_bytes(ps[i].bytes, p.bytes)) {
      return true;
    }
    i += 1;
  }
  return false;
}

function same_bytes(a: u8[], b: u8[]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  let i = 0;
  while (i < a.len()) {
    if (a[i] != b[i]) {
      return false;
    }
    i += 1;
  }
  return true;
}

function with_nat64(conf: ResolvConf, addrs: net.IpAddr[]): net.IpAddr[] {
  return nat64_by(addrs, source_for, (): Nat64Prefix[] => nat64_prefixes(conf));
}

function nat64_by(addrs: net.IpAddr[], src: (net.IpAddr) => Option[net.IpAddr], prefixes: () => Nat64Prefix[]): net.IpAddr[] {
  let i = 0;
  while (i < addrs.len()) {
    if (addrs[i].is_v6()) {
      return addrs;
    }
    i += 1;
  }
  if (addrs.len() == 0) {
    return addrs;
  }
  match (src(addrs[0])) {
    Some(_) => {
      return addrs;
    },
    None => {}
  }
  let found = prefixes();
  if (found.len() == 0) {
    return addrs;
  }
  let out: net.IpAddr[] = [];
  i = 0;
  while (i < addrs.len()) {
    let k = 0;
    while (k < found.len()) {
      out = out.append(synthesize(found[k], addrs[i]));
      k += 1;
    }
    i += 1;
  }
  return out;
}
```

### `81 pub function system_conf(): ResolvConf`

```fern
<<81 pub function system_conf(): ResolvConf>>=
pub function system_conf(): ResolvConf {
  let contents = "";
  match (read_file("/etc/resolv.conf")) {
    Ok(c) => {
      contents = c;
    },
    Err(_) => {}
  }
  return parse_resolv_conf(contents, hostname());
}

pub function system_hosts(): HostsEntry[] {
  match (read_file("/etc/hosts")) {
    Ok(c) => {
      return parse_hosts(c);
    },
    Err(_) => {
      let none: HostsEntry[] = [];
      return none;
    }
  }
  let none2: HostsEntry[] = [];
  return none2;
}

pub function resolve(name: string): Result[net.IpAddr[], DnsError] {
  let conf = system_conf();
  return Ok(reachable(conf, resolve_with(conf, name)?));
}

pub function resolve_plain(name: string): Result[net.IpAddr[], DnsError] {
  match (net.ip_parse(name)) {
    Ok(ip) => {
      return Ok([ip]);
    },
    Err(_) => {}
  }
  return resolve_with(system_conf(), name);
}

pub function synthesized(addrs: net.IpAddr[]): net.IpAddr[] {
  return reachable(system_conf(), addrs);
}
```

### `82 function reachable(conf: ResolvConf, addrs: net.IpAddr[]): net.IpAddr[]`

```fern
<<82 function reachable(conf: ResolvConf, addrs: net.IpAddr[]): net.IpAddr[]>>=
function reachable(conf: ResolvConf, addrs: net.IpAddr[]): net.IpAddr[] {
  return sort_addresses(with_nat64(conf, addrs));
}
```

### `83 pub struct Resolver`

```fern
<<83 pub struct Resolver>>=
pub struct Resolver { seen: Cell[Seen] }

pub struct Seen { at: i64, conf: ResolvConf, hosts: HostsEntry[], nat64: Option[Nat64Prefix[]], routes: Route[] }

pub struct Route { dst: net.IpAddr, src: Option[net.IpAddr] }

const RECHECK_NS: i64 = 5000000000;

const ROUTES_KEPT: i32 = 64;
```

### `84 pub function resolver(): Resolver`

```fern
<<84 pub function resolver(): Resolver>>=
pub function resolver(): Resolver {
  let routes: Route[] = [];
  let hosts: HostsEntry[] = [];
  return Resolver { seen: cell_new(Seen { at: 0 - 1 as i64, conf: parse_resolv_conf("", ""), hosts: hosts, nat64: None, routes: routes }) };
}
```

### `85 function (r: Resolver) fresh(): Seen`

```fern
<<85 function (r: Resolver) fresh(): Seen>>=
function (r: Resolver) fresh(): Seen {
  let s = r.seen.get();
  let now = monotonic_ns();
  if (s.at >= 0 as i64 && now - s.at < RECHECK_NS) {
    return s;
  }
  let routes: Route[] = [];
  let read = Seen { at: now, conf: system_conf(), hosts: system_hosts(), nat64: None, routes: routes };
  r.seen.set(read);
  return read;
}
```

### `86 pub function (r: Resolver) conf(): ResolvConf`

```fern
<<86 pub function (r: Resolver) conf(): ResolvConf>>=
pub function (r: Resolver) conf(): ResolvConf {
  return r.fresh().conf;
}

pub function (r: Resolver) source(dst: net.IpAddr): Option[net.IpAddr] {
  let s = r.fresh();
  for route in s.routes {
    if (route.dst.eq(dst)) {
      return route.src;
    }
  }
  let src = source_for(dst);
  let kept = s.routes;
  if (kept.len() >= ROUTES_KEPT) {
    kept = kept.drop(1);
  }
  r.seen.set(Seen { ...s, routes: kept.append(Route { dst: dst, src: src }) });
  return src;
}
```

### `87 function (r: Resolver) prefixes(): Nat64Prefix[]`

```fern
<<87 function (r: Resolver) prefixes(): Nat64Prefix[]>>=
function (r: Resolver) prefixes(): Nat64Prefix[] {
  let s = r.fresh();
  if let Some(p) = s.nat64 {
    return p;
  }
  let found = nat64_prefixes(s.conf);
  r.seen.set(Seen { ...r.seen.get(), nat64: Some(found) });
  return found;
}
```

### `88 pub function (r: Resolver) resolve_plain(name: string): Result[net.IpAddr[], DnsError]`

```fern
<<88 pub function (r: Resolver) resolve_plain(name: string): Result[net.IpAddr[], DnsError]>>=
pub function (r: Resolver) resolve_plain(name: string): Result[net.IpAddr[], DnsError] {
  match (net.ip_parse(name)) {
    Ok(ip) => {
      return Ok([ip]);
    },
    Err(_) => {}
  }
  let s = r.fresh();
  return resolve_in(s.conf, (): HostsEntry[] => s.hosts, name);
}

pub function (r: Resolver) resolve(name: string): Result[net.IpAddr[], DnsError] {
  return Ok(r.synthesized(r.resolve_plain(name)?));
}

pub function (r: Resolver) synthesized(addrs: net.IpAddr[]): net.IpAddr[] {
  let src: (net.IpAddr) => Option[net.IpAddr] = (d: net.IpAddr): Option[net.IpAddr] => r.source(d);
  return sort_by(nat64_by(addrs, src, (): Nat64Prefix[] => r.prefixes()), src);
}

pub function (r: Resolver) nat64_carried(addr: net.IpAddr): Option[net.IpAddr] {
  return carried_by(addr, (): Nat64Prefix[] => r.prefixes());
}
```

### `89 function resolve_with(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError]`

```fern
<<89 function resolve_with(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError]>>=
function resolve_with(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError] {
  return resolve_in(conf, (): HostsEntry[] => system_hosts(), name);
}

function resolve_in(conf: ResolvConf, hosts_of: () => HostsEntry[], name: string): Result[net.IpAddr[], DnsError] {
  match (net.ip_parse(name)) {
    Ok(ip) => {
      return Ok([ip]);
    },
    Err(_) => {}
  }
  let hosts = hosts_lookup(hosts_of(), name);
  if (hosts.len() > 0) {
    return Ok(hosts);
  }
  return found_addresses(conf, name);
}
```
