# The AES block cipher in CBC mode

This module implements the AES block cipher of FIPS 197 for 128-bit, 192-bit and 256-bit keys, and the CBC mode of NIST SP 800-38A. It is for the formats that still use CBC, such as encrypted PEM files and OpenSSH keys with `aes256-cbc`. New designs use `std/crypto/aes_gcm`.

## Model

`cipher` expands a key into the round keys. `encrypt_block` and `decrypt_block` transform one 16-byte block. `cbc_encrypt` and `cbc_decrypt` transform a sequence of whole blocks after an initialization vector, with no padding.

The S-box is computed, not read from a table: the inverse in GF(2^8) is x^254, and then the affine map of FIPS 197 section 5.1.1 applies. Thus no step reads memory at an index that depends on the key or the data.

## Contracts

A key of a length other than 16, 24 or 32 bytes gives `KeyLength`. An initialization vector of a length other than 16 bytes gives `IvLength`. Data that is not a whole number of blocks gives `DataLength`.

The operations run in constant time: the key and the data are not used in a branch or as an index. The module does not mark its inputs secret. The caller does that.

## Executable example

```fern test name=std-crypto-aes-example
import "std/crypto/aes";

function main(): i32 {
  let key = __alloc_u8(16);
  let iv = __alloc_u8(16);
  let data = "sixteen bytes!!!".as_bytes();
  let Ok(sealed) = aes.cbc_encrypt(key, iv, data) else { return 1; };
  let Ok(opened) = aes.cbc_decrypt(key, iv, sealed) else { return 2; };
  if (opened.len() != 16 || opened[0] != b's' || opened[15] != b'!') { return 3; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum AesError>>

<<4 the field GF(2^8)>>

<<5 the key expansion>>

<<6 the rounds>>

<<7 the CBC mode>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module implements the AES block cipher (FIPS 197) and the CBC mode
// (NIST SP 800-38A), in constant time.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/error";
import "core/cmp";
```

### `3 pub enum AesError`

Each length error keeps the length that the caller gave.

```fern
<<3 pub enum AesError>>=
pub enum AesError {
  KeyLength(i32),
  IvLength(i32),
  DataLength(i32),
}

impl error.Error for AesError {}

impl cmp.Display for AesError {}

pub function (e: AesError) to_string(): string {
  return e.message();
}

pub function (e: AesError) message(): string {
  return match (e) {
    KeyLength(n) => "aes: the key is " + n.to_string() + " bytes, want 16, 24 or 32",
    IvLength(n) => "aes: the initialization vector is " + n.to_string() + " bytes, want 16",
    DataLength(n) => "aes: the data is " + n.to_string() + " bytes, not a multiple of 16",
  };
}
```

### `4 the field GF(2^8)`

The product reduces by the polynomial x^8 + x^4 + x^3 + x + 1 with masks, not with a branch. `__inverse` raises x to the power 254 with 7 squares and 6 products. Each of x^3, x^7, x^15, x^31, x^63 and x^127 takes a square and a product. The last square gives x^254. The inverse of 0 is 0, as FIPS 197 requires.

```fern
<<4 the field GF(2^8)>>=
function __mul(a: i32, b: i32): i32 {
  let p = 0;
  let x = a;
  let y = b;
  for _ in 0..8 {
    p ^= x & 0 - (y & 1);
    x = (x << 1 ^ 0x1b & 0 - (x >> 7 & 1)) & 255;
    y = y >> 1;
  }
  return p;
}

function __inverse(x: i32): i32 {
  let t = __mul(__mul(x, x), x);
  for _ in 0..5 {
    t = __mul(__mul(t, t), x);
  }
  return __mul(t, t);
}

function __rotl8(x: i32, n: i32): i32 {
  return (x << n | x >> 8 - n) & 255;
}

function __sbox(x: i32): i32 {
  let b = __inverse(x);
  return b ^ __rotl8(b, 1) ^ __rotl8(b, 2) ^ __rotl8(b, 3) ^ __rotl8(b, 4) ^ 0x63;
}

function __inverse_sbox(y: i32): i32 {
  return __inverse(__rotl8(y, 1) ^ __rotl8(y, 3) ^ __rotl8(y, 6) ^ 0x05);
}
```

### `5 the key expansion`

`Cipher` holds the number of rounds and the round keys, 16 bytes for each round and one more. The expansion is FIPS 197 section 5.2.

```fern
<<5 the key expansion>>=
pub struct Cipher { rounds: i32, keys: u8[] }

pub function cipher(key: [u8]): Result[Cipher, AesError] {
  let nk = key.len() / 4;
  if (key.len() != 16 && key.len() != 24 && key.len() != 32) {
    return Err(KeyLength(key.len()));
  }
  let rounds = nk + 6;
  let total = 16 * (rounds + 1);
  let w = __bytes_into(__alloc_u8(total), 0, key, 0, key.len());
  let rcon = 1;
  let i = nk;
  while (4 * i < total) {
    let t0 = w[4 * i - 4] as i32;
    let t1 = w[4 * i - 3] as i32;
    let t2 = w[4 * i - 2] as i32;
    let t3 = w[4 * i - 1] as i32;
    if (i % nk == 0) {
      let r0 = __sbox(t1) ^ rcon;
      let r1 = __sbox(t2);
      let r2 = __sbox(t3);
      let r3 = __sbox(t0);
      t0 = r0;
      t1 = r1;
      t2 = r2;
      t3 = r3;
      rcon = __mul(rcon, 2);
    } else if (nk > 6 && i % nk == 4) {
      t0 = __sbox(t0);
      t1 = __sbox(t1);
      t2 = __sbox(t2);
      t3 = __sbox(t3);
    }
    let at = 4 * i;
    w = w.with(at, (w[at - 4 * nk] as i32 ^ t0) as u8).with(at + 1, (w[at + 1 - 4 * nk] as i32 ^ t1) as u8);
    w = w.with(at + 2, (w[at + 2 - 4 * nk] as i32 ^ t2) as u8).with(at + 3, (w[at + 3 - 4 * nk] as i32 ^ t3) as u8);
    i += 1;
  }
  return Ok(Cipher { rounds: rounds, keys: w });
}
```

### `6 the rounds`

The state is 16 bytes in the order of FIPS 197: byte r + 4c is row r of column c. ShiftRows moves row r left by r columns. MixColumns multiplies each column by the matrix of section 5.1.3, and InvMixColumns by its inverse of section 5.3.3.

```fern
<<6 the rounds>>=
function __add_key(own s: u8[], keys: [u8], round: i32): u8[] {
  for i in 0..16 {
    s = s.with(i, s[i] ^ keys[16 * round + i]);
  }
  return s;
}

function __sub_shift(s: [u8]): u8[] {
  let out = __alloc_u8(16);
  for c in 0..4 {
    for r in 0..4 {
      out = out.with(r + 4 * c, __sbox(s[r + 4 * ((c + r) % 4)] as i32) as u8);
    }
  }
  return out;
}

function __inverse_sub_shift(s: [u8]): u8[] {
  let out = __alloc_u8(16);
  for c in 0..4 {
    for r in 0..4 {
      out = out.with(r + 4 * ((c + r) % 4), __inverse_sbox(s[r + 4 * c] as i32) as u8);
    }
  }
  return out;
}

function __mix(own s: u8[]): u8[] {
  for c in 0..4 {
    let a0 = s[4 * c] as i32;
    let a1 = s[4 * c + 1] as i32;
    let a2 = s[4 * c + 2] as i32;
    let a3 = s[4 * c + 3] as i32;
    s = s.with(4 * c, (__mul(a0, 2) ^ __mul(a1, 3) ^ a2 ^ a3) as u8).with(4 * c + 1, (a0 ^ __mul(a1, 2) ^ __mul(a2, 3) ^ a3) as u8);
    s = s.with(4 * c + 2, (a0 ^ a1 ^ __mul(a2, 2) ^ __mul(a3, 3)) as u8).with(4 * c + 3, (__mul(a0, 3) ^ a1 ^ a2 ^ __mul(a3, 2)) as u8);
  }
  return s;
}

function __inverse_mix(own s: u8[]): u8[] {
  for c in 0..4 {
    let a0 = s[4 * c] as i32;
    let a1 = s[4 * c + 1] as i32;
    let a2 = s[4 * c + 2] as i32;
    let a3 = s[4 * c + 3] as i32;
    s = s.with(4 * c, (__mul(a0, 14) ^ __mul(a1, 11) ^ __mul(a2, 13) ^ __mul(a3, 9)) as u8).with(4 * c + 1, (__mul(a0, 9) ^ __mul(a1, 14) ^ __mul(a2, 11) ^ __mul(a3, 13)) as u8);
    s = s.with(4 * c + 2, (__mul(a0, 13) ^ __mul(a1, 9) ^ __mul(a2, 14) ^ __mul(a3, 11)) as u8).with(4 * c + 3, (__mul(a0, 11) ^ __mul(a1, 13) ^ __mul(a2, 9) ^ __mul(a3, 14)) as u8);
  }
  return s;
}

// The cipher of FIPS 197 section 5.1 on the 16 bytes of block.
pub function (c: Cipher) encrypt_block(block: [u8]): u8[] {
  let s = __add_key(__bytes_into(__alloc_u8(16), 0, block, 0, 16), c.keys, 0);
  for round in 1..c.rounds {
    s = __add_key(__mix(__sub_shift(s)), c.keys, round);
  }
  return __add_key(__sub_shift(s), c.keys, c.rounds);
}

// The inverse cipher of FIPS 197 section 5.3 on the 16 bytes of block.
pub function (c: Cipher) decrypt_block(block: [u8]): u8[] {
  let s = __add_key(__bytes_into(__alloc_u8(16), 0, block, 0, 16), c.keys, c.rounds);
  let round = c.rounds - 1;
  while (round > 0) {
    s = __inverse_mix(__add_key(__inverse_sub_shift(s), c.keys, round));
    round -= 1;
  }
  return __add_key(__inverse_sub_shift(s), c.keys, 0);
}
```

### `7 the CBC mode`

Each plaintext block is the XOR of the decrypted block and the ciphertext block before it, or the initialization vector for the first block.

```fern
<<7 the CBC mode>>=
function __checked(key: [u8], iv: [u8], data: [u8]): Result[Cipher, AesError] {
  if (iv.len() != 16) {
    return Err(IvLength(iv.len()));
  }
  if (data.len() % 16 != 0) {
    return Err(DataLength(data.len()));
  }
  return cipher(key);
}

pub function cbc_encrypt(key: [u8], iv: [u8], data: [u8]): Result[u8[], AesError] {
  let c = __checked(key, iv, data)?;
  let out = __alloc_u8(data.len());
  let prev = __bytes_into(__alloc_u8(16), 0, iv, 0, 16);
  let at = 0;
  while (at < data.len()) {
    for i in 0..16 {
      prev = prev.with(i, prev[i] ^ data[at + i]);
    }
    prev = c.encrypt_block(prev);
    out = __bytes_into(out, at, prev, 0, 16);
    at += 16;
  }
  return Ok(out);
}

pub function cbc_decrypt(key: [u8], iv: [u8], data: [u8]): Result[u8[], AesError] {
  let c = __checked(key, iv, data)?;
  let out = __alloc_u8(data.len());
  let at = 0;
  while (at < data.len()) {
    let plain = c.decrypt_block(data[at:at + 16]);
    for i in 0..16 {
      let before = if (at == 0) { iv[i] } else { data[at - 16 + i] };
      out = out.with(at + i, plain[i] ^ before);
    }
    at += 16;
  }
  return Ok(out);
}
```
