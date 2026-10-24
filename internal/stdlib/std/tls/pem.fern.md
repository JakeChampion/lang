# PEM text blocks

This module reads and writes PEM blocks.

## Model

A PEM block combines a label and decoded bytes. Text conversion surrounds base64 content with identical boundary labels.

## Contracts

Decode validates block structure and base64 data. Certificate extraction selects certificate blocks. Trust validation occurs after the text and DER parse steps.

## Executable example

```fern test name=std-tls-pem-example
import "std/tls/pem";

function main(): i32 {
  let text = pem.encode("TEST", [1 as u8, 2 as u8]);
  let Ok(blocks) = pem.decode(text) else { return 1; };
  if (blocks.len() != 1 || blocks[0].label != "TEST" || blocks[0].bytes.len() != 2) { return 2; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub struct Block>>

<<4 pub function (e: PemError) to_string(): string>>

<<5 impl error.Error for PemError>>

<<6 pub function (e: PemError) message(): string>>

<<7 function __label(line: string, kind: string): Option[string]>>

<<8 struct Open>>

<<9 pub function decode(text: string): Result[Block[], PemError]>>

<<10 function __close(cur: Open, line: string): Result[Block, PemError]>>

<<11 pub function certificates(text: string): Result[u8[][], PemError]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads and writes PEM blocks.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/base64";
import "std/string";
import "std/error";
import "core/cmp";
```

### `3 pub struct Block`

```fern
<<3 pub struct Block>>=
pub struct Block { label: string, bytes: u8[] }

pub enum PemError {
  Unterminated(string),
  MismatchedEnd(string),
  BadBase64(string),
}
```

### `4 pub function (e: PemError) to_string(): string`

```fern
<<4 pub function (e: PemError) to_string(): string>>=
pub function (e: PemError) to_string(): string {
  return e.message();
}
```

### `5 impl error.Error for PemError`

```fern
<<5 impl error.Error for PemError>>=
impl error.Error for PemError {}

impl cmp.Display for PemError {}
```

### `6 pub function (e: PemError) message(): string`

```fern
<<6 pub function (e: PemError) message(): string>>=
pub function (e: PemError) message(): string {
  match (e) {
    Unterminated(label) => {
      return "pem: the " + label + " block has no END line";
    },
    MismatchedEnd(label) => {
      return "pem: the " + label + " block ends with another label";
    },
    BadBase64(label) => {
      return "pem: the " + label + " block is not base64";
    }
  }
  return "";
}
```

### `7 function __label(line: string, kind: string): Option[string]`

```fern
<<7 function __label(line: string, kind: string): Option[string]>>=
function __label(line: string, kind: string): Option[string] {
  match (line.strip_prefix("-----" + kind + " ")) {
    Some(rest) => {
      return rest.strip_suffix("-----");
    },
    None => {
      return None;
    }
  }
  return None;
}
```

### `8 struct Open`

```fern
<<8 struct Open>>=
struct Open { label: string, body: string, open: boolean }
```

### `9 pub function decode(text: string): Result[Block[], PemError]`

```fern
<<9 pub function decode(text: string): Result[Block[], PemError]>>=
pub function decode(text: string): Result[Block[], PemError] {
  let out: Block[] = [];
  let cur: Open = Open { label: "", body: "", open: false };
  for raw in text.lines() {
    let line: string = raw.trim();
    if (!cur.open) {
      if let Some(label) = __label(line, "BEGIN") {
        cur = Open { label: label, body: "", open: true };
      }
    } else if (line.starts_with("-----END ")) {
      out = out.append(__close(cur, line)?);
      cur = Open { label: "", body: "", open: false };
    } else {
      cur = Open { ...cur, body: cur.body + line };
    }
  }
  if (cur.open) {
    return Err(Unterminated(cur.label));
  }
  return Ok(out);
}
```

### `10 function __close(cur: Open, line: string): Result[Block, PemError]`

```fern
<<10 function __close(cur: Open, line: string): Result[Block, PemError]>>=
function __close(cur: Open, line: string): Result[Block, PemError] {
  match (__label(line, "END")) {
    Some(label) => {
      if (label != cur.label) {
        return Err(MismatchedEnd(cur.label));
      }
    },
    None => {
      return Err(MismatchedEnd(cur.label));
    }
  }
  match (base64.decode(cur.body)) {
    Ok(b) => {
      return Ok(Block { label: cur.label, bytes: b });
    },
    Err(_) => {
      return Err(BadBase64(cur.label));
    }
  }
  return Err(BadBase64(cur.label));
}
```

### `11 pub function certificates(text: string): Result[u8[][], PemError]`

```fern
<<11 pub function certificates(text: string): Result[u8[][], PemError]>>=
pub function certificates(text: string): Result[u8[][], PemError] {
  let out: u8[][] = [];
  for b in decode(text)? {
    if (b.label == "CERTIFICATE") {
      out = out.append(b.bytes);
    }
  }
  return Ok(out);
}

pub function encode(label: string, bytes: u8[]): string {
  let body: string = base64.encode(bytes);
  let out: string = "-----BEGIN " + label + "-----\n";
  let at: i32 = 0;
  while (at < body.len()) {
    out = out + body.drop(at).take(64) + "\n";
    at = at + 64;
  }
  return out + "-----END " + label + "-----\n";
}
```
