# gRPC messages over HTTP/2

This module reads and writes gRPC messages, status codes, and metadata over HTTP/2.

## Model

Each gRPC message has a length prefix and a compression flag. Status and message metadata use HTTP headers and trailers.

## Contracts

The message decoder distinguishes an incomplete buffer from an invalid message. The returned remainder permits several messages in one input buffer.

## Executable example

```fern test name=std-http-grpc-example
import "std/http/grpc";

function main(): i32 {
  let bytes = grpc.frame([1 as u8, 2 as u8]);
  if (bytes.len() != 7 || grpc.NOT_FOUND != 5) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub const OK: i32>>

<<4 pub function is_grpc(req: HttpRequest): boolean>>

<<5 function __unit(unit: i32, n: i64): Option[Duration]>>

<<6 pub function frame(message: u8[]): u8[]>>

<<7 function __percent(message: string): string>>

<<8 pub function response(messages: u8[][], code: i32, message: string): HttpResponse>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads and writes gRPC messages, status codes, and metadata over HTTP/2.

import "std/unicode";
// This module reads and writes gRPC messages, status codes, and metadata over HTTP/2.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/headers";
import "std/http";
import "std/string";
import "std/time";
```

### `3 pub const OK: i32`

```fern
<<3 pub const OK: i32>>=
pub const OK: i32 = 0;

pub const CANCELLED: i32 = 1;

pub const UNKNOWN: i32 = 2;

pub const INVALID_ARGUMENT: i32 = 3;

pub const DEADLINE_EXCEEDED: i32 = 4;

pub const NOT_FOUND: i32 = 5;

pub const ALREADY_EXISTS: i32 = 6;

pub const PERMISSION_DENIED: i32 = 7;

pub const RESOURCE_EXHAUSTED: i32 = 8;

pub const FAILED_PRECONDITION: i32 = 9;

pub const ABORTED: i32 = 10;

pub const OUT_OF_RANGE: i32 = 11;

pub const UNIMPLEMENTED: i32 = 12;

pub const INTERNAL: i32 = 13;

pub const UNAVAILABLE: i32 = 14;

pub const DATA_LOSS: i32 = 15;

pub const UNAUTHENTICATED: i32 = 16;
```

### `4 pub function is_grpc(req: HttpRequest): boolean`

```fern
<<4 pub function is_grpc(req: HttpRequest): boolean>>=
pub function is_grpc(req: HttpRequest): boolean {
  match (req.headers.get("content-type")) {
    Some(ct) => {
      let lower: string = ct.to_lower();
      if (lower == "application/grpc") {
        return true;
      }
      return lower.starts_with("application/grpc+") || lower.starts_with("application/grpc;");
    },
    None => {
      return false;
    }
  }
  return false;
}

pub function timeout(h: HeaderMap): Option[Duration] {
  match (h.get("grpc-timeout")) {
    Some(v) => {
      let b: u8[] = v.bytes();
      if (b.len() < 2 || b.len() > 9) {
        return None;
      }
      let n: i64 = 0 as i64;
      let i: i32 = 0;
      while (i < b.len() - 1) {
        let d: i32 = b[i] as i32;
        if (d < 48 || d > 57) {
          return None;
        }
        n = n * (10 as i64) + (d - 48) as i64;
        i = i + 1;
      }
      return __unit(b[b.len() - 1] as i32, n);
    },
    None => {
      return None;
    }
  }
  return None;
}
```

### `5 function __unit(unit: i32, n: i64): Option[Duration]`

```fern
<<5 function __unit(unit: i32, n: i64): Option[Duration]>>=
function __unit(unit: i32, n: i64): Option[Duration] {
  if (unit == 72) {
    return Some(time.duration_nanos(n * (3600000000000 as i64)));
  }
  if (unit == 77) {
    return Some(time.duration_nanos(n * (60000000000 as i64)));
  }
  if (unit == 83) {
    return Some(time.duration_nanos(n * (1000000000 as i64)));
  }
  if (unit == 109) {
    return Some(time.duration_nanos(n * (1000000 as i64)));
  }
  if (unit == 117) {
    return Some(time.duration_nanos(n * (1000 as i64)));
  }
  if (unit == 110) {
    return Some(time.duration_nanos(n));
  }
  return None;
}
```

### `6 pub function frame(message: u8[]): u8[]`

```fern
<<6 pub function frame(message: u8[]): u8[]>>=
pub function frame(message: u8[]): u8[] {
  let n: i32 = message.len();
  let head: u8[] = [0 as u8, (n >> 24 & 255) as u8, (n >> 16 & 255) as u8, (n >> 8 & 255) as u8, (n & 255) as u8];
  return head.concat(message);
}

pub function next_message(buffered: u8[]): Result[Option[(u8[], u8[])], string] {
  if (buffered.len() < 5) {
    return Ok(None);
  }
  if (buffered[0] != 0 as u8) {
    return Err("compressed message");
  }
  if (buffered[1] as i32 >= 128) {
    return Err("message length past 2 GiB");
  }
  let n: i32 = buffered[1] as i32 << 24 | buffered[2] as i32 << 16 | buffered[3] as i32 << 8 | buffered[4] as i32;
  if (buffered.len() < 5 + n) {
    return Ok(None);
  }
  let message: u8[] = [];
  let i: i32 = 5;
  while (i < 5 + n) {
    message = message.append(buffered[i]);
    i = i + 1;
  }
  let rest: u8[] = [];
  while (i < buffered.len()) {
    rest = rest.append(buffered[i]);
    i = i + 1;
  }
  return Ok(Some((message, rest)));
}

pub function headers(): HeaderMap {
  return headers.header_map_new().append("Content-Type", "application/grpc");
}

pub function status(code: i32, message: string): HeaderMap {
  let out: HeaderMap = headers.header_map_new().append("Grpc-Status", code.to_string());
  if (message.len() == 0) {
    return out;
  }
  return out.append("Grpc-Message", __percent(message));
}
```

### `7 function __percent(message: string): string`

```fern
<<7 function __percent(message: string): string>>=
function __percent(message: string): string {
  let out: string = "";
  for b in message.bytes() {
    let c: i32 = b as i32;
    if (c == 37 || c < 32 || c > 126) {
      out = out + "%" + __hex_byte(c);
    } else {
      out = out + string_from_bytes_unchecked([b]);
    }
  }
  return out;
}

function __hex_byte(c: i32): string {
  let digits: string = "0123456789ABCDEF";
  return string_from_bytes_unchecked([digits[c >> 4 & 15], digits[c & 15]]);
}
```

### `8 pub function response(messages: u8[][], code: i32, message: string): HttpResponse`

```fern
<<8 pub function response(messages: u8[][], code: i32, message: string): HttpResponse>>=
pub function response(messages: u8[][], code: i32, message: string): HttpResponse {
  let body: u8[] = [];
  for m in messages {
    body = body.concat(frame(m));
  }
  return HttpResponse { status: 200, body: BodyBytes(body), headers: headers(), trailers: status(code, message), background: None };
}

pub function error(code: i32, message: string): HttpResponse {
  return response([], code, message);
}
```
