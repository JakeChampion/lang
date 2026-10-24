# The common error interface

This module defines a common supertype for error values.

## Model

`Error` supplies a message for a concrete error value. The runtime `IoError` implements this interface and the display interface.

## Contracts

Typed errors retain their data while callers can request a common explanation. Message text does not replace the concrete error value in a result.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub trait Error>>

<<3 pub function (e: IoError) message(): string>>

<<4 impl Error for IoError>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module defines a common supertype for error values.
```

### `2 pub trait Error`

```fern
<<2 pub trait Error>>=
pub trait Error {
  function message(self: Self): string;
}
```

### `3 pub function (e: IoError) message(): string`

```fern
<<3 pub function (e: IoError) message(): string>>=
pub function (e: IoError) message(): string {
  match (e) {
    NotFound(_) => {
      return "not found";
    },
    PermissionDenied(_) => {
      return "permission denied";
    },
    AlreadyExists(_) => {
      return "already exists";
    },
    InvalidUtf8(_) => {
      return "invalid UTF-8";
    },
    Interrupted => {
      return "interrupted";
    },
    Unsupported => {
      return "unsupported";
    },
    Other(_, msg, _) => {
      return msg;
    }
  }
  return "";
}

pub function (e: IoError) to_string(): string {
  return e.message();
}
```

### `4 impl Error for IoError`

```fern
<<4 impl Error for IoError>>=
impl Error for IoError {}
```
