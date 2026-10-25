# ChaCha20-Poly1305 authenticated encryption

This module provides ChaCha20-Poly1305 authenticated encryption.

## Model

A key is built once from its 32 bytes and holds them as the eight little-endian words ChaCha20 starts from. Sealing and opening a message pair that key with the message's nonce. ChaCha20 generates the byte stream for encryption, and Poly1305 authenticates the additional data and ciphertext. The key also exposes the raw ChaCha20 stream, and `poly1305` takes its own one-time key.

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

<<5 function __xor_block(own out: u8[], at: i32, len: i32, k0: u32, k1: u32, k2: u32, k3: u32, k4: u32, k5: u...>>

<<6 function __xor_into(k: Stream, counter: u32, own out: u8[], from: i32, len: i32): u8[]>>

<<7 pub function (k: Key) chacha20_xor(nonce: [u8], counter: u32, data: [u8]): Result[u8[], aead.AeadError]>>

<<8 struct Poly>>

<<9 function __poly_new(k0: u32, k1: u32, k2: u32, k3: u32): Poly>>

<<10 function __poly_finish(p: Poly, s0: u32, s1: u32, s2: u32, s3: u32): u8[]>>

<<11 pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError]>>

<<12 function __aead_tag(k: Stream, aad: [u8], ct: [u8], from: i32, len: i32): u8[]>>

<<13 pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides ChaCha20-Poly1305 authenticated encryption.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/crypto/aead";
```

### `3 pub struct Key`

```fern
<<3 pub struct Key>>=
pub struct Key { k0: u32, k1: u32, k2: u32, k3: u32, k4: u32, k5: u32, k6: u32, k7: u32 }

struct Stream { k0: u32, k1: u32, k2: u32, k3: u32, k4: u32, k5: u32, k6: u32, k7: u32, n0: u32, n1: u32, n2: u32 }
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

function __xor_word(own out: u8[], at: i32, w: u32): u8[] {
  return out.with(at, out[at] ^ (w & 255) as u8).with(at + 1, out[at + 1] ^ (w >> 8 & 255) as u8).with(at + 2, out[at + 2] ^ (w >> 16 & 255) as u8).with(at + 3, out[at + 3] ^ (w >> 24 & 255) as u8);
}

pub function key(bytes: [u8]): Result[Key, aead.AeadError] {
  if (bytes.len() != 32) {
    return Err(aead.KeyLength(bytes.len()));
  }
  return Ok(Key { k0: __le32(bytes, 0), k1: __le32(bytes, 4), k2: __le32(bytes, 8), k3: __le32(bytes, 12), k4: __le32(bytes, 16), k5: __le32(bytes, 20), k6: __le32(bytes, 24), k7: __le32(bytes, 28) });
}

function __stream(k: Key, nonce: [u8]): Result[Stream, aead.AeadError] {
  if (nonce.len() != 12) {
    return Err(aead.NonceLength(nonce.len()));
  }
  return __stream_seq(k, nonce, 0 as u64);
}

// The nonce is iv with seq folded into its last eight bytes, as TLS 1.3 and QUIC build a record's.
function __stream_seq(k: Key, iv: [u8], seq: u64): Result[Stream, aead.AeadError] {
  if (iv.len() != 12) {
    return Err(aead.NonceLength(iv.len()));
  }
  return Ok(Stream { k0: k.k0, k1: k.k1, k2: k.k2, k3: k.k3, k4: k.k4, k5: k.k5, k6: k.k6, k7: k.k7, n0: __le32(iv, 0), n1: __le32(iv, 4) ^ __be_word(seq >> 32 as u64), n2: __le32(iv, 8) ^ __be_word(seq) });
}

// The low 32 bits of v as big-endian bytes read little-endian.
function __be_word(v: u64): u32 {
  let w: u32 = (v & 0xffffffff as u64) as u32;
  return (w >> 24) | (w >> 8 & 0xff00) | ((w & 0xff00) << 8) | (w << 24);
}
```

### `5 function __xor_block(own out: u8[], at: i32, len: i32, k0: u32, k1: u32, k2: u32, k3: u32, k4: u32, k5: u...`

```fern
<<5 function __xor_block(own out: u8[], at: i32, len: i32, k0: u32, k1: u32, k2: u32, k3: u32, k4: u32, k5: u...>>=
function __xor_block(own out: u8[], at: i32, len: i32, k0: u32, k1: u32, k2: u32, k3: u32, k4: u32, k5: u32, k6: u32, k7: u32, counter: u32, n0: u32, n1: u32, n2: u32): u8[] {
  let x0: u32 = 0x61707865;
  let x1: u32 = 0x3320646e;
  let x2: u32 = 0x79622d32;
  let x3: u32 = 0x6b206574;
  let x4: u32 = k0;
  let x5: u32 = k1;
  let x6: u32 = k2;
  let x7: u32 = k3;
  let x8: u32 = k4;
  let x9: u32 = k5;
  let x10: u32 = k6;
  let x11: u32 = k7;
  let x12: u32 = counter;
  let x13: u32 = n0;
  let x14: u32 = n1;
  let x15: u32 = n2;
  let r: i32 = 0;
  while (r < 10) {
    x0 = x0 + x4;
    x12 = x12 ^ x0;
    x12 = x12 << 16 | x12 >> 16;
    x8 = x8 + x12;
    x4 = x4 ^ x8;
    x4 = x4 << 12 | x4 >> 20;
    x0 = x0 + x4;
    x12 = x12 ^ x0;
    x12 = x12 << 8 | x12 >> 24;
    x8 = x8 + x12;
    x4 = x4 ^ x8;
    x4 = x4 << 7 | x4 >> 25;
    x1 = x1 + x5;
    x13 = x13 ^ x1;
    x13 = x13 << 16 | x13 >> 16;
    x9 = x9 + x13;
    x5 = x5 ^ x9;
    x5 = x5 << 12 | x5 >> 20;
    x1 = x1 + x5;
    x13 = x13 ^ x1;
    x13 = x13 << 8 | x13 >> 24;
    x9 = x9 + x13;
    x5 = x5 ^ x9;
    x5 = x5 << 7 | x5 >> 25;
    x2 = x2 + x6;
    x14 = x14 ^ x2;
    x14 = x14 << 16 | x14 >> 16;
    x10 = x10 + x14;
    x6 = x6 ^ x10;
    x6 = x6 << 12 | x6 >> 20;
    x2 = x2 + x6;
    x14 = x14 ^ x2;
    x14 = x14 << 8 | x14 >> 24;
    x10 = x10 + x14;
    x6 = x6 ^ x10;
    x6 = x6 << 7 | x6 >> 25;
    x3 = x3 + x7;
    x15 = x15 ^ x3;
    x15 = x15 << 16 | x15 >> 16;
    x11 = x11 + x15;
    x7 = x7 ^ x11;
    x7 = x7 << 12 | x7 >> 20;
    x3 = x3 + x7;
    x15 = x15 ^ x3;
    x15 = x15 << 8 | x15 >> 24;
    x11 = x11 + x15;
    x7 = x7 ^ x11;
    x7 = x7 << 7 | x7 >> 25;
    x0 = x0 + x5;
    x15 = x15 ^ x0;
    x15 = x15 << 16 | x15 >> 16;
    x10 = x10 + x15;
    x5 = x5 ^ x10;
    x5 = x5 << 12 | x5 >> 20;
    x0 = x0 + x5;
    x15 = x15 ^ x0;
    x15 = x15 << 8 | x15 >> 24;
    x10 = x10 + x15;
    x5 = x5 ^ x10;
    x5 = x5 << 7 | x5 >> 25;
    x1 = x1 + x6;
    x12 = x12 ^ x1;
    x12 = x12 << 16 | x12 >> 16;
    x11 = x11 + x12;
    x6 = x6 ^ x11;
    x6 = x6 << 12 | x6 >> 20;
    x1 = x1 + x6;
    x12 = x12 ^ x1;
    x12 = x12 << 8 | x12 >> 24;
    x11 = x11 + x12;
    x6 = x6 ^ x11;
    x6 = x6 << 7 | x6 >> 25;
    x2 = x2 + x7;
    x13 = x13 ^ x2;
    x13 = x13 << 16 | x13 >> 16;
    x8 = x8 + x13;
    x7 = x7 ^ x8;
    x7 = x7 << 12 | x7 >> 20;
    x2 = x2 + x7;
    x13 = x13 ^ x2;
    x13 = x13 << 8 | x13 >> 24;
    x8 = x8 + x13;
    x7 = x7 ^ x8;
    x7 = x7 << 7 | x7 >> 25;
    x3 = x3 + x4;
    x14 = x14 ^ x3;
    x14 = x14 << 16 | x14 >> 16;
    x9 = x9 + x14;
    x4 = x4 ^ x9;
    x4 = x4 << 12 | x4 >> 20;
    x3 = x3 + x4;
    x14 = x14 ^ x3;
    x14 = x14 << 8 | x14 >> 24;
    x9 = x9 + x14;
    x4 = x4 ^ x9;
    x4 = x4 << 7 | x4 >> 25;
    r = r + 1;
  }
  x0 = x0 + 0x61707865;
  x1 = x1 + 0x3320646e;
  x2 = x2 + 0x79622d32;
  x3 = x3 + 0x6b206574;
  x4 = x4 + k0;
  x5 = x5 + k1;
  x6 = x6 + k2;
  x7 = x7 + k3;
  x8 = x8 + k4;
  x9 = x9 + k5;
  x10 = x10 + k6;
  x11 = x11 + k7;
  x12 = x12 + counter;
  x13 = x13 + n0;
  x14 = x14 + n1;
  x15 = x15 + n2;
  if (len == 64) {
    out = __xor_word(out, at + 0, x0);
    out = __xor_word(out, at + 4, x1);
    out = __xor_word(out, at + 8, x2);
    out = __xor_word(out, at + 12, x3);
    out = __xor_word(out, at + 16, x4);
    out = __xor_word(out, at + 20, x5);
    out = __xor_word(out, at + 24, x6);
    out = __xor_word(out, at + 28, x7);
    out = __xor_word(out, at + 32, x8);
    out = __xor_word(out, at + 36, x9);
    out = __xor_word(out, at + 40, x10);
    out = __xor_word(out, at + 44, x11);
    out = __xor_word(out, at + 48, x12);
    out = __xor_word(out, at + 52, x13);
    out = __xor_word(out, at + 56, x14);
    out = __xor_word(out, at + 60, x15);
    return out;
  }
  let ks: u32[] = [x0, x1, x2, x3, x4, x5, x6, x7, x8, x9, x10, x11, x12, x13, x14, x15];
  let i: i32 = 0;
  while (i < len) {
    let w: u32 = ks[i >> 2] >> ((i & 3) * 8) as u32;
    out = out.with(at + i, out[at + i] ^ (w & 255) as u8);
    i = i + 1;
  }
  return out;
}
```

### `6 function __xor_into(k: Stream, counter: u32, own out: u8[], from: i32, len: i32): u8[]`

```fern
<<6 function __xor_into(k: Stream, counter: u32, own out: u8[], from: i32, len: i32): u8[]>>=
// XORs the key stream from block counter over out[from:from + len] in place.
function __xor_into(k: Stream, counter: u32, own out: u8[], from: i32, len: i32): u8[] {
  let at: i32 = 0;
  let block: u32 = counter;
  while (at < len) {
    let n: i32 = len - at;
    if (n > 64) {
      n = 64;
    }
    out = __xor_block(out, from + at, n, k.k0, k.k1, k.k2, k.k3, k.k4, k.k5, k.k6, k.k7, block, k.n0, k.n1, k.n2);
    at = at + 64;
    block = block + 1;
  }
  return out;
}
```

### `7 pub function (k: Key) chacha20_xor(nonce: [u8], counter: u32, data: [u8]): Result[u8[], aead.AeadError]`

```fern
<<7 pub function (k: Key) chacha20_xor(nonce: [u8], counter: u32, data: [u8]): Result[u8[], aead.AeadError]>>=
pub function (k: Key) chacha20_xor(nonce: [u8], counter: u32, data: [u8]): Result[u8[], aead.AeadError] {
  let st: Stream = __stream(k, nonce)?;
  return Ok(__xor_into(st, counter, __bytes_into(__alloc_u8(data.len()), 0, data, 0, data.len()), 0, data.len()));
}
```

### `8 struct Poly`

```fern
<<8 struct Poly>>=
struct Poly { r0: u32, r1: u32, r2: u32, r3: u32, r4: u32, h0: u32, h1: u32, h2: u32, h3: u32, h4: u32 }
```

### `9 function __poly_new(k0: u32, k1: u32, k2: u32, k3: u32): Poly`

```fern
<<9 function __poly_new(k0: u32, k1: u32, k2: u32, k3: u32): Poly>>=
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
  let r0: u64 = p.r0 as u64;
  let r1: u64 = p.r1 as u64;
  let r2: u64 = p.r2 as u64;
  let r3: u64 = p.r3 as u64;
  let r4: u64 = p.r4 as u64;
  let s1: u64 = r1 * 5;
  let s2: u64 = r2 * 5;
  let s3: u64 = r3 * 5;
  let s4: u64 = r4 * 5;
  let h0: u32 = p.h0;
  let h1: u32 = p.h1;
  let h2: u32 = p.h2;
  let h3: u32 = p.h3;
  let h4: u32 = p.h4;
  let tail: u8[] = __alloc_u8(16);
  let at: i32 = 0;
  while (at < len) {
    let t0: u32 = 0;
    let t1: u32 = 0;
    let t2: u32 = 0;
    let t3: u32 = 0;
    let hibit: u32 = 1 << 24;
    if (len - at >= 16) {
      t0 = __le32(b, from + at);
      t1 = __le32(b, from + at + 4);
      t2 = __le32(b, from + at + 8);
      t3 = __le32(b, from + at + 12);
    } else {
      let i: i32 = 0;
      while (i < 16) {
        if (at + i < len) {
          tail = tail.with(i, b[from + at + i]);
        } else {
          tail = tail.with(i, 0 as u8);
        }
        i = i + 1;
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
    h0 = h0 + (t0 & 0x3ffffff);
    h1 = h1 + ((t0 >> 26 | t1 << 6) & 0x3ffffff);
    h2 = h2 + ((t1 >> 20 | t2 << 12) & 0x3ffffff);
    h3 = h3 + ((t2 >> 14 | t3 << 18) & 0x3ffffff);
    h4 = h4 + (t3 >> 8 | hibit);
    let d0: u64 = h0 as u64 * r0 + h1 as u64 * s4 + h2 as u64 * s3 + h3 as u64 * s2 + h4 as u64 * s1;
    let d1: u64 = h0 as u64 * r1 + h1 as u64 * r0 + h2 as u64 * s4 + h3 as u64 * s3 + h4 as u64 * s2;
    let d2: u64 = h0 as u64 * r2 + h1 as u64 * r1 + h2 as u64 * r0 + h3 as u64 * s4 + h4 as u64 * s3;
    let d3: u64 = h0 as u64 * r3 + h1 as u64 * r2 + h2 as u64 * r1 + h3 as u64 * r0 + h4 as u64 * s4;
    let d4: u64 = h0 as u64 * r4 + h1 as u64 * r3 + h2 as u64 * r2 + h3 as u64 * r1 + h4 as u64 * r0;
    let c: u64 = d0 >> 26;
    h0 = (d0 & 0x3ffffff) as u32;
    d1 = d1 + c;
    c = d1 >> 26;
    h1 = (d1 & 0x3ffffff) as u32;
    d2 = d2 + c;
    c = d2 >> 26;
    h2 = (d2 & 0x3ffffff) as u32;
    d3 = d3 + c;
    c = d3 >> 26;
    h3 = (d3 & 0x3ffffff) as u32;
    d4 = d4 + c;
    c = d4 >> 26;
    h4 = (d4 & 0x3ffffff) as u32;
    h0 = h0 + c as u32 * 5;
    let c2: u32 = h0 >> 26;
    h0 = h0 & 0x3ffffff;
    h1 = h1 + c2;
    at = at + 16;
  }
  return Poly { ...p, h0: h0, h1: h1, h2: h2, h3: h3, h4: h4 };
}
```

### `10 function __poly_finish(p: Poly, s0: u32, s1: u32, s2: u32, s3: u32): u8[]`

```fern
<<10 function __poly_finish(p: Poly, s0: u32, s1: u32, s2: u32, s3: u32): u8[]>>=
function __poly_finish(p: Poly, s0: u32, s1: u32, s2: u32, s3: u32): u8[] {
  let h0: u32 = p.h0;
  let h1: u32 = p.h1;
  let h2: u32 = p.h2;
  let h3: u32 = p.h3;
  let h4: u32 = p.h4;
  let c: u32 = h1 >> 26;
  h1 = h1 & 0x3ffffff;
  h2 = h2 + c;
  c = h2 >> 26;
  h2 = h2 & 0x3ffffff;
  h3 = h3 + c;
  c = h3 >> 26;
  h3 = h3 & 0x3ffffff;
  h4 = h4 + c;
  c = h4 >> 26;
  h4 = h4 & 0x3ffffff;
  h0 = h0 + c * 5;
  c = h0 >> 26;
  h0 = h0 & 0x3ffffff;
  h1 = h1 + c;
  let g0: u32 = h0 + 5;
  c = g0 >> 26;
  g0 = g0 & 0x3ffffff;
  let g1: u32 = h1 + c;
  c = g1 >> 26;
  g1 = g1 & 0x3ffffff;
  let g2: u32 = h2 + c;
  c = g2 >> 26;
  g2 = g2 & 0x3ffffff;
  let g3: u32 = h3 + c;
  c = g3 >> 26;
  g3 = g3 & 0x3ffffff;
  let g4: u32 = h4 + c - (1 << 26);
  let keep_g: u32 = (g4 >> 31) - 1;
  let keep_h: u32 = keep_g ^ 0xffffffff;
  h0 = h0 & keep_h | g0 & keep_g;
  h1 = h1 & keep_h | g1 & keep_g;
  h2 = h2 & keep_h | g2 & keep_g;
  h3 = h3 & keep_h | g3 & keep_g;
  h4 = h4 & keep_h | g4 & keep_g;
  let w0: u32 = h0 | h1 << 26;
  let w1: u32 = h1 >> 6 | h2 << 20;
  let w2: u32 = h2 >> 12 | h3 << 14;
  let w3: u32 = h3 >> 18 | h4 << 8;
  let f: u64 = w0 as u64 + s0 as u64;
  w0 = f as u32;
  f = w1 as u64 + s1 as u64 + (f >> 32);
  w1 = f as u32;
  f = w2 as u64 + s2 as u64 + (f >> 32);
  w2 = f as u32;
  f = w3 as u64 + s3 as u64 + (f >> 32);
  w3 = f as u32;
  let tag: u8[] = __alloc_u8(16);
  tag = __put_le32(tag, 0, w0);
  tag = __put_le32(tag, 4, w1);
  tag = __put_le32(tag, 8, w2);
  return __put_le32(tag, 12, w3);
}
```

### `11 pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError]`

```fern
<<11 pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError]>>=
pub function poly1305(key: [u8], msg: [u8]): Result[u8[], aead.AeadError] {
  if (key.len() != 32) {
    return Err(aead.KeyLength(key.len()));
  }
  let p: Poly = __poly_new(__le32(key, 0), __le32(key, 4), __le32(key, 8), __le32(key, 12));
  p = __poly_blocks(p, msg, 0, msg.len(), true);
  return Ok(__poly_finish(p, __le32(key, 16), __le32(key, 20), __le32(key, 24), __le32(key, 28)));
}
```

### `12 function __aead_tag(k: Stream, aad: [u8], ct: [u8], from: i32, len: i32): u8[]`

```fern
<<12 function __aead_tag(k: Stream, aad: [u8], ct: [u8], from: i32, len: i32): u8[]>>=
function __aead_tag(k: Stream, aad: [u8], ct: [u8], from: i32, len: i32): u8[] {
  let zero: u8[] = __alloc_u8(32);
  let i: i32 = 0;
  while (i < 32) {
    zero = zero.with(i, 0 as u8);
    i = i + 1;
  }
  let otk: u8[] = __xor_into(k, 0, zero, 0, 32);
  let p: Poly = __poly_new(__le32(otk, 0), __le32(otk, 4), __le32(otk, 8), __le32(otk, 12));
  p = __poly_blocks(p, aad, 0, aad.len(), false);
  p = __poly_blocks(p, ct, from, len, false);
  let lens: u8[] = __alloc_u8(16);
  lens = __put_le32(lens, 0, aad.len() as u32);
  lens = __put_le32(lens, 4, 0);
  lens = __put_le32(lens, 8, len as u32);
  lens = __put_le32(lens, 12, 0);
  p = __poly_blocks(p, lens, 0, 16, false);
  return __poly_finish(p, __le32(otk, 16), __le32(otk, 20), __le32(otk, 24), __le32(otk, 28));
}
```

### `13 pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]`

```fern
<<13 pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]>>=
pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError] {
  let n: i32 = plaintext.len();
  let a: i32 = aad.len();
  let buf: u8[] = __bytes_into(__bytes_into(__alloc_u8(n + 16 + a), 0, plaintext, 0, n), n + 16, aad, 0, a);
  buf = k.seal_in_place(nonce, 0 as u64, buf, n + 16, a, 0, n)?;
  __arr_set_len(buf, n + 16);
  return Ok(buf);
}

pub function (k: Key) open(nonce: [u8], sealed: [u8], aad: [u8]): Result[u8[], aead.AeadError] {
  if (sealed.len() < 16) {
    __stream(k, nonce)?;
    return Err(aead.Forged);
  }
  let n: i32 = sealed.len() - 16;
  let a: i32 = aad.len();
  let buf: u8[] = __bytes_into(__bytes_into(__alloc_u8(n + 16 + a), 0, sealed, 0, n + 16), n + 16, aad, 0, a);
  buf = k.open_in_place(nonce, 0 as u64, buf, n + 16, a, 0, n)?;
  __arr_set_len(buf, n);
  return Ok(buf);
}

// Seals buf[at:at + n] in place under the nonce iv ^ seq, authenticating buf[aad_at:aad_at + aad_n], with the
// tag at buf[at + n:at + n + 16]: the in-place form std/crypto/aes_gcm's Key.seal_in_place has.
pub function (k: Key) seal_in_place(iv: [u8], seq: u64, own buf: u8[], aad_at: i32, aad_n: i32, at: i32, n: i32): Result[u8[], aead.AeadError] {
  let st: Stream = __stream_seq(k, iv, seq)?;
  buf = __xor_into(st, 1, buf, at, n);
  let tag: u8[] = __aead_tag(st, buf[aad_at:aad_at + aad_n], buf, at, n);
  return Ok(__bytes_into(buf, at + n, tag, 0, 16));
}

// Opens buf[at:at + n] in place, the tag at buf[at + n:at + n + 16]; a tag that does not verify returns Forged
// before anything is decrypted.
pub function (k: Key) open_in_place(iv: [u8], seq: u64, own buf: u8[], aad_at: i32, aad_n: i32, at: i32, n: i32): Result[u8[], aead.AeadError] {
  let st: Stream = __stream_seq(k, iv, seq)?;
  let want: u8[] = __aead_tag(st, buf[aad_at:aad_at + aad_n], buf, at, n);
  let diff: u8 = 0;
  for i in 0..16 {
    diff = diff | want[i] ^ buf[at + n + i];
  }
  want = want.with(0, diff);
  __ct_public(want[0:1]);
  if (want[0] != 0) {
    return Err(aead.Forged);
  }
  return Ok(__xor_into(st, 1, buf, at, n));
}
```
