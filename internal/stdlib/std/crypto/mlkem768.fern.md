# ML-KEM-768 key encapsulation

This module implements ML-KEM-768 under FIPS 203.

## Model

Key generation produces an encapsulation key and a decapsulation key. Encapsulation returns a ciphertext and a shared secret under FIPS 203.

## Contracts

The deterministic entry points accept caller-supplied seed material. Their byte lengths are part of the API contract. Decapsulation validates its key and ciphertext inputs.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum MlKemError>>

<<4 pub function (e: MlKemError) to_string(): string>>

<<5 impl error.Error for MlKemError>>

<<6 pub function (e: MlKemError) message(): string>>

<<7 pub struct KeyPair>>

<<8 function __csubq(x: i32): i32>>

<<9 function __sample_ntt(own a: i32[], at: i32, rho: [u8], j: i32, i: i32): i32[]>>

<<10 function __keygen(d: [u8], z: [u8]): KeyPair>>

<<11 pub function keygen_derand(d: [u8], z: [u8]): Result[KeyPair, MlKemError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module implements ML-KEM-768 under FIPS 203.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/crypto";
import "core/error";
import "core/cmp";
```

### `3 pub enum MlKemError`

```fern
<<3 pub enum MlKemError>>=
pub enum MlKemError {
  SeedLength(i32),
  MessageLength(i32),
  EncapsulationKeyLength(i32),
  DecapsulationKeyLength(i32),
  CiphertextLength(i32),
  EncapsulationKeyModulus,
  DecapsulationKeyHash,
}
```

### `4 pub function (e: MlKemError) to_string(): string`

```fern
<<4 pub function (e: MlKemError) to_string(): string>>=
pub function (e: MlKemError) to_string(): string {
  return e.message();
}
```

### `5 impl error.Error for MlKemError`

```fern
<<5 impl error.Error for MlKemError>>=
impl error.Error for MlKemError {}

impl cmp.Display for MlKemError {}
```

### `6 pub function (e: MlKemError) message(): string`

```fern
<<6 pub function (e: MlKemError) message(): string>>=
pub function (e: MlKemError) message(): string {
  match (e) {
    SeedLength(n) => {
      return "mlkem768: the seed is " + n.to_string() + " bytes, want 32";
    },
    MessageLength(n) => {
      return "mlkem768: the message is " + n.to_string() + " bytes, want 32";
    },
    EncapsulationKeyLength(n) => {
      return "mlkem768: the encapsulation key is " + n.to_string() + " bytes, want 1184";
    },
    DecapsulationKeyLength(n) => {
      return "mlkem768: the decapsulation key is " + n.to_string() + " bytes, want 2400";
    },
    CiphertextLength(n) => {
      return "mlkem768: the ciphertext is " + n.to_string() + " bytes, want 1088";
    },
    EncapsulationKeyModulus => {
      return "mlkem768: the encapsulation key has a coefficient not below q";
    },
    DecapsulationKeyHash => {
      return "mlkem768: the decapsulation key's hash of its encapsulation key does not match";
    }
  }
  return "";
}
```

### `7 pub struct KeyPair`

```fern
<<7 pub struct KeyPair>>=
pub struct KeyPair {
  encapsulation_key: u8[],
  decapsulation_key: u8[],
}

pub struct Encapsulation {
  shared_secret: u8[],
  ciphertext: u8[],
}

const Q: i32 = 3329;

const ZETAS: i32[] = [
  1, 1729, 2580, 3289, 2642, 630, 1897, 848, 1062, 1919, 193, 797, 2786, 3260, 569, 1746,
  296, 2447, 1339, 1476, 3046, 56, 2240, 1333, 1426, 2094, 535, 2882, 2393, 2879, 1974, 821,
  289, 331, 3253, 1756, 1197, 2304, 2277, 2055, 650, 1977, 2513, 632, 2865, 33, 1320, 1915,
  2319, 1435, 807, 452, 1438, 2868, 1534, 2402, 2647, 2617, 1481, 648, 2474, 3110, 1227, 910,
  17, 2761, 583, 2649, 1637, 723, 2288, 1100, 1409, 2662, 3281, 233, 756, 2156, 3015, 3050,
  1703, 1651, 2789, 1789, 1847, 952, 1461, 2687, 939, 2308, 2437, 2388, 733, 2337, 268, 641,
  1584, 2298, 2037, 3220, 375, 2549, 2090, 1645, 1063, 319, 2773, 757, 2099, 561, 2466, 2594,
  2804, 1092, 403, 1026, 1143, 2150, 2775, 886, 1722, 1212, 1874, 1029, 2110, 2935, 885, 2154,
];
```

### `8 function __csubq(x: i32): i32`

```fern
<<8 function __csubq(x: i32): i32>>=
function __csubq(x: i32): i32 {
  let r = x - Q;
  return r + (r >> 31 & Q);
}

function __reduce(x: i32): i32 {
  return __csubq(x - (x as i64 * 1290167 >> 32) as i32 * Q);
}

function __zeros(n: i32): i32[] {
  let a = __alloc_i32(n);
  let i = 0;
  while (i < n) {
    a = a.with(i, 0);
    i += 1;
  }
  return a;
}

function __add(own f: i32[], g: [i32], count: i32): i32[] {
  let i = 0;
  while (i < count) {
    f = f.with(i, __csubq(f[i] + g[i]));
    i += 1;
  }
  return f;
}

function __ntt(own f: i32[], at: i32): i32[] {
  let k = 1;
  let len = 128;
  while (len >= 2) {
    let start = at;
    while (start < at + 256) {
      let zeta = ZETAS[k];
      k += 1;
      let j = start;
      while (j < start + len) {
        let t = __reduce(zeta * f[j + len]);
        let a = f[j];
        f = f.with(j + len, __csubq(a - t + Q));
        f = f.with(j, __csubq(a + t));
        j += 1;
      }
      start += 2 * len;
    }
    len /= 2;
  }
  return f;
}

function __inv_ntt(own f: i32[], at: i32): i32[] {
  let k = 127;
  let len = 2;
  while (len <= 128) {
    let start = at;
    while (start < at + 256) {
      let zeta = ZETAS[k];
      k -= 1;
      let j = start;
      while (j < start + len) {
        let t = f[j];
        let u = f[j + len];
        f = f.with(j, __csubq(t + u));
        f = f.with(j + len, __reduce(zeta * (u - t + Q)));
        j += 1;
      }
      start += 2 * len;
    }
    len *= 2;
  }
  let i = at;
  while (i < at + 256) {
    f = f.with(i, __reduce(f[i] * 3303));
    i += 1;
  }
  return f;
}

function __mul_acc(own acc: i32[], at: i32, a: [i32], ai: i32, b: [i32], bi: i32): i32[] {
  let i = 0;
  while (i < 64) {
    let g = ZETAS[64 + i];
    acc = __basemul(acc, at + 4 * i, a, ai + 4 * i, b, bi + 4 * i, g);
    acc = __basemul(acc, at + 4 * i + 2, a, ai + 4 * i + 2, b, bi + 4 * i + 2, Q - g);
    i += 1;
  }
  return acc;
}

function __basemul(own acc: i32[], at: i32, a: [i32], ai: i32, b: [i32], bi: i32, g: i32): i32[] {
  let a0 = a[ai];
  let a1 = a[ai + 1];
  let b0 = b[bi];
  let b1 = b[bi + 1];
  let c0 = __reduce(a0 * b0 + __reduce(a1 * b1) * g);
  let c1 = __reduce(a0 * b1 + a1 * b0);
  return acc.with(at, __csubq(acc[at] + c0)).with(at + 1, __csubq(acc[at + 1] + c1));
}

function __mat_vec(a: [i32], v: [i32], own acc: i32[]): i32[] {
  let i = 0;
  while (i < 3) {
    let j = 0;
    while (j < 3) {
      acc = __mul_acc(acc, 256 * i, a, 768 * i + 256 * j, v, 256 * j);
      j += 1;
    }
    i += 1;
  }
  return acc;
}

function __dot(a: [i32], v: [i32]): i32[] {
  let acc = __zeros(256);
  let j = 0;
  while (j < 3) {
    acc = __mul_acc(acc, 0, a, 256 * j, v, 256 * j);
    j += 1;
  }
  return acc;
}
```

### `9 function __sample_ntt(own a: i32[], at: i32, rho: [u8], j: i32, i: i32): i32[]`

```fern
<<9 function __sample_ntt(own a: i32[], at: i32, rho: [u8], j: i32, i: i32): i32[]>>=
function __sample_ntt(own a: i32[], at: i32, rho: [u8], j: i32, i: i32): i32[] {
  let seed = __bytes_into(__alloc_u8(34), 0, rho, 0, 32).with(32, j as u8).with(33, i as u8);
  let x = crypto.shake128_new().update(seed).reader();
  let n = 0;
  while (n < 256) {
    let next = x.squeeze(168);
    let b = next.0;
    x = next.1;
    let k = 0;
    while (k < 168 && n < 256) {
      let d1 = b[k] as i32 | (b[k + 1] as i32 & 15) << 8;
      let d2 = b[k + 1] as i32 >> 4 | b[k + 2] as i32 << 4;
      if (d1 < Q) {
        a = a.with(at + n, d1);
        n += 1;
      }
      if (d2 < Q && n < 256) {
        a = a.with(at + n, d2);
        n += 1;
      }
      k += 3;
    }
  }
  return a;
}

function __matrix(rho: [u8], transposed: boolean): i32[] {
  let a = __alloc_i32(2304);
  let i = 0;
  while (i < 3) {
    let j = 0;
    while (j < 3) {
      if (transposed) {
        a = __sample_ntt(a, 768 * i + 256 * j, rho, i, j);
      } else {
        a = __sample_ntt(a, 768 * i + 256 * j, rho, j, i);
      }
      j += 1;
    }
    i += 1;
  }
  return a;
}

function __cbd(own f: i32[], at: i32, s: [u8], n: i32): i32[] {
  let b = crypto.shake256(__bytes_into(__alloc_u8(33), 0, s, 0, 32).with(32, n as u8), 128);
  let i = 0;
  while (i < 128) {
    let x = b[i] as i32;
    let t = (x & 0x55) + (x >> 1 & 0x55);
    f = f.with(at + 2 * i, __csubq((t & 3) - (t >> 2 & 3) + Q));
    f = f.with(at + 2 * i + 1, __csubq((t >> 4 & 3) - (t >> 6 & 3) + Q));
    i += 1;
  }
  return f;
}

function __encode(own out: u8[], at: i32, f: [i32], count: i32, d: i32): u8[] {
  let acc: i64 = 0;
  let bits: i32 = 0;
  let pos = at;
  let i = 0;
  while (i < count) {
    acc = acc | f[i] as i64 << bits;
    bits += d;
    while (bits >= 8) {
      out = out.with(pos, (acc & 255) as u8);
      acc = acc >> 8;
      bits -= 8;
      pos += 1;
    }
    i += 1;
  }
  return out;
}

function __decode(b: [u8], from: i32, count: i32, d: i32): i32[] {
  let f = __alloc_i32(count);
  let mask = (1 as i64 << d) - 1;
  let acc = 0;
  let bits = 0;
  let pos = from;
  let i = 0;
  while (i < count) {
    while (bits < d) {
      acc |= b[pos] as i64 << bits;
      bits += 8;
      pos += 1;
    }
    f = f.with(i, (acc & mask) as i32);
    acc >>= d;
    bits -= d;
    i += 1;
  }
  return f;
}

function __decode12(b: [u8], from: i32): i32[] {
  let f = __decode(b, from, 768, 12);
  let i = 0;
  while (i < 768) {
    f = f.with(i, __csubq(f[i]));
    i += 1;
  }
  return f;
}

function __compress(own f: i32[], d: i32): i32[] {
  let i = 0;
  while (i < f.len()) {
    let y: i64 = (f[i] as i64 << d) + 1664;
    f = f.with(i, (y * 330282857 >> 40) as i32 & (1 << d) - 1);
    i += 1;
  }
  return f;
}

function __decompress(own f: i32[], d: i32): i32[] {
  let i = 0;
  while (i < f.len()) {
    f = f.with(i, f[i] * Q + (1 << d - 1) >> d);
    i += 1;
  }
  return f;
}

function __h(b: [u8]): u8[] {
  return crypto.sha3_256(b);
}

function __g(a: [u8], b: [u8]): u8[] {
  return crypto.sha3_512_new().update(a).update(b).finish();
}
```

### `10 function __keygen(d: [u8], z: [u8]): KeyPair`

```fern
<<10 function __keygen(d: [u8], z: [u8]): KeyPair>>=
function __keygen(d: [u8], z: [u8]): KeyPair {
  let rs = __g(d, [3 as u8]);
  let rho = __bytes_into(__alloc_u8(32), 0, rs, 0, 32);
  __ct_public(rho);
  let sigma = rs[32:64];
  let a = __matrix(rho, false);
  let s = __alloc_i32(768);
  let e = __alloc_i32(768);
  let i = 0;
  while (i < 3) {
    s = __ntt(__cbd(s, 256 * i, sigma, i), 256 * i);
    e = __ntt(__cbd(e, 256 * i, sigma, 3 + i), 256 * i);
    i += 1;
  }
  let t = __mat_vec(a, s, e);
  let ek = __bytes_into(__encode(__alloc_u8(1184), 0, t, 768, 12), 1152, rho, 0, 32);
  let dk = __bytes_into(__encode(__alloc_u8(2400), 0, s, 768, 12), 1152, ek, 0, 1184);
  dk = __bytes_into(dk, 2336, __h(ek), 0, 32);
  dk = __bytes_into(dk, 2368, z, 0, 32);
  return KeyPair { encapsulation_key: ek, decapsulation_key: dk };
}

function __encrypt(t: [i32], rho: [u8], m: [u8], r: [u8]): u8[] {
  let at = __matrix(rho, true);
  let y = __alloc_i32(768);
  let e1 = __alloc_i32(768);
  let i = 0;
  while (i < 3) {
    y = __ntt(__cbd(y, 256 * i, r, i), 256 * i);
    e1 = __cbd(e1, 256 * i, r, 3 + i);
    i += 1;
  }
  let u = __mat_vec(at, y, __zeros(768));
  i = 0;
  while (i < 3) {
    u = __inv_ntt(u, 256 * i);
    i += 1;
  }
  u = __add(u, e1, 768);
  let v = __inv_ntt(__dot(t, y), 0);
  v = __add(v, __cbd(__alloc_i32(256), 0, r, 6), 256);
  v = __add(v, __decompress(__decode(m, 0, 256, 1), 1), 256);
  let c = __encode(__alloc_u8(1088), 0, __compress(u, 10), 768, 10);
  return __encode(c, 960, __compress(v, 4), 256, 4);
}

function __decrypt(s: [i32], c: [u8]): u8[] {
  let u = __decompress(__decode(c, 0, 768, 10), 10);
  let i = 0;
  while (i < 3) {
    u = __ntt(u, 256 * i);
    i += 1;
  }
  let w = __inv_ntt(__dot(s, u), 0);
  let v = __decompress(__decode(c, 960, 256, 4), 4);
  i = 0;
  while (i < 256) {
    v = v.with(i, __csubq(v[i] - w[i] + Q));
    i += 1;
  }
  return __encode(__alloc_u8(32), 0, __compress(v, 1), 256, 1);
}

function __same(a: [u8], b: [u8], n: i32): boolean {
  let diff: u8 = 0;
  let i = 0;
  while (i < n) {
    diff = diff | a[i] ^ b[i];
    i += 1;
  }
  return diff == 0;
}
```

### `11 pub function keygen_derand(d: [u8], z: [u8]): Result[KeyPair, MlKemError]`

```fern
<<11 pub function keygen_derand(d: [u8], z: [u8]): Result[KeyPair, MlKemError]>>=
pub function keygen_derand(d: [u8], z: [u8]): Result[KeyPair, MlKemError] {
  if (d.len() != 32) {
    return Err(SeedLength(d.len()));
  }
  if (z.len() != 32) {
    return Err(SeedLength(z.len()));
  }
  return Ok(__keygen(d, z));
}

pub function encaps_derand(ek: [u8], m: [u8]): Result[Encapsulation, MlKemError] {
  if (ek.len() != 1184) {
    return Err(EncapsulationKeyLength(ek.len()));
  }
  if (m.len() != 32) {
    return Err(MessageLength(m.len()));
  }
  let t = __decode12(ek, 0);
  if (!__same(__encode(__alloc_u8(1152), 0, t, 768, 12), ek, 1152)) {
    return Err(EncapsulationKeyModulus);
  }
  let kr = __g(m, __h(ek));
  return Ok(Encapsulation { shared_secret: __bytes_into(__alloc_u8(32), 0, kr, 0, 32), ciphertext: __encrypt(t, ek[1152:1184], m, kr[32:64]) });
}

pub function decaps(dk: [u8], c: [u8]): Result[u8[], MlKemError] {
  if (dk.len() != 2400) {
    return Err(DecapsulationKeyLength(dk.len()));
  }
  if (c.len() != 1088) {
    return Err(CiphertextLength(c.len()));
  }
  let ek = __bytes_into(__alloc_u8(1184), 0, dk, 1152, 1184);
  let h = __bytes_into(__alloc_u8(32), 0, dk, 2336, 32);
  __ct_public(ek);
  __ct_public(h);
  if (!__same(__h(ek), h, 32)) {
    return Err(DecapsulationKeyHash);
  }
  let m = __decrypt(__decode12(dk, 0), c);
  let kr = __g(m, h);
  let rejected = crypto.shake256_new().update(dk[2368:2400]).update(c).reader().squeeze(32).0;
  let again = __encrypt(__decode12(ek, 0), ek[1152:1184], m, kr[32:64]);
  let diff = 0;
  let i = 0;
  while (i < 1088) {
    diff |= (c[i] ^ again[i]) as i32;
    i += 1;
  }
  let keep = diff - 1 >> 31 & 255;
  let k = __alloc_u8(32);
  i = 0;
  while (i < 32) {
    k = k.with(i, (kr[i] as i32 & keep | rejected[i] as i32 & (keep ^ 255)) as u8);
    i += 1;
  }
  return Ok(k);
}
```
