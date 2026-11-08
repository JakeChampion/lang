# TLS 1.2 client parts

This module holds the parts of TLS 1.2 (RFC 5246) that the client of `std/tls/handshake` uses when a server speaks only TLS 1.2.

## Model

The handshake offers TLS 1.2 beside TLS 1.3 when its configuration asks for it. A server that picks TLS 1.2 sends a ServerHello with no `supported_versions` extension. The handshake then reads the TLS 1.2 messages with the parsers of this module. It derives the keys with the PRF, and it protects the records with `seal` and `open`.

The module takes only ephemeral elliptic-curve key exchange (ECDHE) with an AEAD: AES-GCM (RFC 5289) and ChaCha20-Poly1305 (RFC 7905). It has no RSA key exchange, no CBC suites, no compression and no renegotiation. A TLS 1.2 suite has the same AEAD and PRF hash as one TLS 1.3 suite, so `suite` gives that `keyschedule.CipherSuite`.

AES-GCM records carry an explicit 8-byte nonce: the sequence number. The implicit part is the 4-byte salt from the key block. ChaCha20-Poly1305 records have no explicit nonce: the 12-byte IV is XORed with the sequence number. The additional data of both is the sequence number, the content type, the version and the length of the plaintext.

## Contracts

The functions do no I/O and keep no state. A parser refuses a message that does not use all of its bytes. `seal` and `open` give the protection with the next sequence number. They refuse a plaintext of more than 2^14 bytes and a sequence number that cannot advance.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<suites>>

<<key derivation>>

<<record protection>>

<<messages>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module holds the parts of TLS 1.2 that its client uses.
```

### `dependencies`

```fern
<<dependencies>>=
import "std/crypto";
import "std/crypto/aes_gcm";
import "std/crypto/chacha20poly1305";
import "std/tls/keyschedule";
import "std/tls/message";
import "std/tls/record";
```

### `suites`

The client offers the suites in this order: ECDSA before RSA, AES-128 before AES-256, and ChaCha20-Poly1305 last. Each extension code is the one the client hello sends when it offers TLS 1.2.

```fern
<<suites>>=
pub const VERSION: i32 = 0x0303;

// ECDHE_ECDSA and ECDHE_RSA with AES-128-GCM, AES-256-GCM and ChaCha20-Poly1305.
pub const SUITES: i32[] = [0xc02b, 0xc02f, 0xc02c, 0xc030, 0xcca9, 0xcca8];

// RFC 8422 ec_point_formats, RFC 7627 extended_master_secret and RFC 5746 renegotiation_info.
pub const EXT_EC_POINT_FORMATS: i32 = 11;

pub const EXT_EXTENDED_MASTER_SECRET: i32 = 23;

pub const EXT_RENEGOTIATION_INFO: i32 = 0xff01;

// The TLS 1.3 suite with the AEAD and the hash of a TLS 1.2 suite, or None for a suite this module does not take.
pub function suite(code: i32): Option[keyschedule.CipherSuite] {
  if (code == 0xc02b || code == 0xc02f) {
    return Some(keyschedule.Aes128GcmSha256);
  }
  if (code == 0xc02c || code == 0xc030) {
    return Some(keyschedule.Aes256GcmSha384);
  }
  if (code == 0xcca9 || code == 0xcca8) {
    return Some(keyschedule.ChaCha20Poly1305Sha256);
  }
  return None;
}

// The extensions that offer TLS 1.2: uncompressed points, the extended master secret and an empty renegotiation_info.
pub function hello_extensions(): message.Extension[] {
  return [message.Extension { kind: EXT_EC_POINT_FORMATS, data: [1 as u8, 0 as u8] }, message.Extension { kind: EXT_EXTENDED_MASTER_SECRET, data: [] }, message.Extension { kind: EXT_RENEGOTIATION_INFO, data: [0 as u8] }];
}

// True when the last 8 bytes of a TLS 1.2 server random are the sentinel of a server that speaks TLS 1.3 (RFC 8446 §4.1.3).
pub function downgraded(server_random: [u8]): boolean {
  let sentinel: u8[] = [0x44 as u8, 0x4f as u8, 0x57 as u8, 0x4e as u8, 0x47 as u8, 0x52 as u8, 0x44 as u8, 0x01 as u8];
  if (server_random.len() != 32) {
    return false;
  }
  let i = 0;
  while (i < 8) {
    if (server_random[24 + i] != sentinel[i]) {
      return false;
    }
    i += 1;
  }
  return true;
}
```

### `key derivation`

The PRF is P_hash of RFC 5246 §5 with the hash of the suite. When the server echoes the extension, the master secret is the extended master secret of RFC 7627. When it does not, it is the RFC 5246 one.

```fern
<<key derivation>>=
function __hmac(h: keyschedule.Hash, key: [u8], msg: [u8]): u8[] {
  match (h) {
    keyschedule.Sha384 => {
      return crypto.hmac_sha384(key, msg);
    },
    keyschedule.Sha256 => {
      return crypto.hmac_sha256(key, msg);
    }
  }
  return crypto.hmac_sha256(key, msg);
}

// length bytes of P_hash(secret, label + seed).
pub function prf(h: keyschedule.Hash, secret: [u8], label: string, seed: [u8], length: i32): u8[] {
  let labeled = label.bytes().concat(__bytes_owned(seed));
  let a = __hmac(h, secret, labeled);
  let out: u8[] = [];
  while (out.len() < length) {
    out = out.concat(__hmac(h, secret, __bytes_owned(a).concat(__bytes_owned(labeled))));
    a = __hmac(h, secret, a);
  }
  return __bytes_owned(out[0:length]);
}

// The 48-byte master secret: extended over the session hash, or over the two randoms.
pub function master_secret(h: keyschedule.Hash, pre_master: [u8], extended: boolean, session_hash: [u8], client_random: [u8], server_random: [u8]): u8[] {
  if (extended) {
    return prf(h, pre_master, "extended master secret", session_hash, 48);
  }
  return prf(h, pre_master, "master secret", __bytes_owned(client_random).concat(__bytes_owned(server_random)), 48);
}

// The 12 bytes of a Finished message. label is "client finished" or "server finished".
pub function verify_data(h: keyschedule.Hash, master: [u8], label: string, transcript_hash: [u8]): u8[] {
  return prf(h, master, label, transcript_hash, 12);
}

pub struct Keys { client: record.Protection, server: record.Protection }

// The protections of both directions from the key block.
pub function keys(suite: keyschedule.CipherSuite, master: [u8], client_random: [u8], server_random: [u8]): Keys {
  let kl = suite.aead().key_len();
  let ivl = __iv_len(suite);
  let block = prf(suite.hash(), master, "key expansion", __bytes_owned(server_random).concat(__bytes_owned(client_random)), 2 * kl + 2 * ivl);
  let client = protection(suite, block[0:kl], block[2 * kl:2 * kl + ivl]);
  let server = protection(suite, block[kl:2 * kl], block[2 * kl + ivl:2 * kl + 2 * ivl]);
  return Keys { client: client, server: server };
}

function __iv_len(suite: keyschedule.CipherSuite): i32 {
  match (suite.aead()) {
    keyschedule.ChaCha20Poly1305 => {
      return 12;
    },
    _ => {}
  }
  return 4;
}
```

### `record protection`

```fern
<<record protection>>=
// A protection for one direction: the key, and the 4-byte salt of AES-GCM or the 12-byte IV of ChaCha20-Poly1305.
pub function protection(suite: keyschedule.CipherSuite, key: [u8], iv: [u8]): record.Protection {
  let p = record.Protection { suite: suite, key: __bytes_owned(key), iv: __bytes_owned(iv), seq: 0 as u64, gcm: None, chacha: None };
  match (suite.aead()) {
    keyschedule.ChaCha20Poly1305 => {
      match (chacha20poly1305.key(key)) {
        Ok(k) => {
          return record.Protection { ...p, chacha: Some(k) };
        },
        Err(_) => {
          return p;
        }
      }
    },
    _ => {}
  }
  match (aes_gcm.key(key)) {
    Ok(k) => {
      return record.Protection { ...p, gcm: Some(k) };
    },
    Err(_) => {
      return p;
    }
  }
  return p;
}

function __seq_bytes(seq: u64): u8[] {
  let out: u8[] = [];
  let shift = 56;
  while (shift >= 0) {
    out = out.append((seq >> shift as u64 & 255 as u64) as u8);
    shift -= 8;
  }
  return out;
}

function __aad(seq: u64, t: i32, n: i32): u8[] {
  return __seq_bytes(seq).append(t as u8).append(3 as u8).append(3 as u8).append((n >> 8) as u8).append((n & 255) as u8);
}

// The ChaCha20-Poly1305 nonce: the IV XOR the sequence number in its last 8 bytes.
function __chacha_nonce(iv: [u8], seq: u64): u8[] {
  let s = __seq_bytes(seq);
  let out = __bytes_owned(iv);
  let i = 0;
  while (i < 8) {
    out = out.with(4 + i, out[4 + i] ^ s[i]);
    i += 1;
  }
  return out;
}

function __ready(p: record.Protection): Result[void, record.RecordError] {
  if (p.key.len() != p.suite.aead().key_len() || p.iv.len() != __iv_len(p.suite)) {
    return Err(record.BadKeys(p.key.len(), p.iv.len()));
  }
  if (p.seq == 0xffffffffffffffff as u64) {
    return Err(record.SequenceExhausted);
  }
  return Ok(());
}

// One record of content of type t, framed with the explicit nonce of AES-GCM in front of the sealed bytes.
pub function seal(p: record.Protection, t: record.ContentType, content: [u8]): Result[record.Sealed, record.RecordError] {
  __ready(p)?;
  if (content.len() > 16384) {
    return Err(record.RecordOverflow(content.len()));
  }
  let aad = __aad(p.seq, t.code(), content.len());
  let fragment: u8[] = [];
  if let Some(g) = p.gcm {
    let Ok(sealed) = g.seal(__bytes_owned(p.iv).concat(__seq_bytes(p.seq)), content, aad) else {
      return Err(record.BadKeys(p.key.len(), p.iv.len()));
    };
    fragment = __seq_bytes(p.seq).concat(sealed);
  } else if let Some(c) = p.chacha {
    let Ok(sealed) = c.seal(__chacha_nonce(p.iv, p.seq), content, aad) else {
      return Err(record.BadKeys(p.key.len(), p.iv.len()));
    };
    fragment = sealed;
  } else {
    return Err(record.BadKeys(p.key.len(), p.iv.len()));
  }
  return Ok(record.Sealed { record: record.frame(t, VERSION, fragment), next: record.Protection { ...p, seq: p.seq + 1 as u64 } });
}

// The content of a protected record. The content type is the one in the record header.
pub function open(p: record.Protection, own r: record.Record): Result[record.Opened, record.RecordError] {
  __ready(p)?;
  let frag = r.fragment;
  let content: u8[] = [];
  if let Some(g) = p.gcm {
    if (frag.len() < 8 + 16) {
      return Err(record.BadRecordMac);
    }
    let n = frag.len() - 8 - 16;
    let Ok(opened) = g.open(__bytes_owned(p.iv).concat(__bytes_owned(frag[0:8])), frag[8:frag.len()], __aad(p.seq, r.content_type.code(), n)) else {
      return Err(record.BadRecordMac);
    };
    content = opened;
  } else if let Some(c) = p.chacha {
    if (frag.len() < 16) {
      return Err(record.BadRecordMac);
    }
    let Ok(opened) = c.open(__chacha_nonce(p.iv, p.seq), frag, __aad(p.seq, r.content_type.code(), frag.len() - 16)) else {
      return Err(record.BadRecordMac);
    };
    content = opened;
  } else {
    return Err(record.BadKeys(p.key.len(), p.iv.len()));
  }
  if (content.len() > 16384) {
    return Err(record.RecordOverflow(content.len()));
  }
  return Ok(record.Opened { content_type: r.content_type, content: content, next: record.Protection { ...p, seq: p.seq + 1 as u64 } });
}
```

### `messages`

Each parser takes a whole handshake message: the type, the 3-byte length and the body. The TLS 1.2 Certificate has a different form from the TLS 1.3 one, and TLS 1.3 has no ServerKeyExchange.

```fern
<<messages>>=
// The ECDHE parameters and their signature. params is the part that the signature covers after the two randoms.
pub struct KeyExchange { group: i32, public: u8[], params: u8[], scheme: i32, signature: u8[] }

function __u16(b: [u8], at: i32): i32 {
  return b[at] as i32 << 8 | b[at + 1] as i32;
}

function __u24(b: [u8], at: i32): i32 {
  return b[at] as i32 << 16 | b[at + 1] as i32 << 8 | b[at + 2] as i32;
}

// The body of a whole handshake message whose length field matches.
function __body(raw: [u8], what: string): Result[u8[], message.MsgError] {
  if (raw.len() < 4 || __u24(raw, 1) != raw.len() - 4) {
    return Err(message.DecodeError(what));
  }
  return Ok(__bytes_owned(raw[4:raw.len()]));
}

// The DER certificates of a TLS 1.2 Certificate message, leaf first.
pub function certificate_chain(raw: [u8]): Result[u8[][], message.MsgError] {
  let b = __body(raw, "certificate")?;
  if (b.len() < 3 || __u24(b, 0) != b.len() - 3) {
    return Err(message.DecodeError("certificate_list"));
  }
  let chain: u8[][] = [];
  let at = 3;
  while (at < b.len()) {
    if (at + 3 > b.len()) {
      return Err(message.DecodeError("certificate_list"));
    }
    let n = __u24(b, at);
    if (n == 0 || at + 3 + n > b.len()) {
      return Err(message.DecodeError("certificate_list"));
    }
    chain = chain.append(__bytes_owned(b[at + 3:at + 3 + n]));
    at += 3 + n;
  }
  if (chain.len() == 0) {
    return Err(message.DecodeError("certificate_list"));
  }
  return Ok(chain);
}

// The named-curve ECDHE parameters and the signature of a ServerKeyExchange.
pub function key_exchange(raw: [u8]): Result[KeyExchange, message.MsgError] {
  let b = __body(raw, "server_key_exchange")?;
  if (b.len() < 4 || b[0] != 3 as u8) {
    return Err(message.DecodeError("ECParameters"));
  }
  let n = b[3] as i32;
  let end = 4 + n;
  if (n == 0 || end + 4 > b.len()) {
    return Err(message.DecodeError("ECPoint"));
  }
  let sig_len = __u16(b, end + 2);
  if (end + 4 + sig_len != b.len() || sig_len == 0) {
    return Err(message.DecodeError("signature"));
  }
  return Ok(KeyExchange { group: __u16(b, 1), public: __bytes_owned(b[4:end]), params: __bytes_owned(b[0:end]), scheme: __u16(b, end), signature: __bytes_owned(b[end + 4:b.len()]) });
}

// The 12 bytes of verify_data of a Finished message.
pub function finished(raw: [u8]): Result[u8[], message.MsgError] {
  let b = __body(raw, "finished")?;
  if (b.len() != 12) {
    return Err(message.DecodeError("verify_data"));
  }
  return Ok(b);
}

// True when a handshake message has an empty body, as a ServerHelloDone and a HelloRequest have.
pub function empty(raw: [u8]): boolean {
  return raw.len() == 4 && __u24(raw, 1) == 0;
}

// A whole handshake message of type kind with body.
pub function handshake_message(kind: i32, body: [u8]): u8[] {
  let n = body.len();
  return [kind as u8, (n >> 16) as u8, (n >> 8 & 255) as u8, (n & 255) as u8].concat(__bytes_owned(body));
}

// A ClientKeyExchange with the client's ECDHE public point.
pub function client_key_exchange(public: [u8]): u8[] {
  return handshake_message(message.CLIENT_KEY_EXCHANGE, [public.len() as u8].concat(__bytes_owned(public)));
}
```
