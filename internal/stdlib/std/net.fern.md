# Network address values

This module provides IP addresses, socket addresses, and network error types.

## Model

IP address values distinguish IPv4 from IPv6. A socket address combines an IP address and a port. Prefix values describe address blocks.

## Contracts

Text parsers return optional or error results for invalid addresses. Network failures retain a concrete error kind instead of only an operating-system message.

## Executable example

```fern test name=std-net-example
import "std/net";

function main(): i32 {
  if (net.ipv4(127, 0, 0, 1).to_string() != "127.0.0.1") { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum IpAddr>>

<<4 function __zero_bytes(n: i32): u8[]>>

<<5 pub function ipv4(a: i32, b: i32, c: i32, d: i32): IpAddr>>

<<6 function __all_zero(b: u8[], from: i32, to: i32): boolean>>

<<7 pub function (a: IpAddr) is_unspecified(): boolean>>

<<8 function __nat64_bits(bits: i32): boolean>>

<<9 pub function (a: IpAddr) packed_v4(): Option[i32]>>

<<10 impl cmp.Eq for IpAddr>>

<<11 function __v4_text(b: u8[]): string>>

<<12 pub function (a: IpAddr) to_string(): string>>

<<13 impl cmp.Eq for SocketAddr>>

<<14 pub function prefix_parse(s: string): Option[Prefix]>>

<<15 function __prefix_bits(s: string, width: i32): i32>>

<<16 pub function (p: Prefix) contains(ip: IpAddr): boolean>>

<<17 function __in_family(ip: IpAddr, v4: boolean): u8[]>>

<<18 pub function (p: Prefix) to_string(): string>>

<<19 function __pieces(s: string, sep: i32): string[]>>

<<20 pub function ipv4_parse(s: string): Option[IpAddr]>>

<<21 function __hex_digit(c: i32): i32>>

<<22 pub function ipv6_parse(s: string): Option[IpAddr]>>

<<23 function __port(s: string): i32>>

<<24 pub function socket_addr_parse(s: string): Option[SocketAddr]>>

<<25 function __net_errnos(): i32[]>>

<<26 pub function error_from_host(n: i32): NetError>>

<<27 function __net_error_index(e: NetError): i32>>

<<28 pub function (e: NetError) errno(): i32>>

<<29 function __net_error_message_high(e: NetError): string>>

<<30 pub function (e: NetError) to_string(): string>>

<<31 pub struct ListenOptions>>

<<32 pub function listen_options(): ListenOptions>>

<<33 pub enum Recv>>

<<34 pub function read_deadline(sock: i32, max: i32, deadline: Duration): Result[Recv, NetError]>>

<<35 pub enum Shutdown>>

<<36 pub function set_nodelay(sock: i32, on: boolean): Result[void, NetError]>>

<<37 function __socket_ctl(sock: i32, op: i32, on: boolean): Result[void, NetError]>>

<<38 pub function udp_socket(addr: SocketAddr): Result[i32, NetError]>>

<<39 function __sender(from: u8[]): SocketAddr>>

<<40 pub function recv_from(sock: i32, buf: u8[]): Result[(i32, SocketAddr), NetError]>>

<<41 function __tcp_socket(r: i32): Result[i32, NetError]>>

<<42 pub function listen_unix(path: string, backlog: i32): Result[i32, NetError]>>

<<43 function __name_of(sock: i32, op: i32): Result[SocketAddr, NetError]>>

<<44 pub function local_port(sock: i32): Result[i32, NetError]>>

<<45 impl cmp.Eq for NetError>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides IP addresses, socket addresses, and network error types.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/i32";
import "std/errno";
import "std/error";
import "std/time";
import "std/async";
import "core/cmp";
```

### `3 pub enum IpAddr`

```fern
<<3 pub enum IpAddr>>=
pub enum IpAddr {
  V4(u8[]),
  V6(u8[]),
}

pub struct SocketAddr {
  ip: IpAddr,
  port: i32,
}

pub enum NetError {
  AddrInUse,
  AddrNotAvailable,
  ConnectionRefused,
  ConnectionReset,
  ConnectionAborted,
  TimedOut,
  WouldBlock,
  InProgress,
  NetworkUnreachable,
  HostUnreachable,
  Interrupted,
  Other(i32),
}
```

### `4 function __zero_bytes(n: i32): u8[]`

```fern
<<4 function __zero_bytes(n: i32): u8[]>>=
function __zero_bytes(n: i32): u8[] {
  let out: u8[] = [];
  let i: i32 = 0;
  while (i < n) {
    out = out.append(0 as u8);
    i = i + 1;
  }
  return out;
}
```

### `5 pub function ipv4(a: i32, b: i32, c: i32, d: i32): IpAddr`

```fern
<<5 pub function ipv4(a: i32, b: i32, c: i32, d: i32): IpAddr>>=
pub function ipv4(a: i32, b: i32, c: i32, d: i32): IpAddr {
  let out: u8[] = [];
  out = out.append((a & 255) as u8);
  out = out.append((b & 255) as u8);
  out = out.append((c & 255) as u8);
  out = out.append((d & 255) as u8);
  return V4(out);
}

pub function ipv6(bytes: u8[]): Option[IpAddr] {
  if (bytes.len() != 16) {
    return None;
  }
  return Some(V6(bytes));
}

pub function ipv4_unspecified(): IpAddr {
  return ipv4(0, 0, 0, 0);
}

pub function ipv6_unspecified(): IpAddr {
  return V6(__zero_bytes(16));
}

pub function ipv4_loopback(): IpAddr {
  return ipv4(127, 0, 0, 1);
}

pub function ipv6_loopback(): IpAddr {
  let b: u8[] = __zero_bytes(15);
  return V6(b.append(1 as u8));
}

pub function socket_addr(ip: IpAddr, port: i32): SocketAddr {
  return SocketAddr { ip: ip, port: port };
}

pub function (a: IpAddr) bytes(): u8[] {
  match (a) {
    V4(b) => {
      return b;
    },
    V6(b) => {
      return b;
    }
  }
}

pub function (a: IpAddr) is_v4(): boolean {
  match (a) {
    V4(b) => {
      return true;
    },
    V6(b) => {
      return false;
    }
  }
}

pub function (a: IpAddr) is_v6(): boolean {
  return !a.is_v4();
}
```

### `6 function __all_zero(b: u8[], from: i32, to: i32): boolean`

```fern
<<6 function __all_zero(b: u8[], from: i32, to: i32): boolean>>=
function __all_zero(b: u8[], from: i32, to: i32): boolean {
  let i: i32 = from;
  while (i < to) {
    if (b[i] != 0) {
      return false;
    }
    i = i + 1;
  }
  return true;
}
```

### `7 pub function (a: IpAddr) is_unspecified(): boolean`

```fern
<<7 pub function (a: IpAddr) is_unspecified(): boolean>>=
pub function (a: IpAddr) is_unspecified(): boolean {
  let b: u8[] = a.bytes();
  return __all_zero(b, 0, b.len());
}

pub function (a: IpAddr) is_loopback(): boolean {
  match (a) {
    V4(b) => {
      return b[0] == 127;
    },
    V6(b) => {
      return __all_zero(b, 0, 15) && b[15] == 1;
    }
  }
}

pub function (a: IpAddr) is_private(): boolean {
  match (a) {
    V4(b) => {
      if (b[0] == 10) {
        return true;
      }
      if (b[0] == 172 && (b[1] & 240) == 16) {
        return true;
      }
      return b[0] == 192 && b[1] == 168;
    },
    V6(b) => {
      return (b[0] & 254) == 252;
    }
  }
}

pub function (a: IpAddr) is_link_local(): boolean {
  match (a) {
    V4(b) => {
      return b[0] == 169 && b[1] == 254;
    },
    V6(b) => {
      return b[0] == 254 && (b[1] & 192) == 128;
    }
  }
}

pub function (a: IpAddr) is_multicast(): boolean {
  match (a) {
    V4(b) => {
      return (b[0] & 240) == 224;
    },
    V6(b) => {
      return b[0] == 255;
    }
  }
}

pub function (a: IpAddr) is_v4_mapped(): boolean {
  match (a) {
    V4(b) => {
      return false;
    },
    V6(b) => {
      return __all_zero(b, 0, 10) && b[10] == 255 && b[11] == 255;
    }
  }
}

pub function (a: IpAddr) to_canonical(): IpAddr {
  if (!a.is_v4_mapped()) {
    return a;
  }
  let b: u8[] = a.bytes();
  return ipv4(b[12] as i32, b[13] as i32, b[14] as i32, b[15] as i32);
}

pub function (a: IpAddr) is_global(): boolean {
  match (a) {
    V4(b) => {
      return !__v4_special(b);
    },
    V6(b) => {
      match (__carried_v4(b)) {
        Some(inner) => {
          return !__v4_special(inner);
        },
        None => {
          if (__local_nat64(b)) {
            return __local_nat64_global(b);
          }
          return !__v6_special(b);
        }
      }
    }
  }
  return false;
}

pub function (a: IpAddr) embedded_v4(bits: i32): Option[IpAddr] {
  match (a) {
    V4(b) => {
      return None;
    },
    V6(b) => {
      if (!__nat64_bits(bits)) {
        return None;
      }
      return Some(V4(__nat64_bytes(b, bits)));
    }
  }
  return None;
}
```

### `8 function __nat64_bits(bits: i32): boolean`

```fern
<<8 function __nat64_bits(bits: i32): boolean>>=
function __nat64_bits(bits: i32): boolean {
  return bits == 96 || bits == 64 || bits == 56 || bits == 48 || bits == 40 || bits == 32;
}

function __nat64_bytes(b: u8[], bits: i32): u8[] {
  let out: u8[] = [];
  let at: i32 = bits / 8;
  while (out.len() < 4) {
    if (at != 8) {
      out = out.append(b[at]);
    }
    at = at + 1;
  }
  return out;
}

function __local_nat64(b: u8[]): boolean {
  return b[0] == 0 && b[1] == 100 && b[2] == 255 && b[3] == 155 && b[4] == 0 && b[5] == 1;
}

function __local_nat64_global(b: u8[]): boolean {
  let lengths: i32[] = [48, 56, 64, 96];
  let i: i32 = 0;
  while (i < lengths.len()) {
    if (__nat64_laid_out(b, lengths[i]) && __v4_special(__nat64_bytes(b, lengths[i]))) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __nat64_laid_out(b: u8[], bits: i32): boolean {
  let start: i32 = bits / 8;
  let end: i32 = start + 4;
  if (start <= 8) {
    end = end + 1;
    if (b[8] != 0) {
      return false;
    }
  }
  return __all_zero(b, end, 16) && !__all_zero(__nat64_bytes(b, bits), 0, 4);
}

function __v4_special(b: u8[]): boolean {
  return __v4_low_blocks(b) || __v4_doc_blocks(b) || (b[0] & 240) >= 224;
}

function __v4_low_blocks(b: u8[]): boolean {
  if (b[0] == 0 || b[0] == 10 || b[0] == 127) {
    return true;
  }
  if (b[0] == 100 && (b[1] & 192) == 64) {
    return true;
  }
  if (b[0] == 169 && b[1] == 254) {
    return true;
  }
  return b[0] == 172 && (b[1] & 240) == 16;
}

function __v4_doc_blocks(b: u8[]): boolean {
  if (b[0] == 192) {
    return b[1] == 168 || b[1] == 0 && (b[2] == 0 || b[2] == 2);
  }
  if (b[0] == 198) {
    return (b[1] & 254) == 18 || b[1] == 51 && b[2] == 100;
  }
  return b[0] == 203 && b[1] == 0 && b[2] == 113;
}

function __v6_special(b: u8[]): boolean {
  if (__all_zero(b, 0, 15)) {
    return b[15] == 0 || b[15] == 1;
  }
  if ((b[0] & 254) == 252 || b[0] == 255) {
    return true;
  }
  if (b[0] == 254 && (b[1] & 192) == 128) {
    return true;
  }
  return b[0] == 32 && b[1] == 1 && b[2] == 13 && b[3] == 184;
}

function __carried_v4(b: u8[]): Option[u8[]] {
  if (__all_zero(b, 0, 10) && b[10] == 255 && b[11] == 255) {
    return Some([b[12], b[13], b[14], b[15]]);
  }
  let head: i32 = b[0] as i32 << 24 | b[1] as i32 << 16 | b[2] as i32 << 8 | b[3] as i32;
  if (head == 6619035 && __all_zero(b, 4, 12)) {
    return Some([b[12], b[13], b[14], b[15]]);
  }
  if (head >> 16 == 8194) {
    return Some([b[2], b[3], b[4], b[5]]);
  }
  return None;
}
```

### `9 pub function (a: IpAddr) packed_v4(): Option[i32]`

```fern
<<9 pub function (a: IpAddr) packed_v4(): Option[i32]>>=
pub function (a: IpAddr) packed_v4(): Option[i32] {
  match (a) {
    V4(b) => {
      let p: i32 = b[0] as i32 | b[1] as i32 << 8 | b[2] as i32 << 16 | b[3] as i32 << 24;
      return Some(p);
    },
    V6(b) => {
      return None;
    }
  }
}

pub function (a: IpAddr) eq(other: IpAddr): boolean {
  if (a.is_v4() != other.is_v4()) {
    return false;
  }
  let x: u8[] = a.bytes();
  let y: u8[] = other.bytes();
  let i: i32 = 0;
  while (i < x.len()) {
    if (x[i] != y[i]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}
```

### `10 impl cmp.Eq for IpAddr`

```fern
<<10 impl cmp.Eq for IpAddr>>=
impl cmp.Eq for IpAddr {}

impl cmp.Display for IpAddr {}
```

### `11 function __v4_text(b: u8[]): string`

```fern
<<11 function __v4_text(b: u8[]): string>>=
function __v4_text(b: u8[]): string {
  return (b[0] as i32).to_string() + "." + (b[1] as i32).to_string() + "." + (b[2] as i32).to_string() + "." + (b[3] as i32).to_string();
}

function __v6_groups(b: u8[]): i32[] {
  let g: i32[] = [];
  let i: i32 = 0;
  while (i < 8) {
    g = g.append(b[2 * i] as i32 << 8 | b[2 * i + 1] as i32);
    i = i + 1;
  }
  return g;
}

function __v6_zero_run(g: i32[]): (i32, i32) {
  let best_start: i32 = -1;
  let best_len: i32 = 0;
  let i: i32 = 0;
  while (i < g.len()) {
    let j: i32 = i;
    while (j < g.len() && g[j] == 0) {
      j = j + 1;
    }
    if (j - i > best_len) {
      best_start = i;
      best_len = j - i;
    }
    if (j == i) {
      i = i + 1;
    } else {
      i = j;
    }
  }
  if (best_len < 2) {
    return (-1, 0);
  }
  return (best_start, best_len);
}

function __v6_join(g: i32[], from: i32, to: i32): string {
  let out: string = "";
  let i: i32 = from;
  while (i < to) {
    if (i > from) {
      out = out + ":";
    }
    out = out + g[i].to_string_radix(16);
    i = i + 1;
  }
  return out;
}

function __v6_text(b: u8[]): string {
  let g: i32[] = __v6_groups(b);
  if (__all_zero(b, 0, 10) && b[10] == 255 && b[11] == 255) {
    let tail: u8[] = [];
    tail = tail.append(b[12]);
    tail = tail.append(b[13]);
    tail = tail.append(b[14]);
    tail = tail.append(b[15]);
    return "::ffff:" + __v4_text(tail);
  }
  let run: (i32, i32) = __v6_zero_run(g);
  if (run.0 < 0) {
    return __v6_join(g, 0, 8);
  }
  return __v6_join(g, 0, run.0) + "::" + __v6_join(g, run.0 + run.1, 8);
}
```

### `12 pub function (a: IpAddr) to_string(): string`

```fern
<<12 pub function (a: IpAddr) to_string(): string>>=
pub function (a: IpAddr) to_string(): string {
  match (a) {
    V4(b) => {
      return __v4_text(b);
    },
    V6(b) => {
      return __v6_text(b);
    }
  }
}

pub function (s: SocketAddr) to_string(): string {
  if (s.ip.is_v4()) {
    return s.ip.to_string() + ":" + s.port.to_string();
  }
  return "[" + s.ip.to_string() + "]:" + s.port.to_string();
}

pub function (s: SocketAddr) eq(other: SocketAddr): boolean {
  return s.port == other.port && s.ip.eq(other.ip);
}
```

### `13 impl cmp.Eq for SocketAddr`

```fern
<<13 impl cmp.Eq for SocketAddr>>=
impl cmp.Eq for SocketAddr {}

impl cmp.Display for SocketAddr {}

pub struct Prefix {
  ip: IpAddr,
  bits: i32,
}
```

### `14 pub function prefix_parse(s: string): Option[Prefix]`

```fern
<<14 pub function prefix_parse(s: string): Option[Prefix]>>=
pub function prefix_parse(s: string): Option[Prefix] {
  let slash: Option[i32] = s.index_of("/");
  let text: string = s;
  if let Some(at) = slash {
    text = s.slice_snap(0, at);
  }
  match (ip_parse(text)) {
    Some(ip) => {
      let width: i32 = ip.bytes().len() * 8;
      let bits: i32 = width;
      if let Some(at) = slash {
        bits = __prefix_bits(s.slice_snap(at + 1, s.len()), width);
      }
      if (bits < 0 || !__host_bits_clear(ip.bytes(), bits)) {
        return None;
      }
      if (ip.is_v4_mapped() && bits >= 96) {
        return Some(Prefix { ip: ip.to_canonical(), bits: bits - 96 });
      }
      return Some(Prefix { ip: ip, bits: bits });
    },
    None => {
      return None;
    }
  }
}
```

### `15 function __prefix_bits(s: string, width: i32): i32`

```fern
<<15 function __prefix_bits(s: string, width: i32): i32>>=
function __prefix_bits(s: string, width: i32): i32 {
  if (s.len() == 0 || s.len() > 3) {
    return 0 - 1;
  }
  let n: i32 = 0;
  for b in s.bytes() {
    if (b < 48 as u8 || b > 57 as u8) {
      return 0 - 1;
    }
    n = n * 10 + (b - 48 as u8) as i32;
  }
  if (n > width) {
    return 0 - 1;
  }
  return n;
}

function __host_bits_clear(b: u8[], bits: i32): boolean {
  let i: i32 = 0;
  while (i < b.len()) {
    let keep: i32 = bits - i * 8;
    if (keep < 8 && (b[i] as i32 & __low_mask(keep)) != 0) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __low_mask(keep: i32): i32 {
  if (keep <= 0) {
    return 255;
  }
  return 255 >> keep;
}
```

### `16 pub function (p: Prefix) contains(ip: IpAddr): boolean`

```fern
<<16 pub function (p: Prefix) contains(ip: IpAddr): boolean>>=
pub function (p: Prefix) contains(ip: IpAddr): boolean {
  let a: u8[] = p.ip.bytes();
  let b: u8[] = __in_family(ip, a.len() == 4);
  if (a.len() != b.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i * 8 < p.bits && i < a.len()) {
    let keep: i32 = p.bits - i * 8;
    let mask: i32 = 255 ^ __low_mask(keep);
    if ((a[i] as i32 & mask) != (b[i] as i32 & mask)) {
      return false;
    }
    i = i + 1;
  }
  return true;
}
```

### `17 function __in_family(ip: IpAddr, v4: boolean): u8[]`

```fern
<<17 function __in_family(ip: IpAddr, v4: boolean): u8[]>>=
function __in_family(ip: IpAddr, v4: boolean): u8[] {
  if (v4) {
    return ip.to_canonical().bytes();
  }
  match (ip) {
    V4(b) => {
      return [0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 255 as u8, 255 as u8, b[0], b[1], b[2], b[3]];
    },
    V6(b) => {
      return b;
    }
  }
}
```

### `18 pub function (p: Prefix) to_string(): string`

```fern
<<18 pub function (p: Prefix) to_string(): string>>=
pub function (p: Prefix) to_string(): string {
  return p.ip.to_string() + "/" + p.bits.to_string();
}
```

### `19 function __pieces(s: string, sep: i32): string[]`

```fern
<<19 function __pieces(s: string, sep: i32): string[]>>=
function __pieces(s: string, sep: i32): string[] {
  let out: string[] = [];
  let start: i32 = 0;
  let i: i32 = 0;
  while (i < s.len()) {
    if (s[i] as i32 == sep) {
      out = out.append(slice_unchecked(s, start, i) + "");
      start = i + 1;
    }
    i = i + 1;
  }
  return out.append(slice_unchecked(s, start, s.len()) + "");
}

function __has_byte(s: string, b: i32): boolean {
  let i: i32 = 0;
  while (i < s.len()) {
    if (s[i] as i32 == b) {
      return true;
    }
    i = i + 1;
  }
  return false;
}

function __octet(s: string, from: i32, sep: i32): i32 {
  let i: i32 = from;
  let v: i32 = 0;
  while (i < s.len() && s[i] as i32 != sep) {
    let c: i32 = s[i] as i32;
    if (c < 48 || c > 57 || i - from == 3 || i > from && s[from] == 48) {
      return -1;
    }
    v = v * 10 + (c - 48);
    i = i + 1;
  }
  if (i == from || v > 255) {
    return -1;
  }
  return v | i << 8;
}
```

### `20 pub function ipv4_parse(s: string): Option[IpAddr]`

```fern
<<20 pub function ipv4_parse(s: string): Option[IpAddr]>>=
pub function ipv4_parse(s: string): Option[IpAddr] {
  let a: i32 = __octet(s, 0, 46);
  if (a < 0 || a >> 8 >= s.len()) {
    return None;
  }
  let b: i32 = __octet(s, (a >> 8) + 1, 46);
  if (b < 0 || b >> 8 >= s.len()) {
    return None;
  }
  let c: i32 = __octet(s, (b >> 8) + 1, 46);
  if (c < 0 || c >> 8 >= s.len()) {
    return None;
  }
  let d: i32 = __octet(s, (c >> 8) + 1, 46);
  if (d < 0 || d >> 8 != s.len()) {
    return None;
  }
  return Some(V4([(a & 255) as u8, (b & 255) as u8, (c & 255) as u8, (d & 255) as u8]));
}

pub function is_ipv4(s: string): boolean {
  let at: i32 = 0;
  let n: i32 = 0;
  while (n < 4) {
    let o: i32 = __octet(s, at, 46);
    if (o < 0) {
      return false;
    }
    at = (o >> 8) + 1;
    n = n + 1;
  }
  return at == s.len() + 1;
}
```

### `21 function __hex_digit(c: i32): i32`

```fern
<<21 function __hex_digit(c: i32): i32>>=
function __hex_digit(c: i32): i32 {
  if (c >= 48 && c <= 57) {
    return c - 48;
  }
  if (c >= 97 && c <= 102) {
    return c - 87;
  }
  if (c >= 65 && c <= 70) {
    return c - 55;
  }
  return -1;
}

function __v6_group(s: string): i32 {
  let n: i32 = s.len();
  if (n == 0 || n > 4) {
    return -1;
  }
  let v: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    let d: i32 = __hex_digit(s[i] as i32);
    if (d < 0) {
      return -1;
    }
    v = v << 4 | d;
    i = i + 1;
  }
  return v;
}

function __v6_side(s: string, v4_tail: boolean): Option[i32[]] {
  let groups: i32[] = [];
  if (s.len() == 0) {
    return Some(groups);
  }
  let parts: string[] = __pieces(s, 58);
  let i: i32 = 0;
  while (i < parts.len()) {
    let last: boolean = i == parts.len() - 1;
    if (last && v4_tail && __has_byte(parts[i], 46)) {
      match (ipv4_parse(parts[i])) {
        Some(v4) => {
          let b: u8[] = v4.bytes();
          groups = groups.append(b[0] as i32 << 8 | b[1] as i32);
          groups = groups.append(b[2] as i32 << 8 | b[3] as i32);
        },
        None => {
          return None;
        }
      }
    } else {
      let g: i32 = __v6_group(parts[i]);
      if (g < 0) {
        return None;
      }
      groups = groups.append(g);
    }
    i = i + 1;
  }
  return Some(groups);
}

function __find_double_colon(s: string): i32 {
  let i: i32 = 0;
  while (i + 1 < s.len()) {
    if (s[i] == 58 && s[i + 1] == 58) {
      return i;
    }
    i = i + 1;
  }
  return -1;
}

function __v6_from_groups(left: i32[], right: i32[]): IpAddr {
  let out: u8[] = [];
  let i: i32 = 0;
  while (i < left.len()) {
    out = out.append((left[i] >> 8) as u8);
    out = out.append((left[i] & 255) as u8);
    i = i + 1;
  }
  let zeros: i32 = 8 - left.len() - right.len();
  let z: i32 = 0;
  while (z < zeros) {
    out = out.append(0 as u8);
    out = out.append(0 as u8);
    z = z + 1;
  }
  let j: i32 = 0;
  while (j < right.len()) {
    out = out.append((right[j] >> 8) as u8);
    out = out.append((right[j] & 255) as u8);
    j = j + 1;
  }
  return V6(out);
}

function __v6_compressed(s: string, at: i32): Option[IpAddr] {
  let left_text: string = slice_unchecked(s, 0, at) + "";
  let right_text: string = slice_unchecked(s, at + 2, s.len()) + "";
  if (__find_double_colon(right_text) >= 0) {
    return None;
  }
  let left: i32[] = [];
  match (__v6_side(left_text, false)) {
    Some(g) => {
      left = g;
    },
    None => {
      return None;
    }
  }
  let right: i32[] = [];
  match (__v6_side(right_text, true)) {
    Some(g) => {
      right = g;
    },
    None => {
      return None;
    }
  }
  if (left.len() + right.len() > 7) {
    return None;
  }
  return Some(__v6_from_groups(left, right));
}
```

### `22 pub function ipv6_parse(s: string): Option[IpAddr]`

```fern
<<22 pub function ipv6_parse(s: string): Option[IpAddr]>>=
pub function ipv6_parse(s: string): Option[IpAddr] {
  if (s.len() == 0) {
    return None;
  }
  let at: i32 = __find_double_colon(s);
  if (at >= 0) {
    return __v6_compressed(s, at);
  }
  match (__v6_side(s, true)) {
    Some(g) => {
      if (g.len() != 8) {
        return None;
      }
      return Some(__v6_from_groups(g, []));
    },
    None => {
      return None;
    }
  }
}

pub function ip_parse(s: string): Option[IpAddr] {
  if (__has_byte(s, 58)) {
    return ipv6_parse(s);
  }
  return ipv4_parse(s);
}
```

### `23 function __port(s: string): i32`

```fern
<<23 function __port(s: string): i32>>=
function __port(s: string): i32 {
  let n: i32 = s.len();
  if (n == 0 || n > 5) {
    return -1;
  }
  let v: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    let c: i32 = s[i] as i32;
    if (c < 48 || c > 57) {
      return -1;
    }
    v = v * 10 + (c - 48);
    i = i + 1;
  }
  if (v > 65535) {
    return -1;
  }
  return v;
}

function __socket_addr_bracketed(s: string): Option[SocketAddr] {
  let close: i32 = -1;
  let i: i32 = 1;
  while (i < s.len()) {
    if (s[i] == 93) {
      close = i;
      i = s.len();
    }
    i = i + 1;
  }
  if (close < 0 || close + 1 >= s.len() || s[close + 1] != 58) {
    return None;
  }
  let port: i32 = __port(slice_unchecked(s, close + 2, s.len()) + "");
  if (port < 0) {
    return None;
  }
  match (ipv6_parse(slice_unchecked(s, 1, close) + "")) {
    Some(ip) => {
      return Some(SocketAddr { ip: ip, port: port });
    },
    None => {
      return None;
    }
  }
}
```

### `24 pub function socket_addr_parse(s: string): Option[SocketAddr]`

```fern
<<24 pub function socket_addr_parse(s: string): Option[SocketAddr]>>=
pub function socket_addr_parse(s: string): Option[SocketAddr] {
  if (s.len() > 0 && s[0] == 91) {
    return __socket_addr_bracketed(s);
  }
  let colon: i32 = -1;
  let i: i32 = 0;
  while (i < s.len()) {
    if (s[i] == 58) {
      colon = i;
    }
    i = i + 1;
  }
  if (colon < 0) {
    return None;
  }
  let port: i32 = __port(slice_unchecked(s, colon + 1, s.len()) + "");
  if (port < 0) {
    return None;
  }
  match (ipv4_parse(slice_unchecked(s, 0, colon) + "")) {
    Some(ip) => {
      return Some(SocketAddr { ip: ip, port: port });
    },
    None => {
      return None;
    }
  }
}
```

### `25 function __net_errnos(): i32[]`

```fern
<<25 function __net_errnos(): i32[]>>=
function __net_errnos(): i32[] {
  return [errno.EADDRINUSE, errno.EADDRNOTAVAIL, errno.ECONNREFUSED, errno.ECONNRESET, errno.ECONNABORTED, errno.ETIMEDOUT, errno.EAGAIN, errno.EINPROGRESS, errno.ENETUNREACH, errno.EHOSTUNREACH, errno.EINTR];
}

function __net_error_at(i: i32, n: i32): NetError {
  if (i == 0) {
    return AddrInUse;
  }
  if (i == 1) {
    return AddrNotAvailable;
  }
  if (i == 2) {
    return ConnectionRefused;
  }
  if (i == 3) {
    return ConnectionReset;
  }
  if (i == 4) {
    return ConnectionAborted;
  }
  if (i == 5) {
    return TimedOut;
  }
  return __net_error_at_high(i, n);
}

function __net_error_at_high(i: i32, n: i32): NetError {
  if (i == 6) {
    return WouldBlock;
  }
  if (i == 7) {
    return InProgress;
  }
  if (i == 8) {
    return NetworkUnreachable;
  }
  if (i == 9) {
    return HostUnreachable;
  }
  if (i == 10) {
    return NetError.Interrupted;
  }
  return NetError.Other(n);
}
```

### `26 pub function error_from_host(n: i32): NetError`

```fern
<<26 pub function error_from_host(n: i32): NetError>>=
pub function error_from_host(n: i32): NetError {
  let e: i32 = errno.from_host(n);
  let table: i32[] = __net_errnos();
  let i: i32 = 0;
  while (i < table.len()) {
    if (table[i] == e) {
      return __net_error_at(i, e);
    }
    i = i + 1;
  }
  return NetError.Other(e);
}
```

### `27 function __net_error_index(e: NetError): i32`

```fern
<<27 function __net_error_index(e: NetError): i32>>=
function __net_error_index(e: NetError): i32 {
  match (e) {
    AddrInUse => {
      return 0;
    },
    AddrNotAvailable => {
      return 1;
    },
    ConnectionRefused => {
      return 2;
    },
    ConnectionReset => {
      return 3;
    },
    ConnectionAborted => {
      return 4;
    },
    TimedOut => {
      return 5;
    },
    WouldBlock => {
      return 6;
    },
    InProgress => {
      return 7;
    },
    NetworkUnreachable => {
      return 8;
    },
    HostUnreachable => {
      return 9;
    },
    NetError.Interrupted => {
      return 10;
    },
    NetError.Other(n) => {
      return -1;
    }
  }
}
```

### `28 pub function (e: NetError) errno(): i32`

```fern
<<28 pub function (e: NetError) errno(): i32>>=
pub function (e: NetError) errno(): i32 {
  match (e) {
    NetError.Other(n) => {
      return n;
    },
    _ => {
      return __net_errnos()[__net_error_index(e)];
    }
  }
}

pub function (e: NetError) message(): string {
  match (e) {
    AddrInUse => {
      return "Address already in use";
    },
    AddrNotAvailable => {
      return "Cannot assign requested address";
    },
    ConnectionRefused => {
      return "Connection refused";
    },
    ConnectionReset => {
      return "Connection reset by peer";
    },
    ConnectionAborted => {
      return "Software caused connection abort";
    },
    TimedOut => {
      return "Connection timed out";
    },
    _ => {
      return __net_error_message_high(e);
    }
  }
}
```

### `29 function __net_error_message_high(e: NetError): string`

```fern
<<29 function __net_error_message_high(e: NetError): string>>=
function __net_error_message_high(e: NetError): string {
  match (e) {
    WouldBlock => {
      return "Resource temporarily unavailable";
    },
    InProgress => {
      return "Operation now in progress";
    },
    NetworkUnreachable => {
      return "Network is unreachable";
    },
    HostUnreachable => {
      return "No route to host";
    },
    NetError.Interrupted => {
      return "Interrupted system call";
    },
    NetError.Other(n) => {
      return "Network error (errno " + n.to_string() + ")";
    },
    _ => {
      return "";
    }
  }
}
```

### `30 pub function (e: NetError) to_string(): string`

```fern
<<30 pub function (e: NetError) to_string(): string>>=
pub function (e: NetError) to_string(): string {
  return e.message();
}

pub function (e: NetError) eq(other: NetError): boolean {
  let i: i32 = __net_error_index(e);
  if (i != __net_error_index(other)) {
    return false;
  }
  return i >= 0 || e.errno() == other.errno();
}

pub function (e: NetError) ipv6_unavailable(): boolean {
  return e.eq(AddrNotAvailable) || e.errno() == errno.EAFNOSUPPORT;
}
```

### `31 pub struct ListenOptions`

```fern
<<31 pub struct ListenOptions>>=
pub struct ListenOptions {
  backlog: i32,
  reuse_port: boolean,
}
```

### `32 pub function listen_options(): ListenOptions`

```fern
<<32 pub function listen_options(): ListenOptions>>=
pub function listen_options(): ListenOptions {
  return ListenOptions { backlog: 128, reuse_port: false };
}

pub function listen_with(port: i32, opts: ListenOptions): Result[i32, NetError] {
  return listen_at(socket_addr(ipv4_unspecified(), port), opts);
}

pub function listen_at(addr: SocketAddr, opts: ListenOptions): Result[i32, NetError] {
  return __socket_count(tcp_listen_with(addr.ip.bytes(), addr.port, opts.backlog, opts.reuse_port));
}

pub function read(sock: i32, buf: u8[]): Result[i32, NetError] {
  return __socket_count(tcp_recv_into(sock, buf));
}

pub function read_up_to(sock: i32, max: i32): Result[u8[], NetError] {
  let buf: u8[] = __alloc_u8(max);
  let n: i32 = read(sock, buf)?;
  __arr_set_len(buf, n);
  return Ok(buf);
}

pub function write(sock: i32, data: u8[]): Result[i32, NetError] {
  return __socket_count(tcp_send_bytes(sock, data));
}
```

### `33 pub enum Recv`

```fern
<<33 pub enum Recv>>=
pub enum Recv { Chunk(u8[]), Elapsed, Abandoned }
```

### `34 pub function read_deadline(sock: i32, max: i32, deadline: Duration): Result[Recv, NetError]`

```fern
<<34 pub function read_deadline(sock: i32, max: i32, deadline: Duration): Result[Recv, NetError]>>=
pub function read_deadline(sock: i32, max: i32, deadline: Duration): Result[Recv, NetError] {
  return read_watched(sock, max, deadline.as_millis(), 1);
}

pub function read_watched(sock: i32, max: i32, ms: i64, interest: i32): Result[Recv, NetError] {
  let until: i64 = monotonic_ns() + ms * 1000000i64;
  let set: i32[] = [sock, interest];
  while (true) {
    let wait_ms: i32 = ((until - monotonic_ns()) / 1000000i64) as i32;
    if (wait_ms < 1) {
      wait_ms = 1;
    }
    let woke: i32 = async.wait_any(set, wait_ms);
    if (woke == async.cancelled()) {
      return Ok(Abandoned);
    }
    if (woke != 0) {
      return Ok(Elapsed);
    }
    match (read_up_to(sock, max)) {
      Ok(got) => {
        return Ok(Chunk(got));
      },
      Err(e) => {
        if (!e.eq(WouldBlock)) {
          return Err(e);
        }
      }
    }
  }
  return Ok(Elapsed);
}
```

### `35 pub enum Shutdown`

```fern
<<35 pub enum Shutdown>>=
pub enum Shutdown {
  Read,
  Write,
  Both,
}
```

### `36 pub function set_nodelay(sock: i32, on: boolean): Result[void, NetError]`

```fern
<<36 pub function set_nodelay(sock: i32, on: boolean): Result[void, NetError]>>=
pub function set_nodelay(sock: i32, on: boolean): Result[void, NetError] {
  return __socket_ctl(sock, 1, on);
}

pub function set_keepalive(sock: i32, on: boolean): Result[void, NetError] {
  return __socket_ctl(sock, 2, on);
}

pub function set_nonblocking(sock: i32, on: boolean): Result[void, NetError] {
  return __socket_ctl(sock, 3, on);
}

pub function send_queue(sock: i32): Result[i32, NetError] {
  return __socket_count(tcp_socket_ctl(sock, 6, 0));
}

pub function peer_key(sock: i32): Option[i32] {
  let k: i32 = tcp_socket_ctl(sock, 7, 0);
  if (k == 0) {
    return None;
  }
  return Some(k);
}

pub function shutdown(sock: i32, how: Shutdown): Result[void, NetError] {
  let arg: i32 = 2;
  match (how) {
    Read => {
      arg = 0;
    },
    Write => {
      arg = 1;
    },
    Both => {
      arg = 2;
    }
  }
  return __socket_result(tcp_socket_ctl(sock, 4, arg));
}
```

### `37 function __socket_ctl(sock: i32, op: i32, on: boolean): Result[void, NetError]`

```fern
<<37 function __socket_ctl(sock: i32, op: i32, on: boolean): Result[void, NetError]>>=
function __socket_ctl(sock: i32, op: i32, on: boolean): Result[void, NetError] {
  let arg: i32 = 0;
  if (on) {
    arg = 1;
  }
  return __socket_result(tcp_socket_ctl(sock, op, arg));
}

function __socket_result(r: i32): Result[void, NetError] {
  if (r < 0) {
    return Err(error_from_host(r));
  }
  return Ok(());
}

function __socket_count(r: i32): Result[i32, NetError] {
  if (r < 0) {
    return Err(error_from_host(r));
  }
  return Ok(r);
}
```

### `38 pub function udp_socket(addr: SocketAddr): Result[i32, NetError]`

```fern
<<38 pub function udp_socket(addr: SocketAddr): Result[i32, NetError]>>=
pub function udp_socket(addr: SocketAddr): Result[i32, NetError] {
  return __socket_count(udp_bind(addr.ip.bytes(), addr.port));
}

pub function set_peer(sock: i32, peer: SocketAddr): Result[void, NetError] {
  return __socket_result(udp_connect(sock, peer.ip.bytes(), peer.port));
}

pub function send_to(sock: i32, data: u8[], to: SocketAddr): Result[i32, NetError] {
  return __socket_count(udp_sendto_bytes(sock, to.ip.bytes(), to.port, data));
}

pub function send(sock: i32, data: u8[]): Result[i32, NetError] {
  let peer: u8[] = [];
  return __socket_count(udp_sendto_bytes(sock, peer, 0, data));
}
```

### `39 function __sender(from: u8[]): SocketAddr`

```fern
<<39 function __sender(from: u8[]): SocketAddr>>=
function __sender(from: u8[]): SocketAddr {
  let n: i32 = 4;
  if (from[0] == 6u8) {
    n = 16;
  }
  let b: u8[] = [];
  let i: i32 = 0;
  while (i < n) {
    b = b.append(from[1 + i]);
    i = i + 1;
  }
  let ip: IpAddr = V4(b);
  if (n == 16) {
    ip = V6(b);
  }
  return socket_addr(ip, from[17] as i32 << 8 | from[18] as i32);
}
```

### `40 pub function recv_from(sock: i32, buf: u8[]): Result[(i32, SocketAddr), NetError]`

```fern
<<40 pub function recv_from(sock: i32, buf: u8[]): Result[(i32, SocketAddr), NetError]>>=
pub function recv_from(sock: i32, buf: u8[]): Result[(i32, SocketAddr), NetError] {
  let from: u8[] = __zero_bytes(19);
  let n: i32 = udp_recvfrom(sock, buf, from);
  if (n < 0) {
    return Err(error_from_host(n));
  }
  return Ok((n, __sender(from)));
}

pub function recv(sock: i32, buf: u8[]): Result[i32, NetError] {
  let none: u8[] = [];
  return __socket_count(udp_recvfrom(sock, buf, none));
}

pub function connect(addr: SocketAddr): Result[i32, NetError] {
  return __tcp_socket(tcp_connect_with(addr.ip.bytes(), addr.port, false));
}

pub function connect_start(addr: SocketAddr): Result[i32, NetError] {
  return __tcp_socket(tcp_connect_with(addr.ip.bytes(), addr.port, true));
}

pub function connect_result(sock: i32): Result[void, NetError] {
  return __socket_result(tcp_socket_ctl(sock, 5, 0));
}

pub function accept(sock: i32): Result[i32, NetError] {
  return __tcp_socket(tcp_accept(sock));
}
```

### `41 function __tcp_socket(r: i32): Result[i32, NetError]`

```fern
<<41 function __tcp_socket(r: i32): Result[i32, NetError]>>=
function __tcp_socket(r: i32): Result[i32, NetError] {
  if (r >= 0) {
    tcp_socket_ctl(r, 1, 1);
  }
  return __socket_count(r);
}
```

### `42 pub function listen_unix(path: string, backlog: i32): Result[i32, NetError]`

```fern
<<42 pub function listen_unix(path: string, backlog: i32): Result[i32, NetError]>>=
pub function listen_unix(path: string, backlog: i32): Result[i32, NetError] {
  return __socket_count(unix_listen(path, backlog));
}

pub function connect_unix(path: string): Result[i32, NetError] {
  return __socket_count(unix_connect(path));
}

pub function local_addr(sock: i32): Result[SocketAddr, NetError] {
  return __name_of(sock, 9);
}

pub function peer_addr(sock: i32): Result[SocketAddr, NetError] {
  match (__name_of(sock, 10)) {
    Ok(a) => {
      return Ok(socket_addr(a.ip.to_canonical(), a.port));
    },
    Err(e) => {
      return Err(e);
    }
  }
}
```

### `43 function __name_of(sock: i32, op: i32): Result[SocketAddr, NetError]`

```fern
<<43 function __name_of(sock: i32, op: i32): Result[SocketAddr, NetError]>>=
function __name_of(sock: i32, op: i32): Result[SocketAddr, NetError] {
  let fam: i32 = tcp_socket_ctl(sock, op, 8);
  if (fam < 0) {
    return Err(error_from_host(fam));
  }
  let groups: i32 = 2;
  if (fam == 6) {
    groups = 8;
  }
  let b: u8[] = [];
  let i: i32 = 0;
  while (i < groups) {
    let g: i32 = tcp_socket_ctl(sock, op, i);
    if (g < 0) {
      return Err(error_from_host(g));
    }
    b = b.append((g >> 8) as u8);
    b = b.append((g & 255) as u8);
    i = i + 1;
  }
  let port: i32 = tcp_socket_ctl(sock, op, 9);
  if (port < 0) {
    return Err(error_from_host(port));
  }
  if (fam == 6) {
    return Ok(socket_addr(V6(b), port));
  }
  return Ok(socket_addr(V4(b), port));
}
```

### `44 pub function local_port(sock: i32): Result[i32, NetError]`

```fern
<<44 pub function local_port(sock: i32): Result[i32, NetError]>>=
pub function local_port(sock: i32): Result[i32, NetError] {
  return __socket_count(tcp_local_port(sock));
}

pub function close(sock: i32): Result[void, NetError] {
  return __socket_result(tcp_close(sock));
}
```

### `45 impl cmp.Eq for NetError`

```fern
<<45 impl cmp.Eq for NetError>>=
impl cmp.Eq for NetError {}

impl cmp.Display for NetError {}

impl error.Error for NetError {}
```
