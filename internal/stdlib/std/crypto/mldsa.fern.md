# ML-DSA-65 signatures

This module implements ML-DSA-65 under FIPS 204.

## Model

Key generation makes a key pair from a 32-byte seed. The public key is 1952 bytes and the private key is 4032 bytes. A signature is 3309 bytes.

`sign` adds 32 random bytes to each signature, as the hedged variant of FIPS 204 does. `sign_deterministic` uses 32 zero bytes, so the same key, message and context always give the same signature. `sign_derand` takes the 32 bytes from the caller.

Each message has a context string of at most 255 bytes. The verifier must give the same context as the signer. An empty context is permitted.

`sign_mu_derand` and `verify_mu` take the 64-byte message representative mu of FIPS 204 section 6.2. A caller can then compute mu in a different process from the private key.

## Constant time

Signing runs its rejection loop until a candidate passes. The count of tries depends on secret data, as FIPS 204 permits. The branches of the loop are on flags that are marked public: the result of a rejection test and the choice of a sampler. The challenge of each try is also marked public, as in the reference implementation. No other branch or memory index depends on a secret.

## Module assembly

The root chunk keeps the order of the declarations.

```fern
<<*>>=
<<module introduction>>

<<dependencies>>

<<errors>>

<<parameters>>

<<field arithmetic>>

<<sampling>>

<<rounding>>

<<encoding>>

<<key generation>>

<<signing>>

<<verification>>
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

An entry point refuses input of the wrong length before it does any work. A private key whose s1 or s2 has a coefficient out of the range of eta is also refused.

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
  return match (e) {
    SeedLength(n) => "mldsa: the seed is " + n.to_string() + " bytes, want 32",
    PrivateKeyLength(n) => "mldsa: the private key is " + n.to_string() + " bytes, want 4032",
    PrivateKeyRange => "mldsa: the private key has a coefficient of s1 or s2 out of range",
    ContextLength(n) => "mldsa: the context is " + n.to_string() + " bytes, want at most 255",
    RandomnessLength(n) => "mldsa: the randomness is " + n.to_string() + " bytes, want 32",
    MuLength(n) => "mldsa: mu is " + n.to_string() + " bytes, want 64",
  };
}

@secret
pub struct KeyPair {
  public_key: u8[],
  private_key: u8[],
}
```

### `parameters`

These are the parameters of ML-DSA-65 in FIPS 204 table 1. A vector of polynomials is one flat array of 256 coefficients for each polynomial. The matrix A has K rows and L columns, in row order.

`ZETAS` holds the powers of the root of unity 1753 in bit-reversed order, each multiplied by 2^32 modulo q.

```fern
<<parameters>>=
const Q: i32 = 8380417;

const QINV: i32 = 58728449;

const K: i32 = 6;

const L: i32 = 5;

const ETA: i32 = 4;

const TAU: i32 = 49;

const BETA: i32 = 196;

const GAMMA1: i32 = 524288;

const GAMMA2: i32 = 261888;

const OMEGA: i32 = 55;

const CT_BYTES: i32 = 48;

const PUBLIC_KEY_BYTES: i32 = 1952;

const PRIVATE_KEY_BYTES: i32 = 4032;

const SIGNATURE_BYTES: i32 = 3309;

const ZETAS: i32[] = [
  0, 25847, 5771523, 7861508, 237124, 7602457, 7504169, 466468, 1826347, 2353451, 8021166, 6288512,
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

### `field arithmetic`

Each coefficient is kept in the range [0, q). The Montgomery reduction gives a product times 2^-32. The forward transform uses the zetas in Montgomery form, so its products are exact. A pointwise product has the factor 2^-32, and the inverse transform multiplies by 2^64 / 256 at the end to remove it.

```fern
<<field arithmetic>>=
function __mont(a: i64): i32 {
  let t = a as i32 * QINV;
  return (a - t as i64 * (Q as i64) >> 32) as i32;
}

function __norm(x: i32): i32 {
  return x + (x >> 31 & Q);
}

function __csub(x: i32): i32 {
  return __norm(x - Q);
}

function __fqmul(a: i32, b: i32): i32 {
  return __norm(__mont(a as i64 * (b as i64)));
}

function __centered(x: i32): i32 {
  return x - ((Q - 1) / 2 - x >> 31 & Q);
}

function __abs(x: i32): i32 {
  let s = x >> 31;
  return (x ^ s) - s;
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

function __ntt(own f: i32[], at: i32): i32[] {
  let k = 0;
  let len = 128;
  while (len > 0) {
    let start = 0;
    while (start < 256) {
      k += 1;
      let zeta = ZETAS[k];
      let j = start;
      while (j < start + len) {
        let t = __fqmul(zeta, f[at + j + len]);
        let a = f[at + j];
        f = f.with(at + j + len, __csub(a - t + Q));
        f = f.with(at + j, __csub(a + t));
        j += 1;
      }
      start = j + len;
    }
    len >>= 1;
  }
  return f;
}

function __inv_ntt(own f: i32[], at: i32): i32[] {
  let k = 256;
  let len = 1;
  while (len < 256) {
    let start = 0;
    while (start < 256) {
      k -= 1;
      let zeta = Q - ZETAS[k];
      let j = start;
      while (j < start + len) {
        let t = f[at + j];
        let u = f[at + j + len];
        f = f.with(at + j, __csub(t + u));
        f = f.with(at + j + len, __fqmul(zeta, __csub(t - u + Q)));
        j += 1;
      }
      start = j + len;
    }
    len <<= 1;
  }
  let i = 0;
  while (i < 256) {
    f = f.with(at + i, __fqmul(41978, f[at + i]));
    i += 1;
  }
  return f;
}

function __ntt_all(own f: i32[], count: i32): i32[] {
  let i = 0;
  while (i < count) {
    f = __ntt(f, 256 * i);
    i += 1;
  }
  return f;
}

function __inv_ntt_all(own f: i32[], count: i32): i32[] {
  let i = 0;
  while (i < count) {
    f = __inv_ntt(f, 256 * i);
    i += 1;
  }
  return f;
}

// acc[at..] += a[ai..] * b[bi..], one polynomial in the NTT domain.
function __mul_acc(own acc: i32[], at: i32, a: [i32], ai: i32, b: [i32], bi: i32): i32[] {
  let i = 0;
  while (i < 256) {
    acc = acc.with(at + i, __csub(acc[at + i] + __fqmul(a[ai + i], b[bi + i])));
    i += 1;
  }
  return acc;
}

// A times v, for v in the NTT domain. The result is in the normal domain.
function __mat_vec(a: [i32], v: [i32]): i32[] {
  let w = __zeros(256 * K);
  let r = 0;
  while (r < K) {
    let s = 0;
    while (s < L) {
      w = __mul_acc(w, 256 * r, a, 256 * (L * r + s), v, 256 * s);
      s += 1;
    }
    r += 1;
  }
  return __inv_ntt_all(w, K);
}

// The challenge c times each polynomial of v, for c and v in the NTT domain.
// The result is in the normal domain.
function __scale(c: [i32], v: [i32], count: i32): i32[] {
  let w = __zeros(256 * count);
  let i = 0;
  while (i < count) {
    w = __mul_acc(w, 256 * i, c, 0, v, 256 * i);
    i += 1;
  }
  return __inv_ntt_all(w, count);
}

function __add(own f: i32[], g: [i32]): i32[] {
  let i = 0;
  while (i < f.len()) {
    f = f.with(i, __csub(f[i] + g[i]));
    i += 1;
  }
  return f;
}

function __sub(own f: i32[], g: [i32]): i32[] {
  let i = 0;
  while (i < f.len()) {
    f = f.with(i, __csub(f[i] - g[i] + Q));
    i += 1;
  }
  return f;
}

// 1 when a coefficient of f, as a centered value, is not below bound.
function __exceeds(f: [i32], bound: i32): i32 {
  let bad = 0;
  let i = 0;
  while (i < f.len()) {
    bad |= bound - 1 - __abs(__centered(f[i])) >> 31 & 1;
    i += 1;
  }
  return bad;
}

// The bit b of a flag, as a value that the program can branch on.
function __public(b: i32): boolean {
  let flag = __alloc_u8(1).with(0, b as u8);
  __ct_public(flag);
  return flag[0] != 0;
}
```

### `sampling`

ExpandA, ExpandS, ExpandMask and SampleInBall of FIPS 204 section 7.3. The rejection sampler of ExpandS accepts a half byte below 9. It first computes the flags of a whole block and marks only the flags public.

```fern
<<sampling>>=
function __rej_ntt(own a: i32[], at: i32, rho: [u8], s: i32, r: i32): i32[] {
  let seed = __bytes_into(__alloc_u8(34), 0, rho, 0, 32).with(32, s as u8).with(33, r as u8);
  let x = crypto.shake128_new().update(seed).reader();
  let n = 0;
  while (n < 256) {
    let next = x.squeeze(168);
    let b = next.0;
    x = next.1;
    let i = 0;
    while (i < 168 && n < 256) {
      let v = b[i] as i32 | b[i + 1] as i32 << 8 | (b[i + 2] as i32 & 127) << 16;
      if (v < Q) {
        a = a.with(at + n, v);
        n += 1;
      }
      i += 3;
    }
  }
  return a;
}

function __expand_a(rho: [u8]): i32[] {
  let a = __alloc_i32(256 * K * L);
  let r = 0;
  while (r < K) {
    let s = 0;
    while (s < L) {
      a = __rej_ntt(a, 256 * (L * r + s), rho, s, r);
      s += 1;
    }
    r += 1;
  }
  return a;
}

function __rej_eta(own a: i32[], at: i32, seed: [u8], nonce: i32): i32[] {
  let input = __bytes_into(__alloc_u8(66), 0, seed, 0, 64).with(64, nonce as u8).with(65, (nonce >> 8) as u8);
  let x = crypto.shake256_new().update(input).reader();
  let n = 0;
  while (n < 256) {
    let next = x.squeeze(136);
    let b = next.0;
    x = next.1;
    let ok = __alloc_u8(272);
    let i = 0;
    while (i < 136) {
      ok = ok.with(2 * i, ((b[i] as i32 & 15) - 9 >> 31 & 1) as u8);
      ok = ok.with(2 * i + 1, ((b[i] as i32 >> 4) - 9 >> 31 & 1) as u8);
      i += 1;
    }
    __ct_public(ok);
    i = 0;
    while (i < 272 && n < 256) {
      if (ok[i] != 0) {
        a = a.with(at + n, __norm(ETA - (b[i >> 1] as i32 >> 4 * (i & 1) & 15)));
        n += 1;
      }
      i += 1;
    }
  }
  return a;
}

function __expand_s(rho: [u8]): (i32[], i32[]) {
  let s1 = __alloc_i32(256 * L);
  let s2 = __alloc_i32(256 * K);
  let r = 0;
  while (r < L) {
    s1 = __rej_eta(s1, 256 * r, rho, r);
    r += 1;
  }
  r = 0;
  while (r < K) {
    s2 = __rej_eta(s2, 256 * r, rho, L + r);
    r += 1;
  }
  return (s1, s2);
}

function __expand_mask(rho: [u8], kappa: i32): i32[] {
  let y = __alloc_i32(256 * L);
  let r = 0;
  while (r < L) {
    let n = kappa + r;
    let input = __bytes_into(__alloc_u8(66), 0, rho, 0, 64).with(64, n as u8).with(65, (n >> 8) as u8);
    let v = crypto.shake256_new().update(input).reader().squeeze(640).0;
    let f = __unpack(v, 0, 256, 20);
    let i = 0;
    while (i < 256) {
      y = y.with(256 * r + i, __norm(GAMMA1 - f[i]));
      i += 1;
    }
    r += 1;
  }
  return y;
}

function __sample_in_ball(ct: [u8]): i32[] {
  let c = __zeros(256);
  let x = crypto.shake256_new().update(ct).reader();
  let first = x.squeeze(136);
  let b = first.0;
  x = first.1;
  let signs: i64 = 0;
  let t = 0;
  while (t < 8) {
    signs |= b[t] as i64 << 8 * t;
    t += 1;
  }
  let pos = 8;
  let i = 256 - TAU;
  while (i < 256) {
    let j = 256;
    while (j > i) {
      if (pos == 136) {
        let next = x.squeeze(136);
        b = next.0;
        x = next.1;
        pos = 0;
      }
      j = b[pos] as i32;
      pos += 1;
    }
    let bit = (signs >> i + TAU - 256 & 1) as i32;
    c = c.with(i, c[j]);
    c = c.with(j, 1 + bit * (Q - 2));
    i += 1;
  }
  return c;
}
```

### `rounding`

Power2Round, Decompose, MakeHint and UseHint of FIPS 204 section 7.4, for gamma2 = (q - 1) / 32. Decompose returns r1 in [0, 16) and r0 as a centered value. For r near q - 1 it gives r1 = 0 and r0 = r - q, as FIPS 204 requires.

```fern
<<rounding>>=
function __high(r: i32): i32 {
  let r1 = r + 127 >> 7;
  return r1 * 1025 + (1 << 21) >> 22 & 15;
}

function __low(r: i32): i32 {
  let r0 = r - __high(r) * 2 * GAMMA2;
  return r0 - ((Q - 1) / 2 - r0 >> 31 & Q);
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
```

### `encoding`

Bit packing of FIPS 204 section 7.1, least significant bit first. Each value is in [0, 2^d).

```fern
<<encoding>>=
function __pack(own out: u8[], at: i32, f: [i32], count: i32, d: i32): u8[] {
  let acc: i64 = 0;
  let bits = 0;
  let pos = at;
  let i = 0;
  while (i < count) {
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

function __unpack(b: [u8], from: i32, count: i32, d: i32): i32[] {
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

// bound - f[i] for each coefficient of f as a centered value.
function __offset(f: [i32], bound: i32): i32[] {
  let out = __alloc_i32(f.len());
  let i = 0;
  while (i < f.len()) {
    out = out.with(i, bound - __centered(f[i]));
    i += 1;
  }
  return out;
}

// bound - f[i] for each value of f, reduced modulo q.
function __from_offset(own f: i32[], bound: i32): i32[] {
  let i = 0;
  while (i < f.len()) {
    f = f.with(i, __norm(bound - f[i]));
    i += 1;
  }
  return f;
}

function __w1_encode(w1: [i32]): u8[] {
  return __pack(__alloc_u8(128 * K), 0, w1, 256 * K, 4);
}

function __hint_pack(own out: u8[], at: i32, h: [u8]): u8[] {
  let i = 0;
  while (i < OMEGA + K) {
    out = out.with(at + i, 0 as u8);
    i += 1;
  }
  let index = 0;
  let r = 0;
  while (r < K) {
    let j = 0;
    while (j < 256) {
      if (h[256 * r + j] != 0) {
        out = out.with(at + index, j as u8);
        index += 1;
      }
      j += 1;
    }
    out = out.with(at + OMEGA + r, index as u8);
    r += 1;
  }
  return out;
}

// The hints of a signature, or an empty array when their encoding is not
// the one that FIPS 204 HintBitPack makes.
function __hint_unpack(y: [u8], at: i32): u8[] {
  let h = __alloc_u8(256 * K);
  let i = 0;
  while (i < 256 * K) {
    h = h.with(i, 0 as u8);
    i += 1;
  }
  let index = 0;
  let r = 0;
  while (r < K) {
    let limit = y[at + OMEGA + r] as i32;
    if (limit < index || limit > OMEGA) {
      return [];
    }
    let first = index;
    while (index < limit) {
      if (index > first && y[at + index - 1] >= y[at + index]) {
        return [];
      }
      h = h.with(256 * r + y[at + index] as i32, 1 as u8);
      index += 1;
    }
    r += 1;
  }
  while (index < OMEGA) {
    if (y[at + index] != 0) {
      return [];
    }
    index += 1;
  }
  return h;
}

function __h(a: [u8], n: i32): u8[] {
  return crypto.shake256_new().update(a).reader().squeeze(n).0;
}

function __mu(tr: [u8], msg: [u8], ctx: [u8]): u8[] {
  let prefix = __alloc_u8(2).with(0, 0 as u8).with(1, ctx.len() as u8);
  return crypto.shake256_new().update(tr).update(prefix).update(ctx).update(msg).reader().squeeze(64).0;
}

function __same(a: [u8], b: [u8], n: i32): boolean {
  let diff: u8 = 0;
  let i = 0;
  while (i < n) {
    diff |= a[i] ^ b[i];
    i += 1;
  }
  return diff == 0;
}
```

### `key generation`

ML-DSA.KeyGen_internal of FIPS 204 algorithm 6. Power2Round splits t into t1, which is in the public key, and t0, which is in the private key.

```fern
<<key generation>>=
pub function keygen_derand(seed: [u8]): Result[KeyPair, MlDsaError] {
  if (seed.len() != 32) {
    return Err(SeedLength(seed.len()));
  }
  let input = __bytes_into(__alloc_u8(34), 0, seed, 0, 32).with(32, K as u8).with(33, L as u8);
  let expanded = __h(input, 128);
  let rho = __bytes_into(__alloc_u8(32), 0, expanded, 0, 32);
  __ct_public(rho);
  let s = __expand_s(expanded[32:96]);
  let s1 = s.0;
  let s2 = s.1;
  let t = __add(__mat_vec(__expand_a(rho), __ntt_all(__bytes_i32(s1), L)), s2);
  let t1 = __alloc_i32(256 * K);
  let t0 = __alloc_i32(256 * K);
  let i = 0;
  while (i < 256 * K) {
    let high = t[i] + 4095 >> 13;
    t1 = t1.with(i, high);
    t0 = t0.with(i, 4096 - (t[i] - (high << 13)));
    i += 1;
  }
  let pk = __pack(__bytes_into(__alloc_u8(PUBLIC_KEY_BYTES), 0, rho, 0, 32), 32, t1, 256 * K, 10);
  __ct_public(pk);
  let sk = __bytes_into(__alloc_u8(PRIVATE_KEY_BYTES), 0, rho, 0, 32);
  sk = __bytes_into(sk, 32, expanded, 96, 32);
  sk = __bytes_into(sk, 64, __h(pk, 64), 0, 64);
  sk = __pack(sk, 128, __offset(s1, ETA), 256 * L, 4);
  sk = __pack(sk, 768, __offset(s2, ETA), 256 * K, 4);
  sk = __pack(sk, 1536, t0, 256 * K, 13);
  return Ok(KeyPair { public_key: pk, private_key: sk });
}

function __bytes_i32(f: [i32]): i32[] {
  let out = __alloc_i32(f.len());
  let i = 0;
  while (i < f.len()) {
    out = out.with(i, f[i]);
    i += 1;
  }
  return out;
}
```

### `signing`

ML-DSA.Sign_internal of FIPS 204 algorithm 7. Each hint bit is 1 where the high bits of w - c s2 + c t0 and of w - c s2 differ. A byte array holds the hints, so that the module can mark the accepted hints public before it packs them.

```fern
<<signing>>=
function __check_context(ctx: [u8]): Result[void, MlDsaError] {
  if (ctx.len() > 255) {
    return Err(ContextLength(ctx.len()));
  }
  return Ok(());
}

function __check_private_key(sk: [u8]): Result[void, MlDsaError] {
  if (sk.len() != PRIVATE_KEY_BYTES) {
    return Err(PrivateKeyLength(sk.len()));
  }
  return Ok(());
}

pub function sign(sk: [u8], msg: [u8], ctx: [u8]): Result[u8[], MlDsaError] {
  return sign_derand(sk, msg, ctx, rand.bytes(32));
}

pub function sign_deterministic(sk: [u8], msg: [u8], ctx: [u8]): Result[u8[], MlDsaError] {
  let rnd = __alloc_u8(32);
  let i = 0;
  while (i < 32) {
    rnd = rnd.with(i, 0 as u8);
    i += 1;
  }
  return sign_derand(sk, msg, ctx, rnd);
}

pub function sign_derand(sk: [u8], msg: [u8], ctx: [u8], rnd: [u8]): Result[u8[], MlDsaError] {
  __check_private_key(sk)?;
  __check_context(ctx)?;
  return sign_mu_derand(sk, __mu(sk[64:128], msg, ctx), rnd);
}

pub function sign_mu_derand(sk: [u8], mu: [u8], rnd: [u8]): Result[u8[], MlDsaError] {
  __check_private_key(sk)?;
  if (mu.len() != 64) {
    return Err(MuLength(mu.len()));
  }
  if (rnd.len() != 32) {
    return Err(RandomnessLength(rnd.len()));
  }
  let rho = __bytes_into(__alloc_u8(32), 0, sk, 0, 32);
  __ct_public(rho);
  let s1 = __unpack(sk, 128, 256 * L, 4);
  let s2 = __unpack(sk, 768, 256 * K, 4);
  if (__public(__out_of_range(s1) | __out_of_range(s2))) {
    return Err(PrivateKeyRange);
  }
  let key = SigningKey {
    a: __expand_a(rho),
    s1: __ntt_all(__from_offset(s1, ETA), L),
    s2: __ntt_all(__from_offset(s2, ETA), K),
    t0: __ntt_all(__from_offset(__unpack(sk, 1536, 256 * K, 13), 4096), K),
  };
  let rho2 = crypto.shake256_new().update(sk[32:64]).update(rnd).update(mu).reader().squeeze(64).0;
  let kappa = 0;
  let sig: u8[] = [];
  while (sig.len() == 0) {
    sig = __attempt(key, mu, __expand_mask(rho2, kappa));
    kappa += L;
  }
  return Ok(sig);
}

@secret
struct SigningKey {
  a: i32[],
  s1: i32[],
  s2: i32[],
  t0: i32[],
}

// 1 when a value of f, an encoding of eta - s, is over 2 eta.
function __out_of_range(f: [i32]): i32 {
  let bad = 0;
  let i = 0;
  while (i < f.len()) {
    bad |= 2 * ETA - f[i] >> 31 & 1;
    i += 1;
  }
  return bad;
}

// One try of the loop of algorithm 7 with the mask y: the signature, or an
// empty array when the try is rejected.
function __attempt(key: SigningKey, mu: [u8], own y: i32[]): u8[] {
  let w = __mat_vec(key.a, __ntt_all(__bytes_i32(y), L));
  let w1 = __alloc_i32(256 * K);
  let i = 0;
  while (i < 256 * K) {
    w1 = w1.with(i, __high(w[i]));
    i += 1;
  }
  let ct = crypto.shake256_new().update(mu).update(__w1_encode(w1)).reader().squeeze(CT_BYTES).0;
  __ct_public(ct);
  let ch = __ntt(__sample_in_ball(ct), 0);
  let z = __add(y, __scale(ch, key.s1, L));
  let r = __sub(w, __scale(ch, key.s2, K));
  let bad = __exceeds(z, GAMMA1 - BETA);
  i = 0;
  while (i < 256 * K) {
    bad |= GAMMA2 - BETA - 1 - __abs(__low(r[i])) >> 31 & 1;
    i += 1;
  }
  if (__public(bad)) {
    return [];
  }
  let ct0 = __scale(ch, key.t0, K);
  bad = __exceeds(ct0, GAMMA2);
  let h = __alloc_u8(256 * K);
  let count = 0;
  i = 0;
  while (i < 256 * K) {
    let bit = -(__high(__csub(r[i] + ct0[i])) ^ __high(r[i])) >> 31 & 1;
    h = h.with(i, bit as u8);
    count += bit;
    i += 1;
  }
  if (__public(bad | OMEGA - count >> 31 & 1)) {
    return [];
  }
  __ct_public(h);
  let sig = __bytes_into(__alloc_u8(SIGNATURE_BYTES), 0, ct, 0, CT_BYTES);
  sig = __pack(sig, CT_BYTES, __offset(z, GAMMA1), 256 * L, 20);
  sig = __hint_pack(sig, CT_BYTES + 640 * L, h);
  __ct_public(sig);
  return sig;
}
```

### `verification`

ML-DSA.Verify_internal of FIPS 204 algorithm 8. A public key or a signature of the wrong length, or a malformed hint encoding, gives false.

```fern
<<verification>>=
pub function verify(pk: [u8], msg: [u8], ctx: [u8], sig: [u8]): boolean {
  if (pk.len() != PUBLIC_KEY_BYTES || ctx.len() > 255) {
    return false;
  }
  return verify_mu(pk, __mu(__h(pk, 64), msg, ctx), sig);
}

pub function verify_mu(pk: [u8], mu: [u8], sig: [u8]): boolean {
  if (pk.len() != PUBLIC_KEY_BYTES || mu.len() != 64 || sig.len() != SIGNATURE_BYTES) {
    return false;
  }
  let z = __from_offset(__unpack(sig, CT_BYTES, 256 * L, 20), GAMMA1);
  if (__exceeds(z, GAMMA1 - BETA) != 0) {
    return false;
  }
  let h = __hint_unpack(sig, CT_BYTES + 640 * L);
  if (h.len() == 0) {
    return false;
  }
  let t1 = __unpack(pk, 32, 256 * K, 10);
  let i = 0;
  while (i < 256 * K) {
    t1 = t1.with(i, t1[i] << 13);
    i += 1;
  }
  let ct = sig[0:CT_BYTES];
  let ch = __ntt(__sample_in_ball(ct), 0);
  let az = __mat_vec(__expand_a(pk[0:32]), __ntt_all(z, L));
  let w = __sub(az, __scale(ch, __ntt_all(t1, K), K));
  let w1 = __alloc_i32(256 * K);
  i = 0;
  while (i < 256 * K) {
    w1 = w1.with(i, __use_hint(h[i] as i32, w[i]));
    i += 1;
  }
  let again = crypto.shake256_new().update(mu).update(__w1_encode(w1)).reader().squeeze(CT_BYTES).0;
  return __same(ct, again, CT_BYTES);
}
```
