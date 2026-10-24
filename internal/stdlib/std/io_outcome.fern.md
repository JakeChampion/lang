# I/O failure extraction

This module reads I/O failures from `Option` and `Result` values.

## Model

The failure method extracts an optional I/O error from either an optional error or a result with a void success value.

## Contracts

Successful results produce no failure. The helper preserves the concrete I/O error rather than message text.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub function (o: Option[IoError]) failure(): Option[IoError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads I/O failures from `Option` and `Result` values.
```

### `2 pub function (o: Option[IoError]) failure(): Option[IoError]`

```fern
<<2 pub function (o: Option[IoError]) failure(): Option[IoError]>>=
pub function (o: Option[IoError]) failure(): Option[IoError] {
  return o;
}

pub function (r: Result[void, IoError]) failure(): Option[IoError] {
  if let Err(e) = r {
    return Some(e);
  }
  return None;
}
```
