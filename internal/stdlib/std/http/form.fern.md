# URL-encoded request forms

This module reads and writes URL-encoded form fields.

## Model

A form maps each field name to an array of values. This representation preserves repeated fields instead of a single value.

## Contracts

The request parser checks the form content type and reports a concrete form error. `get` selects one value, while `get_all` returns all values.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct Form>>

<<4 pub function (e: FormError) message(): string>>

<<5 impl error.Error for FormError>>

<<6 pub function parse(req: HttpRequest): Result[Form, FormError]>>

<<7 function __is_form(ct: string): boolean>>

<<8 pub function (f: Form) get(name: string): Option[string]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads and writes URL-encoded form fields.

import "std/unicode";
// This module reads and writes URL-encoded form fields.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/http";
import "std/url";
import "std/string";
import "std/error";
import "core/cmp";
```

### `3 pub struct Form`

```fern
<<3 pub struct Form>>=
pub struct Form { fields: Map[string, string[]] }

pub enum FormError {
  NotAForm(string),
  FormBody(http.BodyError),
}
```

### `4 pub function (e: FormError) message(): string`

```fern
<<4 pub function (e: FormError) message(): string>>=
pub function (e: FormError) message(): string {
  match (e) {
    NotAForm(ct) => {
      if (ct.len() == 0) {
        return "expected a Content-Type of application/x-www-form-urlencoded";
      }
      return "expected application/x-www-form-urlencoded, got " + ct;
    },
    FormBody(why) => {
      return why.message();
    }
  }
  return "";
}

pub function (e: FormError) to_string(): string {
  return e.message();
}
```

### `5 impl error.Error for FormError`

```fern
<<5 impl error.Error for FormError>>=
impl error.Error for FormError {}

impl cmp.Display for FormError {}

impl http.ToResponse for FormError {
  function to_response(self: FormError): HttpResponse {
    match (self) {
      NotAForm(ct) => {
        if (ct.len() == 0) {
          return http.problem(415, "", "expected a Content-Type of application/x-www-form-urlencoded");
        }
        return http.problem(415, "", "expected application/x-www-form-urlencoded, got " + ct);
      },
      FormBody(e) => {
        return e.to_response();
      }
    }
    return http.problem(500, "", "");
  }
}
```

### `6 pub function parse(req: HttpRequest): Result[Form, FormError]`

```fern
<<6 pub function parse(req: HttpRequest): Result[Form, FormError]>>=
pub function parse(req: HttpRequest): Result[Form, FormError] {
  let ct: string = "";
  match (req.headers.get("content-type")) {
    Some(v) => {
      ct = v;
    },
    None => {}
  }
  if (!__is_form(ct)) {
    return Err(NotAForm(ct));
  }
  match (req.body_string()) {
    Ok(text) => {
      return Ok(Form { fields: url.query_parse(text) });
    },
    Err(e) => {
      return Err(FormBody(e));
    }
  }
  return Err(NotAForm(ct));
}
```

### `7 function __is_form(ct: string): boolean`

```fern
<<7 function __is_form(ct: string): boolean>>=
function __is_form(ct: string): boolean {
  let kind: string = ct;
  if let Some(semi) = ct.index_of(";") {
    kind = ct.slice_snap(0, semi);
  }
  return kind.trim().to_lower() == "application/x-www-form-urlencoded";
}
```

### `8 pub function (f: Form) get(name: string): Option[string]`

```fern
<<8 pub function (f: Form) get(name: string): Option[string]>>=
pub function (f: Form) get(name: string): Option[string] {
  match (f.fields.get(name)) {
    Some(vs) => {
      if (vs.len() == 0) {
        return None;
      }
      return Some(vs[0]);
    },
    None => {
      return None;
    }
  }
  return None;
}

pub function (f: Form) get_all(name: string): string[] {
  match (f.fields.get(name)) {
    Some(vs) => {
      return vs;
    },
    None => {
      return [];
    }
  }
  return [];
}

pub function (f: Form) has(name: string): boolean {
  match (f.fields.get(name)) {
    Some(vs) => {
      return true;
    },
    None => {
      return false;
    }
  }
  return false;
}
```
