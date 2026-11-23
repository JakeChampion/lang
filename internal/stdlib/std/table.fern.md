# Text table layout

This module formats text as tables.

## Model

The renderer aligns rows of text into columns. A separate entry point includes a header row.

## Contracts

Input consists of rows and cells rather than preformatted lines. The renderer returns text so the caller controls the output destination.

Two spaces separate the columns. The width of a column is the largest number of code points in its cells. A row can have fewer cells than the table has columns. No line ends with spaces.

## Executable example

```fern test name=std-table-example
import "std/table";

function main(): i32 {
  let text = table.render_with_header(["name", "age"], [["ada", "36"], ["bo"]]);
  if (text != "name  age\n----  ---\nada   36\nbo") { return 1; }
  return 0;
}
```

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
import "std/string";
import "std/array";
```

### `3 function __tbl_pad(s: string, width: i32): string`

```fern
<<3 function __tbl_pad(s: string, width: i32): string>>=
function __tbl_pad(s: string, width: i32): string {
  let out: u8[] = [];
  let i = 0;
  while (i < s.len()) {
    out = out.append(s[i] as u8);
    i += 1;
  }
  let pad = width - s.codepoint_count();
  let p = 0;
  while (p < pad) {
    out = out.append(32 as u8);
    p += 1;
  }
  return unsafe { string_from_bytes_unchecked(out) };
}

function __tbl_widths(rows: string[][]): i32[] {
  let ncols = 0;
  let r = 0;
  while (r < rows.len()) {
    if (rows[r].len() > ncols) {
      ncols = rows[r].len();
    }
    r += 1;
  }
  let widths: i32[] = [];
  let c = 0;
  while (c < ncols) {
    widths = widths.append(0);
    c += 1;
  }
  r = 0;
  while (r < rows.len()) {
    c = 0;
    while (c < rows[r].len()) {
      let w = rows[r][c].codepoint_count();
      if (w > widths[c]) {
        widths = widths.with(c, w);
      }
      c += 1;
    }
    r += 1;
  }
  return widths;
}

// The cells after the last cell with text are not written, so a line does
// not end with spaces.
function __tbl_row(row: string[], widths: i32[]): string {
  let last = row.len() - 1;
  if (last >= widths.len()) {
    last = widths.len() - 1;
  }
  while (last >= 0 && row[last].len() == 0) {
    last -= 1;
  }
  let cells: string[] = [];
  let c = 0;
  while (c <= last) {
    if (c == last) {
      cells = cells.append(row[c]);
    } else {
      cells = cells.append(__tbl_pad(row[c], widths[c]));
    }
    c += 1;
  }
  return cells.join("  ");
}
```

### `4 pub function render(rows: string[][]): string`

```fern
<<4 pub function render(rows: string[][]): string>>=
pub function render(rows: string[][]): string {
  let widths = __tbl_widths(rows);
  let lines: string[] = [];
  let r = 0;
  while (r < rows.len()) {
    lines = lines.append(__tbl_row(rows[r], widths));
    r += 1;
  }
  return lines.join("\n");
}

pub function render_with_header(headers: string[], rows: string[][]): string {
  let all: string[][] = [headers];
  let i = 0;
  while (i < rows.len()) {
    all = all.append(rows[i]);
    i += 1;
  }
  let widths = __tbl_widths(all);
  let lines: string[] = [];
  lines = lines.append(__tbl_row(headers, widths));
  let rule: string[] = [];
  let c = 0;
  while (c < widths.len()) {
    let dashes: u8[] = [];
    let d = 0;
    while (d < widths[c]) {
      dashes = dashes.append(45 as u8);
      d += 1;
    }
    rule = rule.append(unsafe { string_from_bytes_unchecked(dashes) });
    c += 1;
  }
  lines = lines.append(__tbl_row(rule, widths));
  let r = 0;
  while (r < rows.len()) {
    lines = lines.append(__tbl_row(rows[r], widths));
    r += 1;
  }
  return lines.join("\n");
}
```
