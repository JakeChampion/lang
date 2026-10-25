# The common error interface

This module defines a common supertype for error values.

## Model

`Error` supplies a message for a concrete error value. The runtime's builtin errors, `IoError` and the `StreamFault` a byte stream ends with, implement this interface and the display interface.

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

<<5 pub function (f: StreamFault) message(): string>>
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

### `5 pub function (f: StreamFault) message(): string`

```fern
<<5 pub function (f: StreamFault) message(): string>>=
pub function (f: StreamFault) message(): string {
  match (f) {
    StreamTooLarge => {
      return "the body is past the limit";
    },
    StreamFramingTooLarge => {
      return "the body's framing is past the limit";
    },
    StreamTimeout => {
      return "the body arrived too slowly";
    },
    StreamMalformed => {
      return "the body was malformed";
    },
    StreamCutShort => {
      return "the body was cut short";
    },
    StreamCancelled => {
      return "the request was cancelled before its body arrived";
    }
  }
  return "";
}

pub function (f: StreamFault) to_string(): string {
  return f.message();
}

impl Error for StreamFault {}
```
