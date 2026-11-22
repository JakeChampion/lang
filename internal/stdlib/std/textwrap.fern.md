# Text line widths

This module divides text into lines of a specified width.

## Model

The wrapper divides text into lines at a requested width. It returns the complete wrapped text as a string.

A line feed ends a paragraph, and the wrapper divides each paragraph independently. Spaces, tabs and carriage returns separate the words of a paragraph. One space joins the words of an output line. The width counts code points. A word longer than the width stays whole on its own line. A width of zero or less returns the text unchanged.

## Contracts

The width belongs to text layout policy. The caller controls the destination and any terminal-specific behavior.

## Executable example

```fern test name=std-textwrap-example
import "std/textwrap";

function main(): i32 {
  if (textwrap.word_wrap("the quick brown fox", 10) != "the quick\nbrown fox") { return 1; }
  return 0;
}
```

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
import "std/string";
import "std/array";
```

### `3 function __tw_words(line: string): string[]`

```fern
<<3 function __tw_words(line: string): string[]>>=
function __tw_words(line: string): string[] {
  let out: string[] = [];
  let n = line.len();
  let i = 0;
  while (i < n) {
    while (i < n && __tw_is_space(line[i])) {
      i += 1;
    }
    let start = i;
    while (i < n && !__tw_is_space(line[i])) {
      i += 1;
    }
    if (i > start) {
      out = out.append(slice_unchecked(line, start, i) + "");
    }
  }
  return out;
}

function __tw_is_space(b: u8): boolean {
  return b == 32 || b == 9 || b == 13;
}

function __tw_wrap_line(words: string[], width: i32): string {
  let lines: string[] = [];
  let cur = "";
  let curw = 0;
  let i = 0;
  while (i < words.len()) {
    let w = words[i];
    let ww = w.codepoint_count();
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
    i += 1;
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
  let paras = text.split("\n");
  let out: string[] = [];
  let p = 0;
  while (p < paras.len()) {
    out = out.append(__tw_wrap_line(__tw_words(paras[p]), width));
    p += 1;
  }
  return out.join("\n");
}
```
