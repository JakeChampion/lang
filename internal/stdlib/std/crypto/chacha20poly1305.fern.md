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

<<8 function __poly_new(k0: u32, k1: u32, k2: u32, k3: u32): Poly>>

<<9 function __poly_finish(p: Poly, s0: u32, s1: u32, s2: u32, s3: u32): u8[]>>

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
struct Poly { r0: u32, r1: u32, r2: u32, r3: u32, r4: u32, h0: u32, h1: u32, h2: u32, h3: u32, h4: u32 }
```

### `8 function __poly_new(k0: u32, k1: u32, k2: u32, k3: u32): Poly`

```fern
<<8 function __poly_new(k0: u32, k1: u32, k2: u32, k3: u32): Poly>>=
function __poly_new(k0: u32, k1: u32, k2: u32, k3: u32): Poly {
  return Poly {
    r0: k0 & 0x3ffffff,
    r1: (k0 >> 26 | k1 << 6) & 0x3ffff03,
    r2: (k1 >> 20 | k2 << 12) & 0x3ffc0ff,
    r3: (k2 >> 14 | k3 << 18) & 0x3f03fff,
    r4: k3 >> 8 & 0x00fffff,
    h0: 0, h1: 0, h2: 0, h3: 0, h4: 0,
  };
}

function __poly_blocks(p: Poly, b: [u8], from: i32, len: i32, pad_one: boolean): Poly {
  let r0 = p.r0 as u64;
  let r1 = p.r1 as u64;
  let r2 = p.r2 as u64;
  let r3 = p.r3 as u64;
  let r4 = p.r4 as u64;
  let s1 = r1 * 5;
  let s2 = r2 * 5;
  let s3 = r3 * 5;
  let s4 = r4 * 5;
  let h0 = p.h0;
  let h1 = p.h1;
  let h2 = p.h2;
  let h3 = p.h3;
  let h4 = p.h4;
  let tail = __alloc_u8(16);
  let at = 0;
  while (at < len) {
    let t0 = 0;
    let t1 = 0;
    let t2 = 0;
    let t3 = 0;
    let hibit = 1 << 24;
    if (len - at >= 16) {
      t0 = __le32(b, from + at);
      t1 = __le32(b, from + at + 4);
      t2 = __le32(b, from + at + 8);
      t3 = __le32(b, from + at + 12);
    } else {
      let i = 0;
      while (i < 16) {
        if (at + i < len) {
          tail = tail.with(i, b[from + at + i]);
        } else {
          tail = tail.with(i, 0 as u8);
        }
        i += 1;
      }
      if (pad_one) {
        tail = tail.with(len - at, 1 as u8);
        hibit = 0;
      }
      t0 = __le32(tail, 0);
      t1 = __le32(tail, 4);
      t2 = __le32(tail, 8);
      t3 = __le32(tail, 12);
    }
    h0 += t0 & 0x3ffffff;
    h1 += (t0 >> 26 | t1 << 6) & 0x3ffffff;
    h2 += (t1 >> 20 | t2 << 12) & 0x3ffffff;
    h3 += (t2 >> 14 | t3 << 18) & 0x3ffffff;
    h4 += t3 >> 8 | hibit;
    let d0 = h0 as u64 * r0 + h1 as u64 * s4 + h2 as u64 * s3 + h3 as u64 * s2 + h4 as u64 * s1;
    let d1 = h0 as u64 * r1 + h1 as u64 * r0 + h2 as u64 * s4 + h3 as u64 * s3 + h4 as u64 * s2;
    let d2 = h0 as u64 * r2 + h1 as u64 * r1 + h2 as u64 * r0 + h3 as u64 * s4 + h4 as u64 * s3;
    let d3 = h0 as u64 * r3 + h1 as u64 * r2 + h2 as u64 * r1 + h3 as u64 * r0 + h4 as u64 * s4;
    let d4 = h0 as u64 * r4 + h1 as u64 * r3 + h2 as u64 * r2 + h3 as u64 * r1 + h4 as u64 * r0;
    let c = d0 >> 26;
    h0 = (d0 & 0x3ffffff) as u32;
    d1 += c;
    c = d1 >> 26;
    h1 = (d1 & 0x3ffffff) as u32;
    d2 += c;
    c = d2 >> 26;
    h2 = (d2 & 0x3ffffff) as u32;
    d3 += c;
    c = d3 >> 26;
    h3 = (d3 & 0x3ffffff) as u32;
    d4 += c;
    c = d4 >> 26;
    h4 = (d4 & 0x3ffffff) as u32;
    h0 += c as u32 * 5;
    let c2 = h0 >> 26;
    h0 &= 0x3ffffff;
    h1 += c2;
    at += 16;
  }
  return Poly { ...p, h0: h0, h1: h1, h2: h2, h3: h3, h4: h4 };
}
```

### `9 function __poly_finish(p: Poly, s0: u32, s1: u32, s2: u32, s3: u32): u8[]`

```fern
<<9 function __poly_finish(p: Poly, s0: u32, s1: u32, s2: u32, s3: u32): u8[]>>=
function __poly_finish(p: Poly, s0: u32, s1: u32, s2: u32, s3: u32): u8[] {
  let h0 = p.h0;
  let h1 = p.h1;
  let h2 = p.h2;
  let h3 = p.h3;
  let h4 = p.h4;
  let c = h1 >> 26;
  h1 &= 0x3ffffff;
  h2 += c;
  c = h2 >> 26;
  h2 &= 0x3ffffff;
  h3 += c;
  c = h3 >> 26;
  h3 &= 0x3ffffff;
  h4 += c;
  c = h4 >> 26;
  h4 &= 0x3ffffff;
  h0 += c * 5;
  c = h0 >> 26;
  h0 &= 0x3ffffff;
  h1 += c;
  let g0 = h0 + 5;
  c = g0 >> 26;
  g0 &= 0x3ffffff;
  let g1 = h1 + c;
  c = g1 >> 26;
  g1 &= 0x3ffffff;
  let g2 = h2 + c;
  c = g2 >> 26;
  g2 &= 0x3ffffff;
  let g3 = h3 + c;
  c = g3 >> 26;
  g3 &= 0x3ffffff;
  let g4 = h4 + c - (1 << 26);
  let keep_g = (g4 >> 31) - 1;
  let keep_h = keep_g ^ 0xffffffff;
  h0 = h0 & keep_h | g0 & keep_g;
  h1 = h1 & keep_h | g1 & keep_g;
  h2 = h2 & keep_h | g2 & keep_g;
  h3 = h3 & keep_h | g3 & keep_g;
  h4 = h4 & keep_h | g4 & keep_g;
  let w0 = h0 | h1 << 26;
  let w1 = h1 >> 6 | h2 << 20;
  let w2 = h2 >> 12 | h3 << 14;
  let w3 = h3 >> 18 | h4 << 8;
  let f = w0 as u64 + s0 as u64;
  w0 = f as u32;
  f = w1 as u64 + s1 as u64 + (f >> 32);
  w1 = f as u32;
  f = w2 as u64 + s2 as u64 + (f >> 32);
  w2 = f as u32;
  f = w3 as u64 + s3 as u64 + (f >> 32);
  w3 = f as u32;
  let tag = __alloc_u8(16);
  tag = __put_le32(tag, 0, w0);
  tag = __put_le32(tag, 4, w1);
  tag = __put_le32(tag, 8, w2);
  return __put_le32(tag, 12, w3);
}
```

### `10 pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError]`

```fern
<<10 pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError]>>=
pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError] {
  if (key.len() != 32) {
    return Err(aead.KeyLength(key.len()));
  }
  let p = __poly_new(__le32(key, 0), __le32(key, 4), __le32(key, 8), __le32(key, 12));
  p = __poly_blocks(p, msg, 0, msg.len(), true);
  return Ok(__poly_finish(p, __le32(key, 16), __le32(key, 20), __le32(key, 24), __le32(key, 28)));
}
```

### `11 function __aead_tag(k: Key, ctr: u8[], aad: [u8], ct: [u8], from: i32, len: i32): u8[]`

```fern
<<11 function __aead_tag(k: Key, ctr: u8[], aad: [u8], ct: [u8], from: i32, len: i32): u8[]>>=
// ctr holds block counter 0, whose key stream starts with the one-time Poly1305 key.
function __aead_tag(k: Key, ctr: u8[], aad: [u8], ct: [u8], from: i32, len: i32): u8[] {
  let otk = __chacha20_xor_into(k.k, ctr, __alloc_u8(32), 0, 32);
  let p = __poly_new(__le32(otk, 0), __le32(otk, 4), __le32(otk, 8), __le32(otk, 12));
  p = __poly_blocks(p, aad, 0, aad.len(), false);
  p = __poly_blocks(p, ct, from, len, false);
  let lens = __alloc_u8(16);
  lens = __put_le32(lens, 0, aad.len() as u32);
  lens = __put_le32(lens, 4, 0);
  lens = __put_le32(lens, 8, len as u32);
  lens = __put_le32(lens, 12, 0);
  p = __poly_blocks(p, lens, 0, 16, false);
  let s0 = __le32(otk, 16);
  let s1 = __le32(otk, 20);
  let s2 = __le32(otk, 24);
  let s3 = __le32(otk, 28);
  crypto.erase(otk);
  return __poly_finish(p, s0, s1, s2, s3);
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
