# HTTP request identifiers

This module assigns an identifier to each HTTP request and response.

## Model

The wrapper obtains a request identifier from the peer or creates a fresh identifier. It makes that identifier available on the request and response.

## Contracts

Identifiers from peers pass through the module validation policy. Fresh identifiers use the supplied random callback, which also permits deterministic tests.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub const HEADER: string>>

<<4 pub function stamped[P: platform.Platform](req: HttpRequest, plat: P, next: (HttpRequest)>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module assigns an identifier to each HTTP request and response.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/http";
import "std/platform";
import "std/headers";
import "std/string";
import "std/option";
```

### `3 pub const HEADER: string`

```fern
<<3 pub const HEADER: string>>=
pub const HEADER: string = "X-Request-Id";
```

### `4 pub function stamped[P: platform.Platform](req: HttpRequest, plat: P, next: (HttpRequest)`

```fern
<<4 pub function stamped[P: platform.Platform](req: HttpRequest, plat: P, next: (HttpRequest)>>=
pub function stamped[P: platform.Platform](req: HttpRequest, plat: P, next: (HttpRequest) => Result[HttpResponse, dyn http.ToResponse]): Result[HttpResponse, dyn http.ToResponse] {
  let id: string = incoming(req).unwrap_or_else((): string => fresh((): i32 => plat.random_i32()));
  let resp: HttpResponse = http.respond(next(req.with_header(HEADER, id)));
  return Ok(resp.with_header(HEADER, id));
}

pub function of(req: HttpRequest): string {
  match (req.headers.get("x-request-id")) {
    Some(id) => {
      return id;
    },
    None => {
      return "";
    }
  }
  return "";
}

pub function incoming(req: HttpRequest): Option[string] {
  let all: string[] = req.headers.get_all("x-request-id");
  if (all.len() != 1) {
    return None;
  }
  let id: string = all[0].trim();
  if (id.len() == 0 || id.len() > 128) {
    return None;
  }
  for b in id.bytes() {
    if (b < 33 as u8 || b > 126 as u8) {
      return None;
    }
  }
  return Some(id);
}

pub function fresh(random: () => i32): string {
  let out: u8[] = [];
  let i: i32 = 0;
  while (i < 4) {
    let r: i32 = random();
    let k: i32 = 0;
    while (k < 8) {
      let shift: i32 = 28 - 4 * k;
      let nibble: i32 = r >> shift & 15;
      if (nibble < 10) {
        out = out.append((48 + nibble) as u8);
      } else {
        out = out.append((87 + nibble) as u8);
      }
      k = k + 1;
    }
    i = i + 1;
  }
  return string_from_bytes_unchecked(out);
}
```
