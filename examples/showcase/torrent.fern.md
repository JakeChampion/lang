# Torrent kernel

The torrent page shares a file between browsers with the BitTorrent wire protocol over WebRTC, as WebTorrent does.
This module is the protocol half of the page. It writes and reads bencode and builds the metainfo of a single file.
It also computes the info hash, checks each piece with SHA-1, and builds the wire messages.
The page's JavaScript only opens the WebRTC data channels and moves bytes. The build compiles this document to the `torrent.wasm` kernel.

```fern
<<*>>=
<<Imports>>
<<Bencode>>
<<Metainfo>>
<<Wire messages>>
<<Sample file>>
<<Command entry>>
```

## Imports

```fern
<<Imports>>=
import "std/crypto";
import "std/array";
```

## Bencode

BitTorrent encodes its dictionaries with bencode. A string is its length, a colon, and its bytes. An integer is `i`, the digits, and `e`.
A dictionary is `d`, its keys and values in key order, and `e`.

The reader returns the end of the value at a position, or −1 for bytes that are not bencode. It also reads the integers of a dictionary that the protocol needs.
`int_field` finds an integer value by its key at the top level of a dictionary, or in the dictionary under `m`.

```fern
<<Bencode>>=
function text(s: string): u8[] {
  return s.bytes();
}

function bstr(b: u8[]): u8[] {
  return text(b.len().to_string() + ":").concat(b);
}

function bint(n: i64): u8[] {
  return text("i" + n.to_string() + "e");
}

function digits(b: u8[], at: i32, stop: u8): (i64, i32) {
  let i = at;
  let negative = false;
  if (i < b.len() && b[i] == b'-') {
    negative = true;
    i = i + 1;
  }
  let n: i64 = 0;
  let start = i;
  while (i < b.len() && b[i] >= b'0' && b[i] <= b'9' && i - start < 18) {
    n = n * 10 + (b[i] - b'0') as i64;
    i = i + 1;
  }
  if (i == start || i >= b.len() || b[i] != stop) {
    return (0, 0 - 1);
  }
  if (negative) {
    n = 0 - n;
  }
  return (n, i + 1);
}

// The end of the bencoded value at `at`, or -1.
function skip(b: u8[], at: i32, depth: i32): i32 {
  if (at >= b.len() || depth > 32) {
    return 0 - 1;
  }
  let c = b[at];
  if (c == b'i') {
    let (n, end) = digits(b, at + 1, b'e');
    return end;
  }
  if (c == b'l' || c == b'd') {
    let i = at + 1;
    while (i >= 0 && i < b.len() && b[i] != b'e') {
      i = skip(b, i, depth + 1);
    }
    if (i < 0 || i >= b.len()) {
      return 0 - 1;
    }
    return i + 1;
  }
  let (len, start) = digits(b, at, b':');
  if (start < 0 || len < 0 || len > (b.len() - start) as i64) {
    return 0 - 1;
  }
  return start + len as i32;
}

// The start of the value under `key` in the dictionary at `at`, or -1.
function lookup(b: u8[], at: i32, key: string): i32 {
  if (at >= b.len() || b[at] != b'd') {
    return 0 - 1;
  }
  let want = key.bytes();
  let i = at + 1;
  while (i >= 0 && i < b.len() && b[i] != b'e') {
    let (len, start) = digits(b, i, b':');
    if (start < 0 || len < 0 || len > (b.len() - start) as i64) {
      return 0 - 1;
    }
    let value = start + len as i32;
    if (crypto.consteq(b[start:value], want[:])) {
      return value;
    }
    i = skip(b, value, 1);
  }
  return 0 - 1;
}

function int_at(b: u8[], at: i32): i64 {
  if (at < 0 || at >= b.len() || b[at] != b'i') {
    return 0 - 1;
  }
  let (n, end) = digits(b, at + 1, b'e');
  if (end < 0) {
    return 0 - 1;
  }
  return n;
}

function bytes_at(b: u8[], at: i32): u8[] {
  if (at < 0) {
    return [];
  }
  let (len, start) = digits(b, at, b':');
  if (start < 0 || len < 0 || len > (b.len() - start) as i64) {
    return [];
  }
  return array.slice(b, start, start + len as i32);
}
```

## Metainfo

A single-file info dictionary holds four keys in order: `length`, `name`, `piece length`, and `pieces`, the SHA-1 hashes of the pieces one after another.
The info hash is the SHA-1 hash of the bencoded dictionary. It names the swarm on the tracker and in the magnet link.

`describe` reads an info dictionary that a peer sent and returns its fields as JSON. It returns an empty string for a dictionary that is not a valid single file.
`piece_ok` compares the hash of one received piece with the hash in the metainfo.

```fern
<<Metainfo>>=
@export("fern:showcase/torrent", "info")
pub function info(name: string, data: u8[], piece_length: i32): u8[] {
  let pieces: u8[] = [];
  let at = 0;
  while (at < data.len()) {
    let end = (at + piece_length).min(data.len());
    pieces = pieces.concat(crypto.sha1(data[at:end]));
    at = end;
  }
  return text("d6:length").concat(bint(data.len() as i64)).concat(text("4:name")).concat(bstr(name.bytes())).concat(text("12:piece length")).concat(bint(piece_length as i64)).concat(text("6:pieces")).concat(bstr(pieces)).concat(text("e"));
}

@export("fern:showcase/torrent", "infohash")
pub function infohash(info: u8[]): u8[] {
  return crypto.sha1(info);
}

@export("fern:showcase/torrent", "torrent-file")
pub function torrent_file(info: u8[], announce: string): u8[] {
  return text("d8:announce").concat(bstr(announce.bytes())).concat(text("4:info")).concat(info).concat(text("e"));
}

// A JSON string of printable ASCII. Other bytes become `?`.
function json_text(b: u8[]): string {
  let out: u8[] = [b'"'];
  for c in b {
    if (c == b'"' || c == b'\\') {
      out = out.append(b'\\').append(c);
    } else if (c < 32 || c > 126) {
      out = out.append(b'?');
    } else {
      out = out.append(c);
    }
  }
  return string_from_bytes_unchecked(out.append(b'"'));
}

@export("fern:showcase/torrent", "describe")
pub function describe(info: u8[]): string {
  if (skip(info, 0, 0) != info.len() || lookup(info, 0, "files") >= 0) {
    return "";
  }
  let length = int_at(info, lookup(info, 0, "length"));
  let piece_length = int_at(info, lookup(info, 0, "piece length"));
  let name = bytes_at(info, lookup(info, 0, "name"));
  let pieces = bytes_at(info, lookup(info, 0, "pieces"));
  if (length < 0 || piece_length <= 0 || pieces.len() % 20 != 0) {
    return "";
  }
  let count = (length + piece_length - 1) / piece_length;
  if (count != (pieces.len() / 20) as i64) {
    return "";
  }
  return "{\"name\":" + json_text(name) + ",\"length\":" + length.to_string() + ",\"piece_length\":" + piece_length.to_string() + ",\"pieces\":" + count.to_string() + "}";
}

@export("fern:showcase/torrent", "piece-ok")
pub function piece_ok(info: u8[], index: i32, data: u8[]): i32 {
  let pieces = bytes_at(info, lookup(info, 0, "pieces"));
  if (index < 0 || (index + 1) * 20 > pieces.len()) {
    return 0;
  }
  if (crypto.consteq(crypto.sha1(data)[:], pieces[index * 20:index * 20 + 20])) {
    return 1;
  }
  return 0;
}
```

## Wire messages

A connection starts with a handshake: the protocol name, eight reserved bytes, the info hash, and the peer ID.
Bit 0x10 of the sixth reserved byte announces the extension protocol, which carries the metadata exchange (`ut_metadata`, BEP 9).
Every later message is a four-byte big-endian length, a message ID, and a payload. Message 20 is an extension message, whose first payload byte is the extension ID.

A browser that opens a magnet link has only the info hash. It asks a peer for the info dictionary in 16 KiB pieces and checks the result against the hash.

`ext_fields` reads the integer fields of an extension payload: `msg_type`, `piece`, `total_size`, `metadata_size`, and `ut_metadata` under `m`.
It also gives `data`, the offset where the raw bytes of a metadata piece start.

```fern
<<Wire messages>>=
const PROTOCOL: string = "BitTorrent protocol";

@export("fern:showcase/torrent", "handshake")
pub function handshake(info_hash: u8[], peer_id: u8[]): u8[] {
  let reserved: u8[] = [0, 0, 0, 0, 0, 16, 0, 0];
  return [19 as u8].concat(text(PROTOCOL)).concat(reserved).concat(info_hash).concat(peer_id);
}

function frame(payload: u8[]): u8[] {
  let n = payload.len();
  let head: u8[] = [(n >> 24 & 255) as u8, (n >> 16 & 255) as u8, (n >> 8 & 255) as u8, (n & 255) as u8];
  return head.concat(payload);
}

function extended(ext_id: i32, body: u8[]): u8[] {
  return frame([20 as u8, ext_id as u8].concat(body));
}

@export("fern:showcase/torrent", "ext-handshake")
pub function ext_handshake(metadata_size: i32): u8[] {
  let body = text("d1:md11:ut_metadatai1ee");
  if (metadata_size > 0) {
    body = text("d1:md11:ut_metadatai1ee13:metadata_size").concat(bint(metadata_size as i64)).concat(text("e"));
  } else {
    body = body.concat(text("e"));
  }
  return extended(0, body);
}

@export("fern:showcase/torrent", "metadata-request")
pub function metadata_request(ext_id: i32, piece: i32): u8[] {
  return extended(ext_id, text("d8:msg_typei0e5:piece").concat(bint(piece as i64)).concat(text("e")));
}

@export("fern:showcase/torrent", "metadata-piece")
pub function metadata_piece(ext_id: i32, info: u8[], piece: i32): u8[] {
  let at = piece * 16384;
  if (piece < 0 || at >= info.len()) {
    return extended(ext_id, text("d8:msg_typei2e5:piece").concat(bint(piece as i64)).concat(text("e")));
  }
  let end = (at + 16384).min(info.len());
  let head = text("d8:msg_typei1e5:piece").concat(bint(piece as i64)).concat(text("10:total_size")).concat(bint(info.len() as i64)).concat(text("e"));
  return extended(ext_id, head.concat(array.slice(info, at, end)));
}

function field(name: string, value: i64): string {
  return "\"" + name + "\":" + value.to_string();
}

@export("fern:showcase/torrent", "ext-fields")
pub function ext_fields(payload: u8[]): string {
  let end = skip(payload, 0, 0);
  if (end < 0) {
    return "";
  }
  let parts: string[] = [field("data", end as i64)];
  for key in ["msg_type", "piece", "total_size", "metadata_size"] {
    let n = int_at(payload, lookup(payload, 0, key));
    if (n >= 0) {
      parts = parts.append(field(key, n));
    }
  }
  let m = lookup(payload, 0, "m");
  if (m >= 0) {
    let id = int_at(payload, lookup(payload, m, "ut_metadata"));
    if (id >= 0) {
      parts = parts.append(field("ut_metadata", id));
    }
  }
  return "{" + parts.join(",") + "}";
}
```

## Sample file

The page offers a generated text file for a visitor with no file at hand. A xorshift step makes the same text from the same seed.

```fern
<<Sample file>>=
const WORDS: string[] = ["fern", "frond", "spore", "moss", "lichen", "cedar", "river", "stone", "light", "shade", "root", "leaf", "bark", "seed", "rain", "field"];

@export("fern:showcase/torrent", "sample")
pub function sample(seed: i32, size: i32): u8[] {
  let s: u32 = seed as u32 * 2654435761 | 1;
  let out: u8[] = [];
  let line = 0;
  while (out.len() < size) {
    let words: string[] = [];
    for i in 0..10 {
      s = s ^ s << 13;
      s = s ^ s >> 17;
      s = s ^ s << 5;
      words = words.append(WORDS[(s % 16) as i32]);
    }
    out = out.concat(text(line.to_string() + " " + words.join(" ") + "\n"));
    line = line + 1;
  }
  return array.slice(out, 0, size);
}
```

## Command entry

The browser instantiates the core module and calls its exports. It never calls `_start`.

```fern
<<Command entry>>=
function main(): i32 {
  return 0;
}
```

## Check the behavior

The metainfo describes itself, each piece passes its own check, and the extension fields read back.

```fern test name=torrent-example
<<Imports>>
<<Bencode>>
<<Metainfo>>
<<Wire messages>>
<<Sample file>>
function main(): i32 {
  let data = sample(4, 40000);
  if (data.len() != 40000) { return 1; }
  let meta = info("fern.txt", data, 16384);
  if (describe(meta) != "{\"name\":\"fern.txt\",\"length\":40000,\"piece_length\":16384,\"pieces\":3}") { return 2; }
  if (piece_ok(meta, 2, array.slice(data, 32768, data.len())) != 1 || piece_ok(meta, 1, array.slice(data, 0, 16384)) != 0) { return 3; }
  if (infohash(meta).len() != 20 || handshake(infohash(meta), infohash(meta)).len() != 68) { return 4; }
  let hello = ext_handshake(meta.len());
  if (ext_fields(array.slice(hello, 6, hello.len())) != "{\"data\":" + (hello.len() - 6).to_string() + ",\"metadata_size\":" + meta.len().to_string() + ",\"ut_metadata\":1}") { return 5; }
  let piece = metadata_piece(3, meta, 0);
  if (piece[5] != 3 as u8) { return 6; }
  let fields = ext_fields(array.slice(piece, 6, piece.len()));
  if (fields != "{\"data\":" + (piece.len() - 6 - meta.len()).to_string() + ",\"msg_type\":1,\"piece\":0,\"total_size\":" + meta.len().to_string() + "}") { return 7; }
  if (describe(text("d5:filesle4:name1:xe")) != "" || describe(text("garbage")) != "") { return 8; }
  return 0;
}
```
