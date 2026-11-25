# ML-DSA-65 signatures

This module implements the parameter set ML-DSA-65 of FIPS 204: key generation, signing and verification of the pure ML-DSA of section 5. It does not implement HashML-DSA.

`private_key` makes a key from a 32-byte seed with ML-DSA.KeyGen_internal. Store the seed: section 3.6.3 of the standard permits it as the stored form of a private key, and `seed` gives it back. `generate` makes a key from a fresh random seed. `bytes` gives the 4032-byte encoding of a private key, and `expanded_private_key` reads that encoding back. A key read from its encoding has no seed.

`sign` is the hedged signing of Algorithm 2 with 32 random bytes from `std/rand`. `sign_deterministic` uses 32 zero bytes. `sign_derand` takes the 32 bytes from the caller. The context has 255 bytes or fewer.

`public_key` reads a 1952-byte public key, and `verify` checks a 3309-byte signature. Each function that reads input returns a `Result` with an `MlDsaError`. No function stops the process on bad input.

## Executable example

```fern test name=std-crypto-mldsa-example
import "std/crypto/mldsa";
import "std/string";

function main(): i32 {
  let Ok(k) = mldsa.private_key("an example seed of 32 bytes long".bytes()) else { return 1; };
  let msg = "message".bytes();
  let Ok(sig) = k.sign(msg, []) else { return 2; };
  let pk = k.public_key();
  let Ok(_) = pk.verify(msg, sig, []) else { return 3; };
  match (pk.verify("massage".bytes(), sig, [])) {
    Ok(_) => { return 4; },
    Err(e) => {}
  }
  let Ok(again) = mldsa.public_key(pk.bytes()) else { return 5; };
  let Ok(_) = again.verify(msg, sig, []) else { return 6; };
  return 0;
}
```

## Model

The module stores a polynomial as 256 coefficients of type `i32` in [0, q), with q = 8380417. A vector of polynomials is one array, and polynomial i starts at index 256 i. The matrix A has K = 6 rows and L = 5 columns, and entry (r, s) starts at 256 (5 r + s).

Multiplication uses the Montgomery reduction of Appendix A. The table `ZETAS` holds the values of Appendix B multiplied by 2^32 mod q, so a butterfly gives the product with the plain value. A product of two values in the NTT domain carries a factor 2^-32. The inverse transform multiplies by 2^32 / 256, so it returns such a product to the plain domain. The module applies the inverse transform only to products.

## Constant time

Signing and key generation have no branch and no table index that depends on a secret. The module uses masks for each comparison of a secret value. It declassifies only these values:

- ρ, tr and the public key. The standard makes them public.
- The verdict of each round of the signing loop. The standard reveals the number of rounds.
- The signature of the last round.
- The verdict of the check of an expanded private key.
- The decision of a sampler to read more bytes. The next paragraph gives its probability.

Two samplers in the standard reject input bytes that depend on a secret: RejBoundedPoly and SampleInBall. This module runs each of them over a fixed number of bytes, at least the limit of Appendix C. A sampler reads more bytes only when that number is not sufficient. Appendix C gives a probability of 2^-256 or less for this event.

RejBoundedPoly keeps each nibble below 9 in its order. The module moves each kept nibble down by the number of nibbles that it rejected before that nibble. Each pass moves the nibbles by one bit of that distance, from the least significant bit. Two kept nibbles cannot land on the same position, so the pass needs no index that depends on a secret.

SampleInBall keeps the τ = 49 nonzero coefficients as a list of positions. A step of the shuffle examines each entry of the list with a mask, so the positions are not table indices.

## Module assembly

```fern
<<*>>=
<<parameters>>

<<errors>>

<<arithmetic>>

<<transforms>>

<<encodings>>

<<sampling>>

<<keys>>

<<signing>>

<<verification>>
```

### Parameters

```fern
<<parameters>>=
// Sign and verify with ML-DSA-65 (FIPS 204).
import "core/cmp";
import "core/error";
import "std/crypto";
import "std/rand";

pub const SEED_SIZE: i32 = 32;

pub const PUBLIC_KEY_SIZE: i32 = 1952;

pub const PRIVATE_KEY_SIZE: i32 = 4032;

pub const SIGNATURE_SIZE: i32 = 3309;

pub const MAX_CONTEXT_SIZE: i32 = 255;

const Q: i32 = 8380417;

const QINV: i32 = 58728449;

const K: i32 = 6;

const L: i32 = 5;

const TAU: i32 = 49;

const BETA: i32 = 196;

const GAMMA1: i32 = 524288;

const GAMMA2: i32 = 261888;

const OMEGA: i32 = 55;

const CTILDE: i32 = 48;

const Z_AT: i32 = 48;

const HINT_AT: i32 = 3248;
```

### Errors

```fern
<<errors>>=
pub enum MlDsaError {
  SeedLength(i32),
  RandomnessLength(i32),
  PrivateKeyLength(i32),
  PrivateKeyEncoding,
  PublicKeyLength(i32),
  ContextLength(i32),
  SignatureLength(i32),
  HintEncoding,
  ResponseRange,
  SignatureMismatch,
}

impl error.Error for MlDsaError {}

impl cmp.Display for MlDsaError {}

pub function (e: MlDsaError) message(): string {
  return match (e) {
    SeedLength(n) => "mldsa: the seed is " + n.to_string() + " bytes, want 32",
    RandomnessLength(n) => "mldsa: the signing randomness is " + n.to_string() + " bytes, want 32",
    PrivateKeyLength(n) => "mldsa: the private key is " + n.to_string() + " bytes, want 4032",
    PrivateKeyEncoding => "mldsa: the private key has a coefficient out of range or does not agree with its public key",
    PublicKeyLength(n) => "mldsa: the public key is " + n.to_string() + " bytes, want 1952",
    ContextLength(n) => "mldsa: the context is " + n.to_string() + " bytes, the limit is 255",
    SignatureLength(n) => "mldsa: the signature is " + n.to_string() + " bytes, want 3309",
    HintEncoding => "mldsa: the hint of the signature does not have its canonical encoding",
    ResponseRange => "mldsa: the response of the signature is out of range",
    SignatureMismatch => "mldsa: the signature does not agree with the message and the public key",
  };
}

pub function (e: MlDsaError) to_string(): string {
  return e.message();
}
```

### Arithmetic

`__high` and `__low` are Decompose (Algorithm 36) for 2γ2 = (q - 1) / 16. They use a multiplication and shifts in place of a division. Each mask function gives -1 or 0.

```fern
<<arithmetic>>=
// a 2^-32 mod q in (-q, q), for |a| <= 2^31 q (Algorithm 49).
function __mont(a: i64): i32 {
  let t = a as i32 * QINV;
  return (a - t as i64 * (Q as i64) >> 32) as i32;
}

// x in (-q, q) to [0, q).
function __freeze(x: i32): i32 {
  return x + (x >> 31 & Q);
}

// x in [0, 2q) to [0, q).
function __csub(x: i32): i32 {
  let r = x - Q;
  return r + (r >> 31 & Q);
}

// The representative of x in [0, q) in [-(q - 1) / 2, (q - 1) / 2].
function __centered(x: i32): i32 {
  return x - ((Q - 1) / 2 - x >> 31 & Q);
}

// x in (-q, q) as a coefficient in [0, q).
function __modq(x: i32): i32 {
  return x + (x >> 31 & Q);
}

// -1 when x == y, else 0, for x and y in [0, 2^30).
function __eq_mask(x: i32, y: i32): i32 {
  return (x ^ y) - 1 >> 31;
}

// 1 when |x mod± q| >= bound, else 0.
function __beyond(x: i32, bound: i32): i32 {
  let c = __centered(x);
  let s = c >> 31;
  return bound - 1 - ((c ^ s) - s) >> 31 & 1;
}

function __high(a: i32): i32 {
  let a1 = a + 127 >> 7;
  return a1 * 1025 + (1 << 21) >> 22 & 15;
}

function __low(a: i32): i32 {
  let a0 = a - __high(a) * 2 * GAMMA2;
  return a0 - ((Q - 1) / 2 - a0 >> 31 & Q);
}

// UseHint (Algorithm 40). Verification calls it on public values only.
function __use_hint(h: u8, r: i32): i32 {
  let r1 = __high(r);
  if (h == 0u8) {
    return r1;
  }
  if (__low(r) > 0) {
    return r1 + 1 & 15;
  }
  return r1 - 1 & 15;
}

function __zeros(n: i32): i32[] {
  let a = __alloc_i32(n);
  for i in 0..n {
    a = a.with(i, 0);
  }
  return a;
}

function __zero_bytes(n: i32): u8[] {
  let a = __alloc_u8(n);
  for i in 0..n {
    a = a.with(i, 0u8);
  }
  return a;
}

function __copy(b: [u8], from: i32, n: i32): u8[] {
  return __bytes_into(__alloc_u8(n), 0, b, from, n);
}

function __copy_i32(f: [i32]): i32[] {
  let out = __alloc_i32(f.len());
  for i in 0..f.len() {
    out = out.with(i, f[i]);
  }
  return out;
}

function __add(own f: i32[], g: [i32]): i32[] {
  for i in 0..f.len() {
    f = f.with(i, __csub(f[i] + g[i]));
  }
  return f;
}

function __sub(own f: i32[], g: [i32]): i32[] {
  for i in 0..f.len() {
    f = f.with(i, __csub(f[i] - g[i] + Q));
  }
  return f;
}

function __erase_all(own a: i32[], own b: i32[], own c: i32[], own d: i32[]): void {
  crypto.erase(a);
  crypto.erase(b);
  crypto.erase(c);
  crypto.erase(d);
}
```

### Transforms

```fern
<<transforms>>=
const ZETAS: i32[] = [
  0, 25847, 5771523, 7861508, 237124, 7602457, 7504169, 466468,
  1826347, 2353451, 8021166, 6288512, 3119733, 5495562, 3111497, 2680103,
  2725464, 1024112, 7300517, 3585928, 7830929, 7260833, 2619752, 6271868,
  6262231, 4520680, 6980856, 5102745, 1757237, 8360995, 4010497, 280005,
  2706023, 95776, 3077325, 3530437, 6718724, 4788269, 5842901, 3915439,
  4519302, 5336701, 3574422, 5512770, 3539968, 8079950, 2348700, 7841118,
  6681150, 6736599, 3505694, 4558682, 3507263, 6239768, 6779997, 3699596,
  811944, 531354, 954230, 3881043, 3900724, 5823537, 2071892, 5582638,
  4450022, 6851714, 4702672, 5339162, 6927966, 3475950, 2176455, 6795196,
  7122806, 1939314, 4296819, 7380215, 5190273, 5223087, 4747489, 126922,
  3412210, 7396998, 2147896, 2715295, 5412772, 4686924, 7969390, 5903370,
  7709315, 7151892, 8357436, 7072248, 7998430, 1349076, 1852771, 6949987,
  5037034, 264944, 508951, 3097992, 44288, 7280319, 904516, 3958618,
  4656075, 8371839, 1653064, 5130689, 2389356, 8169440, 759969, 7063561,
  189548, 4827145, 3159746, 6529015, 5971092, 8202977, 1315589, 1341330,
  1285669, 6795489, 7567685, 6940675, 5361315, 4499357, 4751448, 3839961,
  2091667, 3407706, 2316500, 3817976, 5037939, 2244091, 5933984, 4817955,
  266997, 2434439, 7144689, 3513181, 4860065, 4621053, 7183191, 5187039,
  900702, 1859098, 909542, 819034, 495491, 6767243, 8337157, 7857917,
  7725090, 5257975, 2031748, 3207046, 4823422, 7855319, 7611795, 4784579,
  342297, 286988, 5942594, 4108315, 3437287, 5038140, 1735879, 203044,
  2842341, 2691481, 5790267, 1265009, 4055324, 1247620, 2486353, 1595974,
  4613401, 1250494, 2635921, 4832145, 5386378, 1869119, 1903435, 7329447,
  7047359, 1237275, 5062207, 6950192, 7929317, 1312455, 3306115, 6417775,
  7100756, 1917081, 5834105, 7005614, 1500165, 777191, 2235880, 3406031,
  7838005, 5548557, 6709241, 6533464, 5796124, 4656147, 594136, 4603424,
  6366809, 2432395, 2454455, 8215696, 1957272, 3369112, 185531, 7173032,
  5196991, 162844, 1616392, 3014001, 810149, 1652634, 4686184, 6581310,
  5341501, 3523897, 3866901, 269760, 2213111, 7404533, 1717735, 472078,
  7953734, 1723600, 6577327, 1910376, 6712985, 7276084, 8119771, 4546524,
  5441381, 6144432, 7959518, 6094090, 183443, 7403526, 1612842, 4834730,
  7826001, 3919660, 8332111, 7018208, 3937738, 1400424, 7534263, 1976782,
];

// 2^64 / 256 mod q.
const INV_SCALE: i64 = 41978i64;

// NTT (Algorithm 41) of the polynomial at index at.
function __ntt(own f: i32[], at: i32): i32[] {
  let m = 0;
  let len = 128;
  while (len >= 1) {
    let start = at;
    while (start < at + 256) {
      m += 1;
      let z = ZETAS[m] as i64;
      for j in start..start + len {
        let t = __freeze(__mont(z * (f[j + len] as i64)));
        let a = f[j];
        f = f.with(j + len, __csub(a - t + Q));
        f = f.with(j, __csub(a + t));
      }
      start += 2 * len;
    }
    len /= 2;
  }
  return f;
}

// NTT^-1 (Algorithm 42) of the polynomial at index at, multiplied by 2^32.
function __inv_ntt(own f: i32[], at: i32): i32[] {
  let m = 256;
  let len = 1;
  while (len < 256) {
    let start = at;
    while (start < at + 256) {
      m -= 1;
      let z = ZETAS[m] as i64;
      for j in start..start + len {
        let t = f[j];
        let u = f[j + len];
        f = f.with(j, __csub(t + u));
        f = f.with(j + len, __freeze(__mont(z * ((u - t + Q) as i64))));
      }
      start += 2 * len;
    }
    len *= 2;
  }
  for j in at..at + 256 {
    f = f.with(j, __freeze(__mont(INV_SCALE * (f[j] as i64))));
  }
  return f;
}

function __ntt_all(own f: i32[], count: i32): i32[] {
  for i in 0..count {
    f = __ntt(f, 256 * i);
  }
  return f;
}

function __inv_ntt_all(own f: i32[], count: i32): i32[] {
  for i in 0..count {
    f = __inv_ntt(f, 256 * i);
  }
  return f;
}

// acc[at + i] += a[ai + i] b[bi + i] 2^-32 for each i below 256.
function __mul_acc(own acc: i32[], at: i32, a: [i32], ai: i32, b: [i32], bi: i32): i32[] {
  for i in 0..256 {
    let p = __freeze(__mont(a[ai + i] as i64 * (b[bi + i] as i64)));
    acc = acc.with(at + i, __csub(acc[at + i] + p));
  }
  return acc;
}

// The product of the matrix a and a vector v of L polynomials.
function __mat_vec(a: [i32], v: [i32]): i32[] {
  let w = __zeros(K * 256);
  for i in 0..K {
    for j in 0..L {
      w = __mul_acc(w, 256 * i, a, 256 * (L * i + j), v, 256 * j);
    }
  }
  return w;
}

// The product of one polynomial c and each of count polynomials of v.
function __scale(c: [i32], v: [i32], count: i32): i32[] {
  let w = __zeros(256 * count);
  for i in 0..count {
    w = __mul_acc(w, 256 * i, c, 0, v, 256 * i);
  }
  return w;
}
```

### Encodings

`__pack` and `__unpack` are SimpleBitPack and SimpleBitUnpack (Algorithms 16 and 18) for one or more polynomials. BitPack and BitUnpack (Algorithms 17 and 19) subtract each coefficient from b first.

```fern
<<encodings>>=
// Writes count values of f from index from, bits bits each, at byte at.
function __pack(own out: u8[], at: i32, f: [i32], from: i32, count: i32, bits: i32): u8[] {
  let acc = 0i64;
  let have = 0;
  let pos = at;
  for i in from..from + count {
    acc |= f[i] as i64 << have;
    have += bits;
    while (have >= 8) {
      out = out.with(pos, (acc & 255i64) as u8);
      acc >>= 8;
      have -= 8;
      pos += 1;
    }
  }
  return out;
}

function __unpack(b: [u8], from: i32, count: i32, bits: i32): i32[] {
  let f = __alloc_i32(count);
  let mask = (1i64 << bits) - 1i64;
  let acc = 0i64;
  let have = 0;
  let pos = from;
  for i in 0..count {
    while (have < bits) {
      acc |= b[pos] as i64 << have;
      have += 8;
      pos += 1;
    }
    f = f.with(i, (acc & mask) as i32);
    acc >>= bits;
    have -= bits;
  }
  return f;
}

// b - (f[i] mod± q) for each coefficient of f.
function __offset(f: [i32], b: i32): i32[] {
  let out = __alloc_i32(f.len());
  for i in 0..f.len() {
    out = out.with(i, b - __centered(f[i]));
  }
  return out;
}

// b - v[i] mod q for each value of v.
function __unoffset(own v: i32[], b: i32): i32[] {
  for i in 0..v.len() {
    v = v.with(i, __modq(b - v[i]));
  }
  return v;
}

// pkEncode (Algorithm 22).
function __encode_public(rho: [u8], t1: [i32]): u8[] {
  return __pack(__bytes_into(__zero_bytes(PUBLIC_KEY_SIZE), 0, rho, 0, 32), 32, t1, 0, K * 256, 10);
}

// w1Encode (Algorithm 28).
function __encode_w1(w1: [i32]): u8[] {
  return __pack(__zero_bytes(K * 128), 0, w1, 0, K * 256, 4);
}

// HintBitPack (Algorithm 20) of public hint bits.
function __pack_hint(own out: u8[], at: i32, h: [u8]): u8[] {
  let index = 0;
  for i in 0..K {
    for j in 0..256 {
      if (h[256 * i + j] != 0u8) {
        out = out.with(at + index, j as u8);
        index += 1;
      }
    }
    out = out.with(at + OMEGA + i, index as u8);
  }
  return out;
}

// HintBitUnpack (Algorithm 21). Each refusal is a malformed encoding.
function __unpack_hint(y: [u8], at: i32): Result[u8[], MlDsaError] {
  let h = __zero_bytes(K * 256);
  let index = 0;
  for i in 0..K {
    let limit = y[at + OMEGA + i] as i32;
    if (limit < index || limit > OMEGA) {
      return Err(HintEncoding);
    }
    let first = index;
    while (index < limit) {
      if (index > first && y[at + index - 1] >= y[at + index]) {
        return Err(HintEncoding);
      }
      h = h.with(256 * i + y[at + index] as i32, 1u8);
      index += 1;
    }
  }
  for i in index..OMEGA {
    if (y[at + i] != 0u8) {
      return Err(HintEncoding);
    }
  }
  return Ok(h);
}
```

### Sampling

`__rej_ntt` and `__expand_a` read the public seed ρ, so they branch on its bytes. The other samplers read secret seeds.

```fern
<<sampling>>=
// RejNTTPoly (Algorithm 30) for the seed rho || s || r, at index at.
function __rej_ntt(own a: i32[], at: i32, rho: [u8], s: i32, r: i32): i32[] {
  let seed = __bytes_into(__alloc_u8(34), 0, rho, 0, 32).with(32, s as u8).with(33, r as u8);
  let x = crypto.shake128_new().update(seed).reader();
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

// ExpandA (Algorithm 32).
function __expand_a(rho: [u8]): i32[] {
  let a = __alloc_i32(K * L * 256);
  for r in 0..K {
    for s in 0..L {
      a = __rej_ntt(a, 256 * (L * r + s), rho, s, r);
    }
  }
  return a;
}

// RejBoundedPoly (Algorithm 31) for eta = 4 and the seed rho || r, at index
// at. Appendix C bounds the loop at 481 bytes, and the first pass reads 544.
function __rej_bounded(own out: i32[], at: i32, rho: [u8], r: i32): i32[] {
  let seed = __bytes_into(__alloc_u8(66), 0, rho, 0, 64).with(64, (r & 255) as u8).with(65, (r >> 8) as u8);
  let blocks = 4;
  while (true) {
    let b = crypto.shake256_new().update(seed).reader().squeeze(136 * blocks).0;
    let n = 2 * b.len();
    let v = __alloc_i32(n);
    let shift = __alloc_i32(n);
    let kept = __alloc_i32(n);
    let rejected = 0;
    for i in 0..n {
      let z = b[i >> 1] as i32 >> 4 * (i & 1) & 15;
      let keep = z - 9 >> 31;
      v = v.with(i, 4 - z);
      shift = shift.with(i, rejected);
      kept = kept.with(i, keep);
      rejected += 1 + keep;
    }
    crypto.erase(b);
    let bit = 0;
    while (1 << bit < n) {
      let step = 1 << bit;
      for p in step..n {
        let move = kept[p] & -(shift[p] >> bit & 1);
        let t = p - step;
        v = v.with(t, v[t] ^ (v[t] ^ v[p]) & move);
        shift = shift.with(t, shift[t] ^ (shift[t] ^ shift[p]) & move);
        kept = kept.with(t, kept[t] | move);
        kept = kept.with(p, kept[p] & (-1 ^ move));
      }
      bit += 1;
    }
    let full = __alloc_u8(1).with(0, (kept[255] & 1) as u8);
    __ct_public(full);
    if (full[0] == 1u8) {
      for i in 0..256 {
        out = out.with(at + i, __modq(v[i]));
      }
      __erase_all(v, shift, kept, []);
      crypto.erase(seed);
      return out;
    }
    __erase_all(v, shift, kept, []);
    blocks *= 2;
  }
  return out;
}

// ExpandS (Algorithm 33): s1 and s2 for the secret seed rho.
function __expand_s(rho: [u8]): (i32[], i32[]) {
  let s1 = __zeros(L * 256);
  let s2 = __zeros(K * 256);
  for r in 0..L {
    s1 = __rej_bounded(s1, 256 * r, rho, r);
  }
  for r in 0..K {
    s2 = __rej_bounded(s2, 256 * r, rho, L + r);
  }
  return (s1, s2);
}

// ExpandMask (Algorithm 34).
function __expand_mask(rho: [u8], kappa: i32): i32[] {
  let y = __zeros(L * 256);
  let seed = __bytes_into(__alloc_u8(66), 0, rho, 0, 64);
  for r in 0..L {
    seed = seed.with(64, (kappa + r & 255) as u8).with(65, (kappa + r >> 8) as u8);
    let v = crypto.shake256_new().update(seed).reader().squeeze(640).0;
    let f = __unpack(v, 0, 256, 20);
    for i in 0..256 {
      y = y.with(256 * r + i, __modq(GAMMA1 - f[i]));
    }
    crypto.erase(v);
    crypto.erase(f);
  }
  crypto.erase(seed);
  return y;
}

// SampleInBall (Algorithm 29). pos[e] is the position of the nonzero
// coefficient that the shuffle placed in step e, and bit e of the first eight
// bytes is its sign. Appendix C bounds the bytes at 221, and the first pass
// reads 272.
function __sample_in_ball(ctilde: [u8]): i32[] {
  let x = crypto.shake256_new().update(ctilde).reader();
  let first = x.squeeze(272);
  let b = first.0;
  x = first.1;
  let signs = __copy(b, 0, 8);
  let pos = __zeros(TAU);
  let count = 0;
  let from = 8;
  while (true) {
    for p in from..b.len() {
      let j = b[p] as i32;
      let i = 256 - TAU + count;
      let take = count - TAU >> 31 & (-1 ^ i - j >> 31);
      for e in 0..TAU {
        let moved = take & e - count >> 31 & __eq_mask(pos[e], j);
        let placed = take & __eq_mask(e, count);
        pos = pos.with(e, pos[e] ^ (pos[e] ^ i) & moved ^ (pos[e] ^ j) & placed);
      }
      count -= take;
    }
    crypto.erase(b);
    let done = __alloc_u8(1).with(0, (__eq_mask(count, TAU) & 1) as u8);
    __ct_public(done);
    if (done[0] == 1u8) {
      let c = __zeros(256);
      for k in 0..256 {
        let value = 0;
        for e in 0..TAU {
          let negative = -(signs[e >> 3] as i32 >> (e & 7) & 1);
          value |= __eq_mask(pos[e], k) & 1 + (negative & Q - 2);
        }
        c = c.with(k, value);
      }
      crypto.erase(pos);
      crypto.erase(signs);
      return c;
    }
    let next = x.squeeze(136);
    b = next.0;
    x = next.1;
    from = 0;
  }
  return [];
}
```

### Keys

The private key is opaque: only this module makes one or reads its fields. It holds its vectors in the NTT domain, as signing uses them.

`expanded_private_key` checks that each coefficient of s1 and s2 is in [-4, 4]. It also computes t = A s1 + s2 again and checks that t0 and tr agree with the encoding. Thus each key that it accepts is the output of skEncode for its public key.

```fern
<<keys>>=
@value
pub opaque struct PublicKey {
  bytes: u8[],
  a: i32[],
  tr: u8[],
  t1: i32[],
}

@value
@secret
pub opaque struct PrivateKey {
  seed: u8[],
  has_seed: boolean,
  key: u8[],
  s1: i32[],
  s2: i32[],
  t0: i32[],
  encoded: u8[],
  public: PublicKey,
}

// The key for the public encoding pk, with the matrix a of its seed.
function __public(own pk: u8[], a: i32[]): PublicKey {
  let tr = crypto.shake256_new().update(pk).reader().squeeze(64).0;
  let t1 = __unpack(pk, 32, K * 256, 10);
  for i in 0..K * 256 {
    t1 = t1.with(i, t1[i] << 13);
  }
  return PublicKey { bytes: pk, a: a, tr: tr, t1: __ntt_all(t1, K) };
}

// t = A s1 + s2 split by Power2Round (Algorithm 35) into t1 and t0 mod q.
function __split(a: [i32], s1: [i32], s2: [i32]): (i32[], i32[]) {
  let t = __add(__inv_ntt_all(__mat_vec(a, __ntt_all(__copy_i32(s1), L)), K), s2);
  let t1 = __alloc_i32(K * 256);
  for i in 0..K * 256 {
    let high = t[i] + 4095 >> 13;
    t1 = t1.with(i, high);
    t = t.with(i, __modq(t[i] - (high << 13)));
  }
  return (t1, t);
}

// skEncode (Algorithm 24) and the key that holds it.
function __assemble(own seed: u8[], has_seed: boolean, rho: [u8], own key: u8[], own s1: i32[], own s2: i32[], own t0: i32[], own pk: u8[], a: i32[]): PrivateKey {
  let public = __public(pk, a);
  let sk = __bytes_into(__bytes_into(__bytes_into(__zero_bytes(PRIVATE_KEY_SIZE), 0, rho, 0, 32), 32, key, 0, 32), 64, public.tr, 0, 64);
  let e1 = __offset(s1, 4);
  let e2 = __offset(s2, 4);
  let e0 = __offset(t0, 4096);
  sk = __pack(sk, 128, e1, 0, L * 256, 4);
  sk = __pack(sk, 768, e2, 0, K * 256, 4);
  sk = __pack(sk, 1536, e0, 0, K * 256, 13);
  __erase_all(e1, e2, e0, []);
  return PrivateKey { seed: seed, has_seed: has_seed, key: key, s1: __ntt_all(s1, L), s2: __ntt_all(s2, K), t0: __ntt_all(t0, K), encoded: sk, public: public };
}

// ML-DSA.KeyGen_internal (Algorithm 6).
function __keygen(seed: [u8]): PrivateKey {
  let expanded = crypto.shake256_new().update(seed).update([6u8, 5u8]).reader().squeeze(128).0;
  let rho = __copy(expanded, 0, 32);
  __ct_public(rho);
  let key = __copy(expanded, 96, 32);
  let s = __expand_s(expanded[32:96]);
  crypto.erase(expanded);
  let a = __expand_a(rho);
  let parts = __split(a, s.0, s.1);
  let pk = __encode_public(rho, parts.0);
  __ct_public(pk);
  crypto.erase(parts.0);
  return __assemble(__copy(seed, 0, 32), true, rho, key, s.0, s.1, parts.1, pk, a);
}

pub function private_key(seed: [u8]): Result[PrivateKey, MlDsaError] {
  if (seed.len() != SEED_SIZE) {
    return Err(SeedLength(seed.len()));
  }
  return Ok(__keygen(seed));
}

// ML-DSA.KeyGen (Algorithm 1) with a seed from the operating system.
pub function generate(): PrivateKey {
  let seed = rand.bytes(SEED_SIZE);
  let k = __keygen(seed);
  crypto.erase(seed);
  return k;
}

// skDecode (Algorithm 25) of an untrusted encoding, with the checks above.
pub function expanded_private_key(sk: [u8]): Result[PrivateKey, MlDsaError] {
  if (sk.len() != PRIVATE_KEY_SIZE) {
    return Err(PrivateKeyLength(sk.len()));
  }
  let rho = __copy(sk, 0, 32);
  __ct_public(rho);
  let tr = __copy(sk, 64, 64);
  __ct_public(tr);
  let e1 = __unpack(sk, 128, L * 256, 4);
  let e2 = __unpack(sk, 768, K * 256, 4);
  let bad = 0;
  for i in 0..L * 256 {
    bad |= 8 - e1[i] >> 31 & 1;
  }
  for i in 0..K * 256 {
    bad |= 8 - e2[i] >> 31 & 1;
  }
  let s1 = __unoffset(e1, 4);
  let s2 = __unoffset(e2, 4);
  let t0 = __unoffset(__unpack(sk, 1536, K * 256, 13), 4096);
  let a = __expand_a(rho);
  let parts = __split(a, s1, s2);
  for i in 0..K * 256 {
    let d = parts.1[i] ^ t0[i];
    bad |= (d | -d) >> 31 & 1;
  }
  let pk = __encode_public(rho, parts.0);
  __ct_public(pk);
  let verdict = __alloc_u8(1).with(0, bad as u8);
  __ct_public(verdict);
  let tr2 = crypto.shake256_new().update(pk).reader().squeeze(64).0;
  __erase_all(parts.0, parts.1, [], []);
  if (verdict[0] != 0u8 || !crypto.consteq(tr, tr2)) {
    __erase_all(s1, s2, t0, []);
    return Err(PrivateKeyEncoding);
  }
  return Ok(__assemble([], false, rho, __copy(sk, 32, 32), s1, s2, t0, pk, a));
}

// The seed of a key made by private_key or generate.
pub function (k: PrivateKey) seed(): Option[u8[]] {
  if (!k.has_seed) {
    return None;
  }
  return Some(k.seed);
}

// The 4032-byte encoding of skEncode (Algorithm 24).
pub function (k: PrivateKey) bytes(): u8[] {
  return k.encoded;
}

pub function (k: PrivateKey) public_key(): PublicKey {
  return k.public;
}

// Consumes this key holder. Zeroes uniquely held secret arrays before release.
pub function (own k: PrivateKey) erase(): void {
  crypto.erase(k.seed);
  crypto.erase(k.key);
  crypto.erase(k.encoded);
  __erase_all(k.s1, k.s2, k.t0, []);
}

// pkDecode (Algorithm 23). Each coefficient of t1 is in range, so only the
// length can be wrong.
pub function public_key(bytes: [u8]): Result[PublicKey, MlDsaError] {
  if (bytes.len() != PUBLIC_KEY_SIZE) {
    return Err(PublicKeyLength(bytes.len()));
  }
  return Ok(__public(__copy(bytes, 0, PUBLIC_KEY_SIZE), __expand_a(bytes[0:32])));
}

// The 1952-byte encoding of pkEncode (Algorithm 22).
pub function (k: PublicKey) bytes(): u8[] {
  return k.bytes;
}
```

### Signing

Each round computes all the checks of lines 23 and 28 of Algorithm 7 and declassifies one verdict. The hint is MakeHint (Algorithm 39) of -ct0 and w - cs2 + ct0. Thus it compares the high bits of w - cs2 + ct0 with the high bits of w - cs2.

```fern
<<signing>>=
// The message representative mu of Algorithm 7 for M' of Algorithm 2.
function __mu(tr: [u8], msg: [u8], ctx: [u8]): u8[] {
  return crypto.shake256_new().update(tr).update([0u8, ctx.len() as u8]).update(ctx).update(msg).reader().squeeze(64).0;
}

// ML-DSA.Sign_internal (Algorithm 7).
function __sign(k: PrivateKey, msg: [u8], ctx: [u8], rnd: [u8]): u8[] {
  let mu = __mu(k.public.tr, msg, ctx);
  let rho = crypto.shake256_new().update(k.key).update(rnd).update(mu).reader().squeeze(64).0;
  let kappa = 0;
  while (true) {
    let y = __expand_mask(rho, kappa);
    let w = __inv_ntt_all(__mat_vec(k.public.a, __ntt_all(__copy_i32(y), L)), K);
    let w1 = __alloc_i32(K * 256);
    for i in 0..K * 256 {
      w1 = w1.with(i, __high(w[i]));
    }
    let packed = __encode_w1(w1);
    let ctilde = crypto.shake256_new().update(mu).update(packed).reader().squeeze(CTILDE).0;
    crypto.erase(packed);
    crypto.erase(w1);
    let c = __ntt(__sample_in_ball(ctilde), 0);
    let z = __add(y, __inv_ntt_all(__scale(c, k.s1, L), L));
    let r = __sub(w, __inv_ntt_all(__scale(c, k.s2, K), K));
    let ct0 = __inv_ntt_all(__scale(c, k.t0, K), K);
    crypto.erase(c);
    let bad = 0;
    for i in 0..L * 256 {
      bad |= __beyond(z[i], GAMMA1 - BETA);
    }
    let hint = __alloc_u8(K * 256);
    let ones = 0;
    for i in 0..K * 256 {
      let x = r[i];
      bad |= __beyond(__low(x), GAMMA2 - BETA) | __beyond(ct0[i], GAMMA2);
      let h = 1 + __eq_mask(__high(__csub(x + ct0[i])), __high(x));
      hint = hint.with(i, h as u8);
      ones += h;
    }
    bad |= OMEGA - ones >> 31 & 1;
    __erase_all(r, ct0, [], []);
    let verdict = __alloc_u8(1).with(0, bad as u8);
    __ct_public(verdict);
    if (verdict[0] == 0u8) {
      crypto.erase(rho);
      let e = __offset(z, GAMMA1);
      crypto.erase(z);
      let sig = __pack(__bytes_into(__zero_bytes(SIGNATURE_SIZE), 0, ctilde, 0, CTILDE), Z_AT, e, 0, L * 256, 20);
      crypto.erase(e);
      __ct_public(sig);
      __ct_public(hint);
      return __pack_hint(sig, HINT_AT, hint);
    }
    crypto.erase(z);
    crypto.erase(hint);
    crypto.erase(ctilde);
    kappa += L;
  }
  return [];
}

function __check_sign(ctx: [u8], rnd: [u8]): Result[void, MlDsaError] {
  if (ctx.len() > MAX_CONTEXT_SIZE) {
    return Err(ContextLength(ctx.len()));
  }
  if (rnd.len() != 32) {
    return Err(RandomnessLength(rnd.len()));
  }
  return Ok(());
}

// ML-DSA.Sign (Algorithm 2) with the 32 bytes rnd from the caller.
pub function (k: PrivateKey) sign_derand(msg: [u8], ctx: [u8], rnd: [u8]): Result[u8[], MlDsaError] {
  __check_sign(ctx, rnd)?;
  return Ok(__sign(k, msg, ctx, rnd));
}

// The deterministic variant of ML-DSA.Sign: rnd is 32 zero bytes.
pub function (k: PrivateKey) sign_deterministic(msg: [u8], ctx: [u8]): Result[u8[], MlDsaError] {
  return k.sign_derand(msg, ctx, __zero_bytes(32));
}

// The hedged ML-DSA.Sign: rnd comes from the operating system.
pub function (k: PrivateKey) sign(msg: [u8], ctx: [u8]): Result[u8[], MlDsaError] {
  let rnd = rand.bytes(32);
  let sig = k.sign_derand(msg, ctx, rnd);
  crypto.erase(rnd);
  return sig;
}
```

### Verification

```fern
<<verification>>=
// ML-DSA.Verify (Algorithm 3) and ML-DSA.Verify_internal (Algorithm 8).
pub function (k: PublicKey) verify(msg: [u8], sig: [u8], ctx: [u8]): Result[void, MlDsaError] {
  if (ctx.len() > MAX_CONTEXT_SIZE) {
    return Err(ContextLength(ctx.len()));
  }
  if (sig.len() != SIGNATURE_SIZE) {
    return Err(SignatureLength(sig.len()));
  }
  let h = __unpack_hint(sig, HINT_AT)?;
  let z = __unpack(sig, Z_AT, L * 256, 20);
  for i in 0..L * 256 {
    let x = GAMMA1 - z[i];
    if (x >= GAMMA1 - BETA || -x >= GAMMA1 - BETA) {
      return Err(ResponseRange);
    }
    z = z.with(i, __modq(x));
  }
  let mu = __mu(k.tr, msg, ctx);
  let c = __ntt(__sample_in_ball(sig[0:CTILDE]), 0);
  let w = __inv_ntt_all(__sub(__mat_vec(k.a, __ntt_all(z, L)), __scale(c, k.t1, K)), K);
  let w1 = __alloc_i32(K * 256);
  for i in 0..K * 256 {
    w1 = w1.with(i, __use_hint(h[i], w[i]));
  }
  let ctilde = crypto.shake256_new().update(mu).update(__encode_w1(w1)).reader().squeeze(CTILDE).0;
  if (!crypto.consteq(ctilde, sig[0:CTILDE])) {
    return Err(SignatureMismatch);
  }
  return Ok(());
}
```
