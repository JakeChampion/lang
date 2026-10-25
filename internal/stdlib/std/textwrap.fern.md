# Text line widths

This module divides text into lines of a specified width.

## Model

The wrapper divides text into lines at a requested width. It returns the complete wrapped text as a string.

## Contracts

The width belongs to text layout policy. The caller controls the destination and any terminal-specific behavior.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 function __tw_words(line: string): string[]>>

<<4 pub function word_wrap(text: string, width: i32): string>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module divides text into lines of a specified width.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "std/utf8";
import "std/string";
import "std/array";
```

### `3 function __tw_words(line: string): string[]`

```fern
<<3 function __tw_words(line: string): string[]>>=
function __tw_words(line: string): string[] {
  let raw: string[] = line.split(" ");
  let out: string[] = [];
  let i: i32 = 0;
  while (i < raw.len()) {
    if (raw[i].len() > 0) {
      out = out.append(raw[i]);
    }
    i = i + 1;
  }
  return out;
}

function __tw_wrap_line(words: string[], width: i32): string {
  let lines: string[] = [];
  let cur: string = "";
  let curw: i32 = 0;
  let i: i32 = 0;
  while (i < words.len()) {
    let w: string = words[i];
    let ww: i32 = w.codepoint_count();
    if (curw == 0) {
      cur = w;
      curw = ww;
    } else if (curw + 1 + ww <= width) {
      cur = cur + " " + w;
      curw = curw + 1 + ww;
    } else {
      lines = lines.append(cur);
      cur = w;
      curw = ww;
    }
    i = i + 1;
  }
  if (curw > 0) {
    lines = lines.append(cur);
  }
  return lines.join("\n");
}
```

### `4 pub function word_wrap(text: string, width: i32): string`

```fern
<<4 pub function word_wrap(text: string, width: i32): string>>=
pub function word_wrap(text: string, width: i32): string {
  if (width <= 0) {
    return text;
  }
  let paras: string[] = text.split("\n");
  let out: string[] = [];
  let p: i32 = 0;
  while (p < paras.len()) {
    out = out.append(__tw_wrap_line(__tw_words(paras[p]), width));
    p = p + 1;
  }
  return out.join("\n");
}
```
