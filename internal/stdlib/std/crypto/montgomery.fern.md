# Montgomery modular arithmetic

This module provides constant-time Montgomery arithmetic for odd moduli.

## Model

`Modulus` stores the odd modulus and its Montgomery conversion constants. Limb arrays represent values under that modulus. A limb holds 64 bits, as the bits of an `i64`. `__mulhi_u64` gives the high half of each 64-bit product, so a product of two values of n limbs takes 2n² multiplications.

## Contracts

Byte conversion separates the external integer representation from the internal Montgomery representation. `from_bytes` accepts a big-endian value of any length and reduces it. Range checks and conditional selection support the signature algorithms. No branch and no memory index depends on the value of a limb. `inv` branches on the bits of the modulus, which is public in each caller.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<3 pub struct Modulus>>

<<4 pub function modulus(hex: string, bytes: i32): Modulus>>

<<5 zeros>>

<<6 carries and borrows>>

<<7 pub function mul(m: Modulus, a: [i64], b: [i64]): i64[]>>

<<8 pub function select(a: [i64], b: [i64], flag: i64): i64[]>>

<<9 bytes>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides constant-time Montgomery arithmetic for odd moduli.
```

### `3 pub struct Modulus`

`limbs` is the number of 64-bit limbs, and R is 2^(64 limbs). `kernel` is n0 = -p^-1 mod 2^64 and then the limbs of p, as `__mont_mul` takes them. `r2` is R² mod p, and `one` is R mod p, the Montgomery form of 1.

```fern
<<3 pub struct Modulus>>=
pub struct Modulus { p: i64[], kernel: i64[], r2: i64[], one: i64[], limbs: i32, bytes: i32 }
```

### `4 pub function modulus(hex: string, bytes: i32): Modulus`

`modulus` takes a public modulus in hex. `modulus_of` takes a modulus in big-endian bytes and its bit length. It has no branch and no index on the bytes, so the modulus can be secret, as an RSA prime is.

Newton's iteration doubles the correct low bits of p^-1 at each step. Six steps from 1 give 64 bits, because p is odd. R mod p comes from 2^(bits - 1), which is below p, by one doubling for each bit from there to R. R² mod p is the Montgomery form of 2^(64 limbs): the Montgomery form of 2 to the power 64 limbs, by square-and-multiply on that public exponent.

```fern
<<4 pub function modulus(hex: string, bytes: i32): Modulus>>=
pub function modulus(hex: string, bytes: i32): Modulus {
  let b = __alloc_u8(bytes);
  let bits = 0;
  let d = hex.len() - 1;
  while (d >= 0) {
    let v = __nibble(hex[d]);
    let at = bytes - 1 - (hex.len() - 1 - d) / 2;
    if (v != 0 && at >= 0) {
      b = b.with(at, b[at] | (v << 4 * ((hex.len() - 1 - d) % 2)) as u8);
      bits = 4 * (hex.len() - d);
      let top = 8;
      while (v < top) {
        bits -= 1;
        top >>= 1;
      }
    }
    d -= 1;
  }
  return modulus_of(b, bits);
}

pub function modulus_of(b: [u8], bits: i32): Modulus {
  let bytes = b.len();
  let limbs = (bytes + 7) / 8;
  let p = __limbs(b, limbs);
  let p0 = p[0] as u64;
  let x = 1 as u64;
  let i = 0;
  while (i < 6) {
    x *= 2 as u64 - p0 * x;
    i += 1;
  }
  let n0 = (0 as u64 - x) as i64;
  let kernel = __alloc_i64(limbs + 1).with(0, n0);
  i = 0;
  while (i < limbs) {
    kernel = kernel.with(i + 1, p[i]);
    i += 1;
  }
  let m = Modulus { p: p, kernel: kernel, r2: [], one: [], limbs: limbs, bytes: bytes };
  let r = __zeros(limbs).with((bits - 1) / 64, (1 as u64 << ((bits - 1) % 64) as u64) as i64);
  i = bits - 1;
  while (i < 64 * limbs) {
    r = add(m, r, r);
    i += 1;
  }
  let two = add(m, r, r);
  let rr = r;
  let e = 64 * limbs;
  let k = 31;
  while (k >= 0) {
    rr = mul(m, rr, rr);
    if ((e >> k & 1) == 1) {
      rr = mul(m, rr, two);
    }
    k -= 1;
  }
  return Modulus { ...m, r2: rr, one: r };
}

function __nibble(c: u8): i32 {
  if (c >= b'a') {
    return c as i32 - 87;
  }
  return c as i32 - 48;
}

// The limbs of a big-endian value of at most 8 limbs bytes, with no
// conversion.
function __limbs(b: [u8], limbs: i32): i64[] {
  let out = __zeros(limbs);
  let bit = 0;
  let i = b.len() - 1;
  while (i >= 0) {
    let limb = bit / 64;
    out = out.with(limb, (out[limb] as u64 | b[i] as u64 << (bit % 64) as u64) as i64);
    bit += 8;
    i -= 1;
  }
  return out;
}
```

### `5 zeros`

```fern
<<5 zeros>>=
pub function zero(m: Modulus): i64[] {
  return __zeros(m.limbs);
}

function __zeros(n: i32): i64[] {
  let out = __alloc_i64(n);
  let i = 0;
  while (i < n) {
    out = out.with(i, 0 as i64);
    i += 1;
  }
  return out;
}
```

### `6 carries and borrows`

The carry of a sum s = a + b comes from the top bits of a, b and s, with no compare. The borrow of a difference d = a - b comes from the top bits of a, b and d.

```fern
<<6 carries and borrows>>=
function __carry(a: u64, b: u64, s: u64): u64 {
  return (a & b | (a | b) & ~s) >> 63;
}

function __borrow(a: u64, b: u64, d: u64): u64 {
  return (~a & b | ~(a ^ b) & d) >> 63;
}
```

### `7 pub function mul(m: Modulus, a: [i64], b: [i64]): i64[]`

`mul` gives a b R^-1 mod p. The compiler kernel `__mont_mul` computes it by coarsely integrated operand scanning (CIOS) in the general registers of each target, with no branch on a limb. For values below p, the result is below p.

```fern
<<7 pub function mul(m: Modulus, a: [i64], b: [i64]): i64[]>>=
pub function mul(m: Modulus, a: [i64], b: [i64]): i64[] {
  return __mont_mul(a, b, m.kernel);
}

// t + top 2^(64 n) less p when that is not negative, else t.
function __reduced(m: Modulus, own t: i64[], top: u64): i64[] {
  let d = __zeros(m.limbs);
  let borrow = 0 as u64;
  let i = 0;
  while (i < m.limbs) {
    let a = t[i] as u64;
    let b = m.p[i] as u64;
    let x = a - b;
    let y = x - borrow;
    d = d.with(i, y as i64);
    borrow = __borrow(a, b, x) | __borrow(x, borrow, y);
    i += 1;
  }
  return select(t, d, (top | borrow ^ 1 as u64) as i64 & 1 as i64);
}
```

### `8 pub function select(a: [i64], b: [i64], flag: i64): i64[]`

`select` gives b when flag is 1 and a when flag is 0. `is_zero` gives 1 for a value of all zero limbs.

```fern
<<8 pub function select(a: [i64], b: [i64], flag: i64): i64[]>>=
pub function select(a: [i64], b: [i64], flag: i64): i64[] {
  let mask = 0 as i64 - flag;
  let out = __alloc_i64(a.len());
  let i = 0;
  while (i < a.len()) {
    out = out.with(i, a[i] ^ (a[i] ^ b[i]) & mask);
    i += 1;
  }
  return out;
}

pub function add(m: Modulus, a: [i64], b: [i64]): i64[] {
  let s = __zeros(m.limbs);
  let carry = 0 as u64;
  let i = 0;
  while (i < m.limbs) {
    let x = a[i] as u64;
    let y = b[i] as u64;
    let u = x + y;
    let v = u + carry;
    s = s.with(i, v as i64);
    carry = __carry(x, y, u) | __carry(u, carry, v);
    i += 1;
  }
  return __reduced(m, s, carry);
}

pub function sub(m: Modulus, a: [i64], b: [i64]): i64[] {
  let d = __zeros(m.limbs);
  let borrow = 0 as u64;
  let i = 0;
  while (i < m.limbs) {
    let x = a[i] as u64;
    let y = b[i] as u64;
    let u = x - y;
    let v = u - borrow;
    d = d.with(i, v as i64);
    borrow = __borrow(x, y, u) | __borrow(u, borrow, v);
    i += 1;
  }
  let mask = 0 as u64 - borrow;
  let carry = 0 as u64;
  i = 0;
  while (i < m.limbs) {
    let x = d[i] as u64;
    let y = m.p[i] as u64 & mask;
    let u = x + y;
    let v = u + carry;
    d = d.with(i, v as i64);
    carry = __carry(x, y, u) | __carry(u, carry, v);
    i += 1;
  }
  return d;
}

pub function is_zero(a: [i64]): i64 {
  let acc = 0 as u64;
  for v in a {
    acc = acc | v as u64;
  }
  return ((acc | 0 as u64 - acc) >> 63 ^ 1 as u64) as i64;
}

// 1 when b is in 1 to p - 1, else 0. `b` is no longer than the modulus. A
// longer input stops the program at an index.
pub function in_range(m: Modulus, b: [u8]): i64 {
  let v = __plain(m, b);
  let borrow = 0 as u64;
  let i = 0;
  while (i < m.limbs) {
    let x = v[i] as u64;
    let y = m.p[i] as u64;
    let u = x - y;
    borrow = __borrow(x, y, u) | __borrow(u, borrow, u - borrow);
    i += 1;
  }
  return borrow as i64 & 1 as i64 - is_zero(v);
}
```

### `9 bytes`

`from_bytes` reads a big-endian value of any length. It takes the bytes in groups of R from the top: each step multiplies the result by R and adds the next group. The count of steps depends only on the length.

```fern
<<9 bytes>>=
pub function from_bytes(m: Modulus, b: [u8]): i64[] {
  let w = 8 * m.limbs;
  if (b.len() <= w) {
    return mul(m, __plain(m, b), m.r2);
  }
  let first = b.len() % w;
  if (first == 0) {
    first = w;
  }
  let acc = mul(m, __plain(m, b[0:first]), m.r2);
  let at = first;
  while (at < b.len()) {
    acc = add(m, mul(m, acc, m.r2), mul(m, __plain(m, b[at:at + w]), m.r2));
    at += w;
  }
  return acc;
}

// The limbs of a big-endian value of at most 8 limbs bytes, with no
// conversion.
pub function plain(m: Modulus, b: [u8]): i64[] {
  return __plain(m, b);
}

function __plain(m: Modulus, b: [u8]): i64[] {
  return __limbs(b, m.limbs);
}

pub function to_bytes(m: Modulus, a: [i64]): u8[] {
  return plain_bytes(m, mul(m, a, __zeros(m.limbs).with(0, 1 as i64)));
}

// The big-endian bytes of the limbs of v, with no conversion.
pub function plain_bytes(m: Modulus, v: [i64]): u8[] {
  let out = __alloc_u8(m.bytes);
  let i = 0;
  while (i < m.bytes) {
    let bit = 8 * (m.bytes - 1 - i);
    out = out.with(i, (v[bit / 64] as u64 >> (bit % 64) as u64 & 255 as u64) as u8);
    i += 1;
  }
  return out;
}

// a^(p - 2), the inverse of a for a prime p. The exponent is public.
pub function inv(m: Modulus, a: [i64]): i64[] {
  let e = __zeros(m.limbs);
  let borrow = 2 as u64;
  let k = 0;
  while (k < m.limbs) {
    let x = m.p[k] as u64;
    let d = x - borrow;
    e = e.with(k, d as i64);
    borrow = __borrow(x, borrow, d);
    k += 1;
  }
  let r = m.one;
  let i = 64 * m.limbs - 1;
  while (i >= 0) {
    r = mul(m, r, r);
    if ((e[i / 64] as u64 >> (i % 64) as u64 & 1 as u64) == 1 as u64) {
      r = mul(m, r, a);
    }
    i -= 1;
  }
  return r;
}
```
