# CSV fields and documents

This module reads and writes CSV fields, records, and documents under RFC 4180.

```fern
<<*>>=
// This module reads and writes CSV fields, records, and documents under RFC 4180.

import "std/string";
import "std/error";
import "core/cmp";
import "std/i32";

<<errors>>

<<escape>>

<<join>>

<<parse_line>>

<<parse>>

<<__parse>>

<<serialize>>
```

## Errors and byte offsets

Every parse operation returns a `Result` with a concrete `ParseError`.
The error retains its kind and its byte offset from zero.
An unclosed quote reports the offset immediately after the input.
These offsets count UTF-8 bytes, rather than characters.

```fern
<<errors>>=
@derive(cmp.Eq)
pub enum ParseErrorKind {
  UnexpectedQuote,
  UnexpectedByteAfterQuote(u8),
  UnclosedQuote,
  UnexpectedRecordTerminator,
}

// offset counts bytes from zero. An unclosed quote reports the end of the input.
@derive(cmp.Eq)
pub struct ParseError { offset: i32, kind: ParseErrorKind }

impl error.Error for ParseError {}

impl cmp.Display for ParseError {}

// message returns the display text for this error.
pub function (e: ParseError) message(): string {
  return e.to_string();
}

// to_string identifies the byte offset and the error condition.
pub function (e: ParseError) to_string(): string {
  let reason: string = "invalid CSV";
  match (e.kind) {
    ParseErrorKind.UnexpectedQuote => {
      reason = "quote inside an unquoted field";
    },
    ParseErrorKind.UnexpectedByteAfterQuote(byte) => {
      reason = "unexpected byte after a closing quote: " + (byte as i32).to_string();
    },
    ParseErrorKind.UnclosedQuote => {
      reason = "unclosed quoted field";
    },
    ParseErrorKind.UnexpectedRecordTerminator => {
      reason = "unexpected record terminator";
    }
  }
  return "CSV byte " + e.offset.to_string() + ": " + reason;
}
```

## Field output

A field needs quotes when it contains a comma, a quote, CR, or LF. Each quote inside that field becomes two quotes.
The function returns other fields without a copy.

```fern
<<escape>>=
// escape quotes a field when a CSV delimiter requires quotes.
pub function escape(s: string): string {
  if (!s.contains(",") && !s.contains("\"")
    && !s.contains("\n") && !s.contains("\r")) {
    return s;
  }
  return "\"" + s.replace("\"", "\"\"") + "\"";
}
```

## Record output

The first pass calculates the exact output size. The second pass writes the fields and separators into one byte buffer.
This allocation avoids repeated copies of a record that grows.

```fern
<<join>>=
// join escapes each field and separates the fields with commas.
pub function join(arr: string[]): string {
  let n: i32 = arr.len();
  if (n == 0) {
    return "";
  }
  let total: i32 = 0;
  let i: i32 = 0;
  while (i < n) {
    if (i > 0) {
      total = total + 1;
    }
    total = total + escape(arr[i]).len();
    i = i + 1;
  }
  let buf: u8[] = __alloc_u8(total);
  let pos: i32 = 0;
  i = 0;
  while (i < n) {
    if (i > 0) {
      buf = buf.with(pos, 44 as u8);
      pos = pos + 1;
    }
    let f: string = escape(arr[i]);
    let fl: i32 = f.len();
    let j: i32 = 0;
    while (j < fl) {
      buf = buf.with(pos, f[j] as u8);
      pos = pos + 1;
      j = j + 1;
    }
    i = i + 1;
  }
  return string_from_bytes_unchecked(buf);
}
```

## One record

`parse_line` accepts one record. A quoted field can contain CR or LF, but a record separator outside quotes is an error.
Empty input represents one empty field.

```fern
<<parse_line>>=
// parse_line decodes one record, with one empty field for empty input.
pub function parse_line(s: string): Result[string[], ParseError] {
  let rows: string[][] = __parse(s, true)?;
  return Ok(rows[0]);
}
```

## A complete document

`parse` accepts CR, LF, and CRLF separators. A separator at the end does not add an empty row.
Empty input has no rows. Rows can have different field counts.

```fern
<<parse>>=
// parse decodes a document with CR, LF, or CRLF record separators.
pub function parse(s: string): Result[string[][], ParseError] {
  return __parse(s, false);
}
```

## The parser state

`in_quotes` permits separators inside a field. `closed` marks the end of a quoted field.
After the final quote of a field, only a field separator, a record separator, or the end of the input is valid.
`seen` distinguishes an empty document from a record that contains an empty quoted field.
The parser copies field bytes without changes to their UTF-8 representation.

```fern
<<__parse>>=
function __parse(s: string, single: boolean): Result[string[][], ParseError] {
  let rows: string[][] = [];
  let record: string[] = [];
  let field: u8[] = [];
  let in_quotes: boolean = false;
  let closed: boolean = false;
  let seen: boolean = false;
  let n: i32 = s.len();
  let i: i32 = 0;
  while (i < n) {
    let c: i32 = s[i] as i32;
    if (in_quotes) {
      if (c == 34) {
        if (i + 1 < n && s[i + 1] == 34) {
          field = field.append(34 as u8);
          i = i + 2;
        } else {
          in_quotes = false;
          closed = true;
          i = i + 1;
        }
      } else {
        field = field.append(s[i] as u8);
        i = i + 1;
      }
    } else if (c == 44) {
      record = record.append(string_from_bytes_unchecked(field));
      field = [];
      closed = false;
      seen = true;
      i = i + 1;
    } else if (c == 10 || c == 13) {
      if (single) {
        return Err(ParseError { offset: i, kind: ParseErrorKind.UnexpectedRecordTerminator });
      }
      record = record.append(string_from_bytes_unchecked(field));
      field = [];
      rows = rows.append(record);
      record = [];
      closed = false;
      seen = false;
      if (c == 13 && i + 1 < n && s[i + 1] == 10) {
        i = i + 2;
      } else {
        i = i + 1;
      }
    } else if (closed) {
      return Err(ParseError { offset: i, kind: ParseErrorKind.UnexpectedByteAfterQuote(s[i] as u8) });
    } else if (c == 34) {
      if (field.len() != 0) {
        return Err(ParseError { offset: i, kind: ParseErrorKind.UnexpectedQuote });
      }
      in_quotes = true;
      seen = true;
      i = i + 1;
    } else {
      field = field.append(s[i] as u8);
      seen = true;
      i = i + 1;
    }
  }
  if (in_quotes) {
    return Err(ParseError { offset: n, kind: ParseErrorKind.UnclosedQuote });
  }
  if (single || seen || record.len() > 0) {
    record = record.append(string_from_bytes_unchecked(field));
    rows = rows.append(record);
  }
  return Ok(rows);
}
```

## Document output

`serialize` uses the same field rules as `join`. It inserts CRLF between records and does not add a final separator.
The parser accepts this output even when fields contain quotes or record separators.

```fern
<<serialize>>=
// serialize escapes the fields and separates records with CRLF.
pub function serialize(rows: string[][]): string {
  let lines: string[] = [];
  for row in rows {
    lines = lines.append(join(row));
  }
  return lines.join("\r\n");
}
```

## Executable examples

These examples import this module through its public path. Each example returns zero when its checks pass.

```fern test name=csv-round-trip
import "std/csv";
import "std/string";
function main(): i32 {
  let source: string[][] = [["name", "value"], ["a,b", "say \"hi\""]];
  let encoded: string = csv.serialize(source);
  let Ok(rows) = csv.parse(encoded) else { return 1; };
  if (rows.len() != 2 || rows[1][0] != "a,b" || rows[1][1] != "say \"hi\"") { return 2; }
  return 0;
}
```

```fern test name=csv-invalid-quote
import "std/csv";
function main(): i32 {
  match (csv.parse("a\"b")) {
    Err(problem) => {
      if (problem.offset != 1 || problem.kind != csv.UnexpectedQuote) { return 1; }
    },
    Ok(_) => { return 2; }
  }
  return 0;
}
```
