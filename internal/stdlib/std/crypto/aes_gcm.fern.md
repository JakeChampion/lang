# AES-GCM authenticated encryption

This module provides AES-128-GCM and AES-256-GCM authenticated encryption.

## Model

A key is built once from its 16 or 32 bytes and retains the AES round keys and the GCM authentication subkey. Sealing and opening a message pair that key with the message's nonce.

## Contracts

Seal authenticates both the plaintext and additional data. Open validates the authentication tag. Invalid key or nonce lengths and authentication failures return AEAD errors.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct Key>>

<<4 pub function key(bytes: [u8]): Result[Key, aead.AeadError]>>

<<5 const ZERO_BLOCK: u8[]>>

<<6 pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides AES-128-GCM and AES-256-GCM authenticated encryption.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/crypto/aead";
```

### `3 pub struct Key`

```fern
<<3 pub struct Key>>=
pub struct Key { rk: u8[], h: u8[] }
```

### `4 pub function key(bytes: [u8]): Result[Key, aead.AeadError]`

```fern
<<4 pub function key(bytes: [u8]): Result[Key, aead.AeadError]>>=
pub function key(bytes: [u8]): Result[Key, aead.AeadError] {
  if (bytes.len() != 16 && bytes.len() != 32) {
    return Err(aead.KeyLength(bytes.len()));
  }
  let rk: u8[] = __aes_expand_key(bytes);
  return Ok(Key { rk: rk, h: __aes_ctr32(rk, ZERO_BLOCK, ZERO_BLOCK) });
}
```

### `5 const ZERO_BLOCK: u8[]`

```fern
<<5 const ZERO_BLOCK: u8[]>>=
const ZERO_BLOCK: u8[] = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
```

### `6 pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]`

```fern
<<6 pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]>>=
pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError] {
  if (nonce.len() != 12) {
    return Err(aead.NonceLength(nonce.len()));
  }
  return Ok(__aes_gcm_seal(k.rk, k.h, nonce, aad, plaintext));
}

pub function (k: Key) open(nonce: [u8], sealed: [u8], aad: [u8]): Result[u8[], aead.AeadError] {
  if (nonce.len() != 12) {
    return Err(aead.NonceLength(nonce.len()));
  }
  if (sealed.len() < 16) {
    return Err(aead.Forged);
  }
  let n: i32 = sealed.len() - 16;
  let out: u8[] = __aes_gcm_open(k.rk, k.h, nonce, aad, sealed[0:n]);
  let diff: u8 = 0;
  let i: i32 = 0;
  while (i < 16) {
    diff = diff | out[n + i] ^ sealed[n + i];
    i = i + 1;
  }
  let verdict: u8[] = __alloc_u8(1);
  verdict = verdict.with(0, diff);
  __ct_public(verdict);
  if (verdict[0] != 0) {
    return Err(aead.Forged);
  }
  __arr_set_len(out, n);
  return Ok(out);
}
```
