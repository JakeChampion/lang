# The AES block cipher in CBC mode

This module implements the AES block cipher of FIPS 197 for 128-bit, 192-bit and 256-bit keys, and the CBC mode of NIST SP 800-38A. It is for the formats that still use CBC, such as encrypted PEM files and OpenSSH keys with `aes256-cbc`. New designs use `std/crypto/aes_gcm`.

CBC operates on whole blocks and supplies neither padding nor a message authentication code (MAC). Ciphertext changes can go undetected. Use `std/crypto/aes_gcm` for authenticated encryption.

## Model

`cipher` expands a key into the round keys. `encrypt_block` and `decrypt_block` transform one 16-byte block. `cbc_encrypt` and `cbc_decrypt` transform a sequence of whole blocks after an initialization vector, with no padding.

Each operation runs in the AES kernel of the target, which `std/crypto/aes_gcm` also uses: the AES instructions on x86-64 and arm64, and a bitsliced cipher on WebAssembly. Each of these runs in constant time. Thus no step reads memory at an index that depends on the key or the data.

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

<<4 the key expansion>>

<<5 the block operations>>

<<6 the CBC mode>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module implements the AES block cipher (FIPS 197) and the CBC mode
// (NIST SP 800-38A), in constant time.
// CBC provides neither padding nor authentication. Use std/crypto/aes_gcm
// for authenticated encryption.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/crypto";
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

### `4 the key expansion`

`Cipher` holds the round keys, 16 bytes for each round and one more. The kernel does the expansion of FIPS 197 section 5.2.

```fern
<<4 the key expansion>>=
@secret
pub struct Cipher { keys: u8[] }

// Consumes the key and zeroes its round keys when it owns the last reference.
pub function (own c: Cipher) erase(): void {
  crypto.erase(c.keys);
}

pub function cipher(key: [u8]): Result[Cipher, AesError] {
  if (key.len() != 16 && key.len() != 24 && key.len() != 32) {
    return Err(KeyLength(key.len()));
  }
  return Ok(Cipher { keys: __aes_expand_key(key) });
}
```

### `5 the block operations`

One counter block of CTR mode with a zero text is the cipher of the counter. Thus the CTR kernel gives the cipher of one block.

```fern
<<5 the block operations>>=
// The cipher of FIPS 197 section 5.1 on the 16 bytes of block.
pub function (c: Cipher) encrypt_block(block: [u8]): u8[] {
  return __aes_ctr32(c.keys, block[0:16], ZERO_BLOCK);
}

const ZERO_BLOCK: u8[] = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0];

// The inverse cipher of FIPS 197 section 5.3 on the 16 bytes of block.
pub function (c: Cipher) decrypt_block(block: [u8]): u8[] {
  return __aes_dec_blocks(c.keys, block[0:16]);
}
```

### `6 the CBC mode`

Each plaintext block is the XOR of the decrypted block and the ciphertext block before it, or the initialization vector for the first block.

```fern
<<6 the CBC mode>>=
function __checked(key: [u8], iv: [u8], data: [u8]): Result[Cipher, AesError] {
  if (iv.len() != 16) {
    return Err(IvLength(iv.len()));
  }
  if (data.len() % 16 != 0) {
    return Err(DataLength(data.len()));
  }
  return cipher(key);
}

// Encrypts whole blocks without padding or a MAC. Use std/crypto/aes_gcm for authenticated encryption.
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

// Decrypts whole blocks without checking a MAC or removing padding. Use std/crypto/aes_gcm to detect tampering.
pub function cbc_decrypt(key: [u8], iv: [u8], data: [u8]): Result[u8[], AesError] {
  let c = __checked(key, iv, data)?;
  let out = __aes_dec_blocks(c.keys, data);
  for i in 0..out.len() {
    let before = if (i < 16) { iv[i] } else { data[i - 16] };
    out = out.with(i, out[i] ^ before);
  }
  return Ok(out);
}
```
