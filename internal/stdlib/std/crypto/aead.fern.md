# Authenticated-encryption errors

This module defines the error type for authenticated encryption.

## Model

Authenticated ciphers share one error enum. Length errors retain the observed key or nonce length. A forged message has a separate error kind.

## Contracts

The message and display methods expose the same explanation. A failed authentication must remain an error rather than a successful plaintext value.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum AeadError>>

<<4 pub function (e: AeadError) to_string(): string>>

<<5 impl error.Error for AeadError>>

<<6 pub function (e: AeadError) message(): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module defines the error type for authenticated encryption.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/int";
import "std/error";
import "core/cmp";
```

### `3 pub enum AeadError`

```fern
<<3 pub enum AeadError>>=
pub enum AeadError {
  KeyLength(i32),
  NonceLength(i32),
  Forged,
}
```

### `4 pub function (e: AeadError) to_string(): string`

```fern
<<4 pub function (e: AeadError) to_string(): string>>=
pub function (e: AeadError) to_string(): string {
  return e.message();
}
```

### `5 impl error.Error for AeadError`

```fern
<<5 impl error.Error for AeadError>>=
impl error.Error for AeadError {}

impl cmp.Display for AeadError {}
```

### `6 pub function (e: AeadError) message(): string`

```fern
<<6 pub function (e: AeadError) message(): string>>=
pub function (e: AeadError) message(): string {
  match (e) {
    KeyLength(n) => {
      return "aead: the key is " + int.int_to_string(n) + " bytes, the wrong length for this cipher";
    },
    NonceLength(n) => {
      return "aead: the nonce is " + int.int_to_string(n) + " bytes, want 12";
    },
    Forged => {
      return "aead: the message does not verify";
    }
  }
  return "";
}
```
