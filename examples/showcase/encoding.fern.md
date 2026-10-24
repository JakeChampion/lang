# Compression across a reverse proxy

Static assets and woven lessons have Brotli, gzip, and zstd variants from the build.
The standard static server selects a variant with Accept-Encoding quality values.
Dynamic HTML and JSON use the native Brotli, gzip, and zstd encoders. SSE and WebSocket streams keep their streaming bodies.

This document is the source program. Fern expands the named sections in `<<*>>` before compilation.
The HTML source view links each section and each local import.

```fern
<<*>>=
<<Restore an explicit proxy header>>
```

## Restore an explicit proxy header

A reverse proxy can change Accept-Encoding before the application sees it.
`FERN_ORIGINAL_ACCEPT_ENCODING` selects a header that the trusted proxy preserves and overwrites.
The default header is `Netlify-Original-Accept-Encoding`. The Netlify deployment supplies the original browser value.

An absent proxy header leaves Accept-Encoding unchanged. An empty configured name disables restoration.
The outer middleware adds both header names to Vary. Precompressed responses bypass dynamic compression.

```fern
<<Restore an explicit proxy header>>=
import "std/http";
import "std/option";

pub function request(req: HttpRequest, original_header: string): HttpRequest {
  if (original_header != "") {
    if let Some(value) = req.headers.get(original_header) {
      return HttpRequest { ...req, headers: req.headers.set("Accept-Encoding", value) };
    }
  }
  return req;
}
```

## Check the behavior

This example compiles against the same sections as the application. Run it with `fern -doctest`.

```fern test name=encoding-example
<<Restore an explicit proxy header>>
function main(): i32 {
  let req = http.request("GET", "/").with_header("Accept-Encoding", "gzip").with_header("X-Original-Encoding", "br, zstd");
  let restored = request(req, "X-Original-Encoding");
  if (restored.headers.get("Accept-Encoding").unwrap_or("") != "br, zstd") { return 1; }
  return 0;
}
```
