# Montgomery modular arithmetic

This module provides constant-time Montgomery arithmetic for odd moduli of up to 390 bits.

## Model

`Modulus` stores the odd modulus and its Montgomery conversion constants. Limb arrays represent values under that modulus.

## Contracts

Byte conversion separates the external integer representation from the internal Montgomery representation. Range checks and conditional selection support the signature algorithms.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 const BITS: i64>>

<<4 pub function modulus(hex: string, bytes: i32): Modulus>>

<<5 function __hex_int(s: string): bigint.BigInt>>

<<6 pub function zero(m: Modulus): i64[]>>

<<7 function __zeros(n: i32): i64[]>>

<<8 pub function mul(m: Modulus, a: [i64], b: [i64]): i64[]>>

<<9 function __normal(own t: i64[]): i64[]>>

<<10 pub function select(a: [i64], b: [i64], flag: i64): i64[]>>

<<11 function __plain(m: Modulus, b: [u8]): i64[]>>

<<12 pub function to_bytes(m: Modulus, a: [i64]): u8[]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides constant-time Montgomery arithmetic for odd moduli of up to 390 bits.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/bigint";
import "std/string";
```

### `3 const BITS: i64`

```fern
<<3 const BITS: i64>>=
const BITS: i64 = 26;

const MASK: i64 = 67108863;

pub struct Modulus { p: i64[], n0: i64, r2: i64[], one: i64[], limbs: i32, bytes: i32 }
```

### `4 pub function modulus(hex: string, bytes: i32): Modulus`

```fern
<<4 pub function modulus(hex: string, bytes: i32): Modulus>>=
pub function modulus(hex: string, bytes: i32): Modulus {
  let limbs = (8 * bytes + 26) / 26;
  let p = __hex_int(hex);
  let r = bigint.from_u64(1 as u64).shl(26 * limbs);
  let pl = __limbs_of(p, limbs);
  let x = 1 as i64;
  let i = 0;
  while (i < 5) {
    x = x * (2 as i64 - pl[0] * x) & MASK;
    i += 1;
  }
  return Modulus { p: pl, n0: 0 as i64 - x & MASK, r2: __limbs_of(r.mul(r).divmod(p).1, limbs), one: __limbs_of(r.divmod(p).1, limbs), limbs: limbs, bytes: bytes };
}
```

### `5 function __hex_int(s: string): bigint.BigInt`

```fern
<<5 function __hex_int(s: string): bigint.BigInt>>=
function __hex_int(s: string): bigint.BigInt {
  let v = bigint.zero();
  for c in s.bytes() {
    let d = c as i32 - 48;
    if (c as i32 >= 97) {
      d = c as i32 - 87;
    }
    v = v.shl(4).add(bigint.from_u64(d as u64));
  }
  return v;
}

function __limbs_of(v: bigint.BigInt, limbs: i32): i64[] {
  let out: i64[] = [];
  let rest = v;
  let i = 0;
  while (i < limbs) {
    out = out.append(rest.low_u64() as i64 & MASK);
    rest = rest.shr(26);
    i += 1;
  }
  return out;
}
```

### `6 pub function zero(m: Modulus): i64[]`

```fern
<<6 pub function zero(m: Modulus): i64[]>>=
pub function zero(m: Modulus): i64[] {
  return __zeros(m.limbs);
}
```

### `7 function __zeros(n: i32): i64[]`

```fern
<<7 function __zeros(n: i32): i64[]>>=
function __zeros(n: i32): i64[] {
  let out: i64[] = [];
  let i = 0;
  while (i < n) {
    out = out.append(0 as i64);
    i += 1;
  }
  return out;
}
```

### `8 pub function mul(m: Modulus, a: [i64], b: [i64]): i64[]`

```fern
<<8 pub function mul(m: Modulus, a: [i64], b: [i64]): i64[]>>=
pub function mul(m: Modulus, a: [i64], b: [i64]): i64[] {
  let n = m.limbs;
  let t = __zeros(n);
  let i = 0;
  while (i < n) {
    let ai = a[i];
    let t0 = t[0] + ai * b[0];
    let q = (t0 & MASK) * m.n0 & MASK;
    let carry = t0 + q * m.p[0] >> BITS;
    let j = 1;
    while (j < n) {
      t = t.with(j - 1, t[j] + ai * b[j] + q * m.p[j] + carry);
      carry = 0 as i64;
      j += 1;
    }
    t = t.with(n - 1, carry);
    i += 1;
  }
  return __reduced(m, __normal(t));
}
```

### `9 function __normal(own t: i64[]): i64[]`

```fern
<<9 function __normal(own t: i64[]): i64[]>>=
function __normal(own t: i64[]): i64[] {
  let carry = 0 as i64;
  let i = 0;
  while (i < t.len()) {
    let v = t[i] + carry;
    t = t.with(i, v & MASK);
    carry = v >> BITS;
    i += 1;
  }
  return t;
}

function __reduced(m: Modulus, t: [i64]): i64[] {
  let d: i64[] = [];
  let borrow = 0 as i64;
  let i = 0;
  while (i < m.limbs) {
    let v = t[i] - m.p[i] + borrow;
    d = d.append(v & MASK);
    borrow = v >> BITS;
    i += 1;
  }
  return select(t, d, borrow + 1 as i64);
}
```

### `10 pub function select(a: [i64], b: [i64], flag: i64): i64[]`

```fern
<<10 pub function select(a: [i64], b: [i64], flag: i64): i64[]>>=
pub function select(a: [i64], b: [i64], flag: i64): i64[] {
  let mask = 0 as i64 - flag;
  let out: i64[] = [];
  let i = 0;
  while (i < a.len()) {
    out = out.append(a[i] ^ (a[i] ^ b[i]) & mask);
    i += 1;
  }
  return out;
}

pub function add(m: Modulus, a: [i64], b: [i64]): i64[] {
  let s: i64[] = [];
  let i = 0;
  while (i < m.limbs) {
    s = s.append(a[i] + b[i]);
    i += 1;
  }
  return __reduced(m, __normal(s));
}

pub function sub(m: Modulus, a: [i64], b: [i64]): i64[] {
  let d: i64[] = [];
  let borrow = 0 as i64;
  let i = 0;
  while (i < m.limbs) {
    let v = a[i] - b[i] + borrow;
    d = d.append(v & MASK);
    borrow = v >> BITS;
    i += 1;
  }
  let mask = borrow;
  let s: i64[] = [];
  i = 0;
  while (i < m.limbs) {
    s = s.append(d[i] + (m.p[i] & mask));
    i += 1;
  }
  return __normal(s);
}

pub function is_zero(a: [i64]): i64 {
  let acc = 0 as i64;
  for v in a {
    acc = acc | v;
  }
  return acc - 1 as i64 >> 63 & 1 as i64;
}

pub function in_range(m: Modulus, b: [u8]): i64 {
  let v = __plain(m, b);
  let borrow = 0 as i64;
  let i = 0;
  while (i < m.limbs) {
    borrow = v[i] - m.p[i] + borrow >> BITS;
    i += 1;
  }
  return 0 as i64 - borrow & 1 as i64 - is_zero(v);
}

pub function from_bytes(m: Modulus, b: [u8]): i64[] {
  return mul(m, __plain(m, b), m.r2);
}
```

### `11 function __plain(m: Modulus, b: [u8]): i64[]`

```fern
<<11 function __plain(m: Modulus, b: [u8]): i64[]>>=
function __plain(m: Modulus, b: [u8]): i64[] {
  let out = __zeros(m.limbs);
  let bit = 0;
  let i = b.len() - 1;
  while (i >= 0) {
    let limb = bit / 26;
    let shift = bit % 26;
    out = out.with(limb, out[limb] | b[i] as i64 << shift as i64 & MASK);
    if (shift > 18 && limb + 1 < m.limbs) {
      out = out.with(limb + 1, out[limb + 1] | b[i] as i64 >> (26 - shift) as i64);
    }
    bit += 8;
    i -= 1;
  }
  return out;
}
```

### `12 pub function to_bytes(m: Modulus, a: [i64]): u8[]`

```fern
<<12 pub function to_bytes(m: Modulus, a: [i64]): u8[]>>=
pub function to_bytes(m: Modulus, a: [i64]): u8[] {
  let one = __zeros(m.limbs).with(0, 1 as i64);
  let v = mul(m, a, one);
  let out: u8[] = [];
  let i = m.bytes - 1;
  while (i >= 0) {
    let bit = 8 * i;
    let limb = bit / 26;
    let shift = bit % 26;
    let byte = v[limb] >> shift as i64;
    if (shift > 18 && limb + 1 < m.limbs) {
      byte |= v[limb + 1] << (26 - shift) as i64;
    }
    out = out.append((byte & 255 as i64) as u8);
    i -= 1;
  }
  return out;
}

pub function inv(m: Modulus, a: [i64]): i64[] {
  let e = sub(m, zero(m), __zeros(m.limbs).with(0, 2 as i64));
  let r = m.one;
  let i = 26 * m.limbs - 1;
  while (i >= 0) {
    r = mul(m, r, r);
    if ((e[i / 26] >> (i % 26) as i64 & 1 as i64) == 1 as i64) {
      r = mul(m, r, a);
    }
    i -= 1;
  }
  return r;
}
```
