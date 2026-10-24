# SHA-2 selection for signatures

This module defines the SHA-2 digest types for signature algorithms.

## Model

`Hash` selects SHA-256, SHA-384, or SHA-512. The selector supplies the digest size and the byte-digest operation.

## Contracts

Digest input is a borrowed byte view. The result owns its digest bytes. Signature modules use this selector to keep size and algorithm consistent.

## Executable example

```fern test name=std-crypto-sha2-example
import "std/crypto/sha2";

function main(): i32 {
  if (sha2.Hash.Sha256.size() != 32 || sha2.Hash.Sha512.size() != 64) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum Hash>>

<<4 pub function (h: Hash) size(): i32>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module defines the SHA-2 digest types for signature algorithms.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/crypto";
```

### `3 pub enum Hash`

```fern
<<3 pub enum Hash>>=
pub enum Hash {
  Sha256,
  Sha384,
  Sha512,
}
```

### `4 pub function (h: Hash) size(): i32`

```fern
<<4 pub function (h: Hash) size(): i32>>=
pub function (h: Hash) size(): i32 {
  match (h) {
    Sha256 => {
      return 32;
    },
    Sha384 => {
      return 48;
    },
    Sha512 => {
      return 64;
    }
  }
  return 0;
}

pub function (h: Hash) digest(msg: [u8]): u8[] {
  match (h) {
    Sha256 => {
      return crypto.sha256_new().update(msg).final_bytes();
    },
    Sha384 => {
      return crypto.sha384_new().update(msg).final_bytes();
    },
    Sha512 => {
      return crypto.sha512_new().update(msg).final_bytes();
    }
  }
  return [];
}
```
