# RSA signatures and encryption

This module verifies RSA signatures and encrypts with RSA-OAEP under RFC 8017.

## Model

A public key stores the modulus, exponent, and encoded size. Construction validates these integer inputs before signature verification. A private key adds the private exponent and the Montgomery form of the modulus.

## Contracts

PKCS#1 v1.5 and PSS are separate verification methods on the public key. Both use an explicit SHA-2 selector and return false for an invalid signature. OAEP encryption takes its seed from the caller. OAEP decryption does not branch or index on a secret value, and all of its failures give one error.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum RsaError>>

<<4 pub function (e: RsaError) to_string(): string>>

<<5 impl error.Error for RsaError>>

<<6 pub function (e: RsaError) message(): string>>

<<7 pub struct PublicKey>>

<<8 function __from_be(b: [u8]): bigint.BigInt>>

<<9 pub function public_key(modulus: [u8], exponent: [u8]): Result[PublicKey, RsaError]>>

<<10 function __modpow(b: bigint.BigInt, e: bigint.BigInt, n: bigint.BigInt): bigint.BigInt>>

<<11 pub function (key: PublicKey) verify_pkcs1v15(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean>>

<<12 function __mgf1(hash: sha2.Hash, seed: [u8], len: i32): u8[]>>

<<13 pub function (key: PublicKey) verify_pss(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean>>

<<14 pub struct PrivateKey>>

<<15 constant-time exponentiation>>

<<16 OAEP>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module verifies RSA signatures and encrypts with RSA-OAEP under RFC 8017.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/bigint";
import "std/crypto/sha2";
import "std/crypto/montgomery";
import "std/hex";
import "core/error";
import "core/cmp";
```

### `3 pub enum RsaError`

```fern
<<3 pub enum RsaError>>=
pub enum RsaError {
  ModulusTooSmall(i32),
  ModulusTooLarge(i32),
  EvenModulus,
  BadExponent,
  InvalidPrime,
  InvalidModulus,
  InvalidExponents,
  MessageTooLong,
  Decryption,
}
```

### `4 pub function (e: RsaError) to_string(): string`

```fern
<<4 pub function (e: RsaError) to_string(): string>>=
pub function (e: RsaError) to_string(): string {
  return e.message();
}
```

### `5 impl error.Error for RsaError`

```fern
<<5 impl error.Error for RsaError>>=
impl error.Error for RsaError {}

impl cmp.Display for RsaError {}
```

### `6 pub function (e: RsaError) message(): string`

```fern
<<6 pub function (e: RsaError) message(): string>>=
pub function (e: RsaError) message(): string {
  match (e) {
    ModulusTooSmall(bits) => {
      return "rsa: the modulus is " + bits.to_string() + " bits, want at least 1024";
    },
    ModulusTooLarge(bits) => {
      return "rsa: the modulus is " + bits.to_string() + " bits, want at most 16384";
    },
    EvenModulus => {
      return "rsa: the modulus is even";
    },
    BadExponent => {
      return "rsa: the exponent must be odd, at least 3 and at most 31 bits";
    },
    InvalidPrime => {
      return "rsa: a prime of the private key is 1 or less";
    },
    InvalidModulus => {
      return "rsa: the product of the primes is not the modulus";
    },
    InvalidExponents => {
      return "rsa: d * e is not 1 modulo p - 1 and q - 1";
    },
    MessageTooLong => {
      return "rsa: the message is too long for the key";
    },
    Decryption => {
      return "rsa: decryption error";
    }
  }
  return "";
}
```

### `7 pub struct PublicKey`

```fern
<<7 pub struct PublicKey>>=
pub struct PublicKey { n: bigint.BigInt, e: bigint.BigInt, bytes: i32, bits: i32 }
```

### `8 function __from_be(b: [u8]): bigint.BigInt`

```fern
<<8 function __from_be(b: [u8]): bigint.BigInt>>=
function __from_be(b: [u8]): bigint.BigInt {
  let v = bigint.zero();
  let i = 0;
  while (i < b.len()) {
    v = v.shl(8).add(bigint.from_u64(b[i] as u64));
    i += 1;
  }
  return v;
}

function __to_be(v: bigint.BigInt, len: i32): u8[] {
  let out = __alloc_u8(len);
  let i = 0;
  while (i < len) {
    let w = v.word64(i / 8);
    out = out.with(len - 1 - i, (w >> (i % 8 * 8) as u64 & 255) as u8);
    i += 1;
  }
  return out;
}
```

### `9 pub function public_key(modulus: [u8], exponent: [u8]): Result[PublicKey, RsaError]`

```fern
<<9 pub function public_key(modulus: [u8], exponent: [u8]): Result[PublicKey, RsaError]>>=
pub function public_key(modulus: [u8], exponent: [u8]): Result[PublicKey, RsaError] {
  let n = __from_be(modulus);
  let e = __from_be(exponent);
  let bits = n.bit_length();
  if (bits < 1024) {
    return Err(ModulusTooSmall(bits));
  }
  if (bits > 16384) {
    return Err(ModulusTooLarge(bits));
  }
  if (!n.bit(0)) {
    return Err(EvenModulus);
  }
  if (!e.bit(0) || e.bit_length() < 2 || e.bit_length() > 31) {
    return Err(BadExponent);
  }
  return Ok(PublicKey { n: n, e: e, bytes: (bits + 7) / 8, bits: bits });
}
```

### `10 function __modpow(b: bigint.BigInt, e: bigint.BigInt, n: bigint.BigInt): bigint.BigInt`

```fern
<<10 function __modpow(b: bigint.BigInt, e: bigint.BigInt, n: bigint.BigInt): bigint.BigInt>>=
function __modpow(b: bigint.BigInt, e: bigint.BigInt, n: bigint.BigInt): bigint.BigInt {
  let r = bigint.from_u64(1 as u64);
  let i = e.bit_length() - 1;
  while (i >= 0) {
    r = r.mul(r).divmod(n).1;
    if (e.bit(i)) {
      r = r.mul(b).divmod(n).1;
    }
    i -= 1;
  }
  return r;
}

function __open(key: PublicKey, sig: [u8], len: i32): Option[u8[]] {
  if (sig.len() != key.bytes) {
    return None;
  }
  let s = __from_be(sig);
  if (s.cmp(key.n) >= 0) {
    return None;
  }
  let m = __modpow(s, key.e, key.n);
  if (m.bit_length() > len * 8) {
    return None;
  }
  return Some(__to_be(m, len));
}

function __digest_info(h: sha2.Hash): string {
  match (h) {
    sha2.Sha256 => {
      return "3031300d060960864801650304020105000420";
    },
    sha2.Sha384 => {
      return "3041300d060960864801650304020205000430";
    },
    sha2.Sha512 => {
      return "3051300d060960864801650304020305000440";
    }
  }
  return "";
}

function __unhex(s: string): u8[] {
  let out = __alloc_u8(s.len() / 2);
  let i = 0;
  while (i < s.len() / 2) {
    out = out.with(i, (__nibble(s[2 * i] as i32) * 16 + __nibble(s[2 * i + 1] as i32)) as u8);
    i += 1;
  }
  return out;
}

function __nibble(c: i32): i32 {
  if (c >= 97) {
    return c - 87;
  }
  return c - 48;
}

function __range(b: [u8], from: i32, len: i32): u8[] {
  let out = __alloc_u8(len);
  let i = 0;
  while (i < len) {
    out = out.with(i, b[from + i]);
    i += 1;
  }
  return out;
}

function __same(a: [u8], b: [u8]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  let i = 0;
  while (i < a.len()) {
    if (a[i] != b[i]) {
      return false;
    }
    i += 1;
  }
  return true;
}

function __pkcs1_em(hash: sha2.Hash, msg: [u8], len: i32): Option[u8[]] {
  let info = __unhex(__digest_info(hash));
  let digest = hash.digest(msg);
  let t = info.len() + digest.len();
  if (len < t + 11) {
    return None;
  }
  let em = __alloc_u8(len);
  let ps_end = len - t - 1;
  let i = 0;
  while (i < ps_end) {
    em = em.with(i, 255 as u8);
    i += 1;
  }
  em = em.with(0, 0 as u8).with(1, 1 as u8).with(ps_end, 0 as u8);
  i = 0;
  while (i < t) {
    if (i < info.len()) {
      em = em.with(ps_end + 1 + i, info[i]);
    } else {
      em = em.with(ps_end + 1 + i, digest[i - info.len()]);
    }
    i += 1;
  }
  return Some(em);
}
```

### `11 pub function (key: PublicKey) verify_pkcs1v15(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean`

```fern
<<11 pub function (key: PublicKey) verify_pkcs1v15(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean>>=
pub function (key: PublicKey) verify_pkcs1v15(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean {
  match (__open(key, sig, key.bytes)) {
    Some(got) => {
      match (__pkcs1_em(hash, msg, key.bytes)) {
        Some(want) => {
          return __same(got, want);
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

### `12 function __mgf1(hash: sha2.Hash, seed: [u8], len: i32): u8[]`

```fern
<<12 function __mgf1(hash: sha2.Hash, seed: [u8], len: i32): u8[]>>=
function __mgf1(hash: sha2.Hash, seed: [u8], len: i32): u8[] {
  let out = __alloc_u8(len);
  let at = 0;
  let counter = 0;
  let input = __alloc_u8(seed.len() + 4);
  let i = 0;
  while (i < seed.len()) {
    input = input.with(i, seed[i]);
    i += 1;
  }
  while (at < len) {
    let c = seed.len();
    input = input.with(c, (counter >> 24 & 255) as u8).with(c + 1, (counter >> 16 & 255) as u8).with(c + 2, (counter >> 8 & 255) as u8).with(c + 3, (counter & 255) as u8);
    let block = hash.digest(input);
    let j = 0;
    while (j < block.len() && at < len) {
      out = out.with(at, block[j]);
      at += 1;
      j += 1;
    }
    counter += 1;
  }
  return out;
}

function __pss_salt(hash: sha2.Hash, em: [u8], h: [u8], top: i32, s_len: i32): Option[u8[]] {
  let db_len = em.len() - h.len() - 1;
  let mask = __mgf1(hash, h, db_len);
  let db = __alloc_u8(db_len);
  let i = 0;
  while (i < db_len) {
    db = db.with(i, em[i] ^ mask[i]);
    i += 1;
  }
  db = db.with(0, (db[0] as i32 & 255 >> top) as u8);
  let ps = db_len - s_len - 1;
  i = 0;
  while (i < ps) {
    if (db[i] != 0 as u8) {
      return None;
    }
    i += 1;
  }
  if (db[ps] != 1 as u8) {
    return None;
  }
  return Some(__range(db, ps + 1, s_len));
}

function __pss_hash(hash: sha2.Hash, m_hash: [u8], salt: [u8]): u8[] {
  let m2 = __alloc_u8(8 + m_hash.len() + salt.len());
  let i = 0;
  while (i < m2.len()) {
    if (i < 8) {
      m2 = m2.with(i, 0 as u8);
    } else if (i < 8 + m_hash.len()) {
      m2 = m2.with(i, m_hash[i - 8]);
    } else {
      m2 = m2.with(i, salt[i - 8 - m_hash.len()]);
    }
    i += 1;
  }
  return hash.digest(m2);
}
```

### `13 pub function (key: PublicKey) verify_pss(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean`

```fern
<<13 pub function (key: PublicKey) verify_pss(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean>>=
pub function (key: PublicKey) verify_pss(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean {
  let em_bits = key.bits - 1;
  let em_len = (em_bits + 7) / 8;
  let top = 8 * em_len - em_bits;
  let m_hash = hash.digest(msg);
  let h_len = m_hash.len();
  match (__open(key, sig, em_len)) {
    Some(em) => {
      if (em_len < 2 * h_len + 2 || em[em_len - 1] != 188 as u8 || (em[0] as i32 & (255 >> top ^ 255)) != 0) {
        return false;
      }
      let h = __range(em, em_len - h_len - 1, h_len);
      match (__pss_salt(hash, em, h, top, h_len)) {
        Some(salt) => {
          return __same(__pss_hash(hash, m_hash, salt), h);
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

### `14 pub struct PrivateKey`

A private key keeps its public key and the values of the Chinese remainder theorem (CRT): the Montgomery forms of p and q, dp = d mod (p - 1), dq = d mod (q - 1), and qinv = q^-1 mod p. It also keeps the Montgomery form of the modulus n for the check of each decryption. Construction checks the key as Go's `rsa.PrivateKey.Validate` does, and computes the CRT values. That work uses `std/bigint`, which is not constant-time, so a caller marks the key secret after construction.

```fern
<<14 pub struct PrivateKey>>=
// The CRT values are big-endian bytes: p and q, dp and dq with the length
// of p and q, and qinv with the length of p.
pub struct PrivateKey {
  public: PublicKey,
  m: montgomery.Modulus,
  p: u8[],
  q: u8[],
  p_bits: i32,
  q_bits: i32,
  dp: u8[],
  dq: u8[],
  qinv: u8[],
}

function __one(): bigint.BigInt {
  return bigint.from_u64(1 as u64);
}

// d * e is 1 modulo prime - 1.
function __inverse_mod(d: bigint.BigInt, e: bigint.BigInt, prime: bigint.BigInt): boolean {
  return d.mul(e).divmod(prime.sub(__one())).1.cmp(__one()) == 0;
}

// The private key of an RSA key pair with two primes.
pub function private_key(modulus: [u8], exponent: [u8], d: [u8], p: [u8], q: [u8]): Result[PrivateKey, RsaError] {
  let public = public_key(modulus, exponent)?;
  let bp = __from_be(p);
  let bq = __from_be(q);
  if (bp.cmp(__one()) <= 0 || bq.cmp(__one()) <= 0) {
    return Err(InvalidPrime);
  }
  if (bp.mul(bq).cmp(public.n) != 0) {
    return Err(InvalidModulus);
  }
  let bd = __from_be(d);
  if (!__inverse_mod(bd, public.e, bp) || !__inverse_mod(bd, public.e, bq)) {
    return Err(InvalidExponents);
  }
  let m = montgomery.modulus(hex.encode(modulus), public.bytes);
  let p_bits = bp.bit_length();
  let q_bits = bq.bit_length();
  let p_bytes = __to_be(bp, (p_bits + 7) / 8);
  let q_bytes = __to_be(bq, (q_bits + 7) / 8);
  let mp = montgomery.modulus_of(p_bytes, p_bits);
  let dp = __to_be(bd.divmod(bp.sub(__one())).1, p_bytes.len());
  let dq = __to_be(bd.divmod(bq.sub(__one())).1, q_bytes.len());
  let qinv = montgomery.to_bytes(mp, montgomery.inv(mp, montgomery.from_bytes(mp, q_bytes)));
  return Ok(PrivateKey { public: public, m: m, p: p_bytes, q: q_bytes, p_bits: p_bits, q_bits: q_bits, dp: dp, dq: dq, qinv: qinv });
}

// The secret values of the key, for a caller that marks them secret under
// the constant-time gate after construction: p, q, dp, dq and qinv.
pub function (k: PrivateKey) secrets(): u8[][] {
  return [k.p, k.q, k.dp, k.dq, k.qinv];
}

pub function (k: PrivateKey) public_key(): PublicKey {
  return k.public;
}
```

### `15 constant-time exponentiation`

The private exponent is secret. The exponentiation reads d four bits at a time from a table of 16 powers. It selects each table entry with a mask, so no branch and no index depends on d. The public exponent takes the plain square-and-multiply.

```fern
<<15 constant-time exponentiation>>=
// 1 when a == b, else 0, with no branch.
function __eq_mask(a: i64, b: i64): i64 {
  return (a ^ b) - 1 as i64 >> 63 & 1 as i64;
}

function __pow_secret(m: montgomery.Modulus, base: [i64], d: [u8]): i64[] {
  let table: i64[][] = [m.one];
  let i = 1;
  while (i < 16) {
    table = table.append(montgomery.mul(m, table[i - 1], base));
    i += 1;
  }
  let r = m.one;
  for byte in d {
    let shift = 4;
    while (shift >= 0) {
      r = montgomery.mul(m, r, r);
      r = montgomery.mul(m, r, r);
      r = montgomery.mul(m, r, r);
      r = montgomery.mul(m, r, r);
      let nibble = (byte >> shift as u8 & 15 as u8) as i64;
      let pick = table[0];
      let k = 1;
      while (k < 16) {
        pick = montgomery.select(pick, table[k], __eq_mask(k as i64, nibble));
        k += 1;
      }
      r = montgomery.mul(m, r, pick);
      shift -= 4;
    }
  }
  return r;
}

// c^d mod n by the CRT, as Go's crypto/rsa computes it:
//   m1 = c^dp mod p, m2 = c^dq mod q, h = qinv (m1 - m2) mod p, m = m2 + h q.
// Each value of the steps is secret. As Go does, the result is checked: m^e
// must be c again, so a fault in the computation gives an error and no
// value. Only the verdict of the check is public.
function __decrypt(key: PrivateKey, c: [u8]): Result[u8[], RsaError] {
  let mp = montgomery.modulus_of(key.p, key.p_bits);
  let mq = montgomery.modulus_of(key.q, key.q_bits);
  let m1 = __pow_secret(mp, montgomery.from_bytes(mp, c), key.dp);
  let m2 = montgomery.to_bytes(mq, __pow_secret(mq, montgomery.from_bytes(mq, c), key.dq));
  let diff = montgomery.sub(mp, m1, montgomery.from_bytes(mp, m2));
  let h = montgomery.to_bytes(mp, montgomery.mul(mp, diff, montgomery.from_bytes(mp, key.qinv)));
  let hq = montgomery.mul(key.m, montgomery.plain(key.m, h), montgomery.from_bytes(key.m, key.q));
  let m = montgomery.add(key.m, hq, montgomery.plain(key.m, m2));
  let again = montgomery.to_bytes(key.m, __pow_public(key.m, montgomery.mul(key.m, m, key.m.r2), key.public.e));
  let diff_c = 0 as u8;
  let i = 0;
  while (i < c.len()) {
    diff_c = diff_c | again[i] ^ c[i];
    i += 1;
  }
  let verdict = __alloc_u8(1).with(0, diff_c);
  __ct_public(verdict);
  if (verdict[0] != 0) {
    return Err(Decryption);
  }
  return Ok(montgomery.plain_bytes(key.m, m));
}

function __pow_public(m: montgomery.Modulus, base: [i64], e: bigint.BigInt): i64[] {
  let r = m.one;
  let i = e.bit_length() - 1;
  while (i >= 0) {
    r = montgomery.mul(m, r, r);
    if (e.bit(i)) {
      r = montgomery.mul(m, r, base);
    }
    i -= 1;
  }
  return r;
}
```

### `16 OAEP`

RSAES-OAEP (RFC 8017 section 7.1) with MGF1 over the same digest. `encrypt_oaep` takes the seed from the caller, as the deterministic entry points of `std/crypto/mlkem768` do. The seed has the length of the digest. `decrypt_oaep` checks the padding with no branch on its bytes. Only the verdict and the start of the message become public.

```fern
<<16 OAEP>>=
function __xor_into(own a: u8[], b: [u8]): u8[] {
  let i = 0;
  while (i < a.len()) {
    a = a.with(i, a[i] ^ b[i]);
    i += 1;
  }
  return a;
}

// RSAES-OAEP-ENCRYPT of msg under the label, with a seed of hash.size() bytes.
pub function (key: PublicKey) encrypt_oaep(hash: sha2.Hash, seed: [u8], msg: [u8], label: [u8]): Result[u8[], RsaError] {
  let k = key.bytes;
  let h_len = hash.size();
  if (msg.len() > k - 2 * h_len - 2 || seed.len() != h_len) {
    return Err(MessageTooLong);
  }
  let db = __alloc_u8(k - h_len - 1);
  let l_hash = hash.digest(label);
  let i = 0;
  while (i < h_len) {
    db = db.with(i, l_hash[i]);
    i += 1;
  }
  let at = db.len() - msg.len();
  db = db.with(at - 1, 1 as u8);
  i = 0;
  while (i < msg.len()) {
    db = db.with(at + i, msg[i]);
    i += 1;
  }
  let masked_db = __xor_into(db, __mgf1(hash, seed, k - h_len - 1));
  let masked_seed = __xor_into(__range(seed, 0, h_len), __mgf1(hash, masked_db, h_len));
  let em = __alloc_u8(k);
  i = 0;
  while (i < h_len) {
    em = em.with(1 + i, masked_seed[i]);
    i += 1;
  }
  i = 0;
  while (i < masked_db.len()) {
    em = em.with(1 + h_len + i, masked_db[i]);
    i += 1;
  }
  let m = montgomery.modulus(hex.encode(__to_be(key.n, k)), k);
  let c = __pow_public(m, montgomery.from_bytes(m, em), key.e);
  return Ok(montgomery.to_bytes(m, c));
}

// 1 when b is 0, else 0, with no branch.
function __zero_mask(b: u8): i64 {
  return b as i64 - 1 as i64 >> 63 & 1 as i64;
}

// The checks of EM after the decryption: the first byte is 0, the label
// hash matches, and a 1 ends the zero padding. The result is the verdict
// and the index of the message, and both are public.
function __oaep_unpad(em: [u8], l_hash: [u8]): (boolean, i32) {
  let h_len = l_hash.len();
  let good = __zero_mask(em[0]);
  let i = 0;
  while (i < h_len) {
    good &= __zero_mask(em[1 + h_len + i] ^ l_hash[i]);
    i += 1;
  }
  let looking = 1 as i64;
  let index = 0 as i64;
  i = 1 + 2 * h_len;
  while (i < em.len()) {
    let zero = __zero_mask(em[i]);
    let one = __zero_mask(em[i] ^ 1 as u8);
    index |= 0 as i64 - (looking & one) & i as i64;
    looking &= zero;
    i += 1;
  }
  good &= 1 as i64 - looking;
  let verdict = __alloc_u8(5).with(0, good as u8).with(1, (index & 255 as i64) as u8).with(2, (index >> 8 & 255 as i64) as u8).with(3, (index >> 16 & 255 as i64) as u8).with(4, (index >> 24 & 255 as i64) as u8);
  __ct_public(verdict);
  return (verdict[0] == 1 as u8, verdict[1] as i32 | verdict[2] as i32 << 8 | verdict[3] as i32 << 16 | verdict[4] as i32 << 24);
}

// RSAES-OAEP-DECRYPT of c under the label.
pub function (key: PrivateKey) decrypt_oaep(hash: sha2.Hash, c: [u8], label: [u8]): Result[u8[], RsaError] {
  let k = key.public.bytes;
  let h_len = hash.size();
  if (c.len() != k || k < 2 * h_len + 2 || __from_be(c).cmp(key.public.n) >= 0) {
    return Err(Decryption);
  }
  let em = __decrypt(key, c)?;
  let seed = __xor_into(__range(em, 1, h_len), __mgf1(hash, __range(em, 1 + h_len, k - h_len - 1), h_len));
  let db = __xor_into(__range(em, 1 + h_len, k - h_len - 1), __mgf1(hash, seed, k - h_len - 1));
  let full = __alloc_u8(k).with(0, em[0]);
  let i = 0;
  while (i < db.len()) {
    full = full.with(1 + h_len + i, db[i]);
    i += 1;
  }
  let (good, index) = __oaep_unpad(full, hash.digest(label));
  if (!good) {
    return Err(Decryption);
  }
  return Ok(__range(full, index + 1, k - index - 1));
}
```
