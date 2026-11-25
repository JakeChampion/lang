# ML-DSA-65 digital signatures

This module implements ML-DSA-65 under FIPS 204: key generation from a seed, hedged and deterministic signing, and verification.

## Model

A key pair has a public key of 1952 bytes and a private key of 4032 bytes. A signature has 3309 bytes. The 32-byte seed of key generation gives the complete key pair.

A hedged signature mixes 32 bytes of fresh randomness into the signature. A deterministic signature uses 32 zero bytes. Both verify with the same public key.

## Contracts

Each function takes borrowed byte views and returns owned bytes. A seed, a private key, a context, a randomness or a mu of the wrong length is a refusal with a `MlDsaError`. A context has at most 255 bytes.

`verify` returns false for a public key or a signature of the wrong length, and for a context of more than 255 bytes.

Key generation and signatures have no branch and no memory index that depends on a secret. The rejections that FIPS 204 allows to be public are the exception. `TestSelfHostCtGateX86_64` checks this under valgrind.

## Executable example

```fern test name=std-crypto-mldsa-example
import "std/crypto/mldsa";

function main(): i32 {
  let seed = __alloc_u8(32);
  let Ok(k) = mldsa.keygen_derand(seed) else { return 1; };
  let message = "attack at dawn".as_bytes();
  let Ok(sig) = mldsa.sign_deterministic(k.private_key, message, []) else { return 2; };
  if (!mldsa.verify(k.public_key, message, [], sig)) { return 3; }
  if (mldsa.verify(k.public_key, "attack at dusk".as_bytes(), [], sig)) { return 4; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<errors>>

<<key pair and sizes>>

<<parameters>>

<<arithmetic modulo q>>

<<number-theoretic transform>>

<<bit packing>>

<<sampling>>

<<decomposition and hints>>

<<key generation>>

<<signing>>

<<verification>>

<<public interface>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module implements ML-DSA-65 under FIPS 204.
```

### `dependencies`

```fern
<<dependencies>>=
import "std/crypto";
import "std/rand";
import "core/error";
import "core/cmp";
```

### `errors`

Each refusal names the input and the length or the range that the module takes.

```fern
<<errors>>=
pub enum MlDsaError {
  SeedLength(i32),
  PrivateKeyLength(i32),
  PrivateKeyRange,
  ContextLength(i32),
  RandomnessLength(i32),
  MuLength(i32),
}

pub function (e: MlDsaError) to_string(): string {
  return e.message();
}

impl error.Error for MlDsaError {}

impl cmp.Display for MlDsaError {}

pub function (e: MlDsaError) message(): string {
  match (e) {
    SeedLength(n) => {
      return "mldsa: the seed is " + n.to_string() + " bytes, want 32";
    },
    PrivateKeyLength(n) => {
      return "mldsa: the private key is " + n.to_string() + " bytes, want 4032";
    },
    PrivateKeyRange => {
      return "mldsa: the private key has a coefficient of s1 or s2 outside [-4, 4]";
    },
    ContextLength(n) => {
      return "mldsa: the context is " + n.to_string() + " bytes, want at most 255";
    },
    RandomnessLength(n) => {
      return "mldsa: the randomness is " + n.to_string() + " bytes, want 32";
    },
    MuLength(n) => {
      return "mldsa: mu is " + n.to_string() + " bytes, want 64";
    }
  }
  return "";
}
```

### `key pair and sizes`

A key pair is `@secret`, so its drop wipes the private key. The sizes are the ML-DSA-65 sizes of FIPS 204 table 2.

```fern
<<key pair and sizes>>=
@secret
pub struct KeyPair {
  public_key: u8[],
  private_key: u8[],
}

pub const SEED_SIZE: i32 = 32;

pub const PUBLIC_KEY_SIZE: i32 = 1952;

pub const PRIVATE_KEY_SIZE: i32 = 4032;

pub const SIGNATURE_SIZE: i32 = 3309;
```

### `parameters`

The parameters are those of ML-DSA-65 in FIPS 204 table 1. `HINTS_AT` is the offset of the hints in a signature: the 48 bytes of c~ and then 5 polynomials of z at 640 bytes each. The NTT uses the zetas in Montgomery form, so that one Montgomery reduction gives each product of a butterfly.

```fern
<<parameters>>=
const Q: i32 = 8380417;

const HALF_Q: i32 = 4190208;

// q^-1 mod 2^32.
const QINV: i32 = 58728449;

// 2^64 mod q.
const R2: i32 = 2365951;

// 256^-1 * 2^32 mod q.
const F_MONT: i32 = 16382;

const K: i32 = 6;

const L: i32 = 5;

const ETA: i32 = 4;

const TAU: i32 = 49;

const BETA: i32 = 196;

const GAMMA1: i32 = 524288;

const GAMMA2: i32 = 261888;

const OMEGA: i32 = 55;

const CTILDE: i32 = 48;

const HINTS_AT: i32 = 3248;

// 1753^BitRev8(k) * 2^32 mod q, for k from 0 to 255.
const ZETAS: i32[] = [
  4193792, 25847, 5771523, 7861508, 237124, 7602457, 7504169, 466468, 1826347, 2353451, 8021166, 6288512,
  3119733, 5495562, 3111497, 2680103, 2725464, 1024112, 7300517, 3585928, 7830929, 7260833, 2619752, 6271868,
  6262231, 4520680, 6980856, 5102745, 1757237, 8360995, 4010497, 280005, 2706023, 95776, 3077325, 3530437,
  6718724, 4788269, 5842901, 3915439, 4519302, 5336701, 3574422, 5512770, 3539968, 8079950, 2348700, 7841118,
  6681150, 6736599, 3505694, 4558682, 3507263, 6239768, 6779997, 3699596, 811944, 531354, 954230, 3881043,
  3900724, 5823537, 2071892, 5582638, 4450022, 6851714, 4702672, 5339162, 6927966, 3475950, 2176455, 6795196,
  7122806, 1939314, 4296819, 7380215, 5190273, 5223087, 4747489, 126922, 3412210, 7396998, 2147896, 2715295,
  5412772, 4686924, 7969390, 5903370, 7709315, 7151892, 8357436, 7072248, 7998430, 1349076, 1852771, 6949987,
  5037034, 264944, 508951, 3097992, 44288, 7280319, 904516, 3958618, 4656075, 8371839, 1653064, 5130689,
  2389356, 8169440, 759969, 7063561, 189548, 4827145, 3159746, 6529015, 5971092, 8202977, 1315589, 1341330,
  1285669, 6795489, 7567685, 6940675, 5361315, 4499357, 4751448, 3839961, 2091667, 3407706, 2316500, 3817976,
  5037939, 2244091, 5933984, 4817955, 266997, 2434439, 7144689, 3513181, 4860065, 4621053, 7183191, 5187039,
  900702, 1859098, 909542, 819034, 495491, 6767243, 8337157, 7857917, 7725090, 5257975, 2031748, 3207046,
  4823422, 7855319, 7611795, 4784579, 342297, 286988, 5942594, 4108315, 3437287, 5038140, 1735879, 203044,
  2842341, 2691481, 5790267, 1265009, 4055324, 1247620, 2486353, 1595974, 4613401, 1250494, 2635921, 4832145,
  5386378, 1869119, 1903435, 7329447, 7047359, 1237275, 5062207, 6950192, 7929317, 1312455, 3306115, 6417775,
  7100756, 1917081, 5834105, 7005614, 1500165, 777191, 2235880, 3406031, 7838005, 5548557, 6709241, 6533464,
  5796124, 4656147, 594136, 4603424, 6366809, 2432395, 2454455, 8215696, 1957272, 3369112, 185531, 7173032,
  5196991, 162844, 1616392, 3014001, 810149, 1652634, 4686184, 6581310, 5341501, 3523897, 3866901, 269760,
  2213111, 7404533, 1717735, 472078, 7953734, 1723600, 6577327, 1910376, 6712985, 7276084, 8119771, 4546524,
  5441381, 6144432, 7959518, 6094090, 183443, 7403526, 1612842, 4834730, 7826001, 3919660, 8332111, 7018208,
  3937738, 1400424, 7534263, 1976782,
];
```

### `arithmetic modulo q`

Each coefficient is in [0, q) between operations. The reductions have no branch and no division, because a coefficient of a secret polynomial must not change the time of an operation. `__public` makes a flag public for the constant-time gate. Only a flag that the result reveals anyway goes through it: a rejection of a candidate, or a refusal of a private key.

```fern
<<arithmetic modulo q>>=
function __csubq(x: i32): i32 {
  let r = x - Q;
  return r + (r >> 31 & Q);
}

function __canon(x: i32): i32 {
  return x + (x >> 31 & Q);
}

// a * 2^-32 mod q, in (-q, q), for |a| < 2^31 * q.
function __mont(a: i64): i32 {
  let t = a as i32 * QINV;
  return (a - t as i64 * (Q as i64) >> 32) as i32;
}

// x mod q, for 0 <= x < 2^54.
function __reduce(x: i64): i32 {
  return __canon(__mont(__canon(__mont(x)) as i64 * (R2 as i64)));
}

function __mul(a: i32, b: i32): i32 {
  return __reduce(a as i64 * (b as i64));
}

// z * a mod q, where z holds a constant times 2^32 mod q.
function __zmul(z: i32, a: i32): i32 {
  return __canon(__mont(z as i64 * (a as i64)));
}

function __centered(x: i32): i32 {
  return x - (HALF_Q - x >> 31 & Q);
}

// 1 when |c| >= bound, else 0.
function __abs_over(c: i32, bound: i32): i32 {
  let m = c >> 31;
  return bound - 1 - ((c ^ m) - m) >> 31 & 1;
}

function __over(x: i32, bound: i32): i32 {
  return __abs_over(__centered(x), bound);
}

// A flag that the caller can branch on. Only a flag that the output reveals
// anyway goes through here.
function __public(flag: i32): boolean {
  let b = __alloc_u8(1).with(0, (flag & 1) as u8);
  __ct_public(b);
  return b[0] != 0;
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

function __zero_bytes(n: i32): u8[] {
  let a = __alloc_u8(n);
  let i = 0;
  while (i < n) {
    a = a.with(i, 0 as u8);
    i += 1;
  }
  return a;
}

function __copy(f: [i32]): i32[] {
  let a = __alloc_i32(f.len());
  let i = 0;
  while (i < f.len()) {
    a = a.with(i, f[i]);
    i += 1;
  }
  return a;
}

function __add(own f: i32[], g: [i32]): i32[] {
  let i = 0;
  while (i < f.len()) {
    f = f.with(i, __csubq(f[i] + g[i]));
    i += 1;
  }
  return f;
}

function __sub(own f: i32[], g: [i32]): i32[] {
  let i = 0;
  while (i < f.len()) {
    f = f.with(i, __csubq(f[i] - g[i] + Q));
    i += 1;
  }
  return f;
}
```

### `number-theoretic transform`

Algorithms 41 and 42 of FIPS 204. A polynomial is 256 coefficients at an offset in a flat array. A vector of polynomials is a run of those blocks, and the matrix A is K rows of L blocks. `__mat_vec` adds the L products of a row in 64 bits and then reduces the sum one time.

```fern
<<number-theoretic transform>>=
function __ntt(own f: i32[], at: i32): i32[] {
  let m = 0;
  let len = 128;
  while (len >= 1) {
    let start = at;
    while (start < at + 256) {
      m += 1;
      let z = ZETAS[m];
      let j = start;
      while (j < start + len) {
        let t = __zmul(z, f[j + len]);
        let a = f[j];
        f = f.with(j + len, __csubq(a - t + Q));
        f = f.with(j, __csubq(a + t));
        j += 1;
      }
      start += 2 * len;
    }
    len >>= 1;
  }
  return f;
}

function __inv_ntt(own f: i32[], at: i32): i32[] {
  let m = 256;
  let len = 1;
  while (len < 256) {
    let start = at;
    while (start < at + 256) {
      m -= 1;
      let z = Q - ZETAS[m];
      let j = start;
      while (j < start + len) {
        let t = f[j];
        let u = f[j + len];
        f = f.with(j, __csubq(t + u));
        f = f.with(j + len, __zmul(z, __csubq(t - u + Q)));
        j += 1;
      }
      start += 2 * len;
    }
    len <<= 1;
  }
  let i = at;
  while (i < at + 256) {
    f = f.with(i, __zmul(F_MONT, f[i]));
    i += 1;
  }
  return f;
}

function __ntt_all(own v: i32[], count: i32): i32[] {
  let i = 0;
  while (i < count) {
    v = __ntt(v, 256 * i);
    i += 1;
  }
  return v;
}

function __inv_ntt_all(own v: i32[], count: i32): i32[] {
  let i = 0;
  while (i < count) {
    v = __inv_ntt(v, 256 * i);
    i += 1;
  }
  return v;
}

// The product of the K by L matrix a and the vector v, in the NTT domain.
function __mat_vec(a: [i32], v: [i32]): i32[] {
  let out = __alloc_i32(256 * K);
  let i = 0;
  while (i < K) {
    let n = 0;
    while (n < 256) {
      let acc: i64 = 0;
      let j = 0;
      while (j < L) {
        acc += a[256 * (L * i + j) + n] as i64 * (v[256 * j + n] as i64);
        j += 1;
      }
      out = out.with(256 * i + n, __reduce(acc));
      n += 1;
    }
    i += 1;
  }
  return out;
}

// The product of the polynomial c and each polynomial of v, in the NTT domain.
function __scale(c: [i32], v: [i32]): i32[] {
  let out = __alloc_i32(v.len());
  let i = 0;
  while (i < v.len()) {
    out = out.with(i, __mul(c[i & 255], v[i]));
    i += 1;
  }
  return out;
}
```

### `bit packing`

SimpleBitPack and BitPack of FIPS 204 pack each coefficient into d bits, least significant bit first. BitPack(w, a, b) packs b - w, which `__offset` computes from the centered coefficients.

```fern
<<bit packing>>=
function __encode(own out: u8[], at: i32, f: [i32], d: i32): u8[] {
  let acc: i64 = 0;
  let bits = 0;
  let pos = at;
  let i = 0;
  while (i < f.len()) {
    acc |= f[i] as i64 << bits;
    bits += d;
    while (bits >= 8) {
      out = out.with(pos, (acc & 255) as u8);
      acc >>= 8;
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
  let acc: i64 = 0;
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

// The coefficients b - c of each centered c of f.
function __offset(f: [i32], b: i32): i32[] {
  let out = __alloc_i32(f.len());
  let i = 0;
  while (i < f.len()) {
    out = out.with(i, b - __centered(f[i]));
    i += 1;
  }
  return out;
}

// The coefficients b - x of each x of f, as canonical values.
function __unoffset(own f: i32[], b: i32): i32[] {
  let i = 0;
  while (i < f.len()) {
    f = f.with(i, __canon(b - f[i]));
    i += 1;
  }
  return f;
}
```

### `sampling`

ExpandA, ExpandS, ExpandMask and SampleInBall of FIPS 204. ExpandS rejects a candidate from the secret seed. The positions of the candidates that it keeps are public, because the values of those candidates do not depend on the rejections.

```fern
<<sampling>>=
function __seed_with(seed: [u8], n: i32, a: i32, b: i32): u8[] {
  return __bytes_into(__alloc_u8(n + 2), 0, seed, 0, n).with(n, a as u8).with(n + 1, b as u8);
}

function __rej_ntt_poly(own a: i32[], at: i32, rho: [u8], s: i32, r: i32): i32[] {
  let x = crypto.shake128_new().update(__seed_with(rho, 32, s, r)).reader();
  let n = 0;
  while (n < 256) {
    let next = x.squeeze(168);
    let b = next.0;
    x = next.1;
    let k = 0;
    while (k < 168 && n < 256) {
      let z = b[k] as i32 | b[k + 1] as i32 << 8 | (b[k + 2] as i32 & 127) << 16;
      if (z < Q) {
        a = a.with(at + n, z);
        n += 1;
      }
      k += 3;
    }
  }
  return a;
}

// The matrix A of FIPS 204 ExpandA, in the NTT domain, row by row.
function __expand_a(rho: [u8]): i32[] {
  let a = __alloc_i32(256 * K * L);
  let r = 0;
  while (r < K) {
    let s = 0;
    while (s < L) {
      a = __rej_ntt_poly(a, 256 * (L * r + s), rho, s, r);
      s += 1;
    }
    r += 1;
  }
  return a;
}

// Polynomial r of FIPS 204 ExpandS, with coefficients in [-ETA, ETA]. The
// choice of each candidate depends on the secret seed. It is public, because
// the coefficients that it keeps do not depend on it.
function __rej_bounded_poly(own a: i32[], at: i32, rhop: [u8], r: i32): i32[] {
  let x = crypto.shake256_new().update(__seed_with(rhop, 64, r & 255, r >> 8)).reader();
  let n = 0;
  while (n < 256) {
    let next = x.squeeze(136);
    let b = next.0;
    x = next.1;
    let keep = __alloc_u8(272);
    let k = 0;
    while (k < 136) {
      keep = keep.with(2 * k, ((b[k] as i32 & 15) - 9 >> 31 & 1) as u8);
      keep = keep.with(2 * k + 1, ((b[k] as i32 >> 4) - 9 >> 31 & 1) as u8);
      k += 1;
    }
    __ct_public(keep);
    k = 0;
    while (k < 272 && n < 256) {
      if (keep[k] == 1) {
        let z = b[k >> 1] as i32 >> 4 * (k & 1) & 15;
        a = a.with(at + n, __csubq(ETA - z + Q));
        n += 1;
      }
      k += 1;
    }
    crypto.erase(b);
  }
  return a;
}

function __expand_s(rhop: [u8], first: i32, count: i32): i32[] {
  let s = __alloc_i32(256 * count);
  let r = 0;
  while (r < count) {
    s = __rej_bounded_poly(s, 256 * r, rhop, first + r);
    r += 1;
  }
  return s;
}

function __expand_mask(rhopp: [u8], kappa: i32): i32[] {
  let y = __alloc_i32(256 * L);
  let r = 0;
  while (r < L) {
    let v = kappa + r;
    let b = crypto.shake256_new().update(__seed_with(rhopp, 64, v & 255, v >> 8 & 255)).reader().squeeze(640).0;
    let c = __decode(b, 0, 256, 20);
    let i = 0;
    while (i < 256) {
      y = y.with(256 * r + i, __csubq(GAMMA1 - c[i] + Q));
      i += 1;
    }
    crypto.erase(b);
    crypto.erase(c);
    r += 1;
  }
  return y;
}

// The challenge polynomial of FIPS 204 SampleInBall. ctilde is public.
function __sample_in_ball(ctilde: [u8]): i32[] {
  let c = __zeros(256);
  let x = crypto.shake256_new().update(ctilde).reader();
  let next = x.squeeze(136);
  let b = next.0;
  x = next.1;
  let signs = __bytes_into(__alloc_u8(8), 0, b, 0, 8);
  let pos = 8;
  let i = 256 - TAU;
  while (i < 256) {
    let j = 256;
    while (j > i) {
      if (pos == 136) {
        next = x.squeeze(136);
        b = next.0;
        x = next.1;
        pos = 0;
      }
      j = b[pos] as i32;
      pos += 1;
    }
    c = c.with(i, c[j]);
    let bit = i + TAU - 256;
    c = c.with(j, 1 + (signs[bit >> 3] as i32 >> (bit & 7) & 1) * (Q - 2));
    i += 1;
  }
  return c;
}
```

### `decomposition and hints`

`__high` and `__low` give r1 and r0 of Decompose for GAMMA2 = (q - 1) / 32 with no branch. A test of each value of [0, q) against the definition of FIPS 204 gave the same result. The hint functions are HintBitPack and HintBitUnpack. The unpack refuses an index that is not above the index before it, a count above OMEGA, and a nonzero byte after the last index.

```fern
<<decomposition and hints>>=
// r1 of FIPS 204 Decompose, for a in [0, q).
function __high(a: i32): i32 {
  let a1 = a + 127 >> 7;
  a1 = a1 * 1025 + (1 << 21) >> 22;
  return a1 & 15;
}

// r0 of FIPS 204 Decompose, in (-GAMMA2, GAMMA2].
function __low(a: i32): i32 {
  let a0 = a - __high(a) * 2 * GAMMA2;
  return a0 - (HALF_Q - a0 >> 31 & Q);
}

function __use_hint(h: i32, r: i32): i32 {
  let r1 = __high(r);
  if (h == 0) {
    return r1;
  }
  if (__low(r) > 0) {
    return r1 + 1 & 15;
  }
  return r1 - 1 & 15;
}

function __w1_encode(w: [i32]): u8[] {
  let w1 = __alloc_i32(w.len());
  let i = 0;
  while (i < w.len()) {
    w1 = w1.with(i, __high(w[i]));
    i += 1;
  }
  return __encode(__alloc_u8(128 * K), 0, w1, 4);
}

function __challenge(mu: [u8], w1: [u8]): u8[] {
  return crypto.shake256_new().update(mu).update(w1).reader().squeeze(CTILDE).0;
}

function __pack_hints(own out: u8[], h: [u8]): u8[] {
  let i = 0;
  while (i < OMEGA + K) {
    out = out.with(HINTS_AT + i, 0 as u8);
    i += 1;
  }
  let index = 0;
  i = 0;
  while (i < K) {
    let j = 0;
    while (j < 256) {
      if (h[256 * i + j] != 0) {
        out = out.with(HINTS_AT + index, j as u8);
        index += 1;
      }
      j += 1;
    }
    out = out.with(HINTS_AT + OMEGA + i, index as u8);
    i += 1;
  }
  return out;
}

// The hints of FIPS 204 HintBitUnpack, and false for a malformed encoding.
function __unpack_hints(sig: [u8]): (i32[], boolean) {
  let h = __zeros(256 * K);
  let index = 0;
  let i = 0;
  while (i < K) {
    let end = sig[HINTS_AT + OMEGA + i] as i32;
    if (end < index || end > OMEGA) {
      return (h, false);
    }
    let first = index;
    while (index < end) {
      if (index > first && sig[HINTS_AT + index - 1] >= sig[HINTS_AT + index]) {
        return (h, false);
      }
      h = h.with(256 * i + sig[HINTS_AT + index] as i32, 1);
      index += 1;
    }
    i += 1;
  }
  while (index < OMEGA) {
    if (sig[HINTS_AT + index] != 0) {
      return (h, false);
    }
    index += 1;
  }
  return (h, true);
}
```

### `key generation`

ML-DSA.KeyGen_internal, algorithm 6 of FIPS 204.

```fern
<<key generation>>=
function __keygen(xi: [u8]): KeyPair {
  let hs = crypto.shake256_new().update(xi).update([K as u8, L as u8]).reader().squeeze(128).0;
  let rho = __bytes_into(__alloc_u8(32), 0, hs, 0, 32);
  __ct_public(rho);
  let rhop = __bytes_into(__alloc_u8(64), 0, hs, 32, 64);
  let a = __expand_a(rho);
  let s1 = __expand_s(rhop, 0, L);
  let s2 = __expand_s(rhop, L, K);
  let packed_s1 = __offset(s1, ETA);
  let packed_s2 = __offset(s2, ETA);
  let t = __add(__inv_ntt_all(__mat_vec(a, __ntt_all(s1, L)), K), s2);
  let t1 = __alloc_i32(256 * K);
  let t0 = __alloc_i32(256 * K);
  let i = 0;
  while (i < 256 * K) {
    let hi = t[i] + 4095 >> 13;
    t1 = t1.with(i, hi);
    t0 = t0.with(i, 4096 - (t[i] - (hi << 13)));
    i += 1;
  }
  let pk = __bytes_into(__encode(__alloc_u8(PUBLIC_KEY_SIZE), 32, t1, 10), 0, rho, 0, 32);
  let tr = crypto.shake256_new().update(pk).reader().squeeze(64).0;
  let sk = __bytes_into(__alloc_u8(PRIVATE_KEY_SIZE), 0, rho, 0, 32);
  sk = __bytes_into(sk, 32, hs, 96, 32);
  sk = __bytes_into(sk, 64, tr, 0, 64);
  sk = __encode(sk, 128, packed_s1, 4);
  sk = __encode(sk, 768, packed_s2, 4);
  sk = __encode(sk, 1536, t0, 13);
  crypto.erase(hs);
  crypto.erase(rhop);
  crypto.erase(s2);
  crypto.erase(packed_s1);
  crypto.erase(packed_s2);
  crypto.erase(t);
  crypto.erase(t0);
  return KeyPair { public_key: pk, private_key: sk };
}
```

### `signing`

ML-DSA.Sign_internal, algorithm 7 of FIPS 204. A private key with a coefficient of s1 or s2 above 2 * ETA in its packed form is refused, as Wycheproof expects. The decision to reject a candidate signature is public. The signature that the loop returns is public.

```fern
<<signing>>=
// 1 when a 4-bit coefficient of s1 or s2 in sk is above 2 * ETA.
function __eta_out_of_range(sk: [u8]): i32 {
  let bad = 0;
  let i = 128;
  while (i < 1536) {
    let b = sk[i] as i32;
    bad |= 2 * ETA - (b & 15) >> 31 & 1 | 2 * ETA - (b >> 4) >> 31 & 1;
    i += 1;
  }
  return bad;
}

function __sign(sk: [u8], mu: [u8], rnd: [u8]): Result[u8[], MlDsaError] {
  if (sk.len() != PRIVATE_KEY_SIZE) {
    return Err(PrivateKeyLength(sk.len()));
  }
  if (rnd.len() != 32) {
    return Err(RandomnessLength(rnd.len()));
  }
  if (__public(__eta_out_of_range(sk))) {
    return Err(PrivateKeyRange);
  }
  let rho = __bytes_into(__alloc_u8(32), 0, sk, 0, 32);
  __ct_public(rho);
  let a = __expand_a(rho);
  let s1h = __ntt_all(__unoffset(__decode(sk, 128, 256 * L, 4), ETA), L);
  let s2h = __ntt_all(__unoffset(__decode(sk, 768, 256 * K, 4), ETA), K);
  let t0h = __ntt_all(__unoffset(__decode(sk, 1536, 256 * K, 13), 4096), K);
  let rhopp = crypto.shake256_new().update(sk[32:64]).update(rnd).update(mu).reader().squeeze(64).0;
  let kappa = 0;
  while (true) {
    let y = __expand_mask(rhopp, kappa);
    kappa += L;
    let w = __inv_ntt_all(__mat_vec(a, __ntt_all(__copy(y), L)), K);
    let ctilde = __challenge(mu, __w1_encode(w));
    __ct_public(ctilde);
    let ch = __ntt(__sample_in_ball(ctilde), 0);
    let z = __add(y, __inv_ntt_all(__scale(ch, s1h), L));
    let r = __sub(w, __inv_ntt_all(__scale(ch, s2h), K));
    let bad = 0;
    let i = 0;
    while (i < 256 * L) {
      bad |= __over(z[i], GAMMA1 - BETA);
      i += 1;
    }
    i = 0;
    while (i < 256 * K) {
      bad |= __abs_over(__low(r[i]), GAMMA2 - BETA);
      i += 1;
    }
    if (!__public(bad)) {
      let ct0 = __inv_ntt_all(__scale(ch, t0h), K);
      let h = __alloc_u8(256 * K);
      let count = 0;
      i = 0;
      while (i < 256 * K) {
        bad |= __over(ct0[i], GAMMA2);
        let hint = -(__high(r[i]) ^ __high(__csubq(r[i] + ct0[i]))) >> 31 & 1;
        h = h.with(i, hint as u8);
        count += hint;
        i += 1;
      }
      bad |= OMEGA - count >> 31 & 1;
      if (!__public(bad)) {
        __ct_public(h);
        let sig = __bytes_into(__alloc_u8(SIGNATURE_SIZE), 0, ctilde, 0, CTILDE);
        sig = __pack_hints(__encode(sig, CTILDE, __offset(z, GAMMA1), 20), h);
        __ct_public(sig);
        crypto.erase(z);
        crypto.erase(r);
        crypto.erase(ct0);
        crypto.erase(s1h);
        crypto.erase(s2h);
        crypto.erase(t0h);
        crypto.erase(rhopp);
        return Ok(sig);
      }
      crypto.erase(ct0);
    }
    crypto.erase(z);
    crypto.erase(r);
  }
  return Ok([]);
}
```

### `verification`

ML-DSA.Verify_internal, algorithm 8 of FIPS 204. All the inputs are public.

```fern
<<verification>>=
function __verify(pk: [u8], mu: [u8], sig: [u8]): boolean {
  if (pk.len() != PUBLIC_KEY_SIZE || sig.len() != SIGNATURE_SIZE || mu.len() != 64) {
    return false;
  }
  let (h, well_formed) = __unpack_hints(sig);
  if (!well_formed) {
    return false;
  }
  let z = __decode(sig, CTILDE, 256 * L, 20);
  let i = 0;
  while (i < 256 * L) {
    let c = GAMMA1 - z[i];
    if (__abs_over(c, GAMMA1 - BETA) == 1) {
      return false;
    }
    z = z.with(i, __canon(c));
    i += 1;
  }
  let t1 = __decode(pk, 32, 256 * K, 10);
  i = 0;
  while (i < 256 * K) {
    t1 = t1.with(i, t1[i] << 13);
    i += 1;
  }
  let ctilde = sig[0:CTILDE];
  let ch = __ntt(__sample_in_ball(ctilde), 0);
  let az = __mat_vec(__expand_a(pk[0:32]), __ntt_all(z, L));
  let w = __inv_ntt_all(__sub(az, __scale(ch, __ntt_all(t1, K))), K);
  i = 0;
  while (i < 256 * K) {
    w = w.with(i, __use_hint(h[i], w[i]));
    i += 1;
  }
  let again = __challenge(mu, __encode(__alloc_u8(128 * K), 0, w, 4));
  let diff = 0;
  i = 0;
  while (i < CTILDE) {
    diff |= (ctilde[i] ^ again[i]) as i32;
    i += 1;
  }
  return diff == 0;
}
```

### `public interface`

`sign` and `verify` take the pure ML-DSA message and context of algorithms 2 and 3. The `_internal` and `_mu` forms take a formatted message or a mu that the caller computed, as the ACVP tests do.

```fern
<<public interface>>=
// mu = H(tr || M') of FIPS 204, with M' = 0 || len(context) || context || message.
function __mu(tr: [u8], message: [u8], context: [u8]): Result[u8[], MlDsaError] {
  if (context.len() > 255) {
    return Err(ContextLength(context.len()));
  }
  return Ok(crypto.shake256_new().update(tr).update([0 as u8, context.len() as u8]).update(context).update(message).reader().squeeze(64).0);
}

function __tr(public_key: [u8]): u8[] {
  return crypto.shake256_new().update(public_key).reader().squeeze(64).0;
}

function __tr_of_private(private_key: [u8]): Result[u8[], MlDsaError] {
  if (private_key.len() != PRIVATE_KEY_SIZE) {
    return Err(PrivateKeyLength(private_key.len()));
  }
  return Ok(__bytes_into(__alloc_u8(64), 0, private_key, 64, 64));
}

// ML-DSA.KeyGen_internal of FIPS 204 for the 32-byte seed xi.
pub function keygen_derand(seed: [u8]): Result[KeyPair, MlDsaError] {
  if (seed.len() != SEED_SIZE) {
    return Err(SeedLength(seed.len()));
  }
  return Ok(__keygen(seed));
}

// A key pair from a seed of the operating system's entropy source.
pub function keygen(): KeyPair {
  let seed = rand.bytes(SEED_SIZE);
  let k = __keygen(seed);
  crypto.erase(seed);
  return k;
}

// ML-DSA.Sign of FIPS 204: the hedged signature, with 32 bytes of randomness
// from the operating system's entropy source.
pub function sign(private_key: [u8], message: [u8], context: [u8]): Result[u8[], MlDsaError] {
  let rnd = rand.bytes(32);
  let sig = sign_derand(private_key, message, context, rnd);
  crypto.erase(rnd);
  return sig;
}

// The deterministic variant of ML-DSA.Sign, with rnd of 32 zero bytes.
pub function sign_deterministic(private_key: [u8], message: [u8], context: [u8]): Result[u8[], MlDsaError] {
  return sign_derand(private_key, message, context, __zero_bytes(32));
}

// ML-DSA.Sign of FIPS 204 with the caller's 32 bytes of randomness rnd.
pub function sign_derand(private_key: [u8], message: [u8], context: [u8], rnd: [u8]): Result[u8[], MlDsaError] {
  let tr = __tr_of_private(private_key)?;
  return __sign(private_key, __mu(tr, message, context)?, rnd);
}

// ML-DSA.Sign_internal of FIPS 204, for the formatted message m' (message_prime).
pub function sign_internal(private_key: [u8], message_prime: [u8], rnd: [u8]): Result[u8[], MlDsaError] {
  let tr = __tr_of_private(private_key)?;
  return __sign(private_key, crypto.shake256_new().update(tr).update(message_prime).reader().squeeze(64).0, rnd);
}

// ML-DSA.Sign_internal of FIPS 204 for a 64-byte mu that the caller computed,
// for example with external_mu.
pub function sign_mu(private_key: [u8], mu: [u8], rnd: [u8]): Result[u8[], MlDsaError] {
  if (mu.len() != 64) {
    return Err(MuLength(mu.len()));
  }
  return __sign(private_key, mu, rnd);
}

// The 64-byte mu of FIPS 204 for the message and the context under public_key.
pub function external_mu(public_key: [u8], message: [u8], context: [u8]): Result[u8[], MlDsaError] {
  return __mu(__tr(public_key), message, context);
}

// ML-DSA.Verify of FIPS 204. A key, a signature or a context of the wrong
// length gives false.
pub function verify(public_key: [u8], message: [u8], context: [u8], signature: [u8]): boolean {
  if (public_key.len() != PUBLIC_KEY_SIZE) {
    return false;
  }
  let Ok(mu) = __mu(__tr(public_key), message, context) else {
    return false;
  };
  return __verify(public_key, mu, signature);
}

// ML-DSA.Verify_internal of FIPS 204, for the formatted message m'.
pub function verify_internal(public_key: [u8], message_prime: [u8], signature: [u8]): boolean {
  if (public_key.len() != PUBLIC_KEY_SIZE) {
    return false;
  }
  return __verify(public_key, crypto.shake256_new().update(__tr(public_key)).update(message_prime).reader().squeeze(64).0, signature);
}

// ML-DSA.Verify_internal of FIPS 204 for a 64-byte mu.
pub function verify_mu(public_key: [u8], mu: [u8], signature: [u8]): boolean {
  return __verify(public_key, mu, signature);
}
```
