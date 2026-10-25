# TLS 1.3 secret derivation

This module derives TLS 1.3 keys and secrets.

## Model

A cipher suite selects a hash and an authenticated cipher. The key schedule derives secrets, keys, and IVs from handshake inputs and transcript hashes.

## Contracts

Digest size and cipher key length follow the selected suite. A caller must use secrets at the correct handshake phase and for the correct direction.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum Hash>>

<<4 pub function (h: Hash) size(): i32>>

<<5 pub enum Aead>>

<<6 pub function (a: Aead) key_len(): i32>>

<<7 pub enum CipherSuite>>

<<8 pub function (s: CipherSuite) code(): i32>>

<<9 function __put(own dst: u8[], at: i32, src: [u8]): u8[]>>

<<10 pub struct EarlySecret>>

<<11 pub function early_secret(h: Hash, psk: [u8]): EarlySecret>>

<<12 pub struct TrafficKeys>>

<<13 pub function traffic_keys(suite: CipherSuite, traffic_secret: [u8]): TrafficKeys>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module derives TLS 1.3 keys and secrets.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/crypto";
```

### `3 pub enum Hash`

```fern
<<3 pub enum Hash>>=
pub enum Hash {
  Sha256,
  Sha384,
}
```

### `4 pub function (h: Hash) size(): i32`

```fern
<<4 pub function (h: Hash) size(): i32>>=
pub function (h: Hash) size(): i32 {
  match (h) {
    Sha256 => {
      return 32;
    },
    Sha384 => {
      return 48;
    }
  }
  return 0;
}

pub function (h: Hash) digest(msg: [u8]): u8[] {
  match (h) {
    Sha256 => {
      return crypto.sha256_new().update(msg).final_bytes();
    },
    Sha384 => {
      return crypto.sha384_new().update(msg).final_bytes();
    }
  }
  return [];
}
```

### `5 pub enum Aead`

```fern
<<5 pub enum Aead>>=
pub enum Aead {
  Aes128Gcm,
  Aes256Gcm,
  ChaCha20Poly1305,
}
```

### `6 pub function (a: Aead) key_len(): i32`

```fern
<<6 pub function (a: Aead) key_len(): i32>>=
pub function (a: Aead) key_len(): i32 {
  match (a) {
    Aes128Gcm => {
      return 16;
    },
    Aes256Gcm => {
      return 32;
    },
    ChaCha20Poly1305 => {
      return 32;
    }
  }
  return 0;
}
```

### `7 pub enum CipherSuite`

```fern
<<7 pub enum CipherSuite>>=
pub enum CipherSuite {
  Aes128GcmSha256,
  Aes256GcmSha384,
  ChaCha20Poly1305Sha256,
}
```

### `8 pub function (s: CipherSuite) code(): i32`

```fern
<<8 pub function (s: CipherSuite) code(): i32>>=
pub function (s: CipherSuite) code(): i32 {
  match (s) {
    Aes128GcmSha256 => {
      return 0x1301;
    },
    Aes256GcmSha384 => {
      return 0x1302;
    },
    ChaCha20Poly1305Sha256 => {
      return 0x1303;
    }
  }
  return 0;
}

pub function cipher_suite(code: i32): Option[CipherSuite] {
  if (code == 0x1301) {
    return Some(Aes128GcmSha256);
  }
  if (code == 0x1302) {
    return Some(Aes256GcmSha384);
  }
  if (code == 0x1303) {
    return Some(ChaCha20Poly1305Sha256);
  }
  return None;
}

pub function (s: CipherSuite) hash(): Hash {
  match (s) {
    Aes128GcmSha256 => {
      return Sha256;
    },
    Aes256GcmSha384 => {
      return Sha384;
    },
    ChaCha20Poly1305Sha256 => {
      return Sha256;
    }
  }
  return Sha256;
}

pub function (s: CipherSuite) aead(): Aead {
  match (s) {
    Aes128GcmSha256 => {
      return Aes128Gcm;
    },
    Aes256GcmSha384 => {
      return Aes256Gcm;
    },
    ChaCha20Poly1305Sha256 => {
      return ChaCha20Poly1305;
    }
  }
  return ChaCha20Poly1305;
}

pub function hkdf_extract(h: Hash, salt: [u8], ikm: [u8]): u8[] {
  match (h) {
    Sha256 => {
      return crypto.hkdf_extract(salt, ikm);
    },
    Sha384 => {
      return crypto.hkdf_sha384_extract(salt, ikm);
    }
  }
  return [];
}

pub function hkdf_expand_label(h: Hash, secret: [u8], label: string, context: [u8], length: i32): u8[] {
  let full: [u8] = label.as_bytes();
  let info: u8[] = __alloc_u8(10 + full.len() + context.len());
  info = info.with(0, (length >> 8 & 255) as u8).with(1, (length & 255) as u8).with(2, (6 + full.len()) as u8);
  info = __put(info, 3, "tls13 ".as_bytes());
  info = __put(info, 9, full);
  info = info.with(9 + full.len(), context.len() as u8);
  info = __put(info, 10 + full.len(), context);
  match (h) {
    Sha256 => {
      return crypto.hkdf_expand(secret, info, length);
    },
    Sha384 => {
      return crypto.hkdf_sha384_expand(secret, info, length);
    }
  }
  return [];
}

pub function derive_secret(h: Hash, secret: [u8], label: string, transcript_hash: [u8]): u8[] {
  return hkdf_expand_label(h, secret, label, transcript_hash, h.size());
}
```

### `9 function __put(own dst: u8[], at: i32, src: [u8]): u8[]`

```fern
<<9 function __put(own dst: u8[], at: i32, src: [u8]): u8[]>>=
function __put(own dst: u8[], at: i32, src: [u8]): u8[] {
  let i: i32 = 0;
  while (i < src.len()) {
    dst = dst.with(at + i, src[i]);
    i = i + 1;
  }
  return dst;
}

function __next(h: Hash, secret: [u8], ikm: [u8]): u8[] {
  let none: u8[] = [];
  return hkdf_extract(h, derive_secret(h, secret, "derived", h.digest(none)), ikm);
}

function __zeros(n: i32): u8[] {
  let z: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    z = z.with(i, 0 as u8);
    i = i + 1;
  }
  return z;
}
```

### `10 pub struct EarlySecret`

```fern
<<10 pub struct EarlySecret>>=
pub struct EarlySecret { hash: Hash, secret: u8[] }

pub struct HandshakeSecret { hash: Hash, secret: u8[] }

pub struct MasterSecret { hash: Hash, secret: u8[] }
```

### `11 pub function early_secret(h: Hash, psk: [u8]): EarlySecret`

```fern
<<11 pub function early_secret(h: Hash, psk: [u8]): EarlySecret>>=
pub function early_secret(h: Hash, psk: [u8]): EarlySecret {
  if (psk.len() == 0) {
    return EarlySecret { hash: h, secret: hkdf_extract(h, __zeros(h.size()), __zeros(h.size())) };
  }
  return EarlySecret { hash: h, secret: hkdf_extract(h, __zeros(h.size()), psk) };
}

pub function (e: EarlySecret) binder_key(): u8[] {
  let none: u8[] = [];
  return derive_secret(e.hash, e.secret, "res binder", e.hash.digest(none));
}

pub function resumption_psk(h: Hash, resumption_master_secret: [u8], ticket_nonce: [u8]): u8[] {
  return hkdf_expand_label(h, resumption_master_secret, "resumption", ticket_nonce, h.size());
}

pub function (e: EarlySecret) handshake_secret(shared_secret: [u8]): HandshakeSecret {
  return HandshakeSecret { hash: e.hash, secret: __next(e.hash, e.secret, shared_secret) };
}

pub function (s: HandshakeSecret) client_traffic(transcript_hash: [u8]): u8[] {
  return derive_secret(s.hash, s.secret, "c hs traffic", transcript_hash);
}

pub function (s: HandshakeSecret) server_traffic(transcript_hash: [u8]): u8[] {
  return derive_secret(s.hash, s.secret, "s hs traffic", transcript_hash);
}

pub function (s: HandshakeSecret) master_secret(): MasterSecret {
  return MasterSecret { hash: s.hash, secret: __next(s.hash, s.secret, __zeros(s.hash.size())) };
}

pub function (m: MasterSecret) client_traffic(transcript_hash: [u8]): u8[] {
  return derive_secret(m.hash, m.secret, "c ap traffic", transcript_hash);
}

pub function (m: MasterSecret) server_traffic(transcript_hash: [u8]): u8[] {
  return derive_secret(m.hash, m.secret, "s ap traffic", transcript_hash);
}

pub function (m: MasterSecret) exporter(transcript_hash: [u8]): u8[] {
  return derive_secret(m.hash, m.secret, "exp master", transcript_hash);
}

pub function (m: MasterSecret) resumption(transcript_hash: [u8]): u8[] {
  return derive_secret(m.hash, m.secret, "res master", transcript_hash);
}
```

### `12 pub struct TrafficKeys`

```fern
<<12 pub struct TrafficKeys>>=
pub struct TrafficKeys { key: u8[], iv: u8[] }
```

### `13 pub function traffic_keys(suite: CipherSuite, traffic_secret: [u8]): TrafficKeys`

```fern
<<13 pub function traffic_keys(suite: CipherSuite, traffic_secret: [u8]): TrafficKeys>>=
pub function traffic_keys(suite: CipherSuite, traffic_secret: [u8]): TrafficKeys {
  let none: u8[] = [];
  let h: Hash = suite.hash();
  return TrafficKeys {
    key: hkdf_expand_label(h, traffic_secret, "key", none, suite.aead().key_len()),
    iv: hkdf_expand_label(h, traffic_secret, "iv", none, 12),
  };
}

pub function finished_verify_data(h: Hash, base_key: [u8], transcript_hash: [u8]): u8[] {
  let none: u8[] = [];
  let finished_key: u8[] = hkdf_expand_label(h, base_key, "finished", none, h.size());
  match (h) {
    Sha256 => {
      return crypto.hmac_sha256(finished_key, transcript_hash);
    },
    Sha384 => {
      return crypto.hmac_sha384(finished_key, transcript_hash);
    }
  }
  return [];
}

pub function verify_finished(h: Hash, base_key: [u8], transcript_hash: [u8], verify_data: u8[]): boolean {
  return crypto.consteq(finished_verify_data(h, base_key, transcript_hash), verify_data);
}

pub function next_traffic_secret(h: Hash, traffic_secret: [u8]): u8[] {
  let none: u8[] = [];
  return hkdf_expand_label(h, traffic_secret, "traffic upd", none, h.size());
}
```
