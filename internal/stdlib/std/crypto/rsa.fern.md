# RSA signature verification

This module validates RSA signatures under RFC 8017.

## Model

A public key stores the modulus, exponent, and encoded size. Construction validates these integer inputs before signature verification.

## Contracts

PKCS#1 v1.5 and PSS are separate verification methods on the public key. Both use an explicit SHA-2 selector and return false for an invalid signature.

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
```

### `module introduction`

```fern
<<module introduction>>=
// This module validates RSA signatures under RFC 8017.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/bigint";
import "core/int";
import "std/crypto/sha2";
import "std/error";
import "core/cmp";
```

### `3 pub enum RsaError`

```fern
<<3 pub enum RsaError>>=
pub enum RsaError {
  ModulusTooSmall(i32),
  ModulusTooLarge(i32),
  BadExponent,
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
      return "rsa: the modulus is " + int.int_to_string(bits) + " bits, want at least 1024";
    },
    ModulusTooLarge(bits) => {
      return "rsa: the modulus is " + int.int_to_string(bits) + " bits, want at most 16384";
    },
    BadExponent => {
      return "rsa: the exponent must be odd, at least 3 and at most 64 bits";
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
  let v: bigint.BigInt = bigint.zero();
  let i: i32 = 0;
  while (i < b.len()) {
    v = v.shl(8).add(bigint.from_u64(b[i] as u64));
    i = i + 1;
  }
  return v;
}

function __to_be(v: bigint.BigInt, len: i32): u8[] {
  let out: u8[] = __alloc_u8(len);
  let i: i32 = 0;
  while (i < len) {
    let w: u64 = v.word64(i / 8);
    out = out.with(len - 1 - i, (w >> (i % 8 * 8) as u64 & 255) as u8);
    i = i + 1;
  }
  return out;
}
```

### `9 pub function public_key(modulus: [u8], exponent: [u8]): Result[PublicKey, RsaError]`

```fern
<<9 pub function public_key(modulus: [u8], exponent: [u8]): Result[PublicKey, RsaError]>>=
pub function public_key(modulus: [u8], exponent: [u8]): Result[PublicKey, RsaError] {
  let n: bigint.BigInt = __from_be(modulus);
  let e: bigint.BigInt = __from_be(exponent);
  let bits: i32 = n.bit_length();
  if (bits < 1024) {
    return Err(ModulusTooSmall(bits));
  }
  if (bits > 16384) {
    return Err(ModulusTooLarge(bits));
  }
  if (!e.bit(0) || e.bit_length() < 2 || e.bit_length() > 64) {
    return Err(BadExponent);
  }
  return Ok(PublicKey { n: n, e: e, bytes: (bits + 7) / 8, bits: bits });
}
```

### `10 function __modpow(b: bigint.BigInt, e: bigint.BigInt, n: bigint.BigInt): bigint.BigInt`

```fern
<<10 function __modpow(b: bigint.BigInt, e: bigint.BigInt, n: bigint.BigInt): bigint.BigInt>>=
function __modpow(b: bigint.BigInt, e: bigint.BigInt, n: bigint.BigInt): bigint.BigInt {
  let r: bigint.BigInt = bigint.from_u64(1 as u64);
  let i: i32 = e.bit_length() - 1;
  while (i >= 0) {
    r = r.mul(r).divmod(n).1;
    if (e.bit(i)) {
      r = r.mul(b).divmod(n).1;
    }
    i = i - 1;
  }
  return r;
}

function __open(key: PublicKey, sig: [u8], len: i32): Option[u8[]] {
  if (sig.len() != key.bytes) {
    return None;
  }
  let s: bigint.BigInt = __from_be(sig);
  if (s.cmp(key.n) >= 0) {
    return None;
  }
  let m: bigint.BigInt = __modpow(s, key.e, key.n);
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
  let out: u8[] = __alloc_u8(s.len() / 2);
  let i: i32 = 0;
  while (i < s.len() / 2) {
    out = out.with(i, (__nibble(s[2 * i] as i32) * 16 + __nibble(s[2 * i + 1] as i32)) as u8);
    i = i + 1;
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
  let out: u8[] = __alloc_u8(len);
  let i: i32 = 0;
  while (i < len) {
    out = out.with(i, b[from + i]);
    i = i + 1;
  }
  return out;
}

function __same(a: [u8], b: [u8]): boolean {
  if (a.len() != b.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < a.len()) {
    if (a[i] != b[i]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function __pkcs1_em(hash: sha2.Hash, msg: [u8], len: i32): Option[u8[]] {
  let info: u8[] = __unhex(__digest_info(hash));
  let digest: u8[] = hash.digest(msg);
  let t: i32 = info.len() + digest.len();
  if (len < t + 11) {
    return None;
  }
  let em: u8[] = __alloc_u8(len);
  let ps_end: i32 = len - t - 1;
  let i: i32 = 0;
  while (i < ps_end) {
    em = em.with(i, 255 as u8);
    i = i + 1;
  }
  em = em.with(0, 0 as u8).with(1, 1 as u8).with(ps_end, 0 as u8);
  i = 0;
  while (i < t) {
    if (i < info.len()) {
      em = em.with(ps_end + 1 + i, info[i]);
    } else {
      em = em.with(ps_end + 1 + i, digest[i - info.len()]);
    }
    i = i + 1;
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
  let out: u8[] = __alloc_u8(len);
  let at: i32 = 0;
  let counter: i32 = 0;
  let input: u8[] = __alloc_u8(seed.len() + 4);
  let i: i32 = 0;
  while (i < seed.len()) {
    input = input.with(i, seed[i]);
    i = i + 1;
  }
  while (at < len) {
    let c: i32 = seed.len();
    input = input.with(c, (counter >> 24 & 255) as u8).with(c + 1, (counter >> 16 & 255) as u8).with(c + 2, (counter >> 8 & 255) as u8).with(c + 3, (counter & 255) as u8);
    let block: u8[] = hash.digest(input);
    let j: i32 = 0;
    while (j < block.len() && at < len) {
      out = out.with(at, block[j]);
      at = at + 1;
      j = j + 1;
    }
    counter = counter + 1;
  }
  return out;
}

function __pss_salt(hash: sha2.Hash, em: [u8], h: [u8], top: i32, s_len: i32): Option[u8[]] {
  let db_len: i32 = em.len() - h.len() - 1;
  let mask: u8[] = __mgf1(hash, h, db_len);
  let db: u8[] = __alloc_u8(db_len);
  let i: i32 = 0;
  while (i < db_len) {
    db = db.with(i, em[i] ^ mask[i]);
    i = i + 1;
  }
  db = db.with(0, (db[0] as i32 & 255 >> top) as u8);
  let ps: i32 = db_len - s_len - 1;
  i = 0;
  while (i < ps) {
    if (db[i] != 0 as u8) {
      return None;
    }
    i = i + 1;
  }
  if (db[ps] != 1 as u8) {
    return None;
  }
  return Some(__range(db, ps + 1, s_len));
}

function __pss_hash(hash: sha2.Hash, m_hash: [u8], salt: [u8]): u8[] {
  let m2: u8[] = __alloc_u8(8 + m_hash.len() + salt.len());
  let i: i32 = 0;
  while (i < m2.len()) {
    if (i < 8) {
      m2 = m2.with(i, 0 as u8);
    } else if (i < 8 + m_hash.len()) {
      m2 = m2.with(i, m_hash[i - 8]);
    } else {
      m2 = m2.with(i, salt[i - 8 - m_hash.len()]);
    }
    i = i + 1;
  }
  return hash.digest(m2);
}
```

### `13 pub function (key: PublicKey) verify_pss(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean`

```fern
<<13 pub function (key: PublicKey) verify_pss(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean>>=
pub function (key: PublicKey) verify_pss(hash: sha2.Hash, msg: [u8], sig: [u8]): boolean {
  let em_bits: i32 = key.bits - 1;
  let em_len: i32 = (em_bits + 7) / 8;
  let top: i32 = 8 * em_len - em_bits;
  let m_hash: u8[] = hash.digest(msg);
  let h_len: i32 = m_hash.len();
  match (__open(key, sig, em_len)) {
    Some(em) => {
      if (em_len < 2 * h_len + 2 || em[em_len - 1] != 188 as u8 || (em[0] as i32 & (255 >> top ^ 255)) != 0) {
        return false;
      }
      let h: u8[] = __range(em, em_len - h_len - 1, h_len);
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
