# ChaCha20-Poly1305 authenticated encryption

This module provides ChaCha20-Poly1305 authenticated encryption.

## Model

A key is built once from its 32 bytes and holds a copy of them. Sealing and opening a message pair that key with the message's nonce. ChaCha20 generates the byte stream for encryption, and Poly1305 authenticates the additional data and ciphertext. The key also exposes the raw ChaCha20 stream, and `poly1305` takes its own one-time key.

ChaCha20 runs in the compiler's kernel `__chacha20_xor_into`, which XORs the key stream over a range of a byte array in place. The kernel does eight blocks at a time on x86-64 (AVX2), four on arm64 (NEON) and one on wasm. It takes the key and 16 bytes that hold the block counter and the nonce.

## Contracts

Building a key checks its length, and each message checks its nonce length. Open returns an error when authentication fails. The caller supplies the nonce for each message.

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

<<7 struct Poly>>

<<8 function __poly_new(key: [u8]): Poly>>

<<9 function __poly_finish(p: Poly, s0: u64, s1: u64): u8[]>>

<<10 pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError]>>

<<11 function __aead_tag(k: Key, ctr: u8[], aad: [u8], ct: [u8], from: i32, len: i32): u8[]>>

<<12 pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides ChaCha20-Poly1305 authenticated encryption.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/crypto";
import "std/crypto/aead";
```

### `3 pub struct Key`

```fern
<<3 pub struct Key>>=
pub struct Key { k: u8[] }
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
pub function (k: Key) chacha20_xor(nonce: [u8], counter: u32, data: [u8]): Result[u8[], aead.AeadError] {
  let ctr = __counter_block(nonce, 0 as u64, counter)?;
  let blocks = (data.len() as i64 + 63) / 64;
  if (counter as i64 + blocks > 4294967296) {
    return Err(aead.CounterRange);
  }
  return Ok(__chacha20_xor_into(k.k, ctr, __bytes_owned(data), 0, data.len()));
}
```

### `7 struct Poly`

```fern
<<7 struct Poly>>=
// h is the accumulator in 64-bit limbs: h0 + h1 * 2^64 + h2 * 2^128.
struct Poly { r0: u64, r1: u64, h0: u64, h1: u64, h2: u64 }

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
```

### `8 function __poly_new(key: [u8]): Poly`

```fern
<<8 function __poly_new(key: [u8]): Poly>>=
function __poly_new(key: [u8]): Poly {
  return Poly { r0: __le64(key, 0) & 0x0ffffffc0fffffff as u64, r1: __le64(key, 8) & 0x0ffffffc0ffffffc as u64, h0: 0, h1: 0, h2: 0 };
}

// Absorbs b[from:from + len], 16 bytes at a time. A short last block is padded with zeros. With pad_one, it
// also gets a 1 byte after the message and no 2^128 bit, as RFC 8439 section 2.5 pads a message. The
// product with r is not fully reduced. After each block h2 is 4 or less, so h is less than 2p.
function __poly_blocks(p: Poly, b: [u8], from: i32, len: i32, pad_one: boolean): Poly {
  let r0 = p.r0;
  let r1 = p.r1;
  let s1 = r1 + (r1 >> 2);
  let h0 = p.h0;
  let h1 = p.h1;
  let h2 = p.h2;
  let tail = __alloc_u8(16);
  let at = 0;
  while (at < len) {
    let m0: u64 = 0;
    let m1: u64 = 0;
    let hibit: u64 = 1;
    if (len - at >= 16) {
      m0 = __le64(b, from + at);
      m1 = __le64(b, from + at + 8);
    } else {
      for i in 0..16 {
        if (at + i < len) {
          tail = tail.with(i, b[from + at + i]);
        } else {
          tail = tail.with(i, 0 as u8);
        }
      }
      if (pad_one) {
        tail = tail.with(len - at, 1 as u8);
        hibit = 0;
      }
      m0 = __le64(tail, 0);
      m1 = __le64(tail, 8);
    }
    let t0 = h0 + m0;
    let c0 = __carry(h0, m0, t0);
    let t1 = h1 + m1;
    let c1 = __carry(h1, m1, t1);
    h1 = t1 + c0;
    h2 += hibit + c1 + __carry(t1, c0, h1);
    h0 = t0;
    let a = h0 * r0;
    let b1 = h1 * s1;
    let d0 = a + b1;
    let d0_hi = __mulhi_u64(h0, r0) + __mulhi_u64(h1, s1) + __carry(a, b1, d0);
    let e = h0 * r1;
    let f = h1 * r0;
    let d1 = e + f;
    let d1_hi = __mulhi_u64(h0, r1) + __mulhi_u64(h1, r0) + __carry(e, f, d1);
    let g = h2 * s1;
    let d1g = d1 + g;
    d1_hi += __carry(d1, g, d1g);
    h1 = d1g + d0_hi;
    h2 = h2 * r0 + d1_hi + __carry(d1g, d0_hi, h1);
    let c = (h2 >> 2) * 5;
    h2 &= 3;
    h0 = d0 + c;
    let k0 = __carry(d0, c, h0);
    let n1 = h1 + k0;
    h2 += __carry(h1, k0, n1);
    h1 = n1;
    at += 16;
  }
  return Poly { ...p, h0: h0, h1: h1, h2: h2 };
}
```

### `9 function __poly_finish(p: Poly, s0: u64, s1: u64): u8[]`

```fern
<<9 function __poly_finish(p: Poly, s0: u64, s1: u64): u8[]>>=
// h is below 2p, so h - p is h + 5 less 2^130 exactly when that sum reaches bit 130.
function __poly_finish(p: Poly, s0: u64, s1: u64): u8[] {
  let g0 = p.h0 + 5;
  let c0 = __carry(p.h0, 5, g0);
  let g1 = p.h1 + c0;
  let c1 = __carry(p.h1, c0, g1);
  let take_g = 0 - (p.h2 + c1 >> 2);
  let h0 = p.h0 & ~take_g | g0 & take_g;
  let h1 = p.h1 & ~take_g | g1 & take_g;
  let t0 = h0 + s0;
  let t1 = h1 + s1 + __carry(h0, s0, t0);
  return __put_le64(__put_le64(__alloc_u8(16), 0, t0), 8, t1);
}
```

### `10 pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError]`

```fern
<<10 pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError]>>=
pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError] {
  if (key.len() != 32) {
    return Err(aead.KeyLength(key.len()));
  }
  let p = __poly_blocks(__poly_new(key), msg, 0, msg.len(), true);
  return Ok(__poly_finish(p, __le64(key, 16), __le64(key, 24)));
}
```

### `11 function __aead_tag(k: Key, ctr: u8[], aad: [u8], ct: [u8], from: i32, len: i32): u8[]`

```fern
<<11 function __aead_tag(k: Key, ctr: u8[], aad: [u8], ct: [u8], from: i32, len: i32): u8[]>>=
// ctr holds block counter 0, whose key stream starts with the one-time Poly1305 key.
function __aead_tag(k: Key, ctr: u8[], aad: [u8], ct: [u8], from: i32, len: i32): u8[] {
  let otk = __chacha20_xor_into(k.k, ctr, __alloc_u8(32), 0, 32);
  let p = __poly_blocks(__poly_new(otk), aad, 0, aad.len(), false);
  p = __poly_blocks(p, ct, from, len, false);
  let lens = __put_le64(__put_le64(__alloc_u8(16), 0, aad.len() as u64), 8, len as u64);
  p = __poly_blocks(p, lens, 0, 16, false);
  let s0 = __le64(otk, 16);
  let s1 = __le64(otk, 24);
  crypto.erase(otk);
  return __poly_finish(p, s0, s1);
}
```

### `12 pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]`

```fern
<<12 pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]>>=
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
// tag at buf[at + n:at + n + 16]: the in-place form std/crypto/aes_gcm's Key.seal_in_place has.
pub function (k: Key) seal_in_place(iv: [u8], seq: u64, own buf: u8[], aad_at: i32, aad_n: i32, at: i32, n: i32): Result[u8[], aead.AeadError] {
  let ctr = __counter_block(iv, seq, 0)?;
  if (aead.overlaps(aad_at, aad_n, at, n)) {
    return Err(aead.Overlap);
  }
  let text = buf[at:at + n].len();
  buf = __chacha20_xor_into(k.k, ctr.with(0, 1 as u8), buf, at, text);
  let tag = __aead_tag(k, ctr, buf[aad_at:aad_at + aad_n], buf, at, text);
  return Ok(__bytes_into(buf, at + text, tag, 0, 16));
}

// Opens buf[at:at + n] in place, the tag at buf[at + n:at + n + 16]; a tag that does not verify returns Forged
// before anything is decrypted.
pub function (k: Key) open_in_place(iv: [u8], seq: u64, own buf: u8[], aad_at: i32, aad_n: i32, at: i32, n: i32): Result[u8[], aead.AeadError] {
  let ctr = __counter_block(iv, seq, 0)?;
  if (aead.overlaps(aad_at, aad_n, at, n)) {
    return Err(aead.Overlap);
  }
  let text = buf[at:at + n].len();
  let want = __aead_tag(k, ctr, buf[aad_at:aad_at + aad_n], buf, at, text);
  let diff = 0;
  for i in 0..16 {
    diff |= want[i] ^ buf[at + text + i];
  }
  want = want.with(0, diff);
  __ct_public(want[0:1]);
  if (want[0] != 0) {
    return Err(aead.Forged);
  }
  return Ok(__chacha20_xor_into(k.k, ctr.with(0, 1 as u8), buf, at, text));
}
```
