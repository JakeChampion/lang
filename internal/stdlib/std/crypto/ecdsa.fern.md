# ECDSA signatures and key agreement

This module provides ECDSA operations for the NIST P-256 and P-384 curves.

## Model

Curve-tagged keys distinguish the NIST P-256 and P-384 curves. Public-key construction validates an encoded point before signature verification. Signing, verifying and ECDH are methods on the keys.

## Contracts

Private-key construction, signatures, and ECDH return concrete errors for invalid input. The SHA-2 selector determines the digest used by a signature operation.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 pub enum Curve>>

<<3 pub function (e: EcdsaError) to_string(): string>>

<<4 impl error.Error for EcdsaError>>

<<5 pub function (e: EcdsaError) message(): string>>

<<6 pub struct PublicKey>>

<<7 function __hex_int(s: string): bigint.BigInt>>

<<8 struct CurveData>>

<<9 function __curve_data(c: Curve): CurveData>>

<<10 pub function public_key(curve: Curve, point: [u8]): Result[PublicKey, EcdsaError]>>

<<11 function __double(p: bigint.BigInt, q: Point): Point>>

<<12 pub function (key: PublicKey) verify(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean>>

<<13 pub struct PrivateKey>>

<<14 function __signer(c: Curve): Signer>>

<<15 pub function private_key(curve: Curve, d: [u8]): Result[PrivateKey, EcdsaError]>>

<<16 function __scalar_ok(curve: Curve, d: [u8]): boolean>>

<<17 pub function (k: PrivateKey) public_point(): u8[]>>

<<18 function __sign_loop(hash: sha2.Hash, d: [u8], e: [u8], in_range: (u8[])>>

<<19 function __der_integer(v: [u8]): u8[]>>

<<20 struct Drbg>>

<<21 function __mac(h: sha2.Hash, key: [u8], msg: [u8]): u8[]>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module provides ECDSA operations for the NIST P-256 and P-384 curves.
import "core/bigint";
import "std/crypto";
import "std/crypto/sha2";
import "std/crypto/montgomery" as mont;
import "std/crypto/p256";
import "std/error";
import "core/cmp";
```

### `2 pub enum Curve`

```fern
<<2 pub enum Curve>>=
pub enum Curve {
  P256,
  P384,
}

pub enum EcdsaError {
  Encoding,
  NotOnCurve,
  ScalarRange,
  SigningHash,
}
```

### `3 pub function (e: EcdsaError) to_string(): string`

```fern
<<3 pub function (e: EcdsaError) to_string(): string>>=
pub function (e: EcdsaError) to_string(): string {
  return e.message();
}
```

### `4 impl error.Error for EcdsaError`

```fern
<<4 impl error.Error for EcdsaError>>=
impl error.Error for EcdsaError {}

impl cmp.Display for EcdsaError {}
```

### `5 pub function (e: EcdsaError) message(): string`

```fern
<<5 pub function (e: EcdsaError) message(): string>>=
pub function (e: EcdsaError) message(): string {
  match (e) {
    Encoding => {
      return "ecdsa: the key is not an uncompressed SEC 1 point on its curve";
    },
    NotOnCurve => {
      return "ecdsa: the point is not on the curve";
    },
    ScalarRange => {
      return "ecdsa: the private scalar is not in [1, n) at the curve's size";
    },
    SigningHash => {
      return "ecdsa: signing takes SHA-256 or SHA-384";
    }
  }
  return "";
}
```

### `6 pub struct PublicKey`

```fern
<<6 pub struct PublicKey>>=
pub struct PublicKey { curve: Curve, x: bigint.BigInt, y: bigint.BigInt }

struct Params { p: bigint.BigInt, n: bigint.BigInt, b: bigint.BigInt, g: Point, bytes: i32 }

struct Point { x: bigint.BigInt, y: bigint.BigInt, z: bigint.BigInt }
```

### `7 function __hex_int(s: string): bigint.BigInt`

```fern
<<7 function __hex_int(s: string): bigint.BigInt>>=
function __hex_int(s: string): bigint.BigInt {
  let v: bigint.BigInt = bigint.zero();
  let i: i32 = 0;
  while (i < s.len()) {
    let c: i32 = s[i] as i32;
    let d: i32 = c - 48;
    if (c >= 97) {
      d = c - 87;
    }
    v = v.shl(4).add(bigint.from_u64(d as u64));
    i = i + 1;
  }
  return v;
}

function __bytes_int(bytes: [u8]): bigint.BigInt {
  let value: bigint.BigInt = bigint.zero();
  for byte in bytes {
    value = value.shl(8).add(bigint.from_u64(byte as u64));
  }
  return value;
}
```

### `8 struct CurveData`

```fern
<<8 struct CurveData>>=
struct CurveData { p: string, n: string, b: u8[], gx: u8[], gy: u8[], bytes: i32 }
```

### `9 function __curve_data(c: Curve): CurveData`

```fern
<<9 function __curve_data(c: Curve): CurveData>>=
function __curve_data(c: Curve): CurveData {
  match (c) {
    P256 => {
      return CurveData { p: "ffffffff00000001000000000000000000000000ffffffffffffffffffffffff", n: "ffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551", b: [90 as u8, 198 as u8, 53 as u8, 216 as u8, 170 as u8, 58 as u8, 147 as u8, 231 as u8, 179 as u8, 235 as u8, 189 as u8, 85 as u8, 118 as u8, 152 as u8, 134 as u8, 188 as u8, 101 as u8, 29 as u8, 6 as u8, 176 as u8, 204 as u8, 83 as u8, 176 as u8, 246 as u8, 59 as u8, 206 as u8, 60 as u8, 62 as u8, 39 as u8, 210 as u8, 96 as u8, 75 as u8], gx: [107 as u8, 23 as u8, 209 as u8, 242 as u8, 225 as u8, 44 as u8, 66 as u8, 71 as u8, 248 as u8, 188 as u8, 230 as u8, 229 as u8, 99 as u8, 164 as u8, 64 as u8, 242 as u8, 119 as u8, 3 as u8, 125 as u8, 129 as u8, 45 as u8, 235 as u8, 51 as u8, 160 as u8, 244 as u8, 161 as u8, 57 as u8, 69 as u8, 216 as u8, 152 as u8, 194 as u8, 150 as u8], gy: [79 as u8, 227 as u8, 66 as u8, 226 as u8, 254 as u8, 26 as u8, 127 as u8, 155 as u8, 142 as u8, 231 as u8, 235 as u8, 74 as u8, 124 as u8, 15 as u8, 158 as u8, 22 as u8, 43 as u8, 206 as u8, 51 as u8, 87 as u8, 107 as u8, 49 as u8, 94 as u8, 206 as u8, 203 as u8, 182 as u8, 64 as u8, 104 as u8, 55 as u8, 191 as u8, 81 as u8, 245 as u8], bytes: 32 };
    },
    P384 => {
      return CurveData { p: "fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffeffffffff0000000000000000ffffffff", n: "ffffffffffffffffffffffffffffffffffffffffffffffffc7634d81f4372ddf581a0db248b0a77aecec196accc52973", b: [179 as u8, 49 as u8, 47 as u8, 167 as u8, 226 as u8, 62 as u8, 231 as u8, 228 as u8, 152 as u8, 142 as u8, 5 as u8, 107 as u8, 227 as u8, 248 as u8, 45 as u8, 25 as u8, 24 as u8, 29 as u8, 156 as u8, 110 as u8, 254 as u8, 129 as u8, 65 as u8, 18 as u8, 3 as u8, 20 as u8, 8 as u8, 143 as u8, 80 as u8, 19 as u8, 135 as u8, 90 as u8, 198 as u8, 86 as u8, 57 as u8, 141 as u8, 138 as u8, 46 as u8, 209 as u8, 157 as u8, 42 as u8, 133 as u8, 200 as u8, 237 as u8, 211 as u8, 236 as u8, 42 as u8, 239 as u8], gx: [170 as u8, 135 as u8, 202 as u8, 34 as u8, 190 as u8, 139 as u8, 5 as u8, 55 as u8, 142 as u8, 177 as u8, 199 as u8, 30 as u8, 243 as u8, 32 as u8, 173 as u8, 116 as u8, 110 as u8, 29 as u8, 59 as u8, 98 as u8, 139 as u8, 167 as u8, 155 as u8, 152 as u8, 89 as u8, 247 as u8, 65 as u8, 224 as u8, 130 as u8, 84 as u8, 42 as u8, 56 as u8, 85 as u8, 2 as u8, 242 as u8, 93 as u8, 191 as u8, 85 as u8, 41 as u8, 108 as u8, 58 as u8, 84 as u8, 94 as u8, 56 as u8, 114 as u8, 118 as u8, 10 as u8, 183 as u8], gy: [54 as u8, 23 as u8, 222 as u8, 74 as u8, 150 as u8, 38 as u8, 44 as u8, 111 as u8, 93 as u8, 158 as u8, 152 as u8, 191 as u8, 146 as u8, 146 as u8, 220 as u8, 41 as u8, 248 as u8, 244 as u8, 29 as u8, 189 as u8, 40 as u8, 154 as u8, 20 as u8, 124 as u8, 233 as u8, 218 as u8, 49 as u8, 19 as u8, 181 as u8, 240 as u8, 184 as u8, 192 as u8, 10 as u8, 96 as u8, 177 as u8, 206 as u8, 29 as u8, 126 as u8, 129 as u8, 157 as u8, 122 as u8, 67 as u8, 29 as u8, 124 as u8, 144 as u8, 234 as u8, 14 as u8, 95 as u8], bytes: 48 };
    }
  }
  return __curve_data(P256);
}

function __params(c: Curve): Params {
  let h: CurveData = __curve_data(c);
  return __curve(h.p, h.n, h.b, h.gx, h.gy, h.bytes);
}

function __curve(p: string, n: string, b: [u8], gx: [u8], gy: [u8], bytes: i32): Params {
  return Params { p: __hex_int(p), n: __hex_int(n), b: __bytes_int(b), g: Point { x: __bytes_int(gx), y: __bytes_int(gy), z: bigint.from_u64(1 as u64) }, bytes: bytes };
}

function __infinity(): Point {
  return Point { x: __int(1), y: __int(1), z: bigint.zero() };
}

function __int(v: i32): bigint.BigInt {
  return bigint.from_u64(v as u64);
}

function __mod(a: bigint.BigInt, m: bigint.BigInt): bigint.BigInt {
  let r: bigint.BigInt = a.divmod(m).1;
  if (r.is_negative()) {
    return r.add(m);
  }
  return r;
}

function __mul(a: bigint.BigInt, b: bigint.BigInt, m: bigint.BigInt): bigint.BigInt {
  return __mod(a.mul(b), m);
}

function __inv(a: bigint.BigInt, m: bigint.BigInt): bigint.BigInt {
  let e: bigint.BigInt = m.sub(__int(2));
  let r: bigint.BigInt = __int(1);
  let i: i32 = e.bit_length() - 1;
  while (i >= 0) {
    r = __mul(r, r, m);
    if (e.bit(i)) {
      r = __mul(r, a, m);
    }
    i = i - 1;
  }
  return r;
}

function __from_be(b: [u8], from: i32, len: i32): bigint.BigInt {
  let v: bigint.BigInt = bigint.zero();
  let i: i32 = 0;
  while (i < len) {
    v = v.shl(8).add(bigint.from_u64(b[from + i] as u64));
    i = i + 1;
  }
  return v;
}

function __on_curve(c: Params, x: bigint.BigInt, y: bigint.BigInt): boolean {
  let p: bigint.BigInt = c.p;
  if (x.cmp(p) >= 0 || y.cmp(p) >= 0) {
    return false;
  }
  let rhs: bigint.BigInt = __mod(__mul(__mul(x, x, p), x, p).sub(x.mul(__int(3))).add(c.b), p);
  return __mul(y, y, p).eq(rhs);
}
```

### `10 pub function public_key(curve: Curve, point: [u8]): Result[PublicKey, EcdsaError]`

```fern
<<10 pub function public_key(curve: Curve, point: [u8]): Result[PublicKey, EcdsaError]>>=
pub function public_key(curve: Curve, point: [u8]): Result[PublicKey, EcdsaError] {
  let c: Params = __params(curve);
  if (point.len() != 1 + 2 * c.bytes || point[0] != 4 as u8) {
    return Err(Encoding);
  }
  let x: bigint.BigInt = __from_be(point, 1, c.bytes);
  let y: bigint.BigInt = __from_be(point, 1 + c.bytes, c.bytes);
  if (!__on_curve(c, x, y)) {
    return Err(NotOnCurve);
  }
  return Ok(PublicKey { curve: curve, x: x, y: y });
}
```

### `11 function __double(p: bigint.BigInt, q: Point): Point`

```fern
<<11 function __double(p: bigint.BigInt, q: Point): Point>>=
function __double(p: bigint.BigInt, q: Point): Point {
  if (q.z.is_zero() || q.y.is_zero()) {
    return __infinity();
  }
  let delta: bigint.BigInt = __mul(q.z, q.z, p);
  let gamma: bigint.BigInt = __mul(q.y, q.y, p);
  let beta: bigint.BigInt = __mul(q.x, gamma, p);
  let alpha: bigint.BigInt = __mul(__mul(q.x.sub(delta), q.x.add(delta), p), __int(3), p);
  let x3: bigint.BigInt = __mod(__mul(alpha, alpha, p).sub(beta.mul(__int(8))), p);
  let z3: bigint.BigInt = __mod(__mul(q.y.add(q.z), q.y.add(q.z), p).sub(gamma).sub(delta), p);
  let y3: bigint.BigInt = __mod(__mul(alpha, beta.mul(__int(4)).sub(x3), p).sub(__mul(gamma, gamma, p).mul(__int(8))), p);
  return Point { x: x3, y: y3, z: z3 };
}

function __add(p: bigint.BigInt, a: Point, b: Point): Point {
  if (a.z.is_zero()) {
    return b;
  }
  if (b.z.is_zero()) {
    return a;
  }
  let z1z1: bigint.BigInt = __mul(a.z, a.z, p);
  let z2z2: bigint.BigInt = __mul(b.z, b.z, p);
  let u1: bigint.BigInt = __mul(a.x, z2z2, p);
  let u2: bigint.BigInt = __mul(b.x, z1z1, p);
  let s1: bigint.BigInt = __mul(__mul(a.y, b.z, p), z2z2, p);
  let s2: bigint.BigInt = __mul(__mul(b.y, a.z, p), z1z1, p);
  let h: bigint.BigInt = __mod(u2.sub(u1), p);
  let r: bigint.BigInt = __mod(s2.sub(s1).mul(__int(2)), p);
  if (h.is_zero()) {
    if (r.is_zero()) {
      return __double(p, a);
    }
    return __infinity();
  }
  let i: bigint.BigInt = __mul(h.mul(__int(2)), h.mul(__int(2)), p);
  let j: bigint.BigInt = __mul(h, i, p);
  let v: bigint.BigInt = __mul(u1, i, p);
  let x3: bigint.BigInt = __mod(__mul(r, r, p).sub(j).sub(v.mul(__int(2))), p);
  let y3: bigint.BigInt = __mod(__mul(r, v.sub(x3), p).sub(__mul(s1, j, p).mul(__int(2))), p);
  let z3: bigint.BigInt = __mul(__mod(__mul(a.z.add(b.z), a.z.add(b.z), p).sub(z1z1).sub(z2z2), p), h, p);
  return Point { x: x3, y: y3, z: z3 };
}

function __combine(c: Params, u1: bigint.BigInt, u2: bigint.BigInt, q: Point): Point {
  let p: bigint.BigInt = c.p;
  let gq: Point = __add(p, c.g, q);
  let r: Point = __infinity();
  let i: i32 = 8 * c.bytes - 1;
  while (i >= 0) {
    r = __double(p, r);
    let b1: boolean = u1.bit(i);
    let b2: boolean = u2.bit(i);
    if (b1 && b2) {
      r = __add(p, r, gq);
    } else if (b1) {
      r = __add(p, r, c.g);
    } else if (b2) {
      r = __add(p, r, q);
    }
    i = i - 1;
  }
  return r;
}

function __der_int(sig: [u8], at: i32, max: i32): Option[(bigint.BigInt, i32)] {
  if (at + 2 > sig.len() || sig[at] != 2 as u8) {
    return None;
  }
  let len: i32 = sig[at + 1] as i32;
  if (len == 0 || len > max || at + 2 + len > sig.len()) {
    return None;
  }
  if (sig[at + 2] as i32 >= 128) {
    return None;
  }
  if (len > 1 && sig[at + 2] == 0 as u8 && sig[at + 3] as i32 < 128) {
    return None;
  }
  return Some((__from_be(sig, at + 2, len), at + 2 + len));
}

function __verify_rs(c: Params, key: PublicKey, hash: sha2.Hash, msg: [u8], r: bigint.BigInt, s: bigint.BigInt): boolean {
  let n: bigint.BigInt = c.n;
  if (r.is_zero() || s.is_zero() || r.cmp(n) >= 0 || s.cmp(n) >= 0) {
    return false;
  }
  let d: u8[] = hash.digest(msg);
  let take: i32 = d.len();
  if (take > c.bytes) {
    take = c.bytes;
  }
  let e: bigint.BigInt = __from_be(d, 0, take);
  let w: bigint.BigInt = __inv(s, n);
  let u1: bigint.BigInt = __mul(e, w, n);
  let u2: bigint.BigInt = __mul(r, w, n);
  let x: Point = __combine(c, u1, u2, Point { x: key.x, y: key.y, z: __int(1) });
  if (x.z.is_zero()) {
    return false;
  }
  let p: bigint.BigInt = c.p;
  let zinv: bigint.BigInt = __inv(x.z, p);
  let ax: bigint.BigInt = __mul(x.x, __mul(zinv, zinv, p), p);
  return __mod(ax, n).eq(r);
}
```

### `12 pub function (key: PublicKey) verify(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean`

```fern
<<12 pub function (key: PublicKey) verify(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean>>=
pub function (key: PublicKey) verify(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean {
  if (sig.len() < 8 || sig.len() > 129 || sig[0] != 48 as u8 || sig[1] as i32 != sig.len() - 2) {
    return false;
  }
  let c: Params = __params(key.curve);
  match (__der_int(sig, 2, c.bytes + 1)) {
    Some(r) => {
      match (__der_int(sig, r.1, c.bytes + 1)) {
        Some(s) => {
          return s.1 == sig.len() && __verify_rs(c, key, hash, msg, r.0, s.0);
        },
        None => {
          return false;
        }
      }
    },
    None => {
      return false;
    }
  }
  return false;
}
```

### `13 pub struct PrivateKey`

```fern
<<13 pub struct PrivateKey>>=
pub struct PrivateKey { curve: Curve, d: u8[] }

struct Proj { x: i64[], y: i64[], z: i64[] }

struct Signer { fp: mont.Modulus, fn: mont.Modulus, b: i64[], g: Proj, bytes: i32 }
```

### `14 function __signer(c: Curve): Signer`

```fern
<<14 function __signer(c: Curve): Signer>>=
function __signer(c: Curve): Signer {
  let h: CurveData = __curve_data(c);
  let fp: mont.Modulus = mont.modulus(h.p, h.bytes);
  let g: Proj = Proj { x: mont.from_bytes(fp, h.gx), y: mont.from_bytes(fp, h.gy), z: fp.one };
  return Signer { fp: fp, fn: mont.modulus(h.n, h.bytes), b: mont.from_bytes(fp, h.b), g: g, bytes: h.bytes };
}
```

### `15 pub function private_key(curve: Curve, d: [u8]): Result[PrivateKey, EcdsaError]`

```fern
<<15 pub function private_key(curve: Curve, d: [u8]): Result[PrivateKey, EcdsaError]>>=
pub function private_key(curve: Curve, d: [u8]): Result[PrivateKey, EcdsaError] {
  if (!__scalar_ok(curve, d)) {
    return Err(ScalarRange);
  }
  return Ok(PrivateKey { curve: curve, d: __copy(d) });
}
```

### `16 function __scalar_ok(curve: Curve, d: [u8]): boolean`

```fern
<<16 function __scalar_ok(curve: Curve, d: [u8]): boolean>>=
function __scalar_ok(curve: Curve, d: [u8]): boolean {
  match (curve) {
    P256 => {
      return d.len() == 32 && __revealed(p256.in_scalar_range(d));
    },
    _ => {}
  }
  let s: Signer = __signer(curve);
  return d.len() == s.bytes && __revealed(mont.in_range(s.fn, d));
}

function __copy(b: [u8]): u8[] {
  let out: u8[] = [];
  for x in b {
    out = out.append(x);
  }
  return out;
}
```

### `17 pub function (k: PrivateKey) public_point(): u8[]`

```fern
<<17 pub function (k: PrivateKey) public_point(): u8[]>>=
pub function (k: PrivateKey) public_point(): u8[] {
  match (k.curve) {
    P256 => {
      return p256.base_point(k.d);
    },
    _ => {}
  }
  let s: Signer = __signer(k.curve);
  let xy: (u8[], u8[]) = __affine(s, __base_mul(s, k.d));
  let out: u8[] = [4 as u8];
  for b in xy.0 {
    out = out.append(b);
  }
  for b in xy.1 {
    out = out.append(b);
  }
  return out;
}

pub function (key: PrivateKey) ecdh(peer: [u8]): Result[u8[], EcdsaError] {
  match (public_key(key.curve, peer)) {
    Ok(_) => {},
    Err(e) => {
      return Err(e);
    }
  }
  match (key.curve) {
    P256 => {
      return Ok(p256.scalar_mul_x(key.d, peer));
    },
    _ => {}
  }
  let s: Signer = __signer(key.curve);
  let q: Proj = Proj { x: mont.from_bytes(s.fp, peer[1:1 + s.bytes]), y: mont.from_bytes(s.fp, peer[1 + s.bytes:1 + 2 * s.bytes]), z: s.fp.one };
  return Ok(__affine(s, __scalar_mul(s, q, key.d)).0);
}

pub function (key: PrivateKey) sign(hash: sha2.Hash, msg: [u8]): Result[u8[], EcdsaError] {
  match (hash) {
    sha2.Sha512 => {
      return Err(SigningHash);
    },
    _ => {}
  }
  let digest: u8[] = hash.digest(msg);
  match (key.curve) {
    P256 => {
      let e: u8[] = p256.scalar_reduce(digest[0:32]);
      return Ok(__sign_loop(hash, key.d, e, (k: u8[]): i64 => p256.in_scalar_range(k), (k: u8[]): (u8[], u8[]) => p256.sign_rs(key.d, e, k)));
    },
    _ => {}
  }
  let s: Signer = __signer(key.curve);
  let take: i32 = digest.len();
  if (take > s.bytes) {
    take = s.bytes;
  }
  let e: i64[] = mont.from_bytes(s.fn, digest[0:take]);
  return Ok(__sign_loop(hash, key.d, mont.to_bytes(s.fn, e), (k: u8[]): i64 => mont.in_range(s.fn, k), (k: u8[]): (u8[], u8[]) => __sign_with(s, key.d, e, k)));
}
```

### `18 function __sign_loop(hash: sha2.Hash, d: [u8], e: [u8], in_range: (u8[])`

```fern
<<18 function __sign_loop(hash: sha2.Hash, d: [u8], e: [u8], in_range: (u8[])>>=
function __sign_loop(hash: sha2.Hash, d: [u8], e: [u8], in_range: (u8[]) => i64, rs_of: (u8[]) => (u8[], u8[])): u8[] {
  let drbg: Drbg = __drbg_new(hash, d, e);
  while (true) {
    let next: (Drbg, u8[]) = __drbg_next(drbg, d.len());
    drbg = next.0;
    let k: u8[] = next.1;
    if (__revealed(in_range(k))) {
      let rs: (u8[], u8[]) = rs_of(k);
      __ct_public(rs.0);
      __ct_public(rs.1);
      if (!__all_zero(rs.0) && !__all_zero(rs.1)) {
        return __der(rs.0, rs.1);
      }
    }
    drbg = __drbg_reseed(drbg);
  }
  return [];
}

function __sign_with(s: Signer, d: [u8], e: [i64], k: [u8]): (u8[], u8[]) {
  let x: u8[] = __affine(s, __base_mul(s, k)).0;
  let r: i64[] = mont.from_bytes(s.fn, x);
  let kinv: i64[] = mont.inv(s.fn, mont.from_bytes(s.fn, k));
  let sm: i64[] = mont.mul(s.fn, kinv, mont.add(s.fn, e, mont.mul(s.fn, r, mont.from_bytes(s.fn, d))));
  return (mont.to_bytes(s.fn, r), mont.to_bytes(s.fn, sm));
}

function __revealed(flag: i64): boolean {
  let verdict: u8[] = [flag as u8];
  __ct_public(verdict);
  return verdict[0] == 1 as u8;
}

function __all_zero(b: [u8]): boolean {
  for x in b {
    if (x != 0 as u8) {
      return false;
    }
  }
  return true;
}

function __padd(s: Signer, p1: Proj, p2: Proj): Proj {
  let m: mont.Modulus = s.fp;
  let t0: i64[] = mont.mul(m, p1.x, p2.x);
  let t1: i64[] = mont.mul(m, p1.y, p2.y);
  let t2: i64[] = mont.mul(m, p1.z, p2.z);
  let t3: i64[] = mont.mul(m, mont.add(m, p1.x, p1.y), mont.add(m, p2.x, p2.y));
  t3 = mont.sub(m, t3, mont.add(m, t0, t1));
  let t4: i64[] = mont.mul(m, mont.add(m, p1.y, p1.z), mont.add(m, p2.y, p2.z));
  t4 = mont.sub(m, t4, mont.add(m, t1, t2));
  let x3: i64[] = mont.mul(m, mont.add(m, p1.x, p1.z), mont.add(m, p2.x, p2.z));
  let y3: i64[] = mont.sub(m, x3, mont.add(m, t0, t2));
  let z3: i64[] = mont.mul(m, s.b, t2);
  x3 = mont.sub(m, y3, z3);
  x3 = mont.add(m, x3, mont.add(m, x3, x3));
  z3 = mont.sub(m, t1, x3);
  x3 = mont.add(m, t1, x3);
  y3 = mont.mul(m, s.b, y3);
  t2 = mont.add(m, t2, mont.add(m, t2, t2));
  y3 = mont.sub(m, mont.sub(m, y3, t2), t0);
  y3 = mont.add(m, y3, mont.add(m, y3, y3));
  t0 = mont.sub(m, mont.add(m, t0, mont.add(m, t0, t0)), t2);
  t1 = mont.mul(m, t4, y3);
  t2 = mont.mul(m, t0, y3);
  y3 = mont.add(m, mont.mul(m, x3, z3), t2);
  x3 = mont.sub(m, mont.mul(m, t3, x3), t1);
  z3 = mont.add(m, mont.mul(m, t4, z3), mont.mul(m, t3, t0));
  return Proj { x: x3, y: y3, z: z3 };
}

function __base_mul(s: Signer, k: [u8]): Proj {
  return __scalar_mul(s, s.g, k);
}

function __scalar_mul(s: Signer, point: Proj, k: [u8]): Proj {
  let table: Proj[] = [__identity(s), point];
  let i: i32 = 2;
  while (i < 16) {
    table = table.append(__padd(s, table[i - 1], point));
    i = i + 1;
  }
  let r: Proj = __identity(s);
  for b in k {
    r = __add_digit(s, __times16(s, r), table, b as i64 >> 4 as i64);
    r = __add_digit(s, __times16(s, r), table, b as i64 & 15 as i64);
  }
  return r;
}

function __times16(s: Signer, p: Proj): Proj {
  let r: Proj = __padd(s, p, p);
  r = __padd(s, r, r);
  r = __padd(s, r, r);
  return __padd(s, r, r);
}

function __add_digit(s: Signer, r: Proj, table: Proj[], digit: i64): Proj {
  let pick: Proj = table[0];
  let j: i32 = 1;
  while (j < 16) {
    let hit: i64 = mont.is_zero([digit ^ j as i64]);
    pick = Proj { x: mont.select(pick.x, table[j].x, hit), y: mont.select(pick.y, table[j].y, hit), z: mont.select(pick.z, table[j].z, hit) };
    j = j + 1;
  }
  return __padd(s, r, pick);
}

function __identity(s: Signer): Proj {
  return Proj { x: mont.zero(s.fp), y: s.fp.one, z: mont.zero(s.fp) };
}

function __affine(s: Signer, p: Proj): (u8[], u8[]) {
  let zinv: i64[] = mont.inv(s.fp, p.z);
  return (mont.to_bytes(s.fp, mont.mul(s.fp, p.x, zinv)), mont.to_bytes(s.fp, mont.mul(s.fp, p.y, zinv)));
}

function __der(r: [u8], s: [u8]): u8[] {
  let ri: u8[] = __der_integer(r);
  let si: u8[] = __der_integer(s);
  let out: u8[] = [48 as u8, (ri.len() + si.len()) as u8];
  for b in ri {
    out = out.append(b);
  }
  for b in si {
    out = out.append(b);
  }
  return out;
}
```

### `19 function __der_integer(v: [u8]): u8[]`

```fern
<<19 function __der_integer(v: [u8]): u8[]>>=
function __der_integer(v: [u8]): u8[] {
  let at: i32 = 0;
  while (at < v.len() - 1 && v[at] == 0 as u8) {
    at = at + 1;
  }
  let body: u8[] = [];
  if (v[at] as i32 >= 128) {
    body = body.append(0 as u8);
  }
  while (at < v.len()) {
    body = body.append(v[at]);
    at = at + 1;
  }
  let out: u8[] = [2 as u8, body.len() as u8];
  for b in body {
    out = out.append(b);
  }
  return out;
}
```

### `20 struct Drbg`

```fern
<<20 struct Drbg>>=
struct Drbg { hash: sha2.Hash, k: u8[], v: u8[] }
```

### `21 function __mac(h: sha2.Hash, key: [u8], msg: [u8]): u8[]`

```fern
<<21 function __mac(h: sha2.Hash, key: [u8], msg: [u8]): u8[]>>=
function __mac(h: sha2.Hash, key: [u8], msg: [u8]): u8[] {
  match (h) {
    sha2.Sha384 => {
      return crypto.hmac_sha384(key, msg);
    },
    _ => {
      return crypto.hmac_sha256(key, msg);
    }
  }
  return [];
}

function __cat3(a: [u8], tag: i32, b: [u8], c: [u8]): u8[] {
  let out: u8[] = __copy(a);
  if (tag >= 0) {
    out = out.append(tag as u8);
  }
  for x in b {
    out = out.append(x);
  }
  for x in c {
    out = out.append(x);
  }
  return out;
}

function __drbg_new(h: sha2.Hash, x: [u8], h1: [u8]): Drbg {
  let size: i32 = h.size();
  let v: u8[] = [];
  let k: u8[] = [];
  let i: i32 = 0;
  while (i < size) {
    v = v.append(1 as u8);
    k = k.append(0 as u8);
    i = i + 1;
  }
  k = __mac(h, k, __cat3(v, 0, x, h1));
  v = __mac(h, k, v);
  k = __mac(h, k, __cat3(v, 1, x, h1));
  v = __mac(h, k, v);
  return Drbg { hash: h, k: k, v: v };
}

function __drbg_next(g: Drbg, bytes: i32): (Drbg, u8[]) {
  let v: u8[] = g.v;
  let t: u8[] = [];
  while (t.len() < bytes) {
    v = __mac(g.hash, g.k, v);
    for x in v {
      if (t.len() < bytes) {
        t = t.append(x);
      }
    }
  }
  return (Drbg { ...g, v: v }, t);
}

function __drbg_reseed(g: Drbg): Drbg {
  let k: u8[] = __mac(g.hash, g.k, __cat3(g.v, 0, [], []));
  return Drbg { ...g, k: k, v: __mac(g.hash, k, g.v) };
}
```
