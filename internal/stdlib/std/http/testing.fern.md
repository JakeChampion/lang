# HTTP response assertions

This module gives the test runner assertions on an HTTP response.

## Model

Each assertion reads one part of an `HttpResponse`: the status, a header, or the body. It returns a `test.TestOutcome` for a case of `std/test`.

## Contracts

A header name matches without regard to letter case. A failed assertion names the value that it found and the value that it expected.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub function assert_status(resp: HttpResponse, expected: i32): test.TestOutcome>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module gives the test runner assertions on an HTTP response.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/test";
import "std/http";
import "std/headers";
```

### `3 pub function assert_status(resp: HttpResponse, expected: i32): test.TestOutcome`

```fern
<<3 pub function assert_status(resp: HttpResponse, expected: i32): test.TestOutcome>>=
pub function assert_status(resp: HttpResponse, expected: i32): test.TestOutcome {
  if (resp.status == expected) {
    return Pass;
  }
  return Fail("assert_status: got " + resp.status.to_string() + ", expected " + expected.to_string());
}

pub function assert_header(resp: HttpResponse, name: string, expected: string): test.TestOutcome {
  match (resp.headers.get(name)) {
    Some(v) => {
      if (v == expected) {
        return Pass;
      }
      return Fail("assert_header: " + name + " is \"" + v + "\", expected \"" + expected + "\"");
    },
    None => {
      return Fail("assert_header: no " + name + " header, expected \"" + expected + "\"");
    }
  }
}

pub function assert_no_header(resp: HttpResponse, name: string): test.TestOutcome {
  match (resp.headers.get(name)) {
    Some(v) => {
      return Fail("assert_no_header: " + name + " is \"" + v + "\", expected no " + name + " header");
    },
    None => {
      return Pass;
    }
  }
}

pub function assert_body(resp: HttpResponse, expected: string): test.TestOutcome {
  let got = resp.body_text_lossy();
  if (got == expected) {
    return Pass;
  }
  return Fail("assert_body: got \"" + got + "\", expected \"" + expected + "\"");
}
```
