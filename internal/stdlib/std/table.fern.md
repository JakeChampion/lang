# Text table layout

This module formats text as tables.

## Model

The renderer aligns rows of text into columns. A separate entry point includes a header row.

## Contracts

Input consists of rows and cells rather than preformatted lines. The renderer returns text so the caller controls the output destination.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 function __tbl_pad(s: string, width: i32): string>>

<<4 pub function render(rows: string[][]): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module formats text as tables.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/utf8";
import "std/string";
import "std/array";
```

### `3 function __tbl_pad(s: string, width: i32): string`

```fern
<<3 function __tbl_pad(s: string, width: i32): string>>=
function __tbl_pad(s: string, width: i32): string {
  let out: u8[] = [];
  let i: i32 = 0;
  while (i < s.len()) {
    out = out.append(s[i] as u8);
    i = i + 1;
  }
  let pad: i32 = width - s.codepoint_count();
  let p: i32 = 0;
  while (p < pad) {
    out = out.append(32 as u8);
    p = p + 1;
  }
  return string_from_bytes_unchecked(out);
}

function __tbl_widths(rows: string[][]): i32[] {
  let ncols: i32 = 0;
  let r: i32 = 0;
  while (r < rows.len()) {
    if (rows[r].len() > ncols) {
      ncols = rows[r].len();
    }
    r = r + 1;
  }
  let widths: i32[] = [];
  let c: i32 = 0;
  while (c < ncols) {
    widths = widths.append(0);
    c = c + 1;
  }
  r = 0;
  while (r < rows.len()) {
    c = 0;
    while (c < rows[r].len()) {
      let w: i32 = rows[r][c].codepoint_count();
      if (w > widths[c]) {
        widths = widths.with(c, w);
      }
      c = c + 1;
    }
    r = r + 1;
  }
  return widths;
}

function __tbl_row(row: string[], widths: i32[]): string {
  let cells: string[] = [];
  let c: i32 = 0;
  while (c < widths.len()) {
    let cell: string = "";
    if (c < row.len()) {
      cell = row[c];
    }
    if (c == widths.len() - 1) {
      cells = cells.append(cell);
    } else {
      cells = cells.append(__tbl_pad(cell, widths[c]));
    }
    c = c + 1;
  }
  return cells.join("  ");
}
```

### `4 pub function render(rows: string[][]): string`

```fern
<<4 pub function render(rows: string[][]): string>>=
pub function render(rows: string[][]): string {
  let widths: i32[] = __tbl_widths(rows);
  let lines: string[] = [];
  let r: i32 = 0;
  while (r < rows.len()) {
    lines = lines.append(__tbl_row(rows[r], widths));
    r = r + 1;
  }
  return lines.join("\n");
}

pub function render_with_header(headers: string[], rows: string[][]): string {
  let all: string[][] = [headers];
  let i: i32 = 0;
  while (i < rows.len()) {
    all = all.append(rows[i]);
    i = i + 1;
  }
  let widths: i32[] = __tbl_widths(all);
  let lines: string[] = [];
  lines = lines.append(__tbl_row(headers, widths));
  let rule: string[] = [];
  let c: i32 = 0;
  while (c < widths.len()) {
    let dashes: u8[] = [];
    let d: i32 = 0;
    while (d < widths[c]) {
      dashes = dashes.append(45 as u8);
      d = d + 1;
    }
    rule = rule.append(string_from_bytes_unchecked(dashes));
    c = c + 1;
  }
  lines = lines.append(__tbl_row(rule, widths));
  let r: i32 = 0;
  while (r < rows.len()) {
    lines = lines.append(__tbl_row(rows[r], widths));
    r = r + 1;
  }
  return lines.join("\n");
}
```
