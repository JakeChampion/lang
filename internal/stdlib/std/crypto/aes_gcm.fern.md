# AES-GCM authenticated encryption

This module provides AES-128-GCM and AES-256-GCM authenticated encryption.

## Model

A prepared key retains the AES round keys and the GCM authentication subkey. Convenience functions accept raw key bytes and construct that state.

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

<<5 function __prepared(bytes: [u8], nonce: [u8]): Result[Key, aead.AeadError]>>

<<6 const ZERO_BLOCK: u8[]>>

<<7 pub function seal(key: [u8], nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]>>

<<8 pub function (k: Key) seal_in_place(iv: [u8], seq: u64, own buf: u8[], aad_at: i32, aad_n: i32, at: i32, n: i32): Result[u8[], aead.AeadError]>>
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

### `5 function __prepared(bytes: [u8], nonce: [u8]): Result[Key, aead.AeadError]`

```fern
<<5 function __prepared(bytes: [u8], nonce: [u8]): Result[Key, aead.AeadError]>>=
function __prepared(bytes: [u8], nonce: [u8]): Result[Key, aead.AeadError] {
  if (nonce.len() != 12) {
    return Err(aead.NonceLength(nonce.len()));
  }
  return key(bytes);
}
```

### `6 const ZERO_BLOCK: u8[]`

```fern
<<6 const ZERO_BLOCK: u8[]>>=
const ZERO_BLOCK: u8[] = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
```

### `7 pub function seal(key: [u8], nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]`

```fern
<<7 pub function seal(key: [u8], nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError]>>=
pub function seal(key: [u8], nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError] {
  return __prepared(key, nonce)?.seal(nonce, plaintext, aad);
}

pub function (k: Key) seal(nonce: [u8], plaintext: [u8], aad: [u8]): Result[u8[], aead.AeadError] {
  let n: i32 = plaintext.len();
  let a: i32 = aad.len();
  let buf: u8[] = __bytes_into(__bytes_into(__alloc_u8(n + 16 + a), 0, plaintext, 0, n), n + 16, aad, 0, a);
  buf = k.seal_in_place(nonce, 0 as u64, buf, n + 16, a, 0, n)?;
  __arr_set_len(buf, n + 16);
  return Ok(buf);
}

pub function open(key: [u8], nonce: [u8], sealed: [u8], aad: [u8]): Result[u8[], aead.AeadError] {
  return __prepared(key, nonce)?.open(nonce, sealed, aad);
}

pub function (k: Key) open(nonce: [u8], sealed: [u8], aad: [u8]): Result[u8[], aead.AeadError] {
  if (sealed.len() < 16) {
    return Err(aead.Forged);
  }
  let n: i32 = sealed.len() - 16;
  let a: i32 = aad.len();
  let buf: u8[] = __bytes_into(__bytes_into(__alloc_u8(n + 16 + a), 0, sealed, 0, n + 16), n + 16, aad, 0, a);
  buf = k.open_in_place(nonce, 0 as u64, buf, n + 16, a, 0, n)?;
  __arr_set_len(buf, n);
  return Ok(buf);
}
```

### `8 pub function (k: Key) seal_in_place(iv: [u8], seq: u64, own buf: u8[], aad_at: i32, aad_n: i32, at: i32, n: i32): Result[u8[], aead.AeadError]`

A record layer seals and opens in the buffer that carries the record. The nonce is the 12-byte `iv` with the sequence number `seq` folded into its last eight bytes, as TLS 1.3 and QUIC build it; a caller with a whole nonce passes it as `iv` with `seq` 0. `buf[aad_at:aad_at + aad_n]` is the additional data, `buf[at:at + n]` the text, and the tag sits at `buf[at + n:at + n + 16]`. A region outside the buffer aborts, as an out-of-range slice does. The buffer comes back sealed, or opened with the plaintext where the ciphertext was; an open whose tag does not verify returns `Forged` and drops the buffer.

```fern
<<8 pub function (k: Key) seal_in_place(iv: [u8], seq: u64, own buf: u8[], aad_at: i32, aad_n: i32, at: i32, n: i32): Result[u8[], aead.AeadError]>>=
pub function (k: Key) seal_in_place(iv: [u8], seq: u64, own buf: u8[], aad_at: i32, aad_n: i32, at: i32, n: i32): Result[u8[], aead.AeadError] {
  if (iv.len() != 12) {
    return Err(aead.NonceLength(iv.len()));
  }
  return Ok(__aes_gcm_seal_into(k.rk, k.h, iv, seq, buf, aad_at, aad_n, at, n));
}

pub function (k: Key) open_in_place(iv: [u8], seq: u64, own buf: u8[], aad_at: i32, aad_n: i32, at: i32, n: i32): Result[u8[], aead.AeadError] {
  if (iv.len() != 12) {
    return Err(aead.NonceLength(iv.len()));
  }
  buf = __aes_gcm_open_into(k.rk, k.h, iv, seq, buf, aad_at, aad_n, at, n);
  __ct_public(buf[at + n:at + n + 1]);
  if (buf[at + n] != 0) {
    return Err(aead.Forged);
  }
  return Ok(buf);
}
```
