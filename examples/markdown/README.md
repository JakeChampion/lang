# markdown

A CommonMark 0.31.2 renderer in Fern (#15588), with the command line of
[cmark](https://github.com/commonmark/cmark) 0.30.2. For the same input and
options, its output is the output of cmark in each format of cmark: HTML,
XML, CommonMark, groff man and LaTeX. The port does not copy source code from
cmark.

```
fern -target x86-64-linux -o md examples/markdown/md.fern
printf '# Title\n\n- a\n- b\n' | ./md
printf '# Title\n\n"Hi" -- there...\n' | ./md --to xml --sourcepos --smart
```

`md` reads the files that it gets one after the other, or stdin when it gets
none. It has the options of cmark:

- `--to`, `-t FORMAT`: `html` (the default), `xml`, `man`, `commonmark` or
  `latex`.
- `--width N`: the width where the CommonMark, man and LaTeX renderers wrap a
  line, or 0 for no wrap. `md` reads `N` as C `strtol` does.
- `--sourcepos`: the source position of each block in HTML, and of each node
  in XML.
- `--hardbreaks` and `--nobreaks`: each soft line break is a hard line break,
  or a space.
- `--smart`: curly quotes, en dashes, em dashes and ellipses.
- `--unsafe`: raw HTML, and links to `javascript:`, `vbscript:`, `file:` and
  `data:` URLs.
- `--validate-utf8`: invalid UTF-8 as cmark replaces it (see below).
- `--help`, `-h` and `--version`.

`--safe` is the default. Then, as in cmark 0.30, raw HTML is the comment
`<!-- raw HTML omitted -->`, and a link to such a URL has an empty
destination. A `data:` URL of a PNG, GIF, JPEG or WebP image is safe.

An unknown option writes the usage and fails. The errors of `--width` and
`--to`, a file that `md` cannot open, and the exit status are those of cmark.
A directory gives no text, as it does in cmark.

A program can call `markdown.to_html(text, options)`, or
`markdown.render(builder, text, options)` to write into a byte builder.
`markdown.write(writer, text, part, options)` gives the HTML to a buffered
writer each time that `part` bytes or more are ready. `md` uses it, so the
HTML of a document is not in memory all at one time. `markdown.options()`
gives the defaults. `render.render(text, format, options)` gives the other
formats, and `render.tree(text, options)` gives the node tree.

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

With `--sourcepos`, the block parser keeps the position of each block. As in
cmark, a block ends at the end of the line before the line that closes it.
A fenced code block and a setext heading end at the end of that line. The
length of the line of an ATX heading does not count its closing `#` marks.

The other formats need the node tree of cmark. `render.fern` builds it from
the log of blocks. `inlines.build` adds the nodes of the inline text from
the tokens that the HTML writer reads. `nodes.fern` keeps the tree as rows
of i32 fields, and the text of all the nodes in one string.

The tree has the text nodes of cmark. Adjacent text joins. The positions
follow the rules of cmark, such as the column of a string of backticks that
opens no code span.

`xml.fern` writes the XML in parts, as the HTML writer does. `out.fern` is
the writer of the text renderers of cmark. It keeps the prefix of each line
and writes the line breaks that a renderer asks for. It escapes the text for
CommonMark, man or LaTeX, and it wraps the lines. `commonmark.fern`,
`man.fern` and `latex.fern` walk the tree.

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

With `--validate-utf8`, `md` replaces invalid UTF-8 as cmark does. cmark
reads each line without its line ending. It writes one U+FFFD for each of
these sequences:

- a lead byte and the valid continuation bytes after it,
- all the bytes that are left on the line when the sequence is too short
  for them,
- the whole sequence of an overlong form, a surrogate or a code point above
  U+10FFFF.

Without `--validate-utf8`, `md` replaces each maximal part that is not valid
with one U+FFFD, as the Unicode standard recommends. cmark writes the bytes
as they are, but a Fern `string` is always UTF-8.

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
  delimiters, and next to a quote with `--smart`.
- `<!-->` and `<!--->` are comments, a comment can hold `--`, and `<!` with
  a lower-case letter starts a declaration.
- `search` is the name of an HTML block of kind 6, and `source` is not.
- When the title of a definition is not the end of its line, the
  definition has no title. cmark keeps the title, but the line is text.

These differences change the output of examples 354, 625 and 626 of the
spec in each format. The command line has three more differences:

- The usage line of `--help` names `md`, not `cmark`.
- `--version` names `md` and the version of cmark that it follows. It does
  not give the copyright line of cmark.
- Without `--validate-utf8`, invalid UTF-8 becomes U+FFFD (see above).

## Tests

- The unit cases in `markdown_test.fern` run on x86-64, arm64 and
  wasm32-wasi with the leak census (`TestSelfHostExampleMarkdownUnit`).
- `TestSelfHostExampleMarkdownSpec` runs the 652 examples of the CommonMark
  0.31.2 spec in `tests/spec.json` (see `licenses/commonmark/NOTICE.txt`)
  with `--unsafe`, as the spec runner of cmark does. It compares the HTML
  exactly, and fails when the set of examples that pass changes. All 652
  examples pass.
- `TestSelfHostExampleMarkdownAgainstCmark` compares the output of `md` and
  of the `cmark` binary on the same input. It needs `cmark` on the PATH, and
  its cases have none of the differences from cmark 0.30.2 above.

Two tests compare the command line with cmark 0.30.2 in each format:

- `TestSelfHostExampleMarkdownCmarkCases` runs `md` on the recorded cases
  of `tests/cmark.json`.
- `TestSelfHostExampleMarkdownFormats` compares `md` and `cmark` on each
  example of the spec.

The cases of `tests/cmark.json` have each option in each format, the errors
of the command line, and invalid UTF-8. Some join files, and some name a
missing file or a directory. `scripts/gen-markdown-cmark-cases.py` records
the stdout, the stderr and the exit status of cmark 0.30.2 for each case.
The test runs `md` on x86-64, arm64 and wasm32-wasi, and compares the three.
When `cmark` is on the PATH, it also makes sure that the record is still the
output of cmark.

`TestSelfHostExampleMarkdownFormats` gives each example of the spec to `md`
on each target and to `cmark`. It uses each format, with 11 sets of options.
Only the output of the examples 354, 625 and 626 is different.

## Speed

Measured 2026-11-13 on the 4-core x86-64 container, with other work on the
machine: the median of 15 runs, and the peak memory. Each program reads the
input from a file and writes to `/dev/null`. Both `md` and cmark use their
default options. md4c 0.4.8 runs through a small driver of `md_html` with
`MD_DIALECT_COMMONMARK`, which writes each part of the HTML when md4c gives
it.

The first input is the Markdown files of the repository (`git ls-files
'*.md'`) one after the other, 23.5 MB. The second input is the 652 spec
examples, each with a blank line after it, repeated to 16.9 MB.

| input | format | `md` | cmark 0.30.2 | md4c 0.4.8 |
|---|---|---:|---:|---:|
| the repository, 23.5 MB | HTML | 0.112 s, 32 MB | 0.415 s, 159 MB | 0.125 s, 29 MB |
| the repository, 23.5 MB | `--to xml` | 0.385 s, 328 MB | 0.464 s, 182 MB | |
| the repository, 23.5 MB | `--to commonmark` | 0.560 s, 351 MB | 0.689 s, 157 MB | |
| the spec examples, 16.9 MB | HTML | 0.356 s, 66 MB | 1.058 s, 444 MB | 0.390 s, 44 MB |
| the spec examples, 16.9 MB | `--to xml` | 1.035 s, 883 MB | 1.243 s, 511 MB | |
| the spec examples, 16.9 MB | `--to commonmark` | 1.179 s, 884 MB | 1.247 s, 434 MB | |

The HTML does not use the node tree. callgrind counts the instructions of
`md` before and after it got the options of cmark. On the first 6 MB of the
repository input, it counts 1.9% more (207 M against 203 M). On the first
4 MB of the spec input, it counts 2.1% more (834 M against 817 M). On both
inputs, the time of the
HTML is in the noise of the machine. The other formats keep the node tree
and its text, so they use more memory than cmark.
