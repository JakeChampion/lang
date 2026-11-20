# ChaCha20-Poly1305 authenticated encryption

This module provides ChaCha20-Poly1305 authenticated encryption.

Raw `chacha20_xor` leaves ciphertext changes undetected. Raw `poly1305` requires a fresh one-time key for each message. For authenticated encryption, use `Key.seal` and `Key.open` with a unique nonce for each message sealed under a key. For a standalone MAC with a reusable key, use `crypto.hmac_sha256`.

## Model

A key is built once from its 32 bytes and holds a copy of them. Sealing and opening a message pair that key with the message's nonce. ChaCha20 generates the byte stream for encryption, and Poly1305 authenticates the additional data and ciphertext. The key also exposes the raw ChaCha20 stream, and `poly1305` takes its own one-time key.

ChaCha20 runs in the compiler's kernel `__chacha20_xor_into`, which XORs the key stream over a range of a byte array in place. The kernel does eight blocks at a time on x86-64 (AVX2), four on arm64 (NEON) and one on wasm. It takes the key and 16 bytes that hold the block counter and the nonce.

Poly1305 runs in the kernel `__poly1305_blocks`, which absorbs a range of a byte array into a 40-byte state: h and then r. On x86-64 the kernel does four blocks at a time from 256 bytes (AVX2, in limbs of 26 bits), and one block at a time in 64-bit limbs below that. The kernel leaves h fully reduced, so the tag is h plus s.

Seal and open run in the kernels `__chacha20_poly1305_seal_into` and `__chacha20_poly1305_open_into`. Each kernel does the whole AEAD over a range of a byte array in one pass. On x86-64 and arm64, Poly1305 runs in the general registers among the vector rounds of ChaCha20, so the two run at the same time. On wasm, the kernel calls the two kernels above in turn.

## Contracts

Building a key checks its length, and each message checks its nonce length. Open returns an error when authentication fails. For the RFC 8439 methods, the caller supplies a unique 12-byte nonce for each message. For data at rest, `seal_random` draws a 24-byte nonce from the operating system and prefixes it to the XChaCha20-Poly1305 ciphertext and tag. Pass this format and the same additional data to `open_prefixed`. With independent random 192-bit nonces, limit a key to 2^80 messages for a nonce collision probability below 2^-32 (draft-irtf-cfrg-xchacha-03 §2.1). The explicit-nonce methods `seal_xchacha` and `open_xchacha` use the same construction with the nonce stored separately. Each explicit nonce must be unique under its key.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct Key>>

<<4 function __le32(b: [u8], at: i32): u32>>

<<5 function __counter_block(iv: [u8], seq: u64, counter: u32): Result[u8[], aead.AeadError]>>

<<6 pub function (k: Key) chacha20_xor(nonce: [u8], counter: u32, data: [u8]): Result[u8[], aead.AeadError]>>

<<7 function __poly_new(key: [u8]): u8[]>>

<<8 function __poly_finish(own st: u8[], s: [u8]): u8[]>>

<<9 pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError]>>

<<10 pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]>>

<<XChaCha20-Poly1305>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides ChaCha20-Poly1305 authenticated encryption.
// Raw chacha20_xor does not authenticate ciphertext. Raw poly1305 requires a
// fresh one-time key for every message. Use seal and open for authenticated
// encryption, with a unique nonce for each message sealed under a key.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/crypto";
import "std/rand";
import "std/array";
import "std/crypto/aead";
```

### `3 pub struct Key`

```fern
<<3 pub struct Key>>=
pub struct Key { k: u8[] }

// Consumes the key and zeroes its key bytes when it owns the last reference.
pub function (own k: Key) erase(): void {
  crypto.erase(k.k);
}
```

### `4 function __le32(b: [u8], at: i32): u32`

```fern
<<4 function __le32(b: [u8], at: i32): u32>>=
function __le32(b: [u8], at: i32): u32 {
  return b[at] as u32 | b[at + 1] as u32 << 8 | b[at + 2] as u32 << 16 | b[at + 3] as u32 << 24;
}

function __put_le32(own d: u8[], at: i32, v: u32): u8[] {
  return d.with(at, (v & 255) as u8).with(at + 1, (v >> 8 & 255) as u8).with(at + 2, (v >> 16 & 255) as u8).with(at + 3, (v >> 24 & 255) as u8);
}

pub function key(bytes: [u8]): Result[Key, aead.AeadError] {
  if (bytes.len() != 32) {
    return Err(aead.KeyLength(bytes.len()));
  }
  return Ok(Key { k: __bytes_owned(bytes) });
}
```

### `5 function __counter_block(iv: [u8], seq: u64, counter: u32): Result[u8[], aead.AeadError]`

```fern
<<5 function __counter_block(iv: [u8], seq: u64, counter: u32): Result[u8[], aead.AeadError]>>=
// The block counter, little-endian, and then the nonce: iv with seq folded into its last eight bytes, as
// TLS 1.3 and QUIC build a record's.
function __counter_block(iv: [u8], seq: u64, counter: u32): Result[u8[], aead.AeadError] {
  if (iv.len() != 12) {
    return Err(aead.NonceLength(iv.len()));
  }
  let b = __put_le32(__bytes_into(__alloc_u8(16), 4, iv, 0, 12), 0, counter);
  for i in 0..8 {
    let shift = (56 - 8 * i) as u64;
    b = b.with(8 + i, b[8 + i] ^ (seq >> shift & 255 as u64) as u8);
  }
  return Ok(b);
}
```

### `6 pub function (k: Key) chacha20_xor(nonce: [u8], counter: u32, data: [u8]): Result[u8[], aead.AeadError]`

```fern
<<6 pub function (k: Key) chacha20_xor(nonce: [u8], counter: u32, data: [u8]): Result[u8[], aead.AeadError]>>=
// Applies the raw stream without authentication. Use seal and open to detect ciphertext changes.
pub function (k: Key) chacha20_xor(nonce: [u8], counter: u32, data: [u8]): Result[u8[], aead.AeadError] {
  let ctr = __counter_block(nonce, 0 as u64, counter)?;
  let blocks = (data.len() as i64 + 63) / 64;
  if (counter as i64 + blocks > 4294967296) {
    return Err(aead.CounterRange);
  }
  return Ok(__chacha20_xor_into(k.k, ctr, __bytes_owned(data), 0, data.len()));
}
```

### `7 function __poly_new(key: [u8]): u8[]`

```fern
<<7 function __poly_new(key: [u8]): u8[]>>=
function __le64(b: [u8], at: i32): u64 {
  return b[at] as u64 | b[at + 1] as u64 << 8 | b[at + 2] as u64 << 16 | b[at + 3] as u64 << 24 | b[at + 4] as u64 << 32 | b[at + 5] as u64 << 40
    | b[at + 6] as u64 << 48 | b[at + 7] as u64 << 56;
}

function __put_le64(own d: u8[], at: i32, v: u64): u8[] {
  return __put_le32(__put_le32(d, at, (v & 0xffffffff as u64) as u32), at + 4, (v >> 32) as u32);
}

// The carry out of the 64-bit sum s = a + b, with no branch.
function __carry(a: u64, b: u64, s: u64): u64 {
  return (a & b | (a | b) & ~s) >> 63;
}

// The state of the kernel __poly1305_blocks: h (24 bytes, zero) and then r, the first 16 bytes of the key. The
// kernel clamps r as it reads it.
function __poly_new(key: [u8]): u8[] {
  return __bytes_into(__alloc_u8(40), 24, key, 0, 16);
}
```

### `8 function __poly_finish(own st: u8[], s: [u8]): u8[]`

```fern
<<8 function __poly_finish(own st: u8[], s: [u8]): u8[]>>=
// The tag: h, which the kernel leaves below p, plus s, mod 2^128. The state is erased.
function __poly_finish(own st: u8[], s: [u8]): u8[] {
  let h0 = __le64(st, 0);
  let s0 = __le64(s, 0);
  let t0 = h0 + s0;
  let t1 = __le64(st, 8) + __le64(s, 8) + __carry(h0, s0, t0);
  crypto.erase(st);
  return __put_le64(__put_le64(__alloc_u8(16), 0, t0), 8, t1);
}
```

### `9 pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError]`

```fern
<<9 pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError]>>=
// Computes a MAC with a one-time key. Reusing the key breaks authentication. Use crypto.hmac_sha256 for a reusable MAC key.
pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError] {
  if (key.len() != 32) {
    return Err(aead.KeyLength(key.len()));
  }
  let st = __poly1305_blocks(__poly_new(key), msg, 0, msg.len());
  return Ok(__poly_finish(st, key[16:32]));
}
```

### `10 pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]`

```fern
<<10 pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]>>=
pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError] {
  unsafe {
    let n = plaintext.len();
    let a = aad.len();
    let buf = __bytes_into(__bytes_into(__alloc_u8(n + 16 + a), 0, plaintext, 0, n), n + 16, aad, 0, a);
    buf = k.seal_in_place(nonce, 0 as u64, buf, n + 16, a, 0, n)?;
    __arr_set_len(buf, n + 16);
    return Ok(buf);
  }
}

pub function (k: Key) open(nonce: [u8], sealed: [u8], aad: [u8]): Result[u8[], aead.AeadError] {
  unsafe {
    if (sealed.len() < 16) {
      __counter_block(nonce, 0 as u64, 0)?;
      return Err(aead.Forged);
    }
    let n = sealed.len() - 16;
    let a = aad.len();
    let buf = __bytes_into(__bytes_into(__alloc_u8(n + 16 + a), 0, sealed, 0, n + 16), n + 16, aad, 0, a);
    buf = k.open_in_place(nonce, 0 as u64, buf, n + 16, a, 0, n)?;
    __arr_set_len(buf, n);
    return Ok(buf);
  }
}

// Seals buf[at:at + n] in place under the nonce iv ^ seq, authenticating buf[aad_at:aad_at + aad_n], with the
// tag at buf[at + n:at + n + 16]: the in-place form std/crypto/aes_gcm's Key.seal_in_place has. A region outside
// the buffer aborts, as an out-of-range slice does.
pub function (k: Key) seal_in_place(iv: [u8], seq: u64, own buf: u8[], aad_at: i32, aad_n: i32, at: i32, n: i32): Result[u8[], aead.AeadError] {
  let ctr = __counter_block(iv, seq, 0)?;
  if (aead.overlaps(aad_at, aad_n, at, n)) {
    return Err(aead.Overlap);
  }
  return Ok(__chacha20_poly1305_seal_into(k.k, ctr, buf, aad_at, aad_n, at, n));
}

// Opens buf[at:at + n] in place, the tag at buf[at + n:at + n + 16]. The kernel decrypts and checks the tag in
// one pass. If the tag does not verify, the function erases the buffer and returns Forged.
pub function (k: Key) open_in_place(iv: [u8], seq: u64, own buf: u8[], aad_at: i32, aad_n: i32, at: i32, n: i32): Result[u8[], aead.AeadError] {
  let ctr = __counter_block(iv, seq, 0)?;
  if (aead.overlaps(aad_at, aad_n, at, n)) {
    return Err(aead.Overlap);
  }
  buf = __chacha20_poly1305_open_into(k.k, ctr, buf, aad_at, aad_n, at, n);
  __ct_public(buf[at + n:at + n + 1]);
  if (buf[at + n] != 0) {
    crypto.erase(buf);
    return Err(aead.Forged);
  }
  return Ok(buf);
}
```

### XChaCha20-Poly1305

HChaCha20 uses the same 20 rounds as the ChaCha20 block function. To reuse the kernel, subtract its feed-forward additions from the first and last rows. Those rows start with the public ChaCha constant and the first 16 nonce bytes. The remaining eight nonce bytes follow four zero bytes in the IETF ChaCha20-Poly1305 nonce.

```fern
<<XChaCha20-Poly1305>>=
function __hchacha20(k: Key, input: u8[]): Key {
  let block = __chacha20_xor_into(k.k, input, __alloc_u8(64), 0, 64);
  let constant = "expand 32-byte k".as_bytes();
  let subkey = __alloc_u8(32);
  for i in 0..4 {
    let at = i * 4;
    subkey = __put_le32(subkey, at, __le32(block, at) - __le32(constant, at));
    subkey = __put_le32(subkey, 16 + at, __le32(block, 48 + at) - __le32(input, at));
  }
  crypto.erase(block);
  return Key { k: subkey };
}

function __xparameters(k: Key, nonce: [u8]): Result[(Key, u8[]), aead.AeadError] {
  if (nonce.len() != 24) {
    return Err(aead.ExtendedNonceLength(nonce.len()));
  }
  return Ok((__hchacha20(k, __bytes_owned(nonce[0:16])), __bytes_into(__alloc_u8(12), 4, nonce, 16, 8)));
}

pub function (k: Key) seal_xchacha(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError] {
  let (subkey, iv) = __xparameters(k, nonce)?;
  let sealed = subkey.seal(iv, plaintext, aad);
  crypto.erase(subkey.k);
  return sealed;
}

pub function (k: Key) open_xchacha(nonce: [u8], sealed: [u8], aad: [u8]): Result[u8[], aead.AeadError] {
  let (subkey, iv) = __xparameters(k, nonce)?;
  let opened = subkey.open(iv, sealed, aad);
  crypto.erase(subkey.k);
  return opened;
}

pub function (k: Key) seal_random(plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError] {
  let nonce = rand.bytes(24);
  let sealed = k.seal_xchacha(nonce, plaintext, aad)?;
  return Ok(nonce.concat(sealed));
}

pub function (k: Key) open_prefixed(message: [u8], aad: [u8]): Result[u8[], aead.AeadError] {
  if (message.len() < 24 + 16) {
    return Err(aead.Forged);
  }
  return k.open_xchacha(message[0:24], message[24:], aad);
}
```

The HChaCha20 subkey matches draft-irtf-cfrg-xchacha-03 §2.2.1.

```fern test name=hchacha20-subkey
<<*>>
import "std/hex";

function main(): i32 {
  let bytes = __alloc_u8(32);
  for i in 0..32 { bytes = bytes.with(i, i as u8); }
  let Ok(k) = key(bytes) else { return 1; };
  let Ok(input) = hex.decode("000000090000004a0000000031415927") else { return 2; };
  let got = __hchacha20(k, input);
  if (hex.encode(got.k) != "82413b4227b27bfed30e42508a877d73a0f9e4d58a74a853c12ec41326d3ecdc") { return 3; }
  return 0;
}
```
