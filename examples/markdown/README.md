# markdown

A CommonMark 0.31.2 to HTML renderer in Fern (#15588). Its output is the
output of [cmark](https://github.com/commonmark/cmark) with `--unsafe`. The
port does not copy source code from cmark.

```
fern -target x86-64-linux -o md examples/markdown/md.fern
printf '# Title\n\n- a\n- b\n' | ./md
```

`md` reads the files that it gets, or stdin when it gets none, and writes the
HTML to stdout. A program can call `markdown.to_html(text)`, or
`markdown.render(builder, text)` to write into a byte builder.

## How it works

The renderer has the two phases of the appendix of the spec:

1. `blocks.fern` reads the source one line at a time and builds the tree of
   blocks. Each node is a row of i32 fields in one array. A paragraph, a
   heading and a code block keep no copy of their text. Each holds a range of
   line segments, and each segment is a range of the source.
2. `inlines.fern` writes the text of each paragraph and heading.
   `markdown.fern` walks the tree and writes the HTML into one byte builder.

`refs.fern` parses the link reference definitions and normalizes link
labels. `tags.fern` has the HTML tag scanners and the start and end
conditions of the seven kinds of HTML block.

## What it has

- Paragraphs, ATX headings and setext headings.
- Thematic breaks.
- Indented code blocks, and fenced code blocks with an info string.
- Block quotes, with lazy continuation lines.
- Bullet lists and ordered lists, tight and loose.
- The seven kinds of HTML block.
- Link reference definitions. The parser keeps each definition in
  `Doc.refs`, but the inline phase does not use them yet.
- Tabs, with a tab stop of four columns.
- Backslash escapes, hard line breaks and soft line breaks.

The renderer skips a byte order mark at the start of the source. It replaces
each NUL byte with U+FFFD.

## What it does not have yet

The inline phase writes code spans, emphasis, links, images, autolinks, raw
HTML and entity references as text. The next slice of #15588 adds them.

`md` replaces each byte sequence that is not UTF-8 with U+FFFD. It writes one
U+FFFD for each maximal part that is not valid, as the Unicode standard
recommends. cmark writes such bytes as they are, and `cmark --validate-utf8`
replaces some longer sequences with one U+FFFD.

## Differences from the text of the spec

The port gives the output of cmark where cmark and the text of the spec do
not agree:

- A line with only a complete open tag of `pre`, `script`, `style` or
  `textarea`, such as `<pre/>`, starts an HTML block of kind 7. cmark and
  commonmark.js do this too.
- A line that can be a lazy continuation line of a paragraph does not start
  an HTML block of kind 7.

## Tests

- `markdown_test.fern` holds the unit cases. They run on x86-64, arm64 and
  wasm32-wasi with the leak census (`TestSelfHostExampleMarkdownUnit`).
- `TestSelfHostExampleMarkdownSpec` runs the 652 examples of the CommonMark
  0.31.2 spec in `tests/spec.json` (see `licenses/commonmark/NOTICE.txt`).
  It compares the HTML exactly, and fails when the set of examples that pass
  changes. 361 examples pass, and the others need inlines of the next slice.
- `TestSelfHostExampleMarkdownAgainstCmark` compares the output of `md` and
  of the `cmark` binary on the same input. It needs `cmark` on the PATH.
