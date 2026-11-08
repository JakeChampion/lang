# IPv4 over a network device

A kernel that answers the host needs more than frames. This module is the first layers of the network stack of `docs/BARE-METAL-PLAN.md` (#14259) over `std/virtio_net`: Ethernet frames, ARP for the addresses on the link, IPv4, UDP, and the wire format of TCP segments. `std/tcp/conn` is the TCP state machine over those segments, and `std/tcp` binds its connections to an interface.

An interface has one IPv4 address, a gateway and the MAC of its device. It keeps an ARP cache in a page of raw memory that the caller gives it. The interface value then copies freely, and nothing allocates on the receive path. The interface learns a MAC from every IPv4 frame it receives and from every ARP reply. It answers an ARP request for its own address.

`poll` takes one frame from the device, handles ARP itself, and gives a UDP datagram or a TCP segment to the caller. A caller that waits for a datagram calls it in a loop, or after a wake on the IRQ line of the device.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<constants>>

<<byte helpers>>

<<pub struct Interface>>

<<the ARP cache>>

<<receiving>>

<<sending>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module gives a kernel Ethernet, ARP, IPv4 and UDP over a network device.
```

### `dependencies`

```fern
<<dependencies>>=
import "std/option";
import "std/raw";
import "std/tcp/conn";
import "std/virtio_net";
```

### `constants`

The ARP cache has 32 entries of 16 bytes at the start of the state page: the IPv4 address, the MAC, and a byte that marks a live entry. The IPv4 identification counter follows the cache.

```fern
<<constants>>=
const ETHER_IPV4: i32 = 0x0800;

const ETHER_ARP: i32 = 0x0806;

const PROTO_UDP: i32 = 17;

const PROTO_TCP: i32 = 6;

const ARP_ENTRIES: i32 = 32;

const ARP_ENTRY_BYTES: usize = 16;

const ID_COUNTER: usize = 512;

// The Ethernet broadcast address.
pub function broadcast(): u8[] {
  return [255u8, 255u8, 255u8, 255u8, 255u8, 255u8];
}
```

### `byte helpers`

```fern
<<byte helpers>>=
function be16(data: u8[], at: i32): i32 {
  return data[at] as i32 << 8 | data[at + 1] as i32;
}

function push_be16(data: u8[], v: i32): u8[] {
  return data.append((v >> 8 & 255) as u8).append((v & 255) as u8);
}

function be32(data: u8[], at: i32): u32 {
  return (be16(data, at) as i64 * 65536i64 + be16(data, at + 2) as i64) as u32;
}

function push_be32(data: u8[], v: u32): u8[] {
  let high = (v as i64 / 65536i64) as i32;
  return push_be16(push_be16(data, high), (v as i64 % 65536i64) as i32);
}

function push_all(data: u8[], more: u8[]): u8[] {
  for b in more {
    data = data.append(b);
  }
  return data;
}

function bytes(data: u8[], start: i32, end: i32): u8[] {
  let out: u8[] = [];
  for i in start..end {
    out = out.append(data[i]);
  }
  return out;
}

function same(a: u8[], b: u8[]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  for i in 0..a.len() {
    if (a[i] != b[i]) {
      return false;
    }
  }
  return true;
}

// The ones' complement sum of the bytes from start to end, which IPv4 and
// UDP use for their checksums.
pub function checksum(data: u8[], start: i32, end: i32): i32 {
  let sum = 0;
  let i = start;
  while (i + 1 < end) {
    sum += be16(data, i);
    i += 2;
  }
  if (i < end) {
    sum += data[i] as i32 << 8;
  }
  while (sum >> 16 != 0) {
    sum = (sum & 0xffff) + (sum >> 16);
  }
  return sum ^ 0xffff;
}

// The checksum of a UDP or TCP segment, which also covers the pseudo-header:
// the two addresses, the protocol and the length of the segment.
pub function transport_checksum(source: u8[], destination: u8[], proto: i32, segment: u8[]): i32 {
  let data = push_all(push_all([], source), destination);
  data = push_be16(push_be16(data, proto), segment.len());
  data = push_all(data, segment);
  return checksum(data, 0, data.len());
}
```

### `pub struct Interface`

`open` takes the device, the address of the interface, its gateway and a page of raw memory for the ARP cache. It clears the cache.

```fern
<<pub struct Interface>>=
pub struct Interface { net: virtio_net.Net, mac: u8[], address: u8[], gateway: u8[], state: usize }

// A UDP datagram the interface received: the sender, the ports and the data.
pub struct Datagram { source: u8[], source_port: i32, port: i32, payload: u8[] }

// What `poll` gives the caller: a UDP datagram or a TCP segment.
pub enum Packet { Udp(Datagram), Tcp(conn.Segment) }

pub function open(net: virtio_net.Net, address: u8[], gateway: u8[], state: usize): Interface {
  let i = 0 as usize;
  while (i < 4096 as usize) {
    raw.store_u64(state + i, 0 as u64);
    i += 8 as usize;
  }
  return Interface { net: net, mac: net.mac, address: address, gateway: gateway, state: state };
}
```

### `the ARP cache`

`learn` records the MAC of an address, in the entry of the address or in a free one. `resolve` gives the MAC of an address in the cache. `request` asks the link for the MAC of an address. The reply reaches the cache through `poll`.

```fern
<<the ARP cache>>=
function entry(i: Interface, n: i32): usize {
  return i.state + n as usize * ARP_ENTRY_BYTES;
}

function entry_address(i: Interface, n: i32): u8[] {
  let e = entry(i, n);
  return [raw.load_u8(e), raw.load_u8(e + 1 as usize), raw.load_u8(e + 2 as usize), raw.load_u8(e + 3 as usize)];
}

function entry_mac(i: Interface, n: i32): u8[] {
  let out: u8[] = [];
  for k in 4..10 {
    out = out.append(raw.load_u8(entry(i, n) + k as usize));
  }
  return out;
}

function entry_live(i: Interface, n: i32): boolean {
  return raw.load_u8(entry(i, n) + 10 as usize) != 0 as u8;
}

pub function (i: Interface) learn(address: u8[], mac: u8[]): void {
  if (address.len() != 4 || mac.len() != 6) {
    return;
  }
  let slot = -1;
  for n in 0..ARP_ENTRIES {
    if (entry_live(i, n) && same(entry_address(i, n), address)) {
      slot = n;
      break;
    }
    if (slot < 0 && !entry_live(i, n)) {
      slot = n;
    }
  }
  if (slot < 0) {
    slot = 0;
  }
  let e = entry(i, slot);
  for k in 0..4 {
    raw.store_u8(e + k as usize, address[k]);
  }
  for k in 0..6 {
    raw.store_u8(e + (4 + k) as usize, mac[k]);
  }
  raw.store_u8(e + 10 as usize, 1 as u8);
}

pub function (i: Interface) resolve(address: u8[]): Option[u8[]] {
  for n in 0..ARP_ENTRIES {
    if (entry_live(i, n) && same(entry_address(i, n), address)) {
      return Some(entry_mac(i, n));
    }
  }
  return None;
}

function arp_frame(i: Interface, op: i32, target_mac: u8[], target: u8[]): u8[] {
  let dst = target_mac;
  if (op == 1) {
    dst = broadcast();
  }
  let f = push_all(push_all([], dst), i.mac);
  f = push_be16(f, ETHER_ARP);
  f = push_all(f, [0u8, 1u8, 8u8, 0u8, 6u8, 4u8]);
  f = push_be16(f, op);
  f = push_all(push_all(f, i.mac), i.address);
  if (op == 1) {
    f = push_all(f, [0u8, 0u8, 0u8, 0u8, 0u8, 0u8]);
  } else {
    f = push_all(f, target_mac);
  }
  return push_all(f, target);
}

pub function (i: Interface) request(address: u8[]): boolean {
  return i.net.send(arp_frame(i, 1, broadcast(), address));
}
```

### `receiving`

An ARP request for the interface's address gets a reply, and an ARP reply fills the cache. An IPv4 frame for the interface teaches the cache the sender's MAC. A UDP datagram or a TCP segment in it goes to the caller, after its checksum passes. The interface drops every other frame.

```fern
<<receiving>>=
pub function (i: Interface) poll(): Option[Packet] {
  let Some(frame) = i.net.receive() else {
    return None;
  };
  if (frame.len() < 14) {
    return None;
  }
  let kind = be16(frame, 12);
  if (kind == ETHER_ARP) {
    i.take_arp(frame);
    return None;
  }
  if (kind == ETHER_IPV4) {
    return i.take_ipv4(frame);
  }
  return None;
}

function (i: Interface) take_arp(frame: u8[]): void {
  if (frame.len() < 42 || be16(frame, 14) != 1 || be16(frame, 16) != ETHER_IPV4) {
    return;
  }
  let op = be16(frame, 20);
  let sender_mac = bytes(frame, 22, 28);
  let sender = bytes(frame, 28, 32);
  let target = bytes(frame, 38, 42);
  if (op == 2) {
    i.learn(sender, sender_mac);
  } else if (op == 1 && same(target, i.address)) {
    i.learn(sender, sender_mac);
    let sent = i.net.send(arp_frame(i, 2, sender_mac, sender));
  }
}

function (i: Interface) take_ipv4(frame: u8[]): Option[Packet] {
  if (frame.len() < 34 || frame[14] as i32 != 0x45) {
    return None;
  }
  let total = be16(frame, 16);
  if (total < 20 || 14 + total > frame.len()) {
    return None;
  }
  let destination = bytes(frame, 30, 34);
  if (!same(destination, i.address)) {
    return None;
  }
  let source = bytes(frame, 26, 30);
  i.learn(source, bytes(frame, 6, 12));
  let proto = frame[23] as i32;
  let segment = bytes(frame, 34, 14 + total);
  if (proto == PROTO_TCP) {
    return take_tcp(source, destination, segment);
  }
  if (proto == PROTO_UDP) {
    return take_udp(source, destination, segment);
  }
  return None;
}

// Parses a UDP datagram. A checksum of zero means none, and a datagram with
// a bad checksum is dropped.
function take_udp(source: u8[], destination: u8[], segment: u8[]): Option[Packet] {
  if (segment.len() < 8) {
    return None;
  }
  let length = be16(segment, 4);
  if (length < 8 || length > segment.len()) {
    return None;
  }
  let datagram = bytes(segment, 0, length);
  if (be16(datagram, 6) != 0 && transport_checksum(source, destination, PROTO_UDP, datagram) != 0) {
    return None;
  }
  return Some(Udp(Datagram { source: source, source_port: be16(datagram, 0), port: be16(datagram, 2), payload: bytes(datagram, 8, length) }));
}

// Parses a TCP segment: the header, the maximum segment size option of a
// SYN, and the payload. A segment with a bad checksum is dropped.
function take_tcp(source: u8[], destination: u8[], segment: u8[]): Option[Packet] {
  let n = segment.len();
  if (n < 20) {
    return None;
  }
  let offset = (segment[12] as i32 >> 4) * 4;
  if (offset < 20 || offset > n) {
    return None;
  }
  if (transport_checksum(source, destination, PROTO_TCP, segment) != 0) {
    return None;
  }
  return Some(Tcp(conn.Segment { source: source, source_port: be16(segment, 0), destination: destination, port: be16(segment, 2), seq: be32(segment, 4), ack: be32(segment, 8), flags: segment[13] as i32 & 63, window: be16(segment, 14), mss: tcp_mss(segment, offset), payload: bytes(segment, offset, n) }));
}

// The maximum segment size option of a TCP header whose options end at
// `offset`, or 0 when the header carries none.
function tcp_mss(segment: u8[], offset: i32): i32 {
  let at = 20;
  while (at < offset) {
    let kind = segment[at] as i32;
    if (kind == 0) {
      return 0;
    }
    if (kind == 1) {
      at += 1;
      continue;
    }
    if (at + 1 >= offset) {
      return 0;
    }
    let len = segment[at + 1] as i32;
    if (len < 2 || at + len > offset) {
      return 0;
    }
    if (kind == 2 && len == 4) {
      return be16(segment, at + 2);
    }
    at += len;
  }
  return 0;
}
```

### `sending`

`send_udp` builds the UDP header around the payload, and `send_tcp` the TCP header around a segment of `std/tcp/conn`. Both go through `send_ipv4`, which builds the IPv4 header. It sends the frame to the MAC of the destination, or to the MAC of the gateway for an address off the link. It is false when the cache does not have that MAC. `request` asks for it, and `poll` learns the answer.

```fern
<<sending>>=
function (i: Interface) next_id(): i32 {
  let id = raw.load_u16(i.state + ID_COUNTER) as i32;
  raw.store_u16(i.state + ID_COUNTER, (id + 1 & 0xffff) as u32);
  return id;
}

// Whether an address is on the link: the first three bytes match the
// interface's address.
function (i: Interface) on_link(address: u8[]): boolean {
  return address[0] == i.address[0] && address[1] == i.address[1] && address[2] == i.address[2];
}

pub function (i: Interface) send_ipv4(destination: u8[], proto: i32, segment: u8[]): boolean {
  let hop = destination;
  if (!i.on_link(destination)) {
    hop = i.gateway;
  }
  let Some(mac) = i.resolve(hop) else {
    return false;
  };
  let f = push_all(push_all([], mac), i.mac);
  f = push_be16(f, ETHER_IPV4);
  let ip: u8[] = [0x45u8, 0u8];
  ip = push_be16(ip, 20 + segment.len());
  ip = push_be16(ip, i.next_id());
  ip = push_all(ip, [0u8, 0u8, 64u8, proto as u8, 0u8, 0u8]);
  ip = push_all(push_all(ip, i.address), destination);
  let c = checksum(ip, 0, 20);
  ip = ip.with(10, (c >> 8) as u8).with(11, (c & 255) as u8);
  return i.net.send(push_all(push_all(f, ip), segment));
}

// Puts the transport checksum into bytes `at` and `at + 1` of the segment. A
// UDP checksum of zero means none, so it is sent as all ones.
function (i: Interface) with_checksum(destination: u8[], proto: i32, segment: u8[], at: i32): u8[] {
  let c = transport_checksum(i.address, destination, proto, segment);
  if (c == 0 && proto == PROTO_UDP) {
    c = 0xffff;
  }
  return segment.with(at, (c >> 8) as u8).with(at + 1, (c & 255) as u8);
}

pub function (i: Interface) send_udp(destination: u8[], destination_port: i32, source_port: i32, payload: u8[]): boolean {
  let udp: u8[] = [];
  udp = push_be16(push_be16(udp, source_port), destination_port);
  udp = push_be16(push_be16(udp, 8 + payload.len()), 0);
  udp = push_all(udp, payload);
  return i.send_ipv4(destination, PROTO_UDP, i.with_checksum(destination, PROTO_UDP, udp, 6));
}

// Sends a TCP segment of std/tcp/conn, with the maximum segment size option
// when the segment carries one. The segment's source is this interface.
pub function (i: Interface) send_tcp(s: conn.Segment): boolean {
  let header = 20;
  if (s.mss > 0) {
    header = 24;
  }
  let tcp: u8[] = [];
  tcp = push_be16(push_be16(tcp, s.source_port), s.port);
  tcp = push_be32(push_be32(tcp, s.seq), s.ack);
  tcp = tcp.append((header / 4 << 4) as u8).append(s.flags as u8);
  tcp = push_be16(push_be16(tcp, s.window), 0);
  tcp = push_be16(tcp, 0);
  if (s.mss > 0) {
    tcp = push_be16(push_all(tcp, [2u8, 4u8]), s.mss);
  }
  tcp = push_all(tcp, s.payload);
  return i.send_ipv4(s.destination, PROTO_TCP, i.with_checksum(s.destination, PROTO_TCP, tcp, 16));
}
```
