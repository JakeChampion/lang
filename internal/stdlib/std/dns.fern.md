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

<<35 pub function dns_port(): i32>>

<<36 function with_nameserver(ns: net.SocketAddr[], text: string): net.SocketAddr[]>>

<<37 pub struct SearchPlan>>

<<38 pub function search_plan(name: string, rc: ResolvConf): SearchPlan>>

<<39 function zero_bytes(n: i32): u8[]>>

<<40 pub function exchange(ns: net.SocketAddr, q: Message, timeout: Duration): Result[Message, DnsError]>>

<<41 struct Slot>>

<<42 function waiting(s: Slot): boolean>>

<<43 function take_reply(sock: i32, q: Message): Result[Option[Message], DnsError]>>

<<44 pub function exchange_tcp(ns: net.SocketAddr, q: Message, timeout: Duration): Result[Message, DnsError]>>

<<45 function send_tcp(ns: net.SocketAddr, framed: u8[]): Result[i32, DnsError]>>

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

<<70 struct Attempt>>

<<71 pub function connect_race(addrs: net.IpAddr[], port: i32, opts: DialOptions): Result[i32, net.NetError]>>

<<72 function wait_window(race: Race, n: i32, now: i64, deadline: i64): i32>>

<<73 struct Race>>

<<74 function writable(attempts: Attempt[]): i32[]>>

<<75 pub function dial(name: string, port: i32, opts: DialOptions): Result[i32, DnsError]>>

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
import "std/error";
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
  Socket(net.NetError),
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
    Socket(ne) => {
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

pub function type_a(): i32 {
  return 1;
}

pub function type_cname(): i32 {
  return 5;
}

pub function type_ptr(): i32 {
  return 12;
}

pub function type_aaaa(): i32 {
  return 28;
}

pub function type_opt(): i32 {
  return 41;
}

pub function class_in(): i32 {
  return 1;
}

pub function rcode_ok(): i32 {
  return 0;
}

pub function rcode_format_error(): i32 {
  return 1;
}

pub function rcode_server_failure(): i32 {
  return 2;
}

pub function rcode_name_error(): i32 {
  return 3;
}

pub function rcode_not_implemented(): i32 {
  return 4;
}

pub function rcode_refused(): i32 {
  return 5;
}
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
  let qs: Question[] = [Question { name: name, qtype: qtype, qclass: class_in() }];
  return Message {
    id: id & 65535, response: false, opcode: 0, authoritative: false, truncated: false,
    recursion_desired: true, recursion_available: false, rcode: 0,
    questions: qs, answers: [], authority: [], additional: [],
  };
}

pub function udp_payload(): i32 {
  return 4096;
}

pub function with_edns(m: Message): Message {
  let none: u8[] = [];
  let opt: Record = Record { name: "", rtype: type_opt(), rclass: udp_payload(), ttl: 0, data: Raw(none) };
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
  let out: u8[] = b.append((v >> 8 & 255) as u8);
  return out.append((v & 255) as u8);
}

function put32(b: u8[], v: i32): u8[] {
  let out: u8[] = put16(b, v >> 16 & 65535);
  return put16(out, v & 65535);
}
```

### `10 pub function encode_name(name: string): Result[u8[], DnsError]`

```fern
<<10 pub function encode_name(name: string): Result[u8[], DnsError]>>=
pub function encode_name(name: string): Result[u8[], DnsError] {
  let out: u8[] = [];
  let n: string = name;
  if (n.ends_with(".")) {
    n = slice_unchecked(n, 0, n.len() - 1) + "";
  }
  if (n.len() == 0) {
    return Ok(out.append(0 as u8));
  }
  let labels: string[] = n.split(".");
  let i: i32 = 0;
  while (i < labels.len()) {
    let l: string = labels[i];
    if (l.len() == 0 || l.len() > 63) {
      return Err(Malformed);
    }
    out = out.append(l.len() as u8);
    let j: i32 = 0;
    while (j < l.len()) {
      out = out.append(l[j]);
      j = j + 1;
    }
    i = i + 1;
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
  let name: u8[] = encode_name(q.name)?;
  let out: u8[] = b.concat(name);
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
  let name: u8[] = encode_name(r.name)?;
  let data: u8[] = encode_rdata(r.data)?;
  if (data.len() > 65535) {
    return Err(Malformed);
  }
  let out: u8[] = b.concat(name);
  out = put16(out, r.rtype);
  out = put16(out, r.rclass);
  out = put32(out, r.ttl);
  out = put16(out, data.len());
  return Ok(out.concat(data));
}

function encode_records(b: u8[], rs: Record[]): Result[u8[], DnsError] {
  let out: u8[] = b;
  let i: i32 = 0;
  while (i < rs.len()) {
    out = encode_record(out, rs[i])?;
    i = i + 1;
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
  let i: i32 = 0;
  while (i < m.questions.len()) {
    out = encode_question(out, m.questions[i])?;
    i = i + 1;
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
  let flags: i32 = (m.opcode & 15) << 11 | m.rcode & 15;
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
  let out: string = "";
  let total: i32 = 0;
  let p: i32 = pos;
  let next: i32 = -1;
  let hops: i32 = 0;
  while (p < msg.len()) {
    let l: i32 = msg[p] as i32;
    if (l == 0) {
      return Ok(NameAt { name: out, next: first_of(next, p + 1) });
    }
    if ((l & 192) == 192) {
      next = first_of(next, p + 2);
      hops = hops + 1;
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
  let target: i32 = (msg[p] as i32 & 63) << 8 | msg[p + 1] as i32;
  if (target >= p || hops > 127) {
    return Err(Malformed);
  }
  return Ok(target);
}

function join_label(out: string, msg: u8[], at: i32, len: i32): Option[string] {
  let label: u8[] = [];
  let k: i32 = 0;
  while (k < len) {
    label = label.append(msg[at + k]);
    k = k + 1;
  }
  match (utf8.from_bytes(label)) {
    Some(text) => {
      if (out.len() > 0) {
        return Some(out + "." + text);
      }
      return Some(text);
    },
    None => {
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
  let n: NameAt = decode_name(msg, pos)?;
  if (n.next + 4 > msg.len()) {
    return Err(Malformed);
  }
  let q: Question = Question { name: n.name, qtype: be16(msg, n.next), qclass: be16(msg, n.next + 2) };
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
  let k: i32 = 0;
  while (k < rdlen) {
    raw = raw.append(msg[at + k]);
    k = k + 1;
  }
  if (rclass != class_in()) {
    return Ok(Raw(raw));
  }
  if (rtype == type_cname() || rtype == type_ptr()) {
    return decode_name_rdata(msg, rtype, at, rdlen);
  }
  return decode_address_rdata(rtype, raw);
}

function decode_address_rdata(rtype: i32, raw: u8[]): Result[RData, DnsError] {
  if (rtype == type_a() && raw.len() == 4) {
    return Ok(A(net.ipv4(raw[0] as i32, raw[1] as i32, raw[2] as i32, raw[3] as i32)));
  }
  if (rtype == type_aaaa() && raw.len() == 16) {
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
  let n: NameAt = decode_name(msg, at)?;
  if (n.next > at + rdlen) {
    return Err(Malformed);
  }
  if (rtype == type_cname()) {
    return Ok(CNAME(n.name));
  }
  return Ok(PTR(n.name));
}

function decode_record(msg: u8[], pos: i32): Result[RecordAt, DnsError] {
  let n: NameAt = decode_name(msg, pos)?;
  let p: i32 = n.next;
  if (p + 10 > msg.len()) {
    return Err(Malformed);
  }
  let rtype: i32 = be16(msg, p);
  let rclass: i32 = be16(msg, p + 2);
  let ttl: i32 = be32(msg, p + 4);
  let rdlen: i32 = be16(msg, p + 8);
  p = p + 10;
  if (p + rdlen > msg.len()) {
    return Err(Malformed);
  }
  let data: RData = decode_rdata(msg, rtype, rclass, p, rdlen)?;
  let r: Record = Record { name: n.name, rtype: rtype, rclass: rclass, ttl: ttl, data: data };
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
  let p: i32 = pos;
  let i: i32 = 0;
  while (i < count) {
    let r: RecordAt = decode_record(msg, p)?;
    out = out.append(r.r);
    p = r.next;
    i = i + 1;
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
  let flags: i32 = be16(msg, 2);
  let qd: i32 = be16(msg, 4);
  let an: i32 = be16(msg, 6);
  let ns: i32 = be16(msg, 8);
  let ar: i32 = be16(msg, 10);
  let p: i32 = 12;
  let qs: Question[] = [];
  let i: i32 = 0;
  while (i < qd) {
    let q: QuestionAt = decode_question(msg, p)?;
    qs = qs.append(q.q);
    p = q.next;
    i = i + 1;
  }
  let answers: RecordsAt = decode_records(msg, p, an)?;
  let authority: RecordsAt = decode_records(msg, answers.next, ns)?;
  let additional: RecordsAt = decode_records(msg, authority.next, ar)?;
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
  let mq: Question = m.questions[0];
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
  let cur: string = name;
  let hops: i32 = 0;
  while (hops <= m.answers.len()) {
    let at: OwnerScan = scan_owner(m, cur, rtype);
    if (at.found.len() > 0 || at.alias.len() == 0) {
      return Answer { canonical: cur, records: at.found };
    }
    cur = at.alias;
    hops = hops + 1;
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
  let alias: string = "";
  let i: i32 = 0;
  while (i < m.answers.len()) {
    let r: Record = m.answers[i];
    i = i + 1;
    if (r.rclass != class_in() || !names_equal(r.name, owner)) {
      continue;
    }
    if (r.rtype == rtype) {
      found = found.append(r);
    } else if (r.rtype == type_cname() && alias.len() == 0) {
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
  let i: i32 = 0;
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
    i = i + 1;
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
  let lines: string[] = contents.lines();
  let i: i32 = 0;
  while (i < lines.len()) {
    let f: string[] = strip_comment(lines[i], "#").fields();
    i = i + 1;
    if (f.len() < 2) {
      continue;
    }
    match (net.ip_parse(f[0])) {
      Some(ip) => {
        out = out.append(HostsEntry { ip: ip, names: f.drop(1) });
      },
      None => {}
    }
  }
  return out;
}

pub function hosts_lookup(entries: HostsEntry[], name: string): net.IpAddr[] {
  let out: net.IpAddr[] = [];
  let i: i32 = 0;
  while (i < entries.len()) {
    if (entry_names(entries[i], name)) {
      out = out.append(entries[i].ip);
    }
    i = i + 1;
  }
  return out;
}

pub function hosts_canonical(entries: HostsEntry[], name: string): Option[string] {
  let i: i32 = 0;
  while (i < entries.len()) {
    if (entry_names(entries[i], name)) {
      return Some(entries[i].names[0]);
    }
    i = i + 1;
  }
  return None;
}
```

### `33 function entry_names(e: HostsEntry, name: string): boolean`

```fern
<<33 function entry_names(e: HostsEntry, name: string): boolean>>=
function entry_names(e: HostsEntry, name: string): boolean {
  let j: i32 = 0;
  while (j < e.names.len()) {
    if (names_equal(e.names[j], name)) {
      return true;
    }
    j = j + 1;
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

### `35 pub function dns_port(): i32`

```fern
<<35 pub function dns_port(): i32>>=
pub function dns_port(): i32 {
  return 53;
}

pub function resolv_defaults(host: string): ResolvConf {
  let ns: net.SocketAddr[] = [net.socket_addr(net.ipv4_loopback(), dns_port())];
  return ResolvConf { nameservers: ns, search: host_domain(host), ndots: 1, timeout: time.duration_seconds(5 as i64), attempts: 2, rotate: false };
}

pub function parse_resolv_conf(contents: string, host: string): ResolvConf {
  let rc: ResolvConf = resolv_defaults(host);
  let ns: net.SocketAddr[] = [];
  let have_search: boolean = false;
  let lines: string[] = contents.lines();
  let i: i32 = 0;
  while (i < lines.len()) {
    let f: string[] = strip_comment(strip_comment(lines[i], "#"), ";").fields();
    i = i + 1;
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
    Some(ip) => {
      return ns.append(net.socket_addr(ip, dns_port()));
    },
    None => {
      return ns;
    }
  }
  return ns;
}

function search_domains(f: string[]): string[] {
  let out: string[] = [];
  let n: i32 = f.len();
  if (f[0] == "domain") {
    n = 2;
  }
  let i: i32 = 1;
  while (i < n) {
    let d: string = f[i];
    if (d.ends_with(".")) {
      d = slice_unchecked(d, 0, d.len() - 1) + "";
    }
    if (d.len() > 0) {
      out = out.append(d);
    }
    i = i + 1;
  }
  return out;
}

function parse_options(f: string[], rc0: ResolvConf): ResolvConf {
  let rc: ResolvConf = rc0;
  let i: i32 = 1;
  while (i < f.len()) {
    let o: string = f[i];
    i = i + 1;
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
  let dots: i32 = 0;
  let i: i32 = 0;
  while (i < name.len()) {
    if (name[i] == 46) {
      dots = dots + 1;
    }
    i = i + 1;
  }
  let sfx: string[] = [];
  let j: i32 = 0;
  while (j < rc.search.len()) {
    sfx = sfx.append(name + "." + rc.search[j]);
    j = j + 1;
  }
  return SearchPlan { as_is: name, suffixes: sfx, leads: dots >= rc.ndots };
}

pub function search_candidates(name: string, rc: ResolvConf): string[] {
  let p: SearchPlan = search_plan(name, rc);
  let out: string[] = [];
  if (p.leads) {
    out = out.append(p.as_is);
  }
  let j: i32 = 0;
  while (j < p.suffixes.len()) {
    out = out.append(p.suffixes[j]);
    j = j + 1;
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
  let i: i32 = 0;
  while (i < n) {
    out = out.append(0 as u8);
    i = i + 1;
  }
  return out;
}

function now_ms(): i64 {
  return monotonic_ns() / 1000000i64;
}

function readable_within(fd: i32, wait_ms: i32): boolean {
  let set: i32[] = [fd, 1];
  return async.wait_any(set, wait_ms) == 0;
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
  let deadline: i64 = now_ms() + timeout.as_millis();
  let slots: Slot[] = [];
  let i: i32 = 0;
  while (i < qs.len()) {
    slots = slots.append(opened_slot(ns, qs[i]));
    i = i + 1;
  }
  slots = await_udp_replies(slots, deadline);
  let out: Result[Message, DnsError][] = [];
  i = 0;
  while (i < slots.len()) {
    if (slots[i].sock >= 0) {
      let closed: Result[void, net.NetError] = net.close(slots[i].sock);
    }
    out = out.append(slot_result(ns, slots[i], deadline));
    i = i + 1;
  }
  return out;
}
```

### `41 struct Slot`

```fern
<<41 struct Slot>>=
struct Slot { q: Message, sock: i32, reply: Option[Message], err: DnsError }
```

### `42 function waiting(s: Slot): boolean`

```fern
<<42 function waiting(s: Slot): boolean>>=
function waiting(s: Slot): boolean {
  if (s.sock < 0) {
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
      return Slot { q: q, sock: 0 - 1, reply: None, err: e };
    }
  }
  match (send_udp(ns, bytes)) {
    Ok(sock) => {
      return Slot { q: q, sock: sock, reply: None, err: NoReply };
    },
    Err(e) => {
      return Slot { q: q, sock: 0 - 1, reply: None, err: e };
    }
  }
  return Slot { q: q, sock: 0 - 1, reply: None, err: NoReply };
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

function send_udp(ns: net.SocketAddr, bytes: u8[]): Result[i32, DnsError] {
  let sock: i32 = 0;
  match (net.udp_socket(local_for(ns))) {
    Ok(s) => {
      sock = s;
    },
    Err(e) => {
      return Err(Socket(e));
    }
  }
  match (net.send_to(sock, bytes, ns)) {
    Ok(_) => {
      return Ok(sock);
    },
    Err(e) => {
      let closed: Result[void, net.NetError] = net.close(sock);
      return Err(Socket(e));
    }
  }
  return Ok(sock);
}

function pending_slots(slots: Slot[]): i32[] {
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < slots.len()) {
    if (waiting(slots[i])) {
      out = out.append(i);
    }
    i = i + 1;
  }
  return out;
}

function await_udp_replies(slots: Slot[], deadline: i64): Slot[] {
  let out: Slot[] = slots;
  let pending: i32[] = pending_slots(out);
  let left: i32 = (deadline - now_ms()) as i32;
  while (pending.len() > 0 && left > 0) {
    let ready: i32 = first_readable(out, pending, left);
    if (ready < 0) {
      return out;
    }
    out = taken(out, pending[ready]);
    pending = pending_slots(out);
    left = (deadline - now_ms()) as i32;
  }
  return out;
}

function taken(slots: Slot[], k: i32): Slot[] {
  match (take_reply(slots[k].sock, slots[k].q)) {
    Ok(found) => {
      match (found) {
        Some(reply) => {
          return slots.with(k, Slot { ...slots[k], reply: Some(reply) });
        },
        None => {
          return slots;
        }
      }
    },
    Err(e) => {
      return slots.with(k, Slot { ...slots[k], sock: closed_slot(slots[k].sock), err: e });
    }
  }
  return slots;
}

function closed_slot(sock: i32): i32 {
  let closed: Result[void, net.NetError] = net.close(sock);
  return 0 - 1;
}

function first_readable(slots: Slot[], pending: i32[], wait_ms: i32): i32 {
  let set: i32[] = [];
  let i: i32 = 0;
  while (i < pending.len()) {
    set = set.append(slots[pending[i]].sock);
    set = set.append(1);
    i = i + 1;
  }
  return async.wait_any(set, wait_ms);
}
```

### `43 function take_reply(sock: i32, q: Message): Result[Option[Message], DnsError]`

```fern
<<43 function take_reply(sock: i32, q: Message): Result[Option[Message], DnsError]>>=
function take_reply(sock: i32, q: Message): Result[Option[Message], DnsError] {
  let buf: u8[] = zero_bytes(udp_payload());
  let n: i32 = 0;
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
          return Err(Socket(e));
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
  let bytes: u8[] = encode(q)?;
  let framed: u8[] = [];
  framed = put16(framed, bytes.len());
  let sock: i32 = send_tcp(ns, framed.concat(bytes))?;
  let got: Result[u8[], DnsError] = read_framed(sock, now_ms() + timeout.as_millis());
  tcp_close(sock);
  let reply: Message = decode(got?)?;
  if (reply.id != q.id || !reply.response || !reply.answers_question(q.questions[0])) {
    return Err(Malformed);
  }
  return Ok(reply);
}
```

### `45 function send_tcp(ns: net.SocketAddr, framed: u8[]): Result[i32, DnsError]`

```fern
<<45 function send_tcp(ns: net.SocketAddr, framed: u8[]): Result[i32, DnsError]>>=
function send_tcp(ns: net.SocketAddr, framed: u8[]): Result[i32, DnsError] {
  let sock: i32 = 0;
  match (net.connect(ns)) {
    Ok(s) => {
      sock = s;
    },
    Err(e) => {
      return Err(Socket(e));
    }
  }
  let remaining: u8[] = framed;
  while (remaining.len() > 0) {
    let sent: i32 = tcp_send_bytes(sock, remaining);
    if (sent < 0) {
      let failure: net.NetError = net.error_from_host(sent);
      if (!failure.eq(net.Interrupted)) {
        tcp_close(sock);
        return Err(Socket(failure));
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

function read_framed(sock: i32, deadline: i64): Result[u8[], DnsError] {
  let buf: u8[] = [];
  let want: i32 = -1;
  while (want < 0 || buf.len() < want) {
    let left: i32 = (deadline - now_ms()) as i32;
    if (left <= 0 || !readable_within(sock, left)) {
      return Err(NoReply);
    }
    buf = buf.concat(read_some(sock)?);
    if (want < 0 && buf.len() >= 2) {
      want = 2 + be16(buf, 0);
    }
  }
  return Ok(buf.drop(2).take(want - 2));
}

function read_some(sock: i32): Result[u8[], DnsError] {
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
      return Err(Socket(e));
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
  let n: i32 = conf.nameservers.len();
  let asked: Asked[] = [];
  let i: i32 = 0;
  while (i < qs.len()) {
    asked = asked.append(Asked { reply: None, tally: Tally { servfail: false, refused: false, silent: false, last: NoNameserver } });
    i = i + 1;
  }
  if (n > 0) {
    let start: i32 = 0;
    if (conf.rotate) {
      start = (random_i32() & 2147483647) % n;
    }
    let round: i32 = 0;
    while (round < conf.attempts) {
      let k: i32 = 0;
      while (k < n) {
        asked = ask_server(conf.nameservers[(start + k) % n], qs, asked, conf.timeout);
        k = k + 1;
      }
      round = round + 1;
    }
  }
  let out: Result[Message, DnsError][] = [];
  i = 0;
  while (i < asked.len()) {
    out = out.append(asked_result(asked[i]));
    i = i + 1;
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
  let i: i32 = 0;
  while (i < asked.len()) {
    if (unanswered(asked[i])) {
      open = open.append(i);
      open_qs = open_qs.append(qs[i]);
    }
    i = i + 1;
  }
  if (open.len() == 0) {
    return asked;
  }
  let replies: Result[Message, DnsError][] = exchange_many(ns, open_qs, timeout);
  let out: Asked[] = asked;
  i = 0;
  while (i < open.len()) {
    out = out.with(open[i], noted(out[open[i]], replies[i]));
    i = i + 1;
  }
  return out;
}

function noted(a: Asked, r: Result[Message, DnsError]): Asked {
  match (r) {
    Ok(reply) => {
      if (reply.rcode != rcode_server_failure() && reply.rcode != rcode_refused()) {
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
  if (rcode == rcode_server_failure()) {
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
  if (reply.rcode == rcode_name_error()) {
    return Err(NoSuchName);
  }
  if (reply.rcode == rcode_server_failure()) {
    return Err(ServerFailure);
  }
  if (reply.rcode == rcode_refused()) {
    return Err(Refused);
  }
  if (reply.rcode != rcode_ok()) {
    return Err(Malformed);
  }
  let a: Answer = records_for(reply, qname, rtype);
  if (a.records.len() == 0) {
    return Err(NoData);
  }
  return Ok(a);
}

pub function lookup_records_with(conf: ResolvConf, name: string, rtype: i32, asker: (string) => Result[Answer, DnsError]): Result[Answer, DnsError] {
  return search_with(search_plan(name, conf), asker);
}

pub function search_with(plan: SearchPlan, asker: (string) => Result[Answer, DnsError]): Result[Answer, DnsError] {
  let lead_failed: boolean = false;
  let lead_err: DnsError = NoSuchName;
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
  let sfx: Suffixes = search_suffixes(plan.suffixes, asker);
  match (sfx.answer) {
    Some(a) => {
      return Ok(a);
    },
    None => {}
  }
  if (sfx.refused) {
    return Err(Refused);
  }
  let last: DnsError = sfx.last;
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
  let out: Suffixes = Suffixes { answer: None, refused: false, servfail: false, last: NoSuchName };
  let i: i32 = 0;
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
    i = i + 1;
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
  let reply: Message = ask(conf, query(new_id(), qname, rtype))?;
  return answer_records(reply, qname, rtype);
}
```

### `57 pub function lookup_a(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError]`

```fern
<<57 pub function lookup_a(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError]>>=
pub function lookup_a(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError] {
  let a: Answer = lookup_records(conf, name, type_a())?;
  return Ok(addresses_of(a.records));
}

pub function lookup_aaaa(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError] {
  let a: Answer = lookup_records(conf, name, type_aaaa())?;
  return Ok(addresses_of(a.records));
}

pub function canonical_name(conf: ResolvConf, name: string): Result[string, DnsError] {
  let a: Answer = lookup_records(conf, name, type_a())?;
  return Ok(a.canonical);
}

pub function lookup_addresses(conf: ResolvConf, name: string): Result[net.IpAddr[], DnsError] {
  let found: net.IpAddr[] = found_addresses(conf, name)?;
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
  let replies: Result[Message, DnsError][] = ask_many(conf, [query(new_id(), qname, type_a()), query(new_id(), qname, type_aaaa())]);
  return combine_pair(reply_records(replies[0], qname, type_a()), reply_records(replies[1], qname, type_aaaa()));
}

function reply_records(r: Result[Message, DnsError], qname: string, rtype: i32): Result[Answer, DnsError] {
  let reply: Message = r?;
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
  let b: u8[] = ip.bytes();
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
  let n: i32 = 0;
  while (n < b.len() && b[n] == 0u8) {
    n = n + 1;
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
  let ab: u8[] = a.bytes();
  let bb: u8[] = b.bytes();
  let bits: i32 = 0;
  let i: i32 = 0;
  while (i < 8) {
    let x: i32 = (ab[i] ^ bb[i]) as i32;
    if (x != 0) {
      let m: i32 = 128;
      while (m > 0 && (x & m) == 0) {
        bits = bits + 1;
        m = m >> 1;
      }
      return bits;
    }
    bits = bits + 8;
    i = i + 1;
  }
  return bits;
}

pub function source_for(dst: net.IpAddr): Option[net.IpAddr] {
  let probe: net.SocketAddr = net.socket_addr(dst, 9);
  let sock: i32 = 0;
  match (net.udp_socket(local_for(probe))) {
    Ok(s) => {
      sock = s;
    },
    Err(_) => {
      return None;
    }
  }
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
  let closed: Result[void, net.NetError] = net.close(sock);
  return found;
}

pub function order_addresses(dsts: net.IpAddr[], srcs: Option[net.IpAddr][]): net.IpAddr[] {
  let out: net.IpAddr[] = [];
  let taken: boolean[] = [];
  let i: i32 = 0;
  while (i < dsts.len()) {
    taken = taken.append(false);
    i = i + 1;
  }
  let placed: i32 = 0;
  while (placed < dsts.len()) {
    let best: i32 = 0 - 1;
    i = 0;
    while (i < dsts.len()) {
      if (!taken[i] && (best < 0 || prefers(dsts[i], srcs[i], dsts[best], srcs[best]) < 0)) {
        best = i;
      }
      i = i + 1;
    }
    taken = taken.with(best, true);
    out = out.append(dsts[best]);
    placed = placed + 1;
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
  let i: i32 = 0;
  while (i < dsts.len()) {
    srcs = srcs.append(src(dsts[i]));
    i = i + 1;
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
          return 0 - 1;
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
  let scope_a: i32 = scope_of(a);
  let scope_b: i32 = scope_of(b);
  let r: i32 = prefer_true(scope_a == scope_of(sa), scope_b == scope_of(sb));
  if (r != 0) {
    return r;
  }
  let pa: Policy = policy_of(a);
  let pb: Policy = policy_of(b);
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
    return 0 - 1;
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
  let lead_v6: boolean = addrs[0].is_v6();
  let lead: net.IpAddr[] = [];
  let other: net.IpAddr[] = [];
  let i: i32 = 0;
  while (i < addrs.len()) {
    if (addrs[i].is_v6() == lead_v6) {
      lead = lead.append(addrs[i]);
    } else {
      other = other.append(addrs[i]);
    }
    i = i + 1;
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
    i = i + 1;
  }
  return out;
}
```

### `70 struct Attempt`

```fern
<<70 struct Attempt>>=
struct Attempt { sock: i32 }
```

### `71 pub function connect_race(addrs: net.IpAddr[], port: i32, opts: DialOptions): Result[i32, net.NetError]`

```fern
<<71 pub function connect_race(addrs: net.IpAddr[], port: i32, opts: DialOptions): Result[i32, net.NetError]>>=
pub function connect_race(addrs: net.IpAddr[], port: i32, opts: DialOptions): Result[i32, net.NetError] {
  let deadline: i64 = now_ms() + opts.timeout.as_millis();
  let race: Race = Race { open: [], next: 0, due: now_ms(), last: HostUnreachable };
  while (true) {
    let now: i64 = now_ms();
    if (now >= deadline) {
      close_attempts(race.open);
      return Err(TimedOut);
    }
    if (race.next < addrs.len() && (race.open.len() == 0 || now >= race.due)) {
      race = started(race, net.socket_addr(addrs[race.next], port), opts);
      continue;
    }
    if (race.open.len() == 0) {
      return Err(race.last);
    }
    let woke: i32 = async.wait_any(writable(race.open), wait_window(race, addrs.len(), now, deadline));
    if (woke == async.cancelled()) {
      close_attempts(race.open);
      return Err(net.Interrupted);
    }
    match (settled(race)) {
      Ok(sock) => {
        let blocking: Result[void, net.NetError] = net.set_nonblocking(sock, false);
        return Ok(sock);
      },
      Err(after) => {
        race = after;
      }
    }
  }
  return Err(TimedOut);
}
```

### `72 function wait_window(race: Race, n: i32, now: i64, deadline: i64): i32`

```fern
<<72 function wait_window(race: Race, n: i32, now: i64, deadline: i64): i32>>=
function wait_window(race: Race, n: i32, now: i64, deadline: i64): i32 {
  let wait_ms: i32 = (deadline - now) as i32;
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
struct Race { open: Attempt[], next: i32, due: i64, last: net.NetError }
```

### `74 function writable(attempts: Attempt[]): i32[]`

```fern
<<74 function writable(attempts: Attempt[]): i32[]>>=
function writable(attempts: Attempt[]): i32[] {
  let set: i32[] = [];
  let i: i32 = 0;
  while (i < attempts.len()) {
    set = set.append(attempts[i].sock);
    set = set.append(2);
    i = i + 1;
  }
  return set;
}

function started(race: Race, addr: net.SocketAddr, opts: DialOptions): Race {
  let next: i32 = race.next + 1;
  match (net.connect_start(addr)) {
    Ok(sock) => {
      return Race { open: race.open.append(Attempt { sock: sock }), next: next, due: now_ms() + opts.fallback.as_millis(), last: race.last };
    },
    Err(e) => {
      return Race { ...race, next: next, due: now_ms(), last: e };
    }
  }
  return Race { ...race, next: next };
}

function settled(race: Race): Result[i32, Race] {
  let keep: Attempt[] = [];
  let out: Race = race;
  let i: i32 = 0;
  while (i < race.open.len()) {
    let a: Attempt = race.open[i];
    match (net.connect_result(a.sock)) {
      Ok(_) => {
        close_attempts(keep);
        close_attempts(after(race.open, i));
        return Ok(a.sock);
      },
      Err(e) => {
        if (e.eq(net.InProgress)) {
          keep = keep.append(a);
        } else {
          close_attempts([a]);
          out = Race { ...out, due: now_ms(), last: e };
        }
      }
    }
    i = i + 1;
  }
  return Err(Race { ...out, open: keep });
}

function after(attempts: Attempt[], i: i32): Attempt[] {
  let out: Attempt[] = [];
  let k: i32 = i + 1;
  while (k < attempts.len()) {
    out = out.append(attempts[k]);
    k = k + 1;
  }
  return out;
}

function close_attempts(attempts: Attempt[]): void {
  let i: i32 = 0;
  while (i < attempts.len()) {
    let closed: Result[void, net.NetError] = net.close(attempts[i].sock);
    i = i + 1;
  }
}
```

### `75 pub function dial(name: string, port: i32, opts: DialOptions): Result[i32, DnsError]`

```fern
<<75 pub function dial(name: string, port: i32, opts: DialOptions): Result[i32, DnsError]>>=
pub function dial(name: string, port: i32, opts: DialOptions): Result[i32, DnsError] {
  let addrs: net.IpAddr[] = resolve(name)?;
  match (connect_race(interleave_families(addrs), port, opts)) {
    Ok(sock) => {
      return Ok(sock);
    },
    Err(e) => {
      return Err(Socket(e));
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
  let b: u8[] = aaaa.bytes();
  let lengths: i32[] = [96, 64, 56, 48, 40, 32];
  let i: i32 = 0;
  while (i < lengths.len()) {
    match (aaaa.embedded_v4(lengths[i])) {
      Some(v4) => {
        if (is_well_known(v4.bytes())) {
          return Some(Nat64Prefix { bytes: b.take(lengths[i] / 8).concat(zero_bytes(16 - lengths[i] / 8)), bits: lengths[i] });
        }
      },
      None => {}
    }
    i = i + 1;
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
  let src: u8[] = v4.bytes();
  let out: u8[] = prefix.bytes.take(prefix.bits / 8);
  let k: i32 = 0;
  while (out.len() < 16) {
    if (out.len() != 8 && k < 4) {
      out = out.append(src[k]);
      k = k + 1;
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
      let i: i32 = 0;
      while (i < xs.len()) {
        match (nat64_prefix_of(xs[i])) {
          Some(p) => {
            if (!has_prefix(out, p)) {
              out = out.append(p);
            }
          },
          None => {}
        }
        i = i + 1;
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
  let b: u8[] = addr.bytes();
  let known: Nat64Prefix[] = prefixes();
  for p in known {
    if (same_bytes(b.take(p.bits / 8), p.bytes.take(p.bits / 8))) {
      return addr.embedded_v4(p.bits);
    }
  }
  return None;
}

function has_prefix(ps: Nat64Prefix[], p: Nat64Prefix): boolean {
  let i: i32 = 0;
  while (i < ps.len()) {
    if (ps[i].bits == p.bits && same_bytes(ps[i].bytes, p.bytes)) {
      return true;
    }
    i = i + 1;
  }
  return false;
}

function same_bytes(a: u8[], b: u8[]): boolean {
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

function with_nat64(conf: ResolvConf, addrs: net.IpAddr[]): net.IpAddr[] {
  return nat64_by(addrs, source_for, (): Nat64Prefix[] => nat64_prefixes(conf));
}

function nat64_by(addrs: net.IpAddr[], src: (net.IpAddr) => Option[net.IpAddr], prefixes: () => Nat64Prefix[]): net.IpAddr[] {
  let i: i32 = 0;
  while (i < addrs.len()) {
    if (addrs[i].is_v6()) {
      return addrs;
    }
    i = i + 1;
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
  let found: Nat64Prefix[] = prefixes();
  if (found.len() == 0) {
    return addrs;
  }
  let out: net.IpAddr[] = [];
  i = 0;
  while (i < addrs.len()) {
    let k: i32 = 0;
    while (k < found.len()) {
      out = out.append(synthesize(found[k], addrs[i]));
      k = k + 1;
    }
    i = i + 1;
  }
  return out;
}
```

### `81 pub function system_conf(): ResolvConf`

```fern
<<81 pub function system_conf(): ResolvConf>>=
pub function system_conf(): ResolvConf {
  let contents: string = "";
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
  let conf: ResolvConf = system_conf();
  return Ok(reachable(conf, resolve_with(conf, name)?));
}

pub function resolve_plain(name: string): Result[net.IpAddr[], DnsError] {
  match (net.ip_parse(name)) {
    Some(ip) => {
      return Ok([ip]);
    },
    None => {}
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
  let s: Seen = r.seen.get();
  let now: i64 = monotonic_ns();
  if (s.at >= 0 as i64 && now - s.at < RECHECK_NS) {
    return s;
  }
  let routes: Route[] = [];
  let read: Seen = Seen { at: now, conf: system_conf(), hosts: system_hosts(), nat64: None, routes: routes };
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
  let s: Seen = r.fresh();
  for route in s.routes {
    if (route.dst.eq(dst)) {
      return route.src;
    }
  }
  let src: Option[net.IpAddr] = source_for(dst);
  let kept: Route[] = s.routes;
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
  let s: Seen = r.fresh();
  if let Some(p) = s.nat64 {
    return p;
  }
  let found: Nat64Prefix[] = nat64_prefixes(s.conf);
  r.seen.set(Seen { ...r.seen.get(), nat64: Some(found) });
  return found;
}
```

### `88 pub function (r: Resolver) resolve_plain(name: string): Result[net.IpAddr[], DnsError]`

```fern
<<88 pub function (r: Resolver) resolve_plain(name: string): Result[net.IpAddr[], DnsError]>>=
pub function (r: Resolver) resolve_plain(name: string): Result[net.IpAddr[], DnsError] {
  match (net.ip_parse(name)) {
    Some(ip) => {
      return Ok([ip]);
    },
    None => {}
  }
  let s: Seen = r.fresh();
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
    Some(ip) => {
      return Ok([ip]);
    },
    None => {}
  }
  let hosts: net.IpAddr[] = hosts_lookup(hosts_of(), name);
  if (hosts.len() > 0) {
    return Ok(hosts);
  }
  return found_addresses(conf, name);
}
```
