# The DES and Triple DES block ciphers in CBC mode

This module implements the DES block cipher of FIPS 46-3 and Triple DES (TDEA, NIST SP 800-67) with three keys, and the CBC mode of NIST SP 800-38A. It is for old formats that still use them, such as encrypted PEM files with `DES-CBC` or `DES-EDE3-CBC`. DES is not secure, and Triple DES is deprecated: new designs do not use them.

## Model

`cipher` expands an 8-byte key into 16 round keys. `encrypt_block` and `decrypt_block` transform one 8-byte block. A 24-byte key is three DES keys: a Triple DES encryption is the encryption with the first key, the decryption with the second and the encryption with the third. `cbc_encrypt` and `cbc_decrypt` take an 8-byte or a 24-byte key and transform a sequence of whole blocks after an 8-byte initialization vector, with no padding.

## Contracts

A key of a length other than 8 or 24 bytes gives `KeyLength`. An initialization vector of a length other than 8 bytes gives `IvLength`. Data that is not a whole number of blocks gives `DataLength`. The parity bits of a key are not checked, as in Go's crypto/des.

The operations run in constant time. An S-box lookup reads each of the 64 entries of the box. A mask keeps the entry that matches, so no index depends on the key or the data. The module does not mark its inputs secret. The caller does that.

## Executable example

```fern test name=std-crypto-des-example
import "std/crypto/des";

function main(): i32 {
  let key = __alloc_u8(24).with(0, 1u8).with(8, 2u8).with(16, 3u8);
  let iv = __alloc_u8(8);
  let data = "eight by".as_bytes();
  let Ok(sealed) = des.cbc_encrypt(key, iv, data) else { return 1; };
  let Ok(opened) = des.cbc_decrypt(key, iv, sealed) else { return 2; };
  if (opened.len() != 8 || opened[0] != b'e' || opened[7] != b'y') { return 3; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum DesError>>

<<4 the tables>>

<<5 the key schedule>>

<<6 the rounds>>

<<7 the CBC mode>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module implements DES (FIPS 46-3), Triple DES (NIST SP 800-67) and
// the CBC mode (NIST SP 800-38A), in constant time.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/error";
import "core/cmp";
```

### `3 pub enum DesError`

Each length error keeps the length that the caller gave.

```fern
<<3 pub enum DesError>>=
pub enum DesError {
  KeyLength(i32),
  IvLength(i32),
  DataLength(i32),
}

impl error.Error for DesError {}

impl cmp.Display for DesError {}

pub function (e: DesError) to_string(): string {
  return e.message();
}

pub function (e: DesError) message(): string {
  return match (e) {
    KeyLength(n) => "des: the key is " + n.to_string() + " bytes, want 8 or 24",
    IvLength(n) => "des: the initialization vector is " + n.to_string() + " bytes, want 8",
    DataLength(n) => "des: the data is " + n.to_string() + " bytes, not a multiple of 8",
  };
}
```

### `4 the tables`

The tables of FIPS 46-3. A permutation names the input bits from 1, the most significant bit, in the order of the output. Each S-box has 4 rows of 16 values. The row is the first and the last bit of the 6 input bits, and the column is the four bits between them.

```fern
<<4 the tables>>=
const IP: i32[] = [58, 50, 42, 34, 26, 18, 10, 2, 60, 52, 44, 36, 28, 20, 12, 4, 62, 54, 46, 38, 30, 22, 14, 6, 64, 56, 48, 40, 32, 24, 16, 8, 57, 49, 41, 33, 25, 17, 9, 1, 59, 51, 43, 35, 27, 19, 11, 3, 61, 53, 45, 37, 29, 21, 13, 5, 63, 55, 47, 39, 31, 23, 15, 7];

const FP: i32[] = [40, 8, 48, 16, 56, 24, 64, 32, 39, 7, 47, 15, 55, 23, 63, 31, 38, 6, 46, 14, 54, 22, 62, 30, 37, 5, 45, 13, 53, 21, 61, 29, 36, 4, 44, 12, 52, 20, 60, 28, 35, 3, 43, 11, 51, 19, 59, 27, 34, 2, 42, 10, 50, 18, 58, 26, 33, 1, 41, 9, 49, 17, 57, 25];

const E: i32[] = [32, 1, 2, 3, 4, 5, 4, 5, 6, 7, 8, 9, 8, 9, 10, 11, 12, 13, 12, 13, 14, 15, 16, 17, 16, 17, 18, 19, 20, 21, 20, 21, 22, 23, 24, 25, 24, 25, 26, 27, 28, 29, 28, 29, 30, 31, 32, 1];

const P: i32[] = [16, 7, 20, 21, 29, 12, 28, 17, 1, 15, 23, 26, 5, 18, 31, 10, 2, 8, 24, 14, 32, 27, 3, 9, 19, 13, 30, 6, 22, 11, 4, 25];

const PC1: i32[] = [57, 49, 41, 33, 25, 17, 9, 1, 58, 50, 42, 34, 26, 18, 10, 2, 59, 51, 43, 35, 27, 19, 11, 3, 60, 52, 44, 36, 63, 55, 47, 39, 31, 23, 15, 7, 62, 54, 46, 38, 30, 22, 14, 6, 61, 53, 45, 37, 29, 21, 13, 5, 28, 20, 12, 4];

const PC2: i32[] = [14, 17, 11, 24, 1, 5, 3, 28, 15, 6, 21, 10, 23, 19, 12, 4, 26, 8, 16, 7, 27, 20, 13, 2, 41, 52, 31, 37, 47, 55, 30, 40, 51, 45, 33, 48, 44, 49, 39, 56, 34, 53, 46, 42, 50, 36, 29, 32];

const SHIFTS: i32[] = [1, 1, 2, 2, 2, 2, 2, 2, 1, 2, 2, 2, 2, 2, 2, 1];

const SBOXES: i32[] = [
  14, 4, 13, 1, 2, 15, 11, 8, 3, 10, 6, 12, 5, 9, 0, 7,
  0, 15, 7, 4, 14, 2, 13, 1, 10, 6, 12, 11, 9, 5, 3, 8,
  4, 1, 14, 8, 13, 6, 2, 11, 15, 12, 9, 7, 3, 10, 5, 0,
  15, 12, 8, 2, 4, 9, 1, 7, 5, 11, 3, 14, 10, 0, 6, 13,
  15, 1, 8, 14, 6, 11, 3, 4, 9, 7, 2, 13, 12, 0, 5, 10,
  3, 13, 4, 7, 15, 2, 8, 14, 12, 0, 1, 10, 6, 9, 11, 5,
  0, 14, 7, 11, 10, 4, 13, 1, 5, 8, 12, 6, 9, 3, 2, 15,
  13, 8, 10, 1, 3, 15, 4, 2, 11, 6, 7, 12, 0, 5, 14, 9,
  10, 0, 9, 14, 6, 3, 15, 5, 1, 13, 12, 7, 11, 4, 2, 8,
  13, 7, 0, 9, 3, 4, 6, 10, 2, 8, 5, 14, 12, 11, 15, 1,
  13, 6, 4, 9, 8, 15, 3, 0, 11, 1, 2, 12, 5, 10, 14, 7,
  1, 10, 13, 0, 6, 9, 8, 7, 4, 15, 14, 3, 11, 5, 2, 12,
  7, 13, 14, 3, 0, 6, 9, 10, 1, 2, 8, 5, 11, 12, 4, 15,
  13, 8, 11, 5, 6, 15, 0, 3, 4, 7, 2, 12, 1, 10, 14, 9,
  10, 6, 9, 0, 12, 11, 7, 13, 15, 1, 3, 14, 5, 2, 8, 4,
  3, 15, 0, 6, 10, 1, 13, 8, 9, 4, 5, 11, 12, 7, 2, 14,
  2, 12, 4, 1, 7, 10, 11, 6, 8, 5, 3, 15, 13, 0, 14, 9,
  14, 11, 2, 12, 4, 7, 13, 1, 5, 0, 15, 10, 3, 9, 8, 6,
  4, 2, 1, 11, 10, 13, 7, 8, 15, 9, 12, 5, 6, 3, 0, 14,
  11, 8, 12, 7, 1, 14, 2, 13, 6, 15, 0, 9, 10, 4, 5, 3,
  12, 1, 10, 15, 9, 2, 6, 8, 0, 13, 3, 4, 14, 7, 5, 11,
  10, 15, 4, 2, 7, 12, 9, 5, 6, 1, 13, 14, 0, 11, 3, 8,
  9, 14, 15, 5, 2, 8, 12, 3, 7, 0, 4, 10, 1, 13, 11, 6,
  4, 3, 2, 12, 9, 5, 15, 10, 11, 14, 1, 7, 6, 0, 8, 13,
  4, 11, 2, 14, 15, 0, 8, 13, 3, 12, 9, 7, 5, 10, 6, 1,
  13, 0, 11, 7, 4, 9, 1, 10, 14, 3, 5, 12, 2, 15, 8, 6,
  1, 4, 11, 13, 12, 3, 7, 14, 10, 15, 6, 8, 0, 5, 9, 2,
  6, 11, 13, 8, 1, 4, 10, 7, 9, 5, 0, 15, 14, 2, 3, 12,
  13, 2, 8, 4, 6, 15, 11, 1, 10, 9, 3, 14, 5, 0, 12, 7,
  1, 15, 13, 8, 10, 3, 7, 4, 12, 5, 6, 11, 0, 14, 9, 2,
  7, 11, 4, 1, 9, 12, 14, 2, 0, 6, 10, 13, 15, 3, 5, 8,
  2, 1, 14, 7, 4, 10, 8, 13, 15, 12, 9, 0, 3, 5, 6, 11,
];

// The bits of x, a value of width bits, in the order of the table.
function __permute(x: u64, table: [i32], width: i32): u64 {
  let out = 0u64;
  for p in table {
    out = out << 1u64 | x >> (width - p) as u64 & 1u64;
  }
  return out;
}
```

### `5 the key schedule`

The schedule of FIPS 46-3. PC-1 gives two halves of 28 bits, and each round rotates both halves left by its shift. PC-2 takes the 48 bits of the round key.

```fern
<<5 the key schedule>>=
pub struct Cipher { keys: u64[] }

function __load(b: [u8], at: i32): u64 {
  let v = 0u64;
  for i in 0..8 {
    v = v << 8u64 | b[at + i] as u64;
  }
  return v;
}

function __store(own out: u8[], at: i32, v: u64): u8[] {
  for i in 0..8 {
    out = out.with(at + i, (v >> (56 - 8 * i) as u64 & 255u64) as u8);
  }
  return out;
}

function __rotl28(x: u64, n: i32): u64 {
  return (x << n as u64 | x >> (28 - n) as u64) & 0x0fffffffu64;
}

function __schedule(key: [u8], at: i32): Cipher {
  let cd = __permute(__load(key, at), PC1, 64);
  let c = cd >> 28u64;
  let d = cd & 0x0fffffffu64;
  let keys: u64[] = [];
  for s in SHIFTS {
    c = __rotl28(c, s);
    d = __rotl28(d, s);
    keys = keys.append(__permute(c << 28u64 | d, PC2, 56));
  }
  return Cipher { keys: keys };
}

pub function cipher(key: [u8]): Result[Cipher, DesError] {
  if (key.len() != 8) {
    return Err(KeyLength(key.len()));
  }
  return Ok(__schedule(key, 0));
}
```

### `6 the rounds`

The function f of FIPS 46-3. E expands the right half to 48 bits, and the round key joins it with XOR. The eight S-boxes take 6 bits each, and P permutes their 32 bits. A decryption runs the rounds with the keys in the reverse order.

```fern
<<6 the rounds>>=
// One S-box on a 6-bit value, with a mask for each of its 64 entries.
function __sbox(box: i32, v: u64): u64 {
  let out = 0u64;
  for k in 0..64 {
    let row = k >> 4 & 2 | k & 1;
    let col = k >> 1 & 15;
    let hit = 0u64 - ((v ^ k as u64) - 1u64 >> 63u64);
    out |= SBOXES[64 * box + 16 * row + col] as u64 & hit;
  }
  return out;
}

function __f(r: u64, k: u64): u64 {
  let x = __permute(r, E, 32) ^ k;
  let s = 0u64;
  for box in 0..8 {
    s = s << 4u64 | __sbox(box, x >> (42 - 6 * box) as u64 & 63u64);
  }
  return __permute(s, P, 32);
}

function __crypt(c: Cipher, block: u64, decrypt: boolean): u64 {
  let x = __permute(block, IP, 64);
  let l = x >> 32u64;
  let r = x & 0xffffffffu64;
  for i in 0..16 {
    let k = if (decrypt) { c.keys[15 - i] } else { c.keys[i] };
    let next = l ^ __f(r, k);
    l = r;
    r = next;
  }
  return __permute(r << 32u64 | l, FP, 64);
}

pub function (c: Cipher) encrypt_block(block: [u8]): u8[] {
  return __store(__alloc_u8(8), 0, __crypt(c, __load(block, 0), false));
}

pub function (c: Cipher) decrypt_block(block: [u8]): u8[] {
  return __store(__alloc_u8(8), 0, __crypt(c, __load(block, 0), true));
}
```

### `7 the CBC mode`

The ciphers of a key: one for DES, three for Triple DES. Each plaintext block is the XOR of the decrypted block and the ciphertext block before it, or the initialization vector for the first block.

```fern
<<7 the CBC mode>>=
function __ciphers(key: [u8]): Result[Cipher[], DesError] {
  if (key.len() == 8) {
    return Ok([__schedule(key, 0)]);
  }
  if (key.len() == 24) {
    return Ok([__schedule(key, 0), __schedule(key, 8), __schedule(key, 16)]);
  }
  return Err(KeyLength(key.len()));
}

function __checked(key: [u8], iv: [u8], data: [u8]): Result[Cipher[], DesError] {
  if (iv.len() != 8) {
    return Err(IvLength(iv.len()));
  }
  if (data.len() % 8 != 0) {
    return Err(DataLength(data.len()));
  }
  return __ciphers(key);
}

function __encrypt_with(cs: [Cipher], x: u64): u64 {
  if (cs.len() == 1) {
    return __crypt(cs[0], x, false);
  }
  return __crypt(cs[2], __crypt(cs[1], __crypt(cs[0], x, false), true), false);
}

function __decrypt_with(cs: [Cipher], x: u64): u64 {
  if (cs.len() == 1) {
    return __crypt(cs[0], x, true);
  }
  return __crypt(cs[0], __crypt(cs[1], __crypt(cs[2], x, true), false), true);
}

pub function cbc_encrypt(key: [u8], iv: [u8], data: [u8]): Result[u8[], DesError] {
  let cs = __checked(key, iv, data)?;
  let out = __alloc_u8(data.len());
  let prev = __load(iv, 0);
  let at = 0;
  while (at < data.len()) {
    prev = __encrypt_with(cs, prev ^ __load(data, at));
    out = __store(out, at, prev);
    at += 8;
  }
  return Ok(out);
}

pub function cbc_decrypt(key: [u8], iv: [u8], data: [u8]): Result[u8[], DesError] {
  let cs = __checked(key, iv, data)?;
  let out = __alloc_u8(data.len());
  let prev = __load(iv, 0);
  let at = 0;
  while (at < data.len()) {
    let c = __load(data, at);
    out = __store(out, at, __decrypt_with(cs, c) ^ prev);
    prev = c;
    at += 8;
  }
  return Ok(out);
}
```
