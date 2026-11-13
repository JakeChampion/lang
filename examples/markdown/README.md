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
`markdown.write(writer, text, part)` gives the HTML to a buffered writer each
time that `part` bytes or more are ready. `md` uses it, so the HTML of a
document is not in memory all at one time.

## How it works

The renderer has the two phases of the appendix of the spec:

1. `blocks.fern` reads the source one line at a time. It keeps a stack of
   the open blocks, and it writes the blocks to a log in the order of the
   document. A container block has a record where it starts and an END
   record where it ends. A leaf block gets its record when it closes. A
   paragraph, a heading and a code block keep no copy of their text. Each
   holds a range of line segments, and each segment is a range of the
   source.
2. `inlines.fern` parses and writes the text of each paragraph and heading.
   `markdown.fern` reads the log in order and writes the HTML.

md4c has the same design. A link reference definition can come after the
link that uses it, so the inline phase starts after the last line. Until
then, the log keeps all the blocks. A record is two i64 fields and a
segment is one i64 field, so the log is small next to the source.

A list is loose when a block in it ends with a blank line and is not its
last block. Each container on the stack keeps what the parser must know of
its last closed child. When the container gets a new child, the parser
knows that the last child is not the last block, and it does the test. A
paragraph that holds only link reference definitions is not a block, so
its test waits until it closes.

The cursor of the line is in local variables, not in the struct of the
parser. A line that cannot start a block does not go through the tests of
the block starts.

The inline parser reads each byte of the text once. A constant table of 256
entries makes `__scan_set` a vector scan, and the scan finds the next byte
that can start a token.

A token is a row of four i32 fields. The kinds of token are code spans, raw
HTML, autolinks, runs of `*` and `_`, and the brackets of links and images.
Line breaks, backslash escapes, entity references and the bytes that HTML
escapes are tokens too. The delimiter stack, the bracket stack and the links
are i32 tables, which stay from one paragraph to the next.

Emphasis follows the procedure "process emphasis" of the appendix of the
spec. Each run of delimiters keeps the tags that it opens and closes. The
writer copies the text between two tokens as it is.

The text of a paragraph is a part of the source when only a line feed is
between two of its lines. Other paragraphs, such as one in a block quote,
get one string of their lines.

`refs.fern` parses the link reference definitions, normalizes link labels,
and scans link destinations and titles. `tags.fern` has the HTML tag
scanners and the start and end conditions of the seven kinds of HTML block.
`entities.fern` has the named character references of HTML and the Unicode
punctuation ranges. `scripts/gen-markdown-tables.py` writes it.

## What it has

- Paragraphs, ATX headings and setext headings.
- Thematic breaks.
- Indented code blocks, and fenced code blocks with an info string.
- Block quotes, with lazy continuation lines.
- Bullet lists and ordered lists, tight and loose.
- The seven kinds of HTML block.
- Link reference definitions.
- Tabs, with a tab stop of four columns.
- Backslash escapes, and entity references: the 2125 names of HTML, decimal
  and hexadecimal. They apply in text, link destinations, link titles and
  info strings.
- Code spans.
- Emphasis and strong emphasis.
- Inline links, and full, collapsed and shortcut reference links. A link
  holds no other link.
- Images, with the text of their content as the `alt` attribute.
- URI and email autolinks.
- Raw HTML: open and closing tags, comments, processing instructions,
  declarations and CDATA sections.
- Hard line breaks and soft line breaks.
- The percent-encoding of destinations and the HTML escapes of cmark.

The renderer skips a byte order mark at the start of the source. It replaces
each NUL byte with U+FFFD.

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
- A link label has at most 1000 bytes. The spec gives at most 999
  characters.
- A link title is the longest text that the title can be. A backslash before
  a quote can be text, so `[a](/u "x\")` has the title `x\`.
- The entity references in an autolink apply to its destination and its
  text.

## Differences from cmark 0.30.2

cmark 0.30.2 is the version in the package archives. Where it does not
follow spec 0.31.2, the port follows the spec:

- The bottom for openers in "process emphasis" is kept for each length
  modulo 3, and for whether the closer can open, for `_` as for `*`. cmark
  keeps one bottom for all `_` closers, so `__)_>__` is text there and
  strong emphasis here.
- A Unicode symbol, such as `£`, is punctuation next to a run of
  delimiters.
- `<!-->` and `<!--->` are comments, a comment can hold `--`, and `<!` with
  a lower-case letter starts a declaration.
- `search` is the name of an HTML block of kind 6, and `source` is not.
- When the title of a definition is not the end of its line, the
  definition has no title. cmark keeps the title, but the line is text.

## Tests

- The unit cases in `markdown_test.fern` run on x86-64, arm64 and
  wasm32-wasi with the leak census (`TestSelfHostExampleMarkdownUnit`).
- `TestSelfHostExampleMarkdownSpec` runs the 652 examples of the CommonMark
  0.31.2 spec in `tests/spec.json` (see `licenses/commonmark/NOTICE.txt`).
  It compares the HTML exactly, and fails when the set of examples that pass
  changes. All 652 examples pass.
- `TestSelfHostExampleMarkdownAgainstCmark` compares the output of `md` and
  of the `cmark` binary on the same input. It needs `cmark` on the PATH, and
  its cases have none of the differences from cmark 0.30.2 above.

## Speed

Measured 2026-11-13 on the 4-core x86-64 container, with other work on the
machine: the median of 15 runs, and the peak memory. Each program reads the
input from a file and writes to `/dev/null`. md4c 0.4.8 runs through a small
driver of `md_html` with `MD_DIALECT_COMMONMARK`, which writes each part of
the HTML when md4c gives it.

| input | `md` | cmark 0.30.2 | md4c 0.4.8 |
|---|---:|---:|---:|
| the Markdown of `docs/`, 23.9 MB | 0.119 s, 33 MB | 0.451 s, 220 MB | 0.136 s, 28 MB |
| the 652 spec examples, repeated to 16.9 MB | 0.372 s, 66 MB | 1.112 s, 444 MB | 0.414 s, 44 MB |

callgrind counts 210 M instructions for `md` on the first 6 MB of the
`docs/` input, and 305 M for md4c. The inline phase is 58% of the count of
`md` there, and the block phase is 35%. On the first 4 MB of the spec input,
the block phase is about half of the count. The link reference definitions
are a sixth of the block phase there.
