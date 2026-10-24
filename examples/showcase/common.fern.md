# Shared HTTP vocabulary

These helpers give the applications one policy for query values, identifiers, and HTML text.
Malformed input returns `Result`. An absent optional value uses `Option`.

This document is the source program. Fern expands the named sections in `<<*>>` before compilation.
The HTML source view links each section and each local import.

```fern
<<*>>=
<<Imports>>
<<Validate request values>>
<<Create identifiers>>
<<Render HTML>>
```

## Imports

The applications use the HTTP, URL, parse, and crypto modules from the standard library.

```fern
<<Imports>>=
// Shared HTTP validation and page structure for the showcase applications.
import "std/http";
import "std/result";
import "std/option";
import "std/array";
import "std/url";
import "std/parse";
import "std/crypto";
import "std/i32";
import "std/i64";
import "../../compiler/weave";
```

## Validate request values

The server keeps the query in `req.path`. Split it before a path comparison.
Parse integers into `i64` first, then check the range before conversion to `i32`.
The `?` operator returns a typed HTTP error to the router.

```fern
<<Validate request values>>=
pub function path(req: HttpRequest): string {
  return req.path.splitn("?", 2)[0];
}

pub function query(req: HttpRequest, name: string, fallback: string): string {
  let parts: string[] = req.path.splitn("?", 2);
  if (parts.len() < 2) {
    return fallback;
  }
  match (url.query_parse(parts[1]).get(name)) {
    Some(values) => {
      if (values.len() > 0) {
        return values[0];
      }
    },
    None => {}
  }
  return fallback;
}

pub function integer(req: HttpRequest, name: string, fallback: i32, low: i32, high: i32): Result[i32, http.HttpError] {
  let value: i64 = parse.int64(query(req, name, fallback.to_string())).map_err((e: parse.IntError): http.HttpError => http.fail(400, "invalid " + name))?;
  if (value < low as i64 || value > high as i64) {
    return Err(http.fail(400, name + " is outside its range"));
  }
  return Ok(value as i32);
}
```

## Create identifiers

Room identifiers use operating system random bytes. They identify a room, not a user account.
A short allowlist prevents path separators and control characters in room names.

```fern
<<Create identifiers>>=
pub function token(): string {
  let bytes: u8[] = random_bytes(24);
  return crypto.sha256_new().update(bytes).final_hex().take(24);
}

pub function room_name(value: string): boolean {
  if (value.len() < 1 || value.len() > 32) {
    return false;
  }
  for c in value.as_bytes() {
    if (!(c >= b'a' && c <= b'z' || c >= b'0' && c <= b'9' || c == b'-' || c == b'_')) {
      return false;
    }
  }
  return true;
}
```

## Render HTML

Escape user text at the HTML boundary. The shared page function supplies navigation and accessible landmarks.
A successful form POST uses status 303 so a browser follows with GET.

```fern
<<Render HTML>>=
pub function escape(text: string): string {
  return weave.html_escape(text);
}

pub function redirect(location: string): HttpResponse {
  return HttpResponse { ...http.redirect(location), status: 303 };
}

pub function page(title: string, app: string, body: string): string {
  return "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">"
    + "<title>" + escape(title) + " · Fern, in action</title><link rel=\"stylesheet\" href=\"/assets/showcase.css\"></head>"
    + "<body class=\"app-page " + app + "-page\"><a class=\"skip-link\" href=\"#main\">Skip to content</a>"
    + "<header class=\"app-nav\"><a class=\"brand\" href=\"/\"><span class=\"brand-mark\">✳</span> fern<span class=\"brand-suffix\"> / in action</span></a>"
    + "<nav><a href=\"/\">All examples</a><a href=\"/_src/showcase/" + app + ".fern\">View source ↗</a></nav></header>"
    + "<main id=\"main\">" + body + "</main><footer class=\"app-footer\"><span>Made with Fern. Open from top to bottom.</span>"
    + "<a href=\"/_src/\">Read the source ↗</a></footer></body></html>";
}
```

## Check the behavior

This example compiles against the same sections as the application. Run it with `fern -doctest`.

```fern test name=common-example
<<Imports>>
<<Create identifiers>>
function main(): i32 {
  if (!room_name("studio-42") || room_name("../private") || room_name("")) { return 1; }
  return 0;
}
```
