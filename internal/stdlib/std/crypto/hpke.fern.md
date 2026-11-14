# HPKE with the MLKEM768-X25519 KEM

This module implements Hybrid Public Key Encryption (HPKE, RFC 9180) in the base mode. The KEM is MLKEM768-X25519 of draft-ietf-hpke-pq, also known as X-Wing. The KDF is HKDF-SHA256 and the AEAD is ChaCha20Poly1305.

## Model

A key is a 32-byte seed. SHAKE256 expands the seed to 96 bytes: the first 64 bytes are the ML-KEM-768 seed, and the last 32 bytes are the X25519 scalar. The public key is the ML-KEM-768 encapsulation key (1184 bytes) and then the X25519 public key (32 bytes).

Encapsulation makes an ML-KEM-768 ciphertext and an ephemeral X25519 share. The encapsulated key `enc` is the ciphertext (1088 bytes) and then the share (32 bytes). The shared secret is SHA3-256 of the ML-KEM-768 secret, the X25519 secret, the share, the X25519 public key of the recipient and the label `\.//^\`.

The key schedule of RFC 9180 turns the shared secret and the `info` bytes into a `Context`. A `Context` seals or opens messages in sequence. Each message uses the next nonce.

## Contracts

The seed is 32 bytes, the public key 1216 bytes and `enc` 1120 bytes. Each other length gives an error that names the length. An ML-KEM-768 encapsulation key with a coefficient not below q gives `PublicKey`. An X25519 secret of all zeros gives `LowOrder`, on both sides. ML-KEM-768 uses implicit rejection, so a wrong key or a damaged `enc` gives a different shared secret, and then `open` gives `Open`.

The seed, the X25519 scalars, the shared secrets, the AEAD key and the nonces are secret under the constant-time gate. `mlkem768x25519_key` makes the public key public, and `mlkem768x25519_encap_derand` makes the ephemeral share public. `open` makes only its verdict public.

## Executable example

```fern test name=std-crypto-hpke-example
import "std/crypto/hpke";

function main(): i32 {
  let seed = __alloc_u8(32);
  let randomness = __alloc_u8(64).with(0, 1 as u8).with(32, 2 as u8);
  let info = "example".as_bytes();
  let Ok(k) = hpke.mlkem768x25519_key(seed) else { return 1; };
  let Ok(sender) = hpke.setup_sender_derand(k.public_key, info, randomness) else { return 2; };
  let (sealed, _) = sender.context.seal([], "hello".as_bytes());
  let Ok(recipient) = hpke.setup_recipient(k, sender.enc, info) else { return 3; };
  let Ok(opened) = recipient.open([], sealed) else { return 4; };
  if (opened.0.len() != 5 || opened.0[0] != b'h' || opened.0[4] != b'o') { return 5; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum HpkeError>>

<<4 the MLKEM768-X25519 KEM>>

<<5 the key schedule>>

<<6 pub struct Context>>

<<7 the sender and the recipient>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module implements HPKE (RFC 9180) in the base mode with the
// MLKEM768-X25519 KEM of draft-ietf-hpke-pq, HKDF-SHA256 and ChaCha20Poly1305.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/crypto";
import "std/crypto/chacha20poly1305";
import "std/crypto/mlkem768";
import "std/crypto/x25519";
import "core/error";
import "core/cmp";
```

### `3 pub enum HpkeError`

Each length error keeps the length that the caller gave.

```fern
<<3 pub enum HpkeError>>=
pub enum HpkeError {
  SeedLength(i32),
  RandomnessLength(i32),
  PublicKeyLength(i32),
  EncLength(i32),
  PublicKey,
  LowOrder,
  Open,
}

impl error.Error for HpkeError {}

impl cmp.Display for HpkeError {}

pub function (e: HpkeError) to_string(): string {
  return e.message();
}

pub function (e: HpkeError) message(): string {
  return match (e) {
    SeedLength(n) => "hpke: the seed is " + n.to_string() + " bytes, want 32",
    RandomnessLength(n) => "hpke: the randomness is " + n.to_string() + " bytes, want 64",
    PublicKeyLength(n) => "hpke: the public key is " + n.to_string() + " bytes, want 1216",
    EncLength(n) => "hpke: the encapsulated key is " + n.to_string() + " bytes, want 1120",
    PublicKey => "hpke: the ML-KEM-768 encapsulation key has a coefficient not below q",
    LowOrder => "hpke: the X25519 shared secret is all zeros",
    Open => "hpke: the message does not authenticate",
  };
}
```

### `4 the MLKEM768-X25519 KEM`

`HybridKey` holds the expanded seed: the public key, the ML-KEM-768 decapsulation key and the X25519 scalar. The KEM identifier is 0x647a.

```fern
<<4 the MLKEM768-X25519 KEM>>=
pub const KEM_MLKEM768_X25519: i32 = 0x647a;

pub const SEED_SIZE: i32 = 32;

pub const PUBLIC_KEY_SIZE: i32 = 1216;

pub const ENC_SIZE: i32 = 1120;

pub struct HybridKey {
  public_key: u8[],
  decapsulation_key: u8[],
  scalar: u8[],
}

pub struct Encapsulation {
  shared_secret: u8[],
  enc: u8[],
}

const XWING_LABEL: u8[] = [92u8, 46u8, 47u8, 47u8, 94u8, 92u8];

function __combine(ss_pq: [u8], ss_t: [u8], ct_t: [u8], ek_t: [u8]): u8[] {
  return crypto.sha3_256_new().update(ss_pq).update(ss_t).update(ct_t).update(ek_t).update(XWING_LABEL).finish();
}

pub function mlkem768x25519_key(seed: [u8]): Result[HybridKey, HpkeError] {
  if (seed.len() != SEED_SIZE) {
    return Err(SeedLength(seed.len()));
  }
  let full = crypto.shake256(seed, 96);
  let Ok(pq) = mlkem768.keygen_derand(full[0:32], full[32:64]) else {
    return Err(SeedLength(seed.len()));
  };
  let scalar = __bytes_into(__alloc_u8(32), 0, full, 64, 32);
  crypto.erase(full);
  let Ok(ek_t) = x25519.public_key(scalar) else {
    return Err(SeedLength(seed.len()));
  };
  let public_key = __bytes_into(__bytes_into(__alloc_u8(PUBLIC_KEY_SIZE), 0, pq.encapsulation_key, 0, 1184), 1184, ek_t, 0, 32);
  __ct_public(public_key);
  return Ok(HybridKey { public_key: public_key, decapsulation_key: pq.decapsulation_key, scalar: scalar });
}

// The checks of a public key before use: its length and the modulus of its
// ML-KEM-768 part. The X25519 part can be any 32 bytes.
pub function mlkem768x25519_check_public_key(public_key: [u8]): Result[void, HpkeError] {
  if (public_key.len() != PUBLIC_KEY_SIZE) {
    return Err(PublicKeyLength(public_key.len()));
  }
  if let Err(_) = mlkem768.check_encapsulation_key(public_key[0:1184]) {
    return Err(PublicKey);
  }
  return Ok(());
}

// Encapsulation with caller-supplied randomness: the ML-KEM-768 message in
// the first 32 bytes, and the ephemeral X25519 scalar in the last 32 bytes.
pub function mlkem768x25519_encap_derand(public_key: [u8], randomness: [u8]): Result[Encapsulation, HpkeError] {
  if (public_key.len() != PUBLIC_KEY_SIZE) {
    return Err(PublicKeyLength(public_key.len()));
  }
  if (randomness.len() != 64) {
    return Err(RandomnessLength(randomness.len()));
  }
  let ek_t = public_key[1184:1216];
  let pq = match (mlkem768.encaps_derand(public_key[0:1184], randomness[0:32])) {
    Ok(s) => s,
    Err(_) => {
      return Err(PublicKey);
    },
  };
  let Ok(ct_t) = x25519.public_key(randomness[32:64]) else {
    return Err(RandomnessLength(randomness.len()));
  };
  __ct_public(ct_t);
  let Ok(ss_t) = x25519.x25519(randomness[32:64], ek_t) else {
    return Err(LowOrder);
  };
  let ss = __combine(pq.shared_secret, ss_t, ct_t, ek_t);
  crypto.erase(ss_t);
  let enc = __bytes_into(__bytes_into(__alloc_u8(ENC_SIZE), 0, pq.ciphertext, 0, 1088), 1088, ct_t, 0, 32);
  return Ok(Encapsulation { shared_secret: ss, enc: enc });
}

pub function mlkem768x25519_decap(k: HybridKey, enc: [u8]): Result[u8[], HpkeError] {
  if (enc.len() != ENC_SIZE) {
    return Err(EncLength(enc.len()));
  }
  let ct_t = enc[1088:1120];
  let Ok(ss_pq) = mlkem768.decaps(k.decapsulation_key, enc[0:1088]) else {
    return Err(EncLength(enc.len()));
  };
  let Ok(ss_t) = x25519.x25519(k.scalar, ct_t) else {
    return Err(LowOrder);
  };
  let ss = __combine(ss_pq, ss_t, ct_t, k.public_key[1184:1216]);
  crypto.erase(ss_pq);
  crypto.erase(ss_t);
  return Ok(ss);
}
```

### `5 the key schedule`

The suite identifier is "HPKE", the KEM identifier, 0x0001 for HKDF-SHA256 and 0x0003 for ChaCha20Poly1305, each in two bytes, big-endian. `LabeledExtract` and `LabeledExpand` put "HPKE-v1" and the suite identifier before the label.

```fern
<<5 the key schedule>>=
pub const KDF_HKDF_SHA256: i32 = 1;

pub const AEAD_CHACHA20_POLY1305: i32 = 3;

function __cat(a: [u8], b: [u8]): u8[] {
  return __bytes_into(__bytes_into(__alloc_u8(a.len() + b.len()), 0, a, 0, a.len()), a.len(), b, 0, b.len());
}

function __suite_id(kem_id: i32): u8[] {
  let b = __bytes_into(__alloc_u8(10), 0, "HPKE".as_bytes(), 0, 4);
  return b.with(4, (kem_id >> 8 & 255) as u8).with(5, (kem_id & 255) as u8).with(7, KDF_HKDF_SHA256 as u8).with(9, AEAD_CHACHA20_POLY1305 as u8);
}

function __labeled_extract(suite: [u8], salt: [u8], label: string, ikm: [u8]): u8[] {
  let labeled = __cat(__cat(__cat("HPKE-v1".as_bytes(), suite), label.as_bytes()), ikm);
  let prk = crypto.hkdf_sha256_extract(salt, labeled);
  crypto.erase(labeled);
  return prk;
}

function __labeled_expand(suite: [u8], prk: [u8], label: string, info: [u8], length: i32): u8[] {
  let prefix = __alloc_u8(2).with(0, (length >> 8 & 255) as u8).with(1, (length & 255) as u8);
  let labeled = __cat(__cat(__cat(__cat(prefix, "HPKE-v1".as_bytes()), suite), label.as_bytes()), info);
  return crypto.hkdf_sha256_expand(prk, labeled, length);
}

// The base mode of RFC 9180: no PSK and no PSK identifier.
pub function key_schedule(kem_id: i32, shared_secret: [u8], info: [u8]): Context {
  let suite = __suite_id(kem_id);
  let psk_id_hash = __labeled_extract(suite, [], "psk_id_hash", []);
  let info_hash = __labeled_extract(suite, [], "info_hash", info);
  let context = __cat(__cat(__alloc_u8(1).with(0, 0 as u8), psk_id_hash), info_hash);
  let secret = __labeled_extract(suite, shared_secret, "secret", []);
  let key = __labeled_expand(suite, secret, "key", context, 32);
  let base_nonce = __labeled_expand(suite, secret, "base_nonce", context, 12);
  let exporter = __labeled_expand(suite, secret, "exp", context, 32);
  crypto.erase(secret);
  return Context { key: key, base_nonce: base_nonce, seq: 0 as u64, exporter_secret: exporter, suite: suite };
}
```

### `6 pub struct Context`

The nonce of a message is the base nonce with the sequence number, big-endian, in an exclusive OR on its last eight bytes. `seal` and `open` give the next `Context` with the result.

```fern
<<6 pub struct Context>>=
pub struct Context {
  key: u8[],
  base_nonce: u8[],
  seq: u64,
  exporter_secret: u8[],
  suite: u8[],
}

function __next(c: Context): Context {
  return Context { key: c.key, base_nonce: c.base_nonce, seq: c.seq + 1 as u64, exporter_secret: c.exporter_secret, suite: c.suite };
}

pub function (c: Context) seal(aad: [u8], plaintext: [u8]): (u8[], Context) {
  let n = plaintext.len();
  let buf = __bytes_into(__bytes_into(__alloc_u8(n + 16 + aad.len()), 0, plaintext, 0, n), n + 16, aad, 0, aad.len());
  let Ok(k) = chacha20poly1305.key(c.key) else {
    return ([], c);
  };
  let Ok(sealed) = k.seal_in_place(c.base_nonce, c.seq, buf, n + 16, aad.len(), 0, n) else {
    return ([], c);
  };
  return (__bytes_into(__alloc_u8(n + 16), 0, sealed, 0, n + 16), __next(c));
}

pub function (c: Context) open(aad: [u8], sealed: [u8]): Result[(u8[], Context), HpkeError] {
  if (sealed.len() < 16) {
    return Err(Open);
  }
  let n = sealed.len() - 16;
  let buf = __bytes_into(__alloc_u8(sealed.len() + aad.len()), 0, sealed, 0, sealed.len());
  buf = __bytes_into(buf, sealed.len(), aad, 0, aad.len());
  let Ok(k) = chacha20poly1305.key(c.key) else {
    return Err(Open);
  };
  let Ok(opened) = k.open_in_place(c.base_nonce, c.seq, buf, sealed.len(), aad.len(), 0, n) else {
    return Err(Open);
  };
  return Ok((__bytes_into(__alloc_u8(n), 0, opened, 0, n), __next(c)));
}

// The secret export interface of RFC 9180 section 5.3.
pub function (c: Context) export(exporter_context: [u8], length: i32): u8[] {
  return __labeled_expand(c.suite, c.exporter_secret, "sec", exporter_context, length);
}
```

### `7 the sender and the recipient`

`setup_sender_derand` takes the 64 bytes of randomness of `mlkem768x25519_encap_derand`. A caller fills them from a secure random source.

```fern
<<7 the sender and the recipient>>=
pub struct Sender {
  enc: u8[],
  context: Context,
}

pub function setup_sender_derand(public_key: [u8], info: [u8], randomness: [u8]): Result[Sender, HpkeError] {
  let e = mlkem768x25519_encap_derand(public_key, randomness)?;
  let context = key_schedule(KEM_MLKEM768_X25519, e.shared_secret, info);
  crypto.erase(e.shared_secret);
  return Ok(Sender { enc: e.enc, context: context });
}

pub function setup_recipient(k: HybridKey, enc: [u8], info: [u8]): Result[Context, HpkeError] {
  let ss = mlkem768x25519_decap(k, enc)?;
  let context = key_schedule(KEM_MLKEM768_X25519, ss, info);
  crypto.erase(ss);
  return Ok(context);
}
```
